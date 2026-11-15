//
//  ComposerTests.swift
//  eggifyTests
//
//  The composer empties on send and stays empty, and keeps unsent drafts.
//

import Foundation
import Testing
import UIKit
import UniformTypeIdentifiers
@testable import eggify

@Suite("Composer")
@MainActor
struct ComposerTests {
    @Test func takeEmptiesTheComposer() throws {
        let model = ComposerModel()
        model.text = "Fix the login bug"
        let sent = try #require(model.take())
        #expect(sent.text == "Fix the login bug")
        #expect(model.text.isEmpty)
        #expect(!model.canSend)
    }

    @Test func nothingToTakeWhenBlank() {
        let model = ComposerModel()
        model.text = "  \n "
        #expect(model.take() == nil)
    }

    /// The keyboard commits a pending autocorrect word just after the field
    /// was cleared; it must not stay in the composer.
    @Test func autocorrectEchoAfterSendIsCleared() async throws {
        let model = ComposerModel()
        model.text = "Run the tests"
        _ = try #require(model.take())
        model.text = "tests"   // what UIKit writes back
        try await Task.sleep(for: .milliseconds(250))
        #expect(model.text.isEmpty)
    }

    @Test func newTypingAfterSendIsKept() async throws {
        let model = ComposerModel()
        model.text = "Run the tests"
        _ = try #require(model.take())
        model.text = "and then deploy"
        try await Task.sleep(for: .milliseconds(250))
        #expect(model.text == "and then deploy")
    }

    @Test func echoDetection() {
        #expect(ComposerModel.isEcho("tests", of: "Run the tests"))
        #expect(ComposerModel.isEcho(" Run the tests ", of: "Run the tests"))
        #expect(!ComposerModel.isEcho("", of: "Run the tests"))
        #expect(!ComposerModel.isEcho("deploy", of: "Run the tests"))
    }

    @Test func draftSurvivesReopeningTheChat() throws {
        let key = "draft-\(UUID().uuidString)"
        ComposerModel(draftKey: key).text = "half a thought"
        #expect(ComposerModel(draftKey: key).text == "half a thought")

        let reopened = ComposerModel(draftKey: key)
        _ = try #require(reopened.take())
        #expect(ComposerModel(draftKey: key).text.isEmpty)
    }

    /// A pasted image becomes an attachment, like a picked photo, within the limit.
    @Test func pastedImagesBecomeAttachments() async throws {
        let png = try #require(UIGraphicsImageRenderer(size: CGSize(width: 40, height: 30)).image { ctx in
            UIColor.systemPink.setFill()
            ctx.fill(CGRect(x: 0, y: 0, width: 40, height: 30))
        }.pngData())
        func provider() -> NSItemProvider {
            NSItemProvider(item: png as NSData, typeIdentifier: UTType.png.identifier)
        }
        let model = ComposerModel()
        model.addPastedImages((0..<6).map { _ in provider() })
        for _ in 0..<100 where model.attachments.count < ComposerModel.maxAttachments || model.preparingCount > 0 {
            try await Task.sleep(for: .milliseconds(20))
        }
        #expect(model.attachments.count == ComposerModel.maxAttachments)
        #expect(model.attachments.allSatisfy { $0.mediaType == "image/jpeg" && !$0.base64.isEmpty })
        #expect(model.canSend)
        model.addPastedImages([provider()])   // no slot left
        #expect(model.preparingCount == 0)
    }
}
