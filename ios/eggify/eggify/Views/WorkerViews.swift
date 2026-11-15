//
//  WorkerViews.swift
//  eggify
//
//  Worker sessions and teams: the card for an agent's `start_worker_session`
//  proposal (shown inside UserQuestionSheet), the team strip above the
//  composer of the session that started the workers and its workers sheet,
//  the line under a worker chat's header, and a worker's row under its
//  parent in the Agents list.
//

import SwiftUI

// MARK: - Proposal card

/// An agent asks to start another session that builds or reviews for it.
/// Start and Deny answer it; Later keeps it waiting.
struct WorkerProposalView: View {
    let proposal: UserQuestion.WorkerProposal
    let onAnswer: ([String]) -> Void
    let onLater: () -> Void

    private var explanation: String {
        switch proposal.mode {
        case .build:
            return "The agent wants to hand this task to another session. It works on its own and reports back to the agent when it is done."
        case .review:
            return "The agent wants another session to review the work. It reads the code, changes nothing, and reports back with a verdict."
        }
    }

    var body: some View {
        NavigationStack {
            ScrollView {
                VStack(alignment: .leading, spacing: 16) {
                    badges

                    Text(proposal.title)
                        .font(.eggifySerifSemiBold(23, relativeTo: .title3))
                    Text(explanation)
                        .font(.footnote)
                        .foregroundStyle(.secondary)
                        .fixedSize(horizontal: false, vertical: true)

                    field("Task") { Text(proposal.task).textSelection(.enabled) }
                    if !proposal.context.isEmpty {
                        field("Notes from the agent") { Text(proposal.context).font(.footnote) }
                    }
                    if !proposal.workingDirectory.isEmpty {
                        field("Folder") {
                            Text(proposal.workingDirectory).font(.footnote.monospaced()).textSelection(.enabled)
                        }
                    }
                    if !proposal.worktreePath.isEmpty, proposal.worktreePath != proposal.workingDirectory {
                        field("Worktree") {
                            Text(proposal.worktreePath).font(.footnote.monospaced()).textSelection(.enabled)
                        }
                    }
                    if !proposal.model.isEmpty {
                        field("Model") {
                            Text(proposal.provider.isEmpty ? ModelNames.short(proposal.model)
                                 : "\(ModelNames.short(proposal.model)) · \(proposal.provider)")
                        }
                    }
                }
                .padding(20)
                .frame(maxWidth: .infinity, alignment: .leading)
            }
            .safeAreaInset(edge: .bottom) {
                // Two plain buttons, no default action: starting takes a tap on Start.
                HStack(spacing: 12) {
                    Button(role: .destructive) {
                        onAnswer([UserQuestion.WorkerProposal.deny])
                    } label: {
                        Text("Deny").font(.headline).frame(maxWidth: .infinity)
                    }
                    .buttonStyle(.bordered)
                    .controlSize(.large)

                    Button {
                        onAnswer([UserQuestion.WorkerProposal.start])
                    } label: {
                        Label("Start", systemImage: "play.fill").font(.headline).frame(maxWidth: .infinity)
                    }
                    .buttonStyle(.borderedProminent)
                    .controlSize(.large)
                    .tint(EggifyColors.accent)
                    .accessibilityLabel("Start the \(proposal.mode.rawValue) worker")
                }
                .padding(.horizontal, 20)
                .padding(.bottom, 12)
                .background(.bar)
            }
            .navigationTitle("\(proposal.mode.label) worker")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .topBarLeading) {
                    Button("Later", action: onLater)
                }
            }
        }
    }

    /// Wraps onto more lines with large text or long branch names.
    private var badges: some View {
        VStack(alignment: .leading, spacing: 6) {
            HStack(spacing: 6) {
                badge(proposal.sandboxed ? "Sandboxed" : "Not sandboxed",
                      color: proposal.sandboxed ? EggifyColors.success : EggifyColors.warning)
                if proposal.readOnly { badge("Read-only", color: .secondary) }
            }
            if !proposal.worktreeBadge.isEmpty { badge(proposal.worktreeBadge, color: EggifyColors.accent) }
            if !proposal.roundBadge.isEmpty { badge(proposal.roundBadge, color: EggifyColors.accent) }
        }
    }

    private func badge(_ text: String, color: Color) -> some View {
        Text(text)
            .font(.caption2.weight(.bold))
            .lineLimit(2)
            .padding(.horizontal, 8).padding(.vertical, 4)
            .background(color.opacity(0.12), in: RoundedRectangle(cornerRadius: 10, style: .continuous))
            .foregroundStyle(color)
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

// MARK: - Team strip

extension TeamProgress {
    var tint: Color {
        switch stage {
        case .approved: return EggifyColors.success
        case .stopped: return EggifyColors.error
        case .fixing: return EggifyColors.warning
        case .idle where lastVerdict == .changesRequested: return EggifyColors.warning
        case .planning, .idle: return .secondary
        case .building, .reviewing: return EggifyColors.accent
        }
    }
}

/// Where the session's workers are, above the composer; opens the workers sheet.
struct TeamStrip: View {
    let progress: TeamProgress
    let onOpen: () -> Void

    private var detail: String {
        let count = progress.workers.count
        return count == 0 ? "" : "\(count) worker\(count == 1 ? "" : "s")"
    }

    var body: some View {
        Button(action: onOpen) {
            HStack(spacing: 8) {
                if progress.isWorking {
                    ProgressView().controlSize(.mini)
                } else {
                    Image(systemName: progress.symbol)
                        .font(.footnote.weight(.semibold))
                        .foregroundStyle(progress.tint)
                }
                VStack(alignment: .leading, spacing: 1) {
                    Text(progress.headline)
                        .font(.footnote.weight(.semibold))
                        .lineLimit(1)
                    if !detail.isEmpty {
                        Text(detail)
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
        .padding(.horizontal, 12)
        .padding(.vertical, 7)
        .background(.regularMaterial, in: RoundedRectangle(cornerRadius: 14, style: .continuous))
        .overlay(RoundedRectangle(cornerRadius: 14, style: .continuous).strokeBorder(progress.tint.opacity(0.35), lineWidth: 1))
        .accessibilityLabel("Team: \(progress.headline)\(detail.isEmpty ? "" : ", \(detail)")")
        .accessibilityHint("Shows the workers")
    }
}

// MARK: - Workers sheet

/// The workers of a session; tapping one opens its chat.
struct TeamWorkersSheet: View {
    let progress: TeamProgress
    let team: TeamInfo?
    let onOpen: (TeamProgress.Member) -> Void
    @Environment(\.dismiss) private var dismiss

    var body: some View {
        NavigationStack {
            List {
                Section {
                    Label(progress.headline, systemImage: progress.symbol)
                        .font(.headline)
                        .foregroundStyle(progress.tint)
                    if progress.builder != nil, progress.round > 0 {
                        LabeledContent("Review rounds", value: "\(progress.round) of \(progress.maxRounds)")
                    }
                    if let verdict = progress.lastVerdict {
                        LabeledContent("Last verdict", value: verdict.label)
                    }
                }
                if let team {
                    Section("Team") {
                        LabeledContent("Builder", value: ModelNames.short(team.builder.model))
                        LabeledContent("Reviewer", value: ModelNames.short(team.reviewer.model))
                    }
                }
                Section(progress.workers.isEmpty ? "No workers yet" : "Workers") {
                    // Newest first: the ones at work now are at the top.
                    ForEach(progress.workers.reversed()) { worker in
                        Button {
                            onOpen(worker)
                            dismiss()
                        } label: {
                            workerRow(worker)
                        }
                        .buttonStyle(.plain)
                        .accessibilityLabel(accessibilityText(worker))
                        .accessibilityHint("Opens the worker's chat")
                    }
                }
            }
            .navigationTitle("Workers")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .topBarTrailing) {
                    Button("Done") { dismiss() }
                }
            }
        }
        .presentationDetents([.medium, .large])
    }

    private func workerRow(_ worker: TeamProgress.Member) -> some View {
        HStack(spacing: 10) {
            Image(systemName: worker.mode.symbol)
                .font(.footnote.weight(.semibold))
                .foregroundStyle(EggifyColors.accent)
                .frame(width: 22)
                .accessibilityHidden(true)
            VStack(alignment: .leading, spacing: 2) {
                Text(worker.roleLine)
                    .font(.subheadline.weight(.semibold))
                    .lineLimit(1)
                HStack(spacing: 5) {
                    StatusDot(status: worker.status)
                    Text(worker.branch.isEmpty ? worker.stateLine : "\(worker.stateLine) · \(worker.branch)")
                        .font(.caption)
                        .foregroundStyle(verdictColor(worker))
                        .lineLimit(1)
                        .truncationMode(.middle)
                }
            }
            Spacer(minLength: 4)
            Image(systemName: "chevron.right")
                .font(.caption2.weight(.bold))
                .foregroundStyle(.tertiary)
                .accessibilityHidden(true)
        }
        .contentShape(Rectangle())
    }

    private func verdictColor(_ worker: TeamProgress.Member) -> Color {
        guard !worker.isWorking else { return .secondary }
        switch worker.verdict {
        case .approve: return EggifyColors.success
        case .changesRequested: return EggifyColors.warning
        case nil: return .secondary
        }
    }

    private func accessibilityText(_ worker: TeamProgress.Member) -> String {
        var text = "\(worker.mode.label) worker"
        if !worker.model.isEmpty { text += ", \(worker.model)" }
        text += ", \(worker.stateLine)"
        if !worker.branch.isEmpty { text += ", branch \(worker.branch)" }
        return text
    }
}

// MARK: - Worker chat line

/// "Build worker of Shop · worker/abc", under a worker chat's header; tapping
/// it opens the parent's chat when that session is known.
struct WorkerChatLine: View {
    let text: String
    let mode: WorkerInfo.Mode
    /// nil when the parent is not in the session list.
    var onOpenParent: (() -> Void)? = nil

    var body: some View {
        Button {
            onOpenParent?()
        } label: {
            HStack(spacing: 6) {
                Image(systemName: mode.symbol)
                    .font(.caption2.weight(.semibold))
                    .accessibilityHidden(true)
                Text(text)
                    .font(.caption)
                    .lineLimit(1)
                    .truncationMode(.middle)
                Spacer(minLength: 4)
                if onOpenParent != nil {
                    Image(systemName: "chevron.right")
                        .font(.caption2.weight(.bold))
                        .accessibilityHidden(true)
                }
            }
            .foregroundStyle(.secondary)
            .padding(.horizontal, 16)
            .padding(.vertical, 6)
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .disabled(onOpenParent == nil)
        .background(.bar)
        .overlay(alignment: .bottom) {
            Rectangle().fill(EggifyColors.border).frame(height: 0.5)
        }
        .accessibilityLabel(text)
        .accessibilityHint(onOpenParent == nil ? "" : "Opens the session that started it")
    }
}

// MARK: - Home row

/// A worker under its parent's row in the Agents list: one indented line.
struct SessionWorkerRowView: View {
    let worker: SessionWorkerRow
    let parentTitle: String

    private var verdictColor: Color {
        guard !worker.isWorking, worker.session.worker?.mode == .build else { return .secondary }
        switch worker.session.worker?.lastVerdict {
        case .approve: return EggifyColors.success
        case .changesRequested: return EggifyColors.warning
        case nil: return .secondary
        }
    }

    var body: some View {
        HStack(spacing: 8) {
            Image(systemName: "arrow.turn.down.right")
                .font(.caption.weight(.semibold))
                .foregroundStyle(.tertiary)
            Image(systemName: worker.session.worker?.mode.symbol ?? "person.fill")
                .font(.caption2.weight(.semibold))
                .foregroundStyle(worker.isWorking ? EggifyColors.accent : .secondary)
            Text(worker.line)
                .font(.footnote)
                .foregroundStyle(verdictColor)
                .lineLimit(1)
                .truncationMode(.middle)
            Spacer(minLength: 4)
            if worker.isWorking {
                StatusDot(status: .processing)
            }
        }
        .padding(.leading, 30)
        .opacity(worker.status == .ended ? 0.6 : 1)
        .contentShape(Rectangle())
        .accessibilityElement(children: .ignore)
        .accessibilityLabel("Worker of \(parentTitle): \(worker.line.replacingOccurrences(of: " · ", with: ", "))")
    }
}
