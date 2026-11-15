//
//  QRScannerView.swift
//  eggify
//
//  In-app QR scanner (AVFoundation) for Eggify pairing and address codes,
//  with camera permission handling, a torch toggle and a "Paste pairing
//  link" fallback for devices without a camera (the Simulator).
//

import SwiftUI
import AVFoundation
import UIKit

// MARK: - Sheet

struct PairingScannerSheet: View {
    /// Called with a string that parsed as an Eggify code.
    let onCode: (String) -> Void

    @Environment(\.dismiss) private var dismiss
    @Environment(\.openURL) private var openURL
    @State private var permission: AVAuthorizationStatus = AVCaptureDevice.authorizationStatus(for: .video)
    @State private var torchOn = false
    @State private var scanError: String?
    @State private var showPaste = false
    @State private var pasted = ""
    @State private var paused = false

    private var hasCamera: Bool { AVCaptureDevice.default(for: .video) != nil }
    private var hasTorch: Bool { AVCaptureDevice.default(for: .video)?.hasTorch ?? false }

    var body: some View {
        NavigationStack {
            VStack(spacing: 0) {
                cameraArea
                    .frame(maxWidth: .infinity)
                    .frame(height: 360)
                    .clipShape(RoundedRectangle(cornerRadius: 20))
                    .padding(.horizontal, 20)
                    .padding(.top, 8)

                VStack(spacing: 14) {
                    Text("Scan the pairing code")
                        .font(.eggifyDisplay(24, relativeTo: .title2))
                        .foregroundStyle(EggifyColors.textPrimary)
                    Text("On your computer, open Eggify → Settings → Remote access → **Pair the iOS app**. You can also point the iPhone Camera app at the code.")
                        .font(.subheadline)
                        .foregroundStyle(EggifyColors.textSecondary)
                        .multilineTextAlignment(.center)

                    if let scanError {
                        Label(scanError, systemImage: "exclamationmark.triangle.fill")
                            .font(.footnote)
                            .foregroundStyle(EggifyColors.error)
                            .padding(10)
                            .frame(maxWidth: .infinity, alignment: .leading)
                            .background(EggifyColors.error.opacity(0.1), in: RoundedRectangle(cornerRadius: 10))
                            .accessibilityIdentifier("scanner-error")
                    }

                    pasteSection
                }
                .padding(20)

                Spacer(minLength: 0)
            }
            .background(EggifyColors.background.ignoresSafeArea())
            .navigationTitle("Scan QR code")
            .dismissesKeyboardOnScroll()
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Cancel") { dismiss() }
                }
                if permission == .authorized && hasTorch {
                    ToolbarItem(placement: .primaryAction) {
                        Button {
                            torchOn.toggle()
                        } label: {
                            Image(systemName: torchOn ? "flashlight.on.fill" : "flashlight.off.fill")
                        }
                        .accessibilityLabel(torchOn ? "Turn torch off" : "Turn torch on")
                    }
                }
            }
            .task {
                if permission == .notDetermined && hasCamera {
                    let granted = await AVCaptureDevice.requestAccess(for: .video)
                    permission = granted ? .authorized : .denied
                }
                if !hasCamera { showPaste = true }
            }
        }
    }

    @ViewBuilder
    private var cameraArea: some View {
        if !hasCamera {
            placeholder(icon: "camera.metering.unknown",
                        title: "No camera available",
                        text: "Paste the pairing link instead (Eggify → Pair the iOS app → Show code/link).")
        } else {
            switch permission {
            case .authorized:
                ZStack {
                    QRCameraView(torchOn: $torchOn, paused: $paused) { value in handle(value) }
                    RoundedRectangle(cornerRadius: 18)
                        .strokeBorder(EggifyColors.circusMustard, style: StrokeStyle(lineWidth: 3, dash: [14, 8]))
                        .frame(width: 230, height: 230)
                        .accessibilityHidden(true)
                }
                .accessibilityIdentifier("scanner-camera")
            case .denied, .restricted:
                VStack(spacing: 12) {
                    placeholder(icon: "camera.fill",
                                title: "Camera access is off",
                                text: "Allow camera access for Eggify in Settings to scan the code, or paste the pairing link.")
                    Button("Open Settings") {
                        if let url = URL(string: UIApplication.openSettingsURLString) { openURL(url) }
                    }
                    .buttonStyle(.borderedProminent)
                    .tint(EggifyColors.accent)
                }
                .frame(maxWidth: .infinity, maxHeight: .infinity)
                .background(EggifyColors.surface)
            default:
                ProgressView()
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
                    .background(EggifyColors.surface)
            }
        }
    }

    private func placeholder(icon: String, title: String, text: String) -> some View {
        VStack(spacing: 10) {
            Image(systemName: icon)
                .font(.system(size: 40))
                .foregroundStyle(EggifyColors.accent)
            Text(title).font(.headline).foregroundStyle(EggifyColors.textPrimary)
            Text(text)
                .font(.footnote)
                .foregroundStyle(EggifyColors.textSecondary)
                .multilineTextAlignment(.center)
                .padding(.horizontal, 24)
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
        .background(EggifyColors.surface)
    }

    @ViewBuilder
    private var pasteSection: some View {
        if showPaste {
            VStack(spacing: 10) {
                TextField("eggify://pair?u=…&c=…", text: $pasted, axis: .vertical)
                    .lineLimit(1...3)
                    .font(.footnote.monospaced())
                    .textInputAutocapitalization(.never)
                    .autocorrectionDisabled()
                    .keyboardType(.URL)
                    .padding(12)
                    .background(EggifyColors.surface, in: RoundedRectangle(cornerRadius: 10))
                    .accessibilityIdentifier("paste-link-field")
                HStack {
                    Button {
                        if let text = UIPasteboard.general.string { pasted = text }
                    } label: {
                        Label("Paste", systemImage: "doc.on.clipboard")
                    }
                    .buttonStyle(.bordered)
                    Spacer()
                    Button("Use link") { handle(pasted) }
                        .buttonStyle(.borderedProminent)
                        .tint(EggifyColors.accent)
                        .disabled(pasted.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
                        .accessibilityIdentifier("paste-link-submit")
                }
            }
        } else {
            Button {
                showPaste = true
            } label: {
                Label("Paste pairing link", systemImage: "link")
                    .font(.subheadline.weight(.semibold))
            }
            .foregroundStyle(EggifyColors.accent)
            .accessibilityIdentifier("paste-link-toggle")
        }
    }

    private func handle(_ value: String) {
        do {
            _ = try PairingPayload.parse(value)
            EggifyHaptics.success()
            torchOn = false
            dismiss()
            onCode(value)
        } catch {
            EggifyHaptics.warning()
            scanError = error.localizedDescription
            // Let the camera look again after a moment.
            paused = true
            DispatchQueue.main.asyncAfter(deadline: .now() + 1.5) { paused = false }
        }
    }
}

// MARK: - Camera

/// A camera preview that reports QR code strings.
struct QRCameraView: UIViewControllerRepresentable {
    @Binding var torchOn: Bool
    @Binding var paused: Bool
    let onCode: (String) -> Void

    func makeUIViewController(context: Context) -> QRScannerViewController {
        let vc = QRScannerViewController()
        vc.onCode = onCode
        return vc
    }

    func updateUIViewController(_ vc: QRScannerViewController, context: Context) {
        vc.onCode = onCode
        vc.setTorch(torchOn)
        vc.paused = paused
    }
}

final class QRScannerViewController: UIViewController, AVCaptureMetadataOutputObjectsDelegate {
    var onCode: ((String) -> Void)?
    var paused = false

    private let session = AVCaptureSession()
    private var previewLayer: AVCaptureVideoPreviewLayer?
    private var lastValue: String?
    private var lastValueAt = Date.distantPast
    private let sessionQueue = DispatchQueue(label: "eggify.qr.session")

    override func viewDidLoad() {
        super.viewDidLoad()
        view.backgroundColor = .black
        guard let device = AVCaptureDevice.default(for: .video),
              let input = try? AVCaptureDeviceInput(device: device),
              session.canAddInput(input) else { return }
        session.addInput(input)
        let output = AVCaptureMetadataOutput()
        guard session.canAddOutput(output) else { return }
        session.addOutput(output)
        output.setMetadataObjectsDelegate(self, queue: .main)
        output.metadataObjectTypes = [.qr]

        let layer = AVCaptureVideoPreviewLayer(session: session)
        layer.videoGravity = .resizeAspectFill
        view.layer.addSublayer(layer)
        previewLayer = layer
    }

    override func viewDidLayoutSubviews() {
        super.viewDidLayoutSubviews()
        previewLayer?.frame = view.bounds
    }

    override func viewWillAppear(_ animated: Bool) {
        super.viewWillAppear(animated)
        let session = self.session
        sessionQueue.async { if !session.isRunning { session.startRunning() } }
    }

    override func viewWillDisappear(_ animated: Bool) {
        super.viewWillDisappear(animated)
        setTorch(false)
        let session = self.session
        sessionQueue.async { if session.isRunning { session.stopRunning() } }
    }

    func setTorch(_ on: Bool) {
        guard let device = AVCaptureDevice.default(for: .video), device.hasTorch,
              device.isTorchActive != on else { return }
        do {
            try device.lockForConfiguration()
            device.torchMode = on ? .on : .off
            device.unlockForConfiguration()
        } catch {
            // The torch is a convenience; ignore failures.
        }
    }

    nonisolated func metadataOutput(_ output: AVCaptureMetadataOutput, didOutput metadataObjects: [AVMetadataObject],
                                    from connection: AVCaptureConnection) {
        let value = metadataObjects
            .compactMap { ($0 as? AVMetadataMachineReadableCodeObject)?.stringValue }
            .first
        guard let value else { return }
        MainActor.assumeIsolated {
            guard !paused else { return }
            // The same code is reported many times a second; report it once
            // (again after a pause, so a rejected code can be retried).
            if value == lastValue && Date().timeIntervalSince(lastValueAt) < 3 { return }
            lastValue = value
            lastValueAt = Date()
            onCode?(value)
        }
    }
}
