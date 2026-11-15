//
//  WebSocketService.swift
//  eggify
//
//  Real-time connection to the agent server (`/agent/ws`).
//
//  - Starscream transport (HTTP/1.1, works through proxies like ngrok).
//  - Authenticates as the signed-in user with the session token (cookie on
//    the upgrade, then an explicit `auth` message if the cookie was dropped).
//    The server API key is never fetched or stored on the device.
//  - Every inbound frame is decoded with JSONSerialization so no field is lost
//    and handed to the event handler as `(type, payload)`.
//  - Reconnects with capped exponential backoff for as long as the app is in
//    the foreground and signed in; resumes when the app comes back or the
//    network returns.
//  - Only the sessions that are open on screen are (re)subscribed: the server
//    routes a session's permission prompts to the connection that subscribed
//    last, so subscribing to everything would steal them from other devices.
//

import Foundation
import Combine
import Network
import Starscream
import UIKit

// MARK: - Notifications

extension Notification.Name {
    /// Posted when the server rejects the session token (expired/revoked).
    static let sessionExpired = Notification.Name("sessionExpired")
}

// MARK: - Event handler

protocol WebSocketEventHandler: AnyObject {
    /// A server message. `payload` is the full decoded JSON object.
    func handleServerEvent(type: String, payload: [String: Any])
    /// Connection became usable (authenticated) or was lost.
    func onConnectionStatusChanged(_ isConnected: Bool)
}

// MARK: - Connection state

enum WebSocketConnectionState: Equatable {
    case disconnected
    case connecting
    case connected        // transport up, waiting for auth
    case authenticated
    case reconnecting(attempt: Int, delay: TimeInterval)

    var isUsable: Bool { self == .authenticated }

    var label: String {
        switch self {
        case .disconnected: return "Offline"
        case .connecting, .connected: return "Connecting…"
        case .authenticated: return "Live"
        case .reconnecting(let attempt, _): return attempt > 2 ? "Reconnecting…" : "Connecting…"
        }
    }
}

// MARK: - Network monitor

/// Publishes whether the device has a usable network path.
final class NetworkMonitor: ObservableObject {
    @Published private(set) var isConnected = true

    private let monitor = NWPathMonitor()
    private let queue = DispatchQueue(label: "eggify.NetworkMonitor")

    init() {
        monitor.pathUpdateHandler = { [weak self] path in
            let satisfied = path.status == .satisfied
            Task { @MainActor in
                guard let self, self.isConnected != satisfied else { return }
                self.isConnected = satisfied
            }
        }
        monitor.start(queue: queue)
    }

    deinit {
        monitor.cancel()
    }
}

// MARK: - Starscream bridge

/// Receives Starscream callbacks on its background queue and forwards them to
/// the service on the main actor, tagged with the socket generation so events
/// from a socket that was already replaced are ignored.
nonisolated private final class WebSocketDelegateBridge: NSObject, Starscream.WebSocketDelegate, @unchecked Sendable {
    weak var service: WebSocketService?
    let generation: Int

    init(service: WebSocketService, generation: Int) {
        self.service = service
        self.generation = generation
    }

    func didReceive(event: Starscream.WebSocketEvent, client: any Starscream.WebSocketClient) {
        let generation = self.generation
        // Decode text off the main thread; large message batches can be big.
        var decoded: [String: Any]?
        if case .text(let text) = event {
            decoded = WebSocketDelegateBridge.decode(Data(text.utf8))
        } else if case .binary(let data) = event {
            decoded = WebSocketDelegateBridge.decode(data)
        }
        let payload = decoded.map(UncheckedPayload.init)
        Task { @MainActor [weak service] in
            service?.handleTransportEvent(event, payload: payload?.value, generation: generation)
        }
    }

    static func decode(_ data: Data) -> [String: Any]? {
        (try? JSONSerialization.jsonObject(with: data)) as? [String: Any]
    }
}

/// JSON objects from JSONSerialization are immutable value trees; safe to hand over.
nonisolated private struct UncheckedPayload: @unchecked Sendable {
    let value: [String: Any]
}

