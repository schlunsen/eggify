//
//  String+Date.swift
//  eggify
//
//  Date parsing and formatting utilities (formatters are cached).
//

import Foundation

extension String {
    /// Parses an ISO8601 date string (with or without fractional seconds).
    func parseISO8601Date() -> Date? {
        EggifyDate.parse(self)
    }

    /// Short time, e.g. "14:05".
    func formattedTime() -> String {
        guard let date = parseISO8601Date() else { return "" }
        return date.formatted(date: .omitted, time: .shortened)
    }

    /// Relative time, e.g. "5 min. ago".
    func relativeTime() -> String {
        guard let date = parseISO8601Date() else { return self }
        return date.relativeDescription
    }
}

extension Date {
    /// "now", "5m", "2h", "Yesterday", "3 Oct"
    var relativeDescription: String {
        let seconds = Date().timeIntervalSince(self)
        if seconds < 45 { return "now" }
        if seconds < 3600 { return "\(Int(seconds / 60))m ago" }
        if seconds < 86_400 { return "\(Int(seconds / 3600))h ago" }
        if Calendar.current.isDateInYesterday(self) { return "Yesterday" }
        if seconds < 7 * 86_400 { return formatted(.dateTime.weekday(.wide)) }
        return formatted(.dateTime.day().month(.abbreviated))
    }
}
