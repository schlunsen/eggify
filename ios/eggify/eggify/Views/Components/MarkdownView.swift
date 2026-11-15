//
//  MarkdownView.swift
//  eggify
//
//  Lightweight Markdown rendering for agent replies: fenced code blocks,
//  headings, lists (incl. task lists), quotes, rules and tables. Inline
//  formatting (bold, italic, `code`, links) uses Foundation's parser.
//

import SwiftUI
import UIKit

// MARK: - Model

nonisolated enum MarkdownBlock: Equatable, Sendable {
    nonisolated struct ListItem: Equatable, Sendable {
        let level: Int
        let marker: Marker
        let text: String
    }

    nonisolated enum Marker: Equatable, Sendable {
        case bullet
        case number(Int)
        case task(done: Bool)
    }

    case heading(level: Int, text: String)
    case paragraph(String)
    case code(language: String?, code: String)
    case list([ListItem])
    case quote(String)
    case rule
    case table(header: [String], rows: [[String]])
}

// MARK: - Parser

nonisolated enum MarkdownParser {
    static func parse(_ source: String) -> [MarkdownBlock] {
        let lines = source.replacingOccurrences(of: "\r\n", with: "\n").components(separatedBy: "\n")
        var blocks: [MarkdownBlock] = []
        var paragraph: [String] = []
        var i = 0

        func flushParagraph() {
            let text = paragraph.joined(separator: "\n").trimmingCharacters(in: .whitespacesAndNewlines)
            if !text.isEmpty { blocks.append(.paragraph(text)) }
            paragraph.removeAll()
        }

        while i < lines.count {
            let line = lines[i]
            let trimmed = line.trimmingCharacters(in: .whitespaces)

            // Fenced code
            if let fence = codeFence(trimmed) {
                flushParagraph()
                let language = String(trimmed.dropFirst(fence.count)).trimmingCharacters(in: .whitespaces)
                var code: [String] = []
                i += 1
                while i < lines.count, !lines[i].trimmingCharacters(in: .whitespaces).hasPrefix(fence) {
                    code.append(lines[i])
                    i += 1
                }
                i += 1 // closing fence (or end)
                blocks.append(.code(language: language.isEmpty ? nil : language, code: code.joined(separator: "\n")))
                continue
            }

            if trimmed.isEmpty {
                flushParagraph()
                i += 1
                continue
            }

            // Heading
            if let heading = heading(trimmed) {
                flushParagraph()
                blocks.append(heading)
                i += 1
                continue
            }

            // Horizontal rule
            if isRule(trimmed) {
                flushParagraph()
                blocks.append(.rule)
                i += 1
                continue
            }

            // Table: header row followed by a separator row
            if trimmed.contains("|"), i + 1 < lines.count, isTableSeparator(lines[i + 1]) {
                flushParagraph()
                let header = tableCells(trimmed)
                var rows: [[String]] = []
                i += 2
                while i < lines.count, lines[i].contains("|"), !lines[i].trimmingCharacters(in: .whitespaces).isEmpty {
                    rows.append(tableCells(lines[i]))
                    i += 1
                }
                blocks.append(.table(header: header, rows: rows))
                continue
            }

            // Quote
            if trimmed.hasPrefix(">") {
                flushParagraph()
                var quoted: [String] = []
                while i < lines.count {
                    let t = lines[i].trimmingCharacters(in: .whitespaces)
                    guard t.hasPrefix(">") else { break }
                    quoted.append(String(t.dropFirst()).trimmingCharacters(in: .whitespaces))
                    i += 1
                }
                blocks.append(.quote(quoted.joined(separator: "\n")))
                continue
            }

            // List
            if listItem(line) != nil {
                flushParagraph()
                var items: [MarkdownBlock.ListItem] = []
                while i < lines.count {
                    if let item = listItem(lines[i]) {
                        items.append(item)
                        i += 1
                    } else if !lines[i].trimmingCharacters(in: .whitespaces).isEmpty,
                              lines[i].hasPrefix("  "), let last = items.popLast() {
                        // Continuation line of the previous item
                        items.append(.init(level: last.level, marker: last.marker,
                                           text: last.text + " " + lines[i].trimmingCharacters(in: .whitespaces)))
                        i += 1
                    } else {
                        break
                    }
                }
                blocks.append(.list(items))
                continue
            }

            paragraph.append(line)
            i += 1
        }
        flushParagraph()
        return blocks
    }

    private static func codeFence(_ trimmed: String) -> String? {
        if trimmed.hasPrefix("```") { return "```" }
        if trimmed.hasPrefix("~~~") { return "~~~" }
        return nil
    }

    private static func heading(_ trimmed: String) -> MarkdownBlock? {
        let hashes = trimmed.prefix(while: { $0 == "#" }).count
        guard (1...6).contains(hashes) else { return nil }
        let rest = trimmed.dropFirst(hashes)
        guard rest.first == " " else { return nil }
        var text = rest.trimmingCharacters(in: .whitespaces)
        while text.hasSuffix("#") { text.removeLast() }
        return .heading(level: hashes, text: text.trimmingCharacters(in: .whitespaces))
    }

    private static func isRule(_ trimmed: String) -> Bool {
        let compact = trimmed.replacingOccurrences(of: " ", with: "")
        guard compact.count >= 3, let first = compact.first, "-*_".contains(first) else { return false }
        return compact.allSatisfy { $0 == first }
    }

    private static func isTableSeparator(_ line: String) -> Bool {
        let t = line.trimmingCharacters(in: .whitespaces)
        guard t.contains("-"), t.contains("|") || t.hasPrefix("-") else { return false }
        return t.allSatisfy { "|-: ".contains($0) }
    }

    private static func tableCells(_ line: String) -> [String] {
        var t = line.trimmingCharacters(in: .whitespaces)
        if t.hasPrefix("|") { t.removeFirst() }
        if t.hasSuffix("|") { t.removeLast() }
        return t.components(separatedBy: "|").map { $0.trimmingCharacters(in: .whitespaces) }
    }

    static func listItem(_ line: String) -> MarkdownBlock.ListItem? {
        let indent = line.prefix(while: { $0 == " " || $0 == "\t" })
            .reduce(0) { $0 + ($1 == "\t" ? 4 : 1) }
        let body = line.drop(while: { $0 == " " || $0 == "\t" })
        let level = min(indent / 2, 4)

        if let first = body.first, "-*+".contains(first), body.dropFirst().first == " " {
            var text = String(body.dropFirst(2))
            var marker = MarkdownBlock.Marker.bullet
            if text.hasPrefix("[ ] ") { marker = .task(done: false); text.removeFirst(4) }
            else if text.lowercased().hasPrefix("[x] ") { marker = .task(done: true); text.removeFirst(4) }
            return .init(level: level, marker: marker, text: text)
        }

        let digits = body.prefix(while: { $0.isNumber })
        if !digits.isEmpty, digits.count <= 4 {
            let rest = body.dropFirst(digits.count)
            if let sep = rest.first, sep == "." || sep == ")", rest.dropFirst().first == " " {
                return .init(level: level, marker: .number(Int(digits) ?? 1), text: String(rest.dropFirst(2)))
            }
        }
        return nil
    }
}

