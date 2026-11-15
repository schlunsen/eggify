//
//  JSONValue.swift
//  eggify
//
//  A small, Equatable/Sendable representation of arbitrary JSON plus helpers
//  for reading loosely-typed `[String: Any]` payloads from the server.
//

import Foundation

nonisolated enum JSONValue: Equatable, Hashable, Sendable {
    case string(String)
    case number(Double)
    case bool(Bool)
    case object([String: JSONValue])
    case array([JSONValue])
    case null

    /// Build from the output of `JSONSerialization` (or any plain Swift value).
    init(any value: Any?) {
        switch value {
        case nil:
            self = .null
        case let v as JSONValue:
            self = v
        case let n as NSNumber:
            if CFGetTypeID(n) == CFBooleanGetTypeID() {
                self = .bool(n.boolValue)
            } else {
                self = .number(n.doubleValue)
            }
        case let s as String:
            self = .string(s)
        case let b as Bool:
            self = .bool(b)
        case let i as Int:
            self = .number(Double(i))
        case let d as Double:
            self = .number(d)
        case let dict as [String: Any]:
            self = .object(dict.mapValues { JSONValue(any: $0) })
        case let arr as [Any]:
            self = .array(arr.map { JSONValue(any: $0) })
        case is NSNull:
            self = .null
        default:
            self = .string(String(describing: value!))
        }
    }

    var stringValue: String? {
        if case .string(let s) = self { return s }
        return nil
    }

    var doubleValue: Double? {
        switch self {
        case .number(let d): return d
        case .string(let s): return Double(s)
        default: return nil
        }
    }

    var intValue: Int? { doubleValue.map { Int($0) } }

    var boolValue: Bool? {
        if case .bool(let b) = self { return b }
        return nil
    }

    var objectValue: [String: JSONValue]? {
        if case .object(let o) = self { return o }
        return nil
    }

    var arrayValue: [JSONValue]? {
        if case .array(let a) = self { return a }
        return nil
    }

    subscript(key: String) -> JSONValue? {
        objectValue?[key]
    }

    /// Plain Swift value suitable for `JSONSerialization`.
    var anyValue: Any {
        switch self {
        case .string(let s): return s
        case .number(let d):
            if d.rounded() == d, abs(d) < 1e15 { return Int(d) }
            return d
        case .bool(let b): return b
        case .object(let o): return o.mapValues { $0.anyValue }
        case .array(let a): return a.map { $0.anyValue }
        case .null: return NSNull()
        }
    }

    /// Text rendering: strings as-is, everything else as pretty JSON.
    var displayText: String {
        switch self {
        case .string(let s): return s
        case .null: return ""
        case .number, .bool:
            return "\(anyValue)"
        case .object, .array:
            guard JSONSerialization.isValidJSONObject(anyValue),
                  let data = try? JSONSerialization.data(withJSONObject: anyValue, options: [.prettyPrinted, .sortedKeys, .withoutEscapingSlashes]),
                  let text = String(data: data, encoding: .utf8) else {
                return "\(anyValue)"
            }
            return text
        }
    }
}

// MARK: - Loose dictionary access

nonisolated extension Dictionary where Key == String, Value == Any {
    func string(_ key: String) -> String? {
        switch self[key] {
        case let s as String: return s
        case let n as NSNumber: return n.stringValue
        default: return nil
        }
    }

    func nonEmptyString(_ key: String) -> String? {
        guard let s = string(key), !s.isEmpty else { return nil }
        return s
    }

    func int(_ key: String) -> Int? {
        switch self[key] {
        case let n as NSNumber: return n.intValue
        case let s as String: return Int(s)
        default: return nil
        }
    }

    func int64(_ key: String) -> Int64? {
        switch self[key] {
        case let n as NSNumber: return n.int64Value
        case let s as String: return Int64(s)
        default: return nil
        }
    }

    func double(_ key: String) -> Double? {
        switch self[key] {
        case let n as NSNumber: return n.doubleValue
        case let s as String: return Double(s)
        default: return nil
        }
    }

    func bool(_ key: String) -> Bool? {
        switch self[key] {
        case let n as NSNumber: return n.boolValue
        case let s as String: return s == "true" ? true : (s == "false" ? false : nil)
        default: return nil
        }
    }

    func dict(_ key: String) -> [String: Any]? {
        self[key] as? [String: Any]
    }

    func dicts(_ key: String) -> [[String: Any]] {
        (self[key] as? [Any])?.compactMap { $0 as? [String: Any] } ?? []
    }

    func strings(_ key: String) -> [String] {
        switch self[key] {
        case let s as String: return [s]
        case let arr as [Any]: return arr.compactMap { $0 as? String }
        default: return []
        }
    }
}

