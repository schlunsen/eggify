//
//  MessageBubbleView.swift
//  eggify
//
//  Chat rows: user bubbles, full-width agent replies (Markdown, thinking,
//  tool calls), system notices, turn summaries and folded tool activity.
//

import SwiftUI
import UIKit

/// Equatable on the item only (the callbacks never change what is drawn), so
/// SwiftUI skips rows whose message did not change while the list updates.
struct ChatItemView: View, Equatable {
    let item: ChatItem
    let onShowDetails: (AgentMessage) -> Void
    let onImageTap: (ImageBlockData) -> Void

    var body: some View {
        switch item {
        case .message(let message):
            switch message.role {
            case "user":
                UserMessageView(message: message, onShowDetails: onShowDetails, onImageTap: onImageTap)
            case "system":
                NoticeView(text: message.content.textContent, kind: .info)
            case "error":
                NoticeView(text: message.content.textContent, kind: .error)
            case "result":
                TurnSummaryView(summary: message.turnSummary)
            default:
                AssistantMessageView(message: message, onShowDetails: onShowDetails, onImageTap: onImageTap)
            }
        case .activity(_, let messages):
            ActivityGroupView(messages: messages)
        }
    }

    nonisolated static func == (lhs: ChatItemView, rhs: ChatItemView) -> Bool {
        lhs.item == rhs.item
    }
}

// MARK: - User

struct UserMessageView: View {
    let message: AgentMessage
    let onShowDetails: (AgentMessage) -> Void
    let onImageTap: (ImageBlockData) -> Void
    @State private var expanded = false

    private var text: String { message.content.textContent }
    private var isLong: Bool { text.count > 600 || text.lineCount() > 14 }

    var body: some View {
        HStack {
            Spacer(minLength: 40)
            VStack(alignment: .trailing, spacing: 6) {
                let images = message.content.imageBlocks
                if !images.isEmpty {
                    HStack(spacing: 6) {
                        ForEach(images) { block in
                            Base64ImageView(block: block, maxWidth: images.count > 1 ? 120 : 220, maxHeight: 220)
                                .onTapGesture { onImageTap(block) }
                        }
                    }
                }
                if !text.isEmpty {
                    VStack(alignment: .leading, spacing: 4) {
                        Text(text)
                            .font(.eggifySerif())
                            .foregroundStyle(.white)
                            .lineLimit(expanded || !isLong ? nil : 14)
                            .textSelection(.enabled)
                        if isLong {
                            Button(expanded ? "Show less" : "Show more") {
                                withAnimation(.snappy) { expanded.toggle() }
                            }
                            .font(.caption.weight(.semibold))
                            .foregroundStyle(.white.opacity(0.85))
                        }
                    }
                    .padding(.horizontal, 14)
                    .padding(.vertical, 10)
                    .background(EggifyGradients.userBubble, in: RoundedRectangle(cornerRadius: 20, style: .continuous))
                    .messageContextMenu(message, onShowDetails: onShowDetails)
                }
                if message.isPending {
                    Label("Sending", systemImage: "clock")
                        .font(.caption2)
                        .foregroundStyle(.secondary)
                        .labelStyle(.titleAndIcon)
                } else {
                    MessageStamp(date: message.createdDate)
                }
            }
        }
        .accessibilityElement(children: .combine)
        .accessibilityLabel("You: \(text)")
    }
}

// MARK: - Assistant

struct AssistantMessageView: View {
    let message: AgentMessage
    let onShowDetails: (AgentMessage) -> Void
    let onImageTap: (ImageBlockData) -> Void

    var body: some View {
        VStack(alignment: .leading, spacing: 10) {
            if let thinking = message.thinking, !thinking.isEmpty {
                ThinkingDisclosure(text: thinking)
            }

            let text = message.content.textContent.trimmingCharacters(in: .whitespacesAndNewlines)
            if !text.isEmpty {
                MarkdownView(text: text)
                    .messageContextMenu(message, onShowDetails: onShowDetails)
            }

            let tools = message.getTools()
            if !tools.isEmpty {
                ToolCallList(tools: tools)
            }

            // Audio/video the calls made or showed, playable on the phone.
            ForEach(message.media) { item in
                MediaCardView(item: item)
            }

            // Images the tools returned (Read on a screenshot), under the calls.
            if !message.toolImages.isEmpty {
                ScrollView(.horizontal, showsIndicators: false) {
                    HStack(spacing: 8) {
                        ForEach(message.toolImages) { block in
                            Base64ImageView(block: block, maxWidth: 220, maxHeight: 260)
                                .onTapGesture { onImageTap(block) }
                        }
                    }
                }
            }

            // Only replies get a stamp; tool-only steps would repeat it on every row.
            if !text.isEmpty {
                MessageStamp(date: message.createdDate)
            }
        }
        .frame(maxWidth: .infinity, alignment: .leading)
    }
}

/// When a message was sent, e.g. "19:02" or "Yesterday 19:02".
struct MessageStamp: View {
    let date: Date?

    var body: some View {
        if let date {
            Text(EggifyDate.messageStamp(date))
                .font(.caption2)
                .monospacedDigit()
                .foregroundStyle(.tertiary)
                .accessibilityLabel(date.formatted(date: .long, time: .shortened))
        }
    }
}

