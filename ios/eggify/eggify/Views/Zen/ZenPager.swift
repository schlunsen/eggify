//
//  ZenPager.swift
//  eggify
//
//  Zen as a vertical pager over the user's active sessions, Reels style:
//  swipe up for the next session, down for the previous one. Each page is
//  that session's own Circus stage (`ZenStagePage`); the composer, the music
//  and the subscription belong to the pager, so the music plays on across
//  pages and only the session on screen is subscribed.
//
//  Order: the home list's "Needs you", then "Working", then sessions active
//  in the last 30 minutes ("Recent"), with the home project filter, never
//  ended ones. The session Zen was opened from is always in the list. While
//  Zen is open the list is merged, not re-sorted, so a session finishing or
//  a new one starting never moves the page on screen.
//
//  Subscriptions: the pager holds one reference-counted subscription for the
//  page that has settled on screen for a moment (flicking past a session
//  does not subscribe it; previewed neighbours load over REST only). The
//  chat underneath follows the settled page (`onSettle`), so leaving Zen with
//  the back swipe lands in the chat of the session that was on screen; the
//  pager lets go of its subscription a second after leaving, once that chat
//  holds its own. Closing never clears a session's pending approvals.
//

import SwiftUI
import Combine

// MARK: - Which sessions

enum ZenActiveSessions {
    enum Group: Equatable, Sendable {
        case needsYou, working, recent
    }

    /// Idle sessions with activity this recent stay in the pager.
    static let recentWindow: TimeInterval = 30 * 60

    /// Requests waiting for the user: what this device holds, or the server's count.
    static func waiting(_ session: AgentSession, localWaiting: [String: Int]) -> Int {
        max(localWaiting[session.id] ?? 0, session.pending_permissions + session.pending_questions)
    }

    static func status(_ session: AgentSession, statuses: [String: String]) -> SessionStatus {
        SessionStatus(statuses[session.id] ?? session.status).including(subagentsRunning: session.subagents_running)
    }

    /// The pager's group for a session, or nil when it doesn't belong in it.
    static func group(_ session: AgentSession, status: SessionStatus, waiting: Int, now: Date) -> Group? {
        if status == .ended { return nil }
        if waiting > 0 { return .needsYou }
        if status.isActive { return .working }
        if let updated = session.updatedDate, now.timeIntervalSince(updated) <= recentWindow { return .recent }
        return nil
    }

    /// The active sessions in the home list's order. `sessions` comes newest
    /// first (the store's order); "Needs you" ignores the project filter, as
    /// on the home list. A filter for a project without sessions shows all.
    static func ids(sessions: [AgentSession], statuses: [String: String], localWaiting: [String: Int],
                    projectFilter: String, now: Date) -> [String] {
        let filterOn = !projectFilter.isEmpty && sessions.contains { $0.project_id == projectFilter }
        var needs: [String] = [], working: [String] = [], recent: [String] = []
        for session in sessions {
            let g = group(session, status: status(session, statuses: statuses),
                          waiting: waiting(session, localWaiting: localWaiting), now: now)
            let inProject = !filterOn || session.project_id == projectFilter
            switch g {
            case .needsYou: needs.append(session.id)
            case .working where inProject: working.append(session.id)
            case .recent where inProject: recent.append(session.id)
            default: break
            }
        }
        return needs + working + recent
    }

    /// The indicator's word for a session right now.
    static func label(_ session: AgentSession?, statuses: [String: String], localWaiting: [String: Int]) -> String {
        guard let session else { return "Session" }
        if waiting(session, localWaiting: localWaiting) > 0 { return "Needs you" }
        let s = status(session, statuses: statuses)
        switch s {
        case .error: return "Error"
        case .ended: return "Ended"
        default: return s.isActive ? "Working" : "Idle"
        }
    }
}

// MARK: - Pager state

