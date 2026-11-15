//
//  UsageViews.swift
//  eggify
//
//  The account's plan usage: a slim line under the chat header for the
//  runtime the session bills to (Codex's for a Codex session, else Claude's),
//  a details sheet behind it, and the rows of Settings → Usage.
//

import SwiftUI

extension UsageLevel {
    var color: Color {
        switch self {
        case .calm: return EggifyColors.textSecondary
        case .warn: return EggifyColors.warning
        case .limited: return EggifyColors.error
        }
    }

    var icon: String {
        switch self {
        case .calm: return "gauge.with.dots.needle.33percent"
        case .warn: return "gauge.with.dots.needle.67percent"
        case .limited: return "exclamationmark.octagon"
        }
    }
}

/// The open session's plan usage under the header. Hidden while nothing is
/// known; it still keeps the usage polled while the chat is on screen.
struct ChatUsageLine: View {
    let provider: String?
    let model: String?
    @ObservedObject private var store = UsageLimitStore.shared
    @State private var showDetails = false

    private var isCodex: Bool { UsageText.isCodexRuntime(provider: provider, model: model) }

    var body: some View {
        TimelineView(.everyMinute) { context in
            if let usage = SessionUsage.make(isCodex: isCodex, claude: store.claude, codex: store.codex, now: context.date) {
                line(usage)
            } else {
                Color.clear.frame(height: 0)
            }
        }
        .usesPlanUsage(wantsCodex: isCodex)
        .sheet(isPresented: $showDetails) {
            UsageDetailSheet(isCodex: isCodex)
                .presentationDetents([.medium, .large])
                .presentationDragIndicator(.visible)
        }
    }

    private func line(_ usage: SessionUsage) -> some View {
        Button { showDetails = true } label: {
            HStack(spacing: 6) {
                Image(systemName: usage.level.icon)
                Text(usage.text)
                    .lineLimit(1)
                    .truncationMode(.tail)
                Spacer(minLength: 0)
            }
            .font(.footnote.weight(usage.level == .calm ? .regular : .semibold))
            .foregroundStyle(usage.level.color)
            .padding(.horizontal, 16)
            .padding(.vertical, 5)
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .background(.bar)
        .overlay(alignment: .bottom) {
            Rectangle().fill(EggifyColors.border).frame(height: 0.5)
        }
        .accessibilityLabel("\(usage.isCodex ? "Codex" : "Claude") usage: \(usage.text)")
        .accessibilityHint("Shows the details")
    }
}

/// Details behind the chat's usage line.
struct UsageDetailSheet: View {
    let isCodex: Bool
    @Environment(\.dismiss) private var dismiss

    var body: some View {
        NavigationStack {
            List {
                if isCodex {
                    CodexUsageSection()
                } else {
                    ClaudeUsageSection()
                }
            }
            .scrollContentBackground(.hidden)
            .background(EggifyColors.background)
            .navigationTitle("Plan usage")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .confirmationAction) {
                    Button("Done") { dismiss() }
                }
            }
        }
        .usesPlanUsage(wantsCodex: isCodex)
    }
}

/// Claude's usage rows (a List section).
struct ClaudeUsageSection: View {
    var header = "Claude"
    var footer: String? = nil
    @ObservedObject private var store = UsageLimitStore.shared

    var body: some View {
        Section {
            TimelineView(.everyMinute) { context in
                let now = context.date
                if let info = store.claude {
                    VStack(alignment: .leading, spacing: 4) {
                        let summary = UsageText.claudeSummary(info, now: now)
                        Text(summary.isEmpty ? "Within your limits" : summary)
                            .foregroundStyle(info.level == .calm ? EggifyColors.textPrimary : info.level.color)
                        if let detail = claudeDetail(info, now: now) {
                            Text(detail)
                                .font(.caption)
                                .foregroundStyle(.secondary)
                        }
                    }
                    .padding(.vertical, 2)
                } else {
                    Text(store.loaded ? "Not reported yet" : "Loading…")
                        .foregroundStyle(.secondary)
                }
            }
        } header: {
            Text(header)
        } footer: {
            if let footer {
                Text(footer).font(.caption)
            }
        }
    }

    private func claudeDetail(_ info: UsageLimitInfo, now: Date) -> String? {
        var parts: [String] = []
        let kind = UsageText.limitTypeLabel(info.limitType)
        if !kind.isEmpty { parts.append(kind.prefix(1).uppercased() + kind.dropFirst()) }
        if let resets = info.resetsAt, resets > now {
            parts.append("resets \(UsageText.clockText(resets, now: now))")
        }
        if let observed = info.observedAt {
            parts.append("reported \(EggifyDate.relative(observed))")
        }
        return parts.isEmpty ? nil : parts.joined(separator: " · ")
    }
}

/// Codex's usage rows (a List section): every window and the plan.
struct CodexUsageSection: View {
    var header = "Codex"
    var footer: String? = nil
    @ObservedObject private var store = UsageLimitStore.shared

    var body: some View {
        Section {
            TimelineView(.everyMinute) { context in
                let now = context.date
                if let info = store.codex, !info.windows.isEmpty {
                    VStack(alignment: .leading, spacing: 6) {
                        ForEach(Array(info.windows.enumerated()), id: \.offset) { _, window in
                            // Codex says the limit is reached, not which window: blame the tightest.
                            let reached = window.usedPercent >= 100 || (info.limitReached && window == info.tightestWindow)
                            let level = UsageLevel(percent: window.usedPercent, reached: reached)
                            Text(UsageText.codexWindowSummary(window, reached: reached, now: now))
                                .foregroundStyle(level == .calm ? EggifyColors.textPrimary : level.color)
                        }
                        if let detail = codexDetail(info) {
                            Text(detail)
                                .font(.caption)
                                .foregroundStyle(.secondary)
                        }
                    }
                    .padding(.vertical, 2)
                } else {
                    Text(store.codexAsked ? "Not reported yet" : "Loading…")
                        .foregroundStyle(.secondary)
                }
            }
        } header: {
            Text(header)
        } footer: {
            if let footer {
                Text(footer).font(.caption)
            }
        }
    }

    private func codexDetail(_ info: CodexUsageInfo) -> String? {
        var parts: [String] = []
        if let plan = info.planType { parts.append("\(plan.prefix(1).uppercased() + plan.dropFirst()) plan") }
        if let observed = info.observedAt { parts.append("reported \(EggifyDate.relative(observed))") }
        return parts.isEmpty ? nil : parts.joined(separator: " · ")
    }
}
