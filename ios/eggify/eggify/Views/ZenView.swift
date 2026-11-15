//
//  ZenView.swift
//  eggify
//
//  Zen: the session as a Flying Circus paper collage, the same stage as the
//  web app's Circus scene and drawn from the same art. The egg gentleman on
//  his soapbox is the agent: he says what it is doing and sways while it works,
//  wearing a hat that changes every few turns. Each tool plays a little gag
//  (Read: a book flutters past, Edit: a quill scribbles, Bash: the steam machine
//  rises and puffs, search: a magnifier sweeps the stage, web: a cherub flies
//  over, replies: cut-out letters that sometimes spell a summary or a joke), a
//  prompt brings the cherub's fanfare, the announcer banner and makes the bunting
//  hop, subagents march on as the web app's cut-out characters and bow out when
//  done, and an error or a stop brings the giant foot down. Now and then a rare
//  ambient gag passes (tea parade, bureaucrat on a stalk, a balloon, bicycle,
//  train, fish, cat, rabbit, bird, shooting star or gramophone).
//
//  Each session has its own backdrop (the web's hash, so the phone and the
//  desktop agree). The name plate (name + AI summary) and the response card sit
//  above the stage outside the 12 fps TimelineView, so only the stage redraws.
//  Music (shuffled, low, mixes with other audio) follows the beat grid of the
//  track; the stage moves on twos, stands still in the background and with
//  Reduce Motion.
//

import SwiftUI
import Combine
import UIKit

// MARK: - Model

/// A collage gag, played once for a session event.
enum CircusGagKind: Equatable {
    case book       // Read
    case quill      // Edit / Write
    case steam      // Bash: a puff from the steam machine
    case magnifier  // Grep / Glob
    case cherub     // WebFetch / WebSearch
    case letters    // The agent said something: a word from the reply
    case message    // ...or a summary / joke spelled on the boards
    case pop        // Any other tool
    // Ambient gags (rare, on cooldowns)
    case banner
    case tea
    case stalk
    case crossing(CircusCrossingProp)

    /// How long the gag is on stage, in seconds.
    var duration: TimeInterval {
        switch self {
        case .book: 40 / Circus.fps
        case .quill: 34 / Circus.fps
        case .steam: 22 / Circus.fps
        case .magnifier: 36 / Circus.fps
        case .cherub: 44 / Circus.fps
        case .letters: 26 / Circus.fps
        case .message: 66 / Circus.fps
        case .pop: 10 / Circus.fps
        case .banner: 8
        case .tea: 11
        case .stalk: 8
        case .crossing(let prop): prop.crossing.seconds
        }
    }

    var isAmbient: Bool {
        switch self {
        case .banner, .tea, .stalk, .crossing: true
        default: false
        }
    }

    /// The gag a tool plays, by the same categories as the web app.
    static func forTool(_ name: String) -> CircusGagKind? {
        switch CircusActivity.forTool(name) {
        case .read: .book
        case .edit: .quill
        case .bash: .steam
        case .search: .magnifier
        case .web: .cherub
        case .agent: nil  // the subagents walk on themselves
        default: .pop
        }
    }
}

struct CircusGag: Identifiable, Equatable {
    let id: Int
    let kind: CircusGagKind
    let start: Date
    /// Stable randomness in [0, 1) for where the gag plays.
    let seed: Double
    var letters: [String] = []
    /// Lines of a spelled message, or the banner / bureaucrat's words.
    var lines: [String] = []
}

/// A subagent on stage, as one of the web app's cut-out characters.
struct CircusCastMember: Identifiable, Equatable {
    let id: String
    let look: CircusLook
    let slot: Int
    /// When it marched on (distantPast if it was already there when Zen opened).
    let enteredAt: Date
    /// When it finished: it bows, then walks off.
    var leftAt: Date?
}

/// What the stage shows, derived from the session store.
@MainActor
final class CircusZenModel: ObservableObject {
    /// One line for the egg (his latest words, plain, or what he is doing).
    @Published private(set) var speech = ""
    /// The latest reply as markdown, when the latest thing he did was talk.
    @Published private(set) var reply: String?
    @Published private(set) var status: SessionStatus = .idle
    @Published private(set) var needsApproval = false
    @Published private(set) var subagentsRunning = 0
    /// The session's cached one-line AI summary (never generated just for Zen).
    @Published private(set) var summary: String?
    /// When the last prompt went out (fanfare) and the last error/stop (foot).
    @Published private(set) var cherubAt: Date?
    @Published private(set) var footAt: Date?
    @Published private(set) var gags: [CircusGag] = []
    @Published private(set) var cast: [CircusCastMember] = []
    /// The steam machine is on stage from `steamSince` until a while after `steamLast`.
    @Published private(set) var steamSince: Date?
    @Published private(set) var steamLast: Date?
    /// Prompts sent since Zen opened: the hat changes every three.
    @Published private(set) var turn = 0

    /// This session's backdrop (stable per session, as on the desktop).
    let backdrop: CircusBackdropID

    /// Most gags and characters on stage at once (as on the web).
    static let maxGags = 6
    static let maxCast = 6
    /// The steam machine sinks this long after the last Bash call.
    static let steamLinger: TimeInterval = 40 / Circus.fps
    /// A finished subagent bows and walks off in this time.
    static let exitDuration: TimeInterval = 34 / Circus.fps

    let sessionID: String
    let store: SessionsWebSocketViewModel
    private var cancellables = Set<AnyCancellable>()
    private var seenMessages: Set<String>?
    private var seenTools = Set<String>()
    private var gagSeq = 0
    /// When the page first came on screen (nil while only previewed in the pager).
    private var activatedAt: Date?
    private var activity = CircusActivityState(now: Date().timeIntervalSinceReferenceDate)
    private var lastUserMessage: String?
    private var ambientTimer: AnyCancellable?
    private var lastAmbient: [String: TimeInterval] = [:]
    private var lastAnyAmbient: TimeInterval = 0

    init(sessionID: String, store: SessionsWebSocketViewModel? = nil) {
        let store = store ?? .shared
        self.sessionID = sessionID
        self.store = store
        self.backdrop = CircusBackdropStore.backdrop(for: sessionID, open: store.sessions.map(\.id))
        let id = sessionID

        // nil = not loaded yet (a previewed page in the pager): the first
        // list that arrives is history and plays nothing.
        store.$sessionMessages
            .map { $0[id] }
            .removeDuplicates()
            .sink { [weak self] messages in
                guard let self, let messages else { return }
                self.update(messages: messages)
            }
            .store(in: &cancellables)

        store.$sessionStatus
            .map { $0[id] }
            .removeDuplicates()
            .sink { [weak self] raw in
                guard let self else { return }
                let new = SessionStatus(raw ?? store.session(id)?.status ?? "idle")
                if new == .error, self.status != .error { self.footAt = Date() }
                self.status = new
            }
            .store(in: &cancellables)

        store.$pendingPermissions
            .map { !($0[id] ?? []).isEmpty }
            .removeDuplicates()
            .sink { [weak self] in self?.needsApproval = $0 }
            .store(in: &cancellables)

        store.$subagents
            .map { $0[id] ?? [] }
            .removeDuplicates()
            .sink { [weak self] in self?.update(subagents: $0) }
            .store(in: &cancellables)

        #if DEBUG
        // UI tests and screenshots: a cast on stage and every gag in turn.
        if ProcessInfo.processInfo.arguments.contains("-EggifyDemoCircus") { startDemo() }
        #endif
    }

    /// The hat the egg wears now (nil: his own bowler), as `hatFor` on the web.
    var hat: String? { CircusAmbient.hat(sessionID: sessionID, turn: turn) }

    #if DEBUG
    private func startDemo() {
        let now = Date()
        summary = "Teaching the egg gentleman to juggle the build pipeline without dropping a single teacup."
        reply = """
            **Splendid.** The build is green again:

            - fixed the flaky `TeapotTests`
            - moved the hats into their own *cut-out* layer
            - the bureaucrat now queues properly

            Shall I open a pull request, or would you like a biscuit first? There is rather a lot more to say \
            about the paperwork, the stamps and the slightly damp forms, but it can wait until after tea.
            """
        speech = "Splendid. The build is green again."
        cast = ["demo-a", "demo-b", "demo-c", "demo-d", "demo-e"].enumerated().map { i, id in
            CircusCastMember(id: id, look: CircusLook(agentID: id), slot: i, enteredAt: now)
        }
        let kinds: [CircusGagKind] = [.book, .steam, .quill, .magnifier, .message, .letters, .cherub, .pop,
                                      .banner, .crossing(.bicycle), .tea, .crossing(.balloon), .stalk,
                                      .crossing(.cat), .crossing(.bird), .crossing(.train)]
        var next = 0
        Timer.publish(every: 1.2, on: .main, in: .common).autoconnect()
            .sink { [weak self] date in
                guard let self else { return }
                let kind = kinds[next % kinds.count]
                if kind == .message {
                    self.play(.message, at: date, lines: CircusMessages.wrap(CircusMessages.sanitize("Egg-cellent commit!")))
                } else if kind.isAmbient {
                    if !self.gags.contains(where: { $0.kind.isAmbient && date.timeIntervalSince($0.start) < $0.kind.duration }) {
                        self.playAmbient(kind, at: date)
                    }
                } else {
                    self.play(kind, at: date, label: "Splendid")
                }
                if next % 5 == 4 { self.turn += 1 }
                next += 1
            }
            .store(in: &cancellables)
    }
    #endif

