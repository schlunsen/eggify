//
//  CircusMessages.swift
//  eggify
//
//  What the Circus "cut-out letters" spell: a word from the reply (the old
//  gag), a short summary of the session, or a joke. A port of the web's
//  zen/circusMessages.ts and circusMessagePools.ts, with the same pools,
//  sanitiser, context pools and no-repeat window (kept in memory here).
//
//  Only curated text, the session's cached digest summary and fixed phrases
//  reach the tiles; the last user message is matched against keywords, never shown.
//

import Foundation

enum CircusActivity: String, Sendable {
    case read, edit, bash, search, web, agent, message, error, other

    /// The category of a tool, by the same names as the web app.
    static func forTool(_ name: String) -> CircusActivity {
        switch name.lowercased() {
        case "read", "notebookread": .read
        case "edit", "write", "multiedit", "notebookedit", "filechange": .edit
        case "bash", "bashoutput", "killshell": .bash
        case "grep", "glob", "search", "ls": .search
        case "webfetch", "websearch": .web
        case "task", "agent": .agent
        default: .other
        }
    }
}

enum CircusJokePool: String, CaseIterable, Sendable {
    case general, oops, done, idle, build
}

enum CircusMessages {
    /// Longest message spelled across the tiles.
    static let maxMessageChars = 28
    /// Characters per tile line; a message wraps to at most `maxLines` lines.
    static let lineChars = 18
    static let maxLines = 2
    /// A joke or summary is never repeated within this many picks.
    static let recentWindow = 15
    /// Chance that a firing is a joke or summary instead of a word from the reply.
    static let specialChance = 0.4
    /// Of those, the share that is a summary (the rest are jokes).
    static let summaryShare = 0.4
    /// A matching context pool is used this often (else the general pool).
    static let contextPoolChance = 0.7

    static let errorWindow: TimeInterval = 60
    static let buildWindow: TimeInterval = 60
    static let longIdle: TimeInterval = 120

    // MARK: Pools (circusMessagePools.ts)

    static let jokes: [CircusJokePool: [String]] = [
        .general: [
            "Mind the null, old bean", "Egg-cellent commit!", "Tea is a build step",
            "Mostly harmless code", "Pip pip, refactor!", "Quite the paper trail",
            "Jolly good diff, what?", "One must always rebase", "Stiff upper lip, merge",
            "A most curious stack", "Do mind the gap, chap", "Rather fond of semicolons",
            "Branch early, tea often", "Spiffing variable name!", "Cut, paste, and carry on",
            "Terribly clever, that", "Never trust a lone TODO", "Hatch a plan, old thing",
            "Sunny side up, always", "Whisk first, ask later", "Shell games are afoot",
            "Fetch the good biscuits", "Scrambled, but sound", "A tidy commit, madam",
            "Hold the hot fix!", "Cogs, springs, and YAML", "Dash it all, a comment!",
            "Behold, an endless loop", "Bravo, humble function", "Pardon me, wrong branch",
            "Hard-boiled logic, this", "The plot thickens, yolkly", "Lint, sir, is watching",
            "Frightfully good indent", "One lump or two, cache?", "May I suggest a nap?",
            "Gold star for naming",
        ],
        .oops: [
            "Sorry about the bug", "Oh crumbs, a crash", "Egg on my face, again",
            "That was not the plan", "Cracked, but not broken", "Dash it, a stack trace",
            "Frightfully sorry, old thing", "A slight mishap, ahem", "Do not look at the log",
            "Blame the intern, quietly",
        ],
        .done: [
            "Splendid, all done!", "Tea time, I say!", "Jolly well done!", "Egg-cellent work!",
            "Hip hip, hatched!", "Another one for the shelf", "Time for a biscuit",
            "Accomplished, old bean", "That will do nicely", "Cheers, all around!",
        ],
        .idle: [
            "Tea break?", "Anyone for a biscuit?", "Quiet as a sleeping egg", "Still here, still egg",
            "Humming to myself", "Counting the clouds", "Shall we begin, then?", "Waiting, most politely",
        ],
        .build: [
            "Splendid, a green build!", "Tests pass, mostly", "Compiling, with tea",
            "Behold, the steam engine", "Shovel more coal in", "Do wait for the whistle",
            "Linking, most gingerly", "A watched build never boils", "Fingers crossed, chaps",
            "Red today, green tomorrow",
        ],
    ]

