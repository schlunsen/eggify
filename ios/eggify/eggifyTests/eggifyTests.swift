//
//  eggifyTests.swift
//  eggifyTests
//
//  Parsing and presentation logic. The server sends messages in two shapes
//  (REST rows and live WebSocket events); both must end up as the same model.
//

import Foundation
import Testing
@testable import eggify

// MARK: - Helpers

private func json(_ text: String) -> [String: Any] {
    (try? JSONSerialization.jsonObject(with: Data(text.utf8))) as? [String: Any] ?? [:]
}

// MARK: - Messages

@Suite("AgentMessage parsing")
struct AgentMessageParsingTests {
    @Test func restRowWithToolUsesAndSequence() throws {
        let row = json("""
        {"id":"m1","role":"assistant","content":"Done.","created_at":"2026-10-03T10:00:00.123456789Z",
         "thinking_content":"hmm","sequence":7,"model_name":"opus",
         "tool_uses":[{"id":"t1","name":"Bash","input":{"command":"ls -la","description":"List"}}]}
        """)
        let message = try #require(AgentMessage.parse(row))
        #expect(message.role == "assistant")
        #expect(message.content.textContent == "Done.")
        #expect(message.thinking == "hmm")
        #expect(message.sequence == 7)
        #expect(message.modelName == "opus")
        #expect(message.createdDate != nil, "nanosecond timestamps from Go must parse")
        let tool = try #require(message.getTools().first)
        #expect(tool.name == "Bash")
        #expect(tool.bashCommand == "ls -la")
        #expect(tool.summary == "List")
    }

    @Test func liveAssistantEvent() throws {
        let event = json("""
        {"type":"agent_message","id":"e1","session_id":"s1","role":"assistant",
         "content":{"type":"assistant","text":["Hello","world"],"thinking":["step 1"],
                    "tools":[{"id":"t1","name":"Edit","input":{"file_path":"/a/b.swift","old_string":"x","new_string":"y"},"status":"running"}]}}
        """)
        let message = try #require(AgentMessage.parse(event))
        #expect(message.content.textContent == "Hello\n\nworld")
        #expect(message.thinking == "step 1")
        let tool = try #require(message.getTools().first)
        #expect(tool.summary == "b.swift")
        #expect(tool.edits.count == 1)
        #expect(tool.edits.first?.old == "x")
        #expect(tool.edits.first?.new == "y")
    }

    @Test func userPromptWithImagesStoredAsJSON() throws {
        let content = #"[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"iVBORw0KGgo="}},{"type":"text","text":"What is this?"}]"#
        let row: [String: Any] = ["id": "u1", "role": "user", "content": content]
        let message = try #require(AgentMessage.parse(row))
        #expect(message.content.textContent == "What is this?")
        #expect(message.content.imageBlocks.count == 1)
        #expect(message.content.imageBlocks.first?.mediaType == "image/png")
    }

    @Test func plainTextStartingWithBracketStaysText() throws {
        let message = try #require(AgentMessage.parse(["id": "u2", "role": "user", "content": "[draft] fix the bug"]))
        #expect(message.content == .string("[draft] fix the bug"))
    }

    @Test func resultBecomesTurnSummary() throws {
        let event = json("""
        {"id":"r1","role":"result","content":{"type":"result","success":true,"num_turns":3,"duration_ms":12500,"cost_usd":0.0421}}
        """)
        let message = try #require(AgentMessage.parse(event))
        #expect(message.role == "result")
        #expect(message.turnSummary?.durationMs == 12500)
        #expect(message.turnSummary?.costUSD == 0.0421)
        #expect(!message.isEmptyForDisplay)
    }

