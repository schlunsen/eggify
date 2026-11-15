//
//  SessionSkillsSheet.swift
//  eggify
//
//  The Skills sheet (chat menu): the approved skills the session gets and
//  why, Use to put `/<name> ` in the composer, and adding or removing other
//  projects' skills. A note says when a change reaches the agent.
//

import SwiftUI

struct SessionSkillsSheet: View {
    let sessionID: String
    /// Use was tapped: the chat fills the composer and closes the sheet.
    let onUse: (SessionSkill) -> Void

    @ObservedObject private var store = SessionSkillsStore.shared
    @Environment(\.dismiss) private var dismiss
    @State private var query = ""

    /// More than this many skills get a search field.
    static let searchThreshold = 8

    private var list: SessionSkills? { store.lists[sessionID] }
    private var active: [SessionSkill] { SessionSkills.filter(list?.active ?? [], query: query) }
    private var others: [SessionSkill] { SessionSkills.filter(list?.others ?? [], query: query) }

    var body: some View {
        NavigationStack {
            content
                .navigationTitle("Skills")
                .navigationBarTitleDisplayMode(.inline)
                .toolbar {
                    ToolbarItem(placement: .topBarTrailing) {
                        Button("Done") { dismiss() }
                    }
                }
                .refreshable { await store.load(sessionID) }
                .task { await store.load(sessionID) }
                .onDisappear { store.saveErrors[sessionID] = nil }
        }
        .presentationDetents([.medium, .large])
        .presentationDragIndicator(.visible)
    }

    @ViewBuilder
    private var content: some View {
        if let list {
            if list.skills.isEmpty {
                ContentUnavailableView("No approved skills",
                                       systemImage: "wand.and.stars",
                                       description: Text("Approve skills on your computer: Skills page."))
            } else if list.skills.count > Self.searchThreshold {
                skillList(list).searchable(text: $query, prompt: "Search skills")
            } else {
                skillList(list)
            }
        } else if let error = store.loadErrors[sessionID] {
            ContentUnavailableView {
                Label("Couldn't load skills", systemImage: "wifi.exclamationmark")
            } description: {
                Text(error)
            } actions: {
                Button("Try again") { Task { await store.load(sessionID) } }
                    .buttonStyle(.borderedProminent)
            }
        } else {
            ProgressView()
                .frame(maxWidth: .infinity, maxHeight: .infinity)
        }
    }

    private func skillList(_ list: SessionSkills) -> some View {
        List {
            if let error = store.saveErrors[sessionID] {
                Section {
                    Label(error, systemImage: "exclamationmark.triangle.fill")
                        .font(.footnote)
                        .foregroundStyle(EggifyColors.error)
                }
            }

            if !active.isEmpty {
                Section {
                    ForEach(active) { skill in
                        SessionSkillRow(skill: skill, isSaving: store.saving.contains(skill.id),
                                        onUse: skill.userInvocable ? { use(skill) } : nil,
                                        onRemove: skill.added ? { remove(skill) } : nil)
                    }
                } header: {
                    Text("In this session")
                } footer: {
                    if others.isEmpty { noteView(list) }
                }
            }

            if !others.isEmpty {
                Section {
                    ForEach(others) { skill in
                        SessionSkillRow(skill: skill, isSaving: store.saving.contains(skill.id),
                                        onAdd: { add(skill) })
                    }
                } header: {
                    Text("Other projects' skills")
                } footer: {
                    noteView(list)
                }
            }
        }
        .overlay {
            if active.isEmpty && others.isEmpty && !query.isEmpty {
                ContentUnavailableView.search(text: query)
            }
        }
    }

    @ViewBuilder
    private func noteView(_ list: SessionSkills) -> some View {
        if let note = list.note {
            Text(note)
        }
    }

    private func use(_ skill: SessionSkill) {
        EggifyHaptics.tap()
        onUse(skill)
    }

    private func add(_ skill: SessionSkill) {
        Task { await store.add(skill, to: sessionID) }
    }

    private func remove(_ skill: SessionSkill) {
        Task { await store.remove(skill, from: sessionID) }
    }
}

struct SessionSkillRow: View {
    let skill: SessionSkill
    var isSaving = false
    var onUse: (() -> Void)?
    var onRemove: (() -> Void)?
    var onAdd: (() -> Void)?

    var body: some View {
        HStack(alignment: .center, spacing: 12) {
            VStack(alignment: .leading, spacing: 4) {
                HStack(spacing: 6) {
                    Text(skill.name)
                        .font(.subheadline.weight(.semibold))
                        .lineLimit(1)
                    if let label = skill.reachLabel {
                        Text(label)
                            .font(.caption2.weight(.semibold))
                            .padding(.horizontal, 6)
                            .padding(.vertical, 2)
                            .background(EggifyColors.accent.opacity(0.15), in: Capsule())
                            .foregroundStyle(EggifyColors.accent)
                    }
                }
                if let description = skill.description {
                    Text(description)
                        .font(.footnote)
                        .foregroundStyle(.secondary)
                        .lineLimit(2)
                }
                if onUse != nil, let hint = skill.argumentHint {
                    Text("/\(skill.name) \(hint)")
                        .font(.caption.monospaced())
                        .foregroundStyle(.tertiary)
                        .lineLimit(1)
                }
            }
            .frame(maxWidth: .infinity, alignment: .leading)

            trailing
        }
        .padding(.vertical, 2)
        .swipeActions(edge: .trailing) {
            if let onRemove, !isSaving {
                Button("Remove", role: .destructive, action: onRemove)
            }
        }
        .contextMenu {
            if let onUse {
                Button { onUse() } label: { Label("Use", systemImage: "text.cursor") }
            }
            if let onRemove {
                Button(role: .destructive) { onRemove() } label: { Label("Remove", systemImage: "minus.circle") }
            }
            if let onAdd {
                Button { onAdd() } label: { Label("Add to this session", systemImage: "plus.circle") }
            }
        }
    }

    @ViewBuilder
    private var trailing: some View {
        if isSaving {
            ProgressView()
                .accessibilityLabel("Saving")
        } else if let onUse {
            Button("Use", action: onUse)
                .buttonStyle(.bordered)
                .controlSize(.small)
                .accessibilityLabel("Use \(skill.name)")
        } else if let onRemove {
            // No Use button to sit next to: show Remove itself.
            Button("Remove", role: .destructive, action: onRemove)
                .buttonStyle(.bordered)
                .controlSize(.small)
                .accessibilityLabel("Remove \(skill.name)")
        } else if let onAdd {
            Button("Add", action: onAdd)
                .buttonStyle(.bordered)
                .controlSize(.small)
                .accessibilityLabel("Add \(skill.name)")
        }
    }
}
