//
//  MachineSwitchOverlay.swift
//  eggify
//
//  The card shown while the app switches to another machine, the same one the
//  desktop app shows (machine_transition.js): the machine's colour, "Switching
//  to", an egg in its colour and its name. It slides in from the side the
//  machine comes from; a glint runs under the name if the switch takes long.
//  Reduce Motion: a plain fade.
//

import SwiftUI

struct MachineSwitchOverlay: View {
    let machineSwitch: MachineSwitch
    @Environment(\.accessibilityReduceMotion) private var reduceMotion
    @State private var shown = false
    @State private var slow = false

    var body: some View {
        let h = machineSwitch.hue
        ZStack {
            RadialGradient(
                colors: [MachineLook.color(hue: h, saturation: 0.48, lightness: 0.20),
                         MachineLook.color(hue: h, saturation: 0.40, lightness: 0.07)],
                center: UnitPoint(x: 0.5, y: 0.42), startRadius: 0, endRadius: 700)
                .ignoresSafeArea()
            VStack(spacing: 10) {
                Text("Switching to")
                    .font(.caption2.weight(.semibold))
                    .textCase(.uppercase)
                    .tracking(2)
                    .opacity(0.62)
                HStack(spacing: 12) {
                    Ellipse()
                        .fill(LinearGradient(
                            colors: [MachineLook.color(hue: h, saturation: 0.9, lightness: 0.78),
                                     MachineLook.color(hue: h, saturation: 0.75, lightness: 0.55)],
                            startPoint: .topLeading, endPoint: .bottomTrailing))
                        .frame(width: 14, height: 17)
                        .shadow(color: MachineLook.color(hue: h, saturation: 0.9, lightness: 0.65, opacity: 0.55), radius: 9)
                    Text(machineSwitch.name)
                        .font(.system(size: 30, weight: .semibold))
                        .lineLimit(1)
                        .minimumScaleFactor(0.6)
                }
                GlintBar(hue: h, running: slow && !reduceMotion)
                    .opacity(slow ? 1 : 0)
                    .padding(.top, 4)
            }
            .foregroundStyle(MachineLook.color(hue: h, saturation: 0.30, lightness: 0.96))
            .padding(.horizontal, 32)
            .offset(x: shown || reduceMotion ? 0 : CGFloat(machineSwitch.direction) * 48)
            .opacity(shown ? 1 : 0)
        }
        .accessibilityElement(children: .combine)
        .accessibilityLabel("Switching to \(machineSwitch.name)")
        .onAppear {
            withAnimation(reduceMotion ? .easeOut(duration: 0.12) : .spring(response: 0.32, dampingFraction: 0.86)) {
                shown = true
            }
        }
        .task {
            try? await Task.sleep(for: .milliseconds(450))
            withAnimation(.easeIn(duration: 0.2)) { slow = true }
        }
    }
}

private struct GlintBar: View {
    let hue: Int
    let running: Bool
    @State private var phase: CGFloat = -1

    var body: some View {
        Capsule()
            .fill(MachineLook.color(hue: hue, saturation: 0.40, lightness: 0.30, opacity: 0.6))
            .frame(width: 132, height: 2)
            .overlay(alignment: .leading) {
                Capsule()
                    .fill(LinearGradient(colors: [.clear, MachineLook.color(hue: hue, saturation: 0.9, lightness: 0.75), .clear],
                                         startPoint: .leading, endPoint: .trailing))
                    .frame(width: 53)
                    .offset(x: phase * 132)
            }
            .clipShape(Capsule())
            .onChange(of: running, initial: true) { _, run in
                guard run else { return }
                phase = -0.4
                withAnimation(.easeInOut(duration: 0.9).repeatForever(autoreverses: false)) { phase = 1 }
            }
    }
}

/// Puts the card over content while a switch runs; the content behind it
/// shrinks a little and blurs, like the page on the Mac.
struct MachineSwitchHost<Content: View>: View {
    let machineSwitch: MachineSwitch?
    @ViewBuilder let content: () -> Content
    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    var body: some View {
        let switching = machineSwitch != nil && !reduceMotion
        content()
            .scaleEffect(switching ? 0.965 : 1)
            .blur(radius: switching ? 8 : 0)
            .overlay {
                if let machineSwitch {
                    MachineSwitchOverlay(machineSwitch: machineSwitch)
                        .id(machineSwitch.id)
                        .transition(.opacity)
                }
            }
            .animation(reduceMotion ? .easeOut(duration: 0.12) : .easeOut(duration: 0.28), value: machineSwitch)
    }
}
