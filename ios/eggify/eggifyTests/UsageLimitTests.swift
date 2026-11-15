//
//  UsageLimitTests.swift
//  eggifyTests
//
//  Plan usage: parsing GET /api/agent/usage-limit (Claude's report and
//  Codex's windows), and the texts and levels ported from the web's
//  `limitWait.ts`, checked against the cases in its `limitWait.test.ts`.
//

import Foundation
import Testing
@testable import eggify

@Suite("Plan usage")
struct UsageLimitTests {
    private let calendar = Calendar.current
    /// 8 Oct 2026, 22:30 local time, like the web test.
    private var now: Date { at(22, 30) }

    private func at(_ hour: Int, _ minute: Int = 0, dayOffset: Int = 0) -> Date {
        calendar.date(from: DateComponents(year: 2026, month: 10, day: 8 + dayOffset, hour: hour, minute: minute))!
    }

    private func iso(_ date: Date) -> String { ISO8601DateFormatter().string(from: date) }

    private func matches(_ text: String, _ pattern: String) -> Bool {
        text.range(of: pattern, options: .regularExpression) != nil
    }

    // MARK: Parsing

    @Test func parsesClaudesReport() {
        let report = UsageLimitReport.parse([
            "limit": ["status": "allowed_warning", "limit_type": "five_hour", "utilization": 0.9,
                      "resets_at": "2026-10-09T00:00:00Z", "observed_at": "2026-10-08T20:00:00.123456789Z"],
        ])
        let info = report.claude
        #expect(info?.status == "allowed_warning")
        #expect(info?.limitType == "five_hour")
        #expect(info?.utilization == 0.9)
        #expect(info?.resetsAt == ISO8601DateFormatter().date(from: "2026-10-09T00:00:00Z"))
        #expect(info?.observedAt != nil)
        #expect(!report.hasCodex)
        #expect(report.codex == nil)
    }

    @Test func parsesAMissingReport() {
        let report = UsageLimitReport.parse(["limit": NSNull()])
        #expect(report.claude == nil)
        #expect(!report.hasCodex)
        #expect(UsageLimitReport.parse([:]).claude == nil)
    }

    @Test func parsesCodexsWindows() {
        let report = UsageLimitReport.parse([
            "limit": NSNull(),
            "codex": ["plan_type": "plus", "limit_reached": false, "observed_at": "2026-10-08T20:00:00Z",
                      "windows": [
                        ["used_percent": 20, "window_minutes": 300, "resets_at": "2026-10-08T23:00:00Z"],
                        ["used_percent": 64, "window_minutes": 10080],
                        ["window_minutes": 60],
                      ]],
        ])
        #expect(report.hasCodex)
        let codex = report.codex
        #expect(codex?.planType == "plus")
        #expect(codex?.limitReached == false)
        #expect(codex?.windows.count == 2)
        #expect(codex?.windows.first?.resetsAt != nil)
        #expect(codex?.tightestWindow?.windowMinutes == 10080)
    }

    @Test func codexNullMeansAskedButUnknown() {
        let report = UsageLimitReport.parse(["limit": NSNull(), "codex": NSNull()])
        #expect(report.hasCodex)
        #expect(report.codex == nil)
    }

    // MARK: Texts (limitWait.test.ts)

