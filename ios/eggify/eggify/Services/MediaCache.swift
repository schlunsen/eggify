//
//  MediaCache.swift
//  eggify
//
//  Downloaded session media (audio/video) on disk, per server:
//  Caches/EggifyMedia/<host>/<media id>-<size>.<ext>. Files are played from
//  here, reused while present, and the oldest are deleted once all servers
//  together hold more than `maxBytes`. Signing out deletes a server's folder.
//

import Foundation

nonisolated final class MediaCache: @unchecked Sendable {
    static let shared = MediaCache()

    /// Total size kept across all servers.
    static let maxBytes: Int64 = 2 * 1024 * 1024 * 1024

    let root: URL
    private let fm = FileManager.default

    init(root: URL? = nil) {
        self.root = root ?? FileManager.default.urls(for: .cachesDirectory, in: .userDomainMask)[0]
            .appendingPathComponent("EggifyMedia", isDirectory: true)
    }

    // MARK: Paths

    /// Host ids are UUIDs; keep only safe characters so nothing can leave the folder.
    static func folderName(host: String?) -> String {
        let safe = String((host ?? "").unicodeScalars
            .filter { CharacterSet.alphanumerics.contains($0) || $0 == "-" || $0 == "_" }
            .map(Character.init))
        return safe.isEmpty ? "default" : safe
    }

    func fileURL(for item: SessionMedia, compatible: Bool, host: String?) -> URL {
        root.appendingPathComponent(Self.folderName(host: host), isDirectory: true)
            .appendingPathComponent(item.cacheFileName(compatible: compatible))
    }

    /// The downloaded file, if it is there (and marks it as recently used).
    func existingFile(_ url: URL) -> URL? {
        guard let size = (try? fm.attributesOfItem(atPath: url.path))?[.size] as? NSNumber,
              size.int64Value > 0 else { return nil }
        try? fm.setAttributes([.modificationDate: Date()], ofItemAtPath: url.path)
        return url
    }

    /// Moves a finished download into place (replacing an older copy).
    func place(_ temporary: URL, at destination: URL) throws {
        try fm.createDirectory(at: destination.deletingLastPathComponent(), withIntermediateDirectories: true)
        if fm.fileExists(atPath: destination.path) { try fm.removeItem(at: destination) }
        try fm.moveItem(at: temporary, to: destination)
        // Not backed up; protected while the device is locked after boot.
        try? fm.setAttributes([.protectionKey: FileProtectionType.completeUntilFirstUserAuthentication],
                              ofItemAtPath: destination.path)
    }

    // MARK: Size cap

    /// Deletes the least recently used files until everything fits in
    /// `maxBytes`. `keeping` (the file just downloaded) is never deleted.
    func prune(maxBytes: Int64 = MediaCache.maxBytes, keeping: URL? = nil) {
        let keys: [URLResourceKey] = [.fileSizeKey, .contentModificationDateKey, .isRegularFileKey]
        guard let enumerator = fm.enumerator(at: root, includingPropertiesForKeys: keys) else { return }
        var files: [(url: URL, size: Int64, date: Date)] = []
        for case let url as URL in enumerator {
            guard let values = try? url.resourceValues(forKeys: Set(keys)), values.isRegularFile == true else { continue }
            files.append((url, Int64(values.fileSize ?? 0), values.contentModificationDate ?? .distantPast))
        }
        var total = files.reduce(Int64(0)) { $0 + $1.size }
        guard total > maxBytes else { return }
        let keep = keeping?.standardizedFileURL.path
        for file in files.sorted(by: { $0.date < $1.date }) where total > maxBytes {
            if file.url.standardizedFileURL.path == keep { continue }
            if (try? fm.removeItem(at: file.url)) != nil { total -= file.size }
        }
    }

    /// Forget everything downloaded from a server (sign-out).
    func clear(host: String?) {
        let dir = root.appendingPathComponent(Self.folderName(host: host), isDirectory: true)
        DispatchQueue.global(qos: .utility).async { try? FileManager.default.removeItem(at: dir) }
    }

    // MARK: Sharing

    /// The file under its real name (a hard link in tmp), so Save to Files
    /// and AirDrop don't show the cache name. Falls back to the cached file.
    func shareCopy(of file: URL, named name: String) -> URL {
        let clean = (name as NSString).lastPathComponent
            .replacingOccurrences(of: "/", with: "_")
            .trimmingCharacters(in: .whitespacesAndNewlines)
        guard !clean.isEmpty, !clean.hasPrefix(".") else { return file }
        let dir = fm.temporaryDirectory
            .appendingPathComponent("EggifyMediaShare", isDirectory: true)
            .appendingPathComponent(file.deletingPathExtension().lastPathComponent, isDirectory: true)
        let target = dir.appendingPathComponent(clean)
        if fm.fileExists(atPath: target.path) { return target }
        do {
            try fm.createDirectory(at: dir, withIntermediateDirectories: true)
            try fm.linkItem(at: file, to: target)
            return target
        } catch {
            return file
        }
    }
}

// MARK: - Download

