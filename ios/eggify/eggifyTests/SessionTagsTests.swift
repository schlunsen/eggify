//
//  SessionTagsTests.swift
//  eggifyTests
//
//  Tag normalising and suggestions, mirroring the web's `utils/sessionTags.ts`.
//

import Foundation
import Testing
@testable import eggify

@Suite("Session tags")
struct SessionTagsTests {
    @Test func splitTrimsAndDropsRepeats() {
        #expect(SessionTags.split("bug,  Deploy  now , BUG,,") == ["bug", "Deploy now"])
    }

    @Test func normalizeKeepsServerLimits() {
        let many = (1...10).map { "t\($0)" }
        #expect(SessionTags.normalize(many).count == SessionTags.maxTags)
        let long = String(repeating: "x", count: 60)
        #expect(SessionTags.normalize([long]).first?.count == SessionTags.maxLength)
    }

    @Test func knownIsMostUsedFirst() {
        let known = SessionTags.known([["ios", "bug"], ["Bug"], ["deploy", "bug"], ["ios"]])
        #expect(known == ["bug", "ios", "deploy"])
    }

    @Test func suggestPrefersPrefixAndSkipsTagged() {
        let known = ["debug", "bugfix", "deploy", "bug"]
        #expect(SessionTags.suggest("bug", known: known, exclude: ["BUG"]) == ["bugfix", "debug"])
        #expect(SessionTags.suggest("", known: known, exclude: ["deploy"], limit: 2) == ["debug", "bugfix"])
    }
}
