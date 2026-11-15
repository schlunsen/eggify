//
//  AgentModels.swift
//  eggify
//
//  Agent sessions, messages, tool calls and the interactive requests
//  (permission prompts, agent questions) the server sends.
//
//  Messages and sessions arrive in two shapes: REST rows (flat, content as a
//  string) and live WebSocket events (content as a structured object). Both
//  are parsed here so the UI only ever deals with one model.
//

import Foundation

// MARK: - Session

nonisolated struct AgentSession: Identifiable, Equatable, Sendable {
    let id: String
    var status: String
    var model_name: String?
    var provider: String?
    var git_branch: String?
    var working_directory: String?
    var created_at: String
    var updated_at: String
    var message_count: Int?
    var cost: Double?
    var project_id: String?
    var selected_avatar_id: Int64?
    var selected_avatar: Avatar?

    var agent_name: String? = nil
    var pending_permissions: Int = 0
    var pending_questions: Int = 0
    var permission_mode: String? = nil
    var yolo: Bool = false
    var sandboxed: Bool = false
    var effort_level: String? = nil
    var num_turns: Int = 0
    var error_message: String? = nil
    var subagents_running: Int = 0
    /// The user's own tags (sidebar "Add tag" on the web), stored on the server.
    var tags: [String] = []
    /// Loop mode (`options.mode == "loop"`), and where the loop is.
    var loop_mode: Bool = false
    var loop: LoopState? = nil
    /// When a Claude Code /loop (ScheduleWakeup) runs next (`scheduled_loop.next_at`).
    var scheduled_loop_next_at: String? = nil
    /// The session's own question policy (nil = follows the global default).
    var question_policy: QuestionPolicy? = nil
    /// The policy that applies (override or global); nil from older servers.
    var question_policy_effective: QuestionPolicy? = nil
    /// The session that started this one (`options.spawned_by_session_id`).
    var spawned_by_session_id: String? = nil
    /// Set on a worker session: what it builds or reviews, and its review state.
    var worker: WorkerInfo? = nil
    /// Set on a team's orchestrator: the models its workers run on.
    var team: TeamInfo? = nil
    /// The session may not change files (review workers).
    var read_only: Bool = false

    /// Sessions that wait for the user (a permission prompt or a question).
    var needsAttention: Bool { pending_permissions + pending_questions > 0 }

    var shortID: String { String(id.prefix(8)) }

    /// Human title: agent name, else the working directory's last component.
    var displayTitle: String {
        if let name = agent_name, !name.isEmpty { return name }
        if let dir = working_directory, !dir.isEmpty {
            return (dir as NSString).lastPathComponent
        }
        return "Session \(shortID)"
    }

    var createdDate: Date? { EggifyDate.parse(created_at) }
    var updatedDate: Date? { EggifyDate.parse(updated_at) }
    var scheduledLoopNextDate: Date? { scheduled_loop_next_at.flatMap { EggifyDate.parse($0) } }

    static func parse(_ data: [String: Any]) -> AgentSession? {
        guard let id = data.nonEmptyString("id") ?? data.nonEmptyString("session_id") else { return nil }
        let options = data.dict("options") ?? [:]

        var avatar: Avatar?
        if let a = data.dict("selected_avatar"), let avatarId = a.int64("id") {
            avatar = Avatar(
                id: avatarId,
                theme_id: a.int64("theme_id") ?? 0,
                name: a.string("name") ?? "Unknown",
                type: a.string("type"),
                image_path: a.string("image_path"),
                image_url: a.string("image_url"),
                style: a.string("style"),
                seed: a.string("seed"),
                color: a.string("color"),
                created_at: a.string("created_at") ?? EggifyDate.string()
            )
        }

        let now = EggifyDate.string()
        let permissionMode = options.string("permission_mode")
        let yolo = options.bool("dangerously_skip_permissions") == true
            || ["yolo", "bypassPermissions", "allow-all"].contains(permissionMode ?? "")

        return AgentSession(
            id: id,
            status: data.string("status") ?? "idle",
            model_name: data.nonEmptyString("model_name") ?? options.nonEmptyString("model"),
            provider: data.nonEmptyString("provider") ?? options.nonEmptyString("provider"),
            git_branch: data.nonEmptyString("git_branch"),
            working_directory: data.nonEmptyString("working_directory") ?? options.nonEmptyString("working_directory"),
            created_at: data.string("created_at") ?? now,
            updated_at: data.string("updated_at") ?? now,
            message_count: data.int("message_count"),
            cost: data.double("cost_usd") ?? data.double("cost"),
            project_id: data.nonEmptyString("project_id") ?? options.nonEmptyString("project_id"),
            selected_avatar_id: data.int64("selected_avatar_id") ?? avatar?.id,
            selected_avatar: avatar,
            agent_name: options.nonEmptyString("agent_name"),
            pending_permissions: data.int("pending_permissions") ?? 0,
            pending_questions: data.int("pending_questions") ?? 0,
            permission_mode: permissionMode,
            yolo: yolo,
            sandboxed: options.bool("sandboxed") ?? false,
            effort_level: options.nonEmptyString("effort_level"),
            num_turns: data.int("num_turns") ?? 0,
            error_message: data.nonEmptyString("error_message"),
            subagents_running: data.int("subagents_running") ?? 0,
            tags: (data["tags"] as? [Any])?.compactMap { ($0 as? String)?.trimmingCharacters(in: .whitespaces) }
                .filter { !$0.isEmpty } ?? [],
            loop_mode: options.string("mode") == "loop",
            loop: data.dict("loop_state").flatMap { LoopState.parse($0) },
            scheduled_loop_next_at: data.dict("scheduled_loop")?.nonEmptyString("next_at"),
            question_policy: QuestionPolicy.parse(data["question_policy"] ?? options["question_policy"]),
            question_policy_effective: QuestionPolicy.parse(data["question_policy_effective"]),
            spawned_by_session_id: options.nonEmptyString("spawned_by_session_id"),
            worker: options.dict("worker").map(WorkerInfo.parse),
            team: options.dict("team").map(TeamInfo.parse),
            read_only: options.bool("read_only") ?? false
        )
    }

    /// Apply a live update; fields the update does not carry keep their value.
    func merged(with data: [String: Any]) -> AgentSession {
        guard var u = AgentSession.parse(data.merging(["id": id]) { current, _ in current }) else { return self }
        if data["selected_avatar"] == nil {
            u.selected_avatar = selected_avatar
            u.selected_avatar_id = u.selected_avatar_id ?? selected_avatar_id
        }
        if data["options"] == nil {
            u.agent_name = agent_name
            u.permission_mode = permission_mode
            u.yolo = yolo
            u.sandboxed = sandboxed
            u.effort_level = effort_level
            u.loop_mode = loop_mode
            u.spawned_by_session_id = spawned_by_session_id
            u.worker = worker
            u.team = team
            u.read_only = read_only
        }
        if data["loop_state"] == nil { u.loop = loop }
        if data["scheduled_loop"] == nil { u.scheduled_loop_next_at = scheduled_loop_next_at }
        if data["created_at"] == nil { u.created_at = created_at }
        if data["updated_at"] == nil { u.updated_at = updated_at }
        if data["status"] == nil { u.status = status }
        if data["pending_permissions"] == nil { u.pending_permissions = pending_permissions }
        if data["pending_questions"] == nil { u.pending_questions = pending_questions }
        if data["num_turns"] == nil { u.num_turns = num_turns }
        if data["subagents_running"] == nil { u.subagents_running = subagents_running }
        if data["tags"] == nil { u.tags = tags }
        if data["question_policy"] == nil && (data["options"] as? [String: Any])?["question_policy"] == nil {
            u.question_policy = question_policy
        }
        if data["question_policy_effective"] == nil { u.question_policy_effective = question_policy_effective }
        u.model_name = u.model_name ?? model_name
        u.provider = u.provider ?? provider
        u.git_branch = u.git_branch ?? git_branch
        u.working_directory = u.working_directory ?? working_directory
        u.message_count = u.message_count ?? message_count
        u.cost = u.cost ?? cost
        u.project_id = u.project_id ?? project_id
        u.error_message = data["error_message"] == nil ? error_message : u.error_message
        return u
    }
}