// MARK: - Service

@MainActor
final class WebSocketService: ObservableObject {
    static let shared = WebSocketService()

    private static let callbackQueue = DispatchQueue(label: "eggify.websocket", qos: .userInitiated)

    @Published private(set) var state: WebSocketConnectionState = .disconnected
    @Published private(set) var lastError: String?

    var isConnected: Bool { state == .connected || state == .authenticated }
    var isAuthenticated: Bool { state == .authenticated }

    weak var eventHandler: WebSocketEventHandler?

    // Tuning
    private let initialReconnectDelay: TimeInterval = 1
    private let maxReconnectDelay: TimeInterval = 30
    private let pingInterval: TimeInterval = 25
    private let pongTimeout: TimeInterval = 60
    private let cookieAuthGrace: Duration = .milliseconds(700)
    private let authTimeout: Duration = .seconds(8)
    private let maxQueuedMessages = 50

    private let apiClient = EggifyAPIClient.shared
    private let networkMonitor = NetworkMonitor()

    private var socket: Starscream.WebSocket?
    private var bridge: WebSocketDelegateBridge?
    private var generation = 0

    private var reconnectAttempts = 0
    private var reconnectTask: Task<Void, Never>?
    private var authTask: Task<Void, Never>?
    private var pingTask: Task<Void, Never>?
    private var lastPong = Date()

    /// Messages sent before the connection was authenticated.
    private var pendingMessages: [[String: Any]] = []
    /// Sessions open on screen; re-subscribed after every reconnect.
    private var activeSubscriptions: [String: Int] = [:]

    /// Whether the app wants a connection (signed in and in the foreground).
    private var wantsConnection = false
    private var isInBackground = false
    private var backgroundedAt: Date?

    private var cancellables = Set<AnyCancellable>()

    private init() {
        observeLifecycle()
        observeNetwork()
    }

    // MARK: Public API

    /// Start (or keep) a connection. Returns once authenticated, failed, or
    /// after a short timeout; queued sends are delivered when auth completes.
    func connect() async {
        wantsConnection = true
        if state == .disconnected {
            openSocket()
        }
        // Wait briefly so callers that immediately send have a live socket.
        let deadline = Date().addingTimeInterval(4)
        while !isAuthenticated, Date() < deadline, wantsConnection {
            if case .reconnecting = state { break }
            try? await Task.sleep(for: .milliseconds(50))
        }
    }

    /// Close the connection and stop reconnecting (e.g. on logout).
    func disconnect() {
        wantsConnection = false
        pendingMessages.removeAll()
        activeSubscriptions.removeAll()
        teardownSocket()
        setState(.disconnected)
    }

    /// Reconnect right away (pull-to-refresh, "retry" buttons).
    func reconnectNow() {
        wantsConnection = true
        reconnectAttempts = 0
        teardownSocket()
        setState(.disconnected)
        openSocket()
    }

    /// Send a message. Queued (and delivered after auth) while not connected.
    @discardableResult
    func send(_ message: [String: Any]) -> Bool {
        guard isAuthenticated else {
            enqueue(message)
            if state == .disconnected { Task { await connect() } }
            return false
        }
        return write(message)
    }

    // MARK: Session helpers

    /// Subscribe to a session's live events. Reference-counted per screen.
    func subscribe(sessionId: String) {
        activeSubscriptions[sessionId, default: 0] += 1
        if activeSubscriptions[sessionId] == 1 {
            send(["type": "subscribe_session", "session_id": sessionId])
        }
    }

    /// Stop re-subscribing a session after reconnects (the server has no
    /// unsubscribe; this only affects what we ask for next time).
    func unsubscribe(sessionId: String) {
        guard let count = activeSubscriptions[sessionId] else { return }
        if count <= 1 {
            activeSubscriptions.removeValue(forKey: sessionId)
        } else {
            activeSubscriptions[sessionId] = count - 1
        }
    }

