//
//  UsageLimit.swift
//  eggify
//
//  The account's plan usage, from GET /api/agent/usage-limit: Claude's latest
//  rate-limit report (from the Claude CLI's last request) and, with
//  `?codex=1`, Codex's plan windows. The texts are ported from the web app's
//  `utils/agents/limitWait.ts`, so the phone and the desktop say the same.
//

import Foundation

/// Claude's latest rate-limit report.
nonisolated struct UsageLimitInfo: Equatable, Sendable {
    /// "allowed", "allowed_warning" or "rejected".
    var status: String
    /// "five_hour", "seven_day", ….
    var limitType: String?
    var resetsAt: Date?
    /// 0...1, when the CLI reported it.
    var utilization: Double?
    var observedAt: Date?

    static func parse(_ data: [String: Any]?) -> UsageLimitInfo? {
        guard let data, let status = data.nonEmptyString("status") else { return nil }
        return UsageLimitInfo(
            status: status,
            limitType: data.nonEmptyString("limit_type"),
            resetsAt: EggifyDate.parse(data.string("resets_at")),
            utilization: data.double("utilization"),
            observedAt: EggifyDate.parse(data.string("observed_at"))
        )
    }

    var isRejected: Bool { status == "rejected" }

    /// The meter's percentage: the utilization, else 75 for a warning.
    var percent: Double? {
        if let utilization { return utilization * 100 }
        return status == "allowed_warning" ? 75 : nil
    }

    var level: UsageLevel { UsageLevel(percent: percent, reached: isRejected) }
}

/// One of Codex's plan windows.
nonisolated struct CodexUsageWindow: Equatable, Sendable {
    var usedPercent: Double
    var windowMinutes: Int?
    var resetsAt: Date?

    static func parse(_ data: [String: Any]) -> CodexUsageWindow? {
        guard let used = data.double("used_percent") else { return nil }
        return CodexUsageWindow(
            usedPercent: used,
            windowMinutes: data.int("window_minutes"),
            resetsAt: EggifyDate.parse(data.string("resets_at"))
        )
    }

    var label: String { UsageText.windowLabel(windowMinutes) }
}

/// Codex's plan usage (ChatGPT sign-in only).
nonisolated struct CodexUsageInfo: Equatable, Sendable {
    var planType: String?
    var windows: [CodexUsageWindow]
    var limitReached: Bool
    var observedAt: Date?

    static func parse(_ data: [String: Any]?) -> CodexUsageInfo? {
        guard let data else { return nil }
        return CodexUsageInfo(
            planType: data.nonEmptyString("plan_type"),
            windows: data.dicts("windows").compactMap(CodexUsageWindow.parse),
            limitReached: data.bool("limit_reached") ?? false,
            observedAt: EggifyDate.parse(data.string("observed_at"))
        )
    }

    /// The window closest to its limit (the one that will stop Codex first).
    var tightestWindow: CodexUsageWindow? {
        windows.reduce(nil) { best, w in
            guard let best else { return w }
            return w.usedPercent > best.usedPercent ? w : best
        }
    }

    var level: UsageLevel {
        UsageLevel(percent: tightestWindow?.usedPercent, reached: limitReached)
    }
}

/// The body of GET /api/agent/usage-limit.
nonisolated struct UsageLimitReport: Equatable, Sendable {
    var claude: UsageLimitInfo?
    var codex: CodexUsageInfo?
    /// The answer had a `codex` field (it was asked with `?codex=1`).
    var hasCodex: Bool

    static func parse(_ data: [String: Any]) -> UsageLimitReport {
        UsageLimitReport(
            claude: UsageLimitInfo.parse(data.dict("limit")),
            codex: CodexUsageInfo.parse(data.dict("codex")),
            hasCodex: data["codex"] != nil
        )
    }
}

/// How the usage line looks: calm below 75 %, warn from 75 %, limited when reached.
nonisolated enum UsageLevel: String, Sendable {
    case calm, warn, limited

    init(percent: Double?, reached: Bool) {
        if reached { self = .limited }
        else if let percent, percent >= 75 { self = .warn }
        else { self = .calm }
    }
}

/// What a session's usage line shows.
nonisolated struct SessionUsage: Equatable, Sendable {
    var text: String
    var level: UsageLevel
    var isCodex: Bool

    /// The usage of the runtime the session bills to: Codex's for a Codex
    /// session, else Claude's. Nil when there's nothing to show.
    static func make(isCodex: Bool, claude: UsageLimitInfo?, codex: CodexUsageInfo?, now: Date = Date()) -> SessionUsage? {
        let text = isCodex ? UsageText.codexSummary(codex, now: now) : UsageText.claudeSummary(claude, now: now)
        guard !text.isEmpty else { return nil }
        let level = isCodex ? (codex?.level ?? .calm) : (claude?.level ?? .calm)
        return SessionUsage(text: text, level: level, isCodex: isCodex)
    }
}

