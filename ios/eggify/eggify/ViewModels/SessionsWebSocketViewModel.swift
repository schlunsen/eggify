//
//  SessionsWebSocketViewModel.swift
//  eggify
//
//  The app's live session store: the session list (REST), each open
//  session's messages (REST history + live WebSocket events), and the
//  requests that wait for the user (tool approvals, agent questions).
//
//  Lists and history come from REST on purpose. Over the WebSocket,
//  `list_sessions` and `load_messages` make the server route every listed
//  session's permission prompts to this connection, which would take them
//  away from a desktop tab. Only sessions opened on screen are subscribed.
//

import SwiftUI
import Combine
import UIKit

@MainActor
final class SessionsWebSocketViewModel: ObservableObject, WebSocketEventHandler {
    static let shared = SessionsWebSocketViewModel()

    /// A short-lived banner message.
    struct Notice: Identifiable, Equatable {
        enum Kind { case info, warning, error, signInRequired }
        let id = UUID()
        let message: String
        let kind: Kind
    }

    // MARK: Published state

    @Published private(set) var sessions: [AgentSession] = []
    @Published private(set) var isLoading = false
    @Published var errorMessage: String?
    @Published private(set) var connectionState: WebSocketConnectionState = .disconnected

    @Published private(set) var sessionMessages: [String: [AgentMessage]] = [:]
    /// Images tool results returned live (Read on a screenshot), newest last,
    /// by tool call id. The server doesn't store them, so a history reload
    /// puts them back from here, as long as the app runs.
    private var toolResultImages: [String: [(id: String, image: ImageBlockData)]] = [:]
    private static let maxToolResultImages = 40
    @Published private(set) var hasMoreMessages: [String: Bool] = [:]
    @Published private(set) var loadingMessages: Set<String> = []
    @Published private(set) var messageLoadErrors: [String: String] = [:]
    @Published private(set) var sessionStatus: [String: String] = [:]
    /// Cached AI topic tags per session (the web overview's digest tags).
    @Published private(set) var topicTags: [String: [String]] = [:]
    /// Cached one-line AI summaries per session (the web sidebar's row summary).
    @Published private(set) var digestSummaries: [String: String] = [:]
    private var topicTagVersions: [String: String] = [:]

    @Published private(set) var pendingPermissions: [String: [PermissionRequest]] = [:]
    /// Subagents per session, oldest first (only for chats that were opened).
    @Published private(set) var subagents: [String: [Subagent]] = [:]
    @Published private(set) var pendingQuestions: [String: [UserQuestion]] = [:]
    /// Questions the user chose to answer later (hidden until reopened).
    @Published var deferredQuestionIds: Set<String> = []
    /// The global policy for agent questions (nil until loaded, or an older server).
    @Published private(set) var questionPolicyDefault: QuestionPolicy?

    @Published var notice: Notice?

    /// Session currently on screen (used for notices about other sessions).
    @Published var selectedSessionId: String?

    var webSocketConnected: Bool { connectionState.isUsable }

    private let apiClient = EggifyAPIClient.shared
    private let webSocket = WebSocketService.shared
    private var cancellables = Set<AnyCancellable>()
    private var pendingCreates: [String: CheckedContinuation<AgentSession, Error>] = [:]
    private var sessionsRefreshTask: Task<Void, Never>?

    private init() {
        webSocket.eventHandler = self
        webSocket.$state
            .removeDuplicates()
            .sink { [weak self] in self?.connectionState = $0 }
            .store(in: &cancellables)
    }

    // MARK: - Lifecycle

    /// Connect and load the session list.
    func startMonitoring() async {
        // Show the last known list at once; the server's replaces it.
        if sessions.isEmpty {
            let cached = await apiClient.cachedSessions()
            if sessions.isEmpty, !cached.isEmpty {
                sessions = cached.sorted { ($0.updatedDate ?? .distantPast) > ($1.updatedDate ?? .distantPast) }
            }
        }
        async let connect: Void = webSocket.connect()
        async let load: Void = loadSessions()
        async let policy: Bool = loadQuestionPolicyDefault()
        _ = await (connect, load, policy)
    }

    /// Sets a session's status, without publishing when it is unchanged:
    /// every streamed message would otherwise re-render each view that
    /// observes the store.
    private func setStatus(_ status: String?, for sessionId: String) {
        if sessionStatus[sessionId] != status { sessionStatus[sessionId] = status }
    }

    /// Disconnect and forget everything (logout / host switch).
    func stopMonitoring() {
        webSocket.disconnect()
        sessions = []
        sessionMessages = [:]
        hasMoreMessages = [:]
        sessionStatus = [:]
        pendingPermissions = [:]
        pendingQuestions = [:]
        deferredQuestionIds = []
        questionPolicyDefault = nil
        selectedSessionId = nil
        lastSendAt = [:]
        subagents = [:]
        SessionMediaStore.shared.reset()
        SessionSkillsStore.shared.reset()
        UsageLimitStore.shared.reset()
        loadingMessages = []
        messageLoadErrors = [:]
        notice = nil
        errorMessage = nil
        for (_, continuation) in pendingCreates {
            continuation.resume(throwing: CancellationError())
        }
        pendingCreates = [:]
    }

    // MARK: - Sessions

    /// When a prompt was last sent from this device, per session. The server
    /// may still report "idle" for a moment after a send.
    private var lastSendAt: [String: Date] = [:]

