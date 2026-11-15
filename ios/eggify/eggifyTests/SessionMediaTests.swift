//
//  SessionMediaTests.swift
//  eggifyTests
//
//  Audio/video a session made: the server's list, when a live message is
//  worth reloading it, where downloads land, and that a step with a player
//  is shown under its tool call instead of being folded away.
//

import Foundation
import Testing
@testable import eggify

@Suite("Session media")
@MainActor
struct SessionMediaTests {
    private let videoJSON: [String: Any] = [
        "id": "3f2a9c0d1e4b5a6c7d8e", "name": "render.mp4", "title": "Final render",
        "path": "out/render.mp4", "kind": "video", "mime_type": "video/mp4", "size": 1_482_113,
        "modified": "2026-10-11T09:30:12Z", "shown": true, "message_id": "m-12",
        "tool_use_id": "toolu_7", "tool_use_ids": ["toolu_3", "toolu_7"], "sequence": 42,
        "ios_plays": true, "url": "/api/agent/sessions/s1/media/3f2a9c0d1e4b5a6c7d8e",
        "poster_url": "/api/agent/sessions/s1/media/3f2a9c0d1e4b5a6c7d8e/poster",
    ]

    private func media(_ id: String, tool: String = "", message: String = "", plays: Bool = true,
                       kind: String = "audio", name: String = "a.mp3") -> SessionMedia {
        SessionMedia.parse(["id": id, "name": name, "kind": kind, "size": 10, "url": "/m/\(id)",
                            "tool_use_id": tool, "message_id": message, "ios_plays": plays])!
    }

    private func toolStep(_ id: String, toolID: String, name: String = "Bash",
                          input: [String: JSONValue] = ["command": .string("ls")]) -> AgentMessage {
        AgentMessage(id: id, role: "assistant", content: .string(""), created_at: nil, thinking: nil,
                     tools: [ToolUseInfo(id: toolID, name: name, status: nil, input: .object(input))])
    }

    // MARK: Parsing

    @Test func parsesTheServerList() {
        let list = SessionMediaList.parse([
            "items": [videoJSON, [
                "id": "b1", "name": "voice.ogg", "path": "voice.ogg", "kind": "audio", "mime_type": "audio/ogg",
                "size": 2048, "shown": false, "tool_use_id": "", "sequence": 3, "ios_plays": false,
                "url": "/api/agent/sessions/s1/media/b1", "compatible": "ready",
                "compatible_url": "/api/agent/sessions/s1/media/b1?variant=compatible",
            ], ["name": "no id"]],
            "ffmpeg": true,
        ])
        #expect(list.ffmpeg)
        #expect(list.items.count == 2, "an item without an id is skipped")

        let video = list.items[0]
        #expect(video.kind == .video)
        #expect(video.displayTitle == "Final render")
        #expect(video.size == 1_482_113)
        #expect(video.modified != nil)
        #expect(video.shown)
        #expect(video.toolUseID == "toolu_7")
        #expect(video.toolUseIDs == ["toolu_3", "toolu_7"])
        #expect(video.posterURL?.hasSuffix("/poster") == true)
        #expect(video.playableURL == video.url)
        #expect(!video.playsCompatibleCopy)

        let audio = list.items[1]
        #expect(audio.kind == .audio)
        #expect(!audio.iosPlays)
        #expect(audio.compatible == .ready)
        #expect(audio.playableURL == "/api/agent/sessions/s1/media/b1?variant=compatible")
        #expect(audio.playsCompatibleCopy)
    }

    @Test func anUnplayableFileWaitsForItsCopy() {
        var item = media("w", plays: false, kind: "video", name: "clip.webm")
        #expect(item.playableURL == nil)
        item.compatible = .running
        #expect(item.playableURL == nil)
        item.compatible = .ready
        #expect(item.playableURL == "/m/w?variant=compatible", "the copy's URL is derived when the list has none")
    }

