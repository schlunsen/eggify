//
//  ChatViewModel.swift
//  eggify
//
//  State for one open chat, derived from the shared session store.
//

import SwiftUI
import Combine
import PhotosUI

/// One row in the chat list.
nonisolated enum ChatItem: Identifiable, Equatable, Sendable {
    case message(AgentMessage)
    /// Several consecutive agent steps that only ran tools.
    case activity(id: String, messages: [AgentMessage])

    var id: String {
        switch self {
        case .message(let m): return m.id
        case .activity(let id, _): return id
        }
    }

    /// Minimum run of tool-only steps that gets folded into one row.
    static let foldThreshold = 3

    /// `media`: the session's media list; each item is put on the step whose
    /// tool call named it, and a step with a player is never folded away.
    static func build(from messages: [AgentMessage], media: [SessionMedia] = []) -> [ChatItem] {
        let messages = messages.attachingMedia(media)
        var items: [ChatItem] = []
        var run: [AgentMessage] = []

        func flush() {
            if run.count >= foldThreshold {
                items.append(.activity(id: "activity-\(run[0].id)", messages: run))
            } else {
                items.append(contentsOf: run.map(ChatItem.message))
            }
            run.removeAll()
        }

        for message in messages where !message.isEmptyForDisplay {
            if isToolOnlyStep(message) {
                run.append(message)
            } else {
                flush()
                items.append(.message(message))
            }
        }
        flush()
        return items
    }

    static func isToolOnlyStep(_ m: AgentMessage) -> Bool {
        m.role == "assistant"
            && m.hasTools
            && m.content.textContent.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
            && m.toolImages.isEmpty
            && m.media.isEmpty
    }
}

/// An image picked for the next prompt.
nonisolated struct ComposerAttachment: Identifiable, Equatable, @unchecked Sendable {
    let id = UUID()
    let thumbnail: UIImage
    let mediaType: String
    let base64: String
}

@MainActor
final class ChatViewModel: ObservableObject {
    // MARK: Identity
    let sessionID: String
    let sessionTitle: String

    // MARK: Derived from the store
    @Published private(set) var messages: [AgentMessage] = []
    @Published private(set) var items: [ChatItem] = []
    /// Bumped whenever the user sends a prompt, so the chat follows the reply.
    @Published private(set) var sendCount = 0
    @Published private(set) var status: SessionStatus = .idle
    @Published private(set) var session: AgentSession?
    @Published private(set) var permissions: [PermissionRequest] = []
    @Published private(set) var questions: [UserQuestion] = []
    /// Every waiting question, including ones put off with Later (live: timers change).
    @Published private(set) var allQuestions: [UserQuestion] = []
    /// The global question policy, once known.
    @Published private(set) var globalQuestionPolicy: QuestionPolicy?
    @Published private(set) var waitingQuestionCount = 0
    @Published private(set) var hasMore = false
    @Published private(set) var isLoadingMessages = false
    @Published private(set) var loadError: String?
    @Published private(set) var connection: WebSocketConnectionState = .disconnected
    /// Subagent counts only; their output is observed by the sheet.
    @Published private(set) var subagentsRunning = 0
    @Published private(set) var subagentsTotal = 0
    @Published var showSubagents = false
    /// Audio/video files of the session (the Media sheet lists them).
    @Published private(set) var mediaCount = 0
    @Published var showMedia = false
    /// Skills the session gets, once its Skills sheet has loaded them (0 before).
    @Published private(set) var skillsCount = 0
    @Published var showSkills = false
    /// Use was tapped in the Skills sheet: focus the composer once it closes.
    var focusComposerAfterSkills = false
    @Published var showLoopDetails = false
    /// Where this session's workers are; nil unless it is a team or has workers.
    @Published private(set) var team: TeamProgress?
    @Published var showTeam = false
    /// A worker chat's line under the header ("Build worker of Shop · worker/abc").
    @Published private(set) var workerLine: String?
    /// The session that started this worker, when it is in the list.
    @Published private(set) var parentSession: AgentSession?

    // MARK: Local UI state
    /// Text and attachments being written. Its own object, so typing does
    /// not re-render the conversation.
    let composer: ComposerModel
    @Published var detailMessage: AgentMessage?
    @Published var viewerImage: ImageBlockData?
    @Published var showModelPicker = false
    @Published var showTags = false
    @Published private(set) var didInitialLoad = false

    // Navigation
    @Published var navigateToJust = false
    @Published var navigateToGit = false
    @Published var navigateToStats = false
    @Published var navigateToZen = false

    let store: SessionsWebSocketViewModel
    let mediaStore: SessionMediaStore
    private let avatarManager: AvatarManager
    private var cancellables = Set<AnyCancellable>()

    var projectId: String? { session?.project_id }

