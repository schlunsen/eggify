//
//  PairingStatusOverlay.swift
//  eggify
//
//  The only screen a QR-code sign-in shows: "Connecting to <host>…", a short
//  success state, a clear error (with "show a new code" guidance when the
//  code expired), or "Switch to <host>?" when already signed in elsewhere.
//

import SwiftUI

struct PairingStatusOverlay: View {
    @EnvironmentObject var auth: AuthenticationViewModel
    /// Opens the in-app scanner (for "Scan again").
    var onScanAgain: (() -> Void)?

    var body: some View {
        if auth.pairingState.isActive {
            ZStack {
                Color.black.opacity(0.35)
                    .ignoresSafeArea()
                    .onTapGesture { if canDismiss { auth.dismissPairing() } }
                card
                    .padding(24)
                    .transition(.scale(scale: 0.92).combined(with: .opacity))
            }
            .animation(.spring(response: 0.35, dampingFraction: 0.85), value: auth.pairingState)
            .accessibilityIdentifier("pairing-overlay")
        }
    }

    private var canDismiss: Bool {
        if case .connecting = auth.pairingState { return false }
        return true
    }

    private var insecureWarning: some View {
        Label("Plain http: only use this on your own network.", systemImage: "lock.open.fill")
            .font(.footnote)
            .foregroundStyle(EggifyColors.warning)
    }

    private var card: some View {
        VStack(spacing: 16) {
            switch auth.pairingState {
            case .idle:
                EmptyView()

            case .connecting(let host, let insecure):
                ProgressView()
                    .controlSize(.large)
                    .tint(EggifyColors.accent)
                Text("Connecting to \(host)…")
                    .font(.headline)
                    .foregroundStyle(EggifyColors.textPrimary)
                    .multilineTextAlignment(.center)
                    .accessibilityIdentifier("pairing-connecting")
                if insecure {
                    insecureWarning
                }

            case .success(let host, let username):
                Image(systemName: "checkmark.circle.fill")
                    .font(.system(size: 52))
                    .foregroundStyle(EggifyColors.success)
                    .symbolEffect(.bounce, value: host)
                Text("Signed in")
                    .font(.eggifyDisplay(26, relativeTo: .title2))
                    .foregroundStyle(EggifyColors.textPrimary)
                Text("as \(username) on \(host)")
                    .font(.subheadline)
                    .foregroundStyle(EggifyColors.textSecondary)
                    .accessibilityIdentifier("pairing-success")

            case .added(_, let name):
                Image(systemName: "checkmark.circle.fill")
                    .font(.system(size: 52))
                    .foregroundStyle(EggifyColors.success)
                    .symbolEffect(.bounce, value: name)
                Text("\(name) added")
                    .font(.eggifyDisplay(24, relativeTo: .title2))
                    .foregroundStyle(EggifyColors.textPrimary)
                    .multilineTextAlignment(.center)
                    .accessibilityIdentifier("pairing-added")
                Text("This iPhone is signed in to \(name) with its own login. Switch from the server menu at the top any time.")
                    .font(.subheadline)
                    .foregroundStyle(EggifyColors.textSecondary)
                    .multilineTextAlignment(.center)
                HStack {
                    Button("Stay here") { auth.dismissPairing() }
                        .buttonStyle(.bordered)
                    Button("Switch to \(name)") { auth.switchToAddedHost() }
                        .buttonStyle(.borderedProminent)
                        .tint(EggifyColors.accent)
                        .accessibilityIdentifier("pairing-added-switch")
                }

            case .failed(let host, let message, let isExpired):
                Image(systemName: isExpired ? "clock.badge.exclamationmark" : "exclamationmark.triangle.fill")
                    .font(.system(size: 44))
                    .foregroundStyle(isExpired ? EggifyColors.warning : EggifyColors.error)
                Text(isExpired ? "Code expired" : "Couldn't sign in")
                    .font(.eggifyDisplay(24, relativeTo: .title2))
                    .foregroundStyle(EggifyColors.textPrimary)
                if let host {
                    Text(host)
                        .font(.footnote.monospaced())
                        .foregroundStyle(EggifyColors.textTertiary)
                }
                Text(message)
                    .font(.subheadline)
                    .foregroundStyle(EggifyColors.textSecondary)
                    .multilineTextAlignment(.center)
                    .accessibilityIdentifier("pairing-error")
                if isExpired {
                    Text("Show a new code on your computer (Eggify → Settings → Remote access → Pair the iOS app) and scan it again.")
                        .font(.footnote)
                        .foregroundStyle(EggifyColors.textSecondary)
                        .multilineTextAlignment(.center)
                }
                HStack {
                    Button("Close") { auth.dismissPairing() }
                        .buttonStyle(.bordered)
                    if let onScanAgain {
                        Button("Scan again") {
                            auth.dismissPairing()
                            onScanAgain()
                        }
                        .buttonStyle(.borderedProminent)
                        .tint(EggifyColors.accent)
                    }
                }

            case .confirmSwitch(let link, let currentHost):
                Image(systemName: "arrow.left.arrow.right.circle.fill")
                    .font(.system(size: 44))
                    .foregroundStyle(EggifyColors.accent)
                Text("Switch to \(link.hostLabel)?")
                    .font(.eggifyDisplay(22, relativeTo: .title2))
                    .foregroundStyle(EggifyColors.textPrimary)
                    .multilineTextAlignment(.center)
                    .accessibilityIdentifier("pairing-switch")
                if link.isInsecureLocal {
                    insecureWarning
                }
                Text("You're signed in to \(currentHost). Your login there stays saved on this iPhone, and you can switch back from the server menu. Add only saves \(link.hostLabel) without leaving \(currentHost).")
                    .font(.subheadline)
                    .foregroundStyle(EggifyColors.textSecondary)
                    .multilineTextAlignment(.center)
                HStack {
                    Button("Cancel") { auth.dismissPairing() }
                        .buttonStyle(.bordered)
                    Button("Add only") { auth.addWithoutSwitching() }
                        .buttonStyle(.bordered)
                        .accessibilityIdentifier("pairing-add-only")
                    Button("Switch") { auth.confirmSwitch() }
                        .buttonStyle(.borderedProminent)
                        .tint(EggifyColors.accent)
                        .accessibilityIdentifier("pairing-switch-confirm")
                }
            }
        }
        .padding(24)
        .frame(maxWidth: 380)
        .background(EggifyColors.surface, in: RoundedRectangle(cornerRadius: 22))
        .overlay(
            RoundedRectangle(cornerRadius: 22)
                .strokeBorder(EggifyColors.border, lineWidth: 1)
        )
        .shadow(color: .black.opacity(0.25), radius: 20, y: 8)
    }
}
