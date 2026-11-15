//
//  SelectTextSheet.swift
//  eggify
//
//  Copying part of a message: long-press → Select Text opens the message in a
//  native text view, where the usual selection handles and Copy work on any
//  part of it (as in Messages).
//

import SwiftUI
import UIKit

extension View {
    /// The long-press menu of a chat message (Copy, Select Text, Share,
    /// Details), with a short preview so a long message doesn't fill the screen.
    func messageContextMenu(_ message: AgentMessage, onShowDetails: @escaping (AgentMessage) -> Void) -> some View {
        modifier(MessageContextMenu(message: message, onShowDetails: onShowDetails))
    }
}

private struct MessageContextMenu: ViewModifier {
    let message: AgentMessage
    let onShowDetails: (AgentMessage) -> Void
    @State private var selectingText = false

    func body(content: Content) -> some View {
        content
            .contextMenu {
                MessageMenu(message: message, onShowDetails: onShowDetails) {
                    selectingText = true
                }
            } preview: {
                MessagePreview(text: message.content.textContent)
            }
            .sheet(isPresented: $selectingText) {
                SelectTextSheet(text: message.content.textContent)
            }
    }
}

/// First lines of the message, for the long-press preview.
private struct MessagePreview: View {
    let text: String

    var body: some View {
        Text(SelectableText.plain(text))
            .font(.callout)
            .lineLimit(8)
            .padding(14)
            .frame(width: 300, alignment: .leading)
    }
}

struct SelectTextSheet: View {
    let text: String
    @Environment(\.dismiss) private var dismiss

    var body: some View {
        NavigationStack {
            SelectableTextView(attributed: SelectableText.attributed(text))
                .ignoresSafeArea(edges: .bottom)
                .navigationTitle("Select Text")
                .navigationBarTitleDisplayMode(.inline)
                .toolbar {
                    ToolbarItem(placement: .topBarLeading) {
                        Button("Done") { dismiss() }
                    }
                    ToolbarItem(placement: .topBarTrailing) {
                        Button {
                            UIPasteboard.general.string = SelectableText.plain(text)
                            EggifyHaptics.success()
                            dismiss()
                        } label: {
                            Label("Copy All", systemImage: "doc.on.doc")
                        }
                    }
                }
        }
        .presentationDetents([.medium, .large])
        .presentationDragIndicator(.visible)
    }
}

/// A read-only UITextView: native selection handles, Copy, Look Up, Share.
struct SelectableTextView: UIViewRepresentable {
    let attributed: NSAttributedString

    func makeUIView(context: Context) -> UITextView {
        let view = UITextView()
        view.isEditable = false
        view.isSelectable = true
        view.adjustsFontForContentSizeCategory = true
        view.dataDetectorTypes = [.link]
        view.backgroundColor = .clear
        view.textContainerInset = UIEdgeInsets(top: 16, left: 14, bottom: 32, right: 14)
        view.attributedText = attributed
        return view
    }

    func updateUIView(_ view: UITextView, context: Context) {
        if view.attributedText != attributed { view.attributedText = attributed }
    }
}