    /// Local summary phrases keyed by words in the last user message. First match wins.
    static let topicPhrases: [(words: [String], phrase: String)] = [
        (["test", "spec", "vitest", "pytest"], "Fixing the tests"),
        (["bug", "error", "crash", "broken", "fix"], "Squashing a bug"),
        (["refactor", "cleanup", "clean up", "simplify"], "Tidying the code"),
        (["doc", "readme", "changelog"], "Writing it all down"),
        (["deploy", "release", "ship"], "Preparing to ship"),
        (["commit", "merge", "rebase", "branch"], "Minding the branches"),
        (["style", "css", "theme", "design", "layout"], "Making it pretty"),
        (["api", "endpoint", "server", "backend"], "Tending the server"),
        (["database", "sql", "migration", "schema"], "Filing the data"),
        (["build", "compile"], "Stoking the build"),
        (["review", "audit"], "Reviewing the work"),
        (["add", "feature", "implement", "create"], "Building something new"),
    ]

    /// Summary phrases by what the agent last did, when nothing else is known.
    static func activityPhrase(_ a: CircusActivity?) -> String {
        switch a {
        case .read: "Reading the code"
        case .edit: "Editing the code"
        case .bash: "Running a command"
        case .search: "Hunting for clues"
        case .web: "Consulting the web"
        case .agent: "Calling in helpers"
        case .error: "Mopping up a mess"
        case .message, .other: "Pondering, quietly"
        case nil: "Tea break?"
        }
    }

    // MARK: Text

    private static let allowed = Set("ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789 .,!?'-")

    /// UI-safe text for the tiles: ASCII letters, digits and basic punctuation,
    /// uppercase, single spaces, at most `max` characters (cut at a word edge).
    static func sanitize(_ input: String, max: Int = maxMessageChars) -> String {
        let stripped = String(String.UnicodeScalarView(
            input.decomposedStringWithCanonicalMapping.unicodeScalars
                .filter { !(0x300...0x36f).contains($0.value) }))
        var mapped = ""
        for ch in stripped.uppercased() {
            mapped.append(allowed.contains(ch) ? ch : " ")
        }
        let folded = mapped.split(separator: " ", omittingEmptySubsequences: true).joined(separator: " ")
        let chars = Array(folded)
        if chars.count <= max { return folded }
        let cut = Array(chars.prefix(max + 1))
        let edge = cut.lastIndex(of: " ") ?? 0
        var text = edge > 0 ? String(cut[..<edge]) : String(chars.prefix(max))
        while let last = text.last, " ,.-".contains(last) { text.removeLast() }
        return text.trimmingCharacters(in: .whitespaces)
    }

    /// Greedy word wrap into at most `maxLines` lines of `perLine` characters.
    static func wrap(_ text: String, perLine: Int = lineChars, maxLines: Int = maxLines) -> [String] {
        var lines: [String] = []
        var cur = ""
        for sub in text.split(separator: " ") {
            var word = String(sub)
            while word.count > perLine {
                if !cur.isEmpty { lines.append(cur); cur = "" }
                lines.append(String(word.prefix(perLine)))
                word = String(word.dropFirst(perLine))
            }
            if cur.isEmpty { cur = word }
            else if cur.count + 1 + word.count <= perLine { cur += " " + word }
            else { lines.append(cur); cur = word }
        }
        if !cur.isEmpty { lines.append(cur) }
        return Array(lines.prefix(maxLines))
    }

    /// First few words (2-4) of a digest summary that fit the tiles.
    static func shortenSummary(_ summary: String?) -> String? {
        let first = (summary ?? "").split(whereSeparator: { ".!?;:".contains($0) }).first.map(String.init) ?? ""
        let clean = sanitize(first, max: 80)
        var out = ""
        var n = 0
        for w in clean.split(separator: " ") {
            let next = out.isEmpty ? String(w) : out + " " + w
            if n >= 4 || next.count > maxMessageChars { break }
            out = next
            n += 1
        }
        return n >= 2 ? out : nil
    }

    static func topicPhrase(_ text: String?) -> String? {
        let t = (text ?? "").lowercased()
        guard !t.isEmpty else { return nil }
        for (words, phrase) in topicPhrases {
            // A word boundary before the keyword, as the web's `\b<word>`.
            for w in words {
                var search = t.startIndex..<t.endIndex
                while let r = t.range(of: w, range: search) {
                    let atStart = r.lowerBound == t.startIndex
                    let before = atStart ? nil : t[t.index(before: r.lowerBound)]
                    if atStart || !(before!.isLetter || before!.isNumber || before! == "_") { return phrase }
                    search = r.upperBound..<t.endIndex
                }
            }
        }
        return nil
    }
}

// MARK: - Picker

/// What the agent has been doing lately (the web's ActivityState).
struct CircusActivityState: Sendable {
    var lastEventAt: TimeInterval
    var lastTool: CircusActivity?
    var lastToolAt: TimeInterval = -.infinity
    var lastErrorAt: TimeInterval = -.infinity