// MARK: - Dates

nonisolated enum EggifyDate {
    nonisolated(unsafe) private static let fractional: ISO8601DateFormatter = {
        let f = ISO8601DateFormatter()
        f.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        return f
    }()

    nonisolated(unsafe) private static let plain: ISO8601DateFormatter = {
        let f = ISO8601DateFormatter()
        f.formatOptions = [.withInternetDateTime]
        return f
    }()

    private static let lock = NSLock()

    static func parse(_ string: String?) -> Date? {
        guard let string, !string.isEmpty else { return nil }
        lock.lock(); defer { lock.unlock() }
        if let d = fractional.date(from: string) { return d }
        if let d = plain.date(from: string) { return d }
        // Go may emit nanosecond precision, which ISO8601DateFormatter rejects;
        // trim the fraction to milliseconds and retry.
        if let dot = string.firstIndex(of: ".") {
            let afterDot = string[string.index(after: dot)...]
            let digits = afterDot.prefix(while: { $0.isNumber })
            let rest = afterDot.dropFirst(digits.count)
            let trimmed = String(string[..<dot]) + "." + String(digits.prefix(3)) + rest
            if let d = fractional.date(from: trimmed) { return d }
        }
        return nil
    }

    nonisolated(unsafe) private static let relativeFormatter: RelativeDateTimeFormatter = {
        let f = RelativeDateTimeFormatter()
        f.unitsStyle = .abbreviated
        return f
    }()

    nonisolated(unsafe) private static let mediumFormatter: DateFormatter = {
        let f = DateFormatter()
        f.dateStyle = .medium
        f.timeStyle = .short
        return f
    }()

    /// "5 min. ago" for an ISO 8601 string; `fallback` if it doesn't parse.
    /// Formatters are shared: making them per row is slow.
    static func relative(_ string: String?, fallback: String = "Unknown") -> String {
        guard let date = parse(string) else { return fallback }
        return relative(date)
    }

    static func relative(_ date: Date) -> String {
        lock.lock(); defer { lock.unlock() }
        return relativeFormatter.localizedString(for: date, relativeTo: Date())
    }

    /// "3 Oct 2026 at 17:01" for an ISO 8601 string; `fallback` if it doesn't parse.
    static func medium(_ string: String?, fallback: String = "Unknown") -> String {
        guard let date = parse(string) else { return fallback }
        lock.lock(); defer { lock.unlock() }
        return mediumFormatter.string(from: date)
    }

    /// The stamp under a chat message: "19:02" (today), "Yesterday 19:02",
    /// "Mon 19:02" (this week), "3 Oct, 19:02" (this year), "3 Oct 2025, 19:02".
    static func messageStamp(_ date: Date, now: Date = Date(), calendar: Calendar = .current,
                             locale: Locale = .current) -> String {
        var calendar = calendar
        calendar.locale = locale
        func style(_ base: Date.FormatStyle) -> Date.FormatStyle {
            var s = base
            s.calendar = calendar
            s.timeZone = calendar.timeZone
            s.locale = locale
            return s
        }
        let time = date.formatted(style(Date.FormatStyle(date: .omitted, time: .shortened)))
        if calendar.isDate(date, inSameDayAs: now) { return time }
        if calendar.isDate(date, inSameDayAs: calendar.date(byAdding: .day, value: -1, to: now) ?? now) {
            return "Yesterday \(time)"
        }
        let startOfToday = calendar.startOfDay(for: now)
        if date < startOfToday, startOfToday.timeIntervalSince(date) < 6 * 86_400 {
            return "\(date.formatted(style(Date.FormatStyle().weekday(.abbreviated)))) \(time)"
        }
        let day = calendar.component(.year, from: date) == calendar.component(.year, from: now)
            ? style(Date.FormatStyle().day().month(.abbreviated))
            : style(Date.FormatStyle().day().month(.abbreviated).year())
        return "\(date.formatted(day)), \(time)"
    }

    static func string(from date: Date = Date()) -> String {
        lock.lock(); defer { lock.unlock() }
        return fractional.string(from: date)
    }
}