    // MARK: Lifecycle (called by the view)

    /// Zen is on screen and the app is active: load what the stage needs and
    /// start the ambient timer. `motion` is false with Reduce Motion.
    func activate(motion: Bool) {
        if activatedAt == nil { activatedAt = Date() }
        Task { await store.loadSubagents(sessionID) }
        Task { await loadSummary() }
        ambientTimer = nil
        guard motion else { return }
        lastAnyAmbient = Self.clock()
        ambientTimer = Timer.publish(every: 1, on: .main, in: .common).autoconnect()
            .sink { [weak self] _ in self?.maybeAmbient() }
    }

    func deactivate() {
        ambientTimer = nil
    }

    private var prepared = false

    /// The page was built (on screen or as a neighbour previewed during a
    /// swipe): load what the still stage shows. Messages come over REST, so
    /// a preview never subscribes the session.
    func prepare() {
        guard !prepared else { return }
        prepared = true
        if summary == nil { Task { await loadSummary() } }
        if store.sessionMessages[sessionID] == nil {
            Task { await store.loadMessages(sessionID) }
        }
    }

    private func loadSummary() async {
        guard let session = store.session(sessionID) else { return }
        let text = await EggifyAPIClient.shared.getCachedSessionSummary(
            sessionID: sessionID, messageCount: session.message_count ?? 0, status: session.status)
        if let text { summary = text }
    }

    private static func clock() -> TimeInterval { ProcessInfo.processInfo.systemUptime }

    // MARK: Messages → gags

    private func update(messages: [AgentMessage]) {
        let line = Self.speech(from: messages)
        speech = line.text
        reply = line.isReply ? line.markdown : nil
        if let lastUser = messages.last(where: { $0.isUser }) {
            lastUserMessage = lastUser.content.textContent
        }
        // The history loaded on open plays nothing; only what arrives after.
        guard var seen = seenMessages else {
            seenMessages = Set(messages.map(\.id))
            seenTools = Set(messages.flatMap { $0.getTools().map(\.id) })
            return
        }
        let now = Date()
        let t = now.timeIntervalSinceReferenceDate
        var played = 0
        // A burst (e.g. after a reconnect) plays only its last few events.
        for message in messages.suffix(8) {
            let isNew = seen.insert(message.id).inserted
            if message.isUser {
                if isNew { promptSent(at: now) }
                continue
            }
            if isNew, message.role == "error"
                || (message.role == "system" && message.content.textContent.hasPrefix("Stopped")) {
                footAt = now
                activity.note(.error, isError: true, at: t)
                continue
            }
            if isNew, message.role == "assistant" {
                let text = SelectableText.plain(message.content.textContent)
                if !text.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty {
                    playLetters(label: text, at: now)
                    activity.note(.message, at: t)
                    played += 1
                }
            }
            // Tools can be added to a message that is already on screen.
            for tool in message.getTools() where seenTools.insert(tool.id).inserted {
                activity.note(CircusActivity.forTool(tool.name), at: t)
                guard let kind = CircusGagKind.forTool(tool.name), played < 4 else { continue }
                play(kind, at: now)
                played += 1
            }
        }
        seenMessages = seen
    }

    /// A prompt went out: the fanfare, a new turn (maybe a new hat) and, now and then, the banner.
    private func promptSent(at now: Date) {
        cherubAt = now
        turn += 1
        let t = Self.clock()
        guard ambientTimer != nil,
              t - (lastAmbient[CircusAmbientKind.banner.rawValue] ?? -.infinity) >= CircusAmbient.bannerCooldown,
              liveGags(at: now).count < 3 else { return }
        lastAmbient[CircusAmbientKind.banner.rawValue] = t
        lastAnyAmbient = t
        playAmbient(.banner, at: now)
    }

    /// Letters from the egg: usually a word of the reply, sometimes a summary or a joke.
    private func playLetters(label: String, at now: Date) {
        guard !gags.contains(where: { ($0.kind == .letters || $0.kind == .message) && now.timeIntervalSince($0.start) < $0.kind.duration })
        else { return }
        let message = CircusMessagePicker.shared.next(
            context: CircusMessageContext(summary: summary, lastUserMessage: lastUserMessage),
            activity: activity, idle: !status.isActive, now: now.timeIntervalSinceReferenceDate)
        if message.kind != .word, !message.text.isEmpty {
            play(.message, at: now, lines: CircusMessages.wrap(message.text))
        } else {
            play(.letters, at: now, label: label)
        }
    }

    private func liveGags(at now: Date) -> [CircusGag] {
        gags.filter { now.timeIntervalSince($0.start) <= $0.kind.duration }
    }

    private func play(_ kind: CircusGagKind, at now: Date, label: String = "", lines: [String] = []) {
        gags = liveGags(at: now)
        if kind == .steam {
            if steamSince == nil || now.timeIntervalSince(steamLast ?? .distantPast) > Self.steamLinger + 0.5 {
                steamSince = now
            }
            steamLast = now
            // At most four puffs in the air.
            if gags.filter({ $0.kind == .steam }).count >= 4 { return }
        } else if [.magnifier, .cherub, .letters, .message].contains(kind), gags.contains(where: { $0.kind == kind }) {
            return  // one at a time, as on the web
        }
        if gags.count >= Self.maxGags { gags.removeFirst(gags.count - Self.maxGags + 1) }
        gagSeq += 1
        var gag = CircusGag(id: gagSeq, kind: kind, start: now,
                            seed: Circus.hash01("\(sessionID):\(gagSeq)"))
        if kind == .letters { gag.letters = Self.letters(from: label) }
        gag.lines = lines
        gags.append(gag)
    }

    // MARK: Ambient gags

    /// Once a second: maybe one rare gag, as the web's `maybeAmbient`.
    private func maybeAmbient() {
        let t = Self.clock()
        guard t - lastAnyAmbient >= CircusAmbient.idleAfter,
              Double.random(in: 0..<1) <= CircusAmbient.chancePerSecond else { return }
        let now = Date()
        guard let pick = CircusAmbient.pick(now: t, last: lastAmbient, lastAny: lastAnyAmbient,
                                            onStage: liveGags(at: now).count,
                                            playing: ZenMusicPlayer.shared.isPlaying,
                                            backdrop: backdrop, rand: { Double.random(in: 0..<1) })
        else { return }
        lastAmbient[pick.key] = t
        lastAnyAmbient = t
        switch pick.kind {
        case .banner: playAmbient(.banner, at: now)
        case .tea: playAmbient(.tea, at: now)
        case .stalk: playAmbient(.stalk, at: now)
        case .crossing: if let prop = pick.prop { playAmbient(.crossing(prop), at: now) }
        }
    }

    private func playAmbient(_ kind: CircusGagKind, at now: Date) {
        let live = liveGags(at: now)
        // One of a kind at a time, and leave room for the session's own gags.
        guard live.count < Self.maxGags - 1,
              !live.contains(where: { Self.sameAmbient($0.kind, kind) }) else { return }
        var lines: [String] = []
        switch kind {
        case .banner: lines = [CircusAmbient.bannerLines.randomElement() ?? ""]
        case .stalk: lines = [CircusAmbient.stalkLines.randomElement() ?? ""]
        default: break
        }
        play(kind, at: now, lines: lines)
    }

    private static func sameAmbient(_ a: CircusGagKind, _ b: CircusGagKind) -> Bool {
        switch (a, b) {
        case (.crossing, .crossing): true
        default: a == b
        }
    }

    // MARK: Subagents

    private func update(subagents: [Subagent]) {
        let now = Date()
        // What arrives in the first moments (the store's list, the catch-up load)
        // was already running: those characters are simply there.
        let firstLoad = activatedAt.map { now.timeIntervalSince($0) < 2 } ?? true
        subagentsRunning = subagents.filter { $0.status.isActive }.count

        // Gone for good once they have walked off.
        cast.removeAll { member in
            guard let left = member.leftAt else { return false }
            return now.timeIntervalSince(left) > Self.exitDuration
        }
        let activeIDs = Set(subagents.filter { $0.status.isActive }.map(\.id))
        for i in cast.indices where cast[i].leftAt == nil && !activeIDs.contains(cast[i].id) {
            cast[i].leftAt = now
        }
        for agent in subagents where agent.status.isActive && !cast.contains(where: { $0.id == agent.id }) {
            let taken = Set(cast.map(\.slot))
            guard let slot = (0..<Self.maxCast).first(where: { !taken.contains($0) }) else { break }
            // Subagents already running when Zen opens (or after a reload) are simply there.
            cast.append(CircusCastMember(id: agent.id, look: CircusLook(agentID: agent.id), slot: slot,
                                         enteredAt: firstLoad ? .distantPast : now))
        }
    }

