//
//  SessionSkillsTests.swift
//  eggifyTests
//
//  A running session's skills: the server's list, the two sections, the
//  added list sent on add/remove, Use's slash command in the draft, search
//  and the note about when a change reaches the agent.
//

import Foundation
import Testing
@testable import eggify

@Suite("Session skills")
@MainActor
struct SessionSkillsTests {
    private let json: [String: Any] = [
        "session_id": "s1",
        "runtime": "claude-sdk",
        "skills": [
            ["id": "k1", "name": "review", "description": "Review the diff", "scope": "personal",
             "user_invocable": true, "reach": "everywhere", "added": false],
            ["id": "k2", "name": "deploy", "scope": "project", "argument_hint": "<env>",
             "user_invocable": true, "reach": "folder", "added": false],
            ["id": "k3", "name": "migrate", "description": "", "scope": "project",
             "user_invocable": false, "reach": "added", "added": true],
            ["id": "k4", "name": "release-notes", "description": "Write release notes", "scope": "project",
             "user_invocable": true, "reach": "", "added": false],
            ["name": "no id"],
        ],
        "restart_pending": true,
        "subagents_running": 2,
    ]

    private func skill(_ name: String, invocable: Bool = true) -> SessionSkill {
        SessionSkill.parse(["id": name, "name": name, "user_invocable": invocable, "reach": "everywhere"])!
    }

    // MARK: Parsing

    @Test func parsesTheServerList() {
        let list = SessionSkills.parse(json)
        #expect(list.sessionID == "s1")
        #expect(list.runtime == .claude)
        #expect(list.restartPending)
        #expect(list.subagentsRunning == 2)
        #expect(list.skills.map(\.id) == ["k1", "k2", "k3", "k4"])

        let deploy = list.skills[1]
        #expect(deploy.reach == .folder)
        #expect(deploy.argumentHint == "<env>")
        #expect(deploy.description == nil)
        #expect(deploy.userInvocable)

        let migrate = list.skills[2]
        #expect(migrate.description == nil)
        #expect(migrate.added)
        #expect(!migrate.userInvocable)

        #expect(list.skills[3].reach == .none)
    }

    @Test func toleratesMissingAndUnknownFields() {
        let list = SessionSkills.parse(["skills": [["id": "a", "name": "a", "reach": "somewhere"]]])
        #expect(list.runtime == .unknown)
        #expect(!list.restartPending)
        #expect(list.subagentsRunning == 0)
        #expect(list.skills.first?.reach == SessionSkill.Reach.none)
        #expect(list.skills.first?.userInvocable == false)
        #expect(SessionSkills.parse(["runtime": "codex"]).runtime == .codex)
        #expect(SessionSkills.parse(["runtime": "direct"]).runtime == .direct)
    }

    // MARK: Sections

    @Test func groupsIntoThisSessionAndOtherProjects() {
        let list = SessionSkills.parse(json)
        #expect(list.active.map(\.id) == ["k1", "k2", "k3"])
        #expect(list.others.map(\.id) == ["k4"])
        #expect(list.active.map(\.reachLabel) == ["Everywhere", "This project", "Added"])
        #expect(list.others.first?.reachLabel == nil)
    }

    // MARK: Added list

    @Test func addingSendsTheCurrentAddedIdsPlusOne() {
        let list = SessionSkills.parse(json)
        #expect(list.addedIDs(adding: "k4") == ["k3", "k4"])
        // Already added: unchanged.
        #expect(list.addedIDs(adding: "k3") == ["k3"])
    }

    @Test func removingSendsTheOthers() {
        let list = SessionSkills.parse(json)
        #expect(list.addedIDs(removing: "k3") == [])
        #expect(list.addedIDs(removing: "k1") == ["k3"])
    }

    // MARK: Use

    @Test func useSetsTheSlashCommandOnAnEmptyDraft() {
        #expect(SessionSkills.draft(using: skill("review"), in: "") == "/review ")
        #expect(SessionSkills.draft(using: skill("review"), in: "  \n") == "/review ")
    }

    @Test func usePutsTheSlashCommandInFrontOfADraft() {
        #expect(SessionSkills.draft(using: skill("review"), in: "the auth change") == "/review the auth change")
        #expect(SessionSkills.draft(using: skill("review"), in: "  focus on tests") == "/review focus on tests")
    }

    @Test func useDoesNotRepeatTheCommand() {
        #expect(SessionSkills.draft(using: skill("review"), in: "/review the auth change") == "/review the auth change")
        // Another skill's command is kept as text.
        #expect(SessionSkills.draft(using: skill("deploy"), in: "/review x") == "/deploy /review x")
    }

    // MARK: Search

    @Test func searchMatchesNameAndDescription() {
        let list = SessionSkills.parse(json)
        #expect(SessionSkills.filter(list.skills, query: "").count == 4)
        #expect(SessionSkills.filter(list.skills, query: "DEPLOY").map(\.id) == ["k2"])
        #expect(SessionSkills.filter(list.skills, query: "release").map(\.id) == ["k4"])
        #expect(SessionSkills.filter(list.skills, query: "diff").map(\.id) == ["k1"])
        #expect(SessionSkills.filter(list.skills, query: "nothing").isEmpty)
    }

    // MARK: Note

    @Test func claudeNoteWhenARestartIsPending() {
        var list = SessionSkills.parse(json)
        #expect(list.note == "Takes effect with your next message. The agent restarts then, which stops 2 running subagents.")
        list.subagentsRunning = 1
        #expect(list.note == "Takes effect with your next message. The agent restarts then, which stops 1 running subagent.")
        list.subagentsRunning = 0
        #expect(list.note == "Takes effect with your next message.")
        list.restartPending = false
        #expect(list.note == nil)
    }

    @Test func codexAndDirectNote() {
        let text = "This agent sees its skill list when its conversation starts; added skills reach it through its skill tools."
        #expect(SessionSkills.parse(["runtime": "codex"]).note == text)
        #expect(SessionSkills.parse(["runtime": "direct", "restart_pending": true]).note == text)
        #expect(SessionSkills.parse([:]).note == nil)
    }
}
