//
//  LicenseRequiredView.swift
//  eggify
//
//  Full-screen state while Eggify on the computer isn't activated (see
//  LicenseGate). The iPhone has no license of its own; the user activates the
//  Mac, then taps Retry.
//

import SwiftUI

struct LicenseRequiredView: View {
    @ObservedObject var monitor: LicenseMonitor
    var hostName: String?

    var body: some View {
        ZStack {
            EggifyColors.background.ignoresSafeArea()
            VStack(spacing: 18) {
                Image(systemName: "key.horizontal.fill")
                    .font(.system(size: 46))
                    .foregroundStyle(EggifyColors.accent)
                    .accessibilityHidden(true)
                Text(LicenseGate.title)
                    .font(.eggifyDisplay(26, relativeTo: .title2))
                    .foregroundStyle(EggifyColors.textPrimary)
                    .multilineTextAlignment(.center)
                Text(LicenseGate.message)
                    .font(.body)
                    .foregroundStyle(EggifyColors.textSecondary)
                    .multilineTextAlignment(.center)
                    .accessibilityIdentifier("license-required-message")
                if let hostName, !hostName.isEmpty {
                    Text(hostName)
                        .font(.footnote.monospaced())
                        .foregroundStyle(EggifyColors.textTertiary)
                }
                if let error = monitor.lastCheckError {
                    Text(error)
                        .font(.footnote)
                        .foregroundStyle(EggifyColors.warning)
                        .multilineTextAlignment(.center)
                        .accessibilityIdentifier("license-required-error")
                }
                Button {
                    Task { await monitor.retry() }
                } label: {
                    if monitor.isChecking {
                        ProgressView().tint(.white)
                            .frame(maxWidth: 220)
                    } else {
                        Text("Retry").frame(maxWidth: 220)
                    }
                }
                .buttonStyle(.borderedProminent)
                .tint(EggifyColors.accent)
                .controlSize(.large)
                .disabled(monitor.isChecking)
                .accessibilityIdentifier("license-required-retry")
            }
            .padding(32)
            .frame(maxWidth: 420)
        }
        .accessibilityIdentifier("license-required")
    }
}