// MARK: - Inline

@MainActor
enum MarkdownInline {
    private static var cache: [String: AttributedString] = [:]

    static func attributed(_ text: String, codeBackground: Color) -> AttributedString {
        if let hit = cache[text] { return hit }
        var result = (try? AttributedString(
            markdown: text,
            options: .init(allowsExtendedAttributes: true,
                           interpretedSyntax: .inlineOnlyPreservingWhitespace,
                           failurePolicy: .returnPartiallyParsedIfPossible)
        )) ?? AttributedString(text)
        for run in result.runs {
            if let intent = run.inlinePresentationIntent, intent.contains(.code) {
                result[run.range].font = .eggifyMono(16, relativeTo: .callout)
                result[run.range].backgroundColor = codeBackground
            }
        }
        linkBareURLs(in: &result)
        if cache.count > 600 { cache.removeAll(keepingCapacity: true) }
        cache[text] = result
        return result
    }

    private static let linkDetector = try? NSDataDetector(types: NSTextCheckingResult.CheckingType.link.rawValue)

    /// Agents mostly print plain URLs, which Markdown parsing leaves as text.
    /// Make them tappable, except inside inline code or existing links.
    static func linkBareURLs(in result: inout AttributedString) {
        let plain = String(result.characters)
        guard plain.contains("http") || plain.contains("www."), let detector = linkDetector else { return }
        let matches = detector.matches(in: plain, range: NSRange(plain.startIndex..., in: plain))
        for match in matches {
            guard let url = match.url, let scheme = url.scheme?.lowercased(),
                  scheme == "http" || scheme == "https",
                  let range = Range(match.range, in: plain) else { continue }
            let start = result.characters.index(result.startIndex,
                                                offsetBy: plain.distance(from: plain.startIndex, to: range.lowerBound))
            let end = result.characters.index(start, offsetBy: plain.distance(from: range.lowerBound, to: range.upperBound))
            let target = start..<end
            let alreadyStyled = result[target].runs.contains {
                $0.link != nil || ($0.inlinePresentationIntent?.contains(.code) ?? false)
            }
            if !alreadyStyled { result[target].link = url }
        }
    }
}