    @discardableResult
    func sendPrompt(_ sessionId: String, prompt: String, images: [(mediaType: String, base64: String)] = []) -> Bool {
        if images.isEmpty {
            return send(["type": "send_prompt", "session_id": sessionId, "prompt": prompt])
        }
        var blocks: [[String: Any]] = images.map {
            ["type": "image", "source": ["type": "base64", "media_type": $0.mediaType, "data": $0.base64]]
        }
        if !prompt.isEmpty {
            blocks.append(["type": "text", "text": prompt])
        }
        return send(["type": "send_prompt", "session_id": sessionId, "content": blocks])
    }

    @discardableResult
    func interruptSession(_ sessionId: String) -> Bool {
        send(["type": "interrupt_session", "session_id": sessionId])
    }

    @discardableResult
    func stopLoop(_ sessionId: String) -> Bool {
        send(["type": "stop_loop", "session_id": sessionId])
    }

    @discardableResult
    func respondToPermission(sessionId: String, permissionId: String, approved: Bool) -> Bool {
        send(["type": "permission_response", "session_id": sessionId,
              "permission_id": permissionId, "approved": approved])
    }

    @discardableResult
    func answerQuestion(sessionId: String, questionId: String, answers: [String]) -> Bool {
        send(["type": "user_question_response", "session_id": sessionId,
              "question_id": questionId, "answers": answers])
    }

    @discardableResult
    func dismissQuestion(sessionId: String, questionId: String) -> Bool {
        send(["type": "user_question_dismiss", "session_id": sessionId, "question_id": questionId])
    }

    /// "Keep waiting": stops the auto-answer timer of one question.
    @discardableResult
    func holdQuestion(sessionId: String, questionId: String) -> Bool {
        send(["type": "user_question_hold", "session_id": sessionId, "question_id": questionId])
    }

    @discardableResult
    func changeModel(sessionId: String, provider: String, model: String) -> Bool {
        send(["type": "change_session_model", "session_id": sessionId,
              "provider": provider, "model": model])
    }

    @discardableResult
    func setYOLOMode(sessionId: String, enabled: Bool) -> Bool {
        send(["type": "toggle_yolo_mode", "session_id": sessionId, "enabled": enabled])
    }

    @discardableResult
    func deleteSession(_ sessionId: String) -> Bool {
        send(["type": "delete_session", "session_id": sessionId])
    }

    @discardableResult
    func endSession(_ sessionId: String) -> Bool {
        send(["type": "end_session", "session_id": sessionId])
    }

    /// Create a new agent session. Returns the client-chosen session id.
    @discardableResult
    func createSession(
        workingDirectory: String,
        modelProvider: String,
        model: String,
        permissionMode: String = "default",
        tools: [String] = [],
        systemPrompt: String? = nil,
        agentName: String? = nil,
        projectId: String? = nil,
        projectAreaId: String? = nil,
        selectedAvatarId: Int64? = nil,
        contextSummary: String? = nil,
        parentSessionId: String? = nil,
        baseURL: String? = nil,
        yoloMode: Bool = false,
        sandboxed: Bool? = nil,
        effortLevel: String? = nil
    ) -> String {
        let sessionId = UUID().uuidString.lowercased()

        var options: [String: Any] = [
            "working_directory": workingDirectory,
            "provider": modelProvider,
            "model": model,
            "permission_mode": permissionMode,
            "tools": tools
        ]
        if let systemPrompt { options["system_prompt"] = systemPrompt }
        if let agentName, !agentName.isEmpty { options["agent_name"] = agentName }
        if let projectId { options["project_id"] = projectId }
        if let projectAreaId { options["project_area_id"] = projectAreaId }
        if let selectedAvatarId { options["selected_avatar_id"] = selectedAvatarId }
        if let contextSummary { options["context_summary"] = contextSummary }
        if let parentSessionId { options["parent_session_id"] = parentSessionId }
        if let baseURL, !baseURL.isEmpty { options["base_url"] = baseURL }
        if let sandboxed { options["sandboxed"] = sandboxed }
        if let effortLevel, !effortLevel.isEmpty { options["effort_level"] = effortLevel }
        if yoloMode {
            options["dangerously_skip_permissions"] = true
            options["allow_dangerously_skip_permissions"] = true
        }

        send(["type": "create_session", "session_id": sessionId, "options": options])
        return sessionId
    }