/// The pages and which one is on screen. Pure, so the rules are testable.
nonisolated struct ZenPagerState: Equatable, Sendable {
    private(set) var ids: [String]
    private(set) var currentID: String

    /// The session Zen opened on is always a page (first, if it isn't active).
    init(currentID: String, active: [String]) {
        var ids = active
        if !ids.contains(currentID) { ids.insert(currentID, at: 0) }
        self.ids = ids
        self.currentID = currentID
    }

    var index: Int { ids.firstIndex(of: currentID) ?? 0 }
    var count: Int { ids.count }

    /// The page `delta` away, or nil past either end (no wrapping).
    func neighbour(_ delta: Int) -> String? {
        let i = index + delta
        return ids.indices.contains(i) ? ids[i] : nil
    }

    /// Whether a page is the current one or next to it (those are built).
    func isNear(_ id: String, within distance: Int = 1) -> Bool {
        guard let i = ids.firstIndex(of: id) else { return false }
        return abs(i - index) <= distance
    }

    @discardableResult
    mutating func select(_ id: String) -> Bool {
        guard ids.contains(id), id != currentID else { return false }
        currentID = id
        return true
    }

    @discardableResult
    mutating func move(_ delta: Int) -> Bool {
        guard let id = neighbour(delta) else { return false }
        return select(id)
    }

    /// A new active list while Zen is open. Pages keep their order (nothing
    /// is re-sorted under the user's thumb), pages no longer active go, except
    /// the one on screen, and new ones go in after the page that precedes them
    /// in the fresh order (at the top if none does).
    mutating func update(active: [String]) {
        let fresh = Set(active)
        var result = ids.filter { fresh.contains($0) || $0 == currentID }
        for (i, id) in active.enumerated() where !result.contains(id) {
            var position = 0
            for j in stride(from: i - 1, through: 0, by: -1) {
                if let k = result.firstIndex(of: active[j]) { position = k + 1; break }
            }
            result.insert(id, at: position)
        }
        ids = result
    }
}

// MARK: - Subscriptions

/// What the pager needs from the store to (un)subscribe a session.
@MainActor
protocol ZenSessionSubscriber: AnyObject {
    func zenSubscribe(_ sessionID: String)
    func zenUnsubscribe(_ sessionID: String)
}

extension SessionsWebSocketViewModel: ZenSessionSubscriber {
    /// Reference-counted: a session the chat underneath already holds is not
    /// sent to the server again.
    func zenSubscribe(_ sessionID: String) {
        Task { await openSession(sessionID) }
    }

    /// Only stops re-subscribing after reconnects; pending approvals stay.
    func zenUnsubscribe(_ sessionID: String) {
        closeSession(sessionID)
    }
}

/// The pager's single subscription: the settled page's session.
@MainActor
final class ZenSubscriptions {
    private let subscriber: ZenSessionSubscriber
    private(set) var held: String?

    init(subscriber: ZenSessionSubscriber) {
        self.subscriber = subscriber
    }

    /// A page settled on screen: hold its session, then let go of the last one.
    func show(_ sessionID: String) {
        guard sessionID != held else { return }
        let previous = held
        held = sessionID
        subscriber.zenSubscribe(sessionID)
        if let previous { subscriber.zenUnsubscribe(previous) }
    }

    /// Zen closed.
    func end() {
        guard let held else { return }
        self.held = nil
        subscriber.zenUnsubscribe(held)
    }
}

#if DEBUG
/// The demo pager has no server.
final class NoopZenSubscriber: ZenSessionSubscriber {
    func zenSubscribe(_ sessionID: String) {}
    func zenUnsubscribe(_ sessionID: String) {}
}
#endif

// MARK: - Requests of the page on screen

/// Approvals and questions waiting in the session on screen.
@MainActor
final class ZenRequestsModel: ObservableObject {
    @Published private(set) var permissions: [PermissionRequest] = []
    @Published private(set) var questions: [UserQuestion] = []
    @Published private(set) var connected = false

    let sessionID: String
    let store: SessionsWebSocketViewModel

    init(sessionID: String, store: SessionsWebSocketViewModel) {
        self.sessionID = sessionID
        self.store = store
        let id = sessionID
        store.$pendingPermissions.map { $0[id] ?? [] }.removeDuplicates().assign(to: &$permissions)
        store.$pendingQuestions.map { $0[id] ?? [] }.removeDuplicates().assign(to: &$questions)
        store.$connectionState.map(\.isUsable).removeDuplicates().assign(to: &$connected)
    }
}

// MARK: - View

struct ZenView: View {
    let sessionTitle: String
    let store: SessionsWebSocketViewModel
    /// The page that settled on screen (the chat underneath follows it).
    var onSettle: ((String) -> Void)?

