//
//  SessionMediaStore.swift
//  eggify
//
//  Media of open sessions (audio/video the agent made or showed), the
//  downloads that play them, and the one audio player.
//
//  Its own object rather than part of the session store: download progress
//  and audio time change many times a second, and only the cards showing
//  them should re-render, never the whole chat. The chat reads the lists
//  (to place a card under the tool call that named the file); each card
//  observes its own `MediaDownload`.
//

import SwiftUI
import Combine
import AVFoundation
import AVKit
import UIKit
import Photos

@MainActor
final class SessionMediaStore: ObservableObject {
    static let shared = SessionMediaStore()

    /// Per session, once its chat was opened.
    @Published private(set) var lists: [String: SessionMediaList] = [:]
    @Published private(set) var loadErrors: [String: String] = [:]
    @Published private(set) var loading: Set<String> = []

    private let apiClient = EggifyAPIClient.shared
    /// Sessions whose list is kept current (their chat was opened).
    private var opened: Set<String> = []
    private var refreshTasks: [String: Task<Void, Never>] = [:]
    private var pollTasks: [String: Task<Void, Never>] = [:]
    private var downloads: [String: MediaDownload] = [:]
    private let posters = NSCache<NSString, UIImage>()

    /// Live messages that may have made media reload the list this much later
    /// (a render often writes several files in a row).
    nonisolated static let refreshDelay: Duration = .milliseconds(1500)
    /// While a conversion runs, the list is read again this often.
    nonisolated static let pollInterval: Duration = .seconds(3)

    private init() {
        posters.totalCostLimit = 30 * 1024 * 1024
    }

    // MARK: Lists

    func items(_ sessionId: String) -> [SessionMedia] { lists[sessionId]?.items ?? [] }

    /// Loads (or reloads) a session's media list; quietly fails on older servers.
    func load(_ sessionId: String) async {
        opened.insert(sessionId)
        guard !loading.contains(sessionId) else { return }
        loading.insert(sessionId)
        defer { loading.remove(sessionId) }
        do {
            let list = try await apiClient.getSessionMedia(sessionID: sessionId)
            guard opened.contains(sessionId) else { return }
            if lists[sessionId] != list { lists[sessionId] = list }
            if loadErrors[sessionId] != nil { loadErrors[sessionId] = nil }
            for item in list.items { downloads[downloadKey(item, compatible: item.playsCompatibleCopy)]?.update(item) }
            pollWhileConverting(sessionId, list)
        } catch is CancellationError {
        } catch EggifyAPIClient.EggifyAPIError.notFound {
            // A server without media support (or the session is gone).
            if lists[sessionId] == nil { lists[sessionId] = SessionMediaList(items: [], ffmpeg: false) }
        } catch {
            loadErrors[sessionId] = error.localizedDescription
        }
    }

    /// A live message arrived: reload the list soon if it may have made or
    /// shown media (only for chats that were opened).
    func noteLiveMessage(_ message: AgentMessage, sessionId: String) {
        guard opened.contains(sessionId), SessionMediaRefresh.shouldRefresh(after: message) else { return }
        scheduleRefresh(sessionId)
    }

    func scheduleRefresh(_ sessionId: String, after delay: Duration = SessionMediaStore.refreshDelay) {
        refreshTasks[sessionId]?.cancel()
        refreshTasks[sessionId] = Task { [weak self] in
            try? await Task.sleep(for: delay)
            guard !Task.isCancelled, let self else { return }
            self.refreshTasks[sessionId] = nil
            await self.load(sessionId)
        }
    }

    private func pollWhileConverting(_ sessionId: String, _ list: SessionMediaList) {
        guard list.items.contains(where: { $0.compatible == .running }) else {
            pollTasks[sessionId]?.cancel()
            pollTasks[sessionId] = nil
            return
        }
        guard pollTasks[sessionId] == nil else { return }
        pollTasks[sessionId] = Task { [weak self] in
            try? await Task.sleep(for: SessionMediaStore.pollInterval)
            guard !Task.isCancelled, let self else { return }
            self.pollTasks[sessionId] = nil
            await self.load(sessionId)
        }
    }

