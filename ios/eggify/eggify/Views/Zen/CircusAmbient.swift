//
//  CircusAmbient.swift
//  eggify
//
//  Rare "ambient" gags on the Circus stage, ported from the web's
//  zen/circusGags.ts: the announcer banner on a new turn, a tea parade, a
//  bureaucrat's head on a stalk, and props crossing the stage (balloon,
//  bicycle, train, fish, cat, rabbit, bird, shooting star, gramophone). Each
//  has a cooldown, some only suit certain backdrops, and at most three are on
//  stage. The coconut clip-clop needs the beat clock and is web only. Also the
//  egg gentleman's hats (one per session, changing every few turns).
//

import Foundation

enum CircusCrossingProp: String, CaseIterable, Sendable {
    case balloon, bicycle, train, fish, cat, shootingstar, gramophone, rabbit, bird

    var asset: String { "CircusProp" + rawValue.prefix(1).uppercased() + rawValue.dropFirst() }

    /// Native art size (width, height) of the cut-out, for its aspect ratio.
    var artSize: CGSize {
        switch self {
        case .balloon: CGSize(width: 140, height: 190)
        case .bicycle: CGSize(width: 200, height: 130)
        case .train: CGSize(width: 340, height: 150)
        case .fish: CGSize(width: 260, height: 130)
        case .cat: CGSize(width: 220, height: 200)
        case .shootingstar: CGSize(width: 220, height: 80)
        case .gramophone: CGSize(width: 150, height: 150)
        case .rabbit: CGSize(width: 110, height: 170)
        case .bird: CGSize(width: 150, height: 110)
        }
    }

    enum Lane: Sendable { case sky, ground, horizon, diagonal }

    /// The web's CROSS table: height as a share of the stage, seconds on stage,
    /// lane, hop height (pt) and wave (share of the stage height).
    var crossing: (height: Double, seconds: Double, lane: Lane, hop: Double, wave: Double) {
        switch self {
        case .balloon: (0.3, 18, .sky, 6, 0.03)
        case .bicycle: (0.19, 9, .ground, 8, 0)
        case .train: (0.17, 10, .horizon, 4, 0)
        case .fish: (0.15, 12, .sky, 10, 0.07)
        case .cat: (0.42, 14, .ground, 8, 0)
        case .shootingstar: (0.06, 1.8, .diagonal, 0, 0)
        case .gramophone: (0.18, 10, .ground, 14, 0)
        case .rabbit: (0.17, 8, .ground, 34, 0)
        case .bird: (0.11, 10, .sky, 8, 0.06)
        }
    }
}

enum CircusAmbientKind: String, Sendable { case banner, tea, stalk, crossing }

struct CircusAmbientPick: Equatable, Sendable {
    let kind: CircusAmbientKind
    var prop: CircusCrossingProp?
    /// Key for cooldowns, as on the web (`stalk`, `crossing:cat`).
    var key: String { prop.map { "\(kind.rawValue):\($0.rawValue)" } ?? kind.rawValue }
}

enum CircusAmbient {
    struct Rule: Sendable {
        let kind: CircusAmbientKind
        var prop: CircusCrossingProp?
        let cooldown: TimeInterval
        let weight: Double
        var needsMusic = false
        var backdrops: Set<CircusBackdropID>?
    }

    private static let darkSkies: Set<CircusBackdropID> = [.dusk, .storm, .gothic, .space]

