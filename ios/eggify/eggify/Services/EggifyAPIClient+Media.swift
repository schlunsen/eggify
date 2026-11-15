//
//  EggifyAPIClient+Media.swift
//  eggify
//
//  Session media: the list, conversion to an iPhone-friendly copy, video
//  posters and file downloads. Everything goes through the app's own
//  URLSession, so the Bearer token and the relay certificate pin apply.
//

import Foundation

extension EggifyAPIClient {
    /// The session's audio and video files, newest first.
    func getSessionMedia(sessionID: String) async throws -> SessionMediaList {
        SessionMediaList.parse(try await json("api/agent/sessions/\(sessionID)/media"))
    }

    /// Starts (or reports) the iPhone-friendly copy of a file. Throws a 501
    /// server error when the server has no ffmpeg.
    func convertSessionMedia(sessionID: String, mediaID: String) async throws
        -> (state: SessionMedia.Conversion?, compatibleURL: String?) {
        let response = try await json("api/agent/sessions/\(sessionID)/media/\(mediaID)/convert", method: "POST")
        return (response.string("state").flatMap(SessionMedia.Conversion.init(rawValue:)),
                response.nonEmptyString("compatible_url"))
    }

    /// A server path (`url`, `poster_url`, …) on the current server.
    func mediaURL(_ serverPath: String) throws -> URL {
        guard let baseURL else { throw EggifyAPIError.notConfigured }
        guard let url = SessionMedia.absoluteURL(serverPath, base: baseURL) else { throw EggifyAPIError.invalidURL }
        return url
    }

    /// A video's poster frame (JPEG).
    func getMediaPoster(_ serverPath: String) async throws -> Data {
        try await send(authorizedRequest(url: try mediaURL(serverPath)))
    }

    /// Downloads a file to `destination`, reporting progress (bytes received,
    /// total; on any queue). `resumeData` continues an earlier download.
    func downloadMedia(_ serverPath: String, to destination: URL, expectedBytes: Int64, resumeData: Data? = nil,
                       progress: @escaping @Sendable (Int64, Int64) -> Void,
                       onResumeData: @escaping @Sendable (Data) -> Void) async throws {
        var request = authorizedRequest(url: try mediaURL(serverPath))
        request.timeoutInterval = 60
        try await MediaDownloader.download(request, resumeData: resumeData, session: mediaSession, to: destination,
                                           cache: .shared, expectedBytes: expectedBytes,
                                           progress: progress, onResumeData: onResumeData)
    }
}
