//
//  ToolCallViews.swift
//  eggify
//
//  How tool calls look in the chat: a compact row per call that expands to
//  show what it did (the command, the edit as a diff, the file written, the
//  todo list, ...). The same detail view is reused by approval cards.
//

import SwiftUI

// MARK: - Tool list inside a message

struct ToolCallList: View {
    let tools: [ToolUseInfo]

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            ForEach(tools) { tool in
                ToolCallRow(tool: tool)
            }
        }
    }
}

struct ToolCallRow: View {
    let tool: ToolUseInfo
    @State private var expanded = false

    private var tint: Color { ToolStyle.tint(for: tool.name) }

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            Button {
                withAnimation(.snappy(duration: 0.22)) { expanded.toggle() }
            } label: {
                HStack(spacing: 8) {
                    Image(systemName: tool.symbol)
                        .font(.footnote.weight(.semibold))
                        .foregroundStyle(tint)
                        .frame(width: 20)
                    Text(tool.displayName)
                        .font(.footnote.weight(.semibold))
                        .foregroundStyle(.primary)
                    if !tool.summary.isEmpty {
                        Text(tool.summary)
                            .font(.eggifyMono(13))
                            .foregroundStyle(.secondary)
                            .lineLimit(1)
                            .truncationMode(.middle)
                    }
                    Spacer(minLength: 4)
                    Image(systemName: "chevron.right")
                        .font(.caption2.weight(.bold))
                        .foregroundStyle(.tertiary)
                        .rotationEffect(.degrees(expanded ? 90 : 0))
                }
                .padding(.horizontal, 10)
                .padding(.vertical, 8)
                .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            .accessibilityLabel("\(tool.displayName) \(tool.summary)")
            .accessibilityHint(expanded ? "Collapse details" : "Show details")

            if expanded {
                Divider().padding(.horizontal, 10)
                ToolDetailView(tool: tool)
                    .padding(10)
                    .transition(.opacity)
            }
        }
        .background(tint.opacity(0.07))
        .clipShape(RoundedRectangle(cornerRadius: 10, style: .continuous))
        .overlay(
            RoundedRectangle(cornerRadius: 10, style: .continuous)
                .strokeBorder(tint.opacity(0.18), lineWidth: 0.5)
        )
    }
}

enum ToolStyle {
    static func tint(for name: String) -> Color {
        switch name.lowercased() {
        case "bash": return .green
        case "read", "glob", "grep": return .blue
        case "edit", "multiedit", "write", "notebookedit": return .orange
        case "webfetch", "websearch": return .teal
        case "task", "agent": return .purple
        case "todowrite": return .indigo
        default: return name.hasPrefix("mcp__") ? .pink : .gray
        }
    }
}

// MARK: - Tool detail

struct ToolDetailView: View {
    let tool: ToolUseInfo
    var maxCodeLines = 40

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            switch tool.name.lowercased() {
            case "bash":
                if let description = tool.bashDescription, !description.isEmpty {
                    Text(description).font(.footnote).foregroundStyle(.secondary)
                }
                CodeBlockView(language: "shell", code: tool.bashCommand ?? "")

            case "edit", "multiedit":
                pathLabel
                ForEach(Array(tool.edits.enumerated()), id: \.offset) { _, edit in
                    DiffView(old: edit.old, new: edit.new)
                }

            case "write":
                pathLabel
                CodeBlockView(language: fileExtension, code: limited(tool.writeContent ?? ""))

            case "todowrite":
                VStack(alignment: .leading, spacing: 6) {
                    ForEach(Array(tool.todos.enumerated()), id: \.offset) { _, todo in
                        HStack(alignment: .firstTextBaseline, spacing: 8) {
                            Image(systemName: todo.status == "completed" ? "checkmark.circle.fill"
                                  : todo.status == "in_progress" ? "circle.dotted.circle" : "circle")
                                .foregroundStyle(todo.status == "completed" ? EggifyColors.success
                                                 : todo.status == "in_progress" ? EggifyColors.accent : .secondary)
                            Text(todo.content)
                                .font(.footnote)
                                .strikethrough(todo.status == "completed", color: .secondary)
                                .foregroundStyle(todo.status == "completed" ? .secondary : .primary)
                        }
                    }
                }

            case "read":
                pathLabel
                if let limit = tool.readLimit {
                    Text("Lines: \(tool.input?["offset"]?.intValue ?? 1)–\((tool.input?["offset"]?.intValue ?? 1) + limit)")
                        .font(.caption).foregroundStyle(.secondary)
                }

            case "grep", "glob":
                keyValue("Pattern", tool.inputString("pattern"))
                keyValue("In", tool.inputString("path"))
                keyValue("Files", tool.inputString("glob"))

            case "task", "agent":
                keyValue("Agent", tool.inputString("subagent_type"))
                if let prompt = tool.inputString("prompt") {
                    Text(prompt).font(.footnote).foregroundStyle(.secondary).lineLimit(12)
                }

            default:
                if let input = tool.input, input != .null {
                    CodeBlockView(language: "json", code: limited(input.displayText))
                } else {
                    Text("No input").font(.footnote).foregroundStyle(.secondary)
                }
            }
        }
        .frame(maxWidth: .infinity, alignment: .leading)
    }

    @ViewBuilder
    private var pathLabel: some View {
        if let path = tool.filePath {
            Label {
                Text(path).font(.eggifyMono(12, relativeTo: .caption)).lineLimit(2).truncationMode(.head)
            } icon: {
                Image(systemName: "doc").font(.caption)
            }
            .foregroundStyle(.secondary)
            .textSelection(.enabled)
        }
    }

    @ViewBuilder
    private func keyValue(_ key: String, _ value: String?) -> some View {
        if let value, !value.isEmpty {
            HStack(alignment: .firstTextBaseline, spacing: 6) {
                Text(key).font(.caption.weight(.semibold)).foregroundStyle(.secondary)
                Text(value).font(.eggifyMono(13)).textSelection(.enabled)
            }
        }
    }

    private var fileExtension: String? {
        tool.filePath.map { ($0 as NSString).pathExtension }.flatMap { $0.isEmpty ? nil : $0 }
    }

    private func limited(_ text: String) -> String {
        let lines = text.components(separatedBy: "\n")
        guard lines.count > maxCodeLines else { return text }
        return lines.prefix(maxCodeLines).joined(separator: "\n") + "\n… \(lines.count - maxCodeLines) more lines"
    }
}

