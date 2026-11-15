//
//  LicenseGateTests.swift
//  eggifyTests
//
//  A licensed Eggify that isn't activated answers 402 license_required
//  (REST), refuses the WebSocket upgrade with 402, or sends a WebSocket error
//  with that code. The app maps all three to one state (LicenseGate).
//

import Foundation
import Testing
@testable import eggify

@Suite("License required")
struct LicenseGateTests {
    @Test func mapsRESTRefusal() {
        #expect(LicenseGate.isLicenseRequired(statusCode: 402, body: ["code": "license_required", "error": "Activate Eggify with your serial number"]))
        #expect(!LicenseGate.isLicenseRequired(statusCode: 402, body: ["code": "payment"]))
        #expect(!LicenseGate.isLicenseRequired(statusCode: 403, body: ["code": "license_required"]))
        #expect(!LicenseGate.isLicenseRequired(statusCode: 403, body: ["code": "user_required"]))
    }

    @Test func mapsWebSocketRefusals() {
        #expect(LicenseGate.isLicenseRequired(wsPayload: ["type": "error", "code": "license_required", "message": "x"]))
        #expect(!LicenseGate.isLicenseRequired(wsPayload: ["type": "error", "code": "user_required"]))
        #expect(!LicenseGate.isLicenseRequired(wsPayload: ["type": "session_updated", "code": "license_required"]))
        #expect(LicenseGate.isLicenseRequired(upgradeStatus: 402))
        #expect(!LicenseGate.isLicenseRequired(upgradeStatus: 403))
    }

    @Test func errorSaysWhatToDo() {
        let text = EggifyAPIClient.EggifyAPIError.licenseRequired.errorDescription ?? ""
        #expect(text == "Eggify on your computer isn't activated. Activate it there with your serial number.")
    }

    @Test func readsTheLicenseStatusForRetry() {
        #expect(LicenseGate.stillRequired(["required": true, "state": "unlicensed"]))
        #expect(LicenseGate.stillRequired(["required": true, "state": "expired"]))
        #expect(!LicenseGate.stillRequired(["required": true, "state": "active"]))
        #expect(!LicenseGate.stillRequired(["required": false, "state": "unlicensed"]))
    }

    @MainActor @Test func announcementShowsTheState() async {
        let center = NotificationCenter()
        let monitor = LicenseMonitor(center: center)
        #expect(!monitor.isRequired)
        center.post(name: .licenseRequired, object: nil)
        for _ in 0..<50 where !monitor.isRequired {
            try? await Task.sleep(for: .milliseconds(10))
        }
        #expect(monitor.isRequired)
        monitor.reset()
        #expect(!monitor.isRequired)
    }
}
