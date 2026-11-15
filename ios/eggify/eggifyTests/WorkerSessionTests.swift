//
//  WorkerSessionTests.swift
//  eggifyTests
//
//  Worker sessions and teams: `options.worker` / `options.team` from the
//  session list, the start_worker_session proposal card, and where a parent
//  and its workers are (`TeamProgress`).
//

import Foundation
import Testing
@testable import eggify

@Suite("Worker sessions")
@MainActor
struct WorkerSessionTests {
    private let parentID = "5d0c2f0e-6c1b-4b7e-9a55-0f6a3c1d2e10"
    private let builderID = "a81f3c52-2a0e-4f55-8a0b-7d1e9c4b6f21"
    private let reviewerID = "c3b7e9a4-91d2-4c08-b6f3-2e5a8d7c1b32"

    /// One entry of `GET /api/agent/sessions?status=all`, as the server sends it.
    private func decode(_ json: String) throws -> AgentSession {
        let object = try JSONSerialization.jsonObject(with: Data(json.utf8))
        let data = try #require(object as? [String: Any])
        return try #require(AgentSession.parse(data))
    }

    private func worker(_ id: String, mode: String, status: String = "idle", created: String,
                        model: String = "deepseek-flash", parent: String? = nil, reviews: String? = nil,
                        rounds: Int = 0, maxRounds: Int = 3, verdict: String? = nil,
                        branch: String = "") -> AgentSession {
        var config: [String: Any] = ["task": "Do it", "mode": mode]
        if let reviews { config["reviews_session_id"] = reviews }
        if rounds > 0 { config["review_rounds"] = rounds }
        if mode == "build" { config["max_review_rounds"] = maxRounds }
        if let verdict { config["last_verdict"] = verdict }
        if !branch.isEmpty { config["branch"] = branch; config["base_branch"] = "main" }
        return AgentSession.parse([
            "id": id, "status": status, "created_at": created, "updated_at": created, "model_name": model,
            "options": ["spawned_by_session_id": parent ?? parentID, "worker": config,
                        "read_only": mode == "review", "sandboxed": true],
        ])!
    }

    private func parent(team: Bool = true, status: String = "idle") -> AgentSession {
        var options: [String: Any] = ["agent_name": "Shop"]
        if team {
            options["team"] = ["builder": ["provider": "deepseek", "model": "deepseek-flash"],
                               "reviewer": ["provider": "codex", "model": "gpt-6-astra"],
                               "max_review_rounds": 3, "brief_sent": true]
        }
        return AgentSession.parse(["id": parentID, "status": status, "created_at": "2026-11-14T09:00:00Z",
                                   "updated_at": "2026-11-14T09:00:00Z", "options": options])!
    }

    private func at(_ minute: Int) -> String { String(format: "2026-11-14T10:%02d:00Z", minute) }

    // MARK: Decoding

    @Test func decodesABuildWorkerFromTheSessionList() throws {
        let session = try decode("""
        {
          "id": "\(builderID)", "status": "processing", "model_name": "deepseek-flash", "provider": "deepseek",
          "working_directory": "/Users/me/shop/.eggify/worktrees/worker-a81f3c52",
          "created_at": "2026-11-14T10:00:00Z", "updated_at": "2026-11-14T10:04:00Z", "message_count": 12,
          "options": {
            "agent_name": "Build: cart totals", "sandboxed": true, "provider": "deepseek", "model": "deepseek-flash",
            "spawned_by_session_id": "\(parentID)", "parent_session_id": "\(parentID)",
            "worker": {
              "task": "Fix the cart totals", "mode": "build",
              "worktree_path": "/Users/me/shop/.eggify/worktrees/worker-a81f3c52",
              "branch": "worker/a81f3c52", "base_branch": "main",
              "review_rounds": 1, "max_review_rounds": 3, "last_verdict": "changes_requested"
            }
          }
        }
        """)
        #expect(session.spawned_by_session_id == parentID)
        #expect(session.isWorker)
        #expect(!session.read_only)
        #expect(session.team == nil)
        let worker = try #require(session.worker)
        #expect(worker.mode == .build)
        #expect(worker.task == "Fix the cart totals")
        #expect(worker.branch == "worker/a81f3c52")
        #expect(worker.baseBranch == "main")
        #expect(worker.reviewRounds == 1)
        #expect(worker.maxReviewRounds == 3)
        #expect(worker.lastVerdict == .changesRequested)
        #expect(worker.reviewsSessionID == nil)
        #expect(session.workerLine(parentName: "Shop") == "Build worker of Shop · worker/a81f3c52")
        #expect(session.workerLine(parentName: nil) == "Build worker · worker/a81f3c52")
    }