// MARK: - Diff

nonisolated enum LineDiff {
    enum Kind: Equatable { case same, removed, added }
    struct Line: Equatable { let kind: Kind; let text: String }

    /// Line diff via LCS; falls back to "all removed, all added" for big inputs.
    static func diff(old: String, new: String, limit: Int = 300) -> [Line] {
        let a = old.isEmpty ? [] : old.components(separatedBy: "\n")
        let b = new.isEmpty ? [] : new.components(separatedBy: "\n")
        guard a.count <= limit, b.count <= limit else {
            return a.map { Line(kind: .removed, text: $0) } + b.map { Line(kind: .added, text: $0) }
        }
        var lcs = Array(repeating: Array(repeating: 0, count: b.count + 1), count: a.count + 1)
        for i in stride(from: a.count - 1, through: 0, by: -1) {
            for j in stride(from: b.count - 1, through: 0, by: -1) {
                lcs[i][j] = a[i] == b[j] ? lcs[i + 1][j + 1] + 1 : max(lcs[i + 1][j], lcs[i][j + 1])
            }
        }
        var result: [Line] = []
        var i = 0, j = 0
        while i < a.count, j < b.count {
            if a[i] == b[j] {
                result.append(Line(kind: .same, text: a[i])); i += 1; j += 1
            } else if lcs[i + 1][j] >= lcs[i][j + 1] {
                result.append(Line(kind: .removed, text: a[i])); i += 1
            } else {
                result.append(Line(kind: .added, text: b[j])); j += 1
            }
        }
        while i < a.count { result.append(Line(kind: .removed, text: a[i])); i += 1 }
        while j < b.count { result.append(Line(kind: .added, text: b[j])); j += 1 }
        return result
    }
}

extension LineDiff {
    private final class Box { let lines: [Line]; init(_ lines: [Line]) { self.lines = lines } }
    private static let cache: NSCache<NSString, Box> = {
        let cache = NSCache<NSString, Box>()
        cache.countLimit = 64
        return cache
    }()

    /// `diff(old:new:)`, remembered: the LCS table is up to 300×300 and views
    /// showing a diff (the approval card, Edit tool rows) re-render often.
    @MainActor static func cached(old: String, new: String) -> [Line] {
        var hasher = Hasher()
        hasher.combine(old)
        hasher.combine(new)
        let key = "\(hasher.finalize())-\(old.utf8.count)-\(new.utf8.count)" as NSString
        if let hit = cache.object(forKey: key) { return hit.lines }
        let lines = diff(old: old, new: new)
        cache.setObject(Box(lines), forKey: key)
        return lines
    }
}

struct DiffView: View {
    let old: String
    let new: String
    var maxLines = 60

