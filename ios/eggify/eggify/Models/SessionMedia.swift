//
//  SessionMedia.swift
//  eggify
//
//  Audio and video files a session made (renders, or files the agent showed
//  with show_media). Comes from GET /api/agent/sessions/:id/media, newest
//  first. The app downloads a file through its own URLSession (Bearer token,
//  relay certificate pin) and plays the local copy: AVPlayer's own
//  networking can't use the pin.
//

import Foundation

nonisolated struct SessionMedia: Identifiable, Equatable, Sendable {
    enum Kind: String, Sendable { case video, audio }

    /// State of the iPhone-friendly copy (H.264/AAC), when one was asked for.
    enum Conversion: String, Sendable { case running, ready, failed }

    let id: String
    var name: String
    var title: String?
    /// For display: relative to the working directory, or ~/…
    var path: String
    var kind: Kind
    var mimeType: String
    var size: Int64
    var modified: Date?
    /// The agent presented it with show_media.
    var shown: Bool
    /// The stored message and tool call that last named it (tool id may be "").
    var messageID: String
    var toolUseID: String
    var toolUseIDs: [String]
    var sequence: Int
    /// False for formats iPhone can't play (WebM, MKV, Ogg, Opus).
    var iosPlays: Bool
    /// Server paths (may carry a query), relative to the server's base URL.
    var url: String
    var posterURL: String?
    var compatible: Conversion?
    var compatibleURL: String?
    var compatibleError: String?

    static func parse(_ data: [String: Any]) -> SessionMedia? {
        guard let id = data.nonEmptyString("id"), let url = data.nonEmptyString("url") else { return nil }
        let name = data.nonEmptyString("name") ?? id
        let kind = Kind(rawValue: data.string("kind") ?? "")
            ?? ((data.string("mime_type") ?? "").hasPrefix("audio/") ? .audio : .video)
        return SessionMedia(
            id: id,
            name: name,
            title: data.nonEmptyString("title"),
            path: data.nonEmptyString("path") ?? name,
            kind: kind,
            mimeType: data.string("mime_type") ?? "",
            size: data.int64("size") ?? 0,
            modified: EggifyDate.parse(data.string("modified")),
            shown: data.bool("shown") ?? false,
            messageID: data.string("message_id") ?? "",
            toolUseID: data.string("tool_use_id") ?? "",
            toolUseIDs: data.strings("tool_use_ids"),
            sequence: data.int("sequence") ?? 0,
            iosPlays: data.bool("ios_plays") ?? true,
            url: url,
            posterURL: data.nonEmptyString("poster_url"),
            compatible: data.string("compatible").flatMap(Conversion.init(rawValue:)),
            compatibleURL: data.nonEmptyString("compatible_url"),
            compatibleError: data.nonEmptyString("compatible_error")
        )
    }

    var displayTitle: String { title ?? name }

    /// "webm", "mp4", … from the file name (lower case, letters and digits only).
    var fileExtension: String {
        let ext = (name as NSString).pathExtension.lowercased()
        let clean = String(ext.unicodeScalars.filter { CharacterSet.alphanumerics.contains($0) }.map(Character.init))
        if !clean.isEmpty { return clean }
        return kind == .video ? "mp4" : "m4a"
    }

    /// The server path the phone should download to play it: the file itself,
    /// or the converted copy when iPhone can't play the original. Nil when
    /// neither is available yet.
    var playableURL: String? {
        if iosPlays { return url }
        if compatible == .ready { return compatibleURL ?? "\(url)?variant=compatible" }
        return nil
    }

    /// Whether `playableURL` is the converted copy.
    var playsCompatibleCopy: Bool { !iosPlays && compatible == .ready }

    /// The local file name of a download: `<id>-<size>.<ext>`, so a file that
    /// changed on the server (new size) is fetched again. The converted copy
    /// is an MP4 (video) or M4A (audio).
    func cacheFileName(compatible: Bool) -> String {
        let safeID = String(id.unicodeScalars.filter { CharacterSet.alphanumerics.contains($0) || $0 == "-" || $0 == "_" }
            .map(Character.init))
        if compatible {
            return "\(safeID)-\(size)-compat.\(kind == .video ? "mp4" : "m4a")"
        }
        return "\(safeID)-\(size).\(fileExtension)"
    }

    /// "1.4 MB"
    var sizeText: String { ByteCountFormatter.string(fromByteCount: size, countStyle: .file) }

    /// A server path such as `/api/agent/sessions/x/media/y?variant=compatible`
    /// on the server's base URL (kept: a base path, the query as sent).
    static func absoluteURL(_ serverPath: String, base: URL) -> URL? {
        guard var components = URLComponents(url: base, resolvingAgainstBaseURL: false) else { return nil }
        let parts = serverPath.split(separator: "?", maxSplits: 1, omittingEmptySubsequences: false)
        let path = String(parts.first ?? "")
        var basePath = components.percentEncodedPath
        while basePath.hasSuffix("/") { basePath.removeLast() }
        components.percentEncodedPath = basePath + (path.hasPrefix("/") ? path : "/" + path)
        components.percentEncodedQuery = parts.count > 1 ? String(parts[1]) : nil
        components.fragment = nil
        return components.url
    }
}