@MainActor
enum MarkdownCache {
    private static var blocks: [String: [MarkdownBlock]] = [:]

    static func blocks(for text: String) -> [MarkdownBlock] {
        if let hit = blocks[text] { return hit }
        let parsed = MarkdownParser.parse(text)
        if blocks.count > 400 { blocks.removeAll(keepingCapacity: true) }
        blocks[text] = parsed
        return parsed
    }
}

// MARK: - View

struct MarkdownView: View {
    let text: String
    var foreground: Color = .primary
    var isOnAccent = false

    private var codeBackground: Color {
        isOnAccent ? Color.white.opacity(0.18) : EggifyColors.codeBackground
    }

    var body: some View {
        let blocks = MarkdownCache.blocks(for: text)
        VStack(alignment: .leading, spacing: 8) {
            ForEach(Array(blocks.enumerated()), id: \.offset) { _, block in
                blockView(block)
            }
        }
        .foregroundStyle(foreground)
        .tint(isOnAccent ? .white : EggifyColors.accent)
    }

    @ViewBuilder
    private func blockView(_ block: MarkdownBlock) -> some View {
        switch block {
        case .heading(let level, let text):
            Text(MarkdownInline.attributed(text, codeBackground: codeBackground))
                .font(level <= 2 ? .eggifyDisplay(level <= 1 ? 22 : 19, relativeTo: .title3) : .eggifySerifSemiBold(19, relativeTo: .headline))
                .padding(.top, level <= 2 ? 4 : 2)
                .accessibilityAddTraits(.isHeader)
                .textSelection(.enabled)

        case .paragraph(let text):
            Text(MarkdownInline.attributed(text, codeBackground: codeBackground))
                .font(.eggifySerif())
                .lineSpacing(2)
                .textSelection(.enabled)
                .fixedSize(horizontal: false, vertical: true)

        case .code(let language, let code):
            CodeBlockView(language: language, code: code, onAccent: isOnAccent)

        case .list(let items):
            VStack(alignment: .leading, spacing: 4) {
                ForEach(Array(items.enumerated()), id: \.offset) { _, item in
                    HStack(alignment: .firstTextBaseline, spacing: 6) {
                        markerView(item.marker)
                            .frame(minWidth: 14, alignment: .trailing)
                        Text(MarkdownInline.attributed(item.text, codeBackground: codeBackground))
                            .font(.eggifySerif())
                            .strikethrough(item.marker == .task(done: true), color: foreground.opacity(0.5))
                            .textSelection(.enabled)
                            .fixedSize(horizontal: false, vertical: true)
                    }
                    .padding(.leading, CGFloat(item.level) * 16)
                }
            }

        case .quote(let text):
            HStack(alignment: .top, spacing: 8) {
                RoundedRectangle(cornerRadius: 1.5)
                    .fill(foreground.opacity(0.35))
                    .frame(width: 3)
                Text(MarkdownInline.attributed(text, codeBackground: codeBackground))
                    .font(.custom(EggifyFonts.serifItalic, size: 18, relativeTo: .body))
                    .foregroundStyle(foreground.opacity(0.8))
                    .textSelection(.enabled)
                    .fixedSize(horizontal: false, vertical: true)
            }

        case .rule:
            Divider().overlay(foreground.opacity(0.2))

        case .table(let header, let rows):
            MarkdownTableView(header: header, rows: rows, codeBackground: codeBackground)
        }
    }

