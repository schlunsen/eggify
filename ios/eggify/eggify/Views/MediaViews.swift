//
//  MediaViews.swift
//  eggify
//
//  Players for the audio and video a session made: a card under the tool
//  call that named the file, and the Media sheet (chat menu) with all of
//  them. A file is downloaded to the phone first (progress ring), then
//  played locally: video full screen with Picture in Picture, audio in the
//  card, one at a time, and it keeps playing while the chat scrolls.
//

import SwiftUI
import Combine

// MARK: - Environment

private struct MediaSessionIDKey: EnvironmentKey {
    static let defaultValue = ""
}

extension EnvironmentValues {
    /// The session whose media cards are shown (set by the chat and the sheet).
    var mediaSessionID: String {
        get { self[MediaSessionIDKey.self] }
        set { self[MediaSessionIDKey.self] = newValue }
    }
}

// MARK: - Card

/// A player card for one file. Picks the download that plays it (the file,
/// or its converted copy); each card observes only its own download.
struct MediaCardView: View {
    let item: SessionMedia
    /// The Media sheet shows where the file is; in the chat the tool call does.
    var showsPath = false
    @Environment(\.mediaSessionID) private var sessionID

    var body: some View {
        let store = SessionMediaStore.shared
        let download = store.download(for: item, sessionId: sessionID)
        // Nothing playable yet (an unsupported format): a stand-in that never downloads.
        let shown = download ?? MediaDownload(key: "none-\(item.id)", item: item, sessionId: sessionID,
                                              serverPath: "", destination: URL(fileURLWithPath: "/dev/null"))
        Group {
            switch item.kind {
            case .video:
                VideoMediaCard(item: item, download: shown, playable: download != nil, showsPath: showsPath)
            case .audio:
                AudioMediaCard(item: item, download: shown, playable: download != nil, showsPath: showsPath)
            }
        }
        .frame(maxWidth: 400, alignment: .leading)
    }
}

private struct VideoMediaCard: View {
    let item: SessionMedia
    @ObservedObject var download: MediaDownload
    let playable: Bool
    let showsPath: Bool

    @State private var poster: UIImage?

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            Button(action: play) {
                ZStack {
                    posterView
                    centerControl
                }
                .aspectRatio(16 / 9, contentMode: .fit)
                .frame(maxWidth: .infinity)
                .clipShape(RoundedRectangle(cornerRadius: 10, style: .continuous))
                .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            .disabled(!playable)
            .accessibilityLabel(playLabel)

            MediaFooter(item: item, download: download, playable: playable, showsPath: showsPath)
            MediaConversionRow(item: item)
            MediaErrorRow(download: download)
            MediaSaveRow(download: download)
        }
        .mediaCardStyle()
        .onAppear { if playable { download.checkCache() } }
        .task(id: item.posterURL.map { "\($0)#\(item.size)" }) {
            poster = SessionMediaStore.shared.cachedPoster(item)
            if poster == nil { poster = await SessionMediaStore.shared.poster(item) }
        }
    }

    @ViewBuilder
    private var posterView: some View {
        if let poster {
            Image(uiImage: poster)
                .resizable()
                .scaledToFill()
                .frame(minWidth: 0, maxWidth: .infinity, minHeight: 0, maxHeight: .infinity)
                .clipped()
                .accessibilityHidden(true)
        } else {
            Rectangle()
                .fill(Color.black.opacity(0.85))
                .overlay {
                    Image(systemName: "film")
                        .font(.system(size: 28))
                        .foregroundStyle(.white.opacity(0.35))
                        .offset(y: playable ? -34 : 0)
                }
        }
    }

    @ViewBuilder
    private var centerControl: some View {
        if playable {
            switch download.state {
            case .downloading(let fraction):
                MediaProgressRing(fraction: fraction, size: 54, tint: .white)
            default:
                Image(systemName: "play.fill")
                    .font(.system(size: 22, weight: .bold))
                    .foregroundStyle(.white)
                    .frame(width: 54, height: 54)
                    .background(.black.opacity(0.45), in: Circle())
                    .overlay(Circle().strokeBorder(.white.opacity(0.6), lineWidth: 1))
            }
        }
    }

    private var playLabel: String {
        if download.isDownloading { return "Downloading \(item.displayTitle). Tap to cancel" }
        return "Play \(item.displayTitle)"
    }

    private func play() {
        if download.isDownloading {
            download.cancel()
            return
        }
        EggifyHaptics.tap()
        Task {
            if let file = await download.fetch() {
                VideoPlayerPresenter.shared.present(file)
            }
        }
    }
}

