//
//  AuthenticationViewModel.swift
//  eggify
//
//  Sign-in state with multi-host support and MFA.
//

import SwiftUI
import Combine

@MainActor
final class AuthenticationViewModel: ObservableObject {
    @Published var isAuthenticated = false
    @Published var isLoading = false
    @Published var errorMessage: String?

    /// Set when the password was accepted and a one-time code is needed.
    @Published var pendingMFAToken: String?

    @Published var savedHosts: [Host] = []
    @Published var currentHost: Host?

    /// QR-code pairing in progress (see AuthenticationViewModel+Pairing.swift).
    @Published var pairingState: PairingFlowState = .idle
    /// A server URL from an address-only QR code, for the login form.
    @Published var prefillServerURL: String?
    /// The in-app QR scanner sheet (presented by the app root).
    @Published var showScanner = false
    /// The card shown while switching to another machine (MachineSwitchOverlay).
    @Published var machineSwitch: MachineSwitch?
    /// Other Macs the current Mac knows and this phone hasn't saved yet
    /// (AuthenticationViewModel+PairedMachines.swift).
    @Published var knownMacs: [PairedMachine] = []
    /// The known Mac being added right now.
    @Published var addingMacID: String?

    let apiClient = EggifyAPIClient.shared
    let hostManager = HostManager.shared
    var pairingClient = PairingClient()
    private var cancellables = Set<AnyCancellable>()

    init() {
        savedHosts = hostManager.hosts
        currentHost = hostManager.currentHost
        isAuthenticated = apiClient.baseURL != nil && apiClient.sessionToken != nil

        NotificationCenter.default.publisher(for: .sessionExpired)
            .receive(on: RunLoop.main)
            .sink { [weak self] _ in
                guard let self, self.isAuthenticated else { return }
                SessionsWebSocketViewModel.shared.stopMonitoring()
                self.isAuthenticated = false
                self.errorMessage = "Your session expired. Please sign in again."
            }
            .store(in: &cancellables)
        // Macs paired over the relay before their names were looked up.
        if isAuthenticated { Task { await refreshMachineName() } }
    }

    // MARK: - Hosts

    func loadSavedHosts() {
        savedHosts = hostManager.hosts
        currentHost = hostManager.currentHost
    }

    func addNewHost(name: String, url: String, username: String? = nil) async throws -> Host {
        let host = try await hostManager.addHost(name: name, url: url, username: username)
        loadSavedHosts()
        return host
    }

    /// Switch to a saved host. Signs in silently if a valid token is stored.
    func switchHost(hostId: String) async throws {
        SessionsWebSocketViewModel.shared.stopMonitoring()
        LicenseMonitor.shared.reset()
        try await apiClient.switchHost(hostId: hostId)
        try await hostManager.switchHost(id: hostId)
        loadSavedHosts()
        isAuthenticated = apiClient.sessionToken != nil
        if !isAuthenticated {
            errorMessage = "Sign in to \(hostManager.currentHost?.name ?? "this host")."
        }
    }

    /// Switch to another saved machine with the switching card: it covers the
    /// rebuild (the new machine's cached list shows at once behind it) and
    /// leaves after a short hold, about 0.75 s in all.
    func switchMachine(to hostId: String) async throws {
        guard hostId != currentHost?.id, let target = savedHosts.first(where: { $0.id == hostId }) else { return }
        let from = savedHosts.firstIndex { $0.id == currentHost?.id } ?? 0
        let to = savedHosts.firstIndex { $0.id == hostId } ?? 0
        let card = MachineSwitch(name: target.name, hue: MachineLook.hue(target.name), direction: to >= from ? 1 : -1)
        let started = ContinuousClock.now
        machineSwitch = card
        EggifyHaptics.select()
        try? await Task.sleep(for: .milliseconds(200)) // the card is up
        do {
            try await switchHost(hostId: hostId)
        } catch {
            if machineSwitch?.id == card.id { machineSwitch = nil }
            throw error
        }
        let hold = Duration.milliseconds(550) - (ContinuousClock.now - started)
        if hold > .zero { try? await Task.sleep(for: hold) }
        if machineSwitch?.id == card.id { machineSwitch = nil }
        await refreshMachineName()
    }

