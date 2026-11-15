//
//  TagTests.swift
//  eggifyTests
//
//  Session tags on the Agents list: the user's own tags from the session
//  list, then the AI topic tags, without repeats.
//

import Foundation
import Testing
@testable import eggify

@Suite("Session tags")
@MainActor
struct TagTests {
    @Test func parsesTagsAndKeepsThemOnUpdatesWithout() throws {
        let session = try #require(AgentSession.parse([
            "id": "s1", "status": "idle", "tags": ["refactor", " ", "n0 release"],
        ]))
        #expect(session.tags == ["refactor", "n0 release"])
        #expect(session.merged(with: ["status": "processing"]).tags == ["refactor", "n0 release"])
        #expect(session.merged(with: ["tags": []]).tags.isEmpty)
        #expect(AgentSession.parse(["id": "s2", "status": "idle"])?.tags == [])
    }

    @Test func ownTagsFirstThenTopicsWithoutRepeats() {
        let tags = SessionListModel.rowTags(own: ["Release", "backend"], topics: ["release", "bug-fix", "Backend", "bug-fix"])
        #expect(tags.own == ["Release", "backend"])
        #expect(tags.topics == ["bug-fix"])
    }
}