// MARK: - Avatar

nonisolated struct Avatar: Codable, Identifiable, Equatable, Sendable {
    let id: Int64
    let theme_id: Int64
    let name: String
    let type: String? // "preset", "ai_generated"
    let image_path: String?
    let image_url: String?
    let style: String?
    let seed: String?
    let color: String?
    let created_at: String

    static func == (lhs: Avatar, rhs: Avatar) -> Bool {
        lhs.id == rhs.id
    }
}

nonisolated struct AvatarTheme: Codable, Identifiable, Sendable {
    let id: Int64
    let name: String
}

// MARK: - Content

nonisolated struct ContentBlockSource: Codable, Equatable, Sendable {
    let type: String
    let media_type: String
    let data: String
}

nonisolated struct ContentBlock: Codable, Equatable, Sendable {
    let type: String  // "text", "image"
    let text: String?
    let source: ContentBlockSource?
}

/// Message content: plain text, or content blocks (text + images).
nonisolated enum ContentBlockOrString: Codable, Equatable, Sendable {
    case string(String)
    case blocks([ContentBlock])

    init(from decoder: Decoder) throws {
        let container = try decoder.singleValueContainer()
        if let blocks = try? container.decode([ContentBlock].self) {
            self = .blocks(blocks)
            return
        }
        if let stringValue = try? container.decode(String.self) {
            self = ContentBlockOrString.fromString(stringValue)
            return
        }
        throw DecodingError.dataCorruptedError(in: container, debugDescription: "Cannot decode content")
    }

    /// User prompts with images are stored as a JSON array of content blocks.
    static func fromString(_ value: String) -> ContentBlockOrString {
        let trimmed = value.trimmingCharacters(in: .whitespacesAndNewlines)
        if trimmed.hasPrefix("["), let data = trimmed.data(using: .utf8),
           let blocks = try? JSONDecoder().decode([ContentBlock].self, from: data),
           !blocks.isEmpty {
            return .blocks(blocks)
        }
        return .string(value)
    }

    func encode(to encoder: Encoder) throws {
        var container = encoder.singleValueContainer()
        switch self {
        case .string(let str): try container.encode(str)
        case .blocks(let blocks): try container.encode(blocks)
        }
    }

    var textContent: String {
        switch self {
        case .string(let str):
            return str
        case .blocks(let blocks):
            return blocks.compactMap { $0.text }.joined(separator: "\n")
        }
    }

    var imageBlocks: [ImageBlockData] {
        switch self {
        case .string:
            return []
        case .blocks(let blocks):
            return blocks.enumerated().compactMap { index, block in
                guard block.type == "image", let source = block.source else { return nil }
                return ImageBlockData(index: index, mediaType: source.media_type, base64Data: source.data)
            }
        }
    }
}

