//
//  ZenPagerTests.swift
//  eggifyTests
//
//  The Zen pager: which sessions it pages through and in what order, the
//  page index rules (ends, list changes while viewing), and the
//  subscription it holds for the page on screen.
//

import Foundation
import Testing
@testable import eggify

@Suite("Zen pager")
@MainActor
struct ZenPagerTests {
    private let now = Date(timeIntervalSince1970: 1_800_000_000)

    private func session(_ id: String, status: String = "idle", minutesAgo: Double = 1,
                         project: String? = nil, pendingPermissions: Int = 0) -> AgentSession {
        let updated = EggifyDate.string(from: now.addingTimeInterval(-minutesAgo * 60))
        return AgentSession(id: id, status: status, created_at: updated, updated_at: updated,
                            project_id: project, pending_permissions: pendingPermissions)
    }

    private func ids(_ sessions: [AgentSession], statuses: [String: String] = [:],
                     waiting: [String: Int] = [:], filter: String = "") -> [String] {
        ZenActiveSessions.ids(sessions: sessions, statuses: statuses, localWaiting: waiting,
                              projectFilter: filter, now: now)
    }

    // MARK: Ordering and filtering

    @Test func needsYouThenWorkingThenRecent() {
        let sessions = [
            session("recent", minutesAgo: 2),
            session("working", status: "processing", minutesAgo: 3),
            session("asks", minutesAgo: 4, pendingPermissions: 1),
            session("working2", status: "thinking", minutesAgo: 5),
        ]
        #expect(ids(sessions) == ["asks", "working", "working2", "recent"])
    }

    @Test func skipsEndedAndOldIdleSessions() {
        let sessions = [
            session("ended", status: "ended", minutesAgo: 1),
            session("endedButAsking", status: "ended", minutesAgo: 1, pendingPermissions: 2),
            session("old", minutesAgo: 31),
            session("fresh", status: "error", minutesAgo: 29),
            session("oldButWorking", status: "processing", minutesAgo: 600),
        ]
        #expect(ids(sessions) == ["oldButWorking", "fresh"])
    }

    @Test func liveStatusAndLocalRequestsWin() {
        let sessions = [session("a", minutesAgo: 90), session("b", status: "processing", minutesAgo: 90)]
        // The store says a is working and b has a question waiting on this device.
        #expect(ids(sessions, statuses: ["a": "processing", "b": "idle"], waiting: ["b": 1]) == ["b", "a"])
    }

    @Test func idleSessionWithRunningSubagentsIsWorking() {
        // As on the web's agent overview: the parent's turn ended, its subagent still runs.
        var parent = session("parent", minutesAgo: 90)
        parent.subagents_running = 1
        #expect(ids([session("recent", minutesAgo: 2), parent]) == ["parent", "recent"])
        #expect(ZenActiveSessions.label(parent, statuses: [:], localWaiting: [:]) == "Working")
        #expect(SessionStatus.idle.including(subagentsRunning: 1) == .processing)
        #expect(SessionStatus.error.including(subagentsRunning: 1) == .error)
        #expect(SessionStatus.ended.including(subagentsRunning: 1) == .ended)
    }

    @Test func projectFilterButNeedsYouFromEveryProject() {
        let sessions = [
            session("p1-working", status: "processing", project: "p1"),
            session("p2-working", status: "processing", project: "p2"),
            session("p2-asks", project: "p2", pendingPermissions: 1),
            session("p1-recent", project: "p1"),
        ]
        #expect(ids(sessions, filter: "p1") == ["p2-asks", "p1-working", "p1-recent"])
        // A remembered project without sessions shows everything.
        #expect(ids(sessions, filter: "gone").count == 4)
    }

    @Test func indicatorLabels() {
        let s = session("a")
        #expect(ZenActiveSessions.label(s, statuses: [:], localWaiting: ["a": 1]) == "Needs you")
        #expect(ZenActiveSessions.label(s, statuses: ["a": "processing"], localWaiting: [:]) == "Working")
        #expect(ZenActiveSessions.label(s, statuses: [:], localWaiting: [:]) == "Idle")
        #expect(ZenActiveSessions.label(nil, statuses: [:], localWaiting: [:]) == "Session")
    }

    // MARK: Pager index

    @Test func startsOnTheOpenedSession() {
        let state = ZenPagerState(currentID: "b", active: ["a", "b", "c"])
        #expect(state.index == 1)
        #expect(state.count == 3)
    }

    @Test func openedSessionThatIsNotActiveComesFirst() {
        let state = ZenPagerState(currentID: "old", active: ["a", "b"])
        #expect(state.ids == ["old", "a", "b"])
        #expect(state.index == 0)
    }

