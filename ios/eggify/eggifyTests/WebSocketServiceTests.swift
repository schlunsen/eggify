//
//  WebSocketServiceTests.swift
//  eggifyTests
//
//  Drives the real `WebSocketService` against a local WebSocket server that
//  checks the upgrade the way the Eggify server does (same-origin check on
//  Origin vs Host, session cookie or an `auth` message).
//
//  Set EGGIFY_LIVE_URL / EGGIFY_LIVE_TOKEN (TEST_RUNNER_ prefixed for
//  xcodebuild) to also run the connection against a real server.
//

import Foundation
import Network
import Testing
@testable import eggify

// MARK: - Local test server

/// A minimal WebSocket server on 127.0.0.1 built on Network.framework.
nonisolated final class TestWebSocketServer: @unchecked Sendable {
    /// When true, the session cookie is ignored and the client has to send
    /// `{"type":"auth","token":...}` (a proxy that drops cookies).
    let requireAuthMessage: Bool
    let token: String

    private let queue = DispatchQueue(label: "eggify.TestWebSocketServer")
    private var listener: NWListener!
    private let lock = NSLock()
    private var _requests: [[String: String]] = []
    private var _rejected = 0
    private var _authMessages = 0
    private var connections: [NWConnection] = []

    var requests: [[String: String]] { lock.withLock { _requests } }
    var rejectedUpgrades: Int { lock.withLock { _rejected } }
    var authMessages: Int { lock.withLock { _authMessages } }
    var port: UInt16 { listener.port?.rawValue ?? 0 }
    var baseURL: URL { URL(string: "http://127.0.0.1:\(port)")! }

    init(token: String, requireAuthMessage: Bool = false) throws {
        self.token = token
        self.requireAuthMessage = requireAuthMessage

        // The request handler must be set before the options go into the
        // parameters (they are copied).
        let options = NWProtocolWebSocket.Options()
        options.autoReplyPing = true
        options.setClientRequestHandler(queue) { [weak self] _, headers in
            guard let self else { return .init(status: .reject, subprotocol: nil) }
            var map: [String: String] = [:]
            for header in headers { map[header.name.lowercased()] = header.value }
            let allowed = self.isSameOrigin(map)
            self.lock.withLock {
                self._requests.append(map)
                if !allowed { self._rejected += 1 }
            }
            return .init(status: allowed ? .accept : .reject, subprotocol: nil)
        }
        let parameters = NWParameters.tcp
        parameters.defaultProtocolStack.applicationProtocols.insert(options, at: 0)
        parameters.requiredLocalEndpoint = .hostPort(host: "127.0.0.1", port: .any)
        listener = try NWListener(using: parameters)

        listener.newConnectionHandler = { [weak self] connection in
            self?.accept(connection)
        }
    }

    func start() async throws {
        try await withCheckedThrowingContinuation { (continuation: CheckedContinuation<Void, Error>) in
            var resumed = false
            listener.stateUpdateHandler = { state in
                guard !resumed else { return }
                switch state {
                case .ready: resumed = true; continuation.resume()
                case .failed(let error): resumed = true; continuation.resume(throwing: error)
                default: break
                }
            }
            listener.start(queue: queue)
        }
    }

    func stop() {
        listener.cancel()
        lock.withLock { connections.forEach { $0.cancel() } }
    }

    /// The Eggify server's rule: an Origin, when present, must name the host
    /// the client connected to.
    private func isSameOrigin(_ headers: [String: String]) -> Bool {
        let expected = "127.0.0.1:\(port)"
        if let host = headers["host"], host != expected { return false }
        guard let origin = headers["origin"] else { return true }
        guard let url = URL(string: origin), let host = url.host else { return false }
        return "\(host):\(url.port ?? 80)" == expected
    }

    private func accept(_ connection: NWConnection) {
        lock.withLock { connections.append(connection) }
        // The upgrade request is seen during the handshake, so decide once
        // the connection is ready (like the server, which authenticates the
        // cookie on the upgrade and says auth_success unprompted).
        connection.stateUpdateHandler = { [weak self, weak connection] state in
            guard let self, let connection, case .ready = state else { return }
            let cookieOK = self.lock.withLock { self._requests.last?["cookie"] } == "session_token=\(self.token)"
            if cookieOK && !self.requireAuthMessage {
                self.send(["type": "auth_success"], on: connection)
            }
            self.receive(on: connection)
        }
        connection.start(queue: queue)
    }

    private func receive(on connection: NWConnection) {
        connection.receiveMessage { [weak self] data, _, _, error in
            guard let self, error == nil else { return }
            if let data,
               let message = (try? JSONSerialization.jsonObject(with: data)) as? [String: Any],
               message["type"] as? String == "auth" {
                self.lock.withLock { self._authMessages += 1 }
                if message["token"] as? String == self.token {
                    self.send(["type": "auth_success"], on: connection)
                } else {
                    self.send(["type": "auth_error"], on: connection)
                }
            }
            self.receive(on: connection)
        }
    }

    private func send(_ message: [String: Any], on connection: NWConnection) {
        guard let data = try? JSONSerialization.data(withJSONObject: message) else { return }
        let metadata = NWProtocolWebSocket.Metadata(opcode: .text)
        let context = NWConnection.ContentContext(identifier: "text", metadata: [metadata])
        connection.send(content: data, contentContext: context, isComplete: true, completion: .contentProcessed { _ in })
    }
}

