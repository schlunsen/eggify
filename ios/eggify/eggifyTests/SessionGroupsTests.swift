//
//  SessionGroupsTests.swift
//  eggifyTests
//
//  The Agents list's groups and tag colours, mirroring the web's
//  `__tests__/utils/sessionGroups.test.ts` and `tagColor.test.ts`.
//

import Foundation
import Testing
@testable import eggify

@Suite("Session groups")
@MainActor
struct SessionGroupsTests {
    // A fixed local "now": Tuesday 6 Oct 2026, 15:00 in a fixed time zone.
    private static let calendar: Calendar = {
        var c = Calendar(identifier: .gregorian)
        c.timeZone = TimeZone(identifier: "Europe/Copenhagen")!
        return c
    }()
    private static func date(_ h: Int, _ m: Int = 0, day: Int = 0, month: Int = 10) -> Date {
        calendar.date(from: DateComponents(year: 2026, month: month, day: 6 + day, hour: h, minute: m))!
    }
    private let now = date(15)
    private func at(_ h: Int, _ m: Int = 0, day: Int = 0) -> String {
        EggifyDate.string(from: Self.date(h, m, day: day))
    }

    private func s(_ id: String, _ status: String, _ updated: String,
                   subagents: Int = 0, waiting: String = "", loopNext: String? = nil) -> AgentSession {
        var data: [String: Any] = ["id": id, "status": status, "updated_at": updated,
                                   "subagents_running": subagents]
        if let loopNext { data["scheduled_loop"] = ["next_at": loopNext, "delay_seconds": 1200] }
        var session = AgentSession.parse(data)!
        session.agent_name = waiting.isEmpty ? nil : waiting
        return session
    }

    private func group(_ sessions: [AgentSession]) -> [SessionGroup] {
        SessionGroups.group(sessions, now: now, calendar: Self.calendar,
                            waitingFor: { $0.agent_name ?? "" })
    }

    private func ids(_ groups: [SessionGroup], _ id: SessionGroupID) -> [String] {
        groups.first { $0.id == id }!.rows.map(\.session.id)
    }

    @Test func sortsIntoNeedsWorkingTodayEarlierNewestFirst() {
        let groups = group([
            s("old", "idle", at(10, day: -2)),
            s("q", "idle", at(9), waiting: "Question waiting"),
            s("busy", "processing", at(14)),
            s("sub", "idle", at(11), subagents: 2),
            s("t1", "idle", at(8)),
            s("t2", "idle", at(13)),
            s("perm", "processing", at(14, 30), waiting: "Allow git push?"),
        ])
        #expect(groups.map(\.id) == [.needs, .working, .today, .earlier])
        #expect(groups.map(\.label) == ["Needs you", "Working", "Today", "Earlier"])
        #expect(ids(groups, .needs) == ["perm", "q"])
        #expect(ids(groups, .working) == ["busy", "sub"])
        #expect(ids(groups, .today) == ["t2", "t1"])
        #expect(ids(groups, .earlier) == ["old"])
        #expect(groups[0].rows[0].waitingFor == "Allow git push?")
        #expect(groups[1].rows.first { $0.session.id == "sub" }?.subagents == 2)
        #expect(groups[1].rows.first { $0.session.id == "busy" }?.subagents == 0)
    }

    @Test func splitsTodayFromEarlierAtLocalMidnight() {
        let midnight = Self.calendar.startOfDay(for: now)
        let groups = group([
            s("just-after", "idle", EggifyDate.string(from: midnight.addingTimeInterval(1))),
            s("just-before", "idle", EggifyDate.string(from: midnight.addingTimeInterval(-1))),
        ])
        #expect(ids(groups, .today) == ["just-after"])
        #expect(ids(groups, .earlier) == ["just-before"])
    }

