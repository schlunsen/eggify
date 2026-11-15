//
//  ImageViewerOverlay.swift
//  eggify
//
//  Full-screen image viewer overlay with zoom support
//

import SwiftUI

struct ImageViewerOverlay: View {
    let dataUrl: String
    let onClose: () -> Void

    /// Decoded once, off the main thread (the image can be large and the
    /// chat behind keeps re-rendering while the viewer is open).
    @State private var image: UIImage?
    @State private var didDecode = false

    var body: some View {
        ZStack {
            Color.black.opacity(0.5)
                .ignoresSafeArea()
                .onTapGesture(perform: onClose)
            imageViewerSheet
                .frame(maxWidth: .infinity, maxHeight: .infinity)
        }
        .transition(.opacity)
        .task(id: dataUrl) {
            let source = dataUrl
            image = await Task.detached(priority: .userInitiated) { source.decodeBase64Image() }.value
            didDecode = true
        }
    }

    private var imageViewerSheet: some View {
        VStack(spacing: 0) {
            HStack {
                Button("Done", action: onClose)
                    .font(.body.bold())
                Spacer()
                Text("Image")
                    .font(.headline)
                Spacer()
                if let image {
                    ShareLink(item: Image(uiImage: image),
                              preview: SharePreview("Image", image: Image(uiImage: image))) {
                        Image(systemName: "square.and.arrow.up")
                    }
                    .accessibilityLabel("Share image")
                } else {
                    Button("Done", action: onClose).font(.body.bold()).hidden()
                }
            }
            .padding(16)
            .background(Color(.systemBackground))
            .border(Color(.systemGray3), width: 1)

            if let image {
                ZoomableImageView(image: image)
                    .background(Color(.systemBackground))
            } else if didDecode {
                ErrorImageView(dataUrl: dataUrl)
            } else {
                ProgressView()
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
                    .background(Color(.systemBackground))
            }
        }
    }
}

struct ErrorImageView: View {
    let dataUrl: String

    var body: some View {
        VStack(spacing: 16) {
            Image(systemName: "exclamationmark.triangle.fill")
                .font(.system(size: 40))
                .foregroundStyle(.orange)

            Text("Could not decode image")
                .font(.headline)

            Text("Data URL length: \(dataUrl.count)")
                .font(.caption)
                .foregroundStyle(.secondary)
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
        .background(Color(.systemBackground))
    }
}
