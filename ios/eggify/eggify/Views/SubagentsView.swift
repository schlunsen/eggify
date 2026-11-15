//
//  SubagentsView.swift
//  eggify
//
//  Subagents of a session: a chip in the chat while they run, a sheet with
//  every subagent and its latest steps, and a live feed per subagent.
//

import SwiftUI
import Combine

// MARK: - Model

/// One session's subagents, observed only by the sheet (their output
/// streams often; the chat itself only tracks the counts).
@MainActor
final class SubagentsModel: ObservableObject {
    @Published private(set) var agents: [Subagent] = []
    let sessionID: String
    let store: SessionsWebSocketViewModel

    init(sessionID: String, store: SessionsWebSocketViewModel? = nil) {
        let store = store ?? .shared
        self.sessionID = sessionID
        self.store = store
        store.$subagents
            .map { $0[sessionID] ?? [] }
            .removeDuplicates()
            .assign(to: &$agents)
    }

    /// Running ones first, then newest first.
    var sorted: [Subagent] {
        agents.sorted {
            if $0.status.isActive != $1.status.isActive { return $0.status.isActive }
            return ($0.createdAt ?? .distantPast) > ($1.createdAt ?? .distantPast)
        }
    }

    func agent(_ id: String) -> Subagent? { agents.first { $0.id == id } }

    func refresh() async { await store.loadSubagents(sessionID) }
}

// MARK: - Chip

/// Shown above the composer while subagents run.
struct SubagentChip: View {
    let running: Int
    let action: () -> Void

    var body: some View {
        Button(action: action) {
            HStack(spacing: 8) {
                ProgressView()
                    .controlSize(.mini)
                Text(running == 1 ? "1 subagent working" : "\(running) subagents working")
                    .font(.footnote.weight(.semibold))
                Image(systemName: "chevron.right")
                    .font(.caption2.weight(.bold))
                    .foregroundStyle(.secondary)
            }
            .padding(.horizontal, 12)
            .padding(.vertical, 7)
            .background(.regularMaterial, in: Capsule())
            .overlay(Capsule().strokeBorder(EggifyColors.accent.opacity(0.35), lineWidth: 1))
        }
        .buttonStyle(.plain)
        .accessibilityLabel(running == 1 ? "1 subagent working. Show subagents" : "\(running) subagents working. Show subagents")
    }
}

// MARK: - Sheet

struct SubagentsSheet: View {
    @StateObject private var model: SubagentsModel
    @Environment(\.dismiss) private var dismiss

    init(sessionID: String, store: SessionsWebSocketViewModel? = nil) {
        _model = StateObject(wrappedValue: SubagentsModel(sessionID: sessionID, store: store))
    }

    var body: some View {
        NavigationStack {
            List {
                ForEach(model.sorted) { agent in
                    NavigationLink(value: agent.id) {
                        SubagentRow(agent: agent)
                    }
                }
            }
            .overlay {
                if model.agents.isEmpty {
                    ContentUnavailableView("No subagents",
                                           systemImage: "person.2",
                                           description: Text("When the agent hands work to subagents, they show up here."))
                }
            }
            .navigationTitle("Subagents")
            .navigationBarTitleDisplayMode(.inline)
            .navigationDestination(for: String.self) { id in
                SubagentDetailView(model: model, agentID: id)
            }
            .toolbar {
                ToolbarItem(placement: .topBarTrailing) {
                    Button("Done") { dismiss() }
                }
            }
            .refreshable { await model.refresh() }
            .task { await model.refresh() }
        }
        .presentationDetents([.medium, .large])
        .presentationDragIndicator(.visible)
    }
}

struct SubagentRow: View {
    let agent: Subagent

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            HStack(spacing: 8) {
                SubagentStatusIcon(status: agent.status)
                Text(agent.title)
                    .font(.subheadline.weight(.semibold))
                    .lineLimit(2)
                Spacer(minLength: 4)
                SubagentElapsed(agent: agent)
            }
            HStack(spacing: 6) {
                Text(agent.type)
                    .font(.caption2.weight(.semibold))
                    .padding(.horizontal, 6)
                    .padding(.vertical, 2)
                    .background(EggifyColors.accent.opacity(0.15), in: Capsule())
                    .foregroundStyle(EggifyColors.accent)
                if let label = agent.modelLabel {
                    Text(label)
                        .font(.caption2)
                        .foregroundStyle(.secondary)
                        .opacity(agent.modelRequested ? 0.6 : 1)
                        .accessibilityLabel(agent.modelRequested ? "Model \(label), requested" : "Model \(label)")
                }
                if agent.background {
                    Text("background")
                        .font(.caption2)
                        .foregroundStyle(.secondary)
                }
                if agent.parentAgentId != nil {
                    Text("nested")
                        .font(.caption2)
                        .foregroundStyle(.secondary)
                }
            }
            if let error = agent.errorMessage, agent.status == .failed {
                Text(error)
                    .font(.caption)
                    .foregroundStyle(EggifyColors.error)
                    .lineLimit(2)
            } else {
                ForEach(Array(agent.output.suffix(3).enumerated()), id: \.offset) { _, line in
                    SubagentLineView(line: SubagentLine(line), compact: true)
                }
            }
        }
        .padding(.vertical, 4)
        .accessibilityElement(children: .combine)
    }
}