    /// Asks the server for an iPhone-friendly copy, then follows it in the list.
    func convert(_ item: SessionMedia, sessionId: String) async {
        setItem(item.id, in: sessionId) {
            $0.compatible = .running
            $0.compatibleError = nil
        }
        do {
            let result = try await apiClient.convertSessionMedia(sessionID: sessionId, mediaID: item.id)
            setItem(item.id, in: sessionId) {
                $0.compatible = result.state ?? .running
                if let url = result.compatibleURL { $0.compatibleURL = url }
            }
        } catch {
            setItem(item.id, in: sessionId) {
                $0.compatible = .failed
                $0.compatibleError = error.localizedDescription
            }
            return
        }
        if let list = lists[sessionId] { pollWhileConverting(sessionId, list) }
    }

    private func setItem(_ id: String, in sessionId: String, _ change: (inout SessionMedia) -> Void) {
        guard var list = lists[sessionId], let index = list.items.firstIndex(where: { $0.id == id }) else { return }
        change(&list.items[index])
        lists[sessionId] = list
    }

    /// Forget everything (sign-out / host switch).
    func reset() {
        for task in refreshTasks.values { task.cancel() }
        for task in pollTasks.values { task.cancel() }
        for download in downloads.values { download.cancel() }
        refreshTasks = [:]
        pollTasks = [:]
        downloads = [:]
        opened = []
        lists = [:]
        loadErrors = [:]
        posters.removeAllObjects()
        MediaAudioPlayer.shared.stop()
    }

    // MARK: Downloads

    private func downloadKey(_ item: SessionMedia, compatible: Bool) -> String {
        "\(MediaCache.folderName(host: apiClient.currentHostId))/\(item.cacheFileName(compatible: compatible))"
    }

    /// The download that plays this item (the original, or the converted
    /// copy when iPhone can't play the original). One per file, shared by
    /// the inline card and the Media sheet. Nil while nothing is playable.
    func download(for item: SessionMedia, sessionId: String) -> MediaDownload? {
        guard let path = item.playableURL else { return nil }
        let compatible = item.playsCompatibleCopy
        let key = downloadKey(item, compatible: compatible)
        if let existing = downloads[key] { return existing }
        let destination = MediaCache.shared.fileURL(for: item, compatible: compatible, host: apiClient.currentHostId)
        let download = MediaDownload(key: key, item: item, sessionId: sessionId, serverPath: path, destination: destination)
        downloads[key] = download
        return download
    }

    // MARK: Posters

    func cachedPoster(_ item: SessionMedia) -> UIImage? {
        guard let path = item.posterURL else { return nil }
        return posters.object(forKey: posterKey(path, item) as NSString)
    }

    func poster(_ item: SessionMedia) async -> UIImage? {
        guard let path = item.posterURL else { return nil }
        let key = posterKey(path, item)
        if let hit = posters.object(forKey: key as NSString) { return hit }
        guard let data = try? await apiClient.getMediaPoster(path) else { return nil }
        let image = await Task.detached(priority: .utility) { ImageCache.downsample(data: data, maxPixel: 900) }.value
        if let image {
            let cost = Int(image.size.width * image.size.height * image.scale * image.scale * 4)
            posters.setObject(image, forKey: key as NSString, cost: cost)
        }
        return image
    }

    private func posterKey(_ path: String, _ item: SessionMedia) -> String { "\(path)#\(item.size)" }
}

// MARK: - Download

/// One file's way onto the phone: idle → downloading → ready (local file).
///
/// A dropped connection is retried a few times, each time continuing from the
/// bytes already received (resume data). Leaving the app asks iOS for time
/// to finish; a download cut off in the background continues when the app
/// comes back.
@MainActor
final class MediaDownload: ObservableObject {
    enum State: Equatable {
        case idle
        case downloading(Double)
        case ready(URL)
        case failed(String)
    }

    /// Saving the file to the photo library.
    enum SaveState: Equatable {
        case idle
        case saving
        case saved
        case failed(String)
    }

    @Published private(set) var state: State = .idle
    /// Bytes received / expected while downloading (for "12 of 89 MB").
    @Published private(set) var receivedBytes: Int64 = 0
    @Published private(set) var totalBytes: Int64 = 0
    /// Length in seconds, once the file is on the phone.
    @Published private(set) var duration: Double?
    /// The file under its real name, for the share sheet.
    @Published private(set) var shareURL: URL?
    @Published private(set) var saveState: SaveState = .idle

    /// Attempts per fetch (the first plus retries) on dropped connections.
    nonisolated static let maxAttempts = 5

