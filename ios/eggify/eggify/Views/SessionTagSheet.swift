//
//  SessionTagSheet.swift
//  eggify
//
//  Tag a session, like the desktop's "Add tag" (⌘S): the session's tags as
//  chips (tap × to remove), a field that adds on Return or comma, and the tags
//  already used on other sessions as one-tap suggestions. Every change is
//  saved at once (`SessionsWebSocketViewModel.setTags`).
//

import SwiftUI

struct SessionTagSheet: View {
    let sessionId: String

    @ObservedObject private var list = SessionListModel.shared
    @Environment(\.dismiss) private var dismiss
    @State private var query = ""
    @FocusState private var fieldFocused: Bool

    private var session: AgentSession? { list.sessions.first { $0.id == sessionId } }
    private var tags: [String] { session?.tags ?? [] }
    private var isFull: Bool { tags.count >= SessionTags.maxTags }
    private var suggestions: [String] {
        SessionTags.suggest(query, known: SessionTags.known(list.sessions.map(\.tags)), exclude: tags, limit: 12)
    }

    var body: some View {
        NavigationStack {
            ScrollView {
                VStack(alignment: .leading, spacing: 20) {
                    current
                    field
                    if !suggestions.isEmpty, !isFull {
                        suggestionList
                    }
                }
                .padding(20)
            }
            .scrollDismissesKeyboard(.interactively)
            .navigationTitle("Tags")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .principal) {
                    VStack(spacing: 0) {
                        Text("Tags").font(.headline)
                        if let name = session?.displayTitle {
                            Text(name)
                                .font(.caption)
                                .foregroundStyle(.secondary)
                                .lineLimit(1)
                        }
                    }
                }
                ToolbarItem(placement: .confirmationAction) {
                    Button("Done") {
                        add(query)
                        dismiss()
                    }
                }
            }
        }
        .onAppear { fieldFocused = true }
    }

    // MARK: Parts

    @ViewBuilder
    private var current: some View {
        if tags.isEmpty {
            Text("No tags yet. Tags show on the session in the list here and on the desktop.")
                .font(.footnote)
                .foregroundStyle(.secondary)
        } else {
            FlowLayout(spacing: 8) {
                ForEach(tags, id: \.self) { tag in
                    EditableTagChip(tag: tag) { remove(tag) }
                }
            }
        }
    }

    private var field: some View {
        VStack(alignment: .leading, spacing: 6) {
            HStack(spacing: 8) {
                Image(systemName: "tag")
                    .foregroundStyle(.secondary)
                TextField(tags.isEmpty ? "Tag this session…" : "Another tag…", text: $query)
                    .focused($fieldFocused)
                    .textInputAutocapitalization(.never)
                    .autocorrectionDisabled()
                    .submitLabel(.done)
                    .onSubmit {
                        if query.trimmingCharacters(in: .whitespaces).isEmpty {
                            dismiss()
                        } else {
                            add(query)
                            fieldFocused = true
                        }
                    }
                    .onChange(of: query) { _, value in
                        // A comma ends a tag (pasting "a, b" adds both).
                        guard value.contains(",") else { return }
                        var parts = value.components(separatedBy: ",")
                        let rest = parts.removeLast()
                        add(parts.joined(separator: ","))
                        query = String(rest.drop { $0.isWhitespace })
                    }
                    .disabled(isFull)
            }
            .padding(.horizontal, 12)
            .padding(.vertical, 10)
            .background(EggifyColors.surface, in: RoundedRectangle(cornerRadius: 12, style: .continuous))
            Text(isFull ? "A session can have \(SessionTags.maxTags) tags." : "Return or comma adds a tag.")
                .font(.caption)
                .foregroundStyle(.secondary)
        }
    }

    private var suggestionList: some View {
        VStack(alignment: .leading, spacing: 8) {
            Text(query.trimmingCharacters(in: .whitespaces).isEmpty ? "Used on other sessions" : "Matching tags")
                .font(.caption.weight(.semibold))
                .foregroundStyle(.secondary)
                .textCase(.uppercase)
            FlowLayout(spacing: 8) {
                ForEach(suggestions, id: \.self) { tag in
                    Button {
                        add(tag)
                        query = ""
                    } label: {
                        HStack(spacing: 4) {
                            Image(systemName: "plus")
                                .font(.caption2.weight(.bold))
                            Text(tag)
                        }
                        .font(.subheadline)
                        .padding(.horizontal, 10)
                        .padding(.vertical, 6)
                        .overlay(Capsule().strokeBorder(TagChipColors(tag: tag).border, lineWidth: 1))
                        .foregroundStyle(TagChipColors(tag: tag).text)
                        .contentShape(Capsule())
                    }
                    .buttonStyle(.plain)
                    .accessibilityLabel("Add tag \(tag)")
                }
            }
        }
    }

    // MARK: Actions

    private func add(_ text: String) {
        let fresh = SessionTags.split(text)
        if !fresh.isEmpty {
            SessionsWebSocketViewModel.shared.setTags(sessionId, tags + fresh)
            EggifyHaptics.select()
        }
        if !text.contains(",") { query = "" }
    }

    private func remove(_ tag: String) {
        SessionsWebSocketViewModel.shared.setTags(sessionId, tags.filter { $0.lowercased() != tag.lowercased() })
        EggifyHaptics.select()
    }
}

/// A tag in its own colour with a remove button.
private struct EditableTagChip: View {
    let tag: String
    let onRemove: () -> Void

    var body: some View {
        let colors = TagChipColors(tag: tag)
        HStack(spacing: 6) {
            Text(tag)
                .lineLimit(1)
            Button(action: onRemove) {
                Image(systemName: "xmark")
                    .font(.caption2.weight(.bold))
                    .frame(width: 22, height: 22)
                    .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            .accessibilityLabel("Remove tag \(tag)")
        }
        .font(.subheadline.weight(.semibold))
        .padding(.leading, 12)
        .padding(.trailing, 4)
        .padding(.vertical, 4)
        .background(colors.fill, in: Capsule())
        .overlay(Capsule().strokeBorder(colors.border, lineWidth: 0.75))
        .foregroundStyle(colors.text)
    }
}