    @StateObject private var list: SessionListModel
    @State private var pager: ZenPagerState
    @State private var subscriptions: ZenSubscriptions
    @State private var scrollPosition: ScrollPosition
    /// The page under the middle of the screen while scrolling.
    @State private var liveIndex: Int?
    @State private var scrolling = false
    @State private var pendingActive: [String]?
    @ObservedObject private var music = ZenMusicPlayer.shared
    @State private var isInputFocused = false
    @AppStorage("eggify.homeProjectFilter") private var projectFilter = ""
    @Environment(\.scenePhase) private var scenePhase
    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    init(sessionID: String, sessionTitle: String, viewModel: SessionsWebSocketViewModel? = nil,
         subscriber: ZenSessionSubscriber? = nil, onSettle: ((String) -> Void)? = nil) {
        let store = viewModel ?? .shared
        self.sessionTitle = sessionTitle
        self.store = store
        self.onSettle = onSettle
        _list = StateObject(wrappedValue: SessionListModel(store: store))
        let active = ZenActiveSessions.ids(
            sessions: store.sessions, statuses: store.sessionStatus,
            localWaiting: Self.localWaiting(store.pendingPermissions, store.pendingQuestions),
            projectFilter: UserDefaults.standard.string(forKey: "eggify.homeProjectFilter") ?? "",
            now: Date())
        _pager = State(initialValue: ZenPagerState(currentID: sessionID, active: active))
        _subscriptions = State(initialValue: ZenSubscriptions(subscriber: subscriber ?? store))
        _scrollPosition = State(initialValue: ScrollPosition(id: sessionID))
    }

    private static func localWaiting(_ permissions: [String: [PermissionRequest]],
                                     _ questions: [String: [UserQuestion]]) -> [String: Int] {
        var out: [String: Int] = [:]
        for (id, list) in permissions { out[id, default: 0] += list.count }
        for (id, list) in questions { out[id, default: 0] += list.count }
        return out
    }

    private var activeIDs: [String] {
        ZenActiveSessions.ids(sessions: list.sessions, statuses: list.sessionStatus,
                              localWaiting: Self.localWaiting(list.pendingPermissions, list.pendingQuestions),
                              projectFilter: projectFilter, now: Date())
    }

    private var currentBackdropHex: UInt32 {
        CircusBackdropStore.backdrop(for: pager.currentID, open: list.sessions.map(\.id)).boardHex
    }

    var body: some View {
        VStack(spacing: 0) {
            GeometryReader { outer in
                let topInset = outer.safeAreaInsets.top
                GeometryReader { geo in
                    Group {
                        if reduceMotion {
                            fadePager(size: geo.size, topInset: topInset)
                        } else {
                            scrollPager(size: geo.size, topInset: topInset)
                        }
                    }
                    .overlay(alignment: .trailing) {
                        ZenPageDots(count: pager.count, index: pager.index)
                            .padding(.trailing, 6)
                            .padding(.top, topInset)
                    }
                }
                .ignoresSafeArea(edges: .top)
            }
            ZenComposerBar(sessionID: pager.currentID, store: store, boardHex: currentBackdropHex,
                           isFocused: $isInputFocused)
                .id(pager.currentID)
        }
        .background(Color(circusHex: currentBackdropHex).ignoresSafeArea())
        // No bar title: the paper plate is the title.
        .navigationTitle("")
        .navigationBarTitleDisplayMode(.inline)
        .toolbarBackground(.hidden, for: .navigationBar)
        .toolbar(.hidden, for: .tabBar)
        .toolbar {
            ToolbarItem(placement: .principal) { indicator }
            ToolbarItem(placement: .topBarTrailing) {
                Button {
                    music.toggle()
                } label: {
                    Image(systemName: music.enabled ? "speaker.wave.2.fill" : "speaker.slash.fill")
                }
                .accessibilityLabel(music.enabled ? "Mute music" : "Play music")
                .accessibilityIdentifier("zen-music-toggle")
            }
        }
        // One player for the whole pager: the music plays on across pages.
        .onAppear { setMusic(scenePhase == .active) }
        .onDisappear {
            music.stop()
            // The chat underneath shows this session now and subscribes it
            // when it appears; let go of ours once it has.
            let subscriptions = subscriptions
            Task {
                try? await Task.sleep(for: .seconds(1))
                subscriptions.end()
            }
        }
        .onChange(of: scenePhase) { _, phase in setMusic(phase == .active) }
        // Subscribe the page once it has stayed on screen for a moment, so
        // flicking past a session doesn't take its approvals from another device.
        .task(id: pager.currentID) {
            let id = pager.currentID
            if subscriptions.held != nil {
                try? await Task.sleep(for: .milliseconds(500))
                guard !Task.isCancelled else { return }
            }
            subscriptions.show(id)
            onSettle?(id)
        }
        .onChange(of: activeIDs) { _, fresh in
            if scrolling { pendingActive = fresh } else { apply(fresh) }
        }
    }