    var body: some View {
        let lines = LineDiff.cached(old: old, new: new)
        let shown = lines.prefix(maxLines)
        ScrollView(.horizontal, showsIndicators: false) {
            VStack(alignment: .leading, spacing: 0) {
                ForEach(Array(shown.enumerated()), id: \.offset) { _, line in
                    HStack(spacing: 6) {
                        Text(line.kind == .added ? "+" : line.kind == .removed ? "−" : " ")
                            .foregroundStyle(color(line.kind))
                            .frame(width: 10)
                        Text(line.text.isEmpty ? " " : line.text)
                            .foregroundStyle(line.kind == .same ? Color.secondary : Color.primary)
                    }
                    .font(.eggifyMono(12, relativeTo: .caption))
                    .padding(.horizontal, 8)
                    .padding(.vertical, 1)
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .background(color(line.kind).opacity(line.kind == .same ? 0 : 0.12))
                }
                if lines.count > maxLines {
                    Text("… \(lines.count - maxLines) more lines")
                        .font(.caption2).foregroundStyle(.secondary).padding(8)
                }
            }
            .padding(.vertical, 6)
            .textSelection(.enabled)
        }
        .background(EggifyColors.codeBackground)
        .clipShape(RoundedRectangle(cornerRadius: 8, style: .continuous))
        .accessibilityElement(children: .ignore)
        .accessibilityLabel("Edit: \(lines.filter { $0.kind == .removed }.count) lines removed, \(lines.filter { $0.kind == .added }.count) lines added")
    }

    private func color(_ kind: LineDiff.Kind) -> Color {
        switch kind {
        case .same: return .clear
        case .removed: return .red
        case .added: return .green
        }
    }
}

// MARK: - Approval card

/// A tool call the agent is waiting on. Pinned above the composer.
struct PermissionRequestCard: View {
    let request: PermissionRequest
    let position: Int
    let total: Int
    /// False while disconnected: an answer can't reach the server then, and
    /// the server sends the request again when the app reconnects.
    var isConnected = true
    /// Approve and keep approving: `true` = requests like this one,
    /// `false` = exactly this request. Nil hides the option.
    var onAlwaysAllow: ((Bool) -> Void)? = nil
    let onRespond: (Bool) -> Void
    @State private var showDetails = true
    @State private var detailHeight: CGFloat = 0

    var body: some View {
        VStack(alignment: .leading, spacing: 10) {
            HStack(spacing: 8) {
                Image(systemName: "hand.raised.fill")
                    .foregroundStyle(EggifyColors.warning)
                Text("Approval needed")
                    .font(.subheadline.weight(.semibold))
                if total > 1 {
                    Text("\(position) of \(total)")
                        .font(.caption.weight(.medium))
                        .foregroundStyle(.secondary)
                }
                Spacer()
                Button {
                    withAnimation(.snappy) { showDetails.toggle() }
                } label: {
                    Image(systemName: showDetails ? "chevron.down" : "chevron.up")
                        .font(.caption.weight(.bold))
                        .padding(6)
                }
                .buttonStyle(.borderless)
                .accessibilityLabel(showDetails ? "Hide details" : "Show details")
            }

            HStack(spacing: 6) {
                Image(systemName: request.asToolUse.symbol)
                    .foregroundStyle(ToolStyle.tint(for: request.tool))
                Text(request.description)
                    .font(.footnote)
                    .lineLimit(showDetails ? 4 : 1)
            }

            if showDetails {
                // As tall as the details, up to 220pt; scrolls beyond that.
                ScrollView {
                    ToolDetailView(tool: request.asToolUse, maxCodeLines: 25)
                        .onGeometryChange(for: CGFloat.self) { $0.size.height } action: { detailHeight = $0 }
                }
                .frame(height: min(max(detailHeight, 1), 220))
                .scrollBounceBehavior(.basedOnSize)
            }

            if !isConnected {
                Label("Reconnecting… you can answer once the connection is back.", systemImage: "wifi.exclamationmark")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }

            HStack(spacing: 10) {
                Button(role: .destructive) {
                    onRespond(false)
                } label: {
                    Text("Deny").frame(maxWidth: .infinity)
                }
                .buttonStyle(.bordered)
                .controlSize(.large)

                Button {
                    onRespond(true)
                } label: {
                    Text("Approve").frame(maxWidth: .infinity)
                }
                .buttonStyle(.borderedProminent)
                .tint(EggifyColors.accent)
                .controlSize(.large)
            }
            .disabled(!isConnected)

            if let onAlwaysAllow {
                Menu {
                    if let pattern = AlwaysAllowPattern.make(tool: request.tool, details: request.details) {
                        Button {
                            onAlwaysAllow(true)
                        } label: {
                            Label("Allow \(AlwaysAllowPattern.summary(tool: request.tool, pattern: pattern))",
                                  systemImage: "checkmark.seal")
                        }
                    }
                    Button {
                        onAlwaysAllow(false)
                    } label: {
                        Label("Allow this exact request", systemImage: "checkmark.circle")
                    }
                } label: {
                    Label("Always allow in this session", systemImage: "lock.open")
                        .font(.footnote.weight(.medium))
                        .frame(maxWidth: .infinity)
                }
                .disabled(!isConnected)
                .accessibilityHint("Approves this request and similar ones without asking again")
            }
        }
        .padding(14)
        .background(.regularMaterial, in: RoundedRectangle(cornerRadius: 18, style: .continuous))
        .overlay(
            RoundedRectangle(cornerRadius: 18, style: .continuous)
                .strokeBorder(EggifyColors.warning.opacity(0.5), lineWidth: 1)
        )
        .shadow(color: .black.opacity(0.12), radius: 12, y: 4)
        .accessibilityElement(children: .contain)
        .accessibilityLabel("Approval needed: \(request.description)")
    }
}

