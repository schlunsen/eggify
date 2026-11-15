//
//  EggifyTheme.swift
//  eggify
//
//  Centralized design tokens for the Eggify iOS app.
//  The Flying Circus look of the web dashboard, in iOS terms.
//

import SwiftUI
import UIKit
import CoreText

// MARK: - Colors
//
// The Flying Circus palette from the web dashboard
// (internal/server/frontend/assets/css/themes/flyingcircus.css): Victorian
// engravings glued onto aged paper. Sepia ink and cream paper, with loud pink,
// mustard and teal accents. Dark mode is "the same collage, after closing time".

private func dynamic(light: UInt32, dark: UInt32, alpha: CGFloat = 1) -> Color {
    Color(UIColor { traits in
        UIColor(hex: traits.userInterfaceStyle == .dark ? dark : light, alpha: alpha)
    })
}

extension UIColor {
    convenience init(hex: UInt32, alpha: CGFloat = 1) {
        self.init(red: CGFloat((hex >> 16) & 0xFF) / 255,
                  green: CGFloat((hex >> 8) & 0xFF) / 255,
                  blue: CGFloat(hex & 0xFF) / 255,
                  alpha: alpha)
    }
}

enum EggifyColors {
    // MARK: Accents

    /// Hand-tinted pink: the primary accent.
    static let accent = dynamic(light: 0xC2185B, dark: 0xFF6FA8)
    /// Pink wash for selected rows and badges.
    static let accentLight = dynamic(light: 0xC2185B, dark: 0xFF6FA8, alpha: 0.12)
    /// Sky teal.
    static let secondary = dynamic(light: 0x1F7A70, dark: 0x5CC8B8)
    /// Mustard (darkened for text in light mode).
    static let mustard = dynamic(light: 0x8A6510, dark: 0xF2C14E)

    /// The bright decorative trio used for the cut-out paper strip.
    static let circusPink = dynamic(light: 0xE8458B, dark: 0xFF6FA8)
    static let circusMustard = dynamic(light: 0xD4A017, dark: 0xF2C14E)
    static let circusTeal = dynamic(light: 0x2A9D8F, dark: 0x5CC8B8)

    // MARK: Status

    static let success = dynamic(light: 0x2E7D32, dark: 0x7BC67E)
    static let warning = dynamic(light: 0x8A6510, dark: 0xF2C14E)
    static let error = dynamic(light: 0xB3261E, dark: 0xFF6B5E)

    // MARK: Paper

    /// Aged paper (light) / burnt umber (dark): page background.
    static let background = dynamic(light: 0xEFE4C8, dark: 0x1C1610)
    /// Cut-out card stock: cards and rows.
    static let surface = dynamic(light: 0xFBF5E6, dark: 0x2A2117)
    /// Fresh paper: inputs and nested cards.
    static let surfaceTertiary = dynamic(light: 0xF6EDD6, dark: 0x2E241A)
    /// Code blocks.
    static let codeBackground = dynamic(light: 0xE9DCBC, dark: 0x19130D)
    /// Scissor-cut paper edge.
    static let border = dynamic(light: 0xD9C7A0, dark: 0x4A3A28)

    // MARK: Ink

    static let textPrimary = dynamic(light: 0x2B1D10, dark: 0xF3E9D2)
    static let textSecondary = dynamic(light: 0x5C4630, dark: 0xCDBB98)
    static let textTertiary = dynamic(light: 0x6B5640, dark: 0xA8956F)
}

// MARK: - Gradients

enum EggifyGradients {
    /// User message bubble: pink ink.
    static let userBubble = LinearGradient(
        colors: [EggifyColors.accent, EggifyColors.accent.opacity(0.85)],
        startPoint: .topLeading,
        endPoint: .bottomTrailing
    )

    static let sendButton = userBubble

    static let assistantAvatar = LinearGradient(
        colors: [EggifyColors.secondary, EggifyColors.circusMustard],
        startPoint: .topLeading,
        endPoint: .bottomTrailing
    )

    static let loginButton = LinearGradient(
        colors: [EggifyColors.accent, EggifyColors.accent.opacity(0.88)],
        startPoint: .leading,
        endPoint: .trailing
    )

    static let loginBackground = LinearGradient(
        colors: [EggifyColors.background, EggifyColors.surfaceTertiary],
        startPoint: .top,
        endPoint: .bottom
    )

    static let loginBackgroundLight = loginBackground

    static let emptyState = LinearGradient(
        colors: [EggifyColors.circusPink, EggifyColors.circusMustard],
        startPoint: .topLeading,
        endPoint: .bottomTrailing
    )
}

