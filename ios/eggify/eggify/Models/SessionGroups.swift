//
//  SessionGroups.swift
//  eggify
//
//  The Agents list groups sessions by what they need from you, as the web
//  sidebar does (`utils/agents/sessionGroups.ts`):
//
//    Needs you  a permission request or a question is waiting
//    Working    its turn runs, its subagents still work while it is idle,
//               or a Claude Code /loop is scheduled
//    Today      everything else with activity since local midnight
//    Earlier    the rest (collapsed by default)
//
//  Needs you and Working show full rows; so does an idle session whose last
//  activity is under 12 hours old (`fullRowAfterIdle`), so an agent that just
//  finished stays readable before it shrinks to one line. Ended sessions only
//  go into Today / Earlier and stay small. Newest activity first.
//
//  A worker session (`options.worker`) sits under the row of the session that
//  started it, in that session's group, and a working worker keeps its parent
//  in Working. It is an ordinary row when its parent is not in the list, and
//  while it needs you (so its request can be opened from Needs you).
//

import Foundation

nonisolated enum SessionGroupID: String, CaseIterable, Sendable {
    case needs, working, today, earlier

    var label: String {
        switch self {
        case .needs: return "Needs you"
        case .working: return "Working"
        case .today: return "Today"
        case .earlier: return "Earlier"
        }
    }

    /// Today and Earlier collapse from their header.
    var collapsible: Bool { self == .today || self == .earlier }
    var collapsedByDefault: Bool { self == .earlier }
}

/// A worker session shown under its parent's row.
nonisolated struct SessionWorkerRow: Identifiable, Sendable {
    let session: AgentSession
    let status: SessionStatus

    var id: String { session.id }
    /// Its turn runs, or its subagents still do.
    var isWorking: Bool { TeamProgress.isWorking(session, status: status) }

    /// "Build · sonnet-5-5 · Working", "Review · gpt-6-astra · Idle",
    /// "Build · sonnet-5-5 · Changes requested".
    var line: String {
        guard let worker = session.worker else { return session.displayTitle }
        var parts = [worker.mode.label]
        if let model = session.model_name { parts.append(ModelNames.short(model)) }
        if isWorking {
            parts.append(SessionStatus.processing.label)
        } else if let verdict = worker.lastVerdict, worker.mode == .build, status != .ended, status != .error {
            parts.append(verdict.label)
        } else {
            parts.append(status.label)
        }
        return parts.joined(separator: " · ")
    }
}

nonisolated struct SessionGroupRow: Identifiable, Sendable {
    let session: AgentSession
    let group: SessionGroupID
    /// What the session waits for ("Allow gh pr merge 1124?"); "" when nothing.
    let waitingFor: String
    /// Subagents still working while the session itself is idle.
    let subagents: Int
    let activityAt: Date?
    /// Full row (summary, tags) rather than one line.
    let full: Bool
    /// The workers it started, oldest first (shown under it, one line each).
    var workers: [SessionWorkerRow] = []

    var id: String { session.id }
}

nonisolated struct SessionGroup: Identifiable, Sendable {
    let id: SessionGroupID
    var rows: [SessionGroupRow]
    var label: String { id.label }
    var collapsible: Bool { id.collapsible }
}