    @Test func summarisesClaudesUsage() {
        let warning = UsageLimitInfo(status: "allowed_warning", limitType: "five_hour", resetsAt: at(2, dayOffset: 1), utilization: 0.9)
        #expect(matches(UsageText.claudeSummary(warning, now: now),
                        #"^Session limit: 90% used · resets 0?2[:.]00(\s?[AP]M)? tomorrow$"#))
        #expect(UsageText.claudeSummary(UsageLimitInfo(status: "rejected", limitType: "seven_day"), now: now) == "Weekly limit reached")
        #expect(UsageText.claudeSummary(nil, now: now) == "")
        #expect(UsageText.claudeSummary(UsageLimitInfo(status: "allowed_warning", limitType: "seven_day_opus"), now: now)
                == "Weekly Opus limit almost reached")
        #expect(UsageText.claudeSummary(UsageLimitInfo(status: "allowed"), now: now) == "")
        #expect(UsageText.claudeSummary(UsageLimitInfo(status: "allowed", utilization: 0.125), now: now) == "Usage limit: 13% used")
        // A reset in the past is left out.
        #expect(UsageText.claudeSummary(UsageLimitInfo(status: "rejected", limitType: "five_hour", resetsAt: at(20)), now: now)
                == "Session limit reached")
    }

    @Test func namesLimitTypes() {
        #expect(UsageText.limitTypeLabel("five_hour") == "5-hour window")
        #expect(UsageText.limitTypeLabel("seven_day") == "weekly limit")
        #expect(UsageText.limitTypeLabel("seven_day_overage_included") == "weekly limit")
        #expect(UsageText.limitTypeLabel("seven_day_sonnet") == "weekly Sonnet limit")
        #expect(UsageText.limitTypeLabel("overage") == "extra usage")
        #expect(UsageText.limitTypeLabel("some_new_kind") == "some new kind")
        #expect(UsageText.limitTypeLabel(nil) == "")
    }

    @Test func clockTextSaysTodayTomorrowOrTheWeekday() {
        #expect(matches(UsageText.clockText(at(23, 15), now: now), #"^(11|23)[:.]15(\s?[AP]M)?$"#))
        #expect(matches(UsageText.clockText(at(2, 1, dayOffset: 1), now: now), #"^0?2[:.]01(\s?[AP]M)? tomorrow$"#))
        let later = UsageText.clockText(at(9, dayOffset: 3), now: now)
        #expect(matches(later, #"^\S+ 0?9[:.]00(\s?[AP]M)?$"#))
        #expect(!later.contains("tomorrow"))
    }

    @Test func namesCodexWindowsAndPicksTheTightest() {
        #expect(UsageText.windowLabel(10080) == "Weekly limit")
        #expect(UsageText.windowLabel(300) == "5-hour limit")
        #expect(UsageText.windowLabel(2880) == "2-day limit")
        #expect(UsageText.windowLabel(45) == "45-minute limit")
        #expect(UsageText.windowLabel(nil) == "Usage limit")
        #expect(UsageText.windowLabel(0) == "Usage limit")

        let info = CodexUsageInfo(planType: nil, windows: [
            CodexUsageWindow(usedPercent: 20, windowMinutes: 300),
            CodexUsageWindow(usedPercent: 64, windowMinutes: 10080),
        ], limitReached: false)
        #expect(info.tightestWindow?.windowMinutes == 10080)
        #expect(UsageText.codexSummary(info, now: Date()) == "Weekly limit: 64% used")
        var reached = info
        reached.limitReached = true
        #expect(UsageText.codexSummary(reached, now: Date()) == "Weekly limit reached")
        #expect(UsageText.codexSummary(nil, now: Date()) == "")
        #expect(UsageText.codexSummary(CodexUsageInfo(planType: nil, windows: [], limitReached: true), now: Date()) == "")
    }

    @Test func tellsCodexSessionsApart() {
        #expect(UsageText.isCodexRuntime(provider: "codex", model: "gpt-6-sol"))
        #expect(UsageText.isCodexRuntime(provider: " Codex ", model: nil))
        #expect(UsageText.isCodexRuntime(provider: "openai", model: "gpt-5-codex"))
        #expect(!UsageText.isCodexRuntime(provider: "claude", model: "claude-opus-5-5"))
        #expect(!UsageText.isCodexRuntime(provider: nil, model: nil))
    }

    // MARK: Level

    @Test func levelFollowsThePercentage() {
        #expect(UsageLevel(percent: 50, reached: false) == .calm)
        #expect(UsageLevel(percent: 80, reached: false) == .warn)
        #expect(UsageLevel(percent: 75, reached: false) == .warn)
        #expect(UsageLevel(percent: 10, reached: true) == .limited)
        #expect(UsageLevel(percent: nil, reached: false) == .calm)
        // Claude: a warning without a number counts as 75 %.
        #expect(UsageLimitInfo(status: "allowed_warning").level == .warn)
        #expect(UsageLimitInfo(status: "rejected", utilization: 0.2).level == .limited)
    }

    @Test func sessionUsageShowsTheRuntimeItBillsTo() {
        let claude = UsageLimitInfo(status: "allowed", limitType: "seven_day", utilization: 0.8)
        let codex = CodexUsageInfo(planType: "pro", windows: [CodexUsageWindow(usedPercent: 30, windowMinutes: 300)], limitReached: false)
        let forClaude = SessionUsage.make(isCodex: false, claude: claude, codex: codex, now: now)
        #expect(forClaude?.text == "Weekly limit: 80% used")
        #expect(forClaude?.level == .warn)
        let forCodex = SessionUsage.make(isCodex: true, claude: claude, codex: codex, now: now)
        #expect(forCodex?.text == "5-hour limit: 30% used")
        #expect(forCodex?.level == .calm)
        #expect(SessionUsage.make(isCodex: true, claude: claude, codex: nil, now: now) == nil)
        #expect(SessionUsage.make(isCodex: false, claude: nil, codex: codex, now: now) == nil)
    }
}