    @Test func systemChatterIsDropped() {
        let event = json(#"{"id":"s1","role":"system","content":{"type":"system","subtype":"init","data":{}}}"#)
        #expect(AgentMessage.parse(event) == nil)
    }

    @Test func storedSystemNoticeIsKept() throws {
        let message = try #require(AgentMessage.parse(["id": "n1", "role": "system", "content": "⚠️ Session interrupted by user"]))
        #expect(message.isSystemNotice)
        #expect(!message.isEmptyForDisplay)
    }

    @Test func toolResultImagesAreExtracted() throws {
        let event = json("""
        {"id":"i1","role":"user","content":{"type":"user","content":"","tool_results":[
          {"tool_use_id":"t1","image_preview":{"type":"image","source":{"type":"base64","media_type":"image/jpeg","data":"/9j/4AAQ"}}}]}}
        """)
        let message = try #require(AgentMessage.parse(event))
        #expect(message.toolImages.count == 1)
        #expect(!message.isEmptyForDisplay)
    }

    @Test func messageWithoutIdIsRejected() {
        #expect(AgentMessage.parse(["role": "assistant", "content": "x"]) == nil)
    }
}

// MARK: - Sessions

@Suite("AgentSession parsing")
struct AgentSessionParsingTests {
    @Test func fullSessionFromREST() throws {
        let data = json("""
        {"id":"abc12345-0000","status":"processing","cost_usd":1.25,"message_count":12,"num_turns":4,
         "pending_permissions":1,"pending_questions":0,"model_name":"claude-sonnet-4-5-20250929",
         "created_at":"2026-10-01T09:00:00Z","updated_at":"2026-10-03T09:00:00Z",
         "options":{"working_directory":"/Users/me/code/eggify","agent_name":"Refactor bot",
                    "dangerously_skip_permissions":true,"sandboxed":true,"effort_level":"high","project_id":"p1"},
         "selected_avatar":{"id":19,"theme_id":1,"name":"Bebe","color":"#FF69B4","created_at":"2024-01-01T00:00:00Z"}}
        """)
        let session = try #require(AgentSession.parse(data))
        #expect(session.cost == 1.25)
        #expect(session.working_directory == "/Users/me/code/eggify")
        #expect(session.displayTitle == "Refactor bot")
        #expect(session.yolo)
        #expect(session.sandboxed)
        #expect(session.effort_level == "high")
        #expect(session.project_id == "p1")
        #expect(session.needsAttention)
        #expect(session.selected_avatar?.name == "Bebe")
        #expect(session.selected_avatar_id == 19)
    }

    @Test func titleFallsBackToFolderName() throws {
        let session = try #require(AgentSession.parse(json(#"{"id":"s","options":{"working_directory":"/tmp/project-x"}}"#)))
        #expect(session.displayTitle == "project-x")
    }