    @Test func serverPathsKeepTheirQuery() {
        let base = URL(string: "https://abc.r.example.com")!
        #expect(SessionMedia.absoluteURL("/api/agent/sessions/s/media/m?variant=compatible", base: base)?.absoluteString
                == "https://abc.r.example.com/api/agent/sessions/s/media/m?variant=compatible")
        let withPath = URL(string: "https://example.com:8443/eggify/")!
        #expect(SessionMedia.absoluteURL("/api/x/poster", base: withPath)?.absoluteString
                == "https://example.com:8443/eggify/api/x/poster")
    }

    // MARK: Refresh

    @Test func detectsMediaPaths() {
        #expect(SessionMediaRefresh.mentionsMediaFile("ffmpeg -i in.mov -c:v libx264 out/final.mp4"))
        #expect(SessionMediaRefresh.mentionsMediaFile("/Users/me/renders/Song.MP3"))
        #expect(SessionMediaRefresh.mentionsMediaFile("say hi -o greeting.aiff && afplay greeting.aiff"))
        #expect(SessionMediaRefresh.mentionsMediaFile("voice.opus"))
        #expect(!SessionMediaRefresh.mentionsMediaFile("go test ./internal/sessionmedia"))
        #expect(!SessionMediaRefresh.mentionsMediaFile("notes.mp4x and video.movie"))
        #expect(!SessionMediaRefresh.mentionsMediaFile(".mp4"))
    }

    @Test func refreshesAfterRendersShowMediaAndTurnEnds() {
        let render = toolStep("a", toolID: "t1", input: ["command": .string("blender -b scene.blend -o //out/clip.mp4")])
        #expect(SessionMediaRefresh.shouldRefresh(after: render))

        let nested = toolStep("b", toolID: "t2", name: "mcp__x__render", input: ["outputs": .array([.object(["file": .string("a.wav")])])])
        #expect(SessionMediaRefresh.shouldRefresh(after: nested))

        let show = toolStep("c", toolID: "t3", name: "mcp__eggify-tools__show_media", input: ["id": .string("x")])
        #expect(SessionMediaRefresh.shouldRefresh(after: show))

        var result = AgentMessage.local(role: "result", text: "")
        result.turnSummary = AgentMessage.TurnSummary(durationMs: 10, costUSD: nil, numTurns: 1)
        #expect(SessionMediaRefresh.shouldRefresh(after: result))

        #expect(!SessionMediaRefresh.shouldRefresh(after: toolStep("d", toolID: "t4")))
        #expect(!SessionMediaRefresh.shouldRefresh(after: .local(role: "assistant", text: "rendered clip.mp4")),
                "reply text alone doesn't make files")
    }

    // MARK: Cache

    @Test func cacheFileNames() {
        let video = SessionMedia.parse(videoJSON)!
        #expect(video.cacheFileName(compatible: false) == "3f2a9c0d1e4b5a6c7d8e-1482113.mp4")
        #expect(video.cacheFileName(compatible: true) == "3f2a9c0d1e4b5a6c7d8e-1482113-compat.mp4")

        let webm = media("../evil", plays: false, kind: "audio", name: "voice.OGG")
        #expect(webm.cacheFileName(compatible: false) == "evil-10.ogg", "nothing can leave the folder")
        #expect(webm.cacheFileName(compatible: true) == "evil-10-compat.m4a")

        #expect(MediaCache.folderName(host: "A1B2-c3/../x") == "A1B2-c3x")
        #expect(MediaCache.folderName(host: nil) == "default")
    }

    @Test func pruneDeletesTheOldestFirst() throws {
        let root = FileManager.default.temporaryDirectory.appendingPathComponent("media-cache-\(UUID().uuidString)")
        defer { try? FileManager.default.removeItem(at: root) }
        let cache = MediaCache(root: root)
        let dir = root.appendingPathComponent("host")
        try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
        var files: [URL] = []
        for i in 0..<3 {
            let file = dir.appendingPathComponent("f\(i).mp3")
            try Data(repeating: 1, count: 100).write(to: file)
            try FileManager.default.setAttributes([.modificationDate: Date(timeIntervalSinceNow: Double(i - 10) * 60)],
                                                  ofItemAtPath: file.path)
            files.append(file)
        }
        cache.prune(maxBytes: 150, keeping: files[0])
        #expect(FileManager.default.fileExists(atPath: files[0].path), "the file just downloaded stays")
        #expect(!FileManager.default.fileExists(atPath: files[1].path))
        #expect(!FileManager.default.fileExists(atPath: files[2].path))
        #expect(cache.existingFile(files[0]) != nil)
    }

    // MARK: Placing cards

    @Test func mediaGoesOnTheToolCallThatNamedIt() {
        let messages = [toolStep("a", toolID: "t1"), toolStep("b", toolID: "t2"), toolStep("c", toolID: "t3")]
        let placed = messages.attachingMedia([media("m2", tool: "t2"), media("m9", tool: "gone")])
        #expect(placed[0].media.isEmpty)
        #expect(placed[1].media.map(\.id) == ["m2"])
        #expect(placed[2].media.isEmpty)
    }

    @Test func eachItemIsShownOnce() {
        // A tool id that appears twice (a reloaded copy) still gets one card.
        let messages = [toolStep("a", toolID: "t1"), toolStep("a2", toolID: "t1")]
        let placed = messages.attachingMedia([media("m1", tool: "t1")])
        #expect(placed.flatMap(\.media).map(\.id) == ["m1"])
    }

    @Test func fallsBackToTheStoredMessage() {
        let messages = [toolStep("m-5", toolID: "t1")]
        let placed = messages.attachingMedia([media("m1", message: "m-5")])
        #expect(placed[0].media.map(\.id) == ["m1"])
    }

    @Test func aStepWithAPlayerIsNotFolded() {
        let steps = [toolStep("a", toolID: "t1"), toolStep("b", toolID: "t2"), toolStep("c", toolID: "t3"),
                     toolStep("d", toolID: "t4")]
        let folded = ChatItem.build(from: steps)
        #expect(folded.count == 1, "four plain tool steps fold into one row")

        let items = ChatItem.build(from: steps, media: [media("m", tool: "t2")])
        #expect(items.count == 4, "the step with a player splits the run; the short runs left stay inline")
        #expect(items[0] == .message(steps[0]))
        if case .message(let shown) = items[1] {
            #expect(shown.id == "b")
            #expect(shown.media.map(\.id) == ["m"])
            #expect(!ChatItem.isToolOnlyStep(shown))
        } else {
            Issue.record("the step with media should be its own row")
        }
        if case .message = items[3] {} else { Issue.record("two steps after it stay inline") }
    }

    @Test func mediaChangesTheRow() {
        let steps = [toolStep("a", toolID: "t1")]
        let before = ChatItem.build(from: steps, media: [media("m", tool: "t1")])
        var converted = media("m", tool: "t1")
        converted.compatible = .ready
        let after = ChatItem.build(from: steps, media: [converted])
        #expect(before != after, "equatable rows re-render when their media changes")
    }

    @Test func timeFormat() {
        #expect(MediaTime.format(0) == "0:00")
        #expect(MediaTime.format(75) == "1:15")
        #expect(MediaTime.format(3725) == "1:02:05")
        #expect(MediaTime.format(.nan) == "0:00")
    }

    @Test func droppedConnectionsAreRetriedButServerAnswersAreNot() {
        #expect(MediaDownloader.isTransient(MediaDownloadError.network(URLError(.networkConnectionLost))))
        #expect(MediaDownloader.isTransient(MediaDownloadError.network(URLError(.timedOut))))
        #expect(!MediaDownloader.isTransient(MediaDownloadError.network(URLError(.cancelled))))
        #expect(!MediaDownloader.isTransient(MediaDownloadError.notFound))
        #expect(!MediaDownloader.isTransient(MediaDownloadError.http(500, nil)))
    }

    @Test func resumeDataIsTakenFromTheDroppedDownloadsError() {
        let data = Data("resume".utf8)
        let lost = URLError(.networkConnectionLost, userInfo: [NSURLSessionDownloadTaskResumeData: data])
        #expect(MediaDownloader.resumeData(from: MediaDownloadError.network(lost)) == data)
        #expect(MediaDownloader.resumeData(from: lost) == data)
        #expect(MediaDownloader.resumeData(from: MediaDownloadError.network(URLError(.timedOut))) == nil)
    }

    @Test func downloadProgressReadsInOneUnit() {
        let text = MediaBytes.progress(12_300_000, of: 88_700_000)
        #expect(text.hasSuffix("MB"))
        #expect(text.components(separatedBy: "MB").count == 2) // "12.3 of 88.7 MB", the unit once
        #expect(MediaBytes.progress(5_000, of: 0).contains("KB"))
    }

    @Test func onlyFormatsPhotosTakesOfferSaveToPhotos() {
        #expect(MediaPhotoLibrary.canSave(fileExtension: "mp4"))
        #expect(MediaPhotoLibrary.canSave(fileExtension: "MOV"))
        #expect(!MediaPhotoLibrary.canSave(fileExtension: "webm"))
        #expect(!MediaPhotoLibrary.canSave(fileExtension: "mkv"))
    }
}