    /// - Parameter quietly: background refresh (polling); no loading state,
    ///   and nothing is published when the list did not change.
    func loadSessions(quietly: Bool = false) async {
        if !quietly { isLoading = true }
        defer { if !quietly { isLoading = false } }
        do {
            var fetched = try await apiClient.getAgentSessions()
            // The list's loop state has the verify output cut short; keep the
            // live event's full copy while it describes the same step.
            for i in fetched.indices {
                if let live = session(fetched[i].id)?.loop, let listed = fetched[i].loop,
                   live.phase == listed.phase, live.iteration == listed.iteration {
                    fetched[i].loop = live
                }
                // A tag save still on its way wins over a list fetched before it.
                if let pending = unsavedTags[fetched[i].id] { fetched[i].tags = pending }
            }
            let sorted = fetched.sorted { ($0.updatedDate ?? .distantPast) > ($1.updatedDate ?? .distantPast) }
            if sorted != sessions { sessions = sorted }
            let now = Date()
            for session in fetched {
                // Status events only reach sessions this device is subscribed
                // to, so the list is the source of truth for the others. Keep
                // a local "working" only right after this device sent a prompt.
                let local = SessionStatus(sessionStatus[session.id] ?? "")
                if local.isActive, let sent = lastSendAt[session.id], now.timeIntervalSince(sent) < 8,
                   !SessionStatus(session.status).isActive {
                    continue
                }
                setStatus(session.status, for: session.id)
            }
            if errorMessage != nil { errorMessage = nil }
            await refreshTopicTags(fetched)
        } catch is CancellationError {
        } catch {
            errorMessage = error.localizedDescription
        }
    }

    /// Loads the cached AI topic tags of sessions that changed since the last
    /// look (message count or status), so polling costs nothing while idle.
    private func refreshTopicTags(_ sessions: [AgentSession]) async {
        let changed = sessions.compactMap { s -> (id: String, version: String)? in
            let version = "\(s.message_count ?? 0):\(s.status)"
            return topicTagVersions[s.id] == version ? nil : (s.id, version)
        }
        guard !changed.isEmpty, let found = await apiClient.getCachedDigests(sessions: changed) else { return }
        var next = topicTags
        var summaries = digestSummaries
        for (id, version) in changed.prefix(100) {
            topicTagVersions[id] = version
            if let digest = found[id] {
                next[id] = digest.tags
                if !digest.summary.isEmpty { summaries[id] = digest.summary }
            }
        }
        if next != topicTags { topicTags = next }
        if summaries != digestSummaries { digestSummaries = summaries }
    }

    /// Coalesce bursts of updates into one list refresh.
    private func scheduleSessionsRefresh() {
        sessionsRefreshTask?.cancel()
        sessionsRefreshTask = Task { [weak self] in
            try? await Task.sleep(for: .milliseconds(600))
            guard !Task.isCancelled else { return }
            await self?.loadSessions()
        }
    }

    func session(_ id: String) -> AgentSession? {
        sessions.first { $0.id == id }
    }

    func status(of sessionId: String) -> SessionStatus {
        SessionStatus(sessionStatus[sessionId] ?? session(sessionId)?.status ?? "idle")
    }

    /// Create a session and wait for the server to confirm it.
    func createSession(
        workingDirectory: String,
        provider: String,
        model: String,
        permissionMode: String,
        tools: [String],
        systemPrompt: String?,
        agentName: String? = nil,
        projectId: String?,
        avatarId: Int64?,
        yoloMode: Bool,
        sandboxed: Bool?,
        effortLevel: String?,
        contextSummary: String? = nil,
        parentSessionId: String? = nil
    ) async throws -> AgentSession {
        await webSocket.connect()
        let id = webSocket.createSession(
            workingDirectory: workingDirectory, modelProvider: provider, model: model,
            permissionMode: permissionMode, tools: tools, systemPrompt: systemPrompt,
            agentName: agentName, projectId: projectId, selectedAvatarId: avatarId,
            contextSummary: contextSummary, parentSessionId: parentSessionId,
            yoloMode: yoloMode, sandboxed: sandboxed, effortLevel: effortLevel
        )
        return try await withCheckedThrowingContinuation { continuation in
            pendingCreates[id] = continuation
            Task { [weak self] in
                try? await Task.sleep(for: .seconds(20))
                guard let self, let pending = self.pendingCreates.removeValue(forKey: id) else { return }
                pending.resume(throwing: EggifyAPIClient.EggifyAPIError.server(0, "The server didn't confirm the new session. Check your connection and try again."))
            }
        }
    }

    /// Returns false (and says so) when it can't reach the server; the
    /// session then stays in the list instead of reappearing later.
    @discardableResult
    func deleteSession(_ sessionId: String) -> Bool {
        guard webSocket.isAuthenticated, webSocket.deleteSession(sessionId) else {
            notice = Notice(message: "Not connected. Try deleting again once the connection is back.", kind: .warning)
            return false
        }
        removeSessionLocally(sessionId)
        EggifyHaptics.success()
        return true
    }

    // MARK: - Opening a session

