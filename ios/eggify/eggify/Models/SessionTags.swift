//
//  SessionTags.swift
//  eggify
//
//  The user's own session tags, as the web's `utils/sessionTags.ts`: split on
//  commas, trimmed, case-insensitive repeats dropped; suggestions are the tags
//  already used on other sessions, most used first. The server keeps at most
//  8 tags of 40 characters (`PUT /api/agent/sessions/:id/tags`).
//

import Foundation

nonisolated enum SessionTags {
    static let maxTags = 8
    static let maxLength = 40

    /// "bug,  Deploy , bug" → ["bug", "Deploy"].
    static func split(_ value: String) -> [String] {
        normalize(value.split(separator: ",", omittingEmptySubsequences: false).map(String.init))
    }

    /// Trimmed, inner whitespace collapsed, case-insensitive repeats dropped,
    /// cut to the server's limits.
    static func normalize(_ tags: [String]) -> [String] {
        var out: [String] = []
        var seen = Set<String>()
        for raw in tags {
            let tag = String(raw.split(whereSeparator: \.isWhitespace).joined(separator: " ").prefix(maxLength))
            guard !tag.isEmpty, seen.insert(tag.lowercased()).inserted else { continue }
            out.append(tag)
            if out.count == maxTags { break }
        }
        return out
    }

    /// Every tag in use, most used first (ties alphabetical).
    static func known(_ tagLists: [[String]]) -> [String] {
        var counts: [String: (tag: String, n: Int)] = [:]
        for tags in tagLists {
            for tag in normalize(tags) {
                counts[tag.lowercased(), default: (tag, 0)].n += 1
            }
        }
        return counts.values
            .sorted { $0.n != $1.n ? $0.n > $1.n : $0.tag.localizedCompare($1.tag) == .orderedAscending }
            .map(\.tag)
    }

    /// Known tags that start with (then contain) the query, minus `exclude`.
    static func suggest(_ query: String, known: [String], exclude: [String], limit: Int = 6) -> [String] {
        let q = query.trimmingCharacters(in: .whitespaces).lowercased()
        let skip = Set(exclude.map { $0.lowercased() })
        let pool = known.filter { !skip.contains($0.lowercased()) }
        guard !q.isEmpty else { return Array(pool.prefix(limit)) }
        let starts = pool.filter { $0.lowercased().hasPrefix(q) }
        let contains = pool.filter { !$0.lowercased().hasPrefix(q) && $0.lowercased().contains(q) }
        return Array((starts + contains).prefix(limit))
    }
}