/// A session's media list as the server returns it.
nonisolated struct SessionMediaList: Equatable, Sendable {
    var items: [SessionMedia]
    /// The server has ffmpeg (posters, conversion).
    var ffmpeg: Bool

    static func parse(_ data: [String: Any]) -> SessionMediaList {
        SessionMediaList(items: data.dicts("items").compactMap(SessionMedia.parse),
                         ffmpeg: data.bool("ffmpeg") ?? false)
    }
}

// MARK: - When to look again

nonisolated enum SessionMediaRefresh {
    /// File extensions the server lists (`internal/sessionmedia`).
    static let extensions = ["mp4", "m4v", "mov", "webm", "mkv", "mp3", "m4a", "aac", "wav",
                             "aif", "aiff", "flac", "ogg", "oga", "opus"]

    private static let pattern: NSRegularExpression = {
        let alternatives = extensions.joined(separator: "|")
        // A name ending in a media extension, not followed by more name.
        return try! NSRegularExpression(pattern: "[A-Za-z0-9_\\-]\\.(?:\(alternatives))(?![A-Za-z0-9_])",
                                        options: [.caseInsensitive])
    }()

    /// Whether the text names a media file ("out/render.mp4", "song.MP3").
    static func mentionsMediaFile(_ text: String) -> Bool {
        guard text.count >= 4 else { return false }
        return pattern.firstMatch(in: text, range: NSRange(text.startIndex..., in: text)) != nil
    }

    /// Whether a live message may have made or shown media, so the session's
    /// list is worth reloading: a turn ended, a tool named *show_media ran,
    /// or a tool's input names a media file.
    static func shouldRefresh(after message: AgentMessage) -> Bool {
        if message.role == "result" { return true }
        for tool in message.getTools() {
            if tool.name.lowercased().hasSuffix("show_media") { return true }
            if let input = tool.input, inputMentionsMedia(input) { return true }
        }
        return false
    }

    private static func inputMentionsMedia(_ value: JSONValue) -> Bool {
        switch value {
        case .string(let s): return mentionsMediaFile(s)
        case .object(let o): return o.values.contains(where: inputMentionsMedia)
        case .array(let a): return a.contains(where: inputMentionsMedia)
        default: return false
        }
    }
}

// MARK: - Placing players in the chat

nonisolated extension Array where Element == AgentMessage {
    /// Puts each media item on the message whose tool call last named it
    /// (`tool_use_id`; live and stored tool ids are the same), else on the
    /// stored message (`message_id`). Each item once; items whose message
    /// isn't loaded stay in the Media sheet only.
    func attachingMedia(_ items: [SessionMedia]) -> [AgentMessage] {
        guard !items.isEmpty || contains(where: { !$0.media.isEmpty }) else { return self }
        var byTool: [String: [SessionMedia]] = [:]
        var byMessage: [String: [SessionMedia]] = [:]
        // Oldest first under one call, as they were made.
        for item in items.reversed() {
            if !item.toolUseID.isEmpty {
                byTool[item.toolUseID, default: []].append(item)
            } else if !item.messageID.isEmpty {
                byMessage[item.messageID, default: []].append(item)
            }
        }
        var placed = Set<String>()
        var out = self
        for i in out.indices {
            var media: [SessionMedia] = []
            if !out[i].isUser {
                for tool in out[i].getTools() {
                    for item in byTool[tool.id] ?? [] where placed.insert(item.id).inserted {
                        media.append(item)
                    }
                }
                for item in byMessage[out[i].id] ?? [] where placed.insert(item.id).inserted {
                    media.append(item)
                }
            }
            if out[i].media != media { out[i].media = media }
        }
        return out
    }
}