// MARK: - Fonts
//
// Circus-poster headings (Rye), engraved-book text (EB Garamond) and a
// typewriter for code (Courier Prime), as on the web. The fonts ship in
// Fonts/ (SIL OFL) and are registered at launch, so no Info.plist list is
// needed. All sizes scale with Dynamic Type.

enum EggifyFonts {
    static let display = "Rye-Regular"
    static let serif = "EBGaramond-Regular"
    static let serifMedium = "EBGaramond-Medium"
    static let serifSemiBold = "EBGaramond-SemiBold"
    static let serifItalic = "EBGaramond-Italic"
    static let mono = "CourierPrime-Regular"
    static let monoBold = "CourierPrime-Bold"

    /// Register every bundled font file once.
    static func registerAll() {
        let urls = ["ttf", "otf"].flatMap { Bundle.main.urls(forResourcesWithExtension: $0, subdirectory: nil) ?? [] }
            + ["ttf", "otf"].flatMap { Bundle.main.urls(forResourcesWithExtension: $0, subdirectory: "Fonts") ?? [] }
        for url in Set(urls) {
            CTFontManagerRegisterFontsForURL(url as CFURL, .process, nil)
        }
        applyUIKitAppearance()
    }

    /// Navigation bar titles: Rye for large titles, Garamond for inline ones.
    private static func applyUIKitAppearance() {
        let large = UIFont(name: display, size: 32).map { UIFontMetrics(forTextStyle: .largeTitle).scaledFont(for: $0) }
        let inline = UIFont(name: serifSemiBold, size: 19).map { UIFontMetrics(forTextStyle: .headline).scaledFont(for: $0) }
        let appearance = UINavigationBar.appearance()
        if let large { appearance.largeTitleTextAttributes = [.font: large] }
        if let inline { appearance.titleTextAttributes = [.font: inline] }
    }
}

extension Font {
    /// Rye, for titles and the brand name.
    static func eggifyDisplay(_ size: CGFloat, relativeTo style: Font.TextStyle = .title) -> Font {
        .custom(EggifyFonts.display, size: size, relativeTo: style)
    }

    /// EB Garamond. Its x-height is small, so it runs a size up from SF.
    static func eggifySerif(_ size: CGFloat = 18, relativeTo style: Font.TextStyle = .body) -> Font {
        .custom(EggifyFonts.serif, size: size, relativeTo: style)
    }

    static func eggifySerifSemiBold(_ size: CGFloat = 18, relativeTo style: Font.TextStyle = .body) -> Font {
        .custom(EggifyFonts.serifSemiBold, size: size, relativeTo: style)
    }

    /// Courier Prime, for code.
    static func eggifyMono(_ size: CGFloat = 14, relativeTo style: Font.TextStyle = .footnote) -> Font {
        .custom(EggifyFonts.mono, size: size, relativeTo: style)
    }
}

// MARK: - Cut-out paper strip

/// Pink, mustard and teal strips of paper, as under the web navbar.
struct CircusStripe: View {
    var body: some View {
        VStack(spacing: 0) {
            EggifyColors.circusPink.frame(height: 2)
            EggifyColors.circusMustard.frame(height: 2)
            EggifyColors.circusTeal.frame(height: 2)
        }
        .accessibilityHidden(true)
    }
}

// MARK: - Spacing

enum EggifySpacing {
    static let xxs: CGFloat = 2
    static let xs: CGFloat = 4
    static let sm: CGFloat = 8
    static let md: CGFloat = 12
    static let lg: CGFloat = 16
    static let xl: CGFloat = 24
    static let xxl: CGFloat = 32

    /// Standard card horizontal margin.
    static let cardMargin: CGFloat = 16
    /// Standard card internal horizontal padding.
    static let cardPaddingH: CGFloat = 14
    /// Standard card internal vertical padding.
    static let cardPaddingV: CGFloat = 12
}

// MARK: - Typography Helpers

enum EggifyTypography {
    static let mono = Font.eggifyMono(12, relativeTo: .caption2)
    static let monoSmall = Font.custom(EggifyFonts.monoBold, size: 12, relativeTo: .caption2)
    static let badge = Font.caption2.weight(.bold)
}

// MARK: - Status Color Helper

extension EggifyColors {
    /// Map a status string to the appropriate brand color.
    static func statusColor(for status: String) -> Color {
        switch status.lowercased() {
        case "active", "running", "processing":
            return accent
        case "pending", "planning":
            return secondary
        case "completed":
            return success
        case "failed", "error":
            return error
        case "paused":
            return warning
        case "idle":
            return Color.gray
        default:
            return Color.gray
        }
    }
}
