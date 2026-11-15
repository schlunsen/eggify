//
//  PairingTests.swift
//  eggifyTests
//
//  QR-code pairing: payload parsing, the redeem client and its error states.
//

import Foundation
import Testing
@testable import eggify

// MARK: - Payload parsing

@Suite("Pairing payload parsing")
struct PairingPayloadTests {
    @Test func pairingLink() throws {
        let raw = "eggify://pair?u=https%3A%2F%2Fegg.ngrok.app&c=AbC_d-123"
        guard case .pair(let link) = try PairingPayload.parse(raw) else {
            Issue.record("expected a pairing link"); return
        }
        #expect(link.baseURL.absoluteString == "https://egg.ngrok.app")
        #expect(link.code == "AbC_d-123")
        #expect(link.isInsecureLocal == false)
        #expect(link.hostLabel == "egg.ngrok.app")
    }

    @Test func pairingLinkKeepsPortAndDropsPath() throws {
        guard case .pair(let link) = try PairingPayload.parse("  eggify://pair?u=https://127.0.0.1:4777/settings?x=1&c=abc\n") else {
            Issue.record("expected a pairing link"); return
        }
        // "u" is everything up to the first "&": the server's own query must
        // be percent-encoded by the server (it is), so ?x=1 is dropped here.
        #expect(link.baseURL.absoluteString == "https://127.0.0.1:4777")
        #expect(link.hostLabel == "127.0.0.1:4777")
    }

    @Test func httpOnlyForLocalAddresses() throws {
        guard case .pair(let lan) = try PairingPayload.parse("eggify://pair?u=http%3A%2F%2F192.168.1.20%3A3333&c=abc") else {
            Issue.record("expected a pairing link"); return
        }
        #expect(lan.isInsecureLocal)
        for local in ["http://localhost:3333", "http://127.0.0.1", "http://10.0.0.5", "http://172.20.1.1", "http://mac.local", "http://[::1]:3333"] {
            let encoded = local.addingPercentEncoding(withAllowedCharacters: .alphanumerics)!
            guard case .pair(let link) = try PairingPayload.parse("eggify://pair?u=\(encoded)&c=abc") else {
                Issue.record("\(local) should pair"); continue
            }
            #expect(link.isInsecureLocal, "\(local)")
        }
        #expect(throws: PairingPayloadError.insecureServer("evil.example.com")) {
            try PairingPayload.parse("eggify://pair?u=http%3A%2F%2Fevil.example.com&c=abc")
        }
        #expect(throws: PairingPayloadError.insecureServer("8.8.8.8")) {
            try PairingPayload.parse("eggify://pair?u=http%3A%2F%2F8.8.8.8&c=abc")
        }
    }

    @Test func addressOnlyCode() throws {
        #expect(try PairingPayload.parse("https://egg.ngrok.app/") == .address(URL(string: "https://egg.ngrok.app")!, isInsecureLocal: false))
        #expect(throws: PairingPayloadError.insecureServer("example.com")) {
            try PairingPayload.parse("http://example.com")
        }
    }

    @Test func rejectsEverythingElse() {
        let cases: [(String, PairingPayloadError)] = [
            ("", .empty),
            ("hello world", .notEggify),
            ("WIFI:S:home;T:WPA;P:secret;;", .notEggify),
            ("eggify://open?u=https%3A%2F%2Fx.app&c=abc", .notEggify),
            ("eggify://pair?c=abc", .missingServer),
            ("eggify://pair?u=https%3A%2F%2Fx.app", .missingCode),
            ("eggify://pair?u=https%3A%2F%2Fx.app&c=", .missingCode),
            ("eggify://pair?u=https%3A%2F%2Fx.app&c=a%20b", .missingCode),
            ("eggify://pair?u=ftp%3A%2F%2Fx.app&c=abc", .unsupportedServer),
            ("eggify://pair?u=https%3A%2F%2Fuser%3Apw%40x.app&c=abc", .unsupportedServer),
            ("javascript:alert(1)", .notEggify),
        ]
        for (raw, expected) in cases {
            #expect(throws: expected, "\(raw)") { try PairingPayload.parse(raw) }
        }
    }
}

// MARK: - Redeem client

/// Answers every request with the next canned response and records requests.
final class MockURLProtocol: URLProtocol {
    nonisolated(unsafe) static var handler: ((URLRequest) throws -> (Int, Data))?
    nonisolated(unsafe) static var requests: [URLRequest] = []

    override class func canInit(with request: URLRequest) -> Bool { true }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }

    override func startLoading() {
        var request = self.request
        if request.httpBody == nil, let stream = request.httpBodyStream {
            stream.open()
            var data = Data()
            let buffer = UnsafeMutablePointer<UInt8>.allocate(capacity: 4096)
            while stream.hasBytesAvailable {
                let n = stream.read(buffer, maxLength: 4096)
                if n <= 0 { break }
                data.append(buffer, count: n)
            }
            buffer.deallocate()
            stream.close()
            request.httpBody = data
        }
        Self.requests.append(request)
        do {
            guard let handler = Self.handler else { throw URLError(.badServerResponse) }
            let (status, body) = try handler(request)
            let response = HTTPURLResponse(url: request.url!, statusCode: status, httpVersion: "HTTP/1.1",
                                           headerFields: ["Content-Type": "application/json"])!
            client?.urlProtocol(self, didReceive: response, cacheStoragePolicy: .notAllowed)
            client?.urlProtocol(self, didLoad: body)
            client?.urlProtocolDidFinishLoading(self)
        } catch {
            client?.urlProtocol(self, didFailWithError: error)
        }
    }

    override func stopLoading() {}
}