    @Test func decodesAReviewWorkerAndATeam() throws {
        let review = try decode("""
        {
          "id": "\(reviewerID)", "status": "idle", "model_name": "gpt-6-astra", "provider": "codex",
          "created_at": "2026-11-14T10:05:00Z", "updated_at": "2026-11-14T10:09:00Z",
          "options": {
            "sandboxed": true, "read_only": true, "spawned_by_session_id": "\(parentID)",
            "worker": {"task": "Review the cart fix", "mode": "review", "reviews_session_id": "\(builderID)"}
          }
        }
        """)
        #expect(review.read_only)
        #expect(review.worker?.mode == .review)
        #expect(review.worker?.reviewsSessionID == builderID)
        #expect(review.worker?.lastVerdict == nil)
        #expect(review.worker?.maxReviewRounds == 0)
        #expect(review.workerLine(parentName: "Shop") == "Review worker of Shop · read-only")

        let team = try decode("""
        {
          "id": "\(parentID)", "status": "idle", "created_at": "2026-11-14T09:00:00Z", "updated_at": "2026-11-14T09:00:00Z",
          "options": {
            "agent_name": "Shop",
            "team": {
              "builder": {"provider": "deepseek", "model": "deepseek-flash"},
              "reviewer": {"provider": "codex", "model": "gpt-6-astra"},
              "max_review_rounds": 4, "brief_sent": true
            }
          }
        }
        """)
        #expect(team.worker == nil)
        #expect(team.spawned_by_session_id == nil)
        #expect(team.workerLine(parentName: "x") == nil)
        #expect(team.team?.builder == TeamInfo.Member(provider: "deepseek", model: "deepseek-flash"))
        #expect(team.team?.reviewer.model == "gpt-6-astra")
        #expect(team.team?.maxReviewRounds == 4)
        #expect(team.team?.briefSent == true)
    }