    /// Show a session: subscribe for live events and load its latest messages.
    func openSession(_ sessionId: String) async {
        selectedSessionId = sessionId
        Task { await loadSubagents(sessionId) }
        Task { await SessionMediaStore.shared.load(sessionId) }
        webSocket.subscribe(sessionId: sessionId)
        if !webSocket.isAuthenticated {
            Task { await webSocket.connect() }
        }
        // The chat's last known messages first, while the latest load.
        if (sessionMessages[sessionId] ?? []).isEmpty {
            let cached = await apiClient.cachedMessages(sessionID: sessionId)
            if (sessionMessages[sessionId] ?? []).isEmpty, !cached.isEmpty {
                sessionMessages[sessionId] = cached
            }
        }
        await loadMessages(sessionId)
        #if DEBUG
        if ProcessInfo.processInfo.arguments.contains("-EggifyDemoRequests") {
            // After the subscribe reply, whose question sync would clear it.
            try? await Task.sleep(for: .seconds(1.5))
            injectDemoRequests(sessionId)
        }
        #endif
    }

    #if DEBUG
    /// UI tests: show an approval card and a question without a live agent.
    private func injectDemoRequests(_ sessionId: String) {
        handleServerEvent(type: "permission_request", payload: [
            "type": "permission_request", "session_id": sessionId, "permission_id": "demo-perm",
            "tool": "Bash", "action": "use_tool",
            "details": ["command": "go test ./internal/server -run TestLogin -count=1", "description": "Run login tests"],
            "description": "Run login tests"
        ])
        handleServerEvent(type: "user_question", payload: [
            "type": "user_question", "session_id": sessionId, "question_id": "demo-q",
            "question": "Should empty passwords return 400 or 422?", "header": "Status code",
            "options": [["label": "400 Bad Request", "description": "Matches the other validation errors"],
                        ["label": "422 Unprocessable", "description": "More precise, but new for this API"]],
            "multi_select": false, "question_index": 1, "question_total": 1,
            "recommended": [0], "held": false,
            "auto_answer_at": EggifyDate.string(from: Date().addingTimeInterval(600))
        ])
    }
    #endif

    func closeSession(_ sessionId: String) {
        webSocket.unsubscribe(sessionId: sessionId)
        if selectedSessionId == sessionId { selectedSessionId = nil }
    }

    /// Kept for older call sites.
    func selectSession(_ sessionId: String) {
        Task { await openSession(sessionId) }
    }

    /// Load (or reload) the latest page of messages, keeping unsent local ones.
    func loadMessages(_ sessionId: String) async {
        loadingMessages.insert(sessionId)
        defer { loadingMessages.remove(sessionId) }
        do {
            let page = try await apiClient.getSessionMessages(sessionID: sessionId, limit: 60)
            let existing = sessionMessages[sessionId] ?? []
            var merged = page.messages
            // Keep local-only entries (optimistic prompts, live-only notices)
            // that the server page doesn't contain yet.
            let serverUserTexts = Set(page.messages.filter(\.isUser).map { $0.content.textContent })
            let lastServerDate = page.messages.last?.createdDate ?? .distantPast
            for message in existing where message.id.hasPrefix("local-") || message.role == "result" {
                if message.isUser && serverUserTexts.contains(message.content.textContent) { continue }
                if (message.createdDate ?? .distantPast) >= lastServerDate {
                    merged.append(message)
                }
            }
            merged.attachToolImages(knownToolImages(sessionId))
            sessionMessages[sessionId] = merged
            hasMoreMessages[sessionId] = page.hasMore
            messageLoadErrors[sessionId] = nil
        } catch is CancellationError {
        } catch {
            messageLoadErrors[sessionId] = error.localizedDescription
        }
    }

    /// Page backwards. Returns the number of messages added.
    @discardableResult
    func loadEarlierMessages(_ sessionId: String) async -> Int {
        guard hasMoreMessages[sessionId] == true,
              let firstSequence = sessionMessages[sessionId]?.compactMap(\.sequence).min(),
              !loadingMessages.contains(sessionId) else { return 0 }
        loadingMessages.insert(sessionId)
        defer { loadingMessages.remove(sessionId) }
        do {
            let page = try await apiClient.getSessionMessages(sessionID: sessionId, limit: 60, beforeSequence: firstSequence)
            let known = Set((sessionMessages[sessionId] ?? []).map(\.id))
            var older = page.messages.filter { !known.contains($0.id) }
            older.attachToolImages(knownToolImages(sessionId))
            sessionMessages[sessionId] = older + (sessionMessages[sessionId] ?? [])
            hasMoreMessages[sessionId] = page.hasMore
            return older.count
        } catch {
            notice = Notice(message: "Couldn't load earlier messages: \(error.localizedDescription)", kind: .error)
            return 0
        }
    }

    // MARK: - Actions

    func sendPrompt(_ text: String, images: [(mediaType: String, base64: String)] = [], to sessionId: String) {
        let trimmed = text.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmed.isEmpty || !images.isEmpty else { return }

        var blocks: [ContentBlock] = images.map {
            ContentBlock(type: "image", text: nil, source: ContentBlockSource(type: "base64", media_type: $0.mediaType, data: $0.base64))
        }
        if !trimmed.isEmpty { blocks.append(ContentBlock(type: "text", text: trimmed, source: nil)) }

        var optimistic = AgentMessage(
            id: "local-\(UUID().uuidString)", role: "user",
            content: images.isEmpty ? .string(trimmed) : .blocks(blocks),
            created_at: EggifyDate.string(), thinking: nil, tools: nil
        )
        optimistic.isPending = true
        append(optimistic, to: sessionId)
        setStatus("processing", for: sessionId)
        lastSendAt[sessionId] = Date()

        if !webSocket.sendPrompt(sessionId, prompt: trimmed, images: images), !webSocket.isAuthenticated {
            notice = Notice(message: "Not connected — your message will be sent when the connection is back.", kind: .warning)
        }
    }

