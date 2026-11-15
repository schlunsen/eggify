//
//  SessionRowView.swift
//  eggify
//
//  One agent session in the Agents list. Full rows (Needs you, Working):
//  avatar, name, what it waits for, the AI summary (2 lines) and tags.
//  Compact rows (Today, Earlier): one line with name, tags and time, as the
//  web sidebar's `SessionItem.vue`.
//

import SwiftUI

struct SessionRowView: View {
    enum Variant { case full, compact }

    let session: AgentSession
    let status: SessionStatus
    /// Its row in the Agents list's groups (what it waits for, idle subagents).
    let row: SessionGroupRow
    var variant: Variant = .full
    /// The user's own tags and the AI topic tags.
    var tags: (own: [String], topics: [String]) = ([], [])
    /// The cached one-line AI summary, if any.
    var summary: String? = nil
    var projectName: String? = nil
    var now: Date = Date()

    @ObservedObject private var avatars = AvatarManager.shared

    private var avatar: Avatar? {
        session.selected_avatar ?? session.selected_avatar_id.flatMap { avatars.getAvatar(id: $0) }
    }

    /// Working while its subagents run, even when its own turn has ended.
    private var shownStatus: SessionStatus {
        status.including(subagentsRunning: session.subagents_running)
    }

    private var isWorking: Bool { row.group == .working || shownStatus.isActive }
    private var ended: Bool { status == .ended }

    var body: some View {
        Group {
            switch variant {
            case .full: fullRow
            case .compact: compactRow
            }
        }
        .contentShape(Rectangle())
        .accessibilityElement(children: .combine)
        .accessibilityLabel(accessibilityText)
    }

    // MARK: Full row (Needs you, Working)

    private var fullRow: some View {
        HStack(alignment: .top, spacing: 12) {
            avatarView(size: 42)

            VStack(alignment: .leading, spacing: 3) {
                HStack(spacing: 6) {
                    Text(session.displayTitle)
                        .font(.eggifySerifSemiBold(19))
                        .lineLimit(1)
                    badges
                    Spacer(minLength: 6)
                    timeLabel
                }

                if let statusLine {
                    statusLine
                }

                Text(description)
                    .font(.footnote)
                    .foregroundStyle(.secondary)
                    .lineLimit(hasStatusLine ? 1 : 2)

                if !tags.own.isEmpty || !tags.topics.isEmpty {
                    SessionTagChips(own: tags.own, topics: tags.topics, budget: 28, maxShown: 4)
                        .padding(.top, 2)
                }
            }
        }
        .padding(.vertical, 4)
    }

    private var hasStatusLine: Bool {
        !row.waitingFor.isEmpty || row.subagents > 0 || session.loop != nil || status == .error
    }

    /// What it waits for, that its subagents still work, its loop, or an error.
    private var statusLine: AnyView? {
        if !row.waitingFor.isEmpty {
            return AnyView(
                Label(row.waitingFor, systemImage: "hand.raised.fill")
                    .font(.footnote.weight(.semibold))
                    .foregroundStyle(EggifyColors.warning)
                    .lineLimit(1)
            )
        }
        if row.subagents > 0 {
            return AnyView(
                Text("Idle · \(row.subagents) subagent\(row.subagents == 1 ? "" : "s") working")
                    .font(.caption.weight(.semibold))
                    .foregroundStyle(EggifyColors.accent)
                    .lineLimit(1)
            )
        }
        if let loop = session.loop {
            return AnyView(
                Text(loop.badge)
                    .font(.caption.weight(.semibold))
                    .foregroundStyle(loopColor(loop))
                    .lineLimit(1)
            )
        }
        if status == .error {
            return AnyView(
                Text(session.error_message ?? "Error")
                    .font(.caption.weight(.semibold))
                    .foregroundStyle(EggifyColors.error)
                    .lineLimit(1)
            )
        }
        return nil
    }

    /// The AI summary, else where it runs (project · model · branch).
    private var description: String {
        if let summary, !summary.isEmpty { return summary }
        return subtitle
    }

    // MARK: Compact row (Today, Earlier)

