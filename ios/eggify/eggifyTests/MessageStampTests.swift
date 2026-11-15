//
//  MessageStampTests.swift
//  eggifyTests
//
//  The time/date stamp under chat messages.
//

import Foundation
import Testing
@testable import eggify

@Suite("Message stamp")
struct MessageStampTests {
    // A fixed local "now": Thursday 8 Oct 2026, 19:30 in a fixed time zone.
    private static let calendar: Calendar = {
        var c = Calendar(identifier: .gregorian)
        c.timeZone = TimeZone(identifier: "Europe/Copenhagen")!
        return c
    }()
    private static let locale = Locale(identifier: "en_GB")
    private static func date(_ y: Int, _ mo: Int, _ d: Int, _ h: Int, _ mi: Int) -> Date {
        calendar.date(from: DateComponents(year: y, month: mo, day: d, hour: h, minute: mi))!
    }
    private let now = date(2026, 10, 8, 19, 30)

    private func stamp(_ date: Date) -> String {
        EggifyDate.messageStamp(date, now: now, calendar: Self.calendar, locale: Self.locale)
    }

    @Test func todayShowsTimeOnly() {
        #expect(stamp(Self.date(2026, 10, 8, 19, 2)) == "19:02")
        #expect(stamp(Self.date(2026, 10, 8, 0, 5)) == "0:05")
    }

    @Test func yesterday() {
        #expect(stamp(Self.date(2026, 10, 7, 23, 59)) == "Yesterday 23:59")
    }

    @Test func thisWeekShowsWeekday() {
        #expect(stamp(Self.date(2026, 10, 5, 9, 15)) == "Mon 9:15")
    }

    @Test func olderThisYearShowsDayAndMonth() {
        #expect(stamp(Self.date(2026, 9, 30, 14, 0)) == "30 Sep, 14:00")
        #expect(stamp(Self.date(2026, 3, 3, 8, 0)) == "3 Mar, 8:00")
    }

    @Test func otherYearShowsYear() {
        #expect(stamp(Self.date(2025, 10, 3, 17, 1)) == "3 Oct 2025, 17:01")
    }
}