    /// Names a Mac paired over the Eggify relay after its host name ("Studio")
    /// instead of its relay address, like the desktop app does. Only touches
    /// names pairing gave it (the user's own names stay).
    func refreshMachineName() async {
        guard let host = currentHost, MachineLook.isRelayPlaceholder(host.name, url: host.url) else { return }
        guard let info = try? await apiClient.json("/api/system-info"),
              let system = info["system"] as? [String: Any],
              let name = MachineLook.cleanedHostname(system["hostname"] as? String) else { return }
        try? await hostManager.updateHost(id: host.id, name: name, url: host.url, username: host.username)
        loadSavedHosts()
    }

    func deleteHost(hostId: String) async throws {
        let wasCurrent = apiClient.currentHostId == hostId
        if wasCurrent { await logout() }
        try? SecureStorage.shared.deleteHostCredential(hostId: hostId)
        try await hostManager.deleteHost(id: hostId)
        loadSavedHosts()
    }

    // MARK: - Authentication

    func configureServer(url: String) throws -> URL {
        guard let parsedURL = URL(string: Host.normalizeURL(url)) else {
            throw AuthError.invalidURL
        }
        try apiClient.configureServer(baseURL: parsedURL)
        return parsedURL
    }

    func login(username: String, password: String, hostId: String? = nil) async {
        let user = username.trimmingCharacters(in: .whitespaces)
        guard !user.isEmpty else { errorMessage = "Enter your username"; return }
        guard !password.isEmpty else { errorMessage = "Enter your password"; return }

        isLoading = true
        errorMessage = nil
        defer { isLoading = false }

        if let hostId {
            do {
                try await apiClient.switchHost(hostId: hostId)
                try await hostManager.switchHost(id: hostId)
                loadSavedHosts()
            } catch {
                errorMessage = "Couldn't select the host: \(error.localizedDescription)"
                return
            }
        }

        do {
            switch try await apiClient.login(username: user, password: password) {
            case .signedIn:
                finishSignIn()
            case .mfaRequired(let token):
                pendingMFAToken = token
            }
        } catch {
            EggifyHaptics.error()
            errorMessage = Self.describe(error)
        }
    }

    func verifyMFA(code: String) async {
        guard let token = pendingMFAToken else { return }
        let trimmed = code.trimmingCharacters(in: .whitespaces)
        guard !trimmed.isEmpty else { errorMessage = "Enter the code from your authenticator app"; return }

        isLoading = true
        errorMessage = nil
        defer { isLoading = false }

        do {
            try await apiClient.verifyMFA(temporaryToken: token, code: trimmed)
            finishSignIn()
        } catch {
            EggifyHaptics.error()
            errorMessage = Self.describe(error)
        }
    }

    func cancelMFA() {
        pendingMFAToken = nil
        errorMessage = nil
    }

    func finishSignIn() {
        pendingMFAToken = nil
        errorMessage = nil
        isAuthenticated = true
        EggifyHaptics.success()
        loadSavedHosts()
        Task { await refreshMachineName() }
    }

    /// Sign out on the server and on this device.
    func logout() async {
        SessionsWebSocketViewModel.shared.stopMonitoring()
        // Cached sessions and chats of this server stay only while signed in.
        SessionCache.shared.clear(host: apiClient.currentHostId)
        MediaCache.shared.clear(host: apiClient.currentHostId)
        ComposerModel.clearDrafts()
        await apiClient.logout()
        LicenseMonitor.shared.reset()
        isAuthenticated = false
        pendingMFAToken = nil
        errorMessage = nil
    }

    private static func describe(_ error: Error) -> String {
        if case EggifyAPIClient.EggifyAPIError.unauthorized = error {
            return "Wrong username or password."
        }
        return error.localizedDescription
    }
}

enum AuthError: LocalizedError {
    case invalidURL

    var errorDescription: String? {
        switch self {
        case .invalidURL:
            return "Invalid server URL format"
        }
    }
}