    // MARK: Pages

    private func scrollPager(size: CGSize, topInset: CGFloat) -> some View {
        ScrollView(.vertical) {
            LazyVStack(spacing: 0) {
                ForEach(Array(pager.ids.enumerated()), id: \.element) { i, id in
                    page(id, index: i, size: size, topInset: topInset)
                }
            }
            .scrollTargetLayout()
            .offset(y: -Self.peek * size.height)
        }
        .scrollTargetBehavior(.paging)
        .scrollPosition($scrollPosition)
        .scrollIndicators(.hidden)
        // While typing, the page (and the draft's session) stays put.
        .scrollDisabled(isInputFocused)
        .onScrollGeometryChange(for: Int.self) { geo in
            Int((geo.contentOffset.y / max(geo.containerSize.height, 1)).rounded())
        } action: { _, index in
            liveIndex = index
            // At rest on another page than the current one (pages changed
            // above it): bring the current page back, never switch session.
            if !scrolling, index != pager.index { reanchor() }
        }
        .onScrollPhaseChange { _, phase in
            scrolling = phase != .idle
            if phase == .idle { settle() }
        }
    }

    #if DEBUG
    /// `-EggifyZenPagerPeek 0.4`: the pages drawn part-way to the next one
    /// (negative: the previous one), as mid-swipe, for screenshots.
    private static let peek = CGFloat(UserDefaults.standard.double(forKey: "EggifyZenPagerPeek"))
    #else
    private static let peek: CGFloat = 0
    #endif

    /// Reduce Motion: no slide, the next stage fades in.
    private func fadePager(size: CGSize, topInset: CGFloat) -> some View {
        ZStack {
            page(pager.currentID, index: pager.index, size: size, topInset: topInset)
                .id(pager.currentID)
                .transition(.opacity)
        }
        .contentShape(Rectangle())
        .gesture(
            DragGesture(minimumDistance: 24)
                .onEnded { value in
                    let dy = value.translation.height
                    guard abs(dy) > 60, abs(dy) > abs(value.translation.width) else { return }
                    move(dy < 0 ? 1 : -1)
                }
        )
    }

    /// The stage for a page on screen or next to it; a plain board further away.
    @ViewBuilder
    private func page(_ id: String, index: Int, size: CGSize, topInset: CGFloat) -> some View {
        let near = pager.isNear(id) || liveIndex.map { abs($0 - index) <= 1 } == true
        if near {
            ZenStagePage(
                sessionID: id,
                sessionTitle: id == pager.currentID ? (store.session(id)?.displayTitle ?? sessionTitle)
                    : (store.session(id)?.displayTitle ?? "Session"),
                store: store, size: size, topInset: topInset,
                isCurrent: id == pager.currentID,
                onTapStage: { isInputFocused = false },
                onNext: pager.neighbour(1) == nil ? nil : { move(1) },
                onPrevious: pager.neighbour(-1) == nil ? nil : { move(-1) })
        } else {
            Circus.cream.frame(width: size.width, height: size.height)
        }
    }

    // MARK: Paging

    /// The scroll came to rest: the page under it is the current one.
    private func settle() {
        if let index = liveIndex, pager.ids.indices.contains(index) {
            let id = pager.ids[index]
            if pager.select(id) { changed() }
        }
        if let fresh = pendingActive {
            pendingActive = nil
            apply(fresh)
        }
        // Anchor to the page itself, so pages added above don't move it.
        scrollPosition.scrollTo(id: pager.currentID)
    }

    private func apply(_ fresh: [String]) {
        let before = pager.ids
        pager.update(active: fresh)
        guard pager.ids != before else { return }
        reanchor()
    }

