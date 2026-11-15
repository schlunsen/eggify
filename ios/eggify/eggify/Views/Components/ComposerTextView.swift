//
//  ComposerTextView.swift
//  eggify
//
//  The composers' text field: a UITextView, because a SwiftUI TextField only
//  pastes text. Paste (the edit menu or ⌘V) with an image on the pasteboard
//  hands the image to `onPasteImages` instead. Grows from one line up to
//  `maxLines`, then scrolls.
//

import SwiftUI
import UIKit
import UniformTypeIdentifiers

struct ComposerTextView: UIViewRepresentable {
    @Binding var text: String
    @Binding var isFocused: Bool
    var placeholder: String
    var font: UIFont = .preferredFont(forTextStyle: .body)
    var textColor: UIColor = .label
    var placeholderColor: UIColor = .placeholderText
    var tintColor: UIColor? = nil
    var maxLines: Int = 6
    /// Item providers of pasted images; nil turns image paste off.
    var onPasteImages: (([NSItemProvider]) -> Void)? = nil

    func makeUIView(context: Context) -> PastingTextView {
        let view = PastingTextView()
        view.delegate = context.coordinator
        view.backgroundColor = .clear
        view.textContainerInset = .zero
        view.textContainer.lineFragmentPadding = 0
        view.isScrollEnabled = false
        view.adjustsFontForContentSizeCategory = true
        view.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
        view.accessibilityLabel = placeholder
        view.accessibilityIdentifier = "composer-text"
        return view
    }

    func updateUIView(_ view: PastingTextView, context: Context) {
        context.coordinator.parent = self
        if view.text != text { view.text = text }
        view.font = font
        view.textColor = textColor
        view.placeholderLabel.text = placeholder
        view.placeholderLabel.font = font
        view.placeholderLabel.textColor = placeholderColor
        view.placeholderLabel.isHidden = !text.isEmpty
        if let tintColor { view.tintColor = tintColor }
        view.onPasteImages = onPasteImages

        // Focus follows the binding; the delegate reports changes back.
        if isFocused, !view.isFirstResponder, view.window != nil {
            DispatchQueue.main.async { view.becomeFirstResponder() }
        } else if !isFocused, view.isFirstResponder {
            DispatchQueue.main.async { view.resignFirstResponder() }
        }
    }

    func sizeThatFits(_ proposal: ProposedViewSize, uiView: PastingTextView, context: Context) -> CGSize? {
        let width = proposal.width ?? uiView.bounds.width
        guard width > 0 else { return nil }
        let lineHeight = font.lineHeight
        let fitting = uiView.sizeThatFits(CGSize(width: width, height: .greatestFiniteMagnitude)).height
        let maxHeight = ceil(lineHeight * CGFloat(maxLines))
        let height = min(max(fitting, ceil(lineHeight)), maxHeight)
        let scrolls = fitting > maxHeight + 0.5
        if uiView.isScrollEnabled != scrolls {
            DispatchQueue.main.async { uiView.isScrollEnabled = scrolls }
        }
        return CGSize(width: width, height: height)
    }

    func makeCoordinator() -> Coordinator { Coordinator(self) }

    final class Coordinator: NSObject, UITextViewDelegate {
        var parent: ComposerTextView

        init(_ parent: ComposerTextView) { self.parent = parent }

        func textViewDidChange(_ textView: UITextView) {
            parent.text = textView.text
            (textView as? PastingTextView)?.placeholderLabel.isHidden = !textView.text.isEmpty
        }

        func textViewDidBeginEditing(_ textView: UITextView) {
            if !parent.isFocused { parent.isFocused = true }
        }

        func textViewDidEndEditing(_ textView: UITextView) {
            if parent.isFocused { parent.isFocused = false }
        }
    }
}

final class PastingTextView: UITextView {
    var onPasteImages: (([NSItemProvider]) -> Void)?
    let placeholderLabel = UILabel()

    override init(frame: CGRect, textContainer: NSTextContainer?) {
        super.init(frame: frame, textContainer: textContainer)
        placeholderLabel.numberOfLines = 1
        placeholderLabel.isAccessibilityElement = false
        placeholderLabel.translatesAutoresizingMaskIntoConstraints = false
        addSubview(placeholderLabel)
        NSLayoutConstraint.activate([
            placeholderLabel.leadingAnchor.constraint(equalTo: leadingAnchor),
            placeholderLabel.topAnchor.constraint(equalTo: topAnchor),
            placeholderLabel.widthAnchor.constraint(lessThanOrEqualTo: widthAnchor),
        ])
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) { fatalError("init(coder:) has not been implemented") }

    /// Whether the pasteboard holds an image. `hasImages` doesn't read the
    /// contents, so it never shows the "Allow Paste" prompt.
    private var pasteboardHasImage: Bool {
        onPasteImages != nil && UIPasteboard.general.hasImages
    }

    override func canPerformAction(_ action: Selector, withSender sender: Any?) -> Bool {
        if action == #selector(paste(_:)), pasteboardHasImage { return true }
        return super.canPerformAction(action, withSender: sender)
    }

    override func paste(_ sender: Any?) {
        guard pasteboardHasImage, let onPasteImages else { return super.paste(sender) }
        let providers = UIPasteboard.general.itemProviders.filter {
            $0.hasItemConformingToTypeIdentifier(UTType.image.identifier)
        }
        guard !providers.isEmpty else { return super.paste(sender) }
        onPasteImages(providers)
    }
}
