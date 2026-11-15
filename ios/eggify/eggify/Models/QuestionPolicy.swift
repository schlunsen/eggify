//
//  QuestionPolicy.swift
//  eggify
//
//  What happens when the agent asks a question (AskUserQuestion) and nobody
//  answers: wait, auto-pick after a while, or auto-pick at once. The server
//  keeps a global default and an optional override per session. Confirmation
//  cards (`requires_user`, e.g. loop proposals) never auto-answer.
//

import Foundation

nonisolated struct QuestionPolicy: Equatable, Hashable, Sendable {
    nonisolated enum Mode: String, Equatable, Hashable, Sendable {
        case wait
        case autoAfter = "auto_after"
        case auto
    }

    var mode: Mode
    /// Only for `autoAfter` (10...86400 seconds on the server).
    var timeoutSeconds: Int?

    static let wait = QuestionPolicy(mode: .wait, timeoutSeconds: nil)
    static let auto = QuestionPolicy(mode: .auto, timeoutSeconds: nil)
    static func autoAfter(_ seconds: Int) -> QuestionPolicy {
        QuestionPolicy(mode: .autoAfter, timeoutSeconds: seconds)
    }

    /// The server's default when the user never set one.
    static let serverDefault = autoAfter(120)

    /// What the pickers offer, in order.
    static let choices: [QuestionPolicy] = [
        .wait, .autoAfter(30), .autoAfter(60), .autoAfter(120), .autoAfter(300),
        .autoAfter(600), .autoAfter(1800), .auto,
    ]

    /// Parses a policy object; nil for null, a missing key or an unknown mode.
    static func parse(_ value: Any?) -> QuestionPolicy? {
        guard let data = value as? [String: Any],
              let mode = data.string("mode").flatMap(Mode.init(rawValue:)) else { return nil }
        if mode == .autoAfter {
            guard let seconds = data.int("timeout_seconds"), seconds > 0 else { return nil }
            return .autoAfter(seconds)
        }
        return QuestionPolicy(mode: mode, timeoutSeconds: nil)
    }

    /// The wire form.
    var json: [String: Any] {
        var out: [String: Any] = ["mode": mode.rawValue]
        if mode == .autoAfter, let timeoutSeconds { out["timeout_seconds"] = timeoutSeconds }
        return out
    }

    /// "Wait for me" / "Auto-pick after 2 min" / "Auto-pick now".
    var label: String {
        switch mode {
        case .wait: return "Wait for me"
        case .auto: return "Auto-pick now"
        case .autoAfter: return "Auto-pick after \(Self.duration(timeoutSeconds ?? 0))"
        }
    }

    /// "30 s", "1 min", "1 min 30 s", "2 h", "1 h 30 min".
    static func duration(_ seconds: Int) -> String {
        let s = max(0, seconds)
        if s < 60 { return "\(s) s" }
        if s < 3600 {
            let rest = s % 60
            return rest == 0 ? "\(s / 60) min" : "\(s / 60) min \(rest) s"
        }
        let minutes = (s % 3600) / 60
        return minutes == 0 ? "\(s / 3600) h" : "\(s / 3600) h \(minutes) min"
    }
}

/// Countdown text for a question that will be auto-answered.
nonisolated enum QuestionCountdown {
    /// "0:05", "1:42", "12:00", "1:02:03" (rounded up, never negative).
    static func format(_ remaining: TimeInterval) -> String {
        let total = max(0, Int(remaining.rounded(.up)))
        let h = total / 3600, m = (total % 3600) / 60, s = total % 60
        if h > 0 { return String(format: "%d:%02d:%02d", h, m, s) }
        return String(format: "%d:%02d", m, s)
    }

    /// "Auto-picks “Use 400” in 1:42", or "Auto-picking “Use 400”…" when due.
    static func text(picks labels: [String], remaining: TimeInterval) -> String {
        let what = labels.isEmpty ? "an answer" : labels.map { "“\($0)”" }.joined(separator: ", ")
        if remaining <= 0 { return "Auto-picking \(what)…" }
        return "Auto-picks \(what) in \(format(remaining))"
    }
}

nonisolated extension UserQuestion {
    /// Labels ending in "(Recommended)" (case-insensitive, trailing spaces ignored).
    static func isRecommendedLabel(_ label: String) -> Bool {
        label.trimmingCharacters(in: .whitespaces).lowercased().hasSuffix("(recommended)")
    }

    /// The label without its "(Recommended)" suffix, for showing next to a badge.
    static func strippedLabel(_ label: String) -> String {
        let trimmed = label.trimmingCharacters(in: .whitespaces)
        guard isRecommendedLabel(trimmed) else { return label }
        let stripped = String(trimmed.dropLast("(recommended)".count)).trimmingCharacters(in: .whitespaces)
        return stripped.isEmpty ? label : stripped
    }

    /// Option indexes marked as recommended: the server's list, or (older
    /// servers) the labels that say so. Never for confirmation cards.
    var recommendedIndexes: Set<Int> {
        guard !requiresUser else { return [] }
        if let recommended { return Set(recommended.filter { options.indices.contains($0) }) }
        return Set(options.indices.filter { Self.isRecommendedLabel(options[$0].label) })
    }

    /// What the auto-pick would choose (the server's rule: the recommended
    /// option(s), else the first option).
    var autoPickLabels: [String] {
        let picked = recommendedIndexes.sorted().map { options[$0].label }
        if !picked.isEmpty { return multiSelect ? picked : [picked[0]] }
        return options.first.map { [$0.label] } ?? []
    }

    /// The countdown is shown only for agent questions with a running timer.
    var showsCountdown: Bool { !requiresUser && !held && autoAnswerAt != nil }

    /// "Auto-picks “X” in 1:42" at `now`, or nil without a running timer.
    func countdownText(now: Date = Date()) -> String? {
        guard showsCountdown, let at = autoAnswerAt else { return nil }
        return QuestionCountdown.text(picks: autoPickLabels.map(Self.strippedLabel),
                                      remaining: at.timeIntervalSince(now))
    }

    /// For the other-session banner: "asks: Which status code? · auto in 1:42".
    func bannerText(now: Date = Date()) -> String {
        let short = question.count > 60 ? String(question.prefix(59)) + "…" : question
        guard showsCountdown, let at = autoAnswerAt else { return "asks: \(short)" }
        return "asks: \(short) · auto in \(QuestionCountdown.format(at.timeIntervalSince(now)))"
    }
}