    init(now: TimeInterval = 0) { lastEventAt = now }

    mutating func note(_ category: CircusActivity, isError: Bool = false, at now: TimeInterval) {
        lastEventAt = now
        if isError || category == .error { lastErrorAt = now }
        if category != .message { lastTool = category; lastToolAt = now }
    }

    /// Which joke pool fits the moment.
    func pool(idle: Bool, now: TimeInterval) -> CircusJokePool {
        if now - lastErrorAt < CircusMessages.errorWindow { return .oops }
        if idle, now - lastEventAt < CircusMessages.longIdle { return .done }
        if lastTool == .bash, now - lastToolAt < CircusMessages.buildWindow { return .build }
        if lastEventAt > 0, now - lastEventAt > CircusMessages.longIdle { return .idle }
        return .general
    }
}

struct CircusLetterMessage: Equatable, Sendable {
    enum Kind: Sendable { case word, summary, joke }
    let kind: Kind
    let text: String
}

/// Session context for summaries: the cached digest and, for keywords only, the last user message.
struct CircusMessageContext: Sendable {
    var summary: String?
    var lastUserMessage: String?
}

/// The rotation state: the no-repeat window and whether the last pick was special.
/// One in memory for the app (`shared`), like the web's per-tab state.
final class CircusMessagePicker {
    @MainActor static let shared = CircusMessagePicker()

    private(set) var recent: [String] = []
    private(set) var lastKind: CircusLetterMessage.Kind = .word

    init() {}

    private func remember(_ kind: CircusLetterMessage.Kind, _ text: String) {
        lastKind = kind
        guard kind != .word else { return }
        recent.append(text)
        if recent.count > CircusMessages.recentWindow { recent.removeFirst(recent.count - CircusMessages.recentWindow) }
    }

    private func pick(from pool: [String], rng: () -> Double) -> String? {
        let fresh = pool.filter { !recent.contains(CircusMessages.sanitize($0)) }
        guard !fresh.isEmpty else { return nil }
        return fresh[Int(rng() * Double(fresh.count)) % fresh.count]
    }

    /// A joke that fits the moment and is not in the recent window.
    func joke(_ pool: CircusJokePool, rng: () -> Double) -> String {
        let first = pool != .general && rng() < CircusMessages.contextPoolChance ? pool : .general
        let general = CircusMessages.jokes[.general] ?? []
        if let hit = pick(from: CircusMessages.jokes[first] ?? [], rng: rng) ?? pick(from: general, rng: rng) {
            return CircusMessages.sanitize(hit)
        }
        return CircusMessages.sanitize(general.isEmpty ? "Splendid!" : general[Int(rng() * Double(general.count)) % general.count])
    }

    /// A short phrase about what the session is up to: the cached digest, then
    /// keywords of the last user message, then the last tool.
    func summary(_ ctx: CircusMessageContext, activity a: CircusActivityState, idle: Bool, now: TimeInterval) -> String {
        if let s = CircusMessages.shortenSummary(ctx.summary) { return s }
        if let t = CircusMessages.topicPhrase(ctx.lastUserMessage) { return t }
        if idle, now - a.lastEventAt > CircusMessages.longIdle { return CircusMessages.activityPhrase(nil) }
        if let tool = a.lastTool, now - a.lastToolAt < CircusMessages.longIdle {
            if now - a.lastErrorAt < CircusMessages.errorWindow { return CircusMessages.activityPhrase(.error) }
            return CircusMessages.activityPhrase(tool)
        }
        return CircusMessages.activityPhrase(nil)
    }

    /// Choose what the letters spell. `.word` keeps the old gag (a word from the
    /// reply). Roughly one firing in three is special, never two specials in a row.
    func next(context: CircusMessageContext, activity: CircusActivityState, idle: Bool,
              now: TimeInterval, rng: () -> Double = { Double.random(in: 0..<1) }) -> CircusLetterMessage {
        if lastKind != .word || rng() >= CircusMessages.specialChance {
            remember(.word, "")
            return CircusLetterMessage(kind: .word, text: "")
        }
        if rng() < CircusMessages.summaryShare {
            let text = CircusMessages.sanitize(summary(context, activity: activity, idle: idle, now: now))
            if !text.isEmpty, !recent.contains(text) {
                remember(.summary, text)
                return CircusLetterMessage(kind: .summary, text: text)
            }
        }
        let text = joke(activity.pool(idle: idle, now: now), rng: rng)
        remember(.joke, text)
        return CircusLetterMessage(kind: .joke, text: text)
    }
}
