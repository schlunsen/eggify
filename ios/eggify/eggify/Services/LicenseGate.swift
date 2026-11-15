//
//  LicenseGate.swift
//  eggify
//
//  The iPhone app has no license of its own (docs/adr/013-licensing.md): it
//  follows the Mac. A licensed Eggify that isn't activated answers every API
//  call with 402 {"code": "license_required"} and refuses the live connection
//  (402 before the upgrade, or a WebSocket error with that code). The app then
//  shows one clear full-screen state instead of generic errors.
//

import Foundation
import Combine

nonisolated extension Notification.Name {
    /// Posted when the server says Eggify on the computer isn't activated.
    static let licenseRequired = Notification.Name("licenseRequired")
}

nonisolated enum LicenseGate {
    static let code = "license_required"

    static let title = "Eggify isn't activated"
    static let message = "Eggify on your computer isn't activated. Activate it there with your serial number."

    /// A REST answer that means "not activated": 402 with code license_required.
    static func isLicenseRequired(statusCode: Int, body: [String: Any]) -> Bool {
        statusCode == 402 && body.string("code") == code
    }

    /// A WebSocket message that means "not activated".
    static func isLicenseRequired(wsPayload payload: [String: Any]) -> Bool {
        payload.string("type") == "error" && payload.string("code") == code
    }

    /// A refused WebSocket upgrade: the server answers 402 before upgrading
    /// (its body isn't readable through the upgrade error).
    static func isLicenseRequired(upgradeStatus: Int) -> Bool { upgradeStatus == 402 }

    /// Tell the app (any thread).
    static func announce() {
        NotificationCenter.default.post(name: .licenseRequired, object: nil)
    }

    /// Reads GET /api/license (reachable while not activated): true when the
    /// computer still needs activating.
    static func stillRequired(_ status: [String: Any]) -> Bool {
        let required = (status["required"] as? Bool) ?? false
        return required && status.string("state") != "active"
    }
}

/// Whether the signed-in server is waiting to be activated. The app root
/// covers the signed-in UI with `LicenseRequiredView` while it is.
@MainActor
final class LicenseMonitor: ObservableObject {
    static let shared = LicenseMonitor()

    @Published private(set) var isRequired = false
    @Published private(set) var isChecking = false
    @Published var lastCheckError: String?

    private var cancellables = Set<AnyCancellable>()

    init(center: NotificationCenter = .default) {
        center.publisher(for: .licenseRequired)
            .receive(on: RunLoop.main)
            .sink { [weak self] _ in self?.isRequired = true }
            .store(in: &cancellables)
    }

    /// Forget the state (sign-out, another server).
    func reset() {
        isRequired = false
        lastCheckError = nil
    }

    /// Retry: ask the server again; when it is activated, reconnect.
    func retry() async {
        let api = EggifyAPIClient.shared
        isChecking = true
        defer { isChecking = false }
        do {
            let status = try await api.json("api/license")
            if LicenseGate.stillRequired(status) {
                lastCheckError = "Still not activated. Enter the serial number in Eggify on your computer, then try again."
                return
            }
            lastCheckError = nil
            isRequired = false
            WebSocketService.shared.reconnectNow()
        } catch EggifyAPIClient.EggifyAPIError.licenseRequired {
            lastCheckError = "Still not activated. Enter the serial number in Eggify on your computer, then try again."
        } catch {
            lastCheckError = error.localizedDescription
        }
    }
}
