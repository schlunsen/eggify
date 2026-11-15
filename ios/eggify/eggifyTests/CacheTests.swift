//
//  CacheTests.swift
//  eggifyTests
//
//  Instant launch cache, persisted drafts, and Select Text code blocks.
//

import Foundation
import Testing
@testable import eggify

@Suite("Session cache")
struct SessionCacheTests {
    private func tempCache() -> SessionCache {
        SessionCache(root: FileManager.default.temporaryDirectory.appendingPathComponent("cache-\(UUID().uuidString)"))
    }

    @Test func roundTripsSessionsAndMessagesPerHost() async {
        let cache = tempCache()
        cache.storeSessions([["id": "s1", "status": "idle"]], host: "host-a")
        cache.storeMessages([["id": "m1", "role": "user", "content": "hi"]], session: "s1", host: "host-a")
        await cache.flush()
        #expect(await cache.sessions(host: "host-a").count == 1)
        #expect(await cache.messages(session: "s1", host: "host-a").first?["content"] as? String == "hi")
        #expect(await cache.sessions(host: "host-b").isEmpty, "other servers don't see it")
    }

    @Test func clearForgetsAServer() async {
        let cache = tempCache()
        cache.storeSessions([["id": "s1"]], host: "host-a")
        await cache.flush()
        cache.clear(host: "host-a")
        await cache.flush()
        #expect(await cache.sessions(host: "host-a").isEmpty)
    }

    @Test func pathsCannotEscape() async {
        let cache = tempCache()
        cache.storeMessages([["id": "x"]], session: "../../etc/passwd", host: "../h")
        await cache.flush()
        #expect(await cache.messages(session: "etcpasswd", host: "h").count == 1)
    }

    @Test func keepsOnlyRecentChats() async {
        let cache = tempCache()
        for i in 0..<(SessionCache.maxChats + 5) {
            cache.storeMessages([["id": "m\(i)"]], session: "s\(i)", host: "h")
        }
        await cache.flush()
        var kept = 0
        for i in 0..<(SessionCache.maxChats + 5) where !(await cache.messages(session: "s\(i)", host: "h")).isEmpty {
            kept += 1
        }
        #expect(kept == SessionCache.maxChats)
    }
}

@Suite("Drafts")
@MainActor
struct DraftTests {
    @Test func draftsPersistAndClear() throws {
        let defaults = try #require(UserDefaults(suiteName: "drafts-\(UUID().uuidString)"))
        let saved = ComposerModel.defaults
        ComposerModel.defaults = defaults
        defer { ComposerModel.defaults = saved }

        ComposerModel(draftKey: "s1").text = "half a thought"
        #expect(ComposerModel(draftKey: "s1").text == "half a thought")
        ComposerModel.clearDrafts()
        #expect(ComposerModel(draftKey: "s1").text.isEmpty)
    }
}

@Suite("Select Text")
struct SelectTextTests {
    @Test func codeBlocksAreSeparateSegments() {
        let parts = SelectableText.segments("Intro\n```go\nfunc a() {}\n```\nOutro")
        #expect(parts.map(\.isCode) == [false, true, false])
        #expect(parts[1].text == "func a() {}")
    }
}