nonisolated struct ImageBlockData: Identifiable, Equatable, Sendable {
    let index: Int
    let mediaType: String
    let base64Data: String

    /// Stable across renders so image caches and SwiftUI identity hold.
    var id: String { "\(index)-\(base64Data.count)-\(base64Data.suffix(16))" }

    var dataUrl: String {
        "data:\(mediaType);base64,\(base64Data)"
    }
}

// MARK: - Message

nonisolated struct AgentMessage: Identifiable, Equatable, Sendable {
    let id: String
    let role: String  // "user", "assistant", "system", "error", "result"
    let content: ContentBlockOrString
    let created_at: String?
    let thinking: String?
    let tools: [ToolUseInfo]?

    var sequence: Int? = nil
    /// Optimistic user message not yet confirmed by a reload.
    var isPending: Bool = false
    var modelName: String? = nil
    /// Images returned by tools (e.g. Read on a screenshot).
    var toolImages: [ImageBlockData] = []
    /// For a live tool result: the tool call each of `toolImages` answers.
    var toolImageToolUseIDs: [String] = []
    /// Audio/video its tool calls made or showed (set by the chat from the
    /// session's media list, never stored), each played under the call.
    var media: [SessionMedia] = []
    /// For role "result": turn summary.
    var turnSummary: TurnSummary? = nil

    nonisolated struct TurnSummary: Equatable, Sendable {
        var durationMs: Int?
        var costUSD: Double?
        var numTurns: Int?
    }

    var isUser: Bool { role == "user" }
    var isSystemNotice: Bool { role == "system" || role == "error" }

    func getTools() -> [ToolUseInfo] { tools ?? [] }
    var hasTools: Bool { !(tools ?? []).isEmpty }

    var createdDate: Date? { EggifyDate.parse(created_at) }

    /// Parse a REST row or a live `agent_message` / `messages_loaded` entry.
    /// Returns nil for messages that should never show (SDK init chatter,
    /// tool-result-only echoes).
    static func parse(_ data: [String: Any]) -> AgentMessage? {
        guard let id = data.nonEmptyString("id") else { return nil }
        var role = data.string("role") ?? "assistant"
        var createdAt = data.string("created_at") ?? data.string("timestamp")
        if createdAt == nil, let meta = data.dict("metadata") {
            createdAt = meta.string("created_at") ?? meta.string("timestamp")
        }

        var content: ContentBlockOrString = .string("")
        var thinking: String? = data.nonEmptyString("thinking_content") ?? data.nonEmptyString("thinking")
        var toolsRaw: [Any] = (data["tool_uses"] as? [Any]) ?? []
        var toolImages: [ImageBlockData] = []
        var toolImageIDs: [String] = []
        var summary: TurnSummary?

        switch data["content"] {
        case let text as String:
            content = .fromString(text)
        case let dict as [String: Any]:
            let type = dict.string("type") ?? ""
            switch type {
            case "assistant":
                content = .string(dict.strings("text").joined(separator: "\n\n"))
                let thinkingParts = dict.strings("thinking").filter { !$0.isEmpty }
                if !thinkingParts.isEmpty { thinking = thinkingParts.joined(separator: "\n\n") }
                if let tools = dict["tools"] as? [Any] { toolsRaw = tools }
            case "user":
                switch dict["content"] {
                case let s as String: content = .fromString(s)
                case let arr as [Any]:
                    if let data = try? JSONSerialization.data(withJSONObject: arr),
                       let blocks = try? JSONDecoder().decode([ContentBlock].self, from: data) {
                        content = .blocks(blocks)
                    }
                default: break
                }
                for result in dict.dicts("tool_results") {
                    if let preview = result.dict("image_preview"),
                       let source = preview.dict("source"),
                       let b64 = source.string("data") {
                        toolImages.append(ImageBlockData(index: toolImages.count,
                                                         mediaType: source.string("media_type") ?? "image/png",
                                                         base64Data: b64))
                        toolImageIDs.append(result.string("tool_use_id") ?? "")
                    }
                }
            case "result":
                role = "result"
                summary = TurnSummary(durationMs: dict.int("duration_ms"),
                                      costUSD: dict.double("cost_usd"),
                                      numTurns: dict.int("num_turns"))
            case "system":
                // SDK init / status chatter; only free text is worth showing.
                return nil
            case "agent_error":
                role = "error"
                content = .string(dict.string("error") ?? "The agent hit an error")
            default:
                if let text = dict.string("text") ?? dict.string("content") {
                    content = .string(text)
                }
            }
        default:
            break
        }

        let tools = toolsRaw.enumerated().compactMap { index, raw in
            ToolUseInfo.parse(raw as? [String: Any], fallbackID: "\(id)-tool-\(index)")
        }

        var message = AgentMessage(
            id: id,
            role: role,
            content: content,
            created_at: createdAt ?? EggifyDate.string(),
            thinking: thinking,
            tools: tools.isEmpty ? nil : tools
        )
        message.sequence = data.int("sequence")
        message.modelName = data.nonEmptyString("model_name")
        message.toolImages = toolImages
        message.toolImageToolUseIDs = toolImageIDs
        message.turnSummary = summary
        return message
    }

    /// True when the message has nothing worth a bubble.
    var isEmptyForDisplay: Bool {
        if role == "result" { return false }
        let text = content.textContent.trimmingCharacters(in: .whitespacesAndNewlines)
        return text.isEmpty
            && content.imageBlocks.isEmpty
            && toolImages.isEmpty
            && media.isEmpty
            && getTools().isEmpty
            && (thinking ?? "").isEmpty
    }

    static func local(role: String, text: String, pending: Bool = false) -> AgentMessage {
        var m = AgentMessage(id: "local-\(UUID().uuidString)", role: role, content: .string(text),
                             created_at: EggifyDate.string(), thinking: nil, tools: nil)
        m.isPending = pending
        return m
    }
}