    // MARK: Text

    struct Speech: Equatable {
        var text = ""
        var markdown = ""
        var isReply = false
    }

    /// The egg's line: the agent's latest words, or what it is doing.
    nonisolated static func speech(from messages: [AgentMessage]) -> Speech {
        for message in messages.reversed() where !message.isUser {
            let raw = message.content.textContent
            let text = SelectableText.plain(raw).trimmingCharacters(in: .whitespacesAndNewlines)
            if message.role == "assistant", !text.isEmpty {
                return Speech(text: clipped(text), markdown: raw, isReply: true)
            }
            if let tool = message.getTools().last {
                let what = tool.summary.trimmingCharacters(in: .whitespacesAndNewlines)
                return Speech(text: clipped(what.isEmpty ? tool.displayName : "\(tool.displayName): \(what)"))
            }
            if message.role == "error", !text.isEmpty { return Speech(text: clipped(text)) }
        }
        return Speech()
    }

    nonisolated static func clipped(_ text: String, limit: Int = 160) -> String {
        let flat = text.replacingOccurrences(of: "\n", with: " ")
        return flat.count > limit ? String(flat.prefix(limit - 1)) + "…" : flat
    }

    /// Letters for the cut-out "talking" gag: up to five letters or digits of the reply.
    nonisolated static func letters(from label: String, count: Int = 5) -> [String] {
        let picked = label.filter { $0.isLetter || $0.isNumber || $0 == "?" || $0 == "!" }
            .prefix(count).map { String($0).uppercased() }
        return picked.isEmpty ? ["B", "L", "A", "H"] : picked
    }

    /// Lines the paper caption turns over while the agent works.
    nonisolated static let workingCaptions = [
        "Something completely different…",
        "Pondering…",
        "Consulting the Ministry…",
        "Fetching a shrubbery…",
        "Right. Where was I…",
    ]

    /// A caption in the spirit of the show; `now` turns the working lines over.
    func caption(at now: Date) -> String {
        if needsApproval { return "Awaiting your blessing" }
        switch status {
        case .error: return "'Tis but a scratch"
        case _ where status.isActive:
            let lines = Self.workingCaptions
            return lines[Int(now.timeIntervalSinceReferenceDate / 7) % lines.count]
        default:
            if subagentsRunning > 0 {
                return "Awaiting \(subagentsRunning) subagent\(subagentsRunning == 1 ? "" : "s"). Tea, anyone?"
            }
            return "Resting, not dead"
        }
    }

    func send(_ text: String, images: [(mediaType: String, base64: String)]) {
        store.sendPrompt(text, images: images, to: sessionID)
    }
}

// MARK: - Characters

/// A subagent's cut-out character, picked from its id exactly as the web app
/// does (`circusCharacterFor`), so it looks the same on the phone and the desktop.
struct CircusLook: Equatable {
    enum Kind: String, CaseIterable {
        case gent, knight, hanky, parrot, egg, lady
        var asset: String { "CircusFigure" + rawValue.prefix(1).uppercased() + rawValue.dropFirst() }
    }

    let kind: Kind
    let coat: UInt32
    let accent: UInt32

    private static let coats: [UInt32] = [0x3b3b45, 0x2a9d8f, 0xc2185b, 0x8a6510, 0x4a6fa5, 0x6b4f3a, 0x7a3b69, 0x2f5d50]
    private static let accents: [UInt32] = [0xe8458b, 0xd4a017, 0x2a9d8f, 0xf08a4b, 0xa8d8cc, 0xf2c14e]
    private static let parrotBlue: UInt32 = 0x3d7cc9

    init(agentID: String) {
        let h = Self.hash(agentID)
        let kind = Kind.allCases[Int(h % 6)]
        let coat = kind == .parrot ? Self.parrotBlue : Self.coats[Int((h >> 3) % 8)]
        var accent = Self.accents[Int((h >> 9) % 6)]
        if accent == coat { accent = Self.accents[Int(((h >> 9) + 1) % 6)] }
        self.kind = kind
        self.coat = coat
        self.accent = accent
    }

    /// FNV-1a over UTF-16 code units, as `hashString` in the web app.
    nonisolated static func hash(_ value: String) -> UInt32 {
        var h: UInt32 = 0x811c9dc5
        for unit in value.utf16 {
            h ^= UInt32(unit)
            h = h &* 0x01000193
        }
        return h
    }
}

/// A cut-out character: the figure with its coat and accent tinted in.
struct CircusFigure: View {
    let look: CircusLook

    var body: some View {
        let name = look.kind.asset
        ZStack {
            Image(name).resizable()
            Color(circusHex: look.coat)
                .mask(Image(name + "Coat").resizable().luminanceToAlpha())
                .blendMode(.multiply)
            Color(circusHex: look.accent)
                .mask(Image(name + "Accent").resizable().luminanceToAlpha())
                .blendMode(.multiply)
        }
        .compositingGroup()
        .aspectRatio(76.0 / 96.0, contentMode: .fit)
    }
}

extension Color {
    init(circusHex hex: UInt32) {
        self.init(red: Double((hex >> 16) & 0xff) / 255,
                  green: Double((hex >> 8) & 0xff) / 255,
                  blue: Double(hex & 0xff) / 255)
    }
}

// MARK: - Page

/// One session's stage in the Zen pager: backdrop, cast, gags, name plate and
/// response card. The pager owns the composer, the music and the
/// subscription; only the page on screen (`isCurrent`) animates.
struct ZenStagePage: View {
    let sessionID: String
    let sessionTitle: String
    let size: CGSize
    let topInset: CGFloat
    let isCurrent: Bool
    let onTapStage: () -> Void
    /// VoiceOver actions on the name plate (nil at the ends of the list).
    var onNext: (() -> Void)?
    var onPrevious: (() -> Void)?

    @StateObject private var model: CircusZenModel
    @State private var hud = CircusHUDFrames()
    @State private var showFullReply = false
    @Environment(\.scenePhase) private var scenePhase
    @Environment(\.accessibilityReduceMotion) private var reduceMotion
    @ObservedObject private var power = PowerState.shared

    init(sessionID: String, sessionTitle: String, store: SessionsWebSocketViewModel,
         size: CGSize, topInset: CGFloat, isCurrent: Bool, onTapStage: @escaping () -> Void,
         onNext: (() -> Void)? = nil, onPrevious: (() -> Void)? = nil) {
        self.sessionID = sessionID
        self.sessionTitle = sessionTitle
        self.size = size
        self.topInset = topInset
        self.isCurrent = isCurrent
        self.onTapStage = onTapStage
        self.onNext = onNext
        self.onPrevious = onPrevious
        _model = StateObject(wrappedValue: CircusZenModel(sessionID: sessionID, store: store))
    }

    private var active: Bool { isCurrent && scenePhase == .active }

    var body: some View {
        let layout = CircusLayout(size: size, topInset: topInset)
        ZStack(alignment: .topLeading) {
            // Only the page on screen runs the 12 fps clock; a previewed
            // neighbour shows one still frame.
            TimelineView(.animation(minimumInterval: 1.0 / PowerState.zenFPS(lowPower: power.lowPower),
                                    paused: !active || reduceMotion)) { context in
                CircusStage(model: model,
                            layout: layout,
                            hud: hud,
                            now: context.date,
                            still: reduceMotion)
            }
            .contentShape(Rectangle())
            .onTapGesture(perform: onTapStage)

            // The name plate and the response card: solid paper above the stage,
            // outside the TimelineView so they only redraw when their text changes.
            CircusHUD(model: model,
                      title: model.store.session(sessionID)?.displayTitle ?? sessionTitle,
                      layout: layout,
                      frames: $hud,
                      showFullReply: $showFullReply,
                      onNext: onNext,
                      onPrevious: onPrevious)
        }
        .coordinateSpace(name: CircusLayout.space)
        .frame(width: size.width, height: size.height)
        .clipped()
        .background(Color(circusHex: model.backdrop.boardHex))
        .sheet(isPresented: $showFullReply) {
            ZenReplySheet(text: model.reply ?? "")
        }
        .onAppear { model.prepare(); setActive(active) }
        .onDisappear { model.deactivate() }
        .onChange(of: active) { _, now in setActive(now) }
    }

    private func setActive(_ on: Bool) {
        if on { model.activate(motion: !reduceMotion) } else { model.deactivate() }
    }
}


// MARK: - Look