    let key: String
    private(set) var item: SessionMedia
    let sessionId: String
    private let serverPath: String
    private let destination: URL
    private var task: Task<URL?, Never>?
    /// Where an interrupted download left off.
    private var resumeData: Data?
    /// Cut off while the app was in the background: continue on return.
    private var resumeOnForeground = false
    private var foregroundObserver: NSObjectProtocol?

    init(key: String, item: SessionMedia, sessionId: String, serverPath: String, destination: URL) {
        self.key = key
        self.item = item
        self.sessionId = sessionId
        self.serverPath = serverPath
        self.destination = destination
        self.totalBytes = item.size
    }

    deinit {
        if let foregroundObserver { NotificationCenter.default.removeObserver(foregroundObserver) }
    }

    var localURL: URL? {
        if case .ready(let url) = state { return url }
        return nil
    }

    var isDownloading: Bool {
        if case .downloading = state { return true }
        return false
    }

    /// Part of the file is on the phone (a cancelled or dropped download).
    var canContinue: Bool { resumeData != nil && !isDownloading && localURL == nil }

    func update(_ item: SessionMedia) { self.item = item }

    /// Picks up a copy downloaded earlier (cheap; call when a card appears).
    func checkCache() {
        guard state == .idle || isFailed, let file = MediaCache.shared.existingFile(destination) else { return }
        becomeReady(file)
    }

    private var isFailed: Bool {
        if case .failed = state { return true }
        return false
    }

    /// The local file, downloading it first if needed. Nil when it failed
    /// or was cancelled.
    func fetch() async -> URL? {
        if let localURL { return localURL }
        if let file = MediaCache.shared.existingFile(destination) {
            becomeReady(file)
            return file
        }
        if let task { return await task.value }
        resumeOnForeground = false
        state = .downloading(totalBytes > 0 ? Double(receivedBytes) / Double(totalBytes) : 0)
        let task = Task<URL?, Never> { [weak self] in
            guard let self else { return nil }
            return await self.run()
        }
        self.task = task
        let result = await task.value
        self.task = nil
        return result
    }

    private func run() async -> URL? {
        // Let the download finish if the app goes to the background.
        let background = UIApplication.shared.beginBackgroundTask(withName: "Download \(item.name)") { [weak self] in
            // Out of time: stop cleanly, keeping what arrived.
            self?.task?.cancel()
            self?.resumeOnForeground = true
        }
        defer { if background != .invalid { UIApplication.shared.endBackgroundTask(background) } }

        var attempt = 0
        while true {
            attempt += 1
            do {
                try await download()
                resumeData = nil
                becomeReady(destination)
                let destination = destination
                Task.detached(priority: .utility) { MediaCache.shared.prune(keeping: destination) }
                return destination
            } catch is CancellationError {
                state = .idle
                if resumeOnForeground { watchForeground() }
                return nil
            } catch {
                let backgrounded = UIApplication.shared.applicationState == .background
                if MediaDownloader.isTransient(error), backgrounded {
                    // iOS cut the connection while the app was away.
                    state = .idle
                    resumeOnForeground = true
                    watchForeground()
                    return nil
                }
                if MediaDownloader.isTransient(error), attempt < Self.maxAttempts, !Task.isCancelled {
                    try? await Task.sleep(for: .seconds(min(8, 1 << (attempt - 1))))
                    if Task.isCancelled { state = .idle; return nil }
                    continue
                }
                if resumeData != nil, case MediaDownloadError.network = error, attempt < Self.maxAttempts {
                    // The resume data itself may be the problem: start over once.
                    resumeData = nil
                    receivedBytes = 0
                    continue
                }
                state = .failed(error.localizedDescription)
                if case MediaDownloadError.changed = error {
                    resumeData = nil
                    receivedBytes = 0
                    // A new size means a new file name: reload the list.
                    await SessionMediaStore.shared.load(sessionId)
                }
                return nil
            }
        }
    }

    private func download() async throws {
        let resume = resumeData
        resumeData = nil
        try await EggifyAPIClient.shared.downloadMedia(serverPath, to: destination, expectedBytes: item.size,
                                                       resumeData: resume) { received, total in
            Task { @MainActor [weak self] in
                guard let self, self.isDownloading else { return }
                self.receivedBytes = received
                if total > 0 { self.totalBytes = total }
                self.state = .downloading(total > 0 ? Double(received) / Double(total) : 0)
            }
        } onResumeData: { data in
            Task { @MainActor [weak self] in self?.resumeData = data }
        }
    }