    @Test func idleSessionsStayFullForTwelveHours() {
        let groups = group([
            s("busy", "processing", at(14)),
            s("just-idle", "idle", at(14, 30)),
            s("idle-11h", "idle", at(3, 30)),
            s("idle-13h", "idle", at(2)),
            s("late-yesterday", "idle", at(23, day: -1)),
            s("ended-now", "ended", at(14, 45)),
        ])
        let full = Dictionary(uniqueKeysWithValues: groups.flatMap(\.rows).map { ($0.session.id, $0.full) })
        #expect(full == ["busy": true, "just-idle": true, "idle-11h": true,
                         "idle-13h": false, "late-yesterday": false, "ended-now": false])
    }

    @Test func endedSessionsGoOnlyToTodayOrEarlier() {
        let groups = group([
            s("e1", "ended", at(12), subagents: 3, waiting: "Question waiting"),
            s("e2", "ended", at(12, day: -1)),
        ])
        #expect(ids(groups, .needs).isEmpty)
        #expect(ids(groups, .working).isEmpty)
        #expect(ids(groups, .today) == ["e1"])
        #expect(ids(groups, .earlier) == ["e2"])
        #expect(groups[2].rows[0].waitingFor == "")
    }

    @Test func scheduledClaudeCodeLoopCountsAsWorking() {
        let next = EggifyDate.string(from: now.addingTimeInterval(20 * 60))
        let stale = EggifyDate.string(from: now.addingTimeInterval(-10 * 60))
        let groups = group([
            s("loop", "idle", at(9), loopNext: next),
            s("stale", "idle", at(9, 30), loopNext: stale),
        ])
        #expect(ids(groups, .working) == ["loop"])
        #expect(ids(groups, .today) == ["stale"])
    }

    @Test func usesLiveStatusAndKeepsScheduledLoopOnPartialUpdates() {
        let next = EggifyDate.string(from: now.addingTimeInterval(600))
        let session = s("a", "idle", at(12), loopNext: next)
        #expect(session.merged(with: ["status": "idle"]).scheduled_loop_next_at == next)
        #expect(session.merged(with: ["scheduled_loop": NSNull()]).scheduled_loop_next_at == nil)
        let groups = SessionGroups.group([s("b", "idle", at(12))], now: now, calendar: Self.calendar,
                                         status: { _ in .processing })
        #expect(ids(groups, .working) == ["b"])
    }

    @Test func dropsDuplicateIdsAndFallsBackToCreatedAt() {
        let a = AgentSession.parse(["id": "a", "status": "idle", "created_at": at(12), "updated_at": ""])!
        let b = AgentSession.parse(["id": "b", "status": "idle", "created_at": "", "updated_at": ""])!
        let groups = group([a, a, b])
        #expect(ids(groups, .today) == ["a"])
        #expect(ids(groups, .earlier) == ["b"])
    }

    @Test func visibleOrderSkipsCollapsedGroupsButNeverNeedsOrWorking() {
        let groups = group([
            s("old", "idle", at(10, day: -2)),
            s("busy", "processing", at(14)),
            s("t1", "idle", at(8)),
            s("q", "idle", at(9), waiting: "Question waiting"),
        ])
        #expect(SessionGroups.visibleOrder(groups, collapsed: [:]).map(\.id) == ["q", "busy", "t1"])
        #expect(SessionGroups.visibleOrder(groups, collapsed: [.today: true, .earlier: false]).map(\.id)
                == ["q", "busy", "old"])
        #expect(SessionGroups.visibleOrder(groups, collapsed: [.needs: true, .working: true, .earlier: false]).map(\.id)
                == ["q", "busy", "t1", "old"])
    }

    @Test func relativeTimes() {
        let c = Self.calendar
        #expect(SessionGroups.relativeTime(now.addingTimeInterval(-20), now: now, calendar: c) == "now")
        #expect(SessionGroups.relativeTime(now.addingTimeInterval(-4 * 60), now: now, calendar: c) == "4m")
        #expect(SessionGroups.relativeTime(now.addingTimeInterval(-2 * 3600), now: now, calendar: c) == "2h")
        #expect(SessionGroups.relativeTime(Self.date(23, day: -1), now: now, calendar: c) == "Mon")
        #expect(SessionGroups.relativeTime(Self.date(12, day: 14, month: 9), now: now, calendar: c) == "20 Sep")
        #expect(SessionGroups.relativeTime(nil, now: now, calendar: c) == "")
    }

    // MARK: Waiting labels

    private func permission(_ tool: String, _ details: [String: Any]? = nil) -> PermissionRequest {
        PermissionRequest(id: "p", sessionId: "s", tool: tool, description: "",
                          details: details.map { JSONValue(any: $0) }, receivedAt: Date())
    }

    @Test func permissionLabelsNameTheCommandOrFile() {
        #expect(SessionGroups.permissionWaitLabel(tool: "Bash", details: JSONValue(any: ["command": "gh pr merge 1124"]))
                == "Allow gh pr merge 1124?")
        #expect(SessionGroups.permissionWaitLabel(tool: "Edit", details: JSONValue(any: ["file_path": "/repo/app/app.vue"]))
                == "Allow Edit app.vue?")
        #expect(SessionGroups.permissionWaitLabel(tool: "WebFetch", details: JSONValue(any: ["url": "https://example.com/a"]))
                == "Allow WebFetch example.com/a?")
        #expect(SessionGroups.permissionWaitLabel(tool: "WebFetch", details: nil) == "Allow WebFetch?")
        let long = SessionGroups.permissionWaitLabel(tool: "Bash", details: JSONValue(any: ["command": String(repeating: "x", count: 100)]))
        #expect(long.hasPrefix("Allow x") && long.hasSuffix("…?") && long.count == "Allow ?".count + 60)
    }

    @Test func waitingPrefersQuestionsThenPermissionsThenListedCounts() throws {
        let question = try #require(UserQuestion.parse(["session_id": "s", "question_id": "q", "question": "Which?"]))
        let loopQuestion = try #require(UserQuestion.parse([
            "session_id": "s", "question_id": "q2", "question": "Start a loop?", "kind": "loop_session",
            "proposal": ["goal": "Make tests pass"],
        ]))
        #expect(SessionGroups.waitingLabel(permissions: [permission("Bash", ["command": "ls"])], questions: [question])
                == "Question waiting")
        #expect(SessionGroups.waitingLabel(permissions: [permission("Bash", ["command": "ls"])], questions: []) == "Allow ls?")
        #expect(SessionGroups.waitingLabel(permissions: nil, questions: [loopQuestion]) == "Loop proposal waiting")
        #expect(SessionGroups.waitingLabel(permissions: nil, questions: nil, listedPermissions: 1) == "Approval needed")
        #expect(SessionGroups.waitingLabel(permissions: nil, questions: nil, listedQuestions: 2) == "Question waiting")
        // Answered on this device: the stale listed counts no longer count.
        #expect(SessionGroups.waitingLabel(permissions: [], questions: [], listedPermissions: 1, listedQuestions: 1) == "")
    }
}