// MARK: - Helpers

@MainActor
private func waitUntil(timeout: Duration = .seconds(10), _ condition: @MainActor () -> Bool) async -> Bool {
    let deadline = ContinuousClock.now + timeout
    while ContinuousClock.now < deadline {
        if condition() { return true }
        try? await Task.sleep(for: .milliseconds(50))
    }
    return condition()
}

@MainActor
private func pointClient(at baseURL: URL, token: String) throws {
    let api = EggifyAPIClient.shared
    try api.configureServer(baseURL: baseURL)
    try api.setSessionToken(token)
}

// MARK: - Tests

@Suite("WebSocket handshake headers")
struct WebSocketHandshakeHeaderTests {
    @Test func defaultPortIsLeftOutOfHostAndOrigin() throws {
        let url = try #require(URL(string: "wss://example.ngrok.io/agent/ws"))
        let headers = WebSocketService.handshakeHeaders(for: url, sessionToken: "tok")
        #expect(headers["Host"] == "example.ngrok.io", "a :443 Host fails the server's same-origin check behind ngrok")
        #expect(headers["Origin"] == "https://example.ngrok.io")
        #expect(headers["Cookie"] == "session_token=tok")
    }

    @Test func explicitPortsAreKept() throws {
        let url = try #require(URL(string: "ws://192.168.1.5:3333/agent/ws"))
        let headers = WebSocketService.handshakeHeaders(for: url, sessionToken: "tok")
        #expect(headers["Host"] == "192.168.1.5:3333")
        #expect(headers["Origin"] == "http://192.168.1.5:3333")

        let tls = try #require(URL(string: "wss://example.com:8443/eggify/agent/ws"))
        #expect(WebSocketService.handshakeHeaders(for: tls, sessionToken: "t")["Host"] == "example.com:8443")
    }

    @Test func ipv6HostIsBracketed() throws {
        let url = try #require(URL(string: "ws://[::1]:3333/agent/ws"))
        let headers = WebSocketService.handshakeHeaders(for: url, sessionToken: "t")
        #expect(headers["Host"] == "[::1]:3333")
        #expect(headers["Origin"] == "http://[::1]:3333")
    }
}

@MainActor
@Suite("WebSocketService connection", .serialized)
struct WebSocketServiceConnectionTests {
    @Test func authenticatesWithSessionCookie() async throws {
        let token = "test-token-\(UUID().uuidString)"
        let server = try TestWebSocketServer(token: token)
        try await server.start()
        defer { server.stop() }
        try pointClient(at: server.baseURL, token: token)

        let service = WebSocketService.shared
        service.disconnect()
        await service.connect()
        let authenticated = await waitUntil { service.state == .authenticated }
        let state = service.state
        service.disconnect()

        #expect(authenticated, "state stayed \(state), last error: \(service.lastError ?? "-")")
        #expect(server.rejectedUpgrades == 0, "upgrade headers: \(server.requests)")
        #expect(server.authMessages == 0, "the cookie should be enough: \(server.requests)")
    }

    @Test func fallsBackToAuthMessageWhenCookieIsDropped() async throws {
        let token = "test-token-\(UUID().uuidString)"
        let server = try TestWebSocketServer(token: token, requireAuthMessage: true)
        try await server.start()
        defer { server.stop() }
        try pointClient(at: server.baseURL, token: token)

        let service = WebSocketService.shared
        service.disconnect()
        await service.connect()
        let authenticated = await waitUntil { service.state == .authenticated }
        service.disconnect()

        #expect(authenticated, "last error: \(service.lastError ?? "-")")
        #expect(server.authMessages == 1)
    }

    @Test func queuedMessagesAreDeliveredAfterAuth() async throws {
        let token = "test-token-\(UUID().uuidString)"
        let server = try TestWebSocketServer(token: token)
        try await server.start()
        defer { server.stop() }
        try pointClient(at: server.baseURL, token: token)

        let service = WebSocketService.shared
        service.disconnect()
        // Sent before connecting: queued, then flushed once authenticated.
        #expect(service.send(["type": "ping"]) == false)
        let authenticated = await waitUntil { service.state == .authenticated }
        service.disconnect()
        #expect(authenticated)
    }

    /// Optional: the same connection against a real server (e.g. over ngrok).
    @Test func liveServerReachesAuthenticated() async throws {
        let env = ProcessInfo.processInfo.environment
        guard let urlString = env["EGGIFY_LIVE_URL"], let url = URL(string: urlString),
              let token = env["EGGIFY_LIVE_TOKEN"], !token.isEmpty else { return }
        try pointClient(at: url, token: token)

        let service = WebSocketService.shared
        service.disconnect()
        await service.connect()
        let authenticated = await waitUntil(timeout: .seconds(20)) { service.state == .authenticated }
        let error = service.lastError
        service.disconnect()
        #expect(authenticated, "last error: \(error ?? "-")")
    }
}
