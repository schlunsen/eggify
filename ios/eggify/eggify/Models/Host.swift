//
//  Host.swift
//  eggify
//
//  Data model for Eggify server hosts
//

import Foundation

/// Represents a saved Eggify server configuration
struct Host: Identifiable, Codable, Equatable {
    let id: String  // UUID for this host configuration
    let name: String  // User-friendly name (e.g., "Local Dev", "Production")
    let url: String  // Server URL (e.g., "https://localhost:3333")
    let username: String?  // Optional: last known username for this host
    let createdAt: Date
    let updatedAt: Date
    let lastUsedAt: Date?

    enum CodingKeys: String, CodingKey {
        case id, name, url, username
        case createdAt = "created_at"
        case updatedAt = "updated_at"
        case lastUsedAt = "last_used_at"
    }

    init(
        id: String = UUID().uuidString,
        name: String,
        url: String,
        username: String? = nil,
        createdAt: Date = Date(),
        updatedAt: Date = Date(),
        lastUsedAt: Date? = nil
    ) {
        self.id = id
        self.name = name
        self.url = url
        self.username = username
        self.createdAt = createdAt
        self.updatedAt = updatedAt
        self.lastUsedAt = lastUsedAt
    }

    /// Normalize URL for consistency (add https:// if needed)
    nonisolated static func normalizeURL(_ urlString: String) -> String {
        var normalized = urlString.trimmingCharacters(in: .whitespacesAndNewlines)
        if !normalized.lowercased().hasPrefix("http://") && !normalized.lowercased().hasPrefix("https://") {
            normalized = "https://\(normalized)"
        }
        while normalized.hasSuffix("/") { normalized.removeLast() }
        // A local Eggify server listens on 3333 unless told otherwise.
        if var components = URLComponents(string: normalized),
           let host = components.host, ["localhost", "127.0.0.1"].contains(host), components.port == nil {
            components.port = 3333
            normalized = components.string ?? normalized
        }
        return normalized
    }

    /// Update last used timestamp
    mutating func recordUse() {
        // Create a new Host with updated lastUsedAt
        let updated = Host(
            id: self.id,
            name: self.name,
            url: self.url,
            username: self.username,
            createdAt: self.createdAt,
            updatedAt: Date(),
            lastUsedAt: Date()
        )
        self = updated
    }

    /// Create updated copy with new last used timestamp
    func withUpdatedLastUsed() -> Host {
        return Host(
            id: self.id,
            name: self.name,
            url: self.url,
            username: self.username,
            createdAt: self.createdAt,
            updatedAt: Date(),
            lastUsedAt: Date()
        )
    }
}

/// Represents a saved credential for a host
struct HostCredential: Codable {
    let hostId: String
    let sessionToken: String
    let expiresAt: Date?

    enum CodingKeys: String, CodingKey {
        case hostId = "host_id"
        case sessionToken = "session_token"
        case expiresAt = "expires_at"
    }
}
