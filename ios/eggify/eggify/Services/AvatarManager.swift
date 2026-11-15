//
//  AvatarManager.swift
//  eggify
//
//  Manages avatar caching and lookup with persistent disk storage
//

import Foundation
import SwiftUI
import Combine
import UIKit

@MainActor
class AvatarManager: NSObject, ObservableObject {
    static let shared = AvatarManager()

    @Published var avatarMap: [Int64: Avatar] = [:]
    @Published var avatarImages: [Int64: UIImage] = [:]
    @Published var isLoading = false
    @Published var errorMessage: String?

    private let apiClient = EggifyAPIClient.shared
    private let cacheDirectory: URL

    private override init() {
        // Set up cache directory in Caches folder
        let paths = FileManager.default.urls(for: .cachesDirectory, in: .userDomainMask)
        self.cacheDirectory = paths[0].appendingPathComponent("AvatarImages")

        // Create cache directory if it doesn't exist
        try? FileManager.default.createDirectory(at: cacheDirectory, withIntermediateDirectories: true)

        super.init()
    }

    /// Load avatars from all themes and cache them
    func loadAvatars() async {
        isLoading = true
        errorMessage = nil

        defer { isLoading = false }

        do {
            // Get all available themes
            let themes = try await apiClient.getAvatarThemes()

            // Create a map for quick lookup by ID
            var map: [Int64: Avatar] = [:]

            // Load avatars for each theme
            for theme in themes {
                let avatars = try await apiClient.getThemeAvatars(themeId: theme.id)
                for avatar in avatars {
                    map[avatar.id] = avatar
                }
            }

            self.avatarMap = map

            // Load images for all avatars in background (with disk caching)
            await loadAvatarImages(for: Array(map.values))
        } catch {
            errorMessage = error.localizedDescription
        }
    }

    /// Load avatar images for given avatars (disk cache first, then the
    /// server, in parallel) and publish them in one update.
    private func loadAvatarImages(for avatars: [Avatar]) async {
        var images: [Int64: UIImage] = [:]
        await withTaskGroup(of: (Int64, UIImage?).self) { group in
            for avatar in avatars where avatarImages[avatar.id] == nil {
                group.addTask { @MainActor in (avatar.id, await self.image(for: avatar)) }
            }
            for await (id, image) in group {
                if let image { images[id] = image }
            }
        }
        if !images.isEmpty { avatarImages.merge(images) { _, new in new } }
    }

    /// Avatars are drawn at up to ~170pt (Zen); 512 px covers that on 3x.
    private static let maxPixel: CGFloat = 512
    /// One load per avatar at a time; rows sharing an avatar wait for it.
    private var inFlight: [Int64: Task<UIImage?, Never>] = [:]

    private func image(for avatar: Avatar) async -> UIImage? {
        if let cached = avatarImages[avatar.id] { return cached }
        if let running = inFlight[avatar.id] { return await running.value }

        let id = avatar.id
        let fileURL = cacheDirectory.appendingPathComponent("\(id).png")
        let api = apiClient
        let task = Task<UIImage?, Never> {
            // Disk reads and decoding run off the main thread.
            if let cached = await Task.detached(priority: .utility, operation: {
                Self.decode(contentsOf: fileURL)
            }).value {
                return cached
            }
            do {
                let data = try await api.getAvatarImage(avatarId: id)
                return await Task.detached(priority: .utility, operation: {
                    guard let image = ImageCache.downsample(data: data, maxPixel: Self.maxPixel) else { return nil }
                    try? data.write(to: fileURL)
                    return image
                }).value
            } catch {
                #if DEBUG
                print("❌ Failed to load avatar image \(id): \(error.localizedDescription)")
                #endif
                return nil
            }
        }
        inFlight[id] = task
        let image = await task.value
        inFlight[id] = nil
        return image
    }

    nonisolated private static func decode(contentsOf url: URL) -> UIImage? {
        guard let data = try? Data(contentsOf: url) else { return nil }
        return ImageCache.downsample(data: data, maxPixel: maxPixel)
    }

    /// Get avatar by ID from cache
    func getAvatar(id: Int64) -> Avatar? {
        return avatarMap[id]
    }

    /// Get avatar image by ID from memory cache (already loaded)
    func getAvatarImage(id: Int64) -> UIImage? {
        return avatarImages[id]
    }

    /// Load a single avatar image on-demand (from disk cache or server).
    /// Call this when you have an avatar from the session but it's not in the memory cache yet.
    func loadAvatarImageIfNeeded(avatar: Avatar) async {
        guard avatarImages[avatar.id] == nil, let image = await image(for: avatar) else { return }
        avatarImages[avatar.id] = image
    }
}
