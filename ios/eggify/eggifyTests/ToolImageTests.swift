//
//  ToolImageTests.swift
//  eggifyTests
//
//  A Read on a screenshot: the image arrives as a text-less "user" tool result
//  and is shown under the Read call, as on the desktop.
//

import Foundation
import Testing
@testable import eggify

@Suite("Tool result images")
@MainActor
struct ToolImageTests {
    private func readCall(_ toolID: String) -> AgentMessage {
        AgentMessage.parse([
            "id": "a-\(toolID)", "role": "assistant",
            "content": ["type": "assistant", "text": [String](),
                        "tools": [["id": toolID, "name": "Read", "input": ["file_path": "/tmp/\(toolID).png"]]]],
        ])!
    }

    private func toolResult(_ toolID: String, data: String = "iVBORw0KGgo=") -> AgentMessage {
        AgentMessage.parse([
            "id": "r-\(toolID)", "role": "user",
            "content": ["type": "user", "content": "",
                        "tool_results": [["tool_use_id": toolID, "is_error": false,
                                          "image_preview": ["type": "image",
                                                            "source": ["type": "base64", "media_type": "image/png", "data": data]]]]],
        ])!
    }

    @Test func imageGoesOnTheReadCall() {
        let result = toolResult("toolu_1")
        #expect(result.toolResultImages["toolu_1"]?.base64Data == "iVBORw0KGgo=")

        var list = [readCall("toolu_0"), readCall("toolu_1")]
        let changed = list.attachToolImages(result.toolResultImages)
        #expect(changed)
        #expect(list[0].toolImages.isEmpty)
        #expect(list[1].toolImages.map(\.base64Data) == ["iVBORw0KGgo="])
        #expect(!ChatItem.isToolOnlyStep(list[1]), "a step with an image is shown, not folded away")
    }

    @Test func attachingTwiceAddsItOnce() {
        var list = [readCall("toolu_1")]
        let images = toolResult("toolu_1").toolResultImages
        list.attachToolImages(images)
        let changedAgain = list.attachToolImages(images)
        #expect(!changedAgain)
        #expect(list[0].toolImages.count == 1)
    }

    @Test func aUserPromptWithTextIsNotAToolResult() {
        let prompt = AgentMessage.parse([
            "id": "u", "role": "user",
            "content": ["type": "user", "content": "look at this",
                        "tool_results": [["tool_use_id": "toolu_1",
                                          "image_preview": ["source": ["media_type": "image/png", "data": "abc"]]]]],
        ])!
        #expect(prompt.toolResultImages.isEmpty)
    }
}
