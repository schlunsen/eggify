//
//  ZenMusic.swift
//  eggify
//
//  The Zen music of the web app (the reel and four spy-funk tracks, recoded to
//  96 kbps AAC by scripts/ios-zen-music.sh), shuffled. It plays only while Zen is
//  on screen and the app is active, at a low level with a 2 s fade-in. The audio
//  session is `.ambient` with `.mixWithOthers`: it never stops the user's own
//  music or podcast and it is silent with the ring/silent switch on silent.
//  On by default; the mute toggle in Zen is remembered.
//

import AVFoundation
import Combine

/// A shuffle bag: every track plays once, in random order, before any repeats,
/// and a new round never starts with the track that just played (the web's `pickTrack`).
struct ZenShuffleBag {
    let tracks: [String]
    private(set) var bag: [String] = []
    private(set) var last: String?

    init(tracks: [String]) { self.tracks = tracks }

    mutating func next(rng: () -> Double = { Double.random(in: 0..<1) }) -> String? {
        if bag.isEmpty {
            bag = tracks
            if bag.count > 1 {
                for i in stride(from: bag.count - 1, to: 0, by: -1) {
                    let j = min(i, Int(rng() * Double(i + 1)))
                    bag.swapAt(i, j)
                }
                // Tracks are taken from the end; don't open the round with the last one.
                if bag[bag.count - 1] == last { bag.swapAt(0, bag.count - 1) }
            }
        }
        let next = bag.popLast()
        if next != nil { last = next }
        return next
    }
}

@MainActor
final class ZenMusicPlayer: NSObject, ObservableObject, AVAudioPlayerDelegate {
    static let shared = ZenMusicPlayer()

    /// The bundled tracks, in the web's order (`ZEN_TRACKS`).
    static let trackNames = ["reel", "spyfunk-groove", "spyfunk-groove2", "spyfunk-stakeout", "spyfunk-chase"]
    /// Player volume: low, so it sits under whatever else is going on.
    static let volume: Float = 0.15
    static let fadeIn: TimeInterval = 2
    private static let enabledKey = "eggify.zen.musicEnabled"

    /// Off only after an explicit mute.
    @Published private(set) var enabled: Bool
    @Published private(set) var isPlaying = false

    private var bag: ZenShuffleBag
    private var player: AVAudioPlayer?
    /// The playing track's tempo map, for the beat clock.
    private var grid: ZenBeatGrid?
    /// Zen is on screen and the app is active.
    private var wanted = false

    override init() {
        enabled = UserDefaults.standard.object(forKey: Self.enabledKey) as? Bool ?? true
        let available = Self.trackNames.filter { Bundle.main.url(forResource: $0, withExtension: "m4a") != nil }
        bag = ZenShuffleBag(tracks: available)
        super.init()
    }

    func toggle() {
        enabled.toggle()
        UserDefaults.standard.set(enabled, forKey: Self.enabledKey)
        enabled ? play() : halt()
    }

    /// Zen appeared / the app became active.
    func start() {
        wanted = true
        if enabled { play() }
    }

    /// Zen went away / the app left the foreground.
    func stop() {
        wanted = false
        halt()
    }

    private func play() {
        guard wanted, player == nil else { return }
        #if DEBUG
        if ProcessInfo.processInfo.arguments.contains("-EggifyNoMusic") { return }
        #endif
        let session = AVAudioSession.sharedInstance()
        // Recording (dictation) owns the session; don't take it over.
        guard session.category != .record, session.category != .playAndRecord else { return }
        do {
            try session.setCategory(.ambient, mode: .default, options: [.mixWithOthers])
            try session.setActive(true)
        } catch {
            return
        }
        playNext()
    }

    private func playNext() {
        guard let name = bag.next(), let url = Bundle.main.url(forResource: name, withExtension: "m4a"),
              let next = try? AVAudioPlayer(contentsOf: url) else {
            halt()
            return
        }
        next.delegate = self
        next.volume = 0
        next.prepareToPlay()
        next.play()
        next.setVolume(Self.volume, fadeDuration: Self.fadeIn)
        player = next
        grid = Self.grid(for: name)
        isPlaying = true
    }

    private func halt() {
        player?.stop()
        player = nil
        grid = nil
        isPlaying = false
    }

    /// The relaxed beat motion right now, or nil when no music plays (the stage
    /// then uses its own calm idle). Read once per stage frame.
    func calm() -> ZenCalm? {
        guard let player, player.isPlaying, let grid else { return nil }
        let latency = AVAudioSession.sharedInstance().outputLatency
        return ZenCalm(grid.state(at: player.currentTime, latency: latency), beatsPerBar: grid.beatsPerBar)
    }

    private static func grid(for track: String) -> ZenBeatGrid? {
        guard let url = Bundle.main.url(forResource: track + ".beats", withExtension: "json"),
              let data = try? Data(contentsOf: url),
              let grid = try? JSONDecoder().decode(ZenBeatGrid.self, from: data), grid.isValid else { return nil }
        return grid
    }

    nonisolated func audioPlayerDidFinishPlaying(_ finished: AVAudioPlayer, successfully flag: Bool) {
        Task { @MainActor in
            guard self.player === finished else { return }
            self.player = nil
            if self.wanted, self.enabled { self.playNext() } else { self.isPlaying = false }
        }
    }
}
