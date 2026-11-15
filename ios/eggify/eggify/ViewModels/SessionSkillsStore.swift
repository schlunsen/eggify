//
//  SessionSkillsStore.swift
//  eggify
//
//  Skills of sessions whose Skills sheet was opened: the list, loading and
//  errors, and adding or removing a skill. A `session_skills_updated` event
//  (a change from this phone, the desktop or an agent) reloads a list the
//  app holds. The chat menu shows the active count once a list is known.
//

import SwiftUI
import Combine

@MainActor
final class SessionSkillsStore: ObservableObject {
    static let shared = SessionSkillsStore()

    @Published private(set) var lists: [String: SessionSkills] = [:]
    @Published private(set) var loadErrors: [String: String] = [:]
    @Published private(set) var loading: Set<String> = []
    /// Skills being added or removed, by id.
    @Published private(set) var saving: Set<String> = []
    /// Why the last add or remove failed, per session.
    @Published var saveErrors: [String: String] = [:]

    private let apiClient = EggifyAPIClient.shared

    private init() {}

    /// Loads (or reloads) a session's skills.
    func load(_ sessionId: String) async {
        guard !loading.contains(sessionId) else { return }
        loading.insert(sessionId)
        defer { loading.remove(sessionId) }
        do {
            let list = try await apiClient.getSessionSkills(sessionID: sessionId)
            if lists[sessionId] != list { lists[sessionId] = list }
            if loadErrors[sessionId] != nil { loadErrors[sessionId] = nil }
        } catch is CancellationError {
        } catch EggifyAPIClient.EggifyAPIError.notFound {
            loadErrors[sessionId] = "This server doesn't list a session's skills yet. Update Eggify on your computer."
        } catch {
            loadErrors[sessionId] = error.localizedDescription
        }
    }

    /// The server says the session's skills changed.
    func noteUpdated(_ sessionId: String) {
        guard lists[sessionId] != nil || loadErrors[sessionId] != nil else { return }
        Task { await load(sessionId) }
    }

    func add(_ skill: SessionSkill, to sessionId: String) async {
        guard let list = lists[sessionId] else { return }
        await save(skill, in: sessionId, addedIDs: list.addedIDs(adding: skill.id), verb: "add")
    }

    func remove(_ skill: SessionSkill, from sessionId: String) async {
        guard let list = lists[sessionId] else { return }
        await save(skill, in: sessionId, addedIDs: list.addedIDs(removing: skill.id), verb: "remove")
    }

    private func save(_ skill: SessionSkill, in sessionId: String, addedIDs: [String], verb: String) async {
        guard !saving.contains(skill.id) else { return }
        saving.insert(skill.id)
        defer { saving.remove(skill.id) }
        saveErrors[sessionId] = nil
        do {
            let list = try await apiClient.setSessionSkills(sessionID: sessionId, addedIDs: addedIDs)
            lists[sessionId] = list
            EggifyHaptics.success()
        } catch is CancellationError {
        } catch EggifyAPIClient.EggifyAPIError.userRequired(let message) {
            EggifyHaptics.error()
            saveErrors[sessionId] = message
            SessionsWebSocketViewModel.shared.notice = .init(message: message, kind: .signInRequired)
        } catch {
            EggifyHaptics.error()
            saveErrors[sessionId] = "Couldn't \(verb) \(skill.name): \(error.localizedDescription)"
        }
    }

    /// Forget everything (sign-out, host switch).
    func reset() {
        lists = [:]
        loadErrors = [:]
        saveErrors = [:]
        saving = []
    }
}