    /// Kept for older call sites (Zen view).
    func sendMessage(_ prompt: String) {
        guard let id = selectedSessionId else { return }
        sendPrompt(prompt, to: id)
    }

    func interrupt(_ sessionId: String) {
        _ = webSocket.interruptSession(sessionId)
    }

    /// Ends the loop; a turn in progress finishes, no new iteration starts.
    func stopLoop(_ sessionId: String) {
        guard webSocket.isAuthenticated, webSocket.stopLoop(sessionId) else {
            notice = Notice(message: "Not connected. Try again once the connection is back.", kind: .warning)
            return
        }
    }

    func respond(to request: PermissionRequest, approved: Bool) {
        // Only remove the card once the answer is on its way. A queued answer
        // would arrive after the re-subscribe on reconnect, which drops the
        // server's pending approvals, so it would be lost.
        guard webSocket.isAuthenticated,
              webSocket.respondToPermission(sessionId: request.sessionId, permissionId: request.id, approved: approved) else {
            notice = Notice(message: "Not connected. Answer again once the connection is back.", kind: .warning)
            return
        }
        removePermission(request.id, sessionId: request.sessionId)
        EggifyHaptics.success()
    }

    /// Approves the request and adds an always-allow rule for this session:
    /// for requests like it (`similar`) or exactly this one. The server
    /// approves the waiting request itself when the rule names it.
    func alwaysAllow(_ request: PermissionRequest, similar: Bool) {
        var rule: [String: Any] = [
            "tool": request.tool,
            "match_mode": similar ? "pattern" : "exact",
            "description": request.description,
        ]
        if similar {
            guard let pattern = AlwaysAllowPattern.make(tool: request.tool, details: request.details) else {
                respond(to: request, approved: true)
                return
            }
            rule["pattern"] = pattern
        } else {
            rule["parameters"] = request.details?.anyValue ?? [String: Any]()
        }
        guard webSocket.isAuthenticated,
              webSocket.send(["type": "add_always_allow_rule", "session_id": request.sessionId,
                              "rule": rule, "permission_id": request.id]) else {
            notice = Notice(message: "Not connected. Answer again once the connection is back.", kind: .warning)
            return
        }
        removePermission(request.id, sessionId: request.sessionId)
        let what = similar
            ? AlwaysAllowPattern.summary(tool: request.tool, pattern: AlwaysAllowPattern.make(tool: request.tool, details: request.details) ?? [:])
            : request.description
        append(.local(role: "system", text: "🔓 Always allowed in this session: \(what)"), to: request.sessionId)
        EggifyHaptics.success()
    }

    func answer(_ question: UserQuestion, with answers: [String]) {
        removeQuestion(question.id, sessionId: question.sessionId)
        _ = webSocket.answerQuestion(sessionId: question.sessionId, questionId: question.id, answers: answers)
        EggifyHaptics.success()
    }

    func dismiss(_ question: UserQuestion) {
        removeQuestion(question.id, sessionId: question.sessionId)
        _ = webSocket.dismissQuestion(sessionId: question.sessionId, questionId: question.id)
    }

    func answerLater(_ question: UserQuestion) {
        deferredQuestionIds.insert(question.id)
    }

    /// "Keep waiting": the question stops counting down and waits for the user.
    func holdQuestion(_ question: UserQuestion) {
        guard webSocket.holdQuestion(sessionId: question.sessionId, questionId: question.id) else {
            notice = Notice(message: "Not connected. Try again once the connection is back.", kind: .warning)
            return
        }
        EggifyHaptics.select()
        // The server confirms with user_question_timer; don't wait for it.
        updateQuestion(question.id, sessionId: question.sessionId) {
            $0.held = true
            $0.autoAnswerAt = nil
        }
    }

    // MARK: - Question policy

    /// The global policy (quietly; older servers don't have it).
    @discardableResult
    func loadQuestionPolicyDefault() async -> Bool {
        guard let result = try? await apiClient.getQuestionPolicyDefault() else { return false }
        applyQuestionPolicyDefault(result.policy)
        return true
    }

    /// Sets the global policy. Returns false (and shows why) when refused.
    @discardableResult
    func setQuestionPolicyDefault(_ policy: QuestionPolicy) async -> Bool {
        do {
            applyQuestionPolicyDefault(try await apiClient.setQuestionPolicyDefault(policy))
            return true
        } catch {
            showPolicyError(error)
            return false
        }
    }

    /// Sets (nil: clears) a session's own question policy.
    func setQuestionPolicy(_ sessionId: String, _ policy: QuestionPolicy?) async {
        do {
            let result = try await apiClient.setSessionQuestionPolicy(sessionID: sessionId, policy: policy)
            applySessionQuestionPolicy(sessionId, policy: result.policy, effective: result.effective)
            EggifyHaptics.success()
        } catch {
            showPolicyError(error)
        }
    }