// MARK: - Tool use

extension AgentMessage {
    /// A tool result that only carries images (Read on a screenshot): the
    /// server sends it as a "user" message with no text. Its images belong on
    /// the tool call, as on the desktop, not in an empty user bubble.
    var toolResultImages: [String: ImageBlockData] {
        guard isUser, content.textContent.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty else { return [:] }
        var images: [String: ImageBlockData] = [:]
        for (image, id) in zip(toolImages, toolImageToolUseIDs) where !id.isEmpty && images[id] == nil {
            images[id] = image
        }
        return images
    }
}

extension Array where Element == AgentMessage {
    /// Puts each image on the message that made its tool call, after that
    /// message's own images, in tool order. Returns whether anything changed.
    @discardableResult
    mutating func attachToolImages(_ images: [String: ImageBlockData]) -> Bool {
        guard !images.isEmpty else { return false }
        var changed = false
        for i in indices where !self[i].isUser {
            let tools = self[i].getTools()
            guard tools.contains(where: { images[$0.id] != nil }) else { continue }
            var attached = self[i].toolImages
            // One id per image; images that came with the message have none.
            var ids = self[i].toolImageToolUseIDs
            if ids.count < attached.count { ids = [String](repeating: "", count: attached.count - ids.count) + ids }
            for tool in tools {
                guard let image = images[tool.id], !ids.contains(tool.id) else { continue }
                attached.append(ImageBlockData(index: attached.count, mediaType: image.mediaType, base64Data: image.base64Data))
                ids.append(tool.id)
            }
            if attached.count != self[i].toolImages.count {
                self[i].toolImageToolUseIDs = ids
                self[i].toolImages = attached
                changed = true
            }
        }
        return changed
    }
}