    // MARK: Socket lifecycle

    private func openSocket() {
        guard wantsConnection, !isInBackground else { return }
        guard let baseURL = apiClient.baseURL, let token = apiClient.sessionToken else {
            lastError = "Not signed in"
            setState(.disconnected)
            return
        }
        guard networkMonitor.isConnected else {
            lastError = "You're offline"
            scheduleReconnect()
            return
        }

        teardownSocket()
        reconnectTask?.cancel()
        reconnectTask = nil

        guard let wsURL = Self.webSocketURL(for: baseURL) else {
            lastError = "Invalid server URL"
            setState(.disconnected)
            return
        }

        var request = URLRequest(url: wsURL)
        request.timeoutInterval = 15
        for (name, value) in Self.handshakeHeaders(for: wsURL, sessionToken: token) {
            request.setValue(value, forHTTPHeaderField: name)
        }

        let host = wsURL.host ?? ""
        let isLoopback = ["localhost", "127.0.0.1", "::1"].contains(host)
        let certPinner: CertificatePinning?
        if let pin = CertificatePins.pin(for: wsURL) {
            certPinner = PinnedKeySecurity(pin: pin)
        } else {
            certPinner = isLoopback ? nil : FoundationSecurity()
        }
        let ws = Starscream.WebSocket(request: request, certPinner: certPinner)
        // Starscream calls back on the main queue by default; use a serial
        // background queue so the bridge really decodes frames off the main
        // thread (it hops to the main actor itself, in order).
        ws.callbackQueue = Self.callbackQueue

        generation += 1
        let bridge = WebSocketDelegateBridge(service: self, generation: generation)
        ws.delegate = bridge
        self.bridge = bridge
        self.socket = ws

        setState(.connecting)
        ws.connect()
    }

    /// `https://host[:port]/prefix` → `wss://host[:port]/prefix/agent/ws`
    static func webSocketURL(for baseURL: URL) -> URL? {
        guard var components = URLComponents(url: baseURL, resolvingAgainstBaseURL: false) else { return nil }
        components.scheme = baseURL.scheme == "http" ? "ws" : "wss"
        var path = components.path
        while path.hasSuffix("/") { path.removeLast() }
        components.path = path + "/agent/ws"
        components.query = nil
        components.fragment = nil
        return components.url
    }

    /// Headers for the upgrade request: the session cookie plus explicit
    /// `Host` and `Origin` values without the default port.
    ///
    /// Starscream otherwise sends `Host: example.com:443` and
    /// `Origin: wss://example.com`. Proxies such as ngrok pass that Host
    /// through, and the server's same-origin check then sees
    /// "example.com" ≠ "example.com:443" and answers 403, so the socket never
    /// opened over remote access (it kept "Reconnecting…").
    nonisolated static func handshakeHeaders(for wsURL: URL, sessionToken: String) -> [String: String] {
        var headers = ["Cookie": "session_token=\(sessionToken)"]
        guard let host = wsURL.host(percentEncoded: false), !host.isEmpty else { return headers }
        let secure = wsURL.scheme?.lowercased() == "wss"
        var authority = host.contains(":") ? "[\(host)]" : host
        if let port = wsURL.port, port != (secure ? 443 : 80) {
            authority += ":\(port)"
        }
        headers["Host"] = authority
        headers["Origin"] = (secure ? "https://" : "http://") + authority
        return headers
    }

    private func teardownSocket() {
        authTask?.cancel()
        authTask = nil
        pingTask?.cancel()
        pingTask = nil
        generation += 1 // invalidates events from the old socket
        bridge?.service = nil
        bridge = nil
        socket?.delegate = nil
        socket?.forceDisconnect()
        socket = nil
    }

