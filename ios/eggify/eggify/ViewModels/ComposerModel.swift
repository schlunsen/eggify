//
//  ComposerModel.swift
//  eggify
//
//  The chat composer's own state (text, attachments, photo picker). It is a
//  separate object from ChatViewModel so that typing only re-renders the
//  composer, not the whole conversation.
//

import SwiftUI
import PhotosUI
import UniformTypeIdentifiers
import Combine

@MainActor
final class ComposerModel: ObservableObject {
    @Published var text: String {
        didSet { if let draftKey, text != oldValue { Self.saveDraft(text, for: draftKey) } }
    }
    @Published var attachments: [ComposerAttachment] = []
    /// Picked photos still loading (e.g. from iCloud); Send waits for them.
    @Published private(set) var preparingCount = 0

    static let maxAttachments = 4
    var remainingAttachmentSlots: Int { max(0, Self.maxAttachments - attachments.count - preparingCount) }
    @Published var photoSelection: [PhotosPickerItem] = [] {
        didSet { if !photoSelection.isEmpty { Task { await loadPickedPhotos() } } }
    }

    /// Unsent text per session, kept across leaving the chat and relaunching
    /// the app (UserDefaults, at most `maxDrafts`; cleared on sign-out).
    private static let draftsKey = "eggify.composerDrafts"
    static let maxDrafts = 50
    nonisolated(unsafe) static var defaults: UserDefaults = .standard
    private let draftKey: String?

    init(draftKey: String? = nil) {
        self.draftKey = draftKey
        self.text = draftKey.flatMap { Self.drafts[$0] } ?? ""
    }

    private static var drafts: [String: String] {
        defaults.dictionary(forKey: draftsKey) as? [String: String] ?? [:]
    }

    private static func saveDraft(_ text: String, for key: String) {
        var all = drafts
        if text.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty {
            guard all.removeValue(forKey: key) != nil else { return }
        } else {
            all[key] = text
            if all.count > maxDrafts {
                // Over the limit: drop arbitrary others (rare; keeps this one).
                for other in all.keys where other != key && all.count > maxDrafts { all.removeValue(forKey: other) }
            }
        }
        defaults.set(all, forKey: draftsKey)
    }

    /// Forget every unsent draft (sign-out).
    static func clearDrafts() {
        defaults.removeObject(forKey: draftsKey)
    }

    var canSend: Bool {
        preparingCount == 0
            && (!text.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty || !attachments.isEmpty)
    }

    /// Takes the text and images to send and empties the composer.
    func take() -> (text: String, images: [(mediaType: String, base64: String)])? {
        guard canSend else { return nil }
        let sent = text
        let images = attachments.map { (mediaType: $0.mediaType, base64: $0.base64) }
        attachments = []
        text = ""
        Self.clearEchoes(of: sent, get: { [weak self] in self?.text ?? "" }, clear: { [weak self] in self?.text = "" })
        return (sent, images)
    }

    func removeAttachment(_ attachment: ComposerAttachment) {
        attachments.removeAll { $0.id == attachment.id }
    }

    /// Call right after emptying a text field's binding on send.
    ///
    /// A text field that still holds an uncommitted autocorrect suggestion,
    /// predictive word or dictation result commits it just after the binding
    /// is cleared, which brings (part of) the sent text back. So the field is
    /// checked again over the next few frames and cleared if what came back
    /// is part of what was just sent. Anything else is the user typing anew
    /// and is left alone.
    static func clearEchoes(of sent: String,
                            get: @escaping @MainActor () -> String,
                            clear: @escaping @MainActor () -> Void) {
        Task { @MainActor in
            for _ in 0..<4 {
                try? await Task.sleep(for: .milliseconds(40))
                if isEcho(get(), of: sent) { clear() }
            }
        }
    }

    /// Whether `current` is (part of) `sent` coming back into the field.
    nonisolated static func isEcho(_ current: String, of sent: String) -> Bool {
        let back = current.trimmingCharacters(in: .whitespacesAndNewlines)
        return !back.isEmpty && sent.contains(back)
    }

    private func loadPickedPhotos() async {
        let picked = Array(photoSelection.prefix(remainingAttachmentSlots))
        photoSelection = []
        await addImages(picked.map { item in { try? await item.loadTransferable(type: Data.self) } })
    }

    /// Images pasted into the field (as many as there are free slots).
    func addPastedImages(_ providers: [NSItemProvider]) {
        let pasted = Array(providers.prefix(remainingAttachmentSlots))
        guard !pasted.isEmpty else { return }
        Task { await addImages(pasted.map { provider in { await Self.loadImageData(provider) } }) }
    }

    private func addImages(_ loaders: [() async -> Data?]) async {
        preparingCount += loaders.count
        for load in loaders {
            defer { preparingCount -= 1 }
            guard let data = await load(),
                  let prepared = await Task.detached(priority: .userInitiated, operation: {
                      ComposerModel.prepareImage(data)
                  }).value else { continue }
            attachments.append(prepared)
        }
    }

    private nonisolated static func loadImageData(_ provider: NSItemProvider) async -> Data? {
        await withCheckedContinuation { continuation in
            provider.loadDataRepresentation(forTypeIdentifier: UTType.image.identifier) { data, _ in
                continuation.resume(returning: data)
            }
        }
    }

    /// Downscale to a sensible size and JPEG-encode for the model.
    nonisolated static func prepareImage(_ data: Data) -> ComposerAttachment? {
        guard let image = ImageCache.downsample(data: data, maxPixel: 1568),
              let jpeg = image.jpegData(compressionQuality: 0.82) else { return nil }
        let thumb = ImageCache.downsample(data: jpeg, maxPixel: 180) ?? image
        return ComposerAttachment(thumbnail: thumb, mediaType: "image/jpeg", base64: jpeg.base64EncodedString())
    }
}
