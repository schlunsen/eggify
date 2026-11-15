//
//  LoopViews.swift
//  eggify
//
//  Loop-mode sessions: the strip above the composer (iteration, checking,
//  last result, Stop loop), its details sheet (goal, verify command and the
//  last check's output), and the card for an agent's `start_loop_session`
//  proposal (shown inside UserQuestionSheet).
//

import SwiftUI

// MARK: - Strip

struct LoopStrip: View {
    let loop: LoopState
    let onStop: () -> Void
    let onOpen: () -> Void

    private var tint: Color {
        switch loop.phase {
        case .completed: return EggifyColors.success
        case .failed: return EggifyColors.error
        case .stopped: return .secondary
        case .iteration where loop.verifyPassed == false: return EggifyColors.warning
        default: return EggifyColors.accent
        }
    }

    var body: some View {
        HStack(spacing: 10) {
            Button(action: onOpen) {
                HStack(spacing: 8) {
                    if loop.phase == .verifying {
                        ProgressView().controlSize(.mini)
                    } else {
                        Image(systemName: loop.symbol)
                            .font(.footnote.weight(.semibold))
                            .foregroundStyle(tint)
                            .symbolEffect(.rotate, isActive: loop.phase == .started || loop.phase == .iteration)
                    }
                    VStack(alignment: .leading, spacing: 1) {
                        Text(loop.headline)
                            .font(.footnote.weight(.semibold))
                            .lineLimit(1)
                        if loop.maxIterations > 0, loop.isRunning {
                            ProgressView(value: Double(min(loop.current, loop.maxIterations)),
                                         total: Double(loop.maxIterations))
                                .tint(tint)
                                .frame(maxWidth: 140)
                        } else if !loop.detail.isEmpty {
                            Text(loop.detail)
                                .font(.caption2)
                                .foregroundStyle(.secondary)
                                .lineLimit(1)
                        }
                    }
                    Spacer(minLength: 4)
                    Image(systemName: "chevron.right")
                        .font(.caption2.weight(.bold))
                        .foregroundStyle(.secondary)
                }
                .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            .accessibilityLabel("\(loop.headline). Show loop details")

            if loop.isRunning {
                Button("Stop loop", role: .destructive, action: onStop)
                    .font(.footnote.weight(.semibold))
                    .buttonStyle(.bordered)
                    .controlSize(.small)
                    .tint(EggifyColors.error)
            }
        }
        .padding(.horizontal, 12)
        .padding(.vertical, 7)
        .background(.regularMaterial, in: RoundedRectangle(cornerRadius: 14, style: .continuous))
        .overlay(RoundedRectangle(cornerRadius: 14, style: .continuous).strokeBorder(tint.opacity(0.35), lineWidth: 1))
    }
}

// MARK: - Details

struct LoopDetailSheet: View {
    let loop: LoopState
    let onStop: () -> Void
    @Environment(\.dismiss) private var dismiss

    var body: some View {
        NavigationStack {
            List {
                Section {
                    Label(loop.headline, systemImage: loop.symbol)
                        .font(.headline)
                    if !loop.detail.isEmpty {
                        Text(loop.detail).foregroundStyle(.secondary)
                    }
                    if loop.maxIterations > 0 {
                        LabeledContent("Iterations", value: "\(loop.iteration) done · at most \(loop.maxIterations)")
                    }
                }
                if !loop.goal.isEmpty {
                    Section("Goal") {
                        Text(loop.goal).textSelection(.enabled)
                    }
                }
                if !loop.verifyCommand.isEmpty {
                    Section("Verify command") {
                        Text(loop.verifyCommand)
                            .font(.footnote.monospaced())
                            .textSelection(.enabled)
                    }
                }
                if !loop.verifyOutput.isEmpty {
                    Section {
                        ScrollView(.horizontal) {
                            Text(loop.verifyOutput)
                                .font(.caption.monospaced())
                                .textSelection(.enabled)
                                .fixedSize(horizontal: true, vertical: false)
                        }
                        .scrollIndicators(.visible)
                    } header: {
                        Text(loop.verifyPassed == true ? "Last check passed" : loop.verifyPassed == false ? "Last check failed" : "Last check")
                    }
                }
            }
            .navigationTitle("Loop")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .topBarTrailing) {
                    Button("Done") { dismiss() }
                }
                if loop.isRunning {
                    ToolbarItem(placement: .bottomBar) {
                        Button("Stop loop", role: .destructive) {
                            onStop()
                            dismiss()
                        }
                        .foregroundStyle(EggifyColors.error)
                    }
                }
            }
        }
        .presentationDetents([.medium, .large])
    }
}

