//
//  QuestionPolicyTests.swift
//  eggifyTests
//
//  Agent question policy: the new `user_question` fields (and older servers
//  without them), session policy fields, the auto-pick label and the
//  countdown text.
//

import Foundation
import Testing
@testable import eggify

@Suite("Question policy")
@MainActor
struct QuestionPolicyTests {
    private func question(_ extra: [String: Any] = [:]) -> [String: Any] {
        var q: [String: Any] = [
            "type": "user_question", "session_id": "s1", "question_id": "q1",
            "question": "Which status code?", "header": "Status",
            "options": [["label": "400 Bad Request", "description": ""],
                        ["label": "422 Unprocessable (Recommended)", "description": ""]],
            "multi_select": false,
        ]
        q.merge(extra) { _, new in new }
        return q
    }

    @Test func parsesNewQuestionFields() throws {
        let q = try #require(UserQuestion.parse(question([
            "recommended": [1], "auto_answer_at": "2026-10-07T12:01:42.123456789Z",
            "held": false, "session_name": "Fix login",
        ])))
        #expect(q.recommended == [1])
        #expect(q.autoAnswerAt == EggifyDate.parse("2026-10-07T12:01:42.123Z"))
        #expect(q.held == false)
        #expect(q.sessionName == "Fix login")
        #expect(q.showsCountdown)
        #expect(q.recommendedIndexes == [1])
    }

    @Test func olderServerWithoutNewFieldsStillDecodes() throws {
        let q = try #require(UserQuestion.parse(question()))
        #expect(q.recommended == nil)
        #expect(q.autoAnswerAt == nil)
        #expect(q.held == false)
        #expect(q.sessionName == nil)
        #expect(!q.showsCountdown)
        #expect(q.countdownText() == nil)
        // The label still marks it.
        #expect(q.recommendedIndexes == [1])
    }

    @Test func nullTimerAndHeld() throws {
        let q = try #require(UserQuestion.parse(question(["auto_answer_at": NSNull(), "held": true, "recommended": []])))
        #expect(q.autoAnswerAt == nil)
        #expect(q.held)
        #expect(!q.showsCountdown)
        #expect(q.recommendedIndexes.isEmpty)
    }

    @Test func timerEventUpdatesQuestion() throws {
        let q = try #require(UserQuestion.parse(question(["auto_answer_at": "2026-10-07T12:00:00Z"])))
        let held = q.withTimer(["session_id": "s1", "question_id": "q1", "auto_answer_at": NSNull(), "held": true])
        #expect(held.held)
        #expect(held.autoAnswerAt == nil)
        let restarted = held.withTimer(["auto_answer_at": "2026-10-07T12:05:00Z", "held": false])
        #expect(restarted.autoAnswerAt == EggifyDate.parse("2026-10-07T12:05:00Z"))
        #expect(!restarted.held)
    }

    @Test func confirmationCardsNeverCountDown() throws {
        let q = try #require(UserQuestion.parse(question([
            "requires_user": true, "recommended": [], "auto_answer_at": "2026-10-07T12:00:00Z",
        ])))
        #expect(!q.showsCountdown)
        #expect(q.recommendedIndexes.isEmpty)
        #expect(q.countdownText() == nil)
    }

    @Test func autoPickLabels() throws {
        let single = try #require(UserQuestion.parse(question(["recommended": [1]])))
        #expect(single.autoPickLabels == ["422 Unprocessable (Recommended)"])

        let none = try #require(UserQuestion.parse(question([
            "options": [["label": "A"], ["label": "B"]], "recommended": [],
        ])))
        #expect(none.autoPickLabels == ["A"])

        let multi = try #require(UserQuestion.parse(question([
            "multi_select": true,
            "options": [["label": "Lint (Recommended)"], ["label": "Docs"], ["label": "Tests (recommended) "]],
        ])))
        #expect(multi.autoPickLabels == ["Lint (Recommended)", "Tests (recommended) "])

        #expect(UserQuestion.strippedLabel("Tests (recommended) ") == "Tests")
        #expect(UserQuestion.strippedLabel("Plain") == "Plain")
        #expect(UserQuestion.strippedLabel("(Recommended)") == "(Recommended)")
    }

    @Test func countdownText() throws {
        let at = try #require(EggifyDate.parse("2026-10-07T12:01:42Z"))
        let now = try #require(EggifyDate.parse("2026-10-07T12:00:00Z"))
        let q = try #require(UserQuestion.parse(question([
            "recommended": [1], "auto_answer_at": "2026-10-07T12:01:42Z",
        ])))
        #expect(q.countdownText(now: now) == "Auto-picks “422 Unprocessable” in 1:42")
        #expect(q.countdownText(now: at) == "Auto-picking “422 Unprocessable”…")
        #expect(q.bannerText(now: now) == "asks: Which status code? · auto in 1:42")

        let held = q.withTimer(["auto_answer_at": NSNull(), "held": true])
        #expect(held.bannerText(now: now) == "asks: Which status code?")
    }

    @Test func countdownFormatting() {
        #expect(QuestionCountdown.format(0) == "0:00")
        #expect(QuestionCountdown.format(-5) == "0:00")
        #expect(QuestionCountdown.format(4.2) == "0:05")
        #expect(QuestionCountdown.format(102) == "1:42")
        #expect(QuestionCountdown.format(720) == "12:00")
        #expect(QuestionCountdown.format(3723) == "1:02:03")
        #expect(QuestionCountdown.text(picks: ["A", "B"], remaining: 30) == "Auto-picks “A”, “B” in 0:30")
        #expect(QuestionCountdown.text(picks: [], remaining: 30) == "Auto-picks an answer in 0:30")
    }

    @Test func policyParsingAndLabels() {
        #expect(QuestionPolicy.parse(["mode": "wait"]) == .wait)
        #expect(QuestionPolicy.parse(["mode": "auto"]) == .auto)
        #expect(QuestionPolicy.parse(["mode": "auto_after", "timeout_seconds": 120]) == .autoAfter(120))
        #expect(QuestionPolicy.parse(["mode": "auto_after"]) == nil)
        #expect(QuestionPolicy.parse(["mode": "someday"]) == nil)
        #expect(QuestionPolicy.parse(NSNull()) == nil)
        #expect(QuestionPolicy.parse(nil) == nil)

        #expect(QuestionPolicy.wait.label == "Wait for me")
        #expect(QuestionPolicy.auto.label == "Auto-pick now")
        #expect(QuestionPolicy.autoAfter(30).label == "Auto-pick after 30 s")
        #expect(QuestionPolicy.autoAfter(60).label == "Auto-pick after 1 min")
        #expect(QuestionPolicy.autoAfter(90).label == "Auto-pick after 1 min 30 s")
        #expect(QuestionPolicy.autoAfter(1800).label == "Auto-pick after 30 min")
        #expect(QuestionPolicy.autoAfter(5400).label == "Auto-pick after 1 h 30 min")
        #expect(QuestionPolicyMenu.defaultLabel(.serverDefault) == "Default (Auto-pick after 2 min)")
        #expect(QuestionPolicyMenu.defaultLabel(nil) == "Default")

        #expect(QuestionPolicy.wait.json as NSDictionary == ["mode": "wait"] as NSDictionary)
        #expect(QuestionPolicy.autoAfter(300).json as NSDictionary
                == ["mode": "auto_after", "timeout_seconds": 300] as NSDictionary)
        #expect(QuestionPolicy.choices.count == 8)
    }

    @Test func sessionPolicyFields() throws {
        let plain = try #require(AgentSession.parse(["id": "s1", "status": "idle"]))
        #expect(plain.question_policy == nil)
        #expect(plain.question_policy_effective == nil)

        let s = try #require(AgentSession.parse([
            "id": "s1", "status": "idle",
            "question_policy": ["mode": "wait"],
            "question_policy_effective": ["mode": "wait"],
        ]))
        #expect(s.question_policy == .wait)
        #expect(s.question_policy_effective == .wait)

        // A live update without the fields keeps them; an explicit null clears the override.
        let kept = s.merged(with: ["status": "processing"])
        #expect(kept.question_policy == .wait)
        #expect(kept.question_policy_effective == .wait)
        let cleared = s.merged(with: ["question_policy": NSNull(),
                                      "question_policy_effective": ["mode": "auto_after", "timeout_seconds": 120]])
        #expect(cleared.question_policy == nil)
        #expect(cleared.question_policy_effective == .serverDefault)
    }
}