enum Circus {
    static let fps = 12.0
    static let ink = Color(red: 0x2b / 255, green: 0x1d / 255, blue: 0x10 / 255)
    static let paper = Color(red: 0xfb / 255, green: 0xf5 / 255, blue: 0xe6 / 255)
    // Hand-tinted, muted like the engraved art: dusty rose, ochre, verdigris, cream, sage.
    static let pink = Color(circusHex: 0xb8606e)
    static let mustard = Color(circusHex: 0xc79a3e)
    static let teal = Color(circusHex: 0x5f8f86)
    static let cream = Color(circusHex: 0xf1e6cb)
    static let mint = Color(circusHex: 0xa9c2b6)
    static let wood = Color(red: 0xb0 / 255, green: 0x6a / 255, blue: 0x2c / 255)
    /// Paper colours for bursts, letters and scraps (as on the web).
    static let palette: [Color] = [pink, mustard, teal, cream, mint]

    /// Stop-motion wobble in [-1, 1], changing every `every` frames.
    static func jitter(_ seed: Double, _ frame: Int, every: Int = 3) -> Double {
        let q = Double(frame / every)
        let x = sin(seed * 12.9898 + q * 78.233) * 43758.5453
        return (x - x.rounded(.down)) * 2 - 1
    }

    /// Deterministic hash of a string into [0, 1).
    static func hash01(_ s: String) -> Double {
        Double(CircusLook.hash(s)) / 4_294_967_296
    }
}

extension CircusBackdropID {
    /// Where the picture's centre of interest is (0...1 of its width): a portrait
    /// phone shows a slice of the wide backdrop and keeps this in it.
    var focusX: CGFloat {
        switch self {
        case .meadow: 0.76
        case .desert: 0.42
        case .seaside: 0.45
        case .gothic: 0.65
        case .storm: 0.7
        case .tearoom: 0.78
        case .snow: 0.62
        case .dusk: 0.55
        default: 0.5
        }
    }
}

/// Where things sit on the stage (shared by the stage and the HUD).
struct CircusLayout: Equatable {
    static let space = "circusStage"
    let size: CGSize
    let topInset: CGFloat

    /// The backdrop fills the stage bottom-aligned; its boards start at 756 of its 900.
    var backdropSize: CGSize {
        let h = max(size.height, size.width * 9 / 16)
        return CGSize(width: h * 16 / 9, height: h)
    }
    var groundY: CGFloat { size.height - backdropSize.height * (144.0 / 900.0) }
    var eggHeight: CGFloat { min(size.height * 0.34, size.width * 0.62) }
    var eggWidth: CGFloat { eggHeight * 346 / 600 }
    var boxHeight: CGFloat { eggHeight * 0.22 }
    var boxWidth: CGFloat { eggHeight * 0.62 }
    var eggTop: CGFloat { groundY + 12 - boxHeight - eggHeight }
    var curtainWidth: CGFloat { min(max(size.width * 0.075, 24), 64) }
    /// The plate hangs under the bunting.
    var plateTop: CGFloat { topInset + 22 }
}

/// The HUD cards' frames in stage coordinates, so props can keep clear of them.
struct CircusHUDFrames: Equatable {
    var plate: CGRect = .zero
    var card: CGRect = .zero
    var boxes: [CGRect] { [plate, card].filter { !$0.isEmpty }.map { $0.insetBy(dx: -14, dy: -14) } }
}

// MARK: - HUD

private struct CircusHUD: View {
    @ObservedObject var model: CircusZenModel
    let title: String
    let layout: CircusLayout
    @Binding var frames: CircusHUDFrames
    @Binding var showFullReply: Bool
    var onNext: (() -> Void)?
    var onPrevious: (() -> Void)?

    static let nameMax = 44
    static let summaryMax = 200

    var body: some View {
        let width = min(layout.size.width - layout.curtainWidth * 2 - 16, 460)
        ZStack(alignment: .top) {
            plate
                .frame(maxWidth: width)
                .onGeometryChange(for: CGRect.self) { $0.frame(in: .named(CircusLayout.space)) } action: { frames.plate = $0 }
                .padding(.top, layout.plateTop)
                .frame(maxWidth: .infinity, alignment: .top)

            // The card stands just above the egg's hat, between the plate and the egg.
            let bottom = layout.eggTop - 6
            let top = max(layout.plateTop, frames.plate.maxY) + 10
            let room = max(56, bottom - top)
            VStack {
                Spacer(minLength: 0)
                if !model.speech.isEmpty {
                    ZenResponseCard(speech: model.speech, reply: model.reply, maxHeight: min(room - 40, 168),
                                    onExpand: { showFullReply = true })
                        .frame(maxWidth: min(layout.size.width * 0.84, 460))
                        .onGeometryChange(for: CGRect.self) { $0.frame(in: .named(CircusLayout.space)) } action: { frames.card = $0 }
                        .onDisappear { frames.card = .zero }
                }
            }
            .frame(width: layout.size.width, height: max(0, bottom))
        }
        .frame(width: layout.size.width, height: layout.size.height, alignment: .top)
    }

    private static func clip(_ text: String, _ max: Int) -> String {
        let flat = text.split(whereSeparator: \.isWhitespace).joined(separator: " ")
        return flat.count <= max ? flat : String(flat.prefix(max - 1)).trimmingCharacters(in: .whitespaces) + "…"
    }

    /// The session's title plate: name, the AI summary (if one exists) and the caption.
    private var plate: some View {
        VStack(spacing: 3) {
            Text(Self.clip(title, Self.nameMax))
                .font(.eggifySerifSemiBold(20, relativeTo: .headline))
                .lineLimit(1)
            if let summary = model.summary, !summary.isEmpty {
                Text(Self.clip(summary, Self.summaryMax))
                    .font(.eggifySerif(16, relativeTo: .subheadline))
                    .multilineTextAlignment(.center)
                    .lineLimit(3)
                    .fixedSize(horizontal: false, vertical: true)
            }
            Text(model.caption(at: Date()))
                .font(.eggifySerif(13, relativeTo: .footnote))
                .italic()
                .opacity(0.75)
                .lineLimit(1)
                .minimumScaleFactor(0.8)
        }
        .foregroundStyle(Circus.ink)
        .padding(.horizontal, 16)
        .padding(.vertical, 8)
        // The shadow is the card's, not the text's.
        .background(Rectangle().fill(Circus.paper).shadow(color: Circus.ink.opacity(0.35), radius: 0, x: 3, y: 4))
        .overlay(Rectangle().strokeBorder(Circus.ink, lineWidth: 2))
        .overlay(Rectangle().strokeBorder(Circus.ink, lineWidth: 1).padding(4))
        .rotationEffect(.degrees(-1.2))
        .accessibilityElement(children: .combine)
        .accessibilityIdentifier("zen-plate")
        .accessibilityActions {
            if let onNext { Button("Next session", action: onNext) }
            if let onPrevious { Button("Previous session", action: onPrevious) }
        }
    }
}

/// The egg's latest words: the reply as markdown in a clamped card that follows
/// the end of the text while it streams, with "Read more" and Copy; or what he
/// is doing, in one plain line. Its height is capped, so the stage never jumps.
private struct ZenResponseCard: View {
    let speech: String
    let reply: String?
    let maxHeight: CGFloat
    let onExpand: () -> Void

    @State private var contentHeight: CGFloat = 0
    @State private var copied = false

    private var overflowing: Bool { contentHeight > maxHeight + 4 }

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            if let reply {
                ScrollView {
                    MarkdownView(text: reply, foreground: Circus.ink)
                        .font(.eggifySerif(15, relativeTo: .callout))
                        .frame(maxWidth: .infinity, alignment: .leading)
                        .onGeometryChange(for: CGFloat.self) { $0.size.height } action: { contentHeight = $0 }
                }
                .defaultScrollAnchor(.bottom)
                .scrollIndicators(.hidden)
                // A reply that fits doesn't hold on to the drag, so a swipe
                // on a short card still turns the session page; a long one
                // scrolls itself first.
                .scrollBounceBehavior(.basedOnSize)
                .frame(height: max(20, min(contentHeight, maxHeight)))
                .mask {
                    VStack(spacing: 0) {
                        if overflowing {
                            LinearGradient(colors: [.clear, .black], startPoint: .top, endPoint: .bottom).frame(height: 18)
                        }
                        Color.black
                    }
                }
                HStack(spacing: 14) {
                    Spacer(minLength: 0)
                    if overflowing {
                        Button("Read more", action: onExpand)
                            .accessibilityIdentifier("zen-reply-more")
                    }
                    Button(copied ? "Copied" : "Copy") {
                        UIPasteboard.general.string = SelectableText.plain(reply)
                        copied = true
                        EggifyHaptics.tap()
                        Task { try? await Task.sleep(for: .seconds(1.5)); copied = false }
                    }
                    .accessibilityIdentifier("zen-reply-copy")
                }
                .font(.eggifySerifSemiBold(14, relativeTo: .footnote))
                .foregroundStyle(Circus.pink)
            } else {
                Text(speech)
                    .font(.eggifySerif(15, relativeTo: .callout))
                    .foregroundStyle(Circus.ink)
                    .lineLimit(3)
                    .fixedSize(horizontal: false, vertical: true)
            }
        }
        .padding(12)
        .background(RoundedRectangle(cornerRadius: 16, style: .continuous).fill(Circus.paper)
            .shadow(color: Circus.ink.opacity(0.3), radius: 0, x: 2, y: 3))
        .overlay(RoundedRectangle(cornerRadius: 16, style: .continuous).strokeBorder(Circus.ink, lineWidth: 2))
        .accessibilityElement(children: .contain)
        .accessibilityLabel("The agent says: \(speech)")
        .accessibilityIdentifier("zen-reply")
    }
}