    /// Keep the current page on screen after pages were added or removed
    /// above it: the scroll offset would otherwise stay put and show another
    /// session. Done again after the next layout pass, when the new pages exist.
    private func reanchor() {
        guard !reduceMotion else { return }
        let id = pager.currentID
        scrollPosition.scrollTo(id: id)
        Task { @MainActor in
            await Task.yield()
            guard pager.currentID == id, !scrolling else { return }
            var t = Transaction()
            t.disablesAnimations = true
            withTransaction(t) { scrollPosition.scrollTo(id: id) }
        }
    }

    /// VoiceOver actions and the Reduce Motion swipe.
    private func move(_ delta: Int) {
        guard let next = pager.neighbour(delta) else { return }
        if reduceMotion {
            withAnimation(.easeInOut(duration: 0.35)) { _ = pager.select(next) }
        } else {
            withAnimation(.snappy) { scrollPosition.scrollTo(id: next) }
            pager.select(next)
        }
        changed()
    }

    private func changed() {
        EggifyHaptics.selectionChanged()
        UIAccessibility.post(notification: .screenChanged, argument: indicatorText)
    }

    private func setMusic(_ on: Bool) {
        on ? music.start() : music.stop()
    }

    // MARK: Indicator

    private var indicatorText: String {
        let label = ZenActiveSessions.label(
            list.sessions.first { $0.id == pager.currentID }, statuses: list.sessionStatus,
            localWaiting: Self.localWaiting(list.pendingPermissions, list.pendingQuestions))
        return pager.count > 1 ? "\(label) · \(pager.index + 1) of \(pager.count)" : label
    }

    private var indicator: some View {
        Text(indicatorText)
            .font(.eggifySerif(13, relativeTo: .footnote))
            .foregroundStyle(Circus.ink)
            .padding(.horizontal, 10)
            .padding(.vertical, 3)
            .background(Capsule().fill(Circus.paper.opacity(0.92)))
            .overlay(Capsule().strokeBorder(Circus.ink.opacity(0.6), lineWidth: 1))
            .contentTransition(.numericText())
            .animation(.snappy, value: indicatorText)
            .accessibilityIdentifier("zen-pager-indicator")
            .accessibilityHint(pager.count > 1 ? "Swipe up or down with three fingers, or use the actions, to change session" : "")
            .accessibilityActions {
                if pager.neighbour(1) != nil { Button("Next session") { move(1) } }
                if pager.neighbour(-1) != nil { Button("Previous session") { move(-1) } }
            }
    }
}

/// Small dots on the trailing edge: where this page is in the list.
private struct ZenPageDots: View {
    let count: Int
    let index: Int

    static let maxDots = 7

    var body: some View {
        if count > 1 {
            let window = Self.window(count: count, index: index)
            VStack(spacing: 6) {
                ForEach(window, id: \.self) { i in
                    Circle()
                        .fill(i == index ? Circus.paper : Circus.paper.opacity(0.45))
                        .overlay(Circle().strokeBorder(Circus.ink.opacity(0.7), lineWidth: 1))
                        .frame(width: i == index ? 8 : 6, height: i == index ? 8 : 6)
                }
            }
            .animation(.snappy, value: index)
            .allowsHitTesting(false)
            .accessibilityHidden(true)
        }
    }

    /// At most `maxDots` page numbers around `index`.
    static func window(count: Int, index: Int) -> [Int] {
        guard count > maxDots else { return Array(0..<count) }
        let start = min(max(0, index - maxDots / 2), count - maxDots)
        return Array(start..<(start + maxDots))
    }
}

// MARK: - Composer

/// The bottom of Zen for the session on screen: its approvals and questions,
/// then the composer (a draft per session).
private struct ZenComposerBar: View {
    let sessionID: String
    let store: SessionsWebSocketViewModel
    let boardHex: UInt32
    @Binding var isFocused: Bool

    @StateObject private var composer: ComposerModel
    @StateObject private var requests: ZenRequestsModel
    @State private var presentedQuestion: UserQuestion?