    private func showPolicyError(_ error: Error) {
        if case EggifyAPIClient.EggifyAPIError.userRequired(let message) = error {
            notice = Notice(message: message, kind: .signInRequired)
        } else {
            notice = Notice(message: "Couldn't change the question setting: \(error.localizedDescription)", kind: .error)
        }
    }

    private func applyQuestionPolicyDefault(_ policy: QuestionPolicy) {
        if questionPolicyDefault != policy { questionPolicyDefault = policy }
        for i in sessions.indices where sessions[i].question_policy == nil
            && sessions[i].question_policy_effective != policy {
            sessions[i].question_policy_effective = policy
        }
    }

    private func applySessionQuestionPolicy(_ sessionId: String, policy: QuestionPolicy?, effective: QuestionPolicy?) {
        guard let index = sessions.firstIndex(where: { $0.id == sessionId }) else { return }
        var session = sessions[index]
        session.question_policy = policy
        session.question_policy_effective = effective ?? policy ?? questionPolicyDefault ?? session.question_policy_effective
        if session != sessions[index] { sessions[index] = session }
    }

    // MARK: Tags

    private var tagWrites: [String: Int] = [:]
    /// Tags shown but not saved yet, per session.
    private var unsavedTags: [String: [String]] = [:]

    /// Every tag the user has put on a session, most used first.
    var knownTags: [String] { SessionTags.known(sessions.map(\.tags)) }

    /// Replaces a session's own tags: shown at once, then saved. Only the
    /// newest write's answer is applied, so quick edits can't come back out of
    /// order; a failed save puts the server's last list back.
    func setTags(_ sessionId: String, _ tags: [String]) {
        guard let index = sessions.firstIndex(where: { $0.id == sessionId }) else { return }
        let previous = sessions[index].tags
        let tags = SessionTags.normalize(tags)
        guard tags != previous else { return }
        sessions[index].tags = tags
        let write = (tagWrites[sessionId] ?? 0) + 1
        tagWrites[sessionId] = write
        unsavedTags[sessionId] = tags
        Task {
            do {
                let saved = try await apiClient.setSessionTags(sessionID: sessionId, tags: tags)
                guard tagWrites[sessionId] == write else { return }
                unsavedTags[sessionId] = nil
                applyTags(sessionId, saved)
            } catch {
                guard tagWrites[sessionId] == write else { return }
                unsavedTags[sessionId] = nil
                applyTags(sessionId, previous)
                if case EggifyAPIClient.EggifyAPIError.userRequired(let message) = error {
                    notice = Notice(message: message, kind: .signInRequired)
                } else {
                    notice = Notice(message: "Couldn't save the tags: \(error.localizedDescription)", kind: .error)
                }
            }
        }
    }

    private func applyTags(_ sessionId: String, _ tags: [String]) {
        guard let index = sessions.firstIndex(where: { $0.id == sessionId }),
              sessions[index].tags != tags else { return }
        sessions[index].tags = tags
    }

    func changeModel(_ sessionId: String, provider: String, model: String) {
        guard status(of: sessionId).isActive == false else {
            notice = Notice(message: "Wait for the current turn to finish (or stop it) before switching models.", kind: .warning)
            return
        }
        _ = webSocket.changeModel(sessionId: sessionId, provider: provider, model: model)
    }

    func setYOLO(_ sessionId: String, enabled: Bool) {
        _ = webSocket.setYOLOMode(sessionId: sessionId, enabled: enabled)
    }

    func reconnect() {
        webSocket.reconnectNow()
    }

    // MARK: - Derived

    func visibleQuestions(for sessionId: String) -> [UserQuestion] {
        (pendingQuestions[sessionId] ?? []).filter { !deferredQuestionIds.contains($0.id) }
    }

    /// Sessions with a request waiting for this device or flagged by the server.
    var sessionsNeedingAttention: [AgentSession] {
        sessions.filter { s in
            s.needsAttention || !(pendingPermissions[s.id] ?? []).isEmpty || !(pendingQuestions[s.id] ?? []).isEmpty
        }
    }

    // MARK: - WebSocketEventHandler

    func onConnectionStatusChanged(_ isConnected: Bool) {
        if isConnected {
            // Catch up on anything missed while offline.
            scheduleSessionsRefresh()
            if let open = selectedSessionId {
                Task { await loadMessages(open) }
            }
        }
    }