nonisolated enum SessionGroups {
    /// How long after its due time a scheduled /loop wakeup still counts.
    static let loopDueGrace: TimeInterval = 5 * 60
    /// How long an idle session keeps its full row after its last activity
    /// (web `FULL_ROW_AFTER_IDLE_MS`).
    static let fullRowAfterIdle: TimeInterval = 12 * 3600

    /// Last activity: updated_at, else created_at.
    static func activityAt(_ session: AgentSession) -> Date? {
        EggifyDate.parse(session.updated_at) ?? EggifyDate.parse(session.created_at)
    }

    /// Whether a scheduled Claude Code /loop keeps the session working
    /// (web `loopPhase(...) !== null`).
    static func loopScheduled(nextAt: Date?, status: SessionStatus, now: Date) -> Bool {
        guard let nextAt else { return false }
        if nextAt > now || status.isActive { return true }
        return now.timeIntervalSince(nextAt) < loopDueGrace
    }

    /// Puts every session (duplicate ids once) into its group. All four
    /// groups are returned, empty ones too.
    static func group(
        _ sessions: [AgentSession],
        now: Date,
        calendar: Calendar = .current,
        status: (AgentSession) -> SessionStatus = { SessionStatus($0.status) },
        waitingFor: (AgentSession) -> String = { _ in "" },
        subagentsWorking: (AgentSession) -> Int = { $0.subagents_running }
    ) -> [SessionGroup] {
        var rows: [SessionGroupID: [SessionGroupRow]] = [:]
        let midnight = calendar.startOfDay(for: now)
        var seen = Set<String>()
        let unique = sessions.filter { !$0.id.isEmpty && seen.insert($0.id).inserted }
        let workers = workersByParent(unique, status: status, waitingFor: waitingFor)
        let nested = Set(workers.values.flatMap { $0.map(\.id) })
        for session in unique where !nested.contains(session.id) {
            let state = status(session)
            let children = workers[session.id] ?? []
            let ended = state == .ended
            let waiting = ended ? "" : waitingFor(session)
            let subagents = ended ? 0 : max(0, subagentsWorking(session))
            let activity = activityAt(session)
            let group: SessionGroupID
            if !waiting.isEmpty {
                group = .needs
            } else if !ended && (state.isActive || subagents > 0 || children.contains(where: \.isWorking)
                                 || loopScheduled(nextAt: session.scheduledLoopNextDate, status: state, now: now)) {
                group = .working
            } else {
                group = (activity.map { $0 >= midnight } ?? false) ? .today : .earlier
            }
            let recent = activity.map { now.timeIntervalSince($0) < fullRowAfterIdle } ?? false
            let full = group == .needs || group == .working || (!ended && recent)
            rows[group, default: []].append(SessionGroupRow(
                session: session, group: group, waitingFor: waiting,
                subagents: state == .idle || state == .completed ? subagents : 0, activityAt: activity,
                full: full, workers: children))
        }
        return SessionGroupID.allCases.map { id in
            SessionGroup(id: id, rows: (rows[id] ?? []).sorted { a, b in
                let ta = a.activityAt?.timeIntervalSince1970 ?? 0
                let tb = b.activityAt?.timeIntervalSince1970 ?? 0
                return ta != tb ? ta > tb : a.session.id < b.session.id
            })
        }
    }

    /// The workers that go under their parent's row, by parent id, oldest
    /// first: the parent is in the list (and not itself under another row),
    /// and the worker does not need the user.
    static func workersByParent(
        _ sessions: [AgentSession],
        status: (AgentSession) -> SessionStatus,
        waitingFor: (AgentSession) -> String
    ) -> [String: [SessionWorkerRow]] {
        let ids = Set(sessions.map(\.id))
        var parentOf: [String: String] = [:]
        var states: [String: SessionStatus] = [:]
        for session in sessions {
            guard session.worker != nil, let parent = session.spawned_by_session_id,
                  parent != session.id, ids.contains(parent) else { continue }
            let state = status(session)
            if state != .ended, !waitingFor(session).isEmpty { continue }
            parentOf[session.id] = parent
            states[session.id] = state
        }
        var result: [String: [SessionWorkerRow]] = [:]
        for session in sessions {
            // A worker's own workers stay ordinary rows (one level only).
            guard let parent = parentOf[session.id], parentOf[parent] == nil else { continue }
            result[parent, default: []].append(SessionWorkerRow(session: session, status: states[session.id] ?? .idle))
        }
        return result.mapValues { rows in
            rows.sorted { a, b in
                let ta = a.session.createdDate ?? .distantPast, tb = b.session.createdDate ?? .distantPast
                return ta != tb ? ta < tb : a.id < b.id
            }
        }
    }

    static func isCollapsed(_ id: SessionGroupID, collapsed: [SessionGroupID: Bool]) -> Bool {
        id.collapsible && (collapsed[id] ?? id.collapsedByDefault)
    }

    /// The sessions as shown, top to bottom, leaving out collapsed groups.
    static func visibleOrder(_ groups: [SessionGroup], collapsed: [SessionGroupID: Bool]) -> [AgentSession] {
        groups.flatMap { group in
            isCollapsed(group.id, collapsed: collapsed) ? []
                : group.rows.flatMap { [$0.session] + $0.workers.map(\.session) }
        }
    }

    // MARK: Time

    private static let weekdays = ["Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"]
    private static let months = ["Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"]

    /// "now", "4m", "2h" (today), "Mon" (this week), "3 Oct" (older), "" without a time.
    static func relativeTime(_ at: Date?, now: Date, calendar: Calendar = .current) -> String {
        guard let at else { return "" }
        let diff = now.timeIntervalSince(at)
        if diff < 60 { return "now" }
        if diff < 3600 { return "\(Int(diff / 60))m" }
        if at >= calendar.startOfDay(for: now) { return "\(Int(diff / 3600))h" }
        let parts = calendar.dateComponents([.weekday, .day, .month], from: at)
        if diff < 6 * 86_400 { return weekdays[(parts.weekday ?? 1) - 1] }
        return "\(parts.day ?? 1) \(months[(parts.month ?? 1) - 1])"
    }

    // MARK: Waiting labels

    private static func shorten(_ text: String, _ max: Int) -> String {
        let flat = text.split(whereSeparator: \.isWhitespace).joined(separator: " ")
        return flat.count > max ? String(flat.prefix(max - 1)) + "…" : flat
    }

    /// "Allow gh pr merge 1124?", "Allow Edit app.vue?", "Allow WebFetch?"
    static func permissionWaitLabel(tool: String, details: JSONValue?) -> String {
        let tool = tool.isEmpty ? "tool" : tool
        let d = details?.objectValue ?? [:]
        if let command = d["command"]?.stringValue, !command.trimmingCharacters(in: .whitespaces).isEmpty {
            return "Allow \(shorten(command, 60))?"
        }
        let target = ["file_path", "path", "url", "pattern"].lazy
            .compactMap { d[$0]?.stringValue }
            .first { !$0.trimmingCharacters(in: .whitespaces).isEmpty }
        if let target {
            let name: String
            if target.hasPrefix("http:") || target.hasPrefix("https:") {
                name = target.replacingOccurrences(of: #"^https?://"#, with: "", options: .regularExpression)
            } else {
                name = target.split(separator: "/").last.map(String.init) ?? target
            }
            return "Allow \(tool) \(shorten(name, 48))?"
        }
        return "Allow \(tool)?"
    }

    /// What a session waits for. Requests this device has heard of win (they
    /// go away once answered); for kinds it has no record of (`nil`), the
    /// counts the server sent with the session list are used.
    static func waitingLabel(
        permissions: [PermissionRequest]?,
        questions: [UserQuestion]?,
        listedPermissions: Int = 0,
        listedQuestions: Int = 0
    ) -> String {
        if let question = questions?.first {
            if question.loopProposal != nil { return "Loop proposal waiting" }
            return question.workerProposal != nil ? "Worker proposal waiting" : "Question waiting"
        }
        if let permission = permissions?.first {
            return permissionWaitLabel(tool: permission.tool, details: permission.details)
        }
        if questions == nil, listedQuestions > 0 { return "Question waiting" }
        if permissions == nil, listedPermissions > 0 { return "Approval needed" }
        return ""
    }
}