    @Test func partialUpdateKeepsOtherFields() throws {
        let original = try #require(AgentSession.parse(json("""
        {"id":"s","status":"idle","cost_usd":2.0,"created_at":"2026-10-01T09:00:00Z",
         "options":{"agent_name":"Bot","sandboxed":true},
         "selected_avatar":{"id":3,"theme_id":1,"name":"Kenny","created_at":"x"}}
        """)))
        let updated = original.merged(with: ["status": "processing", "git_branch": "main"])
        #expect(updated.status == "processing")
        #expect(updated.git_branch == "main")
        #expect(updated.agent_name == "Bot")
        #expect(updated.sandboxed)
        #expect(updated.cost == 2.0)
        #expect(updated.selected_avatar?.name == "Kenny")
        #expect(updated.created_at == original.created_at)
    }
}

// MARK: - Interactive requests

@Suite("Permission requests and questions")
struct InteractiveRequestTests {
    @Test func permissionRequestTopLevel() throws {
        let data = json("""
        {"type":"permission_request","session_id":"s1","permission_id":"p1","tool":"Bash","action":"use_tool",
         "details":{"command":"rm -rf build"},"description":"Run: rm -rf build"}
        """)
        let request = try #require(PermissionRequest.parse(data))
        #expect(request.id == "p1")
        #expect(request.tool == "Bash")
        #expect(request.asToolUse.bashCommand == "rm -rf build")
        #expect(request.description == "Run: rm -rf build")
    }

    @Test func permissionRequestNestedInContent() throws {
        let data = json("""
        {"type":"permission_request","session_id":"s1",
         "content":{"type":"permission_request","permission_id":"p2","tool":"Write","details":{"file_path":"/x"}}}
        """)
        let request = try #require(PermissionRequest.parse(data))
        #expect(request.id == "p2")
        #expect(request.tool == "Write")
    }

    @Test func userQuestion() throws {
        let data = json("""
        {"type":"user_question","session_id":"s1","question_id":"q1","question":"Which database?","header":"DB",
         "options":[{"label":"Postgres","description":"Relational"},{"label":"SQLite","description":""}],
         "multi_select":true,"question_index":2,"question_total":3,"requires_user":true}
        """)
        let question = try #require(UserQuestion.parse(data))
        #expect(question.options.map(\.label) == ["Postgres", "SQLite"])
        #expect(question.multiSelect)
        #expect(question.index == 2)
        #expect(question.total == 3)
        #expect(question.requiresUser)
    }
}

// MARK: - Chat grouping

@Suite("Chat items")
struct ChatItemTests {
    private func toolStep(_ id: String) -> AgentMessage {
        AgentMessage(id: id, role: "assistant", content: .string(""), created_at: nil, thinking: nil,
                     tools: [ToolUseInfo(id: "t-\(id)", name: "Read", status: nil, input: .object(["file_path": .string("/f")]))])
    }

    private func text(_ id: String, _ role: String = "assistant") -> AgentMessage {
        AgentMessage(id: id, role: role, content: .string("hi"), created_at: nil, thinking: nil, tools: nil)
    }

    @Test func foldsLongRunsOfToolOnlySteps() {
        let items = ChatItem.build(from: [text("u", "user"), toolStep("a"), toolStep("b"), toolStep("c"), text("r")])
        #expect(items.count == 3)
        if case .activity(_, let messages) = items[1] {
            #expect(messages.map(\.id) == ["a", "b", "c"])
        } else {
            Issue.record("expected an activity group")
        }
    }

    @Test func keepsShortRunsInline() {
        let items = ChatItem.build(from: [toolStep("a"), toolStep("b"), text("r")])
        #expect(items.count == 3)
    }

    @Test func dropsEmptyMessages() {
        let empty = AgentMessage(id: "e", role: "assistant", content: .string("  "), created_at: nil, thinking: nil, tools: nil)
        #expect(ChatItem.build(from: [empty, text("r")]).count == 1)
    }
}

// MARK: - Markdown

@Suite("Markdown parser")
struct MarkdownParserTests {
    @Test func codeFenceKeepsContentVerbatim() {
        let blocks = MarkdownParser.parse("Intro\n\n```swift\nlet x = 1\n# not a heading\n```\nAfter")
        #expect(blocks == [
            .paragraph("Intro"),
            .code(language: "swift", code: "let x = 1\n# not a heading"),
            .paragraph("After")
        ])
    }

    @Test func unterminatedFenceRunsToEnd() {
        #expect(MarkdownParser.parse("```\nstill streaming") == [.code(language: nil, code: "still streaming")])
    }

    @Test func headingsListsAndTasks() {
        let blocks = MarkdownParser.parse("## Plan\n- one\n  - nested\n- [x] done\n- [ ] todo\n1. first\n2) second")
        #expect(blocks.first == .heading(level: 2, text: "Plan"))
        guard case .list(let items) = blocks[1] else {
            Issue.record("expected a list"); return
        }
        #expect(items.count == 6)
        #expect(items[1].level == 1)
        #expect(items[2].marker == .task(done: true))
        #expect(items[3].marker == .task(done: false))
        #expect(items[5].marker == .number(2))
    }

    @Test func tableQuoteAndRule() {
        let blocks = MarkdownParser.parse("| a | b |\n|---|:-:|\n| 1 | 2 |\n\n> quoted\n> more\n\n---")
        #expect(blocks == [
            .table(header: ["a", "b"], rows: [["1", "2"]]),
            .quote("quoted\nmore"),
            .rule
        ])
    }

    @Test func hashWithoutSpaceIsNotAHeading() {
        #expect(MarkdownParser.parse("#hashtag") == [.paragraph("#hashtag")])
    }
}

// MARK: - Diff

@Suite("Line diff")
struct LineDiffTests {
    @Test func marksChangedLines() {
        let diff = LineDiff.diff(old: "a\nb\nc", new: "a\nB\nc\nd")
        #expect(diff.map(\.kind) == [.same, .removed, .added, .same, .added])
    }

    @Test func emptyOldIsAllAdded() {
        #expect(LineDiff.diff(old: "", new: "x\ny").allSatisfy { $0.kind == .added })
    }
}

// MARK: - URLs

@Suite("URLs")
struct URLTests {
    @Test func normalizesHostURLs() {
        #expect(Host.normalizeURL("localhost") == "https://localhost:3333")
        #expect(Host.normalizeURL(" https://eggify.example.com/ ") == "https://eggify.example.com")
        #expect(Host.normalizeURL("http://127.0.0.1:4000") == "http://127.0.0.1:4000")
    }

    @MainActor
    @Test func webSocketURLKeepsPortAndPath() {
        #expect(WebSocketService.webSocketURL(for: URL(string: "https://localhost:3333")!)?.absoluteString
                == "wss://localhost:3333/agent/ws")
        #expect(WebSocketService.webSocketURL(for: URL(string: "http://10.0.0.5:3333/")!)?.absoluteString
                == "ws://10.0.0.5:3333/agent/ws")
        #expect(WebSocketService.webSocketURL(for: URL(string: "https://abc.ngrok.app/eggify")!)?.absoluteString
                == "wss://abc.ngrok.app/eggify/agent/ws")
    }
}

// MARK: - JSON helpers

@Suite("JSON helpers")
struct JSONHelperTests {
    @Test func booleansAreNotNumbers() {
        let value = JSONValue(any: json(#"{"a":true,"b":1}"#))
        #expect(value["a"] == .bool(true))
        #expect(value["b"] == .number(1))
    }

    @Test func looseAccessors() {
        let dict = json(#"{"n":"42","d":1.5,"b":true,"s":["x","y"]}"#)
        #expect(dict.int("n") == 42)
        #expect(dict.double("d") == 1.5)
        #expect(dict.bool("b") == true)
        #expect(dict.strings("s") == ["x", "y"])
    }

    @Test func modelNames() {
        #expect(ModelNames.short("claude-sonnet-4-5-20250929") == "sonnet-4-5")
        #expect(ModelNames.short("gpt-6-astra") == "gpt-6-astra")
    }
}