struct SubagentStatusIcon: View {
    let status: Subagent.Status

    var body: some View {
        switch status {
        case .running, .waiting:
            ProgressView().controlSize(.small)
                .accessibilityLabel(status == .waiting ? "Waiting" : "Running")
        case .completed:
            Image(systemName: "checkmark.circle.fill")
                .foregroundStyle(EggifyColors.success)
                .accessibilityLabel("Finished")
        case .failed:
            Image(systemName: "xmark.octagon.fill")
                .foregroundStyle(EggifyColors.error)
                .accessibilityLabel("Failed")
        }
    }
}

/// Running: a live timer since launch. Finished: how long it took.
struct SubagentElapsed: View {
    let agent: Subagent

    var body: some View {
        Group {
            if agent.status.isActive, let start = agent.createdAt {
                Text(start, style: .timer)
            } else if let start = agent.createdAt, let end = agent.completedAt {
                Text(Duration.seconds(max(0, end.timeIntervalSince(start)))
                    .formatted(.units(allowed: [.hours, .minutes, .seconds], width: .narrow, maximumUnitCount: 2)))
            }
        }
        .font(.caption.monospacedDigit())
        .foregroundStyle(.secondary)
    }
}

struct SubagentLineView: View {
    let line: SubagentLine
    var compact = false

    var body: some View {
        HStack(alignment: .firstTextBaseline, spacing: 6) {
            if let tool = line.tool {
                let info = ToolUseInfo(id: "", name: tool, status: nil, input: nil)
                Image(systemName: info.symbol)
                    .font(.caption2)
                    .foregroundStyle(ToolStyle.tint(for: tool))
                    .frame(width: 14)
                Text(line.text.isEmpty ? tool : "\(tool) · \(line.text)")
                    .font(.eggifyMono(compact ? 11 : 12, relativeTo: .caption))
                    .foregroundStyle(.secondary)
            } else {
                Text(line.text)
                    .font(compact ? .caption : .footnote)
                    .foregroundStyle(compact ? .secondary : .primary)
            }
        }
        .lineLimit(compact ? 1 : nil)
        .frame(maxWidth: .infinity, alignment: .leading)
    }
}

// MARK: - Detail

struct SubagentDetailView: View {
    @ObservedObject var model: SubagentsModel
    let agentID: String

    var body: some View {
        if let agent = model.agent(agentID) {
            ScrollViewReader { proxy in
                ScrollView {
                    VStack(alignment: .leading, spacing: 10) {
                        HStack(spacing: 8) {
                            SubagentStatusIcon(status: agent.status)
                            Text(agent.type)
                                .font(.caption.weight(.semibold))
                                .foregroundStyle(EggifyColors.accent)
                            if let label = agent.modelLabel {
                                Text(label)
                                    .font(.caption)
                                    .foregroundStyle(.secondary)
                                    .opacity(agent.modelRequested ? 0.6 : 1)
                            }
                            Spacer()
                            SubagentElapsed(agent: agent)
                        }
                        if !agent.description.isEmpty {
                            Text(agent.description)
                                .font(.headline)
                        }
                        if let error = agent.errorMessage {
                            Label(error, systemImage: "exclamationmark.triangle.fill")
                                .font(.footnote)
                                .foregroundStyle(EggifyColors.error)
                        }
                        Divider()
                        if agent.output.isEmpty {
                            Text(agent.status.isActive ? "Waiting for output…" : "No output.")
                                .font(.footnote)
                                .foregroundStyle(.secondary)
                        }
                        ForEach(Array(agent.output.enumerated()), id: \.offset) { _, line in
                            SubagentLineView(line: SubagentLine(line))
                        }
                        Color.clear.frame(height: 1).id("end")
                    }
                    .textSelection(.enabled)
                    .padding(16)
                }
                .defaultScrollAnchor(.bottom)
                .onChange(of: agent.output.count) { _, _ in
                    if agent.status.isActive { withAnimation(.snappy) { proxy.scrollTo("end", anchor: .bottom) } }
                }
            }
            .navigationTitle(agent.title)
            .navigationBarTitleDisplayMode(.inline)
        } else {
            ContentUnavailableView("Subagent not found", systemImage: "person.crop.circle.badge.questionmark")
        }
    }
}