private struct AudioMediaCard: View {
    let item: SessionMedia
    @ObservedObject var download: MediaDownload
    let playable: Bool
    let showsPath: Bool

    @ObservedObject private var player = MediaAudioPlayer.shared
    @State private var scrubbing: Double?

    private var isCurrent: Bool { player.isCurrent(download.key) }

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            HStack(spacing: 12) {
                Button(action: toggle) { playButton }
                    .buttonStyle(.plain)
                    .disabled(!playable)
                    .accessibilityLabel(buttonLabel)

                VStack(alignment: .leading, spacing: 4) {
                    Text(item.displayTitle)
                        .font(.subheadline.weight(.semibold))
                        .lineLimit(1)
                        .truncationMode(.middle)
                    if isCurrent {
                        scrubber
                    } else {
                        MediaMetaLine(item: item, download: download)
                    }
                }
                Spacer(minLength: 0)
                if playable {
                    MediaActionsMenu(item: item, download: download)
                }
            }
            if showsPath {
                Text(item.path)
                    .font(.eggifyMono(12))
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
                    .truncationMode(.middle)
            }
            MediaConversionRow(item: item)
            MediaErrorRow(download: download)
        }
        .mediaCardStyle()
        .onAppear { if playable { download.checkCache() } }
    }

    @ViewBuilder
    private var playButton: some View {
        if case .downloading(let fraction) = download.state {
            MediaProgressRing(fraction: fraction, size: 40, tint: EggifyColors.accent)
        } else {
            Image(systemName: isCurrent && player.isPlaying ? "pause.fill" : "play.fill")
                .font(.system(size: 16, weight: .bold))
                .foregroundStyle(.white)
                .frame(width: 40, height: 40)
                .background(playable ? AnyShapeStyle(EggifyColors.accent) : AnyShapeStyle(Color(.systemGray4)), in: Circle())
        }
    }

    private var scrubber: some View {
        let total = max(player.duration, download.duration ?? 0, 0.1)
        return VStack(alignment: .leading, spacing: 0) {
            Slider(value: Binding(get: { min(scrubbing ?? player.currentTime, total) },
                                  set: { scrubbing = $0 }),
                   in: 0...total) { editing in
                if !editing, let value = scrubbing {
                    player.seek(to: value)
                    scrubbing = nil
                }
            }
            .controlSize(.mini)
            .tint(EggifyColors.accent)
            .accessibilityLabel("Position")
            Text("\(MediaTime.format(scrubbing ?? player.currentTime)) / \(MediaTime.format(total))")
                .font(.caption2)
                .monospacedDigit()
                .foregroundStyle(.secondary)
        }
    }

    private var buttonLabel: String {
        if download.isDownloading { return "Downloading \(item.displayTitle). Tap to cancel" }
        return isCurrent && player.isPlaying ? "Pause \(item.displayTitle)" : "Play \(item.displayTitle)"
    }

    private func toggle() {
        if download.isDownloading {
            download.cancel()
            return
        }
        EggifyHaptics.tap()
        let key = download.key
        if isCurrent, let file = download.localURL {
            player.toggle(key: key, file: file)
            return
        }
        Task {
            if let file = await download.fetch() {
                player.toggle(key: key, file: file)
            }
        }
    }
}

// MARK: - Pieces

