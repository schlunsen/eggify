//
//  CertificatePinsTests.swift
//  eggifyTests
//
//  Certificate pins for the Eggify relay. Fixtures come from the Go side
//  (internal/relay: PinFromSPKI, IDFromSPKI) for the P-256 key with private
//  scalar 123456789.
//

import CryptoKit
import Foundation
import Testing
@testable import eggify

@Suite("Certificate pins (Eggify relay)", .serialized)
struct CertificatePinsTests {
    static let pin = "FAmDtsLLZ07J8tbn-5Qyo6gXWDlvaMrD9oODXBft9yU"
    static let id = "cqeyhnwczntu5sps23t7xfbsuoubowbz"
    static let spkiHeader = "3059301306072a8648ce3d020106082a8648ce3d030107034200"
    static let point = "04fb50388f29498d0a93ad25ec4c34037b9d3cc3cca4787eb6fedabe2b3003eac89f7765ca9d6288e6ff734f5cd08f3a5921cf54b21bb398b50ac0d2577fa07472"

    static func hex(_ s: String) -> Data {
        var data = Data()
        var i = s.startIndex
        while i < s.endIndex {
            let j = s.index(i, offsetBy: 2)
            data.append(UInt8(s[i..<j], radix: 16)!)
            i = j
        }
        return data
    }

    @Test func pinOfTheGoFixtureKey() {
        let digest = SHA256.hash(data: Self.hex(Self.spkiHeader) + Self.hex(Self.point))
        let pin = Data(digest).base64EncodedString()
            .replacingOccurrences(of: "+", with: "-")
            .replacingOccurrences(of: "/", with: "_")
            .replacingOccurrences(of: "=", with: "")
        #expect(pin == Self.pin)
    }

    @Test func relayIDFromPinMatchesGo() {
        #expect(CertificatePins.relayID(forPin: Self.pin) == Self.id)
        #expect(CertificatePins.isValidPin(Self.pin))
        #expect(!CertificatePins.isValidPin("short"))
        #expect(!CertificatePins.isValidPin(String(Self.pin.dropLast()) + "!"))
    }

    @Test func pinOnlyFitsItsOwnAddress() {
        #expect(CertificatePins.pinMatchesHost(Self.pin, host: "\(Self.id).r.example.com"))
        #expect(CertificatePins.pinMatchesHost(Self.pin, host: "\(Self.id.uppercased()).r.example.com"))
        #expect(!CertificatePins.pinMatchesHost(Self.pin, host: "egg.ngrok.app"))
        #expect(!CertificatePins.pinMatchesHost(Self.pin, host: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.r.example.com"))
        #expect(!CertificatePins.pinMatchesHost(Self.pin, host: Self.id))
    }

    @Test func recognisesRelayAddresses() {
        #expect(CertificatePins.looksLikeRelayHost("\(Self.id).r.example.com"))
        #expect(!CertificatePins.looksLikeRelayHost("example.ngrok.io"))
        #expect(!CertificatePins.looksLikeRelayHost(Self.id))
        #expect(!CertificatePins.looksLikeRelayHost("\(Self.id)1.r.example.com"))
    }

    @Test func storeKeepsPinsPerAddress() {
        let url = URL(string: "https://\(Self.id).r.example.com")!
        defer { CertificatePins.remove(for: url) }
        #expect(CertificatePins.store(Self.pin, for: url))
        #expect(CertificatePins.pin(host: "\(Self.id).r.example.com", port: 443) == Self.pin)
        #expect(CertificatePins.pin(host: "\(Self.id).r.example.com", port: 8443) == nil)
        // Never for plain http or another server.
        #expect(!CertificatePins.store(Self.pin, for: URL(string: "http://\(Self.id).r.example.com")!))
        #expect(!CertificatePins.store(Self.pin, for: URL(string: "https://evil.example.com")!))
        CertificatePins.remove(for: url)
        #expect(CertificatePins.pin(for: url) == nil)
    }

    @Test func pairingLinkCarriesThePin() throws {
        let server = "https://\(Self.id).r.example.com".addingPercentEncoding(withAllowedCharacters: .alphanumerics)!
        guard case .pair(let link) = try PairingPayload.parse("eggify://pair?u=\(server)&c=abc&p=\(Self.pin)") else {
            Issue.record("expected a pairing link"); return
        }
        #expect(link.pin == Self.pin)
    }

    @Test func pairingLinkRefusesAPinForAnotherAddress() {
        #expect(throws: PairingPayloadError.badPin) {
            try PairingPayload.parse("eggify://pair?u=https%3A%2F%2Fegg.ngrok.app&c=abc&p=\(Self.pin)")
        }
    }

    @Test func pairingLinkWithoutPinIsUnchanged() throws {
        guard case .pair(let link) = try PairingPayload.parse("eggify://pair?u=https%3A%2F%2Fegg.ngrok.app&c=abc") else {
            Issue.record("expected a pairing link"); return
        }
        #expect(link.pin == nil)
    }
}