/// The message text for selecting and copying: inline Markdown rendered
/// (bold, italics, code, links), line breaks kept, code-fence lines dropped.
nonisolated enum SelectableText {
    static func plain(_ markdown: String) -> String {
        String(rendered(markdown).characters)
    }

    static func attributed(_ markdown: String) -> NSAttributedString {
        let body = UIFont.preferredFont(forTextStyle: .body)
        let mono = UIFontMetrics(forTextStyle: .body)
            .scaledFont(for: UIFont.monospacedSystemFont(ofSize: body.pointSize * 0.92, weight: .regular))
        let result = NSMutableAttributedString()
        // Code blocks stay monospaced; the rest gets inline Markdown.
        for (index, segment) in segments(markdown).enumerated() {
            if index > 0 { result.append(NSAttributedString(string: "\n", attributes: [.font: body])) }
            if segment.isCode {
                result.append(NSAttributedString(string: segment.text,
                                                 attributes: [.font: mono, .foregroundColor: UIColor.label]))
            } else {
                result.append(inlineAttributed(segment.text, body: body, mono: mono))
            }
        }
        return result
    }

    /// The text split into code blocks and prose, fence lines dropped.
    static func segments(_ markdown: String) -> [(isCode: Bool, text: String)] {
        var segments: [(isCode: Bool, text: String)] = []
        var lines: [Substring] = []
        var inCode = false
        func flush() {
            segments.append((inCode, lines.joined(separator: "\n")))
            lines.removeAll()
        }
        for line in markdown.split(separator: "\n", omittingEmptySubsequences: false) {
            if line.trimmingCharacters(in: .whitespaces).hasPrefix("```") {
                flush()
                inCode.toggle()
            } else {
                lines.append(line)
            }
        }
        flush()
        // Drop empty prose between fences (e.g. a fence at the very start).
        return segments.filter { $0.isCode || !$0.text.isEmpty }
    }

    private static func inlineAttributed(_ text: String, body: UIFont, mono: UIFont) -> NSAttributedString {
        let result = NSMutableAttributedString()
        let source = (try? AttributedString(
            markdown: text,
            options: .init(interpretedSyntax: .inlineOnlyPreservingWhitespace,
                           failurePolicy: .returnPartiallyParsedIfPossible)
        )) ?? AttributedString(text)
        for run in source.runs {
            let piece = String(source[run.range].characters)
            var attributes: [NSAttributedString.Key: Any] = [.foregroundColor: UIColor.label]
            var font = body
            if let intent = run.inlinePresentationIntent {
                if intent.contains(.code) {
                    font = mono
                } else {
                    var traits: UIFontDescriptor.SymbolicTraits = []
                    if intent.contains(.stronglyEmphasized) { traits.insert(.traitBold) }
                    if intent.contains(.emphasized) { traits.insert(.traitItalic) }
                    if !traits.isEmpty, let descriptor = body.fontDescriptor.withSymbolicTraits(traits) {
                        font = UIFont(descriptor: descriptor, size: 0)
                    }
                }
            }
            attributes[.font] = font === mono ? mono : UIFontMetrics(forTextStyle: .body).scaledFont(for: font)
            if let link = run.link { attributes[.link] = link }
            result.append(NSAttributedString(string: piece, attributes: attributes))
        }
        return result
    }

    private static func rendered(_ markdown: String) -> AttributedString {
        let withoutFences = markdown
            .split(separator: "\n", omittingEmptySubsequences: false)
            .filter { !$0.trimmingCharacters(in: .whitespaces).hasPrefix("```") }
            .joined(separator: "\n")
        return (try? AttributedString(
            markdown: withoutFences,
            options: .init(interpretedSyntax: .inlineOnlyPreservingWhitespace,
                           failurePolicy: .returnPartiallyParsedIfPossible)
        )) ?? AttributedString(withoutFences)
    }
}

/// The system share sheet, for content that should only be built on tap.
enum ShareSheet {
    @MainActor static func present(_ items: [Any]) {
        guard let scene = UIApplication.shared.connectedScenes
                .compactMap({ $0 as? UIWindowScene })
                .first(where: { $0.activationState == .foregroundActive }),
              var top = scene.keyWindow?.rootViewController else { return }
        while let presented = top.presentedViewController { top = presented }
        let controller = UIActivityViewController(activityItems: items, applicationActivities: nil)
        // iPad: anchor the popover to the top-right, where the menu is.
        controller.popoverPresentationController?.sourceView = top.view
        controller.popoverPresentationController?.sourceRect = CGRect(x: top.view.bounds.maxX - 44, y: 60, width: 1, height: 1)
        top.present(controller, animated: true)
    }
}