    private func watchForeground() {
        guard foregroundObserver == nil else { return }
        foregroundObserver = NotificationCenter.default.addObserver(
            forName: UIApplication.didBecomeActiveNotification, object: nil, queue: .main
        ) { [weak self] _ in
            MainActor.assumeIsolated {
                guard let self, self.resumeOnForeground, self.task == nil, self.localURL == nil else { return }
                self.resumeOnForeground = false
                Task { _ = await self.fetch() }
            }
        }
    }

    /// Stops the download; the part already received is kept, so the next
    /// fetch continues from there.
    func cancel() {
        resumeOnForeground = false
        task?.cancel()
        task = nil
        if isDownloading { state = .idle }
    }

    /// Downloads (if needed) and saves the video to the photo library.
    func saveToPhotos() async {
        guard saveState != .saving else { return }
        saveState = .saving
        guard let file = await fetch() else {
            saveState = .idle
            return
        }
        do {
            try await MediaPhotoLibrary.saveVideo(file)
            saveState = .saved
            EggifyHaptics.success()
        } catch {
            saveState = .failed(error.localizedDescription)
        }
    }

    /// Downloads (if needed) and opens the share sheet.
    func share() async {
        guard let file = await fetch() else { return }
        let url = shareURL ?? file
        MediaSharePresenter.present(url)
    }

    private func becomeReady(_ file: URL) {
        state = .ready(file)
        receivedBytes = totalBytes
        let name = item.name
        shareURL = MediaCache.shared.shareCopy(of: file, named: name)
        guard duration == nil else { return }
        Task { [weak self] in
            let seconds = try? await AVURLAsset(url: file).load(.duration).seconds
            guard let self, let seconds, seconds.isFinite, seconds > 0 else { return }
            self.duration = seconds
        }
    }
}

// MARK: - Photos and sharing

nonisolated enum MediaPhotoLibraryError: LocalizedError {
    case denied
    case unsupported

    var errorDescription: String? {
        switch self {
        case .denied: return "Eggify can't add to your photos. Allow it in Settings → Privacy → Photos."
        case .unsupported: return "Photos can't take this format. Use Share → Save to Files instead."
        }
    }
}

nonisolated enum MediaPhotoLibrary {
    /// Video formats the photo library accepts.
    static func canSave(fileExtension ext: String) -> Bool {
        ["mp4", "m4v", "mov"].contains(ext.lowercased())
    }

    @MainActor static func saveVideo(_ file: URL) async throws {
        guard canSave(fileExtension: file.pathExtension) else { throw MediaPhotoLibraryError.unsupported }
        let status = await PHPhotoLibrary.requestAuthorization(for: .addOnly)
        guard status == .authorized || status == .limited else { throw MediaPhotoLibraryError.denied }
        try await PHPhotoLibrary.shared().performChanges {
            let request = PHAssetCreationRequest.forAsset()
            let options = PHAssetResourceCreationOptions()
            options.shouldMoveFile = false
            request.addResource(with: .video, fileURL: file, options: options)
        }
    }
}

@MainActor
enum MediaSharePresenter {
    static func present(_ url: URL) {
        guard let top = VideoPlayerPresenter.topController() else { return }
        let controller = UIActivityViewController(activityItems: [url], applicationActivities: nil)
        // iPad: anchor the popover in the middle of the screen.
        if let popover = controller.popoverPresentationController {
            popover.sourceView = top.view
            popover.sourceRect = CGRect(x: top.view.bounds.midX, y: top.view.bounds.midY, width: 0, height: 0)
            popover.permittedArrowDirections = []
        }
        top.present(controller, animated: true)
    }
}

// MARK: - Audio

/// The one audio file playing (one at a time). Lives outside the chat rows,
/// so it keeps playing while the chat scrolls or the sheet closes.
@MainActor
final class MediaAudioPlayer: ObservableObject {
    static let shared = MediaAudioPlayer()

    /// `MediaDownload.key` of the loaded file.
    @Published private(set) var currentKey: String?
    @Published private(set) var isPlaying = false
    @Published private(set) var currentTime: Double = 0
    @Published private(set) var duration: Double = 0

    private var player: AVPlayer?
    private var timeObserver: Any?
    private var endObserver: NSObjectProtocol?

    private init() {}

    func isCurrent(_ key: String) -> Bool { currentKey == key }

