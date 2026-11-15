//
//  SessionStatus.swift
//  eggify
//
//  Session status enumeration with UI helpers
//

import SwiftUI

enum SessionStatus: String, CaseIterable {
    case idle
    case processing
    case thinking
    case completed
    case error
    case ended

    init(_ string: String) {
        switch string.lowercased() {
        case "processing", "running": self = .processing
        case "thinking": self = .thinking
        case "completed": self = .completed
        case "error", "failed": self = .error
        case "ended": self = .ended
        default: self = .idle // "idle", "active", unknown
        }
    }

    var color: Color {
        switch self {
        case .processing, .thinking: return EggifyColors.accent
        case .completed: return EggifyColors.success
        case .error: return EggifyColors.error
        case .idle: return .gray
        case .ended: return Color(.systemGray3)
        }
    }

    var label: String {
        switch self {
        case .processing, .thinking: return "Working"
        case .completed, .idle: return "Idle"
        case .error: return "Error"
        case .ended: return "Ended"
        }
    }

    /// Whether the agent is currently working on a turn.
    var isActive: Bool {
        self == .processing || self == .thinking
    }

    /// The status to show for a session: an idle session whose subagents still
    /// run counts as working, as on the web's agent overview.
    func including(subagentsRunning: Int) -> SessionStatus {
        subagentsRunning > 0 && (self == .idle || self == .completed) ? .processing : self
    }
}