    func handleServerEvent(type: String, payload: [String: Any]) {
        let sessionId = payload.nonEmptyString("session_id")

        switch type {
        case "session_created":
            guard let data = payload.dict("session") ?? (sessionId.map { ["id": $0, "status": payload.string("status") ?? "idle"] }),
                  let session = AgentSession.parse(data) else { return }
            upsert(session)
            setStatus(session.status, for: session.id)
            if let continuation = pendingCreates.removeValue(forKey: session.id) {
                continuation.resume(returning: session)
            }

        case "session_updated":
            guard let sessionId else { return }
            if let status = payload.string("status") { setStatus(status, for: sessionId) }
            if let index = sessions.firstIndex(where: { $0.id == sessionId }) {
                var data = payload.dict("session") ?? [:]
                if data.isEmpty {
                    if let status = payload.string("status") { data["status"] = status }
                    if let branch = payload.string("git_branch") { data["git_branch"] = branch }
                }
                sessions[index] = sessions[index].merged(with: data)
            } else if let data = payload.dict("session"), let session = AgentSession.parse(data) {
                upsert(session)
            } else {
                scheduleSessionsRefresh()
            }

        case "agent_message":
            guard let sessionId, let message = AgentMessage.parse(payload) else { return }
            let images = message.toolResultImages
            if !images.isEmpty {
                rememberToolImages(images, sessionId: sessionId)
                var list = sessionMessages[sessionId] ?? []
                if list.attachToolImages(images) { sessionMessages[sessionId] = list }
                return
            }
            if message.isUser {
                replacePendingOrAppend(message, sessionId: sessionId)
            } else {
                append(message, to: sessionId)
            }
            // A render or show_media (or a finished turn): look for new media.
            SessionMediaStore.shared.noteLiveMessage(message, sessionId: sessionId)
            if message.role == "result" {
                // Turn finished; the session_updated that follows sets the
                // status, but don't leave the spinner up if it is late.
                if SessionStatus(sessionStatus[sessionId] ?? "").isActive {
                    setStatus("idle", for: sessionId)
                }
                markPendingConfirmed(sessionId)
            } else if !message.isUser {
                setStatus("processing", for: sessionId)
                markPendingConfirmed(sessionId)
            }

        case "agent_error":
            guard let sessionId else { return }
            let content = payload.dict("content") ?? [:]
            let text = payload.nonEmptyString("error") ?? content.nonEmptyString("error") ?? "The agent hit an error"
            append(.local(role: "error", text: text), to: sessionId)
            setStatus("error", for: sessionId)
            markPendingConfirmed(sessionId)

        case "agent_thinking", "agent_tool_use":
            if let sessionId { setStatus("processing", for: sessionId) }

        case "permission_request":
            guard let request = PermissionRequest.parse(payload) else { return }
            var list = pendingPermissions[request.sessionId] ?? []
            guard !list.contains(where: { $0.id == request.id }) else { return }
            list.append(request)
            pendingPermissions[request.sessionId] = list
            EggifyHaptics.warning()
            announceIfElsewhere(request.sessionId, "needs approval to use \(request.tool)",
                                name: payload.nonEmptyString("session_name"))

        case "permission_acknowledged":
            if let sessionId, let id = payload.nonEmptyString("permission_id") {
                removePermission(id, sessionId: sessionId)
            }

        case "user_question":
            guard let question = UserQuestion.parse(payload) else { return }
            var list = pendingQuestions[question.sessionId] ?? []
            if let index = list.firstIndex(where: { $0.id == question.id }) {
                list[index] = question
            } else {
                list.append(question)
                EggifyHaptics.warning()
                announceIfElsewhere(question.sessionId,
                                    question.showsCountdown ? question.bannerText() : "has a question for you",
                                    name: question.sessionName)
            }
            pendingQuestions[question.sessionId] = list

        case "user_question_timer":
            guard let sessionId, let id = payload.nonEmptyString("question_id") else { return }
            updateQuestion(id, sessionId: sessionId) { $0 = $0.withTimer(payload) }

        case "user_question_resolved", "user_question_acknowledged":
            if let sessionId, let id = payload.nonEmptyString("question_id") {
                let wasWaiting = pendingQuestions[sessionId]?.contains { $0.id == id } == true
                removeQuestion(id, sessionId: sessionId)
                if wasWaiting, payload.string("reason") == "auto_answered" {
                    let picked = payload.strings("answer").map(UserQuestion.strippedLabel)
                    let what = picked.isEmpty ? "an answer" : picked.map { "“\($0)”" }.joined(separator: ", ")
                    let name = session(sessionId)?.displayTitle ?? "A session"
                    notice = Notice(message: sessionId == selectedSessionId
                                    ? "Auto-picked \(what)" : "\(name): auto-picked \(what)", kind: .info)
                }
            }

        case "session_question_policy_changed":
            guard let sessionId else { return }
            applySessionQuestionPolicy(sessionId, policy: QuestionPolicy.parse(payload["policy"]),
                                       effective: QuestionPolicy.parse(payload["effective"]))

        case "question_policy_default_changed":
            if let policy = QuestionPolicy.parse(payload["policy"]) { applyQuestionPolicyDefault(policy) }

        case "user_questions_sync":
            guard let sessionId else { return }
            let stillWaiting = Set(payload.strings("question_ids"))
            pendingQuestions[sessionId] = (pendingQuestions[sessionId] ?? []).filter { stillWaiting.contains($0.id) }

        case "session_interrupted":
            guard let sessionId else { return }
            setStatus("idle", for: sessionId)
            pendingPermissions[sessionId] = []
            append(.local(role: "system", text: "Stopped"), to: sessionId)
            markPendingConfirmed(sessionId)

        case "session_ended":
            if let sessionId { setStatus("ended", for: sessionId) }

        case "session_deleted":
            if let sessionId { removeSessionLocally(sessionId) }

        case "all_sessions_deleted":
            scheduleSessionsRefresh()

        case "background_agent_started":
            guard let sessionId, let agent = payload.dict("agent").flatMap(Subagent.parse) else { return }
            updateSubagent(agent.id, in: sessionId, insert: agent) { $0 = agent }

        case "background_agent_progress":
            guard let sessionId, let agentId = payload.nonEmptyString("agent_id") else { return }
            updateSubagent(agentId, in: sessionId) { agent in
                if let status = payload.string("status").flatMap(Subagent.Status.init(rawValue:)) {
                    agent.status = status
                    // A finished agent that was messaged runs again (the server reopens it).
                    if status == .running {
                        agent.completedAt = nil
                        agent.errorMessage = nil
                    }
                }
                if let progress = payload.double("progress") { agent.progress = progress }
                if let output = payload.nonEmptyString("output") { agent.append([output]) }
            }

        case "background_agent_output":
            guard let sessionId, let agentId = payload.nonEmptyString("agent_id") else { return }
            let lines = payload.strings("lines")
            updateSubagent(agentId, in: sessionId) { agent in
                agent.append(lines.isEmpty ? [payload.string("output") ?? ""] : lines)
            }

        case "background_agent_completed":
            guard let sessionId, let agentId = payload.nonEmptyString("agent_id") else { return }
            updateSubagent(agentId, in: sessionId) { agent in
                agent.status = .completed
                agent.progress = 1
                agent.completedAt = EggifyDate.parse(payload.string("completed_at")) ?? Date()
            }

        case "background_agent_failed":
            guard let sessionId, let agentId = payload.nonEmptyString("agent_id") else { return }
            updateSubagent(agentId, in: sessionId) { agent in
                agent.status = .failed
                agent.errorMessage = payload.nonEmptyString("error_message") ?? agent.errorMessage
                agent.completedAt = Date()
            }

        case "background_agents_list":
            guard let sessionId else { return }
            let list = payload.dicts("agents").compactMap(Subagent.parse)
            if subagents[sessionId] != list { subagents[sessionId] = list }

        case "loop_started", "loop_verifying", "loop_iteration", "loop_completed", "loop_failed", "loop_stopped":
            guard let sessionId, let event = LoopState.parse(payload, type: type),
                  let index = sessions.firstIndex(where: { $0.id == sessionId }) else { return }
            let next = sessions[index].loop?.followed(by: event) ?? event
            guard next != sessions[index].loop || !sessions[index].loop_mode else { return }
            sessions[index].loop = next
            sessions[index].loop_mode = true
            switch event.phase {
            case .completed:
                EggifyHaptics.success()
                announceIfElsewhere(sessionId, "finished its loop")
            case .failed:
                EggifyHaptics.warning()
                announceIfElsewhere(sessionId, "gave up on its loop")
            default:
                break
            }

        case "session_model_changed":
            guard let sessionId else { return }
            if let index = sessions.firstIndex(where: { $0.id == sessionId }) {
                sessions[index].model_name = payload.nonEmptyString("model") ?? sessions[index].model_name
                sessions[index].provider = payload.nonEmptyString("provider") ?? sessions[index].provider
            }
            let text = payload.nonEmptyString("message")
                ?? "Switched to \(payload.string("model") ?? "a new model")"
            append(.local(role: "system", text: text), to: sessionId)
            notice = Notice(message: text, kind: .info)

        case "session_skills_updated":
            // Skills were added or removed (here, on the desktop or by an agent).
            guard let sessionId else { return }
            SessionSkillsStore.shared.noteUpdated(sessionId)

        case "yolo_mode_toggled":
            guard let sessionId, let index = sessions.firstIndex(where: { $0.id == sessionId }) else { return }
            sessions[index].yolo = payload.bool("enabled") ?? sessions[index].yolo

        case "error":
            let message = payload.nonEmptyString("message") ?? payload.nonEmptyString("error") ?? "Something went wrong"
            if payload.string("code") == "user_required" {
                notice = Notice(message: message, kind: .signInRequired)
            } else {
                notice = Notice(message: message, kind: .error)
            }
            // A failed or refused create (e.g. user_required for YOLO or a
            // sandbox opt-out) comes back as a plain error without the
            // session id; while a create is waiting, it is that create's.
            if !pendingCreates.isEmpty {
                for (_, continuation) in pendingCreates {
                    continuation.resume(throwing: EggifyAPIClient.EggifyAPIError.server(0, message))
                }
                pendingCreates = [:]
            }

        default:
            break
        }
    }