/// Errors of a media download the UI words itself.
nonisolated enum MediaDownloadError: LocalizedError {
    /// 409: the file changed on the server since the list was loaded.
    case changed
    case unauthorized
    case notFound
    case invalidResponse
    case network(Error)
    case http(Int, String?)

    var errorDescription: String? {
        switch self {
        case .changed: return "The file changed on the server. Try again."
        case .unauthorized: return "Your session has expired. Sign in again."
        case .notFound: return "The file is no longer on the server."
        case .invalidResponse: return "Unexpected response from the server"
        case .network(let error):
            if let urlError = error as? URLError {
                switch urlError.code {
                case .notConnectedToInternet: return "You're offline."
                case .timedOut: return "The server took too long to answer."
                case .cannotConnectToHost, .cannotFindHost: return "Can't reach the server."
                default: break
                }
            }
            return error.localizedDescription
        case .http(_, let message?): return message
        case .http(let code, nil): return "Download failed (\(code))"
        }
    }
}

/// Downloads one file to disk with progress, through the app's own
/// URLSession (Bearer token, relay certificate pin). Nonisolated: the
/// URLSession callbacks arrive on its own queue.
///
/// Big files over the relay take a while and the connection can drop (the
/// phone locks, the app goes to the background, the network changes). A
/// download that stops hands its resume data to `onResumeData`, and passing
/// it back in as `resumeData` continues from there (the server answers Range
/// requests) instead of starting over.
nonisolated enum MediaDownloader {
    private final class Box: @unchecked Sendable {
        var task: URLSessionDownloadTask?
        var observation: NSKeyValueObservation?
        var lastReported = -1.0
    }

    /// Resume data a failed download left in its error, if any.
    static func resumeData(from error: Error) -> Data? {
        if case MediaDownloadError.network(let inner) = error { return resumeData(from: inner) }
        return (error as? URLError)?.downloadTaskResumeData
            ?? (error as NSError).userInfo[NSURLSessionDownloadTaskResumeData] as? Data
    }

    /// Worth trying again (a dropped or timed-out connection), as opposed to
    /// an answer from the server.
    static func isTransient(_ error: Error) -> Bool {
        guard case MediaDownloadError.network(let inner) = error, let urlError = inner as? URLError else { return false }
        switch urlError.code {
        case .networkConnectionLost, .timedOut, .notConnectedToInternet, .cannotConnectToHost,
             .cannotFindHost, .dnsLookupFailed, .backgroundSessionWasDisconnected,
             .internationalRoamingOff, .callIsActive, .dataNotAllowed, .secureConnectionFailed,
             .cannotLoadFromNetwork, .cannotParseResponse, .badServerResponse, .zeroByteResource:
            return true
        default:
            return false
        }
    }

    /// - Parameters:
    ///   - resumeData: continue an earlier download instead of `request`.
    ///   - expectedBytes: used for progress when the server sends no length.
    ///   - progress: (bytes received, total bytes), called on any queue, at most every 0.5 %.
    ///   - onResumeData: resume data of a download that stopped (cancelled or failed).
    static func download(_ request: URLRequest, resumeData: Data? = nil, session: URLSession, to destination: URL,
                         cache: MediaCache, expectedBytes: Int64,
                         progress: @escaping @Sendable (Int64, Int64) -> Void,
                         onResumeData: @escaping @Sendable (Data) -> Void = { _ in }) async throws {
        let box = Box()
        try await withTaskCancellationHandler {
            try await withCheckedThrowingContinuation { (continuation: CheckedContinuation<Void, Error>) in
                let completion: @Sendable (URL?, URLResponse?, Error?) -> Void = { temporary, response, error in
                    box.observation?.invalidate()
                    if let error {
                        if let data = Self.resumeData(from: error) { onResumeData(data) }
                        if (error as? URLError)?.code == .cancelled {
                            continuation.resume(throwing: CancellationError())
                        } else {
                            continuation.resume(throwing: MediaDownloadError.network(error))
                        }
                        return
                    }
                    guard let temporary, let http = response as? HTTPURLResponse else {
                        continuation.resume(throwing: MediaDownloadError.invalidResponse)
                        return
                    }
                    guard (200...299).contains(http.statusCode) else {
                        let body = (try? Data(contentsOf: temporary))
                            .flatMap { try? JSONSerialization.jsonObject(with: $0) as? [String: Any] } ?? [:]
                        let message = body.nonEmptyString("error") ?? body.nonEmptyString("message")
                        switch http.statusCode {
                        case 401: continuation.resume(throwing: MediaDownloadError.unauthorized)
                        case 404: continuation.resume(throwing: MediaDownloadError.notFound)
                        case 409: continuation.resume(throwing: MediaDownloadError.changed)
                        default: continuation.resume(throwing: MediaDownloadError.http(http.statusCode, message))
                        }
                        return
                    }
                    // The temporary file is deleted when this returns.
                    do {
                        try cache.place(temporary, at: destination)
                        continuation.resume()
                    } catch {
                        continuation.resume(throwing: error)
                    }
                }
                let task = resumeData.map { session.downloadTask(withResumeData: $0, completionHandler: completion) }
                    ?? session.downloadTask(with: request, completionHandler: completion)
                box.observation = task.progress.observe(\.completedUnitCount) { p, _ in
                    let total = p.totalUnitCount > 0 ? p.totalUnitCount : expectedBytes
                    guard total > 0 else { return }
                    let fraction = min(1, Double(p.completedUnitCount) / Double(total))
                    guard fraction - box.lastReported >= 0.005 || fraction >= 1 else { return }
                    box.lastReported = fraction
                    progress(min(p.completedUnitCount, total), total)
                }
                box.task = task
                task.resume()
            }
        } onCancel: {
            // Keep what arrived so far: tapping again continues from here.
            box.task?.cancel(byProducingResumeData: { data in
                if let data { onResumeData(data) }
            })
        }
    }
}
