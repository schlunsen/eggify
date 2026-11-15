//
//  SubagentTests.swift
//  eggifyTests
//
//  Subagents from REST and live events, and "Always allow" rule patterns
//  (same cases as the web app's alwaysAllowPattern.test.ts).
//

import Foundation
import Testing
@testable import eggify

@Suite("Subagents")
@MainActor
struct SubagentTests {
    private func agentJSON(_ id: String, status: String = "running") -> [String: Any] {
        ["agent_id": id, "subagent_type": "Explore", "description": "Find the bug",
         "status": status, "progress": 0, "recent_output": ["TOOL:Read: main.go", "Looking at main.go"],
         "created_at": "2026-10-03T10:00:00Z"]
    }

    @Test func parsesRESTShape() throws {
        let agent = try #require(Subagent.parse(agentJSON("a1")))
        #expect(agent.type == "Explore")
        #expect(agent.title == "Find the bug")
        #expect(agent.status == .running)
        #expect(agent.output.count == 2)
        #expect(agent.createdAt != nil)
    }

    @Test func parsesModel() throws {
        var json = agentJSON("m1")
        json["model"] = "claude-haiku-4-5-20251001"
        json["model_source"] = "actual"
        let agent = try #require(Subagent.parse(json))
        #expect(agent.modelLabel == "Haiku 4.5")
        #expect(!agent.modelRequested)
        json["model"] = "sonnet"
        json["model_source"] = "requested"
        let requested = try #require(Subagent.parse(json))
        #expect(requested.modelLabel == "Sonnet")
        #expect(requested.modelRequested)
        #expect(try #require(Subagent.parse(agentJSON("m2"))).modelLabel == nil)
    }

    @Test func modelLabelsMatchTheWeb() {
        #expect(Subagent.modelLabel("claude-sonnet-5-5") == "Sonnet 5.5")
        #expect(Subagent.modelLabel("claude-opus-5-5") == "Opus 5.5")
        #expect(Subagent.modelLabel("claude-mythos-5-2") == "Mythos 5.2")
        #expect(Subagent.modelLabel("claude-3-5-sonnet-20241022") == "Sonnet 3.5")
        #expect(Subagent.modelLabel("gpt-6-astra") == "gpt-6-astra")
    }

    @Test func outputLines() {
        let tool = SubagentLine("TOOL:Bash: go test ./...")
        #expect(tool.tool == "Bash")
        #expect(tool.text == "go test ./...")
        let text = SubagentLine("All tests pass")
        #expect(text.tool == nil)
        #expect(text.text == "All tests pass")
    }

    @Test func liveEventsUpdateTheList() throws {
        let store = SessionsWebSocketViewModel.shared
        let session = "test-\(UUID().uuidString)"

        store.handleServerEvent(type: "background_agent_started",
                                payload: ["session_id": session, "agent": agentJSON("a1")])
        store.handleServerEvent(type: "background_agent_output",
                                payload: ["session_id": session, "agent_id": "a1",
                                          "output": "b", "lines": ["a", "b"]])
        var agent = try #require(store.subagents[session]?.first)
        #expect(agent.output.suffix(2) == ["a", "b"])
        #expect(agent.status.isActive)

        store.handleServerEvent(type: "background_agent_completed",
                                payload: ["session_id": session, "agent_id": "a1"])
        agent = try #require(store.subagents[session]?.first)
        #expect(agent.status == .completed)
        #expect(agent.completedAt != nil)

        store.handleServerEvent(type: "background_agent_started",
                                payload: ["session_id": session, "agent": agentJSON("a2")])
        store.handleServerEvent(type: "background_agent_failed",
                                payload: ["session_id": session, "agent_id": "a2", "error_message": "boom"])
        let failed = try #require(store.subagents[session]?.first { $0.id == "a2" })
        #expect(failed.status == .failed)
        #expect(failed.errorMessage == "boom")
    }

    @Test func outputIsCapped() {
        var agent = Subagent.parse(agentJSON("a1"))!
        agent.append((0..<500).map { "line \($0)" })
        #expect(agent.output.count == Subagent.maxLines)
        #expect(agent.output.last == "line 499")
    }
}

@Suite("Always allow")
struct AlwaysAllowPatternTests {
    private func details(_ dict: [String: Any]) -> JSONValue { JSONValue(any: dict) }

    @Test func bashUsesFirstWord() {
        #expect(AlwaysAllowPattern.make(tool: "Bash", details: details(["command": "git status --short"])) == ["command_prefix": "git"])
        #expect(AlwaysAllowPattern.make(tool: "Bash", details: details(["command": "  npm test"])) == ["command_prefix": "npm"])
    }

    @Test func skipsEnvAssignments() {
        #expect(AlwaysAllowPattern.bashCommandPrefix("FOO=1 BAR=2 go test ./...") == "go")
    }

    @Test func neverAWildcardBashPrefix() {
        #expect(AlwaysAllowPattern.make(tool: "Bash", details: details([:])) == nil)
        #expect(AlwaysAllowPattern.make(tool: "Bash", details: details(["command": "   "])) == nil)
        #expect(AlwaysAllowPattern.make(tool: "Bash", details: details(["command": "FOO=1"])) == nil)
    }

    @Test func fileRulesUseTheDirectory() {
        #expect(AlwaysAllowPattern.make(tool: "Read", details: details(["file_path": "/home/me/project/src/main.go"]))
                == ["directory_path": "/home/me/project/src"])
        #expect(AlwaysAllowPattern.make(tool: "Edit", details: details(["file_path": "/home/me/project/README.md"]))
                == ["directory_path": "/home/me/project"])
    }

    @Test func neverARootOrRelativeScope() {
        #expect(AlwaysAllowPattern.make(tool: "Write", details: details([:])) == nil)
        #expect(AlwaysAllowPattern.make(tool: "Write", details: details(["file_path": "relative.txt"])) == nil)
        #expect(AlwaysAllowPattern.parentDirectory("/etc") == nil)
        #expect(AlwaysAllowPattern.parentDirectory("/") == nil)
    }

    @Test func searchesAreReadOnlyWildcards() {
        #expect(AlwaysAllowPattern.make(tool: "Grep", details: details(["pattern": "x"])) == ["path_pattern": "*"])
    }

    @Test func otherToolsHaveNoSimilarRule() {
        #expect(AlwaysAllowPattern.make(tool: "WebFetch", details: details(["url": "https://x"])) == nil)
    }
}
