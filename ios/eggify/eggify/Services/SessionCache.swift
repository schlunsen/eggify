//
//  SessionCache.swift
//  eggify
//
//  The last session list and the latest messages of recently opened chats,
//  per server, on disk. The app shows them at once on launch and when a chat
//  opens, then replaces them with what the server returns. Raw server JSON is
//  stored, so the normal parsers read it. Signing out deletes a server's cache.
//

import Foundation

nonisolated final class SessionCache: @unchecked Sendable {
    static let shared = SessionCache()

    /// Chats whose messages are kept (most recently written win).
    static let maxChats = 40

    private let root: URL
    private let queue = DispatchQueue(label: "eggify.session-cache", qos: .utility)

    init(root: URL? = nil) {
        self.root = root ?? FileManager.default.urls(for: .cachesDirectory, in: .userDomainMask)[0]
            .appendingPathComponent("SessionCache", isDirectory: true)
    }

    // MARK: Write (in the background)

    func storeSessions(_ rows: [[String: Any]], host: String?) {
        guard let dir = directory(for: host), let data = try? JSONSerialization.data(withJSONObject: rows) else { return }
        queue.async { Self.write(data, to: dir.appendingPathComponent("sessions.json")) }
    }

    func storeMessages(_ rows: [[String: Any]], session: String, host: String?) {
        guard let dir = directory(for: host), let file = Self.messagesFile(session, in: dir),
              let data = try? JSONSerialization.data(withJSONObject: rows) else { return }
        queue.async {
            Self.write(data, to: file)
            Self.prune(dir)
        }
    }

    // MARK: Read

    func sessions(host: String?) async -> [[String: Any]] {
        guard let dir = directory(for: host) else { return [] }
        return await read(dir.appendingPathComponent("sessions.json"))
    }

    func messages(session: String, host: String?) async -> [[String: Any]] {
        guard let dir = directory(for: host), let file = Self.messagesFile(session, in: dir) else { return [] }
        return await read(file)
    }

    /// Forget everything cached for a server (sign-out).
    func clear(host: String?) {
        guard let dir = directory(for: host) else { return }
        queue.async { try? FileManager.default.removeItem(at: dir) }
    }

    /// Waits until queued writes are on disk (tests).
    func flush() async {
        await withCheckedContinuation { continuation in queue.async { continuation.resume() } }
    }

    // MARK: Files

    private func directory(for host: String?) -> URL? {
        guard let host, !host.isEmpty else { return nil }
        return root.appendingPathComponent(Self.safeName(host), isDirectory: true)
    }

    private func read(_ url: URL) async -> [[String: Any]] {
        await withCheckedContinuation { continuation in
            queue.async {
                let rows = (try? Data(contentsOf: url))
                    .flatMap { try? JSONSerialization.jsonObject(with: $0) as? [[String: Any]] } ?? []
                continuation.resume(returning: rows)
            }
        }
    }

    private static func messagesFile(_ session: String, in dir: URL) -> URL? {
        let name = safeName(session)
        return name.isEmpty ? nil : dir.appendingPathComponent("messages-\(name).json")
    }

    /// Ids are UUIDs; keep only safe characters so nothing can leave the folder.
    private static func safeName(_ value: String) -> String {
        String(value.unicodeScalars.filter { CharacterSet.alphanumerics.contains($0) || $0 == "-" || $0 == "_" }
            .map(Character.init))
    }

    private static func write(_ data: Data, to url: URL) {
        try? FileManager.default.createDirectory(at: url.deletingLastPathComponent(), withIntermediateDirectories: true)
        try? data.write(to: url, options: [.atomic, .completeFileProtectionUntilFirstUserAuthentication])
    }

    private static func prune(_ dir: URL) {
        let fm = FileManager.default
        guard let files = try? fm.contentsOfDirectory(at: dir, includingPropertiesForKeys: [.contentModificationDateKey]) else { return }
        let chats = files.filter { $0.lastPathComponent.hasPrefix("messages-") }
        guard chats.count > maxChats else { return }
        let sorted = chats.sorted {
            let a = (try? $0.resourceValues(forKeys: [.contentModificationDateKey]).contentModificationDate) ?? .distantPast
            let b = (try? $1.resourceValues(forKeys: [.contentModificationDateKey]).contentModificationDate) ?? .distantPast
            return a > b
        }
        for file in sorted.dropFirst(maxChats) { try? fm.removeItem(at: file) }
    }
}