    @Test func movesWithoutWrapping() {
        var state = ZenPagerState(currentID: "a", active: ["a", "b", "c"])
        #expect(state.neighbour(-1) == nil)
        do { let ok = state.move(-1); #expect(!ok) }
        #expect(state.currentID == "a")
        do { let ok = state.move(1); #expect(ok) }
        do { let ok = state.move(1); #expect(ok) }
        #expect(state.currentID == "c")
        #expect(state.neighbour(1) == nil)
        do { let ok = state.move(1); #expect(!ok) }
        #expect(state.currentID == "c")
        do { let ok = state.move(-1); #expect(ok) }
        #expect(state.currentID == "b")
    }

    @Test func onlyNeighboursAreNear() {
        let state = ZenPagerState(currentID: "c", active: ["a", "b", "c", "d", "e"])
        #expect(["b", "c", "d"].allSatisfy { state.isNear($0) })
        #expect(!state.isNear("a"))
        #expect(!state.isNear("e"))
        #expect(!state.isNear("unknown"))
    }

    @Test func selectIgnoresUnknownAndCurrent() {
        var state = ZenPagerState(currentID: "a", active: ["a", "b"])
        do { let ok = state.select("zzz"); #expect(!ok) }
        do { let ok = state.select("a"); #expect(!ok) }
        do { let ok = state.select("b"); #expect(ok) }
    }

    @Test func currentSessionFinishingStaysPut() {
        var state = ZenPagerState(currentID: "b", active: ["a", "b", "c"])
        // b finished and fell out of the active list (e.g. ended).
        state.update(active: ["a", "c"])
        #expect(state.currentID == "b")
        #expect(state.ids == ["a", "b", "c"])
        #expect(state.index == 1)
    }

    @Test func otherSessionsLeavingKeepTheCurrentPage() {
        var state = ZenPagerState(currentID: "c", active: ["a", "b", "c", "d"])
        state.update(active: ["c", "d"])
        #expect(state.currentID == "c")
        #expect(state.ids == ["c", "d"])
        #expect(state.neighbour(-1) == nil)
    }

    @Test func groupChangesDontReorderOpenPages() {
        // b moved from Working to Recent: the fresh order puts it after c,
        // but the pages the user is swiping through keep their order.
        var state = ZenPagerState(currentID: "b", active: ["a", "b", "c"])
        state.update(active: ["a", "c", "b"])
        #expect(state.ids == ["a", "b", "c"])
        #expect(state.currentID == "b")
    }

    @Test func newSessionsGoInAfterTheirPredecessor() {
        var state = ZenPagerState(currentID: "b", active: ["a", "b", "c"])
        // A new session needs you (top of the fresh order) and one starts working after b.
        state.update(active: ["new-asks", "a", "b", "new-working", "c"])
        #expect(state.ids == ["new-asks", "a", "b", "new-working", "c"])
        #expect(state.currentID == "b")
        #expect(state.index == 2)
        #expect(state.neighbour(1) == "new-working")
    }

    @Test func emptyActiveListKeepsTheCurrentPage() {
        var state = ZenPagerState(currentID: "a", active: ["a", "b"])
        state.update(active: [])
        #expect(state.ids == ["a"])
        #expect(state.count == 1)
    }

    // MARK: Subscriptions

    private final class FakeSubscriber: ZenSessionSubscriber {
        var calls: [String] = []
        func zenSubscribe(_ sessionID: String) { calls.append("+\(sessionID)") }
        func zenUnsubscribe(_ sessionID: String) { calls.append("-\(sessionID)") }
    }

    @Test func holdsOnlyThePageOnScreen() {
        let fake = FakeSubscriber()
        let subs = ZenSubscriptions(subscriber: fake)
        subs.show("a")
        subs.show("b")
        subs.show("c")
        #expect(fake.calls == ["+a", "+b", "-a", "+c", "-b"])
        #expect(subs.held == "c")
    }

    @Test func showingTheSamePageAgainDoesNothing() {
        let fake = FakeSubscriber()
        let subs = ZenSubscriptions(subscriber: fake)
        subs.show("a")
        subs.show("a")
        #expect(fake.calls == ["+a"])
    }

    @Test func newPageIsSubscribedBeforeTheOldOneIsReleased() {
        // The reference count never touches zero in between for a session
        // the chat underneath also holds.
        let fake = FakeSubscriber()
        let subs = ZenSubscriptions(subscriber: fake)
        subs.show("origin")
        subs.show("other")
        #expect(fake.calls.firstIndex(of: "+other")! < fake.calls.firstIndex(of: "-origin")!)
    }

    @Test func endReleasesOnce() {
        let fake = FakeSubscriber()
        let subs = ZenSubscriptions(subscriber: fake)
        subs.end()
        #expect(fake.calls.isEmpty)
        subs.show("a")
        subs.end()
        subs.end()
        #expect(fake.calls == ["+a", "-a"])
        #expect(subs.held == nil)
    }

    @Test func storeKeepsPendingApprovalsOfASessionLeftBehind() {
        // Leaving a page only stops re-subscribing; the approval card the
        // chat underneath shows is still there.
        let store = SessionsWebSocketViewModel.shared
        store.handleServerEvent(type: "permission_request", payload: [
            "type": "permission_request", "session_id": "zen-left", "permission_id": "perm-1",
            "tool": "Bash", "action": "use_tool", "details": ["command": "ls"], "description": "List"
        ])
        store.zenUnsubscribe("zen-left")
        #expect(store.pendingPermissions["zen-left"]?.map(\.id) == ["perm-1"])
        store.handleServerEvent(type: "permission_acknowledged", payload: [
            "type": "permission_acknowledged", "session_id": "zen-left", "permission_id": "perm-1"
        ])
    }
}
