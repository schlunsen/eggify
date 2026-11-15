//
//  SessionListModel.swift
//  eggify
//
//  What the home list and the tab bar show, mirrored from the session store
//  without the message data. Observing the store itself re-rendered both on
//  every streamed message, even with a chat covering them.
//

import SwiftUI
import Combine

@MainActor
final class SessionListModel: ObservableObject {
    static let shared = SessionListModel()

    @Published private(set) var sessions: [AgentSession] = []
    @Published private(set) var sessionStatus: [String: String] = [:]
    @Published private(set) var topicTags: [String: [String]] = [:]
    @Published private(set) var digestSummaries: [String: String] = [:]
    @Published private(set) var pendingPermissions: [String: [PermissionRequest]] = [:]
    @Published private(set) var pendingQuestions: [String: [UserQuestion]] = [:]
    @Published private(set) var connectionState: WebSocketConnectionState = .disconnected
    @Published private(set) var isLoading = false
    @Published private(set) var errorMessage: String?
    @Published private(set) var notice: SessionsWebSocketViewModel.Notice?

    let store: SessionsWebSocketViewModel

    init(store: SessionsWebSocketViewModel? = nil) {
        let store = store ?? .shared
        self.store = store
        store.$sessions.removeDuplicates().assign(to: &$sessions)
        store.$sessionStatus.removeDuplicates().assign(to: &$sessionStatus)
        store.$topicTags.removeDuplicates().assign(to: &$topicTags)
        store.$digestSummaries.removeDuplicates().assign(to: &$digestSummaries)
        store.$pendingPermissions.removeDuplicates().assign(to: &$pendingPermissions)
        store.$pendingQuestions.removeDuplicates().assign(to: &$pendingQuestions)
        store.$connectionState.removeDuplicates().assign(to: &$connectionState)
        store.$isLoading.removeDuplicates().assign(to: &$isLoading)
        store.$errorMessage.removeDuplicates().assign(to: &$errorMessage)
        store.$notice.removeDuplicates().assign(to: &$notice)
    }

    func status(of sessionId: String) -> SessionStatus {
        SessionStatus(sessionStatus[sessionId] ?? sessions.first { $0.id == sessionId }?.status ?? "idle")
    }

    /// The tags a row shows: the user's own first, then the AI topic tags,
    /// without case-insensitive repeats.
    func tags(of session: AgentSession) -> (own: [String], topics: [String]) {
        Self.rowTags(own: session.tags, topics: topicTags[session.id] ?? [])
    }

    nonisolated static func rowTags(own: [String], topics: [String]) -> (own: [String], topics: [String]) {
        var seen = Set(own.map { $0.lowercased() })
        return (own, topics.filter { seen.insert($0.lowercased()).inserted })
    }

    /// Working: its own turn runs, or a subagent it launched still does.
    func isWorking(_ session: AgentSession) -> Bool {
        status(of: session.id).including(subagentsRunning: session.subagents_running).isActive
    }

    /// What a session waits for ("Allow git push?", "Question waiting"), "" when nothing.
    func waitingFor(_ session: AgentSession) -> String {
        SessionGroups.waitingLabel(permissions: pendingPermissions[session.id],
                                   questions: pendingQuestions[session.id],
                                   listedPermissions: session.pending_permissions,
                                   listedQuestions: session.pending_questions)
    }

    /// The Agents list's groups for these sessions.
    func groups(_ sessions: [AgentSession], now: Date = Date()) -> [SessionGroup] {
        SessionGroups.group(sessions, now: now,
                            status: { SessionStatus(self.sessionStatus[$0.id] ?? $0.status) },
                            waitingFor: { self.waitingFor($0) })
    }

    var sessionsNeedingAttention: [AgentSession] {
        sessions.filter { s in
            s.needsAttention || !(pendingPermissions[s.id] ?? []).isEmpty || !(pendingQuestions[s.id] ?? []).isEmpty
        }
    }

    /// The toast, written back to the store.
    var noticeBinding: Binding<SessionsWebSocketViewModel.Notice?> {
        Binding(get: { self.notice }, set: { self.store.notice = $0 })
    }
}