    @Test func toleratesMissingAndUnknownFields() throws {
        // An ordinary session, and one from an older server without options.
        let plain = try decode(#"{"id": "p", "status": "idle", "options": {"agent_name": "x"}}"#)
        #expect(plain.worker == nil && plain.team == nil && !plain.read_only && plain.spawned_by_session_id == nil)
        #expect(try decode(#"{"id": "p", "status": "idle"}"#).worker == nil)

        // Empty objects, wrong types, a verdict this app does not know.
        let odd = try decode("""
        {"id": "o", "status": "idle", "options": {
          "spawned_by_session_id": "", "read_only": null,
          "worker": {"mode": "audit", "review_rounds": "two", "last_verdict": "maybe"},
          "team": {"builder": {"model": "opus"}}
        }}
        """)
        #expect(odd.spawned_by_session_id == nil)
        #expect(odd.worker?.mode == .build)
        #expect(odd.worker?.task == "")
        #expect(odd.worker?.reviewRounds == 0)
        #expect(odd.worker?.lastVerdict == nil)
        #expect(odd.team?.builder.model == "opus")
        #expect(odd.team?.reviewer == TeamInfo.Member(provider: "", model: ""))
        #expect(odd.team?.maxReviewRounds == 0)
    }

    @Test func liveUpdatesKeepOrReplaceTheWorkerState() {
        let build = worker(builderID, mode: "build", created: at(0), rounds: 1, verdict: "changes_requested",
                           branch: "worker/a81f3c52")
        // A partial session_updated (status only) keeps it.
        let kept = build.merged(with: ["status": "processing"])
        #expect(kept.worker == build.worker)
        #expect(kept.spawned_by_session_id == parentID)
        // One that carries options replaces it.
        let next = build.merged(with: ["options": [
            "spawned_by_session_id": parentID,
            "worker": ["task": "Do it", "mode": "build", "review_rounds": 2, "max_review_rounds": 3,
                       "last_verdict": "approve"],
        ]])
        #expect(next.worker?.reviewRounds == 2)
        #expect(next.worker?.lastVerdict == .approve)
    }

    // MARK: Proposal card

    @Test func workerProposalCard() throws {
        let q = try #require(UserQuestion.parse([
            "session_id": parentID, "question_id": "q1", "question": "Start a build worker?",
            "options": [["label": "Start", "description": "Starts it"], ["label": "Deny", "description": "No"]],
            "requires_user": true, "kind": "worker_session",
            "proposal": ["task": "Fix the cart totals", "mode": "build", "working_directory": "/Users/me/shop",
                         "model": "deepseek-flash", "provider": "deepseek", "runtime": "claude-sdk",
                         "context": "Tests live in cart_test.go", "sandboxed": true, "read_only": false,
                         "parent_session_id": parentID,
                         "worktree_path": "/Users/me/shop/.eggify/worktrees/worker-a81f3c52",
                         "branch": "worker/a81f3c52", "base_branch": "main"],
        ]))
        let p = try #require(q.workerProposal)
        #expect(q.loopProposal == nil)
        #expect(q.requiresUser)
        #expect(p.mode == .build)
        #expect(p.title == "Start a build worker?")
        #expect(p.sandboxed && !p.readOnly)
        #expect(p.worktreeBadge == "Own worktree · worker/a81f3c52 from main")
        #expect(p.roundBadge == "")
        #expect(p.context == "Tests live in cart_test.go")
        // The answers the server expects.
        #expect(q.options.map(\.label) == [UserQuestion.WorkerProposal.start, UserQuestion.WorkerProposal.deny])
        #expect(SessionGroups.waitingLabel(permissions: nil, questions: [q]) == "Worker proposal waiting")

        let review = try #require(UserQuestion.parse([
            "session_id": parentID, "question_id": "q2", "question": "Start a review worker?",
            "options": [["label": "Start"], ["label": "Deny"]], "kind": "worker_session",
            "proposal": ["task": "Review it", "mode": "review", "working_directory": "/Users/me/shop",
                         "model": "gpt-6-astra", "provider": "codex", "runtime": "codex", "sandboxed": true,
                         "read_only": true, "parent_session_id": parentID, "reviews_session_id": builderID,
                         "review_round": 2, "max_review_rounds": 3],
        ])?.workerProposal)
        #expect(review.title == "Start a review worker?")
        #expect(review.readOnly)
        #expect(review.worktreeBadge == "")
        #expect(review.roundBadge == "Review round 2 of 3")
    }

    @Test func unknownCardKindsStayPlainQuestions() throws {
        // A kind this app has no card for, and a proposal without the kind.
        let future = try #require(UserQuestion.parse([
            "session_id": "s", "question_id": "q", "question": "Deploy?", "kind": "deploy_session",
            "options": [["label": "Start"], ["label": "Deny"]], "proposal": ["task": "x", "goal": "y"],
        ]))
        #expect(future.workerProposal == nil && future.loopProposal == nil)
        #expect(future.options.count == 2)
        let plain = try #require(UserQuestion.parse(["session_id": "s", "question_id": "q", "question": "?",
                                                     "proposal": ["task": "x", "mode": "build"]]))
        #expect(plain.workerProposal == nil)
        #expect(SessionGroups.waitingLabel(permissions: nil, questions: [plain]) == "Question waiting")
        // A worker card without a task has nothing to show: plain question too.
        let empty = try #require(UserQuestion.parse(["session_id": "s", "question_id": "q", "question": "?",
                                                     "kind": "worker_session", "proposal": ["mode": "build"]]))
        #expect(empty.workerProposal == nil)
    }

    // MARK: Team progress

    private func progress(_ workers: [AgentSession], team: Bool = true) -> TeamProgress? {
        TeamProgress.compute(parent: parent(team: team), sessions: [parent(team: team)] + workers)
    }

    @Test func noTeamAndNoWorkersIsNoProgress() {
        #expect(progress([], team: false) == nil)
        // Someone else's workers don't count.
        #expect(progress([worker("x", mode: "build", created: at(0), parent: "other")], team: false) == nil)
    }

    @Test func aTeamWithoutWorkersIsPlanning() throws {
        let p = try #require(progress([]))
        #expect(p.stage == .planning)
        #expect(p.builder == nil && p.reviewer == nil && p.workers.isEmpty)
        #expect(p.maxRounds == 3)
        #expect(p.headline == "Planning · no workers yet")
        #expect(!p.isWorking)
    }

    @Test func buildingThenReviewing() throws {
        let building = try #require(progress([worker(builderID, mode: "build", status: "processing", created: at(0))]))
        #expect(building.stage == .building)
        #expect(building.builder?.id == builderID)
        #expect(building.builder?.model == "deepseek-flash")
        #expect(building.headline == "Building · deepseek-flash")
        #expect(building.isWorking)

        let built = try #require(progress([worker(builderID, mode: "build", created: at(0))]))
        #expect(built.stage == .idle)
        #expect(built.headline == "Built · not reviewed yet")

        let reviewing = try #require(progress([
            worker(builderID, mode: "build", created: at(0)),
            worker(reviewerID, mode: "review", status: "processing", created: at(5), model: "gpt-6-astra",
                   reviews: builderID),
        ]))
        #expect(reviewing.stage == .reviewing)
        #expect(reviewing.reviewer?.id == reviewerID)
        #expect(reviewing.round == 1 && reviewing.maxRounds == 3)
        #expect(reviewing.headline == "Review round 1 of 3 · gpt-6-astra")
    }

    @Test func fixingAfterChangesRequestedThenTheSecondRound() throws {
        let reviewer = worker(reviewerID, mode: "review", created: at(5), model: "gpt-6-astra", reviews: builderID)
        let fixing = try #require(progress([
            worker(builderID, mode: "build", status: "processing", created: at(0), rounds: 1,
                   verdict: "changes_requested"),
            reviewer,
        ]))
        #expect(fixing.stage == .fixing)
        #expect(fixing.lastVerdict == .changesRequested)
        #expect(fixing.headline == "Fixing after review 1 of 3 · deepseek-flash")

        // Verdict in, nobody at work yet: the orchestrator's move.
        let waiting = try #require(progress([
            worker(builderID, mode: "build", created: at(0), rounds: 1, verdict: "changes_requested"), reviewer,
        ]))
        #expect(waiting.stage == .idle)
        #expect(waiting.headline == "Changes requested · round 1 of 3")

        var again = reviewer
        again.status = "processing"
        let second = try #require(progress([
            worker(builderID, mode: "build", created: at(0), rounds: 1, verdict: "changes_requested"), again,
        ]))
        #expect(second.stage == .reviewing)
        #expect(second.round == 2)
        #expect(second.headline == "Review round 2 of 3 · gpt-6-astra")
    }

    @Test func approvedAndStopped() throws {
        let reviewer = worker(reviewerID, mode: "review", created: at(5), model: "gpt-6-astra", reviews: builderID)
        let approved = try #require(progress([
            worker(builderID, mode: "build", created: at(0), rounds: 2, verdict: "approve"), reviewer,
        ]))
        #expect(approved.stage == .approved)
        #expect(approved.round == 2)
        #expect(approved.headline == "Approved")
        #expect(approved.builder?.stateLine == "Approved")

        let stopped = try #require(progress([
            worker(builderID, mode: "build", created: at(0), rounds: 3, verdict: "changes_requested"), reviewer,
        ]))
        #expect(stopped.stage == .stopped)
        #expect(stopped.headline == "Stopped after 3 rounds")

        // The build worker's own cap wins over the team's; one round says "round".
        let one = try #require(progress([
            worker(builderID, mode: "build", created: at(0), rounds: 1, maxRounds: 1, verdict: "changes_requested"),
        ]))
        #expect(one.stage == .stopped)
        #expect(one.maxRounds == 1)
        #expect(one.headline == "Stopped after 1 round")
    }

    @Test func theNewestBuilderCountsWithItsOwnReviewers() throws {
        let p = try #require(progress([
            worker("old-build", mode: "build", created: at(0), rounds: 1, verdict: "approve"),
            worker("old-review", mode: "review", status: "processing", created: at(3), reviews: "old-build"),
            worker("new-build", mode: "build", status: "processing", created: at(10), model: "claude-sonnet-5-5"),
            worker("new-review-1", mode: "review", created: at(12), reviews: "new-build"),
            worker("new-review-2", mode: "review", created: at(14), reviews: "new-build"),
        ]))
        #expect(p.builder?.id == "new-build")
        #expect(p.builder?.model == "sonnet-5-5")
        #expect(p.reviewer?.id == "new-review-2")
        // The old builder's reviewer being busy does not make it "reviewing".
        #expect(p.stage == .building)
        #expect(p.lastVerdict == nil)
        #expect(p.workers.map(\.id) == ["old-build", "old-review", "new-build", "new-review-1", "new-review-2"])
        #expect(p.isWorking)
    }

    @Test func aReviewWithoutABuildWorker() throws {
        let p = try #require(progress([
            worker(reviewerID, mode: "review", status: "processing", created: at(1), model: "gpt-6-astra"),
        ], team: false))
        #expect(p.builder == nil)
        #expect(p.reviewer?.id == reviewerID)
        #expect(p.stage == .reviewing)
        #expect(p.headline == "Reviewing · gpt-6-astra")
        #expect(p.maxRounds == TeamProgress.defaultMaxRounds)
    }

    @Test func usesLiveStatusAndCountsWorkingSubagents() throws {
        let build = worker(builderID, mode: "build", created: at(0))
        let live = try #require(TeamProgress.compute(parent: parent(), sessions: [parent(), build],
                                                     status: { $0.id == self.builderID ? .processing : .idle }))
        #expect(live.stage == .building)

        var withSubagents = build
        withSubagents.subagents_running = 2
        #expect(progress([withSubagents])?.stage == .building)

        // An ended worker is never at work.
        var ended = build
        ended.status = "ended"
        ended.subagents_running = 2
        let p = try #require(progress([ended]))
        #expect(p.stage == .idle)
        #expect(p.builder?.stateLine == "Ended")
    }
}