@Suite("Tag colours")
struct TagColorTests {
    /// Expected values from the web's `utils/tagColor.ts` (`tagHash`, `tagHue`), run with node.
    @Test(arguments: [
        ("bug", 1943375221 as UInt32, 181),
        ("refactor", 2142990227, 227),
        ("ios", 2162924518, 238),
        ("sidebar", 3363491783, 263),
        ("release", 272019070, 190),
        ("docs", 686503122, 42),
        ("perf", 2851088486, 86),
        ("ui", 1547907707, 227),
        ("security", 3375155643, 123),
        ("tests", 1529007906, 66),
        ("n0 release", 112169322, 162),
        ("Bug fix", 2787851674, 154),
        ("  Backend ", 2750272591, 31),
        ("æøå", 3517195490, 170),
        ("🥚 egg", 269114632, 232),
        ("", 0x811c9dc5, 61),
    ])
    func matchesTheWeb(tag: String, hash: UInt32, hue: Int) {
        #expect(TagColor.hash(tag) == hash)
        #expect(TagColor.hue(tag) == hue)
    }

    @Test func sameHueWhateverCaseOrSpaces() {
        #expect(TagColor.hue("Bug") == TagColor.hue("bug"))
        #expect(TagColor.hue("  bug ") == TagColor.hue("bug"))
        let hues = ["bug", "refactor", "ios", "sidebar", "release", "docs", "perf", "ui", "security", "tests"].map(TagColor.hue)
        #expect(hues.allSatisfy { (0..<360).contains($0) })
        #expect(Set(hues).count > 7)
    }

    @Test func baseColourIsHsl75And50() {
        let red = TagColor.base(hue: 0)
        #expect(abs(red.r - 0.875) < 0.001 && abs(red.g - 0.125) < 0.001 && abs(red.b - 0.125) < 0.001)
        let cyan = TagColor.base(hue: 180)
        #expect(abs(cyan.r - 0.125) < 0.001 && abs(cyan.g - 0.875) < 0.001 && abs(cyan.b - 0.875) < 0.001)
    }

    @Test func fitKeepsAddingWhileTheyFit() {
        let a = TagColor.fit(["bug", "ios", "release-train"], budget: 12)
        #expect(a.shown == ["bug", "ios"] && a.hidden == ["release-train"])
        let b = TagColor.fit(["a-very-long-tag-name-indeed", "x"], budget: 10)
        #expect(b.shown == ["a-very-long-tag-name-indeed"] && b.hidden == ["x"])
        let c = TagColor.fit(["a", "b", "c", "d", "e"], budget: 100, maxShown: 3)
        #expect(c.shown == ["a", "b", "c"] && c.hidden == ["d", "e"])
        let d = TagColor.fit([], budget: 10)
        #expect(d.shown.isEmpty && d.hidden.isEmpty)
    }
}

// MARK: - Worker sessions

extension SessionGroupsTests {
    private func w(_ id: String, parent: String, _ status: String, _ updated: String, mode: String = "build",
                   created: String? = nil, verdict: String? = nil, waiting: String = "",
                   subagents: Int = 0) -> AgentSession {
        var config: [String: Any] = ["task": "Do it", "mode": mode]
        if let verdict { config["last_verdict"] = verdict }
        var options: [String: Any] = ["spawned_by_session_id": parent, "worker": config]
        if !waiting.isEmpty { options["agent_name"] = waiting }
        return AgentSession.parse([
            "id": id, "status": status, "updated_at": updated, "created_at": created ?? updated,
            "model_name": "claude-sonnet-5-5", "subagents_running": subagents, "options": options,
        ])!
    }