struct ThinkingDisclosure: View {
    let text: String
    @State private var expanded = false

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            Button {
                withAnimation(.snappy) { expanded.toggle() }
            } label: {
                HStack(spacing: 6) {
                    Image(systemName: "brain")
                    Text("Thinking")
                    Image(systemName: "chevron.right")
                        .font(.caption2.weight(.bold))
                        .rotationEffect(.degrees(expanded ? 90 : 0))
                }
                .font(.footnote.weight(.medium))
                .foregroundStyle(.secondary)
            }
            .buttonStyle(.plain)
            .accessibilityHint(expanded ? "Hide the agent's reasoning" : "Show the agent's reasoning")

            if expanded {
                Text(text)
                    .font(.custom(EggifyFonts.serifItalic, size: 16, relativeTo: .footnote))
                    .foregroundStyle(.secondary)
                    .textSelection(.enabled)
                    .padding(.leading, 10)
                    .overlay(alignment: .leading) {
                        Rectangle().fill(Color.secondary.opacity(0.3)).frame(width: 2)
                    }
            }
        }
    }
}

/// Several tool-only steps folded into one row.
struct ActivityGroupView: View {
    let messages: [AgentMessage]
    @State private var expanded = false

    private var tools: [ToolUseInfo] { messages.flatMap { $0.getTools() } }

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            Button {
                withAnimation(.snappy) { expanded.toggle() }
            } label: {
                HStack(spacing: 8) {
                    Image(systemName: "gearshape.2")
                        .foregroundStyle(.secondary)
                    Text("\(tools.count) steps")
                        .font(.footnote.weight(.semibold))
                    Text(summary)
                        .font(.footnote)
                        .foregroundStyle(.secondary)
                        .lineLimit(1)
                    Spacer(minLength: 4)
                    Image(systemName: "chevron.right")
                        .font(.caption2.weight(.bold))
                        .foregroundStyle(.tertiary)
                        .rotationEffect(.degrees(expanded ? 90 : 0))
                }
                .padding(.horizontal, 12)
                .padding(.vertical, 9)
                .background(EggifyColors.surface, in: RoundedRectangle(cornerRadius: 12, style: .continuous))
                .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            .accessibilityLabel("\(tools.count) tool steps: \(summary)")

            if expanded {
                ToolCallList(tools: tools)
                    .padding(.leading, 8)
            }
        }
    }

    /// e.g. "Read ×4, Bash ×2, Edit"
    private var summary: String {
        var counts: [(String, Int)] = []
        for tool in tools {
            if let i = counts.firstIndex(where: { $0.0 == tool.displayName }) {
                counts[i].1 += 1
            } else {
                counts.append((tool.displayName, 1))
            }
        }
        return counts.map { $0.1 > 1 ? "\($0.0) ×\($0.1)" : $0.0 }.joined(separator: ", ")
    }
}

// MARK: - Notices

struct NoticeView: View {
    enum Kind { case info, error }
    let text: String
    let kind: Kind

    var body: some View {
        HStack(alignment: .top, spacing: 8) {
            Image(systemName: kind == .error ? "exclamationmark.triangle.fill" : "info.circle")
                .foregroundStyle(kind == .error ? EggifyColors.error : .secondary)
            Text(text)
                .font(.custom(EggifyFonts.serifItalic, size: 16, relativeTo: .footnote))
                .foregroundStyle(kind == .error ? .primary : .secondary)
                .textSelection(.enabled)
                .fixedSize(horizontal: false, vertical: true)
        }
        .padding(.horizontal, 12)
        .padding(.vertical, 8)
        .frame(maxWidth: .infinity, alignment: kind == .error ? .leading : .center)
        .background(
            kind == .error ? EggifyColors.error.opacity(0.1) : Color.clear,
            in: RoundedRectangle(cornerRadius: 12, style: .continuous)
        )
    }
}

struct TurnSummaryView: View {
    let summary: AgentMessage.TurnSummary?

    var body: some View {
        HStack(spacing: 6) {
            Image(systemName: "checkmark.circle")
            Text(parts.joined(separator: " · "))
        }
        .font(.caption)
        .foregroundStyle(.tertiary)
        .frame(maxWidth: .infinity, alignment: .leading)
        .accessibilityLabel("Turn finished. \(parts.joined(separator: ", "))")
    }

    private var parts: [String] {
        var result = ["Done"]
        if let ms = summary?.durationMs, ms > 0 {
            let seconds = Double(ms) / 1000
            result.append(seconds < 60 ? String(format: "%.0fs", seconds) : String(format: "%.0fm %.0fs", (seconds / 60).rounded(.down), seconds.truncatingRemainder(dividingBy: 60)))
        }
        if let cost = summary?.costUSD, cost > 0 {
            result.append(String(format: "$%.3f", cost))
        }
        return result
    }
}

// MARK: - Context menu

struct MessageMenu: View {
    let message: AgentMessage
    let onShowDetails: (AgentMessage) -> Void
    let onSelectText: () -> Void

    var body: some View {
        Button {
            UIPasteboard.general.string = SelectableText.plain(message.content.textContent)
            EggifyHaptics.success()
        } label: {
            Label("Copy", systemImage: "doc.on.doc")
        }
        Button(action: onSelectText) {
            Label("Select Text", systemImage: "selection.pin.in.out")
        }
        ShareLink(item: message.content.textContent) {
            Label("Share", systemImage: "square.and.arrow.up")
        }
        Button {
            onShowDetails(message)
        } label: {
            Label("Details", systemImage: "info.circle")
        }
    }
}