    init(sessionID: String, store: SessionsWebSocketViewModel, boardHex: UInt32, isFocused: Binding<Bool>) {
        self.sessionID = sessionID
        self.store = store
        self.boardHex = boardHex
        _isFocused = isFocused
        _composer = StateObject(wrappedValue: ComposerModel(draftKey: sessionID))
        _requests = StateObject(wrappedValue: ZenRequestsModel(sessionID: sessionID, store: store))
    }

    var body: some View {
        VStack(spacing: 8) {
            if let request = requests.permissions.first {
                PermissionRequestCard(
                    request: request,
                    position: 1,
                    total: requests.permissions.count,
                    isConnected: requests.connected,
                    onAlwaysAllow: { similar in
                        withAnimation(.snappy) { store.alwaysAllow(request, similar: similar) }
                    }
                ) { approved in
                    withAnimation(.snappy) { store.respond(to: request, approved: approved) }
                }
                .transition(.move(edge: .bottom).combined(with: .opacity))
                .id(request.id)
            }
            if !requests.questions.isEmpty, presentedQuestion == nil {
                Button {
                    store.deferredQuestionIds.subtract(requests.questions.map(\.id))
                    presentedQuestion = requests.questions.first
                } label: {
                    Label(requests.questions.count == 1 ? "The agent asked you a question"
                          : "The agent asked \(requests.questions.count) questions",
                          systemImage: "questionmark.bubble.fill")
                        .font(.subheadline.weight(.semibold))
                        .frame(maxWidth: .infinity)
                }
                .buttonStyle(.borderedProminent)
                .tint(Circus.pink)
            }
            HStack(alignment: .bottom, spacing: 8) {
                ComposerTextView(text: $composer.text, isFocused: $isFocused,
                                 placeholder: "Say something to the egg…",
                                 font: UIFontMetrics(forTextStyle: .body).scaledFont(
                                    for: UIFont(name: EggifyFonts.serif, size: 18) ?? .preferredFont(forTextStyle: .body)),
                                 textColor: UIColor(Circus.ink),
                                 placeholderColor: UIColor(Circus.ink.opacity(0.45)),
                                 tintColor: UIColor(Circus.ink),
                                 maxLines: 4)
                    .padding(.horizontal, 14)
                    .padding(.vertical, 10)
                    .background(Circus.paper, in: RoundedRectangle(cornerRadius: 14, style: .continuous))
                    .overlay(RoundedRectangle(cornerRadius: 14, style: .continuous).strokeBorder(Circus.ink, lineWidth: 2))
                Button(action: send) {
                    Image(systemName: "arrow.up")
                        .font(.body.weight(.bold))
                        .foregroundStyle(Circus.paper)
                        .frame(width: 42, height: 42)
                        .background(composer.canSend ? Circus.pink : Circus.ink.opacity(0.35), in: Circle())
                        .overlay(Circle().strokeBorder(Circus.ink, lineWidth: 2))
                }
                .disabled(!composer.canSend)
                .accessibilityLabel("Send")
            }
        }
        .animation(.snappy, value: requests.permissions.first?.id)
        .padding(.horizontal, 14)
        .padding(.top, 8)
        .padding(.bottom, 10)
        .background(Color(circusHex: boardHex).opacity(0.97).ignoresSafeArea(edges: .bottom))
        .onChange(of: requests.questions.map(\.id)) { _, ids in
            // Answered elsewhere, auto-answered or cancelled: close the sheet.
            if let shown = presentedQuestion, !ids.contains(shown.id) {
                presentedQuestion = nil
            }
        }
        .sheet(item: $presentedQuestion) { question in
            UserQuestionSheet(
                question: requests.questions.first { $0.id == question.id } ?? question,
                onAnswer: { answers in
                    store.answer(question, with: answers)
                    presentedQuestion = nil
                },
                onLater: {
                    store.answerLater(question)
                    presentedQuestion = nil
                },
                onDismiss: {
                    store.dismiss(question)
                    presentedQuestion = nil
                },
                onHold: { store.holdQuestion(question) }
            )
            .presentationDetents([.medium, .large])
            .presentationDragIndicator(.visible)
        }
    }

    private func send() {
        guard let (text, images) = composer.take() else { return }
        EggifyHaptics.tap()
        store.sendPrompt(text, images: images, to: sessionID)
        isFocused = false
    }
}

#Preview {
    NavigationStack {
        ZenView(sessionID: "example", sessionTitle: "Test Session")
    }
}