    // MARK: - Subagents

    /// Loads a session's subagents (with their recent output) over REST.
    func loadSubagents(_ sessionId: String) async {
        guard !loadingSubagents.contains(sessionId) else { return }
        loadingSubagents.insert(sessionId)
        defer { loadingSubagents.remove(sessionId) }
        guard let list = try? await apiClient.getSubagents(sessionID: sessionId) else { return }
        if subagents[sessionId] != list { subagents[sessionId] = list }
    }

    /// Sessions with a subagent load in flight (one at a time per session).
    private var loadingSubagents: Set<String> = []

    /// Applies `change` to one subagent. An unknown agent is inserted when
    /// `insert` is given, otherwise the list is reloaded (an event can arrive
    /// before the app has seen the agent start).
    private func updateSubagent(_ agentId: String, in sessionId: String, insert: Subagent? = nil,
                                _ change: (inout Subagent) -> Void) {
        var list = subagents[sessionId] ?? []
        if let index = list.firstIndex(where: { $0.id == agentId }) {
            change(&list[index])
        } else if var agent = insert {
            change(&agent)
            list.append(agent)
        } else {
            Task { await loadSubagents(sessionId) }
            return
        }
        subagents[sessionId] = list
    }

    // MARK: - Mutation helpers

    private func upsert(_ session: AgentSession) {
        if let index = sessions.firstIndex(where: { $0.id == session.id }) {
            sessions[index] = session
        } else {
            sessions.insert(session, at: 0)
        }
    }