    private func setState(_ newState: WebSocketConnectionState) {
        guard state != newState else { return }
        let wasUsable = state.isUsable
        state = newState
        if wasUsable != newState.isUsable {
            eventHandler?.onConnectionStatusChanged(newState.isUsable)
        }
    }

    private func connectionLost(reason: String) {
        guard socket != nil else { return }
        lastError = reason
        teardownSocket()
        setState(.disconnected)
        scheduleReconnect()
    }

    private func scheduleReconnect() {
        guard wantsConnection, !isInBackground, apiClient.sessionToken != nil else { return }
        guard reconnectTask == nil else { return }

        reconnectAttempts += 1
        let base = min(initialReconnectDelay * pow(2, Double(reconnectAttempts - 1)), maxReconnectDelay)
        let delay = base * Double.random(in: 0.8...1.2)
        setState(.reconnecting(attempt: reconnectAttempts, delay: delay))

        reconnectTask = Task { [weak self] in
            try? await Task.sleep(for: .seconds(delay))
            guard let self, !Task.isCancelled else { return }
            self.reconnectTask = nil
            self.openSocket()
        }
    }

    // MARK: Transport events

    fileprivate func handleTransportEvent(_ event: Starscream.WebSocketEvent, payload: [String: Any]?, generation: Int) {
        guard generation == self.generation else { return }

        switch event {
        case .connected:
            reconnectAttempts = 0
            lastError = nil
            lastPong = Date()
            setState(.connected)
            startPing()
            startAuth()

        case .text, .binary:
            if let payload { handleServerMessage(payload) }

        case .pong:
            lastPong = Date()

        case .ping:
            break

        case .viabilityChanged(let viable):
            if !viable { connectionLost(reason: "Connection interrupted") }

        case .reconnectSuggested(let suggested):
            if suggested { connectionLost(reason: "Network changed") }

        case .disconnected(let reason, _):
            connectionLost(reason: reason.isEmpty ? "Disconnected" : reason)

        case .peerClosed:
            connectionLost(reason: "Server closed the connection")

        case .cancelled:
            connectionLost(reason: "Connection cancelled")

        case .error(let error):
            let message: String
            if let wsError = error as? WSError { message = wsError.message }
            else if case .notAnUpgrade(let status, _)? = error as? HTTPUpgradeError {
                if LicenseGate.isLicenseRequired(upgradeStatus: status) {
                    LicenseGate.announce()
                    message = LicenseGate.message
                } else {
                    message = "Server refused the live connection (HTTP \(status))"
                }
            }
            else if let error { message = error.localizedDescription }
            else { message = "Connection error" }
            connectionLost(reason: message)
        }
    }

    /// Auth: the session cookie on the upgrade usually authenticates us and
    /// the server says `auth_success` unprompted. If not (a proxy dropped the
    /// cookie), present the session token explicitly.
    private func startAuth() {
        authTask?.cancel()
        let gen = generation
        authTask = Task { [weak self] in
            guard let self else { return }
            try? await Task.sleep(for: self.cookieAuthGrace)
            guard !Task.isCancelled, gen == self.generation, self.state == .connected else { return }
            guard let token = self.apiClient.sessionToken else {
                self.connectionLost(reason: "Not signed in")
                return
            }
            self.write(["type": "auth", "token": token])
            try? await Task.sleep(for: self.authTimeout)
            guard !Task.isCancelled, gen == self.generation, self.state == .connected else { return }
            self.connectionLost(reason: "Authentication timed out")
        }
    }

    private func startPing() {
        pingTask?.cancel()
        let gen = generation
        pingTask = Task { [weak self] in
            while let self, !Task.isCancelled, gen == self.generation {
                try? await Task.sleep(for: .seconds(self.pingInterval))
                guard !Task.isCancelled, gen == self.generation else { return }
                if Date().timeIntervalSince(self.lastPong) > self.pongTimeout {
                    self.connectionLost(reason: "Connection timed out")
                    return
                }
                self.socket?.write(ping: Data())
            }
        }
    }

    // MARK: Server messages

