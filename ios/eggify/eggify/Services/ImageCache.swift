//
//  ImageCache.swift
//  eggify
//
//  Decodes base64 images off the main thread, downsampled for display,
//  and keeps them in memory so scrolling a chat never decodes twice.
//

import SwiftUI
import UIKit
import ImageIO

nonisolated final class ImageCache: @unchecked Sendable {
    static let shared = ImageCache()

    private let cache: NSCache<NSString, UIImage> = {
        let cache = NSCache<NSString, UIImage>()
        cache.totalCostLimit = 80 * 1024 * 1024
        return cache
    }()

    func cached(_ key: String) -> UIImage? {
        cache.object(forKey: key as NSString)
    }

    /// Decode (or fetch from cache) a base64 image, downsampled to `maxPixel`.
    func image(key: String, base64: String, maxPixel: CGFloat) async -> UIImage? {
        let cacheKey = "\(key)@\(Int(maxPixel))"
        if let hit = cached(cacheKey) { return hit }
        let image = await Task.detached(priority: .userInitiated) {
            ImageCache.decode(base64: base64, maxPixel: maxPixel)
        }.value
        if let image {
            let cost = Int(image.size.width * image.size.height * image.scale * image.scale * 4)
            cache.setObject(image, forKey: cacheKey as NSString, cost: cost)
        }
        return image
    }

    static func decode(base64: String, maxPixel: CGFloat) -> UIImage? {
        var string = base64
        if let comma = string.firstIndex(of: ","), string.hasPrefix("data:") {
            string = String(string[string.index(after: comma)...])
        }
        guard let data = Data(base64Encoded: string, options: .ignoreUnknownCharacters) else { return nil }
        return downsample(data: data, maxPixel: maxPixel)
    }

    static func downsample(data: Data, maxPixel: CGFloat) -> UIImage? {
        let sourceOptions = [kCGImageSourceShouldCache: false] as CFDictionary
        guard let source = CGImageSourceCreateWithData(data as CFData, sourceOptions) else {
            return UIImage(data: data)
        }
        let options = [
            kCGImageSourceCreateThumbnailFromImageAlways: true,
            kCGImageSourceShouldCacheImmediately: true,
            kCGImageSourceCreateThumbnailWithTransform: true,
            kCGImageSourceThumbnailMaxPixelSize: maxPixel
        ] as CFDictionary
        guard let cgImage = CGImageSourceCreateThumbnailAtIndex(source, 0, options) else {
            return UIImage(data: data)
        }
        return UIImage(cgImage: cgImage)
    }
}

/// Shows a base64 image block, decoded off the main thread.
struct Base64ImageView: View {
    let block: ImageBlockData
    var maxWidth: CGFloat = 260
    var maxHeight: CGFloat = 360

    @State private var image: UIImage?
    @State private var failed = false
    @Environment(\.displayScale) private var displayScale

    var body: some View {
        Group {
            if let image {
                Image(uiImage: image)
                    .resizable()
                    .scaledToFit()
                    .frame(maxWidth: maxWidth, maxHeight: maxHeight)
                    .clipShape(RoundedRectangle(cornerRadius: 12, style: .continuous))
                    .accessibilityLabel("Image")
            } else if failed {
                Label("Image unavailable", systemImage: "photo.badge.exclamationmark")
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .frame(width: 160, height: 80)
                    .background(Color(.secondarySystemBackground), in: RoundedRectangle(cornerRadius: 12))
            } else {
                RoundedRectangle(cornerRadius: 12, style: .continuous)
                    .fill(Color(.secondarySystemBackground))
                    .frame(width: 160, height: 120)
                    .overlay(ProgressView())
            }
        }
        .task(id: block.id) {
            let px = max(maxWidth, maxHeight) * displayScale
            if let img = await ImageCache.shared.image(key: block.id, base64: block.base64Data, maxPixel: px) {
                image = img
            } else {
                failed = true
            }
        }
    }
}