/// The whole reply, from "Read more".
private struct ZenReplySheet: View {
    let text: String
    @Environment(\.dismiss) private var dismiss

    var body: some View {
        NavigationStack {
            ScrollView {
                MarkdownView(text: text, foreground: Circus.ink)
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .padding()
            }
            .background(Circus.paper)
            .navigationTitle("The egg says")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .topBarLeading) {
                    Button("Copy") { UIPasteboard.general.string = SelectableText.plain(text) }
                }
                ToolbarItem(placement: .topBarTrailing) {
                    Button("Done") { dismiss() }
                }
            }
        }
        .presentationDetents([.medium, .large])
    }
}

// MARK: - Stage

private struct CircusStage: View {
    @ObservedObject var model: CircusZenModel
    let layout: CircusLayout
    let hud: CircusHUDFrames
    let now: Date
    let still: Bool

    private var size: CGSize { layout.size }
    private var frame: Int { still ? 0 : Int(now.timeIntervalSinceReferenceDate * Circus.fps) }
    /// The relaxed beat motion while music plays, else nil (the calm idle on twos).
    private var calm: ZenCalm? { still ? nil : ZenMusicPlayer.shared.calm() }

    private var groundY: CGFloat { layout.groundY }
    private var eggHeight: CGFloat { layout.eggHeight }
    private var eggWidth: CGFloat { layout.eggWidth }
    private var boxHeight: CGFloat { layout.boxHeight }
    private var boxWidth: CGFloat { layout.boxWidth }
    private var eggTop: CGFloat { layout.eggTop }
    private var curtainWidth: CGFloat { layout.curtainWidth }

    var body: some View {
        let calm = self.calm
        ZStack(alignment: .topLeading) {
            Color(circusHex: model.backdrop.boardHex)
            backdrop
            clouds(calm)
            if !still { ambientLayer }
            steamMachine
            castLayer(front: false, calm: calm)
            soapboxAndEgg(calm)
            castLayer(front: true, calm: calm)
            bunting(calm)
            if !still {
                gagLayer
                cherub
                foot
            }
            curtains
        }
        .frame(width: size.width, height: size.height)
        .offset(shake)
        .clipped()
    }

    // MARK: Scenery

    private var backdrop: some View {
        let bd = layout.backdropSize
        // Portrait shows a slice of the wide backdrop: keep its centre of interest in it.
        let x = min(0, max(size.width - bd.width, size.width / 2 - bd.width * model.backdrop.focusX))
        return Image(model.backdrop.asset)
            .resizable()
            .frame(width: bd.width, height: bd.height)
            .position(x: x + bd.width / 2, y: size.height - bd.height / 2)
            .accessibilityHidden(true)
    }

    // Paper clouds drifting across, one paper-step at a time (faster while working).
    /// Engraved clouds; none in the night skies, tinted once per backdrop (as circus.css).
    @ViewBuilder
    private func clouds(_ calm: ZenCalm?) -> some View {
        if ![.space, .storm, .gothic].contains(model.backdrop) {
            cloudLayer(calm)
                .saturation(model.backdrop == .snow ? 0.4 : 1)
                .colorMultiply(model.backdrop == .dusk ? Color(circusHex: 0xf3d2bf)
                               : model.backdrop == .desert ? Color(circusHex: 0xf6e8cf) : .white)
        }
    }

    private static let cloudSpeeds: [Double] = [0.35, 0.22, 0.28]
    private static let cloudWidths: [CGFloat] = [130, 96, 112]
    private static let cloudTops: [CGFloat] = [0.2, 0.3, 0.38]

    // Split out with explicit types: as one closure the Release build's type
    // checker gave up ("unable to type-check this expression in reasonable time").
    private func cloudX(_ i: Int) -> CGFloat {
        let span = Double(size.width) + 220
        let speed: Double = Self.cloudSpeeds[i] * (model.status.isActive ? 1.8 : 1)
        let start: Double = Double(i) * span / 3
        let travel: Double = Double(frame / 3 * 3) * speed
        return CGFloat((start + travel).truncatingRemainder(dividingBy: span) - 110)
    }

    private func cloudLift(_ i: Int, _ calm: ZenCalm?) -> CGFloat {
        if let calm { return CGFloat(calm.breath) * 2.5 }
        return CGFloat(Circus.jitter(Double(i), frame, every: 4))
    }

    private func cloudLayer(_ calm: ZenCalm?) -> some View {
        ForEach(0..<3, id: \.self) { (i: Int) in
            cloud(i, calm)
        }
    }

    private func cloud(_ i: Int, _ calm: ZenCalm?) -> some View {
        let width: CGFloat = Self.cloudWidths[i]
        let y: CGFloat = size.height * Self.cloudTops[i] - cloudLift(i, calm)
        return Image("CircusCloud")
            .resizable()
            .frame(width: width, height: width * 461 / 608)
            .position(x: cloudX(i), y: y)
            .accessibilityHidden(true)
    }

    private var curtains: some View {
        ZStack(alignment: .topLeading) {
            Image("CircusCurtainLeft")
                .resizable()
                .frame(width: curtainWidth, height: size.height)
                .position(x: curtainWidth / 2, y: size.height / 2)
            Image("CircusCurtainRight")
                .resizable()
                .frame(width: curtainWidth, height: size.height)
                .position(x: size.width - curtainWidth / 2, y: size.height / 2)
        }
        .allowsHitTesting(false)
        .accessibilityHidden(true)
    }

    // Paper bunting across the top; it hops when a prompt goes out and bumps on the bar.
    private func bunting(_ calm: ZenCalm?) -> some View {
        let hopping = model.cherubAt.map { now.timeIntervalSince($0) < 0.9 } ?? false
        let f = frame
        let bump = CGFloat(calm?.bar ?? 0) * 2
        return Canvas { ctx, sz in
            let w = sz.width
            let top: CGFloat = 6, sag: CGFloat = 26
            func ropeY(_ t: CGFloat) -> CGFloat { top + 4 * t * (1 - t) * sag }
            var rope = Path()
            rope.move(to: CGPoint(x: 0, y: top))
            rope.addQuadCurve(to: CGPoint(x: w, y: top), control: CGPoint(x: w / 2, y: top + 2 * sag))
            ctx.stroke(rope, with: .color(Circus.ink), lineWidth: 1.6)
            let n = max(7, Int(w / 40))
            for i in 0..<n {
                let t = (CGFloat(i) + 0.5) / CGFloat(n)
                let x = t * w
                let hop: CGFloat = hopping && (i + f / 2) % 2 == 0 ? -7 : 0
                let y = ropeY(t) + hop + bump
                var flag = Path()
                flag.move(to: CGPoint(x: x - 13, y: y))
                flag.addLine(to: CGPoint(x: x + 13, y: y + 1))
                flag.addLine(to: CGPoint(x: x, y: y + 24))
                flag.closeSubpath()
                let color = i % 4 == 3 ? Circus.pink : [Circus.pink, Circus.mustard, Circus.teal][i % 3]
                ctx.fill(flag, with: .color(color))
                ctx.stroke(flag, with: .color(Circus.ink), style: StrokeStyle(lineWidth: 1.5, lineJoin: .round))
            }
        }
        .frame(width: size.width, height: 64)
        // Hung just under the status bar, behind the (transparent) navigation bar.
        .position(x: size.width / 2, y: max(0, layout.topInset - 50) + 32)
        .allowsHitTesting(false)
        .accessibilityHidden(true)
    }

    // MARK: The egg gentleman

    /// The egg's pose: with music the relaxed beat motion (slow sway, a breath per
    /// bar, a soft nod on some downbeats); without, the web's calm two-pose lean
    /// when idle and a nod on twos while he talks.
    private func eggPose(_ calm: ZenCalm?) -> (bob: CGFloat, tilt: Double) {
        let working = model.status.isActive
        if let calm {
            let energy = working ? ZenBeatMotion.energyWorking : ZenBeatMotion.energyIdle
            return (CGFloat((calm.breath * 8 + calm.nod * 12) * energy), calm.sway * 2 * energy)
        }
        if still { return (0, 0) }
        if working {
            let beat = (frame / 2) % 2 == 1
            return (beat ? 7 : 0, beat ? -2.5 : 1.5)
        }
        return (0, (frame / 9) % 2 == 1 ? -1 : 0.6)
    }

