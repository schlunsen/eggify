//
//  TextTests.swift
//  eggifyTests
//
//  Bare URLs in replies become links; Select Text / Copy give clean text.
//

import Foundation
import SwiftUI
import Testing
@testable import eggify

@Suite("Message text")
@MainActor
struct TextTests {
    @Test func bareURLBecomesALink() {
        var text = AttributedString("Deployed to https://example.com/app now")
        MarkdownInline.linkBareURLs(in: &text)
        let links = text.runs.compactMap(\.link)
        #expect(links == [URL(string: "https://example.com/app")!])
        #expect(String(text.characters) == "Deployed to https://example.com/app now")
    }

    @Test func urlInsideInlineCodeStaysCode() {
        let text = MarkdownInline.attributed("Run `curl https://example.com` first", codeBackground: .clear)
        #expect(text.runs.allSatisfy { $0.link == nil })
    }

    @Test func markdownLinkIsKept() {
        let text = MarkdownInline.attributed("See [the docs](https://example.com/docs).", codeBackground: .clear)
        #expect(text.runs.compactMap(\.link) == [URL(string: "https://example.com/docs")!])
    }

    @Test func copyGivesCleanText() {
        let markdown = """
        **Done.** Changed `main.go`:

        ```go
        fmt.Println("hi")
        ```
        """
        let plain = SelectableText.plain(markdown)
        #expect(plain.contains("Done. Changed main.go:"))
        #expect(plain.contains("fmt.Println(\"hi\")"))
        #expect(!plain.contains("```"))
        #expect(!plain.contains("**"))
    }

    @Test func selectableTextKeepsLineBreaks() {
        let attributed = SelectableText.attributed("one\ntwo\n\nthree")
        #expect(attributed.string == "one\ntwo\n\nthree")
    }
}