// MARK: - Question sheet

/// AskUserQuestion: pick option(s) or type an answer. Pass the live question
/// (from the store), so timer changes show while the sheet is open.
struct UserQuestionSheet: View {
    let question: UserQuestion
    let onAnswer: ([String]) -> Void
    let onLater: () -> Void
    let onDismiss: () -> Void
    /// "Keep waiting" (stops the auto-answer timer).
    var onHold: () -> Void = {}

    @State private var selected: Set<String> = []
    @State private var otherText = ""
    @State private var useOther = false
    @FocusState private var otherFocused: Bool

    private var answers: [String] {
        var result = question.options.map(\.label).filter { selected.contains($0) }
        let custom = otherText.trimmingCharacters(in: .whitespacesAndNewlines)
        if useOther, !custom.isEmpty { result.append(custom) }
        return result
    }

    var body: some View {
        if let proposal = question.loopProposal {
            LoopProposalView(proposal: proposal, onAnswer: onAnswer, onLater: onLater)
        } else if let proposal = question.workerProposal {
            WorkerProposalView(proposal: proposal, onAnswer: onAnswer, onLater: onLater)
        } else {
            // Also cards of a kind this app does not know: a plain question.
            questionBody
        }
    }

    private var questionBody: some View {
        NavigationStack {
            ScrollView {
                VStack(alignment: .leading, spacing: 16) {
                    HStack(spacing: 8) {
                        if !question.header.isEmpty {
                            Text(question.header.uppercased())
                                .font(.caption2.weight(.bold))
                                .padding(.horizontal, 8).padding(.vertical, 4)
                                .background(EggifyColors.accent.opacity(0.15), in: Capsule())
                                .foregroundStyle(EggifyColors.accent)
                        }
                        if question.total > 1 {
                            Text("Question \(question.index) of \(question.total)")
                                .font(.caption).foregroundStyle(.secondary)
                        }
                    }

                    Text(question.question)
                        .font(.eggifySerifSemiBold(23, relativeTo: .title3))
                        .fixedSize(horizontal: false, vertical: true)

                    if question.multiSelect {
                        Text("Choose all that apply")
                            .font(.footnote).foregroundStyle(.secondary)
                    }

                    QuestionTimerRow(question: question, onHold: onHold)

                    VStack(spacing: 10) {
                        let recommended = question.recommendedIndexes
                        ForEach(Array(question.options.enumerated()), id: \.element.label) { index, option in
                            optionButton(option, recommended: recommended.contains(index))
                        }
                        otherOption
                    }
                }
                .padding(20)
            }
            .safeAreaInset(edge: .bottom) {
                Button {
                    onAnswer(answers)
                } label: {
                    Text("Send answer").font(.headline).frame(maxWidth: .infinity)
                }
                .buttonStyle(.borderedProminent)
                .controlSize(.large)
                .tint(EggifyColors.accent)
                .disabled(answers.isEmpty)
                .padding(.horizontal, 20)
                .padding(.bottom, 12)
                .background(.bar)
            }
            .navigationTitle("The agent asks")
            .dismissesKeyboardOnScroll()
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .topBarLeading) {
                    Button("Later", action: onLater)
                }
                ToolbarItem(placement: .topBarTrailing) {
                    Button("Dismiss", role: .destructive, action: onDismiss)
                        .foregroundStyle(.red)
                }
            }
        }
        .interactiveDismissDisabled(false)
    }

    private func optionButton(_ option: UserQuestion.Option, recommended: Bool) -> some View {
        let isSelected = selected.contains(option.label)
        return Button {
            EggifyHaptics.select()
            if question.multiSelect {
                if isSelected { selected.remove(option.label) } else { selected.insert(option.label) }
            } else {
                selected = isSelected ? [] : [option.label]
                useOther = false
            }
        } label: {
            HStack(alignment: .top, spacing: 12) {
                Image(systemName: question.multiSelect
                      ? (isSelected ? "checkmark.square.fill" : "square")
                      : (isSelected ? "largecircle.fill.circle" : "circle"))
                    .font(.title3)
                    .foregroundStyle(isSelected ? EggifyColors.accent : .secondary)
                VStack(alignment: .leading, spacing: 3) {
                    Text(recommended ? UserQuestion.strippedLabel(option.label) : option.label)
                        .font(.body.weight(.medium)).foregroundStyle(.primary)
                    if recommended {
                        Text("Recommended")
                            .font(.caption2.weight(.bold))
                            .padding(.horizontal, 7).padding(.vertical, 3)
                            .background(EggifyColors.success.opacity(0.15), in: Capsule())
                            .foregroundStyle(EggifyColors.success)
                    }
                    if !option.description.isEmpty {
                        Text(option.description).font(.footnote).foregroundStyle(.secondary)
                            .fixedSize(horizontal: false, vertical: true)
                    }
                }
                Spacer(minLength: 0)
            }
            .padding(14)
            .frame(maxWidth: .infinity, alignment: .leading)
            .background(isSelected ? EggifyColors.accent.opacity(0.1) : EggifyColors.surface,
                        in: RoundedRectangle(cornerRadius: 14, style: .continuous))
            .overlay(
                RoundedRectangle(cornerRadius: 14, style: .continuous)
                    .strokeBorder(isSelected ? EggifyColors.accent : .clear, lineWidth: 1.5)
            )
        }
        .buttonStyle(.plain)
        .accessibilityAddTraits(isSelected ? .isSelected : [])
        .accessibilityHint(recommended ? "Recommended" : "")
    }

    private var otherOption: some View {
        VStack(alignment: .leading, spacing: 8) {
            Button {
                useOther.toggle()
                if useOther {
                    if !question.multiSelect { selected = [] }
                    otherFocused = true
                }
            } label: {
                HStack(spacing: 12) {
                    Image(systemName: useOther ? "pencil.circle.fill" : "pencil.circle")
                        .font(.title3)
                        .foregroundStyle(useOther ? EggifyColors.accent : .secondary)
                    Text("Something else…").font(.body.weight(.medium)).foregroundStyle(.primary)
                    Spacer()
                }
            }
            .buttonStyle(.plain)

            if useOther {
                TextField("Type your answer", text: $otherText, axis: .vertical)
                    .lineLimit(1...5)
                    .focused($otherFocused)
                    .padding(12)
                    .background(Color(.tertiarySystemFill), in: RoundedRectangle(cornerRadius: 10))
            }
        }
        .padding(14)
        .background(EggifyColors.surface, in: RoundedRectangle(cornerRadius: 14, style: .continuous))
    }
}