    init(sessionID: String, sessionTitle: String,
         store: SessionsWebSocketViewModel? = nil,
         avatarManager: AvatarManager? = nil) {
        self.sessionID = sessionID
        self.sessionTitle = sessionTitle
        self.store = store ?? .shared
        self.mediaStore = .shared
        self.avatarManager = avatarManager ?? .shared
        self.composer = ComposerModel(draftKey: sessionID)
        bind()
    }

    private func bind() {
        let id = sessionID
        store.$sessionMessages
            .map { $0[id] ?? [] }
            .removeDuplicates()
            .combineLatest(mediaStore.$lists.map { $0[id]?.items ?? [] }.removeDuplicates())
            .sink { [weak self] messages, media in
                self?.messages = messages
                self?.items = ChatItem.build(from: messages, media: media)
            }
            .store(in: &cancellables)

        mediaStore.$lists
            .map { $0[id]?.items.count ?? 0 }
            .removeDuplicates()
            .sink { [weak self] in self?.mediaCount = $0 }
            .store(in: &cancellables)

        SessionSkillsStore.shared.$lists
            .map { $0[id]?.active.count ?? 0 }
            .removeDuplicates()
            .sink { [weak self] in self?.skillsCount = $0 }
            .store(in: &cancellables)

        store.$sessionStatus
            .map { $0[id] }
            .removeDuplicates()
            .sink { [weak self] raw in
                guard let self else { return }
                self.status = SessionStatus(raw ?? self.session?.status ?? "idle")
            }
            .store(in: &cancellables)

        store.$sessions
            .map { $0.first { $0.id == id } }
            .removeDuplicates()
            .sink { [weak self] in self?.session = $0 }
            .store(in: &cancellables)

        // Workers and the parent are other sessions: follow the whole list.
        store.$sessions.combineLatest(store.$sessionStatus)
            .sink { [weak self] sessions, statuses in
                guard let self else { return }
                let own = sessions.first { $0.id == id }
                let team = own.flatMap {
                    TeamProgress.compute(parent: $0, sessions: sessions,
                                         status: { SessionStatus(statuses[$0.id] ?? $0.status) })
                }
                if self.team != team { self.team = team }
                let parent = own?.worker == nil ? nil
                    : own?.spawned_by_session_id.flatMap { pid in sessions.first { $0.id == pid } }
                if self.parentSession != parent { self.parentSession = parent }
                let line = own?.workerLine(parentName: parent?.displayTitle)
                if self.workerLine != line { self.workerLine = line }
            }
            .store(in: &cancellables)

        store.$pendingPermissions
            .map { $0[id] ?? [] }
            .removeDuplicates()
            .sink { [weak self] in self?.permissions = $0 }
            .store(in: &cancellables)

        store.$pendingQuestions.combineLatest(store.$deferredQuestionIds)
            .map { questions, deferred -> ([UserQuestion], [UserQuestion]) in
                let all = questions[id] ?? []
                return (all.filter { !deferred.contains($0.id) }, all)
            }
            .sink { [weak self] visible, all in
                guard let self else { return }
                if self.questions != visible { self.questions = visible }
                if self.allQuestions != all { self.allQuestions = all }
                if self.waitingQuestionCount != all.count { self.waitingQuestionCount = all.count }
            }
            .store(in: &cancellables)

        store.$questionPolicyDefault.removeDuplicates()
            .sink { [weak self] in self?.globalQuestionPolicy = $0 }.store(in: &cancellables)
        store.$hasMoreMessages.map { $0[id] ?? false }.removeDuplicates()
            .sink { [weak self] in self?.hasMore = $0 }.store(in: &cancellables)
        store.$loadingMessages.map { $0.contains(id) }.removeDuplicates()
            .sink { [weak self] in self?.isLoadingMessages = $0 }.store(in: &cancellables)
        store.$messageLoadErrors.map { $0[id] }.removeDuplicates()
            .sink { [weak self] in self?.loadError = $0 }.store(in: &cancellables)
        store.$subagents
            .map { all -> [Int] in
                let list = all[id] ?? []
                return [list.filter { $0.status.isActive }.count, list.count]
            }
            .removeDuplicates()
            .sink { [weak self] counts in
                self?.subagentsRunning = counts[0]
                self?.subagentsTotal = counts[1]
            }
            .store(in: &cancellables)
        store.$connectionState.removeDuplicates()
            .sink { [weak self] in self?.connection = $0 }.store(in: &cancellables)
    }

    // MARK: Lifecycle

    private var isOpen = false

    /// Runs on every appearance (also when coming back from a pushed screen).
    func onAppear() async {
        store.selectedSessionId = sessionID
        if session == nil, store.sessions.isEmpty {
            Task { await store.loadSessions() }
        }
        if isOpen {
            await store.loadMessages(sessionID)
        } else {
            // Subscribe once per chat: the server drops pending approvals
            // whenever a session is subscribed again.
            isOpen = true
            await store.openSession(sessionID)
        }
        didInitialLoad = true
    }

    isolated deinit {
        if isOpen { store.closeSession(sessionID) }
    }

