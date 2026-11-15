//
//  MachineLook.swift
//  eggify
//
//  How a machine (a saved host: one of your Macs running Eggify) looks when
//  you switch to it: its colour comes from its name exactly like the desktop
//  app's (machineHue in cmd/eggify-desktop/machine_switch.go: FNV-1a over the
//  UTF-8 bytes, mod 360), so "Studio" is the same colour on the phone and on
//  the other Mac. Also the names: a Mac paired over the Eggify relay is named
//  after its host name instead of its relay address.
//

import SwiftUI

enum MachineLook {
    /// 0..<360, the desktop app's machineHue.
    static func hue(_ name: String) -> Int {
        var hash: UInt32 = 0x811c9dc5
        for byte in name.utf8 {
            hash ^= UInt32(byte)
            hash = hash &* 16777619
        }
        return Int(hash % 360)
    }

    /// HSL like the desktop transition's CSS (h in degrees, s and l 0...1).
    static func color(hue h: Int, saturation s: Double, lightness l: Double, opacity: Double = 1) -> Color {
        let v = l + s * min(l, 1 - l)
        let sv = v == 0 ? 0 : 2 * (1 - l / v)
        return Color(hue: Double(h) / 360, saturation: sv, brightness: v, opacity: opacity)
    }

    /// True for the name pairing gives a relay address before the Mac's own
    /// name is known: the capitalised install id (32 base32 characters).
    static func isRelayPlaceholder(_ name: String, url: String) -> Bool {
        guard let host = URL(string: url)?.host?.lowercased() else { return false }
        let label = host.split(separator: ".").first.map(String.init) ?? ""
        guard label.count == 32, label.allSatisfy({ ("a"..."z").contains(String($0)) || ("2"..."7").contains(String($0)) }) else {
            return false
        }
        return name.lowercased() == label
    }

    /// "Studio.local" → "Studio"; nil when there is nothing usable.
    static func cleanedHostname(_ raw: String?) -> String? {
        guard var name = raw?.trimmingCharacters(in: .whitespacesAndNewlines), !name.isEmpty else { return nil }
        if name.lowercased().hasSuffix(".local") { name = String(name.dropLast(6)) }
        name = String(name.prefix(40))
        return name.isEmpty ? nil : name
    }
}

/// A switch in progress: what the card shows.
struct MachineSwitch: Equatable, Identifiable {
    let id = UUID()
    let name: String
    let hue: Int
    /// 1: the machine comes in from the right (later in the list).
    let direction: Int
}

/// The machine chip in the navigation bars (`MachineChip`): which Mac the app
/// shows, like the chip in the desktop app's top bar. Only with 2+ saved
/// hosts; with one there is nothing to tell apart.
enum MachineChipModel {
    static func shows(hostCount: Int) -> Bool { hostCount >= 2 }

    /// The current host's name as the chip shows it ("Mac" when unknown).
    static func name(_ raw: String?) -> String {
        let name = raw?.trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
        return name.isEmpty ? "Mac" : name
    }

    static func accessibilityLabel(_ raw: String?) -> String {
        "Mac: \(name(raw)), double-tap to switch"
    }
}