    private func removeSessionLocally(_ sessionId: String) {
        sessions.removeAll { $0.id == sessionId }
        sessionMessages[sessionId] = nil
        toolResultImages[sessionId] = nil
        sessionStatus[sessionId] = nil
        pendingPermissions[sessionId] = nil
        pendingQuestions[sessionId] = nil
        if selectedSessionId == sessionId { selectedSessionId = nil }
    }

    private func rememberToolImages(_ images: [String: ImageBlockData], sessionId: String) {
        var list = toolResultImages[sessionId] ?? []
        for (id, image) in images where !list.contains(where: { $0.id == id }) {
            list.append((id, image))
        }
        toolResultImages[sessionId] = Array(list.suffix(Self.maxToolResultImages))
    }

    private func knownToolImages(_ sessionId: String) -> [String: ImageBlockData] {
        Dictionary((toolResultImages[sessionId] ?? []).map { ($0.id, $0.image) }, uniquingKeysWith: { first, _ in first })
    }

    private func append(_ message: AgentMessage, to sessionId: String) {
        var list = sessionMessages[sessionId] ?? []
        if let index = list.firstIndex(where: { $0.id == message.id }) {
            list[index] = message
        } else {
            list.append(message)
        }
        sessionMessages[sessionId] = list
    }

    /// A user message from another device, or the server's copy of ours.
    private func replacePendingOrAppend(_ message: AgentMessage, sessionId: String) {
        var list = sessionMessages[sessionId] ?? []
        if let index = list.firstIndex(where: { $0.isPending && $0.content.textContent == message.content.textContent }) {
            list[index] = message
            sessionMessages[sessionId] = list
        } else {
            append(message, to: sessionId)
        }
    }

    /// The agent responded, so our prompts arrived.
    private func markPendingConfirmed(_ sessionId: String) {
        guard var list = sessionMessages[sessionId], list.contains(where: \.isPending) else { return }
        for i in list.indices where list[i].isPending { list[i].isPending = false }
        sessionMessages[sessionId] = list
    }

    private func removePermission(_ id: String, sessionId: String) {
        guard var list = pendingPermissions[sessionId] else { return }
        list.removeAll { $0.id == id }
        pendingPermissions[sessionId] = list
    }

    private func removeQuestion(_ id: String, sessionId: String) {
        deferredQuestionIds.remove(id)
        guard var list = pendingQuestions[sessionId] else { return }
        list.removeAll { $0.id == id }
        pendingQuestions[sessionId] = list
    }

    private func updateQuestion(_ id: String, sessionId: String, _ change: (inout UserQuestion) -> Void) {
        guard var list = pendingQuestions[sessionId], let index = list.firstIndex(where: { $0.id == id }) else { return }
        change(&list[index])
        pendingQuestions[sessionId] = list
    }

    private func announceIfElsewhere(_ sessionId: String, _ what: String, name: String? = nil) {
        guard sessionId != selectedSessionId else { return }
        let name = session(sessionId)?.displayTitle ?? name ?? "A session"
        notice = Notice(message: "\(name) \(what)", kind: .warning)
    }
}

// MARK: - Preview Helper

#if DEBUG
extension SessionsWebSocketViewModel {
    static var preview: SessionsWebSocketViewModel { .shared }

    /// `-EggifyZenPager`: three sessions for the Zen pager without a server
    /// (one needs you, one works, one went idle a few minutes ago).
    func injectZenPagerDemo() {
        let now = Date()
        func at(_ minutesAgo: Double) -> String { EggifyDate.string(from: now.addingTimeInterval(-minutesAgo * 60)) }
        let demo: [(id: String, name: String, status: String, minutes: Double, prompt: String, reply: String)] = [
            ("demo-pager-1", "Ministry of Silly Walks", "processing", 1,
             "Add the walking permit form", "**Right.** The permit form now asks for the *silliness* of the walk."),
            ("demo-pager-2", "Fix login bug", "processing", 2,
             "Empty passwords should be rejected", "Empty passwords now return **400**, with a test for each case."),
            ("demo-pager-3", "Cheese shop inventory", "idle", 6,
             "Do we have any Red Leicester?", "I'm afraid we're fresh out of Red Leicester, sir."),
        ]
        sessions = demo.map {
            AgentSession(id: $0.id, status: $0.status, working_directory: "/Users/demo/\($0.name)",
                         created_at: at($0.minutes + 30), updated_at: at($0.minutes), message_count: 2,
                         agent_name: $0.name)
        }
        for d in demo {
            sessionStatus[d.id] = d.status
            sessionMessages[d.id] = [
                AgentMessage(id: "\(d.id)-u", role: "user", content: .string(d.prompt),
                             created_at: at(d.minutes + 1), thinking: nil, tools: nil),
                AgentMessage(id: "\(d.id)-a", role: "assistant", content: .string(d.reply),
                             created_at: at(d.minutes), thinking: nil, tools: nil),
            ]
        }
        handleServerEvent(type: "permission_request", payload: [
            "type": "permission_request", "session_id": "demo-pager-1", "permission_id": "demo-pager-perm",
            "tool": "Bash", "action": "use_tool",
            "details": ["command": "go test ./internal/walks", "description": "Run the walk tests"],
            "description": "Run the walk tests"
        ])
    }
}
#endif