    static let rules: [Rule] = [
        Rule(kind: .tea, cooldown: 240, weight: 3),
        Rule(kind: .stalk, cooldown: 150, weight: 3),
        Rule(kind: .crossing, prop: .balloon, cooldown: 180, weight: 2, backdrops: [.meadow, .snow, .seaside, .desert]),
        Rule(kind: .crossing, prop: .bicycle, cooldown: 180, weight: 2),
        Rule(kind: .crossing, prop: .train, cooldown: 240, weight: 2, backdrops: [.meadow, .snow, .desert, .dusk, .storm]),
        Rule(kind: .crossing, prop: .fish, cooldown: 200, weight: 3, backdrops: [.seaside, .jungle, .dusk]),
        Rule(kind: .crossing, prop: .cat, cooldown: 300, weight: 2),
        Rule(kind: .crossing, prop: .shootingstar, cooldown: 120, weight: 3, backdrops: darkSkies),
        Rule(kind: .crossing, prop: .gramophone, cooldown: 240, weight: 2, needsMusic: true, backdrops: [.tearoom, .meadow, .snow]),
        Rule(kind: .crossing, prop: .rabbit, cooldown: 220, weight: 2),
        Rule(kind: .crossing, prop: .bird, cooldown: 160, weight: 2),
    ]

    /// The quietest the stage may be between two ambient gags, and how full it may be to add one.
    static let minGap: TimeInterval = 25
    static let maxOnStage = 3
    /// How long the stage must have been without an ambient gag before an idle one is due.
    static let idleAfter: TimeInterval = 45
    /// The chance, checked once a second, that a due gag actually plays.
    static let chancePerSecond = 0.05
    static let bannerCooldown: TimeInterval = 180

    /// Choose an ambient gag, or nil when it is too soon or the stage is full.
    static func pick(now: TimeInterval, last: [String: TimeInterval], lastAny: TimeInterval,
                     onStage: Int, playing: Bool, backdrop: CircusBackdropID,
                     rand: () -> Double) -> CircusAmbientPick? {
        guard onStage < maxOnStage, now - lastAny >= minGap else { return nil }
        let ok = rules.filter { r in
            let key = CircusAmbientPick(kind: r.kind, prop: r.prop).key
            return (!r.needsMusic || playing)
                && (r.backdrops?.contains(backdrop) ?? true)
                && now - (last[key] ?? -.infinity) >= r.cooldown
        }
        let total = ok.reduce(0) { $0 + $1.weight }
        guard total > 0, let lastRule = ok.last else { return nil }
        var x = rand() * total
        for r in ok {
            x -= r.weight
            if x <= 0 { return CircusAmbientPick(kind: r.kind, prop: r.prop) }
        }
        return CircusAmbientPick(kind: lastRule.kind, prop: lastRule.prop)
    }

    /// Announcer banner lines for a new turn (the web's BANNER_LINES).
    static let bannerLines = [
        "And now, something rather different",
        "A brief pause for tea",
        "Please remain calmly seated",
        "The egg will now think",
        "Meanwhile, in a different hat",
        "Entirely unrelated, but important",
        "Stand by for paperwork",
        "Nobody panic, it is only code",
    ]

    /// What the bureaucrat on the stalk says.
    static let stalkLines = [
        "Form 27b, if you please", "Please queue here", "Your hat is in the wrong form", "Next!",
        "Stamped, signed, and slightly damp", "Is this the department of tea?",
    ]

    /// The egg's hats, in the web's order; `bowler` is his own (no extra hat).
    static let hats = ["tophat", "bowler", "crown", "teacosy", "mitre", "chef", "general", "bandsman"]

    /// The hat for this session and turn: the same session wears the same hat
    /// for a run of three turns (the web's `hatFor`). nil = his own bowler.
    static func hat(sessionID: String, turn: Int) -> String? {
        let seed = CircusBackdrops.hash01(sessionID, salt: 31)
        let i = Int((seed * Double(hats.count) + Double(turn / 3)).truncatingRemainder(dividingBy: Double(hats.count)))
        let hat = hats[i]
        return hat == "bowler" ? nil : hat
    }

    /// Native art sizes of the hats (aspect ratio).
    static func hatSize(_ hat: String) -> CGSize {
        switch hat {
        case "tophat", "teacosy": CGSize(width: 100, height: 80)
        case "mitre", "chef": CGSize(width: 100, height: 90)
        case "bandsman": CGSize(width: 100, height: 100)
        default: CGSize(width: 100, height: 70)
        }
    }

    static func hatAsset(_ hat: String) -> String { "CircusHat" + hat.prefix(1).uppercased() + hat.dropFirst() }
}