    private var compactRow: some View {
        HStack(spacing: 10) {
            avatarView(size: 28)
                .opacity(ended ? 0.6 : 1)
            Text(session.displayTitle)
                .font(.eggifySerifSemiBold(17))
                .lineLimit(1)
                .layoutPriority(1)
                .opacity(ended ? 0.6 : 1)
            if !tags.own.isEmpty {
                // Full tag text while the row has room: ahead of the spacer, behind the name.
                SessionTagChips(own: tags.own, topics: [], budget: 20, maxShown: 3)
                    .layoutPriority(0.5)
            }
            if session.loop != nil {
                Image(systemName: "arrow.triangle.2.circlepath")
                    .font(.caption2)
                    .foregroundStyle(EggifyColors.accent)
            }
            Spacer(minLength: 6)
            timeLabel
        }
        .padding(.vertical, 1)
    }

    // MARK: Pieces

    private func avatarView(size: CGFloat) -> some View {
        AvatarView(
            image: avatar.flatMap { avatars.getAvatarImage(id: $0.id) },
            name: avatar?.name ?? session.displayTitle,
            color: avatarColor,
            size: size
        )
        .overlay(alignment: .bottomTrailing) {
            // The one "working" signal on the row (and errors).
            if isWorking || status == .error {
                StatusDot(status: isWorking ? .processing : .error)
                    .padding(2)
                    .background(Circle().fill(Color(.systemBackground)))
                    .offset(x: 2, y: 2)
            }
        }
        .task(id: avatar?.id) {
            if let avatar, avatars.getAvatarImage(id: avatar.id) == nil {
                await avatars.loadAvatarImageIfNeeded(avatar: avatar)
            }
        }
    }

    private var avatarColor: Color {
        avatar?.color?.parseHexColor() ?? .defaultAvatar
    }

    /// The colour of a session's avatar (the row's sheen uses it).
    static func avatarColor(for session: AgentSession) -> Color {
        let avatar = session.selected_avatar ?? session.selected_avatar_id.flatMap { AvatarManager.shared.getAvatar(id: $0) }
        return avatar?.color?.parseHexColor() ?? .defaultAvatar
    }

    @ViewBuilder private var badges: some View {
        if session.yolo {
            Image(systemName: "bolt.fill")
                .font(.caption2)
                .foregroundStyle(.orange)
                .accessibilityLabel("Auto-approve on")
        }
        if session.loop_mode {
            Image(systemName: "arrow.triangle.2.circlepath")
                .font(.caption2)
                .foregroundStyle(EggifyColors.accent)
                .accessibilityLabel("Loop session")
        }
        if session.sandboxed {
            Image(systemName: "shield.lefthalf.filled")
                .font(.caption2)
                .foregroundStyle(.secondary)
                .accessibilityLabel("Sandboxed")
        }
    }

    private var timeLabel: some View {
        Text(SessionGroups.relativeTime(row.activityAt, now: now))
            .font(.caption)
            .monospacedDigit()
            .foregroundStyle(.tertiary)
            .fixedSize()
    }

    private func loopColor(_ loop: LoopState) -> Color {
        switch loop.phase {
        case .completed: return EggifyColors.success
        case .failed: return EggifyColors.error
        case .stopped: return .secondary
        default: return EggifyColors.accent
        }
    }

    private var subtitle: String {
        var parts: [String] = []
        if let projectName { parts.append(projectName) }
        else if let dir = session.working_directory { parts.append((dir as NSString).lastPathComponent) }
        if let model = session.model_name { parts.append(ModelNames.short(model)) }
        if let branch = session.git_branch { parts.append(branch) }
        return parts.joined(separator: " · ")
    }

    private var accessibilityText: String {
        var text = "\(session.displayTitle), \(shownStatus.label)"
        if !row.waitingFor.isEmpty { text += ", needs you: \(row.waitingFor)" }
        if let loop = session.loop { text += ", \(loop.badge)" }
        if variant == .full, let summary, !summary.isEmpty { text += ", \(summary)" }
        let allTags = tags.own + (variant == .full ? tags.topics : [])
        if !allTags.isEmpty { text += ", tags: \(allTags.joined(separator: ", "))" }
        return text
    }
}

