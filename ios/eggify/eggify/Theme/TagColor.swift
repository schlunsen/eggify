//
//  TagColor.swift
//  eggify
//
//  Colours for the user's session tags, as the web's `utils/tagColor.ts`:
//  every tag gets its own hue from an FNV-1a hash of its lowercased text, so
//  "bug" has the same colour here as on the desktop. Only the hue comes from
//  the hash; the chip mixes it with the theme's text colour (text) and card
//  colour (fill, border), like the web's `.tag-chip`, so it reads in light
//  and dark mode alike.
//

import SwiftUI
import UIKit

nonisolated enum TagColor {
    /// FNV-1a over the lowercased, trimmed tag's UTF-16 code units (JS charCodeAt).
    static func hash(_ tag: String) -> UInt32 {
        var hash: UInt32 = 0x811c9dc5
        for unit in tag.trimmingCharacters(in: .whitespacesAndNewlines).lowercased().utf16 {
            hash ^= UInt32(unit)
            hash = hash &* 0x01000193
        }
        return hash
    }

    /// 0..359, stable per tag (case-insensitive).
    static func hue(_ tag: String) -> Int {
        Int(hash(tag) % 360)
    }

    /// Which tags fit on a row: tags are added while their text stays within
    /// `budget` characters (each chip costs its text plus ~2 for padding);
    /// the first is always shown, the rest become a "+N" chip.
    static func fit(_ tags: [String], budget: Int, maxShown: Int = 4) -> (shown: [String], hidden: [String]) {
        var shown: [String] = []
        var used = 0
        for tag in tags {
            let cost = tag.count + 2
            if !shown.isEmpty && (used + cost > budget || shown.count >= maxShown) { break }
            shown.append(tag)
            used += cost
        }
        return (shown, Array(tags.dropFirst(shown.count)))
    }

    /// hsl(hue 75% 50%), the web's `--tag-base`, as sRGB components.
    static func base(hue: Int) -> (r: CGFloat, g: CGFloat, b: CGFloat) {
        let s: CGFloat = 0.75, l: CGFloat = 0.5
        let c = (1 - abs(2 * l - 1)) * s
        let h = CGFloat(hue % 360) / 60
        let x = c * (1 - abs(h.truncatingRemainder(dividingBy: 2) - 1))
        let (r, g, b): (CGFloat, CGFloat, CGFloat)
        switch Int(h) {
        case 0: (r, g, b) = (c, x, 0)
        case 1: (r, g, b) = (x, c, 0)
        case 2: (r, g, b) = (0, c, x)
        case 3: (r, g, b) = (0, x, c)
        case 4: (r, g, b) = (x, 0, c)
        default: (r, g, b) = (c, 0, x)
        }
        let m = l - c / 2
        return (r + m, g + m, b + m)
    }
}

/// The three colours of a tag chip, resolved per light/dark appearance.
struct TagChipColors {
    let text: Color
    let fill: Color
    let border: Color

    init(tag: String) {
        let base = TagColor.base(hue: TagColor.hue(tag))
        // Theme text and card colours (EggifyColors.textPrimary / surface).
        func mixed(_ weight: CGFloat, light: UInt32, dark: UInt32) -> Color {
            Color(UIColor { traits in
                let other = traits.userInterfaceStyle == .dark ? dark : light
                let r = CGFloat((other >> 16) & 0xFF) / 255
                let g = CGFloat((other >> 8) & 0xFF) / 255
                let b = CGFloat(other & 0xFF) / 255
                return UIColor(red: base.r * weight + r * (1 - weight),
                               green: base.g * weight + g * (1 - weight),
                               blue: base.b * weight + b * (1 - weight), alpha: 1)
            })
        }
        text = mixed(0.45, light: 0x2B1D10, dark: 0xF3E9D2)
        fill = mixed(0.15, light: 0xFBF5E6, dark: 0x2A2117)
        border = mixed(0.5, light: 0xFBF5E6, dark: 0x2A2117)
    }
}