    private func row(_ groups: [SessionGroup], _ id: String) -> SessionGroupRow? {
        groups.flatMap(\.rows).first { $0.session.id == id }
    }

    @Test func workersSitUnderTheirParentInItsGroup() {
        let groups = group([
            s("lead", "idle", at(13)),
            w("review", parent: "lead", "idle", at(12), mode: "review", created: at(11)),
            w("build", parent: "lead", "idle", at(14), created: at(10), verdict: "approve"),
            s("other", "idle", at(9)),
        ])
        // Only the parent is a row; its workers hang under it, oldest first,
        // whatever their own last activity.
        #expect(ids(groups, .today) == ["lead", "other"])
        #expect(ids(groups, .working).isEmpty)
        #expect(row(groups, "lead")?.workers.map(\.id) == ["build", "review"])
        #expect(row(groups, "other")?.workers.isEmpty == true)
        #expect(row(groups, "lead")?.workers.map(\.line)
                == ["Build · sonnet-5-5 · Approved", "Review · sonnet-5-5 · Idle"])
    }

    @Test func aWorkingWorkerKeepsItsParentInWorking() {
        let groups = group([
            s("lead", "idle", at(10, day: -1)),
            w("build", parent: "lead", "processing", at(14)),
            s("lead2", "idle", at(9)),
            w("sub", parent: "lead2", "idle", at(9), subagents: 1),
            s("lead3", "idle", at(8)),
            w("done", parent: "lead3", "ended", at(8), subagents: 3),
        ])
        #expect(ids(groups, .working) == ["lead2", "lead"])
        #expect(ids(groups, .today) == ["lead3"])
        #expect(row(groups, "lead")?.full == true)
        #expect(row(groups, "lead")?.workers.first?.line == "Build · sonnet-5-5 · Working")
        #expect(row(groups, "lead")?.workers.first?.isWorking == true)
        #expect(row(groups, "done") == nil)
        #expect(row(groups, "lead3")?.workers.first?.line == "Build · sonnet-5-5 · Ended")
    }

    @Test func aWorkerWithoutItsParentIsAnOrdinaryRow() {
        // The parent is filtered out (another project, a search) or deleted.
        let groups = group([
            w("orphan", parent: "gone", "processing", at(14)),
            w("idle-orphan", parent: "gone", "idle", at(12)),
        ])
        #expect(ids(groups, .working) == ["orphan"])
        #expect(ids(groups, .today) == ["idle-orphan"])
        #expect(groups.flatMap(\.rows).allSatisfy { $0.workers.isEmpty })
    }

    @Test func aWorkerThatNeedsYouIsItsOwnRow() {
        let groups = group([
            s("lead", "idle", at(13)),
            w("ask", parent: "lead", "processing", at(14), waiting: "Allow git push?"),
            w("quiet", parent: "lead", "idle", at(12)),
        ])
        #expect(ids(groups, .needs) == ["ask"])
        #expect(groups[0].rows[0].waitingFor == "Allow git push?")
        // It no longer hangs under the parent, so it does not make it Working.
        #expect(ids(groups, .today) == ["lead"])
        #expect(row(groups, "lead")?.workers.map(\.id) == ["quiet"])
    }

    @Test func nestingIsOneLevelAndIgnoresPlainChildSessions() {
        let plainChild = AgentSession.parse(["id": "loop-child", "status": "idle", "updated_at": at(12),
                                             "options": ["spawned_by_session_id": "lead"]])!
        let groups = group([
            s("lead", "idle", at(13)),
            plainChild,
            w("build", parent: "lead", "idle", at(12)),
            w("grandchild", parent: "build", "idle", at(11)),
            w("self", parent: "self", "idle", at(10)),
        ])
        // A loop session the parent started is not a worker; a worker's own
        // worker and a session naming itself stay ordinary rows.
        #expect(ids(groups, .today) == ["lead", "loop-child", "grandchild", "self"])
        #expect(row(groups, "lead")?.workers.map(\.id) == ["build"])
    }

    @Test func visibleOrderListsWorkersAfterTheirParent() {
        let groups = group([
            s("lead", "processing", at(14)),
            w("build", parent: "lead", "idle", at(12)),
            s("t1", "idle", at(8)),
        ])
        #expect(SessionGroups.visibleOrder(groups, collapsed: [:]).map(\.id) == ["lead", "build", "t1"])
    }
}
