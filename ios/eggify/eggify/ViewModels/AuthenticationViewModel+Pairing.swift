//
//  AuthenticationViewModel+Pairing.swift
//  eggify
//
//  Sign in by scanning the pairing QR code shown by Eggify on the computer:
//  with the system Camera ("Open in Eggify" → eggify://pair?u=…&c=…), the
//  in-app scanner, or a pasted link.
//

import SwiftUI

/// Where a QR-code sign-in stands. Drives PairingStatusOverlay.
enum PairingFlowState: Equatable {
    case idle
    /// Signed in to another server: ask before switching.
    case confirmSwitch(PairingLink, currentHost: String)
    case connecting(host: String, insecure: Bool)
    case success(host: String, username: String)
    /// Saved and signed in on this phone without switching to it (a Mac
    /// added through the Mac it's paired with, or "Add only"): offer to switch.
    case added(hostId: String, name: String)
    case failed(host: String?, message: String, isExpired: Bool)

    var isActive: Bool { self != .idle }
}

extension AuthenticationViewModel {
    /// Entry point for eggify:// links (onOpenURL, cold start included).
    func handleIncomingURL(_ url: URL) {
        guard url.scheme?.lowercased() == PairingPayload.scheme else { return }
        handleScanned(url.absoluteString)
    }

    /// A string from the scanner, the pasteboard or a link.
    func handleScanned(_ text: String) {
        do {
            switch try PairingPayload.parse(text) {
            case .pair(let link):
                beginPairing(link)
            case .address(let url, _):
                // Address-only code: continue with a password login.
                prefillServerURL = url.absoluteString
                if isAuthenticated {
                    pairingState = .failed(host: PairingPayload.hostLabel(url),
                                           message: "That code is only the server address. To sign in to it, sign out first, or show a pairing code in Eggify (Settings → Remote access → Pair the iOS app).",
                                           isExpired: false)
                }
            }
        } catch {
            EggifyHaptics.error()
            pairingState = .failed(host: nil, message: error.localizedDescription, isExpired: false)
        }
    }

    /// Pair now, or ask first when signed in to a different server.
    func beginPairing(_ link: PairingLink) {
        if case .connecting = pairingState { return } // a second scan of the same code
        if isAuthenticated, let current = currentHost,
           Host.normalizeURL(current.url) != Host.normalizeURL(link.baseURL.absoluteString) {
            pairingState = .confirmSwitch(link, currentHost: PairingPayload.hostLabel(URL(string: current.url) ?? link.baseURL))
            return
        }
        Task { await redeem(link) }
    }

    func confirmSwitch() {
        guard case .confirmSwitch(let link, _) = pairingState else { return }
        Task { await redeem(link) }
    }

    /// "Add only": signed in to another server, keep it on screen and save
    /// the new one (with its login) for later.
    func addWithoutSwitching() {
        guard case .confirmSwitch(let link, _) = pairingState else { return }
        Task { await redeemAndSave(link, name: Self.hostName(for: link.baseURL), deviceName: PairingClient.deviceName) }
    }

    /// Switch to the server just added (the `.added` state's button).
    func switchToAddedHost() {
        guard case .added(let hostId, let name) = pairingState else { return }
        pairingState = .idle
        Task {
            do {
                try await switchMachine(to: hostId)
            } catch {
                pairingState = .failed(host: name, message: error.localizedDescription, isExpired: false)
            }
        }
    }

    /// Redeem `link` and keep its login in the Keychain for that server,
    /// without leaving the current one.
    func redeemAndSave(_ link: PairingLink, name: String, deviceName: String) async {
        pairingState = .connecting(host: name, insecure: link.isInsecureLocal)
        if let pin = link.pin {
            CertificatePins.store(pin, for: link.baseURL)
        }
        do {
            let result = try await pairingClient.redeem(link, deviceName: deviceName)
            let host = try await savePairedSession(result, link: link, name: name)
            loadSavedHosts()
            knownMacs.removeAll { Host.normalizeURL($0.baseURL) == Host.normalizeURL(host.url) }
            EggifyHaptics.success()
            pairingState = .added(hostId: host.id, name: host.name)
        } catch is CancellationError {
            pairingState = .idle
        } catch let error as PairingError {
            EggifyHaptics.error()
            pairingState = .failed(host: name, message: error.localizedDescription, isExpired: error.isExpired)
        } catch {
            EggifyHaptics.error()
            pairingState = .failed(host: name, message: error.localizedDescription, isExpired: false)
        }
    }

    /// The saved host for `link` (added if new) with the redeemed session
    /// stored for it.
    @discardableResult
    func savePairedSession(_ result: PairingRedeemResult, link: PairingLink, name: String) async throws -> Host {
        let urlString = link.baseURL.absoluteString
        let host: Host
        if let existing = hostManager.host(matching: urlString) {
            host = existing
            try? await hostManager.updateHost(id: existing.id, name: existing.name, url: existing.url, username: result.username)
        } else {
            host = try await hostManager.addHost(name: name, url: urlString, username: result.username)
        }
        try SecureStorage.shared.storeHostCredential(hostId: host.id, sessionToken: result.token, expiresAt: result.expiresAt)
        return host
    }

    func dismissPairing() {
        pairingState = .idle
    }

    /// Redeem the code, then store the session token in the Keychain for
    /// that server (other servers keep their logins) and sign in.
    func redeem(_ link: PairingLink) async {
        let host = link.hostLabel
        pairingState = .connecting(host: host, insecure: link.isInsecureLocal)
        // The pin must be in place before the first TLS handshake with the
        // server; it only ever applies to the address derived from it.
        if let pin = link.pin {
            CertificatePins.store(pin, for: link.baseURL)
        }
        do {
            let result = try await pairingClient.redeem(link)
            try await adoptPairedSession(result, link: link) // plays the success haptic
            pairingState = .success(host: host, username: result.username)
            try? await Task.sleep(for: .seconds(1.4))
            if case .success = pairingState { pairingState = .idle }
        } catch is CancellationError {
            pairingState = .idle
        } catch let error as PairingError {
            EggifyHaptics.error()
            pairingState = .failed(host: host, message: error.localizedDescription, isExpired: error.isExpired)
        } catch {
            EggifyHaptics.error()
            pairingState = .failed(host: host, message: error.localizedDescription, isExpired: false)
        }
    }

    private func adoptPairedSession(_ result: PairingRedeemResult, link: PairingLink) async throws {
        let host = try await savePairedSession(result, link: link, name: Self.hostName(for: link.baseURL))

        // Leaving another server: stop its live connection. Its login stays
        // in the Keychain, so switching back in Settings → Manage Hosts works.
        SessionsWebSocketViewModel.shared.stopMonitoring()
        // Rebuild the signed-in UI for the new server.
        isAuthenticated = false
        try await apiClient.switchHost(hostId: host.id)
        try await hostManager.switchHost(id: host.id)
        try apiClient.setSessionToken(result.token, expiresAt: result.expiresAt)
        prefillServerURL = nil
        finishSignIn()
    }

    static func hostName(for url: URL) -> String {
        guard let host = url.host else { return "Eggify" }
        if host == "127.0.0.1" || host == "localhost" { return "This Mac" }
        let first = host.split(separator: ".").first.map(String.init) ?? host
        return first.prefix(1).uppercased() + first.dropFirst()
    }
}
