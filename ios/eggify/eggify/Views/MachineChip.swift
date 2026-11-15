//
//  MachineChip.swift
//  eggify
//
//  Which Mac (saved host) the app shows, as the desktop app's top-bar chip:
//  a capsule with an egg in the machine's colour, its name in theme colours
//  and a thin border in the machine's colour. It is HostMenu's label in the
//  navigation bars of the top-level tabs when 2+ hosts are saved
//  (`machineChipInNavigationBar()`), so tapping it opens the same menu
//  (switch, Add a known Mac, Manage Hosts).
//

import SwiftUI

struct MachineChip: View {
    let name: String
    /// Shows the up/down chevron (there is another machine to switch to).
    var switchable = true
    @Environment(\.colorScheme) private var colorScheme

    var body: some View {
        let hue = MachineLook.hue(name)
        let dark = colorScheme == .dark
        HStack(spacing: 6) {
            Ellipse()
                .fill(LinearGradient(
                    colors: [MachineLook.color(hue: hue, saturation: 0.9, lightness: dark ? 0.78 : 0.66),
                             MachineLook.color(hue: hue, saturation: 0.75, lightness: dark ? 0.55 : 0.45)],
                    startPoint: .topLeading, endPoint: .bottomTrailing))
                .frame(width: 9, height: 11)
                .shadow(color: MachineLook.color(hue: hue, saturation: 0.9, lightness: 0.6, opacity: dark ? 0.5 : 0.3), radius: 3)
                .accessibilityHidden(true)
            Text(name)
                .font(.subheadline.weight(.semibold))
                .foregroundStyle(EggifyColors.textPrimary)
                .lineLimit(1)
                .truncationMode(.tail)
                .minimumScaleFactor(0.8)
            if switchable {
                Image(systemName: "chevron.up.chevron.down")
                    .font(.caption2.weight(.bold))
                    .foregroundStyle(EggifyColors.textSecondary)
                    .accessibilityHidden(true)
            }
        }
        .padding(.horizontal, 11)
        .padding(.vertical, 5)
        .background(EggifyColors.surface.opacity(0.9), in: Capsule())
        .overlay(
            Capsule().strokeBorder(
                MachineLook.color(hue: hue, saturation: 0.7, lightness: dark ? 0.62 : 0.45, opacity: dark ? 0.75 : 0.65),
                lineWidth: 1)
        )
        .frame(maxWidth: 220)
        // A nav bar has room for one line; past this size the name truncates.
        .dynamicTypeSize(...DynamicTypeSize.accessibility1)
        .frame(minWidth: 44, minHeight: 44)
        .contentShape(Rectangle())
    }
}

/// A small egg in the machine's colour (chat header subtitle).
struct MachineDot: View {
    let name: String
    @Environment(\.colorScheme) private var colorScheme

    var body: some View {
        Ellipse()
            .fill(MachineLook.color(hue: MachineLook.hue(name), saturation: 0.75,
                                    lightness: colorScheme == .dark ? 0.62 : 0.48))
            .frame(width: 6, height: 7.5)
            .accessibilityHidden(true)
    }
}

extension View {
    /// The machine chip centred in this screen's navigation bar, with 2+
    /// saved hosts (the top-level tabs).
    func machineChipInNavigationBar() -> some View {
        modifier(MachineChipToolbar())
    }
}

private struct MachineChipToolbar: ViewModifier {
    @EnvironmentObject private var authViewModel: AuthenticationViewModel

    func body(content: Content) -> some View {
        content.toolbar {
            if MachineChipModel.shows(hostCount: authViewModel.savedHosts.count) {
                ToolbarItem(placement: .principal) {
                    HostMenu(style: .chip)
                }
                .sharedBackgroundVisibility(.hidden)
            }
        }
    }
}

#Preview {
    VStack(spacing: 12) {
        MachineChip(name: "Studio")
        MachineChip(name: "MacBook-Pro")
        MachineChip(name: "Dev’s very long Mac mini name", switchable: false)
    }
    .padding()
    .background(EggifyColors.background)
}