nonisolated struct ToolUseInfo: Identifiable, Equatable, Sendable {
    let id: String
    let name: String
    let status: String?
    let input: JSONValue?

    /// `fallbackID` is used when the server sent no id; it must be stable
    /// (message id + position) so reloads keep the row's identity and state.
    static func parse(_ data: [String: Any]?, fallbackID: String = UUID().uuidString) -> ToolUseInfo? {
        guard let data, let name = data.nonEmptyString("name") else { return nil }
        let rawInput = data["input"]
        var input: JSONValue? = rawInput.map { JSONValue(any: $0) }
        // Some rows store the input as a JSON string.
        if case .string(let s)? = input, let d = s.data(using: .utf8),
           let obj = try? JSONSerialization.jsonObject(with: d) {
            input = JSONValue(any: obj)
        }
        return ToolUseInfo(id: data.nonEmptyString("id") ?? fallbackID,
                           name: name,
                           status: data.string("status"),
                           input: input)
    }

    private var lowerName: String { name.lowercased() }

    var displayName: String {
        if name.hasPrefix("mcp__") {
            let parts = name.split(separator: "_", omittingEmptySubsequences: true)
            return parts.last.map(String.init)?.replacingOccurrences(of: "_", with: " ") ?? name
        }
        return name
    }

    var inputDict: [String: Any]? { input?.anyValue as? [String: Any] }

    func inputString(_ key: String) -> String? { input?[key]?.stringValue }

    var bashCommand: String? { lowerName == "bash" ? inputString("command") : nil }
    var bashDescription: String? { lowerName == "bash" ? inputString("description") : nil }
    var readFilePath: String? { lowerName == "read" ? inputString("file_path") : nil }
    var readLimit: Int? { lowerName == "read" ? input?["limit"]?.intValue : nil }

    var filePath: String? {
        inputString("file_path") ?? inputString("path") ?? inputString("notebook_path")
    }

    /// Old/new text for Edit-style tools, as pairs.
    var edits: [(old: String, new: String)] {
        switch lowerName {
        case "edit":
            return [(inputString("old_string") ?? "", inputString("new_string") ?? "")]
        case "multiedit":
            return (input?["edits"]?.arrayValue ?? []).map {
                ($0["old_string"]?.stringValue ?? "", $0["new_string"]?.stringValue ?? "")
            }
        default:
            return []
        }
    }

    var writeContent: String? { lowerName == "write" ? inputString("content") : nil }

    struct TodoItem: Equatable { let content: String; let status: String }

    var todos: [TodoItem] {
        guard lowerName == "todowrite" else { return [] }
        return (input?["todos"]?.arrayValue ?? []).compactMap {
            guard let c = $0["content"]?.stringValue ?? $0["activeForm"]?.stringValue else { return nil }
            return TodoItem(content: c, status: $0["status"]?.stringValue ?? "pending")
        }
    }

    /// One-line summary for compact chips.
    var summary: String {
        switch lowerName {
        case "bash": return inputString("description") ?? inputString("command") ?? ""
        case "read", "write", "edit", "multiedit", "notebookedit":
            return filePath.map { ($0 as NSString).lastPathComponent } ?? ""
        case "grep", "glob": return inputString("pattern") ?? ""
        case "webfetch": return inputString("url") ?? ""
        case "websearch": return inputString("query") ?? ""
        case "task", "agent": return inputString("description") ?? ""
        case "todowrite":
            let t = todos
            return t.isEmpty ? "" : "\(t.filter { $0.status == "completed" }.count)/\(t.count) done"
        default: return inputDescription
        }
    }

    var inputDescription: String {
        guard let input else { return "" }
        if let s = input.stringValue { return s }
        guard let dict = input.objectValue else { return "" }
        for key in ["file_path", "pattern", "command", "url", "query", "description", "prompt"] {
            if let v = dict[key]?.stringValue { return v.count > 100 ? String(v.prefix(100)) + "…" : v }
        }
        return dict.keys.sorted().prefix(2).map { "\($0): \(dict[$0]?.displayText.prefix(40) ?? "")" }.joined(separator: ", ")
    }

    /// SF Symbol for the tool.
    var symbol: String {
        switch lowerName {
        case "bash": return "terminal"
        case "read": return "doc.text"
        case "write": return "square.and.pencil"
        case "edit", "multiedit", "notebookedit": return "pencil.line"
        case "glob": return "folder.badge.questionmark"
        case "grep": return "magnifyingglass"
        case "webfetch": return "globe"
        case "websearch": return "safari"
        case "task", "agent": return "person.2"
        case "todowrite": return "checklist"
        case "askuserquestion": return "questionmark.bubble"
        default: return name.hasPrefix("mcp__") ? "puzzlepiece.extension" : "wrench.and.screwdriver"
        }
    }

    var statusIcon: String {
        switch status {
        case "running": return "⏳"
        case "completed": return "✅"
        case "error": return "❌"
        default: return "🔧"
        }
    }
}