    private func soapboxAndEgg(_ calm: ZenCalm?) -> some View {
        let working = model.status.isActive
        let pose = eggPose(calm)
        let squash = isStomping ? 0.82 : 1
        return ZStack {
            Image("CircusEgg")
                .resizable()
                .frame(width: eggWidth, height: eggHeight)
                .overlay(alignment: .topLeading) { hat }
                .scaleEffect(x: 1, y: squash, anchor: .bottom)
                .rotationEffect(.degrees(pose.tilt), anchor: .bottom)
                .offset(y: -boxHeight - eggHeight / 2 + 6 - pose.bob)
                .accessibilityLabel(working ? "The agent is working" : "The agent")
            Text("SOAP")
                .font(.system(size: boxHeight * 0.42, weight: .heavy, design: .serif))
                .foregroundStyle(Circus.paper)
                .frame(width: boxWidth, height: boxHeight)
                .background(Circus.wood)
                .overlay(Rectangle().strokeBorder(Circus.ink, lineWidth: 2))
                .offset(y: -boxHeight / 2)
        }
        .position(x: size.width / 2, y: groundY + 6)
    }

    /// This turn's hat, sat on his head as on the web (.cc-hat: left 1%, width 50%, bottom 81%, -12°).
    @ViewBuilder
    private var hat: some View {
        if let hat = model.hat {
            let art = CircusAmbient.hatSize(hat)
            let w = eggWidth * 0.5
            let h = w * art.height / art.width
            Image(CircusAmbient.hatAsset(hat))
                .resizable()
                .frame(width: w, height: h)
                .rotationEffect(.degrees(-12), anchor: .bottom)
                .offset(x: eggWidth * 0.01, y: eggHeight * 0.19 - h)
                .accessibilityHidden(true)
        }
    }

    // MARK: Subagents

    private var figureHeight: CGFloat { eggHeight * 0.36 }

    /// Slots either side of the soapbox; slot 0/1 nearest, further ones behind.
    private func slotPosition(_ slot: Int) -> (x: CGFloat, side: CGFloat, depth: Int) {
        let side: CGFloat = slot % 2 == 0 ? -1 : 1
        let depth = slot / 2
        let figW = figureHeight * 76 / 96
        let x = size.width / 2 + side * (boxWidth / 2 + figW * 0.42 + CGFloat(depth) * figW * 0.6)
        return (x, side, depth)
    }

    private func castLayer(front: Bool, calm: ZenCalm?) -> some View {
        let members = model.cast
            .filter { (slotPosition($0.slot).depth == 0) == front }
            .sorted { slotPosition($0.slot).depth > slotPosition($1.slot).depth }
        return ForEach(members) { member in
            castMember(member, calm: calm)
        }
        .accessibilityElement(children: .ignore)
        .accessibilityLabel(front && model.subagentsRunning > 0
                            ? (model.subagentsRunning == 1 ? "1 subagent working" : "\(model.subagentsRunning) subagents working")
                            : "")
    }

    @ViewBuilder
    private func castMember(_ member: CircusCastMember, calm: ZenCalm?) -> some View {
        let slot = slotPosition(member.slot)
        let seed = Double(CircusLook.hash(member.id) % 997)
        let figH = figureHeight * (1 - CGFloat(slot.depth) * 0.08)
        let figW = figH * 76 / 96
        let baseY = groundY + 14 - CGFloat(slot.depth) * 6
        let offstage = slot.side < 0 ? -figW : size.width + figW
        let pose = castPose(member, target: slot.x, offstage: offstage, side: slot.side, seed: seed, calm: calm)
        if let pose {
            CircusFigure(look: member.look)
                .frame(height: figH)
                .rotationEffect(.degrees(pose.rot), anchor: .bottom)
                .position(x: pose.x, y: baseY - figH / 2 - pose.bob)
        }
    }

    /// Where a character is and how it leans: marching on, idling, bowing, or walking off.
    private func castPose(_ member: CircusCastMember, target: CGFloat, offstage: CGFloat,
                          side: CGFloat, seed: Double, calm: ZenCalm?) -> (x: CGFloat, bob: CGFloat, rot: Double)? {
        let f = frame
        let hop: CGFloat = (f / 2) % 2 == 0 ? 6 : 0
        if let left = member.leftAt {
            let t = now.timeIntervalSince(left) * Circus.fps
            if still { return nil }
            if t < 14 {
                // A bow, in two poses.
                return (target, 0, (Int(t) / 4) % 2 == 0 ? Double(-side) * 22 : 0)
            }
            let k = min(1, (t - 14) / 20)
            if k >= 1 { return nil }
            // Walk off to the nearer wing.
            return (target + (offstage - target) * CGFloat(k), hop, hop > 0 ? 5 : -4)
        }
        let t = now.timeIntervalSince(member.enteredAt) * Circus.fps
        if !still, t < 24 {
            let k = CGFloat(t / 24)
            return (offstage + (target - offstage) * k, hop, hop > 0 ? -5 : 4)
        }
        if still { return (target, 0, 0) }
        if let calm {
            // A step every two beats with a smooth bounce, as on the web.
            return (target, CGFloat(calm.step) * 4, Double(-side) * calm.stepDir * calm.step * 2.5)
        }
        // Calm idle: a small lean now and then, a hop once in a while.
        let rot = Circus.jitter(seed, f, every: 9) * 1.5
        let bob: CGFloat = ((f + Int(seed)) / 24) % 5 == 0 ? 3 : 0
        return (target, bob, rot)
    }

    // MARK: Steam machine (Bash)

    /// The machine's frame while it is on stage: it rises in three steps,
    /// stays while Bash keeps running and sinks a while after the last call.
    private var steamFrame: CGRect? {
        guard !still, let since = model.steamSince, let last = model.steamLast else { return nil }
        let mh = min(size.height * 0.17, size.width * 0.34)
        let mw = mh * 351 / 420
        let rest = groundY + 10 - mh
        let hidden = size.height + 4
        let step: TimeInterval = 3 / Circus.fps
        let sinkT = now.timeIntervalSince(last) - CircusZenModel.steamLinger
        let y: CGFloat
        if sinkT > 0 {
            let k = min(1, sinkT / step)
            if k >= 1 { return nil }
            y = rest + (hidden - rest) * ceil(CGFloat(k) * 3) / 3
        } else {
            let k = min(1, now.timeIntervalSince(since) / step)
            y = hidden + (rest - hidden) * ceil(CGFloat(k) * 3) / 3
        }
        let x = size.width - curtainWidth - mw - 2
        return CGRect(x: x + Circus.jitter(7, frame), y: y + Circus.jitter(8, frame), width: mw, height: mh)
    }

    private var steamMachine: some View {
        Group {
            if let r = steamFrame {
                Image("CircusSteamMachine")
                    .resizable()
                    .frame(width: r.width, height: r.height)
                    .position(x: r.midX, y: r.midY)
                    .accessibilityHidden(true)
            }
        }
    }

    // MARK: Gags

    private func live(_ gag: CircusGag) -> (local: Double, p: Double)? {
        let t = now.timeIntervalSince(gag.start)
        guard t >= 0, t < gag.kind.duration else { return nil }
        return (t * Circus.fps, t / gag.kind.duration)
    }

    private var gagLayer: some View {
        ForEach(model.gags.filter { !$0.kind.isAmbient }) { gag in
            if let l = live(gag) {
                gagView(gag, local: l.local, p: l.p)
            }
        }
        .allowsHitTesting(false)
        .accessibilityHidden(true)
    }

    /// Ambient props pass behind the egg and the HUD cards.
    private var ambientLayer: some View {
        ForEach(model.gags.filter { $0.kind.isAmbient }) { gag in
            if let l = live(gag) {
                ambientView(gag, local: l.local, p: l.p)
            }
        }
        .allowsHitTesting(false)
        .accessibilityHidden(true)
    }

