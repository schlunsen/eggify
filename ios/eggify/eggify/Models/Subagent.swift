//
//  Subagent.swift
//  eggify
//
//  A subagent (Agent/Task tool) launched by a session's main agent, with its
//  live output. Comes from GET /api/agent/sessions/:id/subagents and the
//  background_agent_* WebSocket events.
//

import Foundation

nonisolated struct Subagent: Identifiable, Equatable, Sendable {
    enum Status: String, Sendable {
        case running, waiting, completed, failed

        var isActive: Bool { self == .running || self == .waiting }
    }

    let id: String              // agent_id
    var type: String            // e.g. "Explore", "general-purpose"
    var description: String
    var status: Status
    var progress: Double        // 0...1, often unknown (0)
    var output: [String]        // latest lines, oldest first
    var errorMessage: String?
    var parentAgentId: String?
    var background: Bool
    var createdAt: Date?
    var completedAt: Date?
    /// The model it runs on ("claude-haiku-4-5-20251001", or "sonnet" as requested).
    var model: String? = nil
    /// The model comes from the launch call, not yet from the agent itself.
    var modelRequested: Bool = false

    /// Lines kept on the device per subagent.
    static let maxLines = 120

    static func parse(_ data: [String: Any]) -> Subagent? {
        guard let id = data.nonEmptyString("agent_id") else { return nil }
        var lines = data.strings("recent_output")
        if lines.isEmpty, let last = data.nonEmptyString("last_output") { lines = [last] }
        return Subagent(
            id: id,
            type: data.nonEmptyString("subagent_type") ?? "Agent",
            description: data.nonEmptyString("description") ?? "",
            status: Status(rawValue: data.string("status") ?? "") ?? .running,
            progress: data.double("progress") ?? 0,
            output: Array(lines.suffix(maxLines)),
            errorMessage: data.nonEmptyString("error_message"),
            parentAgentId: data.nonEmptyString("parent_agent_id"),
            background: data.bool("background") ?? false,
            createdAt: EggifyDate.parse(data.string("created_at")),
            completedAt: EggifyDate.parse(data.string("completed_at")),
            model: data.nonEmptyString("model"),
            modelRequested: data.string("model_source") == "requested"
        )
    }

    mutating func append(_ lines: [String]) {
        let clean = lines.filter { !$0.trimmingCharacters(in: .whitespaces).isEmpty }
        guard !clean.isEmpty else { return }
        output.append(contentsOf: clean)
        if output.count > Self.maxLines { output.removeFirst(output.count - Self.maxLines) }
    }

    var title: String { description.isEmpty ? type : description }

    /// Short model label, e.g. "Haiku 4.5" (a port of the web's utils/agents/modelLabel.ts).
    var modelLabel: String? { model.flatMap(Self.modelLabel) }

    static func modelLabel(_ model: String) -> String? {
        let raw = model.trimmingCharacters(in: .whitespaces)
        guard !raw.isEmpty else { return nil }
        var base = String(raw.split(separator: "/").last ?? Substring(raw))
        if let bracket = base.range(of: #"\[[^\]]*\]$"#, options: .regularExpression) { base.removeSubrange(bracket) }
        let id = base.lowercased()
        let families = ["opus": "Opus", "sonnet": "Sonnet", "haiku": "Haiku", "fable": "Fable", "mythos": "Mythos"]
        let named = try? NSRegularExpression(pattern: #"(?:^|[.\-_])(opus|sonnet|haiku|fable|mythos)(?:-(\d+)(?:[-.](\d{1,2}))?)?(?=$|[-@:_])"#)
        let ns = id as NSString
        guard let m = named?.firstMatch(in: id, range: NSRange(location: 0, length: ns.length)) else {
            return base.count > 24 ? String(base.prefix(23)) + "…" : base
        }
        func group(_ i: Int) -> String? {
            let r = m.range(at: i)
            return r.location == NSNotFound ? nil : ns.substring(with: r)
        }
        let family = families[group(1) ?? ""] ?? ""
        var version = ""
        if let major = group(2), major.count < 6 {
            version = major + (group(3).map { "." + $0 } ?? "")
        }
        if version.isEmpty,
           let old = try? NSRegularExpression(pattern: #"claude-(\d+)(?:[-.](\d))?-(?:opus|sonnet|haiku)"#),
           let o = old.firstMatch(in: id, range: NSRange(location: 0, length: ns.length)) {
            version = ns.substring(with: o.range(at: 1))
            if o.range(at: 2).location != NSNotFound { version += "." + ns.substring(with: o.range(at: 2)) }
        }
        return version.isEmpty ? family : "\(family) \(version)"
    }
}

/// One output line. The server writes a subagent's tool calls as
/// "TOOL:<name>: <what it acts on>"; everything else is text.
nonisolated struct SubagentLine: Equatable, Sendable {
    let tool: String?
    let text: String

    init(_ raw: String) {
        if raw.hasPrefix("TOOL:") {
            let rest = raw.dropFirst("TOOL:".count)
            if let colon = rest.firstIndex(of: ":") {
                tool = String(rest[..<colon]).trimmingCharacters(in: .whitespaces)
                text = String(rest[rest.index(after: colon)...]).trimmingCharacters(in: .whitespaces)
            } else {
                tool = rest.trimmingCharacters(in: .whitespaces)
                text = ""
            }
        } else {
            tool = nil
            text = raw.trimmingCharacters(in: .whitespacesAndNewlines)
        }
    }
}
