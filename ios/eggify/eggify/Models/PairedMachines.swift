//
//  PairedMachines.swift
//  eggify
//
//  The other Macs the Mac you're signed in to knows (ADR 014): its desktop
//  app is paired with them (Machines in its menu bar icon). The phone lists
//  them from `GET /api/machines/paired` (names and relay addresses only) and
//  adds one with one tap: `POST /api/machines/paired/:id/pair-code` has that
//  Mac mint a pair link, which the phone redeems at the other Mac itself, so
//  it gets its own session there (its own row under Signed-in devices). No
//  token ever passes through the first Mac.
//

import Foundation

/// One Mac from `GET /api/machines/paired`.
nonisolated struct PairedMachine: Equatable, Identifiable, Hashable {
    let id: String
    let name: String
    /// Its Eggify relay address, `https://<install id>.r.example.com`.
    let baseURL: String

    static func parseList(_ d: [String: Any]) -> [PairedMachine] {
        d.dicts("machines").compactMap { m in
            guard let id = m.nonEmptyString("id"), let raw = m.nonEmptyString("base_url"),
                  // https relay addresses only; anything else is not offered.
                  let validated = try? PairingPayload.validateServer(raw), !validated.isInsecureLocal else { return nil }
            let url = validated.0
            let name = m.nonEmptyString("name") ?? url.host?.split(separator: ".").first.map(String.init) ?? "Mac"
            return PairedMachine(id: id, name: name, baseURL: url.absoluteString)
        }
    }
}

nonisolated enum PairedMachinesError: LocalizedError, Equatable {
    case badLink

    var errorDescription: String? {
        switch self {
        case .badLink:
            return "The Mac answered with a pair link for a different address. Pair that Mac by scanning its own QR code instead."
        }
    }
}

nonisolated enum PairedMachines {
    /// The Macs not saved on this phone yet (matched by normalized URL), in
    /// the server's order, without duplicates.
    static func unsaved(_ list: [PairedMachine], savedURLs: [String]) -> [PairedMachine] {
        let saved = Set(savedURLs.map(Host.normalizeURL))
        var seen = Set<String>()
        return list.filter { m in
            let url = Host.normalizeURL(m.baseURL)
            return !saved.contains(url) && seen.insert(url).inserted
        }
    }

    /// "MacBook knows 1 more Mac: Studio", "MacBook knows 2 more Macs: Studio, Mini".
    static func summary(macName: String, machines: [PairedMachine]) -> String {
        let names = machines.map(\.name).joined(separator: ", ")
        let count = machines.count == 1 ? "1 more Mac" : "\(machines.count) more Macs"
        return "\(macName) knows \(count): \(names)"
    }

    /// The name the other Mac lists under Signed-in devices:
    /// "Dev’s iPhone (added via MacBook)", at most 64 characters (the
    /// server's limit), the phone's own name shortened first.
    static func deviceName(phone: String, via mac: String) -> String {
        let phone = phone.trimmingCharacters(in: .whitespacesAndNewlines)
        let suffix = " (added via \(mac.trimmingCharacters(in: .whitespacesAndNewlines).prefix(24)))"
        let room = max(8, 64 - suffix.count)
        return String((String(phone.prefix(room)) + suffix).prefix(64))
    }

    /// The pair link the first Mac handed on, checked: an eggify://pair link
    /// for exactly this machine's address, with the key pin that belongs to it.
    static func checkedLink(_ pairURL: String, for machine: PairedMachine) throws -> PairingLink {
        guard case .pair(let link) = try PairingPayload.parse(pairURL),
              link.pin != nil, !link.isInsecureLocal,
              Host.normalizeURL(link.baseURL.absoluteString) == Host.normalizeURL(machine.baseURL) else {
            throw PairedMachinesError.badLink
        }
        return link
    }
}