    func refresh() async {
        async let messages: Void = store.loadMessages(sessionID)
        async let media: Void = mediaStore.load(sessionID)
        _ = await (messages, media)
    }

    /// Returns the id of the item that was first before loading, to keep the scroll position.
    func loadEarlier() async -> String? {
        let anchor = items.first?.id
        let added = await store.loadEarlierMessages(sessionID)
        return added > 0 ? anchor : nil
    }

    // MARK: Actions

    /// Puts `/<name> ` in front of the draft and closes the Skills sheet;
    /// the chat focuses the composer when the sheet is gone.
    func useSkill(_ skill: SessionSkill) {
        composer.text = SessionSkills.draft(using: skill, in: composer.text)
        focusComposerAfterSkills = true
        showSkills = false
    }

    func send() {
        guard let (text, images) = composer.take() else { return }
        EggifyHaptics.tap()
        sendCount += 1
        store.sendPrompt(text, images: images, to: sessionID)
    }

    func interrupt() {
        EggifyHaptics.heavy()
        store.interrupt(sessionID)
    }

    func stopLoop() {
        EggifyHaptics.heavy()
        store.stopLoop(sessionID)
    }

    func respond(_ request: PermissionRequest, approved: Bool) {
        store.respond(to: request, approved: approved)
    }

    func alwaysAllow(_ request: PermissionRequest, similar: Bool) {
        store.alwaysAllow(request, similar: similar)
    }

    func answer(_ question: UserQuestion, _ answers: [String]) {
        store.answer(question, with: answers)
        // A started worker is a new session: fetch it, so the team strip shows it.
        if question.workerProposal != nil, answers == [UserQuestion.WorkerProposal.start] {
            Task { [store] in
                try? await Task.sleep(for: .seconds(2))
                await store.loadSessions(quietly: true)
            }
        }
    }

    func dismiss(_ question: UserQuestion) {
        store.dismiss(question)
    }

    func later(_ question: UserQuestion) {
        store.answerLater(question)
    }

    func hold(_ question: UserQuestion) {
        store.holdQuestion(question)
    }

    /// The latest copy of a question, or nil once it stopped waiting.
    func liveQuestion(_ id: String) -> UserQuestion? {
        allQuestions.first { $0.id == id }
    }

    /// The global question policy (for "Default (…)" in the menu).
    var questionPolicyDefault: QuestionPolicy? {
        globalQuestionPolicy ?? (session?.question_policy == nil ? session?.question_policy_effective : nil)
    }

    func setQuestionPolicy(_ policy: QuestionPolicy?) {
        Task { await store.setQuestionPolicy(sessionID, policy) }
    }

    func showDeferredQuestions() {
        let ids = (store.pendingQuestions[sessionID] ?? []).map(\.id)
        store.deferredQuestionIds.subtract(ids)
    }

    func changeModel(provider: String, model: String) {
        store.changeModel(sessionID, provider: provider, model: model)
    }

    func setYOLO(_ enabled: Bool) {
        store.setYOLO(sessionID, enabled: enabled)
    }

    /// The conversation as Markdown, for sharing: prompts and replies,
    /// without tool activity.
    var transcript: String {
        var parts = ["# \(session?.displayTitle ?? sessionTitle)"]
        for message in messages {
            let text = message.content.textContent.trimmingCharacters(in: .whitespacesAndNewlines)
            guard !text.isEmpty else { continue }
            switch message.role {
            case "user": parts.append("**You:**\n\n\(text)")
            case "assistant": parts.append("**Agent:**\n\n\(text)")
            default: continue
            }
        }
        return parts.joined(separator: "\n\n---\n\n")
    }

    /// Deletes the session on the server; false if it couldn't be sent.
    func deleteSession() -> Bool {
        store.deleteSession(sessionID)
    }

    // MARK: Navigation

    func navigate(to destination: NavigationDestination) {
        switch destination {
        case .just: navigateToJust = true
        case .git: navigateToGit = true
        case .stats: navigateToStats = true
        case .zen: navigateToZen = true
        }
    }

    // MARK: Avatar

    var currentAvatar: Avatar? {
        session?.selected_avatar ?? session?.selected_avatar_id.flatMap { avatarManager.getAvatar(id: $0) }
    }

    var characterName: String { currentAvatar?.name ?? "Agent" }

    var characterColor: Color { currentAvatar?.color?.parseHexColor() ?? .defaultAvatar }

    var avatarImage: UIImage? { currentAvatar.flatMap { avatarManager.getAvatarImage(id: $0.id) } }

    func loadAvatarImageIfNeeded() async {
        guard let avatar = currentAvatar, avatarManager.getAvatarImage(id: avatar.id) == nil else { return }
        await avatarManager.loadAvatarImageIfNeeded(avatar: avatar)
    }
}

enum NavigationDestination {
    case just
    case git
    case stats
    case zen
}