/// The auto-answer countdown ("Auto-picks “X” in 1:42" + Keep waiting), or
/// "Waiting for you" once held. Nothing for confirmation cards or questions
/// that wait without limit. Only this row redraws every second.
struct QuestionTimerRow: View {
    let question: UserQuestion
    let onHold: () -> Void

    var body: some View {
        if question.requiresUser {
            EmptyView()
        } else if question.held {
            Label("Waiting for you", systemImage: "hand.raised.fill")
                .font(.footnote.weight(.semibold))
                .foregroundStyle(.secondary)
                .accessibilityIdentifier("question-held")
        } else if question.showsCountdown {
            HStack(spacing: 10) {
                TimelineView(.periodic(from: .now, by: 1)) { context in
                    Label(question.countdownText(now: context.date) ?? "", systemImage: "timer")
                        .font(.footnote.weight(.semibold))
                        .monospacedDigit()
                        .foregroundStyle(EggifyColors.warning)
                        .fixedSize(horizontal: false, vertical: true)
                }
                Spacer(minLength: 0)
                Button("Keep waiting", action: onHold)
                    .buttonStyle(.bordered)
                    .controlSize(.small)
                    .tint(EggifyColors.accent)
                    .accessibilityIdentifier("question-keep-waiting")
            }
            .padding(12)
            .background(EggifyColors.warning.opacity(0.1), in: RoundedRectangle(cornerRadius: 12, style: .continuous))
        }
    }
}