    /// Play/pause this file; another file stops first.
    func toggle(key: String, file: URL) {
        if currentKey == key, let player {
            if isPlaying {
                player.pause()
                isPlaying = false
            } else {
                MediaPlayback.activate(video: false)
                if duration > 0, currentTime >= duration - 0.25 { player.seek(to: .zero) }
                player.play()
                isPlaying = true
            }
            return
        }
        stop()
        MediaPlayback.activate(video: false)
        let item = AVPlayerItem(url: file)
        let player = AVPlayer(playerItem: item)
        self.player = player
        currentKey = key
        currentTime = 0
        duration = 0
        timeObserver = player.addPeriodicTimeObserver(forInterval: CMTime(seconds: 0.25, preferredTimescale: 600),
                                                      queue: .main) { [weak self] time in
            MainActor.assumeIsolated {
                guard let self else { return }
                self.currentTime = time.seconds.isFinite ? time.seconds : 0
                if let d = self.player?.currentItem?.duration.seconds, d.isFinite, d > 0, d != self.duration {
                    self.duration = d
                }
            }
        }
        endObserver = NotificationCenter.default.addObserver(forName: AVPlayerItem.didPlayToEndTimeNotification,
                                                             object: item, queue: .main) { [weak self] _ in
            MainActor.assumeIsolated {
                self?.isPlaying = false
            }
        }
        player.play()
        isPlaying = true
    }

    func seek(to seconds: Double) {
        guard let player else { return }
        currentTime = seconds
        player.seek(to: CMTime(seconds: seconds, preferredTimescale: 600), toleranceBefore: .zero, toleranceAfter: .zero)
    }

    func pause() {
        player?.pause()
        isPlaying = false
    }

    func stop() {
        if let timeObserver { player?.removeTimeObserver(timeObserver) }
        if let endObserver { NotificationCenter.default.removeObserver(endObserver) }
        timeObserver = nil
        endObserver = nil
        player?.pause()
        player = nil
        currentKey = nil
        isPlaying = false
        currentTime = 0
        duration = 0
    }
}

// MARK: - Video

nonisolated enum MediaPlayback {
    /// Plays through the silent switch, as a media app should.
    @MainActor static func activate(video: Bool) {
        let session = AVAudioSession.sharedInstance()
        // Recording (dictation) owns the session; don't take it over.
        guard session.category != .record else { return }
        try? session.setCategory(.playback, mode: video ? .moviePlayback : .default)
        try? session.setActive(true)
    }
}

/// Plays a local video full screen (AVPlayerViewController, with Picture in
/// Picture), presented from UIKit so it doesn't depend on a chat row staying
/// alive.
@MainActor
final class VideoPlayerPresenter: NSObject, AVPlayerViewControllerDelegate {
    static let shared = VideoPlayerPresenter()

    /// Kept while Picture in Picture runs, to bring the player back.
    private var pipController: AVPlayerViewController?

    func present(_ file: URL) {
        MediaAudioPlayer.shared.pause()
        MediaPlayback.activate(video: true)
        guard let top = Self.topController() else { return }
        let controller = AVPlayerViewController()
        controller.player = AVPlayer(url: file)
        controller.allowsPictureInPicturePlayback = true
        controller.delegate = self
        controller.modalPresentationStyle = .fullScreen
        top.present(controller, animated: true) {
            controller.player?.play()
        }
    }

    static func topController() -> UIViewController? {
        guard let scene = UIApplication.shared.connectedScenes
                .compactMap({ $0 as? UIWindowScene })
                .first(where: { $0.activationState == .foregroundActive }),
              var top = scene.keyWindow?.rootViewController else { return nil }
        while let presented = top.presentedViewController { top = presented }
        return top
    }

    nonisolated func playerViewControllerWillStartPictureInPicture(_ playerViewController: AVPlayerViewController) {
        MainActor.assumeIsolated { pipController = playerViewController }
    }

    nonisolated func playerViewControllerDidStopPictureInPicture(_ playerViewController: AVPlayerViewController) {
        MainActor.assumeIsolated {
            if pipController === playerViewController { pipController = nil }
        }
    }

    nonisolated func playerViewController(_ playerViewController: AVPlayerViewController,
                                          restoreUserInterfaceForPictureInPictureStopWithCompletionHandler completionHandler: @escaping (Bool) -> Void) {
        MainActor.assumeIsolated {
            guard playerViewController.presentingViewController == nil, let top = Self.topController() else {
                completionHandler(true)
                return
            }
            top.present(playerViewController, animated: true) { completionHandler(true) }
        }
    }
}