    @ViewBuilder
    private func markerView(_ marker: MarkdownBlock.Marker) -> some View {
        switch marker {
        case .bullet:
            Text("•").font(.eggifySerifSemiBold())
        case .number(let n):
            Text("\(n).").font(.eggifySerif())
        case .task(let done):
            Image(systemName: done ? "checkmark.square.fill" : "square")
                .font(.body)
                .foregroundStyle(done ? EggifyColors.success : foreground.opacity(0.6))
                .accessibilityLabel(done ? "Done" : "To do")
        }
    }
}

// MARK: - Code block

struct CodeBlockView: View {
    let language: String?
    let code: String
    var onAccent = false
    @State private var copied = false
    @State private var expanded = false

    /// Longer blocks start collapsed to this many lines.
    static let collapsedLines = 30
    private var lineCount: Int { code.reduce(into: 1) { if $1 == "\n" { $0 += 1 } } }
    private var isLong: Bool { lineCount > Self.collapsedLines + 5 }
    private var shownCode: String {
        guard isLong, !expanded else { return code }
        return code.split(separator: "\n", omittingEmptySubsequences: false)
            .prefix(Self.collapsedLines).joined(separator: "\n")
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            HStack {
                Text(language?.lowercased() ?? "code")
                    .font(.caption2.weight(.semibold))
                    .foregroundStyle(.secondary)
                Spacer()
                Button {
                    UIPasteboard.general.string = code
                    EggifyHaptics.tap()
                    copied = true
                    Task {
                        try? await Task.sleep(for: .seconds(1.5))
                        copied = false
                    }
                } label: {
                    Label(copied ? "Copied" : "Copy", systemImage: copied ? "checkmark" : "doc.on.doc")
                        .font(.caption2.weight(.medium))
                        .labelStyle(.titleAndIcon)
                }
                .buttonStyle(.borderless)
                .accessibilityLabel(copied ? "Copied" : "Copy code")
            }
            .padding(.horizontal, 10)
            .padding(.vertical, 6)
            .background(Color.primary.opacity(0.05))

            ScrollView(.horizontal, showsIndicators: false) {
                Text(shownCode)
                    .font(.eggifyMono(14))
                    .foregroundStyle(.primary)
                    .textSelection(.enabled)
                    .fixedSize(horizontal: true, vertical: true)
                    .padding(10)
            }

            if isLong {
                Button {
                    withAnimation(.snappy) { expanded.toggle() }
                } label: {
                    Label(expanded ? "Show less" : "Show all \(lineCount) lines",
                          systemImage: expanded ? "chevron.up" : "chevron.down")
                        .font(.caption.weight(.medium))
                        .frame(maxWidth: .infinity)
                        .padding(.vertical, 7)
                }
                .buttonStyle(.borderless)
                .background(Color.primary.opacity(0.05))
            }
        }
        .background(EggifyColors.codeBackground)
        .clipShape(RoundedRectangle(cornerRadius: 10, style: .continuous))
        .overlay(
            RoundedRectangle(cornerRadius: 10, style: .continuous)
                .strokeBorder(EggifyColors.border, lineWidth: 0.5)
        )
    }
}

// MARK: - Table

private struct MarkdownTableView: View {
    let header: [String]
    let rows: [[String]]
    let codeBackground: Color

    var body: some View {
        ScrollView(.horizontal, showsIndicators: false) {
            Grid(alignment: .leading, horizontalSpacing: 12, verticalSpacing: 6) {
                GridRow {
                    ForEach(Array(header.enumerated()), id: \.offset) { _, cell in
                        Text(MarkdownInline.attributed(cell, codeBackground: codeBackground))
                            .font(.eggifySerifSemiBold(15, relativeTo: .footnote))
                    }
                }
                Divider()
                ForEach(Array(rows.enumerated()), id: \.offset) { _, row in
                    GridRow {
                        ForEach(0..<header.count, id: \.self) { col in
                            Text(MarkdownInline.attributed(col < row.count ? row[col] : "", codeBackground: codeBackground))
                                .font(.eggifySerif(15, relativeTo: .footnote))
                        }
                    }
                }
            }
            .padding(10)
        }
        .background(EggifyColors.surface)
        .clipShape(RoundedRectangle(cornerRadius: 10, style: .continuous))
    }
}
