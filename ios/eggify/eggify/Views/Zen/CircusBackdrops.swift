//
//  CircusBackdrops.swift
//  eggify
//
//  The ten Circus backdrops of the web app (zen/circusBackdrops.ts). A session
//  gets the one its id hashes to with the web's `hash01(id, 77)`, moved on to the
//  next backdrop nobody else open uses, so a session looks the same on the phone
//  and on the desktop. The pictures are generated from the web art by
//  `node scripts/ios-circus-assets.mjs` (Assets.xcassets/CircusBackdrop*): the
//  engraved paper-collage plates and cut-outs of public/zen/circus/backdrops,
//  flattened into one JPEG each.
//

import SwiftUI

enum CircusBackdropID: String, CaseIterable, Sendable {
    // Order matters: it is the web's index order.
    case meadow, dusk, storm, tearoom, snow, seaside, desert, jungle, gothic, space

    /// Image set name, e.g. `CircusBackdropMeadow`.
    var asset: String { "CircusBackdrop" + rawValue.prefix(1).uppercased() + rawValue.dropFirst() }

    /// Dark scene.
    var isDark: Bool {
        switch self {
        case .storm, .tearoom, .gothic, .space: true
        default: false
        }
    }

    /// The colour of the stage boards (also the composer bar's), as on the web (engraved palette).
    var boardHex: UInt32 {
        switch self {
        case .meadow: 0xcdb88c
        case .dusk: 0xa9765a
        case .storm: 0x6f7378
        case .tearoom: 0x8a6448
        case .snow: 0xd9d6cb
        case .seaside: 0xc9b48a
        case .desert: 0xcfa66e
        case .jungle: 0x8e8a62
        case .gothic: 0x5a5560
        case .space: 0x9a978e
        }
    }
}

enum CircusBackdrops {
    static let count = CircusBackdropID.allCases.count

    /// The web's `hash01` (types.ts): a salted 32-bit hash over UTF-16 units into [0, 1).
    nonisolated static func hash01(_ s: String, salt: UInt32 = 0) -> Double {
        var h: UInt32 = 2166136261 ^ salt
        for unit in s.utf16 {
            h ^= UInt32(unit)
            h = h &* 16777619
        }
        h ^= h >> 13
        h = h &* 0x5bd1e995
        h ^= h >> 15
        return Double(h) / 4294967296
    }

    /// The backdrop index a session id hashes to (before collision avoidance).
    nonisolated static func hashIndex(_ sessionID: String) -> Int {
        Int((hash01(sessionID, salt: 77) * Double(count)).rounded(.down)) % count
    }
}

/// Hands out backdrops so that the sessions open together look as different as
/// possible: the web's `createBackdropAssigner`. Once given, a session keeps its
/// backdrop; `given` lets the owner persist that.
struct CircusBackdropAssigner: Sendable {
    private(set) var given: [String: Int]

    init(restore: [String: Int] = [:]) {
        given = restore.filter { $0.value >= 0 && $0.value < CircusBackdrops.count }
    }

    /// `openIDs` are the sessions known right now (including `id`).
    mutating func assign(_ id: String, open openIDs: [String] = []) -> Int {
        if let have = given[id] { return have }
        var used = [Int](repeating: 0, count: CircusBackdrops.count)
        for other in Set(openIDs.filter { !$0.isEmpty && $0 != id }) {
            if let g = given[other] { used[g] += 1 }
        }
        let start = CircusBackdrops.hashIndex(id)
        var best = start
        var bestUse = Int.max
        for k in 0..<CircusBackdrops.count {
            let i = (start + k) % CircusBackdrops.count
            if used[i] < bestUse { bestUse = used[i]; best = i }
            if bestUse == 0 { break }
        }
        given[id] = best
        return best
    }

    /// Every open session gets a backdrop in a stable order (by hash, then id).
    mutating func assignAll(_ openIDs: [String]) {
        let order = openIDs.filter { !$0.isEmpty }.sorted {
            let a = CircusBackdrops.hashIndex($0), b = CircusBackdrops.hashIndex($1)
            return a != b ? a < b : $0 < $1
        }
        for id in order { _ = assign(id, open: openIDs) }
    }

    mutating func forget(_ id: String) { given[id] = nil }
}

/// The device's remembered assignment (like the web's localStorage entry).
@MainActor
enum CircusBackdropStore {
    private static let key = "eggify.zen.circusBackdrops"
    private static let maxRemembered = 200
    private static var assigner = CircusBackdropAssigner(
        restore: (UserDefaults.standard.dictionary(forKey: key) as? [String: Int]) ?? [:])

    static func backdrop(for sessionID: String, open openIDs: [String]) -> CircusBackdropID {
        guard !sessionID.isEmpty else { return .meadow }
        let before = assigner.given
        assigner.assignAll(openIDs + [sessionID])
        let index = assigner.assign(sessionID, open: openIDs + [sessionID])
        if assigner.given != before {
            if assigner.given.count > maxRemembered {
                for k in assigner.given.keys.sorted().prefix(assigner.given.count - maxRemembered) where k != sessionID {
                    assigner.forget(k)
                }
            }
            UserDefaults.standard.set(assigner.given, forKey: key)
        }
        return CircusBackdropID.allCases[index]
    }
}
