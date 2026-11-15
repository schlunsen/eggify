//
//  SessionSkills.swift
//  eggify
//
//  The approved skills a running session can use, from
//  GET /api/agent/sessions/:id/skills. A skill reaches a session in one of
//  three ways: it applies everywhere, it belongs to the project whose folder
//  the session works in, or it was added to the session. Skills of other
//  projects can be added; `PUT` replaces the session's whole added list.
//  The Claude CLI runs a skill when `/<name>` is typed in the chat.
//

import Foundation

nonisolated struct SessionSkill: Identifiable, Equatable, Sendable {
    /// Why the session gets the skill ("" = it doesn't).
    enum Reach: String, Sendable { case everywhere, folder, added, none = "" }

    let id: String
    var name: String
    var description: String?
    var scope: String
    var argumentHint: String?
    /// It can be run as `/<name>` from the chat.
    var userInvocable: Bool
    var reach: Reach
    /// It is on the session's added list (it may also reach it another way).
    var added: Bool

    static func parse(_ data: [String: Any]) -> SessionSkill? {
        guard let id = data.nonEmptyString("id"), let name = data.nonEmptyString("name") else { return nil }
        return SessionSkill(
            id: id,
            name: name,
            description: data.nonEmptyString("description"),
            scope: data.string("scope") ?? "",
            argumentHint: data.nonEmptyString("argument_hint"),
            userInvocable: data.bool("user_invocable") ?? false,
            reach: Reach(rawValue: data.string("reach") ?? "") ?? .none,
            added: data.bool("added") ?? false
        )
    }

    var isActive: Bool { reach != .none }

    /// The small label saying why the session has it.
    var reachLabel: String? {
        switch reach {
        case .everywhere: return "Everywhere"
        case .folder: return "This project"
        case .added: return "Added"
        case .none: return nil
        }
    }

    /// What Use puts in front of the draft: "/name ".
    var slashCommand: String { "/\(name) " }
}

nonisolated struct SessionSkills: Equatable, Sendable {
    /// Which agent runs the session; it decides when a change reaches it.
    enum Runtime: String, Sendable {
        case claude = "claude-sdk"
        case codex
        case direct
        case unknown = ""
    }

    var sessionID: String
    var runtime: Runtime
    var skills: [SessionSkill]
    /// The Claude CLI restarts before the next prompt to pick up a change.
    var restartPending: Bool
    var subagentsRunning: Int

    static func parse(_ data: [String: Any]) -> SessionSkills {
        SessionSkills(
            sessionID: data.string("session_id") ?? "",
            runtime: Runtime(rawValue: data.string("runtime") ?? "") ?? .unknown,
            skills: data.dicts("skills").compactMap(SessionSkill.parse),
            restartPending: data.bool("restart_pending") ?? false,
            subagentsRunning: data.int("subagents_running") ?? 0
        )
    }

    /// "In this session", in the server's order.
    var active: [SessionSkill] { skills.filter(\.isActive) }
    /// "Other projects' skills": ones that can be added.
    var others: [SessionSkill] { skills.filter { !$0.isActive } }

    /// The added list to send after adding (or removing) one skill.
    func addedIDs(adding id: String) -> [String] {
        var ids = skills.filter(\.added).map(\.id)
        if !ids.contains(id) { ids.append(id) }
        return ids
    }

    func addedIDs(removing id: String) -> [String] {
        skills.filter { $0.added && $0.id != id }.map(\.id)
    }

    /// The footer under the list, or nil when there is nothing to say.
    var note: String? {
        switch runtime {
        case .claude:
            guard restartPending else { return nil }
            var text = "Takes effect with your next message."
            if subagentsRunning > 0 {
                text += " The agent restarts then, which stops \(subagentsRunning) running "
                    + (subagentsRunning == 1 ? "subagent." : "subagents.")
            }
            return text
        case .codex, .direct:
            return "This agent sees its skill list when its conversation starts; added skills reach it through its skill tools."
        case .unknown:
            return nil
        }
    }

    /// Skills whose name or description contains the query (case and accents ignored).
    static func filter(_ skills: [SessionSkill], query: String) -> [SessionSkill] {
        let query = query.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !query.isEmpty else { return skills }
        let options: String.CompareOptions = [.caseInsensitive, .diacriticInsensitive]
        return skills.filter {
            $0.name.range(of: query, options: options) != nil
                || ($0.description?.range(of: query, options: options) != nil)
        }
    }

    /// The draft after Use: "/name " on its own when the draft is empty, else
    /// in front of it (a draft that already starts with it is left alone).
    static func draft(using skill: SessionSkill, in draft: String) -> String {
        let command = skill.slashCommand
        let rest = draft.drop(while: \.isWhitespace)
        if rest.isEmpty { return command }
        if rest.hasPrefix(command) { return String(rest) }
        return command + rest
    }
}