/// Title, kind · size · length, and the share button (once downloaded).
private struct MediaFooter: View {
    let item: SessionMedia
    @ObservedObject var download: MediaDownload
    let playable: Bool
    let showsPath: Bool

    var body: some View {
        HStack(alignment: .top, spacing: 8) {
            VStack(alignment: .leading, spacing: 2) {
                Text(item.displayTitle)
                    .font(.subheadline.weight(.semibold))
                    .lineLimit(2)
                    .truncationMode(.middle)
                MediaMetaLine(item: item, download: download)
                if showsPath {
                    Text(item.path)
                        .font(.eggifyMono(12))
                        .foregroundStyle(.secondary)
                        .lineLimit(1)
                        .truncationMode(.middle)
                }
            }
            Spacer(minLength: 0)
            if playable {
                MediaActionsMenu(item: item, download: download)
            }
        }
    }
}

private struct MediaMetaLine: View {
    let item: SessionMedia
    @ObservedObject var download: MediaDownload

    var body: some View {
        Text(parts.joined(separator: " · "))
            .font(.caption)
            .monospacedDigit()
            .foregroundStyle(.secondary)
            .lineLimit(1)
    }

    private var parts: [String] {
        // While downloading (or after a dropped download): how far it got.
        if download.isDownloading || download.canContinue {
            let progress = MediaBytes.progress(download.receivedBytes, of: download.totalBytes)
            return [download.isDownloading ? "Downloading \(progress)" : "Paused at \(progress)"]
        }
        var result = [item.kind == .video ? "Video" : "Audio", item.fileExtension.uppercased()]
        if item.size > 0 { result.append(item.sizeText) }
        if download.localURL != nil { result.append("On iPhone") }
        if let duration = download.duration { result.append(MediaTime.format(duration)) }
        return result
    }
}

/// Download / Save to Photos / Share. Works before the file is on the
/// phone: each action downloads it first.
private struct MediaActionsMenu: View {
    let item: SessionMedia
    @ObservedObject var download: MediaDownload

    private var onPhone: Bool { download.localURL != nil }
    private var canSaveToPhotos: Bool {
        item.kind == .video
            && MediaPhotoLibrary.canSave(fileExtension: item.playsCompatibleCopy ? "mp4" : item.fileExtension)
    }

    var body: some View {
        Menu {
            if canSaveToPhotos {
                Button {
                    EggifyHaptics.tap()
                    Task { await download.saveToPhotos() }
                } label: {
                    Label(download.saveState == .saved ? "Saved to Photos" : "Save to Photos",
                          systemImage: download.saveState == .saved ? "checkmark" : "square.and.arrow.down.on.square")
                }
                .disabled(download.saveState == .saving)
            }
            Button {
                EggifyHaptics.tap()
                Task { await download.share() }
            } label: {
                Label("Share or Save to Files…", systemImage: "square.and.arrow.up")
            }
            if !onPhone {
                if download.isDownloading {
                    Button(role: .destructive) {
                        download.cancel()
                    } label: {
                        Label("Stop download", systemImage: "stop.circle")
                    }
                } else {
                    Button {
                        EggifyHaptics.tap()
                        Task { _ = await download.fetch() }
                    } label: {
                        Label(download.canContinue ? "Continue download" : "Download to iPhone",
                              systemImage: "arrow.down.circle")
                    }
                }
            }
        } label: {
            Image(systemName: onPhone ? "ellipsis.circle" : "arrow.down.circle")
                .font(.body.weight(.semibold))
                .frame(width: 36, height: 36)
                .contentShape(Rectangle())
        }
        .accessibilityLabel(onPhone ? "Save or share the file" : "Download, save or share the file")
    }
}

/// "Saving to Photos…" / "Saved to Photos" / why it couldn't.
private struct MediaSaveRow: View {
    @ObservedObject var download: MediaDownload