// MARK: - Interactive requests

/// A tool call waiting for the user's approval (`permission_request`).
nonisolated struct PermissionRequest: Identifiable, Equatable, Sendable {
    let id: String          // permission_id
    let sessionId: String
    let tool: String
    let description: String
    let details: JSONValue?
    let receivedAt: Date

    static func parse(_ data: [String: Any]) -> PermissionRequest? {
        // Two shapes: top-level fields, or nested under `content`.
        let content = data.dict("content") ?? [:]
        guard let sessionId = data.nonEmptyString("session_id"),
              let id = data.nonEmptyString("permission_id") ?? content.nonEmptyString("permission_id") else {
            return nil
        }
        let tool = data.nonEmptyString("tool") ?? content.nonEmptyString("tool") ?? "Tool"
        let details = data["details"] ?? content["details"]
        return PermissionRequest(
            id: id,
            sessionId: sessionId,
            tool: tool,
            description: data.nonEmptyString("description") ?? content.nonEmptyString("description") ?? "\(tool) wants to run",
            details: details.map { JSONValue(any: $0) },
            receivedAt: Date()
        )
    }

    /// Presents the request as a tool call so the same renderers can show it.
    var asToolUse: ToolUseInfo {
        ToolUseInfo(id: id, name: tool, status: "pending", input: details)
    }
}