    private func handleServerMessage(_ payload: [String: Any]) {
        guard let type = payload["type"] as? String else { return }

        switch type {
        case "auth_success":
            guard state != .authenticated else { return }
            authTask?.cancel()
            authTask = nil
            setState(.authenticated)
            for sessionId in activeSubscriptions.keys {
                write(["type": "subscribe_session", "session_id": sessionId])
            }
            let queued = pendingMessages
            pendingMessages.removeAll()
            for message in queued where !isRedundantSubscribe(message) {
                write(message)
            }

        case "auth_error":
            // The server rejected our session token: it expired or was revoked.
            lastError = "Session expired"
            wantsConnection = false
            teardownSocket()
            setState(.disconnected)
            try? apiClient.clearCredentials()
            NotificationCenter.default.post(name: .sessionExpired, object: nil)

        case "pong":
            lastPong = Date()

        case "error" where LicenseGate.isLicenseRequired(wsPayload: payload):
            LicenseGate.announce()
            eventHandler?.handleServerEvent(type: type, payload: payload)

        default:
            eventHandler?.handleServerEvent(type: type, payload: payload)
        }
    }

    private func isRedundantSubscribe(_ message: [String: Any]) -> Bool {
        guard message["type"] as? String == "subscribe_session",
              let id = message["session_id"] as? String else { return false }
        return activeSubscriptions[id] != nil
    }

    // MARK: Sending

    private func enqueue(_ message: [String: Any]) {
        // Collapse duplicate subscribes; cap the queue.
        if isRedundantSubscribe(message), pendingMessages.contains(where: { NSDictionary(dictionary: $0).isEqual(to: message) }) {
            return
        }
        pendingMessages.append(message)
        if pendingMessages.count > maxQueuedMessages {
            pendingMessages.removeFirst(pendingMessages.count - maxQueuedMessages)
        }
    }

    @discardableResult
    private func write(_ message: [String: Any]) -> Bool {
        guard let socket, isConnected,
              let data = try? JSONSerialization.data(withJSONObject: message),
              let text = String(data: data, encoding: .utf8) else {
            return false
        }
        socket.write(string: text)
        return true
    }

    // MARK: Lifecycle & network

    private func observeLifecycle() {
        let center = NotificationCenter.default
        center.publisher(for: UIApplication.didEnterBackgroundNotification)
            .sink { [weak self] _ in
                guard let self else { return }
                self.isInBackground = true
                self.backgroundedAt = Date()
                self.reconnectTask?.cancel()
                self.reconnectTask = nil
                self.pingTask?.cancel()
            }
            .store(in: &cancellables)

        center.publisher(for: UIApplication.willEnterForegroundNotification)
            .sink { [weak self] _ in
                guard let self else { return }
                self.isInBackground = false
                let away = self.backgroundedAt.map { Date().timeIntervalSince($0) } ?? .infinity
                self.backgroundedAt = nil
                guard self.wantsConnection else { return }
                // iOS drops sockets of suspended apps without telling us.
                // After more than a few seconds away, start over.
                if away > 5 || !self.isAuthenticated {
                    self.reconnectNow()
                } else {
                    self.startPing()
                }
            }
            .store(in: &cancellables)
    }

    private func observeNetwork() {
        networkMonitor.$isConnected
            .removeDuplicates()
            .dropFirst()
            .sink { [weak self] online in
                guard let self, self.wantsConnection else { return }
                if online {
                    self.reconnectNow()
                } else {
                    self.lastError = "You're offline"
                    self.teardownSocket()
                    self.setState(.disconnected)
                }
            }
            .store(in: &cancellables)
    }
}

/// Starscream certificate check for a server paired through the Eggify
/// relay: accepts exactly the pinned key (see CertificatePins).
final class PinnedKeySecurity: CertificatePinning {
    let pin: String

    init(pin: String) {
        self.pin = pin
    }

    func evaluateTrust(trust: SecTrust, domain: String?, completion: ((PinningState) -> ())) {
        if CertificatePins.trustMatches(trust, pin: pin) {
            completion(.success)
        } else {
            completion(.failed(nil))
        }
    }
}
