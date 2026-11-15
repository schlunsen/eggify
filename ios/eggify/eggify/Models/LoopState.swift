//
//  LoopState.swift
//  eggify
//
//  Where a loop-mode session is: its latest `loop_*` event (live, for open
//  chats) or the `loop_state` the session list carries (REST, every session).
//  `iteration` counts finished turns, as the server reports it.
//

import Foundation

nonisolated struct LoopState: Equatable, Sendable {
    enum Phase: String, Sendable {
        case started, verifying, iteration, completed, failed, stopped
    }

    var phase: Phase
    var iteration: Int
    var maxIterations: Int
    var goal: String
    var verifyCommand: String
    var verifyPassed: Bool?
    var verifyOutput: String
    var reason: String

    /// `type` is the event type (`loop_iteration`); without it the payload's own.
    static func parse(_ data: [String: Any], type: String? = nil) -> LoopState? {
        let raw = type ?? data.string("type") ?? ""
        guard raw.hasPrefix("loop_"), let phase = Phase(rawValue: String(raw.dropFirst(5))) else { return nil }
        return LoopState(
            phase: phase,
            iteration: data.int("iteration") ?? 0,
            maxIterations: data.int("max_iterations") ?? 0,
            goal: data.string("goal") ?? "",
            verifyCommand: data.string("verify_command") ?? "",
            verifyPassed: data.bool("verify_passed"),
            verifyOutput: data.string("verify_output") ?? "",
            reason: data.string("reason") ?? ""
        )
    }

    /// The next event, keeping what it does not repeat (the verify output of
    /// the last check stays visible while the next iteration runs).
    func followed(by next: LoopState) -> LoopState {
        var s = next
        if s.goal.isEmpty { s.goal = goal }
        if s.verifyCommand.isEmpty { s.verifyCommand = verifyCommand }
        if s.verifyOutput.isEmpty, s.phase != .started {
            s.verifyOutput = verifyOutput
            if s.verifyPassed == nil { s.verifyPassed = verifyPassed }
        }
        if s.maxIterations == 0 { s.maxIterations = maxIterations }
        return s
    }

    var isRunning: Bool { phase == .started || phase == .verifying || phase == .iteration }

    /// The iteration being worked on or checked (1-based).
    var current: Int {
        switch phase {
        case .started: return max(iteration + 1, 1)
        case .iteration: return iteration + 1
        default: return max(iteration, 1)
        }
    }

    private var ofMax: String { maxIterations > 0 ? "\(current) of \(maxIterations)" : "\(current)" }

    /// Short badge for lists: "Loop 3/20", "Loop done".
    var badge: String {
        switch phase {
        case .started, .iteration, .verifying:
            return maxIterations > 0 ? "Loop \(current)/\(maxIterations)" : "Loop \(current)"
        case .completed: return "Loop done"
        case .failed: return "Loop failed"
        case .stopped: return "Loop stopped"
        }
    }

    /// One line for the chat's loop strip.
    var headline: String {
        let turns = iteration == 1 ? "1 iteration" : "\(iteration) iterations"
        switch phase {
        case .started: return "Loop · iteration \(ofMax)"
        case .verifying: return "Checking iteration \(ofMax)"
        case .iteration: return "Check failed · iteration \(ofMax)"
        case .completed: return "Loop done after \(turns)"
        case .failed: return "Loop gave up after \(turns)"
        case .stopped: return "Loop stopped after \(turns)"
        }
    }

    /// The server's reason in plain words ("verification passed", ...), or "".
    var detail: String {
        switch phase {
        case .completed, .failed, .stopped: return reason
        default: return ""
        }
    }

    var symbol: String {
        switch phase {
        case .started, .iteration: return "arrow.triangle.2.circlepath"
        case .verifying: return "checklist"
        case .completed: return "checkmark.seal.fill"
        case .failed: return "exclamationmark.triangle.fill"
        case .stopped: return "stop.circle.fill"
        }
    }
}