/// A question the agent asked with AskUserQuestion (`user_question`).
nonisolated struct UserQuestion: Identifiable, Equatable, Sendable {
    nonisolated struct Option: Equatable, Hashable, Sendable {
        let label: String
        let description: String
    }

    let id: String          // question_id
    let sessionId: String
    let question: String
    let header: String
    let options: [Option]
    let multiSelect: Bool
    let index: Int
    let total: Int
    let requiresUser: Bool
    /// An agent's `start_loop_session` proposal (`kind: "loop_session"`).
    var loopProposal: LoopProposal? = nil
    /// An agent's `start_worker_session` proposal (`kind: "worker_session"`).
    /// Other kinds this app has no card for are shown as a plain question.
    var workerProposal: WorkerProposal? = nil
    /// Option indexes the auto-pick would choose; nil from older servers.
    var recommended: [Int]? = nil
    /// When the server answers it by itself; nil = waits without limit (or held).
    var autoAnswerAt: Date? = nil
    /// "Keep waiting" was pressed for this question.
    var held: Bool = false
    /// The session's name, for banners about other sessions.
    var sessionName: String? = nil

    /// A `user_question_timer` update.
    func withTimer(_ data: [String: Any]) -> UserQuestion {
        var copy = self
        copy.autoAnswerAt = EggifyDate.parse(data.string("auto_answer_at"))
        copy.held = data.bool("held") ?? false
        return copy
    }

    /// What `start_loop_session` asks the user to approve.
    nonisolated struct LoopProposal: Equatable, Sendable {
        /// The card's answers, as the server expects them (`LoopSessionAnswer*`).
        static let start = "Start"
        static let deny = "Deny"

        let goal: String
        let verifyCommand: String
        let workingDirectory: String
        let maxIterations: Int
        let timeoutMinutes: Int
        let model: String
        let provider: String
        let context: String
        let yolo: Bool
        let sandboxed: Bool

        static func parse(_ data: [String: Any]) -> LoopProposal? {
            guard let goal = data.nonEmptyString("goal") else { return nil }
            return LoopProposal(
                goal: goal,
                verifyCommand: data.string("verify_command") ?? "",
                workingDirectory: data.string("working_directory") ?? "",
                maxIterations: data.int("max_iterations") ?? 0,
                timeoutMinutes: data.int("timeout_minutes") ?? 0,
                model: data.string("model") ?? "",
                provider: data.string("provider") ?? "",
                context: data.string("context") ?? "",
                yolo: data.bool("yolo") ?? true,
                sandboxed: data.bool("sandboxed") ?? false
            )
        }
    }

    static func parse(_ data: [String: Any]) -> UserQuestion? {
        guard let sessionId = data.nonEmptyString("session_id"),
              let id = data.nonEmptyString("question_id"),
              let question = data.nonEmptyString("question") else { return nil }
        let options = data.dicts("options").compactMap { o -> Option? in
            guard let label = o.nonEmptyString("label") else { return nil }
            return Option(label: label, description: o.string("description") ?? "")
        }
        return UserQuestion(
            id: id,
            sessionId: sessionId,
            question: question,
            header: data.string("header") ?? "",
            options: options,
            multiSelect: data.bool("multi_select") ?? false,
            index: data.int("question_index") ?? 1,
            total: data.int("question_total") ?? 1,
            requiresUser: data.bool("requires_user") ?? false,
            loopProposal: data.string("kind") == "loop_session" ? data.dict("proposal").flatMap(LoopProposal.parse) : nil,
            workerProposal: data.string("kind") == "worker_session" ? data.dict("proposal").flatMap(WorkerProposal.parse) : nil,
            recommended: (data["recommended"] as? [Any])?.compactMap { $0 as? Int },
            autoAnswerAt: EggifyDate.parse(data.string("auto_answer_at")),
            held: data.bool("held") ?? false,
            sessionName: data.nonEmptyString("session_name")
        )
    }
}