/// A list row's background: the card colour, plus a slow translucent sheen
/// in the avatar's colour drifting across while the session works (the web
/// sidebar's working sweep, ~4.5 s). Off with Reduce Motion; the animation
/// lives only while the row is on screen.
struct SessionRowBackground: View {
    var working: Bool
    var color: Color

    @Environment(\.accessibilityReduceMotion) private var reduceMotion
    @State private var sweep = false

    var body: some View {
        EggifyColors.surface
            .overlay {
                if working && !reduceMotion {
                    GeometryReader { geo in
                        let band = geo.size.width * 0.45
                        LinearGradient(
                            colors: [color.opacity(0), color.opacity(0.13), color.opacity(0)],
                            startPoint: .leading, endPoint: .trailing
                        )
                        .frame(width: band)
                        .offset(x: sweep ? geo.size.width : -band)
                    }
                    .allowsHitTesting(false)
                    .onAppear {
                        sweep = false
                        withAnimation(.linear(duration: 4.5).repeatForever(autoreverses: false)) { sweep = true }
                    }
                    .onDisappear { sweep = false }
                    .transition(.opacity)
                }
            }
            .clipped()
    }
}

struct AvatarView: View {
    let image: UIImage?
    let name: String
    let color: Color
    var size: CGFloat = 32

    var body: some View {
        ZStack {
            Circle()
                .fill(color.opacity(image == nil ? 0.9 : 1))
            if let image {
                Image(uiImage: image)
                    .resizable()
                    .scaledToFill()
                    .clipShape(Circle())
            } else {
                Text(name.initials)
                    .foregroundStyle(.white)
                    .font(.system(size: size * 0.38, weight: .semibold))
            }
        }
        .frame(width: size, height: size)
        .accessibilityHidden(true)
    }
}

/// A row's tags on one line: the user's own as chips coloured by the tag's
/// hue (`TagColor`, the same as on the desktop), extra ones folded into "+N",
/// then the AI topic tags (outlined, neutral) while they fit.
struct SessionTagChips: View {
    let own: [String]
    let topics: [String]
    var budget: Int = 26
    var maxShown: Int = 4

    var body: some View {
        let fitted = TagColor.fit(own, budget: budget, maxShown: maxShown)
        HStack(spacing: 4) {
            ForEach(Array(fitted.shown.enumerated()), id: \.offset) { _, tag in
                TagChip(tag: tag)
            }
            if !fitted.hidden.isEmpty {
                Text("+\(fitted.hidden.count)")
                    .font(.caption2.weight(.semibold))
                    .padding(.horizontal, 6)
                    .padding(.vertical, 2)
                    .background(Color.primary.opacity(0.1), in: Capsule())
                    .foregroundStyle(.secondary)
                    .fixedSize()
            }
            if !topics.isEmpty {
                // Topics fill what is left, as many as fit.
                ViewThatFits(in: .horizontal) {
                    ForEach((1...topics.count).reversed(), id: \.self) { count in
                        topicChips(Array(topics.prefix(count))).fixedSize()
                    }
                    Color.clear.frame(width: 0, height: 0)
                }
            }
        }
        .accessibilityHidden(true)
    }

    private func topicChips(_ tags: [String]) -> some View {
        HStack(spacing: 4) {
            ForEach(Array(tags.enumerated()), id: \.offset) { _, tag in
                Text(tag)
                    .font(.caption2)
                    .lineLimit(1)
                    .padding(.horizontal, 7)
                    .padding(.vertical, 2)
                    .overlay(Capsule().strokeBorder(EggifyColors.border, lineWidth: 0.5))
                    .foregroundStyle(.secondary)
            }
        }
    }
}

/// One of the user's tags, in its own colour.
struct TagChip: View {
    let tag: String
    var font: Font = .caption2.weight(.semibold)

    var body: some View {
        let colors = TagChipColors(tag: tag)
        Text(tag)
            .font(font)
            .lineLimit(1)
            .truncationMode(.tail)
            .padding(.horizontal, 7)
            .padding(.vertical, 2)
            .background(colors.fill, in: Capsule())
            .overlay(Capsule().strokeBorder(colors.border, lineWidth: 0.75))
            .foregroundStyle(colors.text)
    }
}
