//
//  ZenBeat.swift
//  eggify
//
//  The Circus beat clock (the web's utils/beatClock.ts): each music track ships
//  a tempo map (`<track>.beats.json`, written by scripts/zen-beatgrid.mjs), and
//  the playing position gives the beat. `calmEnvelopes` is the relaxed motion
//  of the web (BEAT_MOTION): a slow sway every two beats, a breath per bar, a
//  soft nod on some downbeats, walkers stepping at half tempo.
//

import Foundation

struct ZenBeatGrid: Decodable, Equatable {
    let bpm: Double
    /// Seconds from the start of the file to the first beat.
    let firstBeat: Double
    let beatsPerBar: Int
    /// Beat index (0..beatsPerBar-1, from firstBeat) that starts a bar.
    let downbeatOffset: Int

    var isValid: Bool {
        bpm >= 40 && bpm <= 240 && firstBeat >= 0 && beatsPerBar >= 1 && beatsPerBar <= 16
            && downbeatOffset >= 0 && downbeatOffset < beatsPerBar
    }

    struct State: Equatable {
        var beatIndex: Int
        var phase: Double
        var beatInBar: Int
        var bar: Int
    }

    private static func mod(_ a: Int, _ n: Int) -> Int { ((a % n) + n) % n }

    /// Beat state at `time` seconds into the file (the web's `beatStateAt`).
    func state(at time: Double, latency: Double = 0) -> State {
        let t = time - max(0, latency)
        let period = 60 / bpm
        let beatsF = (t - firstBeat) / period
        let beatIndex = Int(beatsF.rounded(.down))
        let phase = beatsF - Double(beatIndex)
        let shifted = beatIndex - downbeatOffset
        let bar = Int((Double(shifted) / Double(beatsPerBar)).rounded(.down))
        return State(beatIndex: beatIndex, phase: phase,
                     beatInBar: Self.mod(shifted, beatsPerBar), bar: bar)
    }
}

/// The web's BEAT_MOTION constants.
enum ZenBeatMotion {
    static let swayBeats = 2.0
    static let accentEveryBars = 2
    static let accentSkip = 0.25
    static let accentBeats = 2.0
    static let barBeats = 2.0
    static let walkerDivisor = 2
    static let swayMinAmp = 0.65
    static let energyIdle = 1.0
    static let energyWorking = 1.3
}

struct ZenCalm: Equatable {
    /// -1...1, a slow lean.
    var sway = 0.0
    /// 0...1, once per bar.
    var breath = 0.0
    /// 0...1, a soft accent on some downbeats.
    var nod = 0.0
    /// 0...1, a bump on every downbeat.
    var bar = 0.0
    /// 0...1 and ±1: walkers at half tempo.
    var step = 0.0
    var stepDir = 1.0

    /// Deterministic 0..1 from an integer (the web's Knuth hash).
    static func hash01(_ n: Int) -> Double {
        Double((UInt32(truncatingIfNeeded: n) &* 2654435761) % 1000) / 1000
    }

    /// True when the bar's downbeat carries the soft accent.
    static func barAccents(_ bar: Int) -> Bool {
        let every = max(1, ZenBeatMotion.accentEveryBars)
        if ((bar % every) + every) % every != 0 { return false }
        return hash01(bar * 7 + 3) >= ZenBeatMotion.accentSkip
    }

    private static func bump(_ x: Double) -> Double {
        x > 0 && x < 1 ? pow(sin(.pi * x), 2) : 0
    }

    /// The web's `calmEnvelopes`.
    init(_ s: ZenBeatGrid.State, beatsPerBar: Int = 4) {
        let m = ZenBeatMotion.self
        let t = Double(s.beatIndex) + s.phase
        let x = t / m.swayBeats
        let k = Int(x.rounded(.down))
        let odd = ((k % 2) + 2) % 2 == 1
        sway = sin(.pi * (x - Double(k))) * (odd ? -1 : 1) * (m.swayMinAmp + (1 - m.swayMinAmp) * Self.hash01(k))
        let inBar = Double(s.beatInBar) + s.phase
        breath = 0.5 - 0.5 * cos(2 * .pi * inBar / Double(max(1, beatsPerBar)))
        nod = Self.barAccents(s.bar) ? Self.bump(inBar / m.accentBeats) : 0
        bar = Self.bump(inBar / m.barBeats)
        let div = max(1, m.walkerDivisor)
        let stepIdx = Int((Double(s.beatIndex) / Double(div)).rounded(.down))
        step = Self.bump((Double(((s.beatIndex % div) + div) % div) + s.phase) / Double(div))
        stepDir = ((stepIdx % 2) + 2) % 2 == 1 ? -1 : 1
    }

    init() {}
}