// MARK: - Always-allow rules

/// The pattern of an "Allow similar" rule, as the web app builds it
/// (`utils/agents/alwaysAllowPattern.ts`). Always scoped: the server refuses
/// rules that cover everything.
/// - Bash: the command's first word (skipping VAR=value), e.g. "git". The
///   server never auto-approves compound commands, so "git" does not cover
///   "git status; curl x | sh".
/// - Read/Write/Edit: the directory of the file.
/// - Grep/Glob: read-only, any path.
nonisolated enum AlwaysAllowPattern {
    static func make(tool: String, details: JSONValue?) -> [String: String]? {
        let input = details?.objectValue ?? [:]
        switch tool {
        case "Bash":
            return bashCommandPrefix(input["command"]?.stringValue).map { ["command_prefix": $0] }
        case "Read", "Write", "Edit":
            return parentDirectory(input["file_path"]?.stringValue).map { ["directory_path": $0] }
        case "Grep", "Glob":
            return ["path_pattern": "*"]
        default:
            return nil
        }
    }

    static func bashCommandPrefix(_ command: String?) -> String? {
        guard let command else { return nil }
        let words = command.split(whereSeparator: \.isWhitespace).map(String.init)
        return words.first { word in
            // VAR=value assignments come before the program.
            guard let eq = word.firstIndex(of: "="), eq != word.startIndex else { return true }
            let name = word[..<eq]
            let valid = name.first.map { $0 == "_" || $0.isLetter } == true
                && name.allSatisfy { $0 == "_" || $0.isLetter || $0.isNumber }
            return !valid
        }
    }

    static func parentDirectory(_ path: String?) -> String? {
        guard let path, path.hasPrefix("/") else { return nil }
        var trimmed = path
        while trimmed.hasSuffix("/") { trimmed.removeLast() }
        guard let slash = trimmed.lastIndex(of: "/"), slash != trimmed.startIndex else { return nil }
        return String(trimmed[..<slash])
    }

    /// What "Allow similar" covers, for the button label.
    static func summary(tool: String, pattern: [String: String]) -> String {
        if let prefix = pattern["command_prefix"] { return "all `\(prefix)` commands" }
        if let dir = pattern["directory_path"] {
            let name = (dir as NSString).lastPathComponent
            return "\(tool) in \(name.isEmpty ? dir : name)/"
        }
        if pattern["path_pattern"] != nil { return "all \(tool) searches" }
        return "similar \(tool) requests"
    }
}