// MARK: - Proposal card

/// An agent asks to start a loop session in YOLO mode. Start and Deny answer
/// it; changing the settings ("Edit first") needs the Create Session form on
/// the computer, so the phone keeps it waiting with Later.
struct LoopProposalView: View {
    let proposal: UserQuestion.LoopProposal
    let onAnswer: ([String]) -> Void
    let onLater: () -> Void

    var body: some View {
        NavigationStack {
            ScrollView {
                VStack(alignment: .leading, spacing: 16) {
                    HStack(spacing: 6) {
                        badge("YOLO", color: EggifyColors.error, filled: proposal.yolo)
                        badge(proposal.sandboxed ? "Sandboxed" : "Not sandboxed", color: .secondary, filled: false)
                    }

                    Text("Start a loop session?")
                        .font(.eggifySerifSemiBold(23, relativeTo: .title3))
                    Text("The agent wants to start a new session that keeps working, without asking permission for each step, until the check passes.")
                        .font(.footnote)
                        .foregroundStyle(.secondary)
                        .fixedSize(horizontal: false, vertical: true)

                    field("Goal") { Text(proposal.goal).textSelection(.enabled) }
                    if !proposal.verifyCommand.isEmpty {
                        field("Verify command") {
                            Text(proposal.verifyCommand)
                                .font(.footnote.monospaced())
                                .textSelection(.enabled)
                                .padding(10)
                                .frame(maxWidth: .infinity, alignment: .leading)
                                .background(EggifyColors.codeBackground, in: RoundedRectangle(cornerRadius: 8))
                        }
                    }
                    field("Folder") {
                        Text(proposal.workingDirectory).font(.footnote.monospaced()).textSelection(.enabled)
                    }
                    field("Model") { Text(ModelNames.short(proposal.model)) }
                    field("Limits") {
                        Text("At most \(proposal.maxIterations) iterations · \(proposal.timeoutMinutes) minutes")
                    }
                    if !proposal.context.isEmpty {
                        field("Notes from the agent") { Text(proposal.context).font(.footnote) }
                    }

                    Label("To change these settings first, use Edit first on your computer.", systemImage: "desktopcomputer")
                        .font(.footnote)
                        .foregroundStyle(.secondary)
                }
                .padding(20)
            }
            .safeAreaInset(edge: .bottom) {
                HStack(spacing: 12) {
                    Button(role: .destructive) {
                        onAnswer([UserQuestion.LoopProposal.deny])
                    } label: {
                        Text("Deny").font(.headline).frame(maxWidth: .infinity)
                    }
                    .buttonStyle(.bordered)
                    .controlSize(.large)

                    Button {
                        onAnswer([UserQuestion.LoopProposal.start])
                    } label: {
                        Label("Start loop", systemImage: "play.fill").font(.headline).frame(maxWidth: .infinity)
                    }
                    .buttonStyle(.borderedProminent)
                    .controlSize(.large)
                    .tint(EggifyColors.error)
                }
                .padding(.horizontal, 20)
                .padding(.bottom, 12)
                .background(.bar)
            }
            .navigationTitle("Loop session")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .topBarLeading) {
                    Button("Later", action: onLater)
                }
            }
        }
    }

    private func badge(_ text: String, color: Color, filled: Bool) -> some View {
        Text(text)
            .font(.caption2.weight(.bold))
            .padding(.horizontal, 8).padding(.vertical, 4)
            .background(filled ? color : color.opacity(0.12), in: Capsule())
            .foregroundStyle(filled ? Color.white : color)
    }

    private func field<Content: View>(_ title: String, @ViewBuilder _ content: () -> Content) -> some View {
        VStack(alignment: .leading, spacing: 4) {
            Text(title.uppercased())
                .font(.caption2.weight(.bold))
                .foregroundStyle(.secondary)
            content()
        }
    }
}