    var body: some View {
        switch download.saveState {
        case .idle:
            EmptyView()
        case .saving:
            HStack(spacing: 6) {
                ProgressView().controlSize(.small)
                Text(download.isDownloading ? "Downloading, then saving to Photos…" : "Saving to Photos…")
            }
            .font(.caption)
            .foregroundStyle(.secondary)
        case .saved:
            Label("Saved to Photos", systemImage: "checkmark.circle.fill")
                .font(.caption)
                .foregroundStyle(.secondary)
        case .failed(let message):
            Label(message, systemImage: "exclamationmark.triangle.fill")
                .font(.caption)
                .foregroundStyle(EggifyColors.error)
                .lineLimit(3)
        }
    }
}

nonisolated enum MediaBytes {
    /// "12.3 of 88.7 MB" (or just "12.3 MB" without a total).
    static func progress(_ received: Int64, of total: Int64) -> String {
        let done = ByteCountFormatter.string(fromByteCount: max(0, received), countStyle: .file)
        guard total > 0 else { return done }
        let all = ByteCountFormatter.string(fromByteCount: total, countStyle: .file)
        // Same unit: "12.3 of 88.7 MB"
        let doneParts = done.split(whereSeparator: \.isWhitespace), allParts = all.split(whereSeparator: \.isWhitespace)
        if doneParts.count == 2, allParts.count == 2, doneParts[1] == allParts[1] {
            return "\(doneParts[0]) of \(all)"
        }
        return "\(done) of \(all)"
    }
}

/// Formats iPhone can't play: what can be done about it.
private struct MediaConversionRow: View {
    let item: SessionMedia
    @Environment(\.mediaSessionID) private var sessionID

    var body: some View {
        if !item.iosPlays {
            let ffmpeg = SessionMediaStore.shared.lists[sessionID]?.ffmpeg ?? false
            VStack(alignment: .leading, spacing: 6) {
                switch item.compatible {
                case .ready:
                    Label("Playing a copy converted for iPhone", systemImage: "checkmark.circle")
                        .foregroundStyle(.secondary)
                case .running:
                    HStack(spacing: 8) {
                        ProgressView().controlSize(.small)
                        Text("Converting for iPhone…")
                    }
                    .foregroundStyle(.secondary)
                case .failed, nil:
                    HStack(spacing: 8) {
                        Label("iPhone can't play \(item.fileExtension.uppercased())", systemImage: "exclamationmark.triangle")
                            .foregroundStyle(.secondary)
                        Spacer(minLength: 4)
                        if ffmpeg {
                            Button(item.compatible == .failed ? "Try again" : "Convert") {
                                EggifyHaptics.tap()
                                Task { await SessionMediaStore.shared.convert(item, sessionId: sessionID) }
                            }
                            .buttonStyle(.bordered)
                            .buttonBorderShape(.capsule)
                            .controlSize(.small)
                        }
                    }
                    if item.compatible == .failed {
                        Text(item.compatibleError ?? "The conversion failed.")
                            .foregroundStyle(EggifyColors.error)
                            .lineLimit(3)
                    } else if !ffmpeg {
                        Text("Install ffmpeg on the computer running Eggify to convert it, or share the file to another app.")
                            .foregroundStyle(.secondary)
                    }
                }
            }
            .font(.caption)
        }
    }
}

private struct MediaErrorRow: View {
    @ObservedObject var download: MediaDownload

    var body: some View {
        if case .failed(let message) = download.state {
            Label(message, systemImage: "exclamationmark.triangle.fill")
                .font(.caption)
                .foregroundStyle(EggifyColors.error)
                .lineLimit(3)
        }
    }
}

/// Download progress; tapping the control it sits in cancels.
/// Until the first bytes arrive it spins (a frozen stub looked stuck), then
/// it fills. Progress arrives in bursts, so the fill follows with a smooth
/// spring that keeps its momentum between updates instead of short linear
/// steps that stop dead.
struct MediaProgressRing: View {
    let fraction: Double
    let size: CGFloat
    let tint: Color
    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    private var waiting: Bool { fraction < 0.01 }

