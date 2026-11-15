//
//  ZenParityTests.swift
//  eggifyTests
//
//  The iOS Circus stage must make the same choices as the web app's, so a
//  session looks the same on the phone and the desktop. Expected values were
//  computed with the web sources (zen/types.ts hash01, circusBackdrops.ts,
//  circusMessages.ts, circusGags.ts hatFor, utils/beatClock.ts).
//

import Foundation
import Testing
@testable import eggify

@Suite("Zen parity with the web")
@MainActor
struct ZenParityTests {
    // MARK: Backdrops

    @Test func backdropHashMatchesTheWeb() {
        let expected: [String: Int] = [
            "": 8, "a": 7, "session-1": 2, "demo-a": 3, "héllo": 0,
            "7c9e6679-7425-40de-944b-e07fc1f90ae7": 4,
            "3b241101-e2bb-4255-8caf-4136c566a962": 5,
        ]
        for (id, index) in expected {
            #expect(CircusBackdrops.hashIndex(id) == index, "\(id)")
        }
        #expect(CircusBackdropID.allCases.map(\.rawValue) ==
                ["meadow", "dusk", "storm", "tearoom", "snow", "seaside", "desert", "jungle", "gothic", "space"])
    }

    @Test func assignerSpreadsOpenSessionsLikeTheWeb() {
        let ids = ["a", "b", "c", "d", "e", "f", "g", "h", "i", "j", "k", "l"]
        var assigner = CircusBackdropAssigner()
        assigner.assignAll(ids)
        let expected = ["a": 8, "b": 2, "c": 9, "d": 8, "e": 3, "f": 9, "g": 7, "h": 4, "i": 0, "j": 1, "k": 5, "l": 6]
        #expect(assigner.given == expected)
        // Once given, a session keeps its backdrop.
        #expect(assigner.assign("a", open: ["x"]) == 8)
    }

    @Test func assignerRestoresOnlyValidIndices() {
        let assigner = CircusBackdropAssigner(restore: ["ok": 3, "bad": 10, "neg": -1])
        #expect(assigner.given == ["ok": 3])
    }

    // MARK: Hats

    @Test func hatsMatchTheWeb() {
        func hats(_ id: String) -> [String] { [0, 3, 6].map { CircusAmbient.hat(sessionID: id, turn: $0) ?? "bowler" } }
        #expect(hats("session-1") == ["chef", "general", "bandsman"])
        #expect(hats("demo-a") == ["general", "bandsman", "tophat"])
        #expect(hats("7c9e6679-7425-40de-944b-e07fc1f90ae7") == ["tophat", "bowler", "crown"])
    }

    // MARK: Letters messages

    @Test func sanitiserMatchesTheWeb() {
        let cases: [(String, String)] = [
            ("Crème brûlée — déjà vu!", "CREME BRULEE DEJA VU!"),
            ("hello   world", "HELLO WORLD"),
            ("a very long sentence that goes on and on past the limit", "A VERY LONG SENTENCE THAT"),
            ("Ünïcödé 😀 emoji, ok?", "UNICODE EMOJI, OK?"),
            ("supercalifragilisticexpialidocious-word", "SUPERCALIFRAGILISTICEXPIALID"),
            ("it's (fine) [ok]", "IT'S FINE OK"),
            ("...trailing, cut here. more more more words", "...TRAILING, CUT HERE. MORE"),
        ]
        for (input, output) in cases {
            #expect(CircusMessages.sanitize(input) == output, "\(input)")
        }
    }

    @Test func wrapAndSummaryMatchTheWeb() {
        #expect(CircusMessages.wrap("SPLENDID, A GREEN BUILD!") == ["SPLENDID, A GREEN", "BUILD!"])
        #expect(CircusMessages.wrap("ABCDEFGHIJKLMNOPQRSTUVWXYZ0123") == ["ABCDEFGHIJKLMNOPQR", "STUVWXYZ0123"])
        #expect(CircusMessages.shortenSummary("Porting the iOS Zen stage to match the web. More later.") == "PORTING THE IOS ZEN")
        #expect(CircusMessages.shortenSummary("Hi.") == nil)
    }

    @Test func everyPoolLineFitsTheTiles() {
        for (pool, lines) in CircusMessages.jokes {
            #expect(!lines.isEmpty, "\(pool)")
            for line in lines {
                let clean = CircusMessages.sanitize(line)
                #expect(clean.count <= CircusMessages.maxMessageChars, "\(line)")
                #expect(clean == CircusMessages.sanitize(line, max: 200), "\(line) must not be cut")
                #expect(CircusMessages.wrap(clean).joined(separator: " ") == clean, "\(line) must fit two lines")
            }
        }
        #expect(CircusMessages.jokes[.general]!.count > CircusMessages.recentWindow)
    }

    @Test func pickerNeverRepeatsWithinTheWindowAndNeverTwoSpecialsInARow() {
        let picker = CircusMessagePicker()
        var specials: [String] = []
        var previousSpecial = false
        var seed: UInt64 = 42
        let rng: () -> Double = {
            seed = seed &* 6364136223846793005 &+ 1442695040888963407
            return Double(seed >> 11) / Double(1 << 53)
        }
        for i in 0..<400 {
            let m = picker.next(context: CircusMessageContext(summary: "Porting the iOS Zen stage"),
                                activity: CircusActivityState(now: 0), idle: false, now: Double(i), rng: rng)
            if m.kind == .word {
                previousSpecial = false
                continue
            }
            #expect(!previousSpecial, "two specials in a row")
            previousSpecial = true
            #expect(!specials.suffix(CircusMessages.recentWindow).contains(m.text), "\(m.text) repeated")
            #expect(m.text.count <= CircusMessages.maxMessageChars)
            #expect(m.text == CircusMessages.sanitize(m.text))
            specials.append(m.text)
        }
        #expect(specials.count > 50)
        #expect(specials.contains("PORTING THE IOS ZEN"))
    }

    @Test func contextPools() {
        var a = CircusActivityState(now: 1000)
        #expect(a.pool(idle: false, now: 1010) == .general)
        #expect(a.pool(idle: true, now: 1010) == .done)
        a.note(.bash, at: 1000)
        #expect(a.pool(idle: false, now: 1010) == .build)
        a.note(.error, isError: true, at: 1000)
        #expect(a.pool(idle: false, now: 1010) == .oops)
        #expect(a.pool(idle: false, now: 1200) == .idle)
    }

    @Test func topicPhrasesUseWordStarts() {
        #expect(CircusMessages.topicPhrase("Please fix the login") == "Squashing a bug")
        #expect(CircusMessages.topicPhrase("run the tests") == "Fixing the tests")
        #expect(CircusMessages.topicPhrase("latest news") == nil)  // "test" inside a word does not count
    }

    // MARK: Ambient gags

    @Test func ambientRespectsGapStageAndBackdrops() {
        #expect(CircusAmbient.pick(now: 100, last: [:], lastAny: 90, onStage: 0, playing: false,
                                   backdrop: .meadow, rand: { 0.5 }) == nil)
        #expect(CircusAmbient.pick(now: 100, last: [:], lastAny: 0, onStage: 3, playing: false,
                                   backdrop: .meadow, rand: { 0.5 }) == nil)
        for _ in 0..<200 {
            let pick = CircusAmbient.pick(now: 1000, last: [:], lastAny: 0, onStage: 0, playing: false,
                                          backdrop: .tearoom, rand: { Double.random(in: 0..<1) })
            #expect(pick != nil)
            // No balloon indoors, no gramophone without music, no shooting star in a lit room.
            #expect(![.balloon, .gramophone, .shootingstar, .fish, .train].contains(pick?.prop))
        }
        let star = CircusAmbient.pick(now: 1000, last: ["tea": 999, "stalk": 999, "crossing:bicycle": 999,
                                                        "crossing:cat": 999, "crossing:rabbit": 999, "crossing:bird": 999,
                                                        "crossing:train": 999],
                                      lastAny: 0, onStage: 0, playing: false, backdrop: .storm, rand: { 0.1 })
        #expect(star == CircusAmbientPick(kind: .crossing, prop: .shootingstar))
    }

    // MARK: Beat clock and relaxed motion

    @Test func calmMotionMatchesTheWeb() {
        #expect([0, 1, 2, 5, -3, 1000].map(ZenCalm.hash01) == [0, 0.761, 0.226, 0.917, 0.309, 0.072])
        #expect([0, 1, 2, 3, 4, 6, 8, 10, -2].map(ZenCalm.barAccents) ==
                [true, false, true, false, true, true, false, false, true])
        let grid = ZenBeatGrid(bpm: 96.945, firstBeat: 0.125, beatsPerBar: 4, downbeatOffset: 2)
        let cases: [(Double, Int, Int, Int, Double, Double, Double, Double)] = [
            // time, beatIndex, bar, beatInBar, sway, breath, step, stepDir
            (0, -1, -1, 1, -0.26118571301530924, 0.9750481666910669, 0.09731695729382568, -1),
            (1.3, 1, -1, 3, 0.10318848898063332, 0.006340716075681596, 0.025202045581316647, 1),
            (7.77, 12, 2, 2, 0.466472166763481, 0.9253285556055562, 0.27638247914596564, 1),
            (42.5, 68, 16, 2, 0.5897952708482082, 0.8711834859339351, 0.4488912790797287, 1),
        ]
        for c in cases {
            let s = grid.state(at: c.0)
            #expect(s.beatIndex == c.1 && s.bar == c.2 && s.beatInBar == c.3, "\(c.0)")
            let calm = ZenCalm(s)
            #expect(abs(calm.sway - c.4) < 1e-9 && abs(calm.breath - c.5) < 1e-9, "\(c.0)")
            #expect(abs(calm.step - c.6) < 1e-9 && calm.stepDir == c.7, "\(c.0)")
            #expect(calm.nod == 0)
        }
    }

    @Test func bundledBeatGridsDecode() throws {
        for name in ZenMusicPlayer.trackNames {
            let url = try #require(Bundle(for: ZenMusicPlayer.self).url(forResource: name + ".beats", withExtension: "json"))
            let grid = try JSONDecoder().decode(ZenBeatGrid.self, from: Data(contentsOf: url))
            #expect(grid.isValid, "\(name)")
            #expect(Bundle(for: ZenMusicPlayer.self).url(forResource: name, withExtension: "m4a") != nil)
        }
    }

    // MARK: Music shuffle bag

    @Test func shuffleBagPlaysEveryTrackOnceBeforeRepeating() {
        let tracks = ["a", "b", "c", "d", "e"]
        var bag = ZenShuffleBag(tracks: tracks)
        var played: [String] = []
        for _ in 0..<50 { played.append(bag.next()!) }
        for round in 0..<10 {
            #expect(Set(played[round * 5..<(round + 1) * 5]) == Set(tracks))
        }
        for i in 1..<played.count { #expect(played[i] != played[i - 1], "same track twice in a row at \(i)") }
    }

    @Test func shuffleBagHandlesOneOrNoTracks() {
        var one = ZenShuffleBag(tracks: ["only"])
        #expect(one.next() == "only")
        #expect(one.next() == "only")
        var none = ZenShuffleBag(tracks: [])
        #expect(none.next() == nil)
    }
}