    @ViewBuilder
    private func gagView(_ gag: CircusGag, local: Double, p: Double) -> some View {
        let f = frame
        let W = size.width, H = size.height
        switch gag.kind {
        case .book:
            // A book flutters across the sky, flapping its covers.
            let x = W + 60 - CGFloat(p) * (W + 160)
            let y = H * (0.18 + gag.seed * 0.14) + sin(p * .pi * 3) * H * 0.035
            Image("CircusBook")
                .resizable()
                .frame(width: 84, height: 49)
                .scaleEffect(x: 1, y: (f / 2) % 2 == 0 ? 1 : 0.45)
                .rotationEffect(.degrees(Circus.jitter(gag.seed * 100, f) * 6 - 8))
                .position(x: x, y: y)

        case .quill:
            // A quill scribbles above the hat, then paper scraps fly.
            let bx = W / 2 + eggWidth * 0.34
            let by = eggTop - 14
            ZStack {
                if p < 0.55 {
                    let zig: CGFloat = (f / 2) % 2 == 0 ? 14 : -10
                    quill.offset(x: zig + CGFloat(local) * 1.5, y: CGFloat(Int(local) % 4) * 3)
                        .rotationEffect(.degrees(zig > 0 ? 8 : -6))
                } else {
                    quill.opacity(Int(local) % 2 == 0 ? 1 : 0)
                    let t = CGFloat(local - 18)
                    ForEach(0..<6, id: \.self) { i in
                        let vx = (Circus.hash01("\(gag.id)q\(i)") - 0.5) * 16
                        let vy = -9 - Circus.hash01("\(gag.id)v\(i)") * 9
                        Rectangle()
                            .fill(Circus.palette[i % Circus.palette.count])
                            .overlay(Rectangle().stroke(Circus.ink, lineWidth: 1))
                            .frame(width: 10, height: 8)
                            .rotationEffect(.degrees(Double(t) * 40 * (vx < 0 ? -1 : 1)))
                            .offset(x: CGFloat(vx) * t, y: CGFloat(vy) * t + 1.4 * t * t)
                    }
                }
            }
            .position(x: bx, y: by)

        case .steam:
            // A puff from the machine's chimney.
            if let r = steamFrame {
                let t = CGFloat(local)
                Image("CircusPuff")
                    .resizable()
                    .frame(width: 44, height: 33)
                    .scaleEffect(0.5 + ceil(t / 3) * 0.12)
                    .opacity(t > 15 ? max(0, 1 - Double(ceil((t - 15) / 2)) * 0.25) : 1)
                    .position(x: r.minX + r.width * 0.62 + t * 1.5 + Circus.jitter(Double(gag.id), f) * 3,
                              y: r.minY + 6 - t * 5)
            }

        case .magnifier:
            // A magnifying glass sweeps the stage.
            Image("CircusMagnifier")
                .resizable()
                .frame(width: 62, height: 62)
                .rotationEffect(.degrees(Circus.jitter(31, f) * 5))
                .position(x: W * 0.12 + CGFloat(p) * W * 0.76,
                          y: groundY - H * 0.11 + sin(p * .pi * 4) * H * 0.02)

        case .cherub:
            // A cherub flies over, tooting.
            let w = min(W * 0.3, 130)
            let x = -w + CGFloat(p) * (W + w * 2)
            let y = H * 0.24 + sin(p * .pi * 2) * H * 0.03
            ZStack(alignment: .topLeading) {
                Image("CircusCherub")
                    .resizable()
                    .scaledToFit()
                    .frame(width: w)
                    .offset(y: (f / 2) % 2 == 0 ? -4 : 0)
                    .rotationEffect(.degrees((f / 2) % 2 == 0 ? -4 : 3))
                notes(frame: f, origin: CGPoint(x: w * 0.9, y: w * 0.1))
            }
            .position(x: x, y: y)

        case .letters:
            // Cut-out letters float up from the egg.
            let ox = W / 2 + eggWidth * 0.36
            let oy = eggTop + eggHeight * 0.3
            ZStack {
                ForEach(Array(gag.letters.enumerated()), id: \.offset) { i, ch in
                    let tl = max(0, local - Double(i) * 2)
                    let k = CGFloat(ceil(tl / 2) * 2)
                    Text(ch)
                        .font(.system(size: 17, weight: .heavy, design: i % 2 == 0 ? .monospaced : .serif))
                        .foregroundStyle(Circus.ink)
                        .frame(width: 24, height: 27)
                        .background(Circus.palette[(i + gag.id) % Circus.palette.count])
                        .overlay(Rectangle().strokeBorder(Circus.ink, lineWidth: 1.5))
                        .rotationEffect(.degrees(Double(i % 2 == 0 ? -1 : 1) * (6 + Double(k))))
                        .offset(x: k * (3 + CGFloat(i) * 1.8), y: -k * 4 - CGFloat(i) * 4)
                        .opacity(tl <= 0 ? 0 : p > 0.8 ? 0.4 : 1)
                }
            }
            .position(x: ox, y: oy)

        case .message:
            messageTiles(gag, local: local, p: p)

        case .pop:
            // A little paper burst near the egg.
            let side: CGFloat = gag.seed < 0.5 ? -1 : 1
            Image("CircusBurst")
                .resizable()
                .frame(width: 52, height: 52)
                .colorMultiply([Circus.pink, Circus.mustard, Circus.teal][gag.id % 3])
                .scaleEffect(p < 0.3 ? 0.5 : p < 0.8 ? 1 : 0.7)
                .rotationEffect(.degrees(Circus.jitter(gag.seed * 50, f, every: 2) * 10))
                .opacity(p > 0.8 ? 0.5 : 1)
                .position(x: W / 2 + side * (eggWidth * 0.62 + CGFloat(gag.seed) * W * 0.12),
                          y: eggTop + eggHeight * (0.15 + CGFloat(gag.seed) * 0.4))

        default:
            EmptyView()
        }
    }

    /// A summary or a joke: the tiles fly out of the egg, one every two frames,
    /// and line up on the stage boards (the space above the egg is the card's).
    private func messageTiles(_ gag: CircusGag, local: Double, p: Double) -> some View {
        let W = size.width, H = size.height
        let lines = gag.lines
        let perLine = max(1, lines.map(\.count).max() ?? 1)
        let bannerW = min(W - curtainWidth * 2 - 24, 420)
        let tile = max(11, min(24, bannerW / CGFloat(perLine)))
        let rowH = tile * 1.35
        let boardsMid = groundY + (H - groundY) / 2
        let top = boardsMid - CGFloat(lines.count) * rowH / 2
        var tiles: [(ch: String, x: CGFloat, y: CGFloat)] = []
        for (row, line) in lines.enumerated() {
            let x0 = W / 2 - CGFloat(line.count) * tile / 2 + tile / 2
            for (col, ch) in line.enumerated() where ch != " " {
                tiles.append((String(ch), x0 + CGFloat(col) * tile, top + CGFloat(row) * rowH + tile / 2))
            }
        }
        let ox = W / 2 + eggWidth * 0.3
        let oy = eggTop + eggHeight * 0.4
        return ZStack(alignment: .topLeading) {
            ForEach(Array(tiles.enumerated()), id: \.offset) { i, t in
                let k = ceil(max(0, local - Double(i) * 2) / 2) * 2  // on twos
                let e = min(1, k / 10)
                let ease = 1 - (1 - e) * (1 - e)
                let wobble = Double(i % 2 == 1 ? 1 : -1) * (1 - e) * 40
                Text(t.ch)
                    .font(.system(size: tile * 0.78, weight: .heavy, design: i % 2 == 0 ? .monospaced : .serif))
                    .foregroundStyle(Circus.ink)
                    .frame(width: tile - 1, height: tile * 1.1)
                    .background(Circus.palette[(i + gag.id) % Circus.palette.count])
                    .overlay(Rectangle().strokeBorder(Circus.ink, lineWidth: 1))
                    .rotationEffect(.degrees(wobble + Double((i % 3) - 1) * 2 * e))
                    .position(x: ox + (t.x - ox) * CGFloat(ease), y: oy + (t.y - oy) * CGFloat(ease))
                    .opacity(k <= 0 ? 0 : p > 0.85 ? 0.4 : 1)
            }
        }
        .accessibilityLabel(lines.joined(separator: " "))
    }

    // MARK: Ambient gags

    /// A free horizontal band for a prop of height `h` (its top y), clear of the
    /// HUD cards (the web's `pickLane`), or nil when every lane is covered.
    private func freeLane(minY: CGFloat, maxY: CGFloat, h: CGFloat, margin: CGFloat, seed: Double) -> CGFloat? {
        let steps = 12
        var ys = (0..<steps).map { minY + (maxY - minY) * CGFloat($0) / CGFloat(steps - 1) }
        // A stable shuffle by the gag's seed (Fisher-Yates on a seeded sequence).
        for i in stride(from: ys.count - 1, to: 0, by: -1) {
            let r = Circus.hash01("\(seed):\(i)")
            ys.swapAt(i, min(i, Int(r * Double(i + 1))))
        }
        let hud = hud.boxes
        return ys.first { y in
            let band = CGRect(x: 0, y: y - margin, width: size.width, height: h + margin * 2)
            return !hud.contains { $0.intersects(band) }
        }
    }

    /// The hop of an ambient cut-out on twos (or on the walker step with music).
    private func hopOffset(_ amount: Double, seed: Double) -> CGFloat {
        if let calm { return -CGFloat(calm.step * amount) }
        return (frame / 2 + Int(seed * 10)) % 2 == 0 ? -CGFloat(amount) : 0
    }