    var body: some View {
        ZStack {
            Circle().stroke(tint.opacity(0.25), lineWidth: 3)
            if waiting && !reduceMotion {
                TimelineView(.animation) { context in
                    let turn = context.date.timeIntervalSinceReferenceDate.truncatingRemainder(dividingBy: 1)
                    Circle()
                        .trim(from: 0, to: 0.28)
                        .stroke(tint, style: StrokeStyle(lineWidth: 3, lineCap: .round))
                        .rotationEffect(.degrees(turn * 360 - 90))
                }
            } else {
                Circle()
                    .trim(from: 0, to: max(0.02, min(fraction, 1)))
                    .stroke(tint, style: StrokeStyle(lineWidth: 3, lineCap: .round))
                    .rotationEffect(.degrees(-90))
                    .animation(reduceMotion ? nil : .smooth(duration: 0.6), value: fraction)
            }
            Image(systemName: "stop.fill")
                .font(.system(size: size * 0.26, weight: .bold))
                .foregroundStyle(tint)
        }
        .frame(width: size, height: size)
        .background(tint == .white ? Color.black.opacity(0.45) : Color.clear, in: Circle())
        .accessibilityLabel(waiting ? "Starting download" : "Downloading, \(Int(fraction * 100)) percent")
    }
}

private extension View {
    func mediaCardStyle() -> some View {
        padding(10)
            .background(EggifyColors.surface, in: RoundedRectangle(cornerRadius: 14, style: .continuous))
            .overlay(RoundedRectangle(cornerRadius: 14, style: .continuous).strokeBorder(EggifyColors.border, lineWidth: 0.5))
    }
}

nonisolated enum MediaTime {
    /// 75 → "1:15", 3725 → "1:02:05"
    static func format(_ seconds: Double) -> String {
        guard seconds.isFinite, seconds > 0 else { return "0:00" }
        let total = Int(seconds.rounded())
        let h = total / 3600, m = (total % 3600) / 60, s = total % 60
        return h > 0 ? String(format: "%d:%02d:%02d", h, m, s) : String(format: "%d:%02d", m, s)
    }
}

// MARK: - Sheet

/// Every audio/video file of the session, newest first (chat menu → Media).
struct MediaSheet: View {
    let sessionID: String
    @ObservedObject private var store = SessionMediaStore.shared
    @Environment(\.dismiss) private var dismiss

    private var items: [SessionMedia] { store.items(sessionID) }

    var body: some View {
        NavigationStack {
            ScrollView {
                LazyVStack(alignment: .leading, spacing: 12) {
                    ForEach(items) { item in
                        MediaCardView(item: item, showsPath: true)
                    }
                }
                .padding(16)
                .frame(maxWidth: 760)
                .frame(maxWidth: .infinity)
            }
            .overlay { emptyState }
            .navigationTitle(items.isEmpty ? "Media" : "Media (\(items.count))")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .topBarTrailing) {
                    Button("Done") { dismiss() }
                }
            }
            .refreshable { await store.load(sessionID) }
            .task { await store.load(sessionID) }
        }
        .environment(\.mediaSessionID, sessionID)
        .presentationDetents([.medium, .large])
        .presentationDragIndicator(.visible)
    }

    @ViewBuilder
    private var emptyState: some View {
        if items.isEmpty {
            if store.loading.contains(sessionID) && store.lists[sessionID] == nil {
                ProgressView()
            } else if let error = store.loadErrors[sessionID], store.lists[sessionID] == nil {
                ContentUnavailableView {
                    Label("Couldn't load media", systemImage: "wifi.exclamationmark")
                } description: {
                    Text(error)
                } actions: {
                    Button("Try again") { Task { await store.load(sessionID) } }
                        .buttonStyle(.borderedProminent)
                }
            } else {
                ContentUnavailableView("No media yet",
                                       systemImage: "play.rectangle",
                                       description: Text("Videos and audio the agent renders, or files it shows with show_media, appear here."))
            }
        }
    }
}