@MainActor
@Suite("Pairing redeem client", .serialized)
struct PairingClientTests {
    let link = PairingLink(baseURL: URL(string: "https://egg.ngrok.app")!, code: "the-code", isInsecureLocal: false)

    private func client() -> PairingClient {
        let config = URLSessionConfiguration.ephemeral
        config.protocolClasses = [MockURLProtocol.self]
        MockURLProtocol.requests = []
        return PairingClient(session: URLSession(configuration: config))
    }

    private func respond(_ status: Int, _ json: String) {
        MockURLProtocol.handler = { _ in (status, Data(json.utf8)) }
    }

    @Test func redeemSuccess() async throws {
        let client = client()
        respond(200, #"{"token":"sess-123","token_type":"Bearer","expires_at":"2026-10-10T12:00:00Z","username":"dev","is_admin":true,"session_id":"s1"}"#)
        let result = try await client.redeem(link, deviceName: "Dev's iPhone", deviceModel: "iPhone17,1")
        #expect(result.token == "sess-123")
        #expect(result.username == "dev")
        #expect(result.isAdmin)
        #expect(result.expiresAt != nil)

        let request = try #require(MockURLProtocol.requests.first)
        #expect(request.httpMethod == "POST")
        #expect(request.url?.absoluteString == "https://egg.ngrok.app/api/auth/pairing/redeem")
        #expect(request.value(forHTTPHeaderField: "Authorization") == nil)
        let body = try #require(JSONSerialization.jsonObject(with: request.httpBody ?? Data()) as? [String: String])
        #expect(body == ["code": "the-code", "device_name": "Dev's iPhone", "device_model": "iPhone17,1"])
    }

    @Test func expiredOrUsedCode() async {
        let client = client()
        respond(401, #"{"error":"This pairing code is invalid or has expired.","code":"pairing_invalid"}"#)
        do {
            _ = try await client.redeem(link)
            Issue.record("expected an error")
        } catch let error as PairingError {
            #expect(error == .invalidOrExpired)
            #expect(error.isExpired)
        } catch {
            Issue.record("unexpected \(error)")
        }
    }

    @Test func errorStates() async {
        let cases: [(Int, String, PairingError)] = [
            (403, #"{"error":"Pairing needs an https connection to Eggify.","code":"https_required"}"#, .httpsRequired),
            (429, #"{"error":"Too many pairing attempts."}"#, .rateLimited),
            (404, #"{"error":"Cannot POST"}"#, .notSupported),
            (500, #"{"error":"Failed to create a session"}"#, .server(500, "Failed to create a session")),
            (200, #"{"nope":true}"#, .server(200, "The server sent an unexpected answer.")),
        ]
        for (status, json, expected) in cases {
            let client = client()
            respond(status, json)
            do {
                _ = try await client.redeem(link)
                Issue.record("\(status): expected an error")
            } catch let error as PairingError {
                #expect(error == expected, "\(status)")
            } catch {
                Issue.record("\(status): unexpected \(error)")
            }
        }
    }

    @Test func networkFailure() async {
        let client = client()
        MockURLProtocol.handler = { _ in throw URLError(.cannotConnectToHost) }
        do {
            _ = try await client.redeem(link)
            Issue.record("expected an error")
        } catch let error as PairingError {
            if case .network = error {} else { Issue.record("got \(error)") }
        } catch {
            Issue.record("unexpected \(error)")
        }
    }
}

// MARK: - Flow

@MainActor
@Suite("Pairing flow")
struct PairingFlowTests {
    @Test func invalidScanShowsError() {
        let vm = AuthenticationViewModel()
        vm.handleScanned("WIFI:S:home;;")
        guard case .failed(_, let message, let expired) = vm.pairingState else {
            Issue.record("expected failed, got \(vm.pairingState)"); return
        }
        #expect(!expired)
        #expect(message.contains("isn't an Eggify code"))
    }

    @Test func addressOnlyCodePrefillsServer() {
        let vm = AuthenticationViewModel()
        vm.isAuthenticated = false
        vm.handleScanned("https://egg.ngrok.app")
        #expect(vm.prefillServerURL == "https://egg.ngrok.app")
        #expect(vm.pairingState == .idle)
    }

    @Test func nonEggifyURLIsIgnored() {
        let vm = AuthenticationViewModel()
        vm.handleIncomingURL(URL(string: "https://example.com")!)
        #expect(vm.pairingState == .idle)
    }

    @Test func asksBeforeSwitchingServers() {
        let vm = AuthenticationViewModel()
        vm.isAuthenticated = true
        vm.currentHost = Host(name: "Home", url: "https://home.example.com")
        let link = PairingLink(baseURL: URL(string: "https://egg.ngrok.app")!, code: "c", isInsecureLocal: false)
        vm.beginPairing(link)
        #expect(vm.pairingState == .confirmSwitch(link, currentHost: "home.example.com"))
        vm.dismissPairing()
        #expect(vm.pairingState == .idle)
    }
}
