//
//  LoopTests.swift
//  eggifyTests
//
//  Loop-mode sessions: loop state from the session list and from live
//  `loop_*` events, and the start_loop_session proposal card.
//

import Foundation
import Testing
@testable import eggify

@Suite("Loops")
@MainActor
struct LoopTests {
    private func event(_ type: String, iteration: Int, passed: Bool? = nil, output: String = "") -> [String: Any] {
        var e: [String: Any] = ["type": type, "iteration": iteration, "max_iterations": 20,
                                "goal": "Make the tests pass", "verify_command": "go test ./..."]
        if let passed { e["verify_passed"] = passed }
        if !output.isEmpty { e["verify_output"] = output }
        return e
    }

    @Test func parsesEventsAndLabels() throws {
        let started = try #require(LoopState.parse(event("loop_started", iteration: 0)))
        #expect(started.phase == .started)
        #expect(started.isRunning)
        #expect(started.badge == "Loop 1/20")

        let retry = try #require(LoopState.parse(event("loop_iteration", iteration: 2, passed: false, output: "FAIL")))
        #expect(retry.current == 3)
        #expect(retry.headline == "Check failed · iteration 3 of 20")

        let done = try #require(LoopState.parse(event("loop_completed", iteration: 3, passed: true)))
        #expect(!done.isRunning)
        #expect(done.badge == "Loop done")
        #expect(done.headline == "Loop done after 3 iterations")

        #expect(LoopState.parse(["type": "agent_message"]) == nil)
    }

    @Test func nextEventKeepsTheLastCheck() throws {
        let failed = try #require(LoopState.parse(event("loop_iteration", iteration: 1, passed: false, output: "FAIL x")))
        let verifying = try #require(LoopState.parse(["type": "loop_verifying", "iteration": 2, "max_iterations": 20]))
        let merged = failed.followed(by: verifying)
        #expect(merged.phase == .verifying)
        #expect(merged.verifyOutput == "FAIL x")
        #expect(merged.verifyPassed == false)
        #expect(merged.goal == "Make the tests pass")
    }

    @Test func sessionListCarriesLoopState() throws {
        let session = try #require(AgentSession.parse([
            "id": "s1", "status": "processing",
            "options": ["mode": "loop"],
            "loop_state": event("loop_verifying", iteration: 4),
        ]))
        #expect(session.loop_mode)
        #expect(session.loop?.phase == .verifying)

        // A live update without loop_state keeps it.
        let updated = session.merged(with: ["status": "idle"])
        #expect(updated.loop?.phase == .verifying)
        #expect(updated.loop_mode)
    }

    @Test func liveEventsUpdateTheSession() throws {
        let store = SessionsWebSocketViewModel.shared
        let id = "test-\(UUID().uuidString)"
        store.handleServerEvent(type: "session_created", payload: ["session": ["id": id, "status": "idle"]])

        store.handleServerEvent(type: "loop_started", payload: event("loop_started", iteration: 0).merging(["session_id": id]) { a, _ in a })
        #expect(store.session(id)?.loop_mode == true)
        #expect(store.session(id)?.loop?.phase == .started)

        store.handleServerEvent(type: "loop_iteration",
                                payload: event("loop_iteration", iteration: 1, passed: false, output: "FAIL").merging(["session_id": id]) { a, _ in a })
        let loop = try #require(store.session(id)?.loop)
        #expect(loop.phase == .iteration)
        #expect(loop.verifyOutput == "FAIL")

        store.handleServerEvent(type: "loop_stopped", payload: ["session_id": id, "iteration": 2, "max_iterations": 20, "reason": "stopped by user"])
        let stopped = try #require(store.session(id)?.loop)
        #expect(stopped.phase == .stopped)
        #expect(stopped.detail == "stopped by user")
        #expect(stopped.verifyCommand == "go test ./...")
    }

    @Test func loopProposalCard() throws {
        let q = try #require(UserQuestion.parse([
            "session_id": "s1", "question_id": "q1", "question": "Start a loop session?",
            "options": [["label": "Start"], ["label": "Edit first"], ["label": "Deny"]],
            "requires_user": true, "kind": "loop_session",
            "proposal": ["goal": "Ship it", "verify_command": "make test", "working_directory": "/Users/me/app",
                         "max_iterations": 20, "timeout_minutes": 60, "model": "opus", "provider": "claude",
                         "yolo": true, "sandboxed": false],
        ]))
        let p = try #require(q.loopProposal)
        #expect(p.verifyCommand == "make test")
        #expect(p.maxIterations == 20)
        #expect(p.yolo)
        // The answers the server expects.
        #expect(q.options.map(\.label).contains(UserQuestion.LoopProposal.start))
        #expect(q.options.map(\.label).contains(UserQuestion.LoopProposal.deny))

        // A plain question (or an agent posing as a card without kind) is not one.
        let plain = try #require(UserQuestion.parse(["session_id": "s1", "question_id": "q2", "question": "?",
                                                     "proposal": ["goal": "x"]]))
        #expect(plain.loopProposal == nil)
    }
}
