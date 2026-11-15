//
//  WorkerSession.swift
//  eggify
//
//  Worker sessions: a session another agent started to build or review
//  (`options.worker`, with `options.spawned_by_session_id` naming who started
//  it), and the team setup of an orchestrator (`options.team`). Where a
//  parent and its workers are is worked out in `TeamProgress.swift`.
//

import Foundation

/// `options.worker`: this session is a worker of `spawned_by_session_id`.
nonisolated struct WorkerInfo: Equatable, Sendable {
    enum Mode: String, Sendable {
        case build, review

        /// "Build" / "Review", for rows and labels.
        var label: String { self == .build ? "Build" : "Review" }
        var symbol: String { self == .build ? "hammer.fill" : "checklist" }
    }

    /// A review's verdict on a build worker (`last_verdict`).
    enum Verdict: String, Sendable {
        case approve
        case changesRequested = "changes_requested"

        var label: String { self == .approve ? "Approved" : "Changes requested" }
    }

    var task: String
    var mode: Mode
    /// Build workers in a git repository: their own worktree and branch.
    var worktreePath: String
    var branch: String
    var baseBranch: String
    /// Review workers: the build worker under review.
    var reviewsSessionID: String?
    /// Build workers: reviews so far, the cap (0 = not set) and the latest verdict.
    var reviewRounds: Int
    var maxReviewRounds: Int
    var lastVerdict: Verdict?

    static func parse(_ data: [String: Any]) -> WorkerInfo {
        WorkerInfo(
            task: data.string("task") ?? "",
            // The server knows build and review; anything else is treated as build.
            mode: Mode(rawValue: data.string("mode") ?? "") ?? .build,
            worktreePath: data.string("worktree_path") ?? "",
            branch: data.string("branch") ?? "",
            baseBranch: data.string("base_branch") ?? "",
            reviewsSessionID: data.nonEmptyString("reviews_session_id"),
            reviewRounds: max(0, data.int("review_rounds") ?? 0),
            maxReviewRounds: max(0, data.int("max_review_rounds") ?? 0),
            lastVerdict: data.string("last_verdict").flatMap(Verdict.init(rawValue:))
        )
    }
}

/// `options.team`: this session orchestrates a builder and a reviewer.
nonisolated struct TeamInfo: Equatable, Sendable {
    nonisolated struct Member: Equatable, Sendable {
        var provider: String
        var model: String

        static func parse(_ data: [String: Any]?) -> Member {
            Member(provider: data?.string("provider") ?? "", model: data?.string("model") ?? "")
        }
    }

    var builder: Member
    var reviewer: Member
    /// 0 when the server did not say (it then uses its default).
    var maxReviewRounds: Int
    var briefSent: Bool

    static func parse(_ data: [String: Any]) -> TeamInfo {
        TeamInfo(
            builder: .parse(data.dict("builder")),
            reviewer: .parse(data.dict("reviewer")),
            maxReviewRounds: max(0, data.int("max_review_rounds") ?? 0),
            briefSent: data.bool("brief_sent") ?? false
        )
    }
}

extension AgentSession {
    /// A worker another session started (the parent may be gone from the list).
    var isWorker: Bool { worker != nil }

    /// "Build worker of Shop · worker/abc" / "Review worker of Shop · read-only",
    /// for the line under a worker chat's header; nil for other sessions.
    func workerLine(parentName: String?) -> String? {
        guard let worker else { return nil }
        var text = "\(worker.mode.label) worker"
        if let parentName, !parentName.isEmpty { text += " of \(parentName)" }
        switch worker.mode {
        case .build:
            if !worker.branch.isEmpty { text += " · \(worker.branch)" }
        case .review:
            if read_only { text += " · read-only" }
        }
        return text
    }
}

extension UserQuestion {
    /// What `start_worker_session` asks the user to approve
    /// (`kind: "worker_session"`).
    nonisolated struct WorkerProposal: Equatable, Sendable {
        /// The card's answers, as the server expects them (`WorkerSessionAnswer*`).
        static let start = "Start"
        static let deny = "Deny"

        let task: String
        let mode: WorkerInfo.Mode
        let workingDirectory: String
        let model: String
        let provider: String
        let runtime: String
        let context: String
        let sandboxed: Bool
        let readOnly: Bool
        let parentSessionID: String
        let worktreePath: String
        let branch: String
        let baseBranch: String
        let reviewsSessionID: String
        let reviewRound: Int
        let maxReviewRounds: Int

        var title: String { "Start a \(mode.rawValue) worker?" }

        /// "Own worktree · worker/abc from main", "" without a worktree.
        var worktreeBadge: String {
            guard !worktreePath.isEmpty || !branch.isEmpty else { return "" }
            var text = "Own worktree"
            if !branch.isEmpty { text += " · \(branch)" }
            if !branch.isEmpty, !baseBranch.isEmpty { text += " from \(baseBranch)" }
            return text
        }

        /// "Review round 2 of 3", "" when the review is not tied to a build worker.
        var roundBadge: String {
            guard reviewRound > 0 else { return "" }
            return maxReviewRounds > 0 ? "Review round \(reviewRound) of \(maxReviewRounds)" : "Review round \(reviewRound)"
        }

        static func parse(_ data: [String: Any]) -> WorkerProposal? {
            guard let task = data.nonEmptyString("task") else { return nil }
            let mode = WorkerInfo.Mode(rawValue: data.string("mode") ?? "") ?? .build
            return WorkerProposal(
                task: task,
                mode: mode,
                workingDirectory: data.string("working_directory") ?? "",
                model: data.string("model") ?? "",
                provider: data.string("provider") ?? "",
                runtime: data.string("runtime") ?? "",
                context: data.string("context") ?? "",
                sandboxed: data.bool("sandboxed") ?? false,
                readOnly: data.bool("read_only") ?? (mode == .review),
                parentSessionID: data.string("parent_session_id") ?? "",
                worktreePath: data.string("worktree_path") ?? "",
                branch: data.string("branch") ?? "",
                baseBranch: data.string("base_branch") ?? "",
                reviewsSessionID: data.string("reviews_session_id") ?? "",
                reviewRound: max(0, data.int("review_round") ?? 0),
                maxReviewRounds: max(0, data.int("max_review_rounds") ?? 0)
            )
        }
    }
}