    @ViewBuilder
    private func ambientView(_ gag: CircusGag, local: Double, p: Double) -> some View {
        let W = size.width, H = size.height
        switch gag.kind {
        case .banner:
            // The announcer's card slides in from the left in four steps, holds, then leaves right.
            let text = gag.lines.first ?? ""
            let w = min(W * 0.8, 360)
            let h: CGFloat = 46
            if let y = freeLane(minY: layout.topInset + 40, maxY: eggTop - h, h: h, margin: 6, seed: gag.seed) {
                let rest = (W - w) / 2
                let kin = min(1, p / 0.1)
                let kout = p > 0.9 ? (p - 0.9) / 0.1 : 0
                let x = kout > 0 ? rest + (W - rest) * CGFloat(ceil(kout * 4) / 4)
                    : -w + (rest + w) * CGFloat(ceil(kin * 4) / 4)
                Text(text)
                    .font(.eggifySerifSemiBold(16, relativeTo: .headline))
                    .foregroundStyle(Circus.paper)
                    .lineLimit(1)
                    .minimumScaleFactor(0.7)
                    .padding(.horizontal, 14)
                    .frame(width: w, height: h)
                    .background(Color(circusHex: 0x9e2f4c))
                    .overlay(Rectangle().strokeBorder(Circus.ink, lineWidth: 2.5))
                    .overlay(Rectangle().strokeBorder(Circus.mustard, lineWidth: 1.5).padding(4))
                    .rotationEffect(.degrees(-1.2))
                    .position(x: x + w / 2, y: y + h / 2 + hopOffset(5, seed: gag.seed))
            }

        case .tea:
            // A parade of a teapot and three cups hops across the boards.
            let h = min(H * 0.1, 84)
            let gap = h * 1.5
            let total = gap * 4
            let lead = W + 40 - CGFloat(p) * (W + total + 120)
            ForEach(0..<4, id: \.self) { i in
                let isPot = i == 0
                let w = h * (isPot ? 140.0 / 110.0 : 90.0 / 70.0)
                Image(isPot ? "CircusTeapot" : "CircusTeacup")
                    .resizable()
                    .frame(width: isPot ? w : w * 0.8, height: isPot ? h : h * 0.8)
                    .rotationEffect(.degrees((frame / 2 + i) % 2 == 0 ? 7 : -7))
                    .position(x: lead + CGFloat(i) * gap, y: groundY - h * 0.4 + hopOffset(16, seed: Double(i)))
            }

        case .stalk:
            // A bureaucrat's head on a long stalk rises out of the hills, says his line, sinks.
            let h = min(H * 0.34, 260)
            let w = h * 120 / 150
            let x = W * (0.6 + gag.seed * 0.28)
            let rise: CGFloat = p < 0.12 ? ceil(p / 0.12 * 3) / 3 : p > 0.88 ? 1 - ceil((p - 0.88) / 0.12 * 3) / 3 : 1
            let baseY = groundY - H * 0.05
            ZStack(alignment: .bottom) {
                Image("CircusStalkHead")
                    .resizable()
                    .frame(width: w, height: h)
                    .offset(y: (1 - rise) * h + hopOffset(6, seed: gag.seed))
            }
            .frame(width: w, height: h, alignment: .bottom)
            .clipped()
            .position(x: x, y: baseY - h / 2)
            if rise >= 1, let line = gag.lines.first {
                Text(line)
                    .font(.eggifySerif(13, relativeTo: .footnote))
                    .foregroundStyle(Circus.ink)
                    .multilineTextAlignment(.center)
                    .padding(8)
                    .frame(maxWidth: 150)
                    .background(Circus.paper, in: RoundedRectangle(cornerRadius: 10))
                    .overlay(RoundedRectangle(cornerRadius: 10).strokeBorder(Circus.ink, lineWidth: 1.5))
                    .fixedSize(horizontal: false, vertical: true)
                    .position(x: max(90, x - w * 0.8), y: baseY - h * 0.8)
            }

        case .crossing(let prop):
            crossing(prop, gag: gag, p: p)

        default:
            EmptyView()
        }
    }

    @ViewBuilder
    private func crossing(_ prop: CircusCrossingProp, gag: CircusGag, p: Double) -> some View {
        let W = size.width, H = size.height
        let cfg = prop.crossing
        // On a phone the stage is tall and narrow: size by the shorter side too.
        let h = min(H * cfg.height, W * cfg.height * 1.3)
        let w = h * prop.artSize.width / prop.artSize.height
        let dir: CGFloat = prop == .shootingstar ? 1 : (gag.seed < 0.5 ? 1 : -1)
        let x = dir > 0 ? -w / 2 + CGFloat(p) * (W + w) : W + w / 2 - CGFloat(p) * (W + w)
        let y: CGFloat? = {
            switch cfg.lane {
            case .sky:
                let wave = H * cfg.wave
                guard let top = freeLane(minY: H * 0.08, maxY: max(H * 0.08, groundY - eggHeight - h),
                                         h: h, margin: wave + 6, seed: gag.seed) else { return nil }
                return top + h / 2 + sin(p * .pi * 4 + gag.seed * 6) * wave
            case .horizon:
                return groundY - H * 0.08 - h / 2
            case .diagonal:
                return H * 0.04 + CGFloat(p) * H * 0.3
            case .ground:
                return groundY - h * (prop == .cat ? 0.78 : 0.97) + h / 2
            }
        }()
        if let y {
            Image(prop.asset)
                .resizable()
                .frame(width: w, height: h)
                .scaleEffect(x: dir < 0 && cfg.lane != .diagonal ? -1 : 1, y: 1)
                .rotationEffect(.degrees((frame / 2) % 2 == 0 ? cfg.hop / 4 : -cfg.hop / 6), anchor: .bottom)
                .position(x: x, y: y + hopOffset(cfg.hop, seed: gag.seed) + CGFloat(Circus.jitter(w, frame, every: 4)))
        }
    }

    private var quill: some View {
        Image("CircusQuill")
            .resizable()
            .frame(width: 26, height: 52)
    }

    private func notes(frame f: Int, origin: CGPoint) -> some View {
        ZStack(alignment: .topLeading) {
            ForEach(0..<4, id: \.self) { i in
                let t = CGFloat((f + i * 4) % 16)
                Text(["♪", "♫", "♩", "♬"][i])
                    .font(.system(size: 20, weight: .bold, design: .serif))
                    .foregroundStyle(Circus.ink)
                    .opacity(Double(1 - t / 16))
                    .offset(x: origin.x + t * 3, y: origin.y - t * 2.5 + CGFloat(i % 2) * 10)
            }
        }
    }

    // A prompt went out: the cherub swoops down beside the egg, toots, and flies off.
    private var cherub: some View {
        Group {
            if let start = model.cherubAt {
                let p = now.timeIntervalSince(start) / 3.0
                if p >= 0, p < 1 {
                    let w = min(size.width * 0.32, 140)
                    let hx = size.width / 2 + eggWidth * 0.55 + w * 0.3
                    let hy = eggTop - w * 0.2
                    let pos: CGPoint = {
                        if p < 0.25 {
                            let k = ceil(p / 0.25 * 4) / 4
                            return CGPoint(x: size.width + w - (size.width + w - hx) * k, y: -w + (hy + w) * k)
                        } else if p < 0.7 {
                            return CGPoint(x: hx, y: hy)
                        } else {
                            let k = ceil((p - 0.7) / 0.3 * 4) / 4
                            return CGPoint(x: hx + k * size.width * 0.5, y: hy - k * (hy + w * 1.4))
                        }
                    }()
                    let f = frame
                    ZStack(alignment: .topLeading) {
                        Image("CircusCherub")
                            .resizable()
                            .scaledToFit()
                            .frame(width: w)
                            .offset(y: (f / 2) % 2 == 0 ? -5 : 0)
                            .rotationEffect(.degrees((f / 2) % 2 == 0 ? -4 : 3))
                        if p >= 0.25, p < 0.7 {
                            notes(frame: f, origin: CGPoint(x: w * 0.9, y: w * 0.1))
                        }
                    }
                    .position(pos)
                    .accessibilityHidden(true)
                }
            }
        }
    }

    // MARK: The foot

    private var isStomping: Bool {
        guard let start = model.footAt else { return false }
        let t = now.timeIntervalSince(start)
        return t > 0.3 && t < 1.1
    }

    /// The stage shakes for a moment when the foot lands.
    private var shake: CGSize {
        guard !still, let start = model.footAt else { return .zero }
        let t = now.timeIntervalSince(start)
        guard t > 0.3, t < 0.8 else { return .zero }
        let a = (0.8 - t) / 0.5 * 6
        return CGSize(width: Circus.jitter(1, frame, every: 1) * a, height: Circus.jitter(2, frame, every: 1) * a)
    }

    // An error or a stop: the giant foot comes down on the egg.
    private var foot: some View {
        Group {
            if let start = model.footAt {
                let t = now.timeIntervalSince(start)
                if t >= 0, t < 1.6 {
                    let height = eggHeight * 1.25
                    let rest = groundY - eggHeight * 0.55 - height / 2
                    let top = -height
                    // Down fast, hold, then lift.
                    let y: CGFloat = t < 0.3 ? top + (rest - top) * t / 0.3
                        : t < 1.1 ? rest
                        : rest + (top - rest) * (t - 1.1) / 0.5
                    Image("CircusFoot")
                        .resizable()
                        .scaledToFit()
                        .frame(height: height)
                        .position(x: size.width / 2, y: y)
                        .accessibilityHidden(true)
                }
            }
        }
    }
}
