//
//  TeamProgress.swift
//  eggify
//
//  Where a session and the workers it started are: planning, building, in
//  review, fixing after a review, approved, or stopped after the last round.
//  Worked out on the device from the session list (the parent, and every
//  session whose `options.worker` names it in `spawned_by_session_id`); the
//  server sends no such summary.
//
//  With several build workers the newest one counts, with the reviewers that
//  review it.
//

import Foundation

nonisolated struct TeamProgress: Equatable, Sendable {
    enum Stage: String, Sendable {
        /// A team that has not started a worker yet.
        case planning
        case building
        case reviewing
        /// The builder works again after a changes-requested verdict.
        case fixing
        case approved
        /// The last round ended with changes requested.
        case stopped
        /// Nobody works and nothing above applies (e.g. built, not reviewed yet).
        case idle
    }

    /// One worker, as the strip and the workers sheet show it.
    nonisolated struct Member: Identifiable, Equatable, Sendable {
        let id: String
        let mode: WorkerInfo.Mode
        /// Short model label ("sonnet-5-5"), "" when unknown.
        let model: String
        let status: SessionStatus
        let branch: String
        /// Build workers: the latest verdict on their work.
        let verdict: WorkerInfo.Verdict?
        let title: String

        var isWorking: Bool { status.isActive }

        /// "Build · gpt-6-astra", for rows.
        var roleLine: String { model.isEmpty ? mode.label : "\(mode.label) · \(model)" }

        /// "Working", "Idle", "Approved", "Changes requested", "Ended".
        var stateLine: String {
            if status.isActive || status == .error || status == .ended { return status.label }
            return verdict?.label ?? status.label
        }
    }

    var stage: Stage
    /// The newest build worker, and the newest reviewer of it (without a
    /// build worker: the newest review worker).
    var builder: Member?
    var reviewer: Member?
    /// The review round in progress, else the last finished one (0 = none yet).
    var round: Int
    var maxRounds: Int
    var lastVerdict: WorkerInfo.Verdict?
    /// Every worker of the parent, oldest first.
    var workers: [Member]

    /// The server's default cap (`DefaultMaxReviewRounds`), when neither the
    /// build worker nor the team carries one.
    static let defaultMaxRounds = 3

    /// The workers `parent` started, oldest first.
    static func workers(of parentID: String, in sessions: [AgentSession]) -> [AgentSession] {
        var seen = Set<String>()
        return sessions
            .filter { $0.worker != nil && $0.spawned_by_session_id == parentID && $0.id != parentID
                && seen.insert($0.id).inserted }
            .sorted { a, b in
                let ta = a.createdDate ?? .distantPast, tb = b.createdDate ?? .distantPast
                return ta != tb ? ta < tb : a.id < b.id
            }
    }

    /// Whether a worker keeps its parent in Working: its turn runs, or its
    /// subagents still do.
    static func isWorking(_ worker: AgentSession, status: SessionStatus) -> Bool {
        status != .ended && status.including(subagentsRunning: worker.subagents_running).isActive
    }

    /// nil for a session that is neither a team nor has workers.
    static func compute(
        parent: AgentSession,
        sessions: [AgentSession],
        status: (AgentSession) -> SessionStatus = { SessionStatus($0.status) }
    ) -> TeamProgress? {
        let children = workers(of: parent.id, in: sessions)
        guard parent.team != nil || !children.isEmpty else { return nil }

        func member(_ session: AgentSession) -> Member? {
            guard let worker = session.worker else { return nil }
            let state = status(session)
            return Member(
                id: session.id,
                mode: worker.mode,
                model: session.model_name.map(ModelNames.short) ?? "",
                status: isWorking(session, status: state) ? .processing : state,
                branch: worker.branch,
                verdict: worker.mode == .build ? worker.lastVerdict : nil,
                title: session.displayTitle)
        }

        let members = children.compactMap(member)
        let build = children.last { $0.worker?.mode == .build }
        let reviews = children.filter { $0.worker?.mode == .review }
        // The reviewers of the newest builder; a review not tied to a build
        // worker only counts when there is no builder at all.
        let review: AgentSession?
        if let build {
            review = reviews.last { $0.worker?.reviewsSessionID == build.id }
        } else {
            review = reviews.last
        }

        let builder = build.flatMap(member)
        let reviewer = review.flatMap(member)
        let config = build?.worker
        let done = config?.reviewRounds ?? 0
        let verdict = config?.lastVerdict
        let maxRounds = [config?.maxReviewRounds ?? 0, parent.team?.maxReviewRounds ?? 0]
            .first { $0 > 0 } ?? defaultMaxRounds

        var stage: Stage
        var round = done
        if children.isEmpty {
            stage = .planning
        } else if reviewer?.isWorking == true {
            stage = .reviewing
            // The server counts a round once its verdict is in.
            if build != nil { round = min(done + 1, max(maxRounds, done)) }
        } else if builder?.isWorking == true {
            stage = verdict == .changesRequested ? .fixing : .building
        } else if verdict == .approve {
            stage = .approved
        } else if verdict == .changesRequested, done >= maxRounds {
            stage = .stopped
        } else {
            stage = .idle
        }
        return TeamProgress(stage: stage, builder: builder, reviewer: reviewer, round: round,
                            maxRounds: maxRounds, lastVerdict: verdict, workers: members)
    }

    /// Whether any worker is at work (the strip's spinner, the parent's group).
    var isWorking: Bool { workers.contains(where: \.isWorking) }

    private func with(_ text: String, _ member: Member?) -> String {
        guard let model = member?.model, !model.isEmpty else { return text }
        return "\(text) · \(model)"
    }

    private var roundOfMax: String { "\(max(round, 1)) of \(maxRounds)" }

    /// One line for the chat's team strip: "Building · deepseek-flash",
    /// "Review round 2 of 3 · gpt-6-astra", "Approved", "Stopped after 3 rounds".
    var headline: String {
        switch stage {
        case .planning: return "Planning · no workers yet"
        case .building: return with("Building", builder)
        case .reviewing:
            return with(builder == nil ? "Reviewing" : "Review round \(roundOfMax)", reviewer)
        case .fixing: return with("Fixing after review \(roundOfMax)", builder)
        case .approved: return "Approved"
        case .stopped: return "Stopped after \(round) round\(round == 1 ? "" : "s")"
        case .idle:
            if lastVerdict == .changesRequested { return "Changes requested · round \(roundOfMax)" }
            if builder != nil { return round == 0 ? "Built · not reviewed yet" : "Workers idle" }
            return reviewer != nil ? "Review done" : "Workers idle"
        }
    }

    var symbol: String {
        switch stage {
        case .planning: return "list.bullet.clipboard"
        case .building, .fixing: return "hammer.fill"
        case .reviewing: return "checklist"
        case .approved: return "checkmark.seal.fill"
        case .stopped: return "stop.circle.fill"
        case .idle: return "person.2.fill"
        }
    }
}