nonisolated enum UsageText {
    /// Whether a session runs on Codex (provider "codex", or a model name with "codex").
    static func isCodexRuntime(provider: String?, model: String?) -> Bool {
        (provider ?? "").trimmingCharacters(in: .whitespacesAndNewlines).lowercased() == "codex"
            || (model ?? "").range(of: "codex", options: .caseInsensitive) != nil
    }

    /// "02:01", "02:01 tomorrow" or "Sat 09:00" in the viewer's time.
    static func clockText(_ at: Date, now: Date, calendar: Calendar = .current) -> String {
        let time = timeFormatter.string(from: at)
        if calendar.isDate(at, inSameDayAs: now) { return time }
        if let tomorrow = calendar.date(byAdding: .day, value: 1, to: now),
           calendar.isDate(at, inSameDayAs: tomorrow) {
            return "\(time) tomorrow"
        }
        return "\(weekdayFormatter.string(from: at)) \(time)"
    }

    static func limitTypeLabel(_ type: String?) -> String {
        switch type {
        case "five_hour": return "5-hour window"
        case "seven_day", "seven_day_overage_included": return "weekly limit"
        case "seven_day_opus": return "weekly Opus limit"
        case "seven_day_sonnet": return "weekly Sonnet limit"
        case "overage": return "extra usage"
        default: return (type ?? "").replacingOccurrences(of: "_", with: " ")
        }
    }

    /// "Session limit: 62% used · resets 02:00"; "" when nothing worth showing.
    static func claudeSummary(_ info: UsageLimitInfo?, now: Date) -> String {
        guard let info else { return "" }
        let when = resetsText(info.resetsAt, now: now)
        let kind = info.limitType == "five_hour" ? "session limit" : nonEmpty(limitTypeLabel(info.limitType)) ?? "usage limit"
        let name = kind.prefix(1).uppercased() + kind.dropFirst()
        if info.isRejected { return "\(name) reached\(when)" }
        if let utilization = info.utilization { return "\(name): \(roundPercent(utilization * 100))% used\(when)" }
        return info.status == "allowed_warning" ? "\(name) almost reached\(when)" : ""
    }

    /// "Weekly limit", "5-hour limit", "2-day limit" from a window's length.
    static func windowLabel(_ minutes: Int?) -> String {
        guard let minutes, minutes != 0 else { return "Usage limit" }
        if minutes == 10080 { return "Weekly limit" }
        if minutes % 1440 == 0 { return "\(minutes / 1440)-day limit" }
        if minutes % 60 == 0 { return "\(minutes / 60)-hour limit" }
        return "\(minutes)-minute limit"
    }

    /// "Weekly limit: 50% used · resets Fri 04:00" for Codex; "" when unknown.
    static func codexSummary(_ info: CodexUsageInfo?, now: Date) -> String {
        guard let info, let w = info.tightestWindow else { return "" }
        return codexWindowSummary(w, reached: info.limitReached, now: now)
    }

    /// One Codex window's line ("5-hour limit: 20% used · resets 14:00").
    static func codexWindowSummary(_ w: CodexUsageWindow, reached: Bool, now: Date) -> String {
        let when = resetsText(w.resetsAt, now: now)
        if reached || w.usedPercent >= 100 { return "\(w.label) reached\(when)" }
        return "\(w.label): \(roundPercent(w.usedPercent))% used\(when)"
    }

    // MARK: Helpers

    /// " · resets 02:00" for a reset still ahead; "" otherwise.
    private static func resetsText(_ at: Date?, now: Date) -> String {
        guard let at, at > now else { return "" }
        return " · resets \(clockText(at, now: now))"
    }

    /// JavaScript's Math.round for the positive percentages used here.
    private static func roundPercent(_ value: Double) -> Int {
        Int((value + 0.5).rounded(.down))
    }

    private static func nonEmpty(_ s: String) -> String? { s.isEmpty ? nil : s }

    private static let timeFormatter: DateFormatter = {
        let f = DateFormatter()
        f.setLocalizedDateFormatFromTemplate("jjmm")
        return f
    }()

    private static let weekdayFormatter: DateFormatter = {
        let f = DateFormatter()
        f.setLocalizedDateFormatFromTemplate("EEE")
        return f
    }()
}
