//
//  PairingPayload.swift
//  eggify
//
//  What a scanned QR code (or an opened eggify:// link) means.
//
//  Eggify shows two kinds of QR code:
//   - a pairing code:  eggify://pair?u=<server URL>&c=<one-time code>[&p=<pin>]
//     → redeem it at the server and sign in without a password; p is the
//     server's certificate pin when it is reached through the Eggify relay
//     (see CertificatePins);
//   - the address only (Settings → Remote access): https://<server>
//     → prefill the server URL and sign in with a password.
//

import Foundation

/// A pairing link: the server to sign in to and the one-time code.
nonisolated struct PairingLink: Equatable {
    let baseURL: URL
    let code: String
    /// http to a local/LAN address: allowed, but shown with a warning.
    let isInsecureLocal: Bool
    /// The server's certificate pin (Eggify relay), checked on every connection.
    var pin: String? = nil

    /// "egg.ngrok.app", "192.168.1.5:3333"
    var hostLabel: String { PairingPayload.hostLabel(baseURL) }
}

/// The result of parsing a scanned string.
nonisolated enum ScannedPayload: Equatable {
    case pair(PairingLink)
    case address(URL, isInsecureLocal: Bool)
}

nonisolated enum PairingPayloadError: LocalizedError, Equatable {
    case empty
    case notEggify
    case missingServer
    case missingCode
    case insecureServer(String)
    case unsupportedServer
    case badPin

    var errorDescription: String? {
        switch self {
        case .empty:
            return "Nothing to read. Scan the QR code shown by Eggify on your computer."
        case .notEggify:
            return "That isn't an Eggify code. In Eggify on your computer, open Settings → Remote access → Pair the iOS app."
        case .missingServer:
            return "This pairing link has no server address. Show a new code on your computer."
        case .missingCode:
            return "This pairing link has no code. Show a new code on your computer."
        case .insecureServer(let host):
            return "\(host) uses plain http. Eggify only pairs over https (plain http is allowed only for addresses on this network)."
        case .unsupportedServer:
            return "The server address in this code isn't valid."
        case .badPin:
            return "This code's security check doesn't match its server address. Show a new code on your computer."
        }
    }
}

nonisolated enum PairingPayload {
    static let scheme = "eggify"

    /// Parse a scanned QR string or an opened URL.
    static func parse(_ raw: String) throws -> ScannedPayload {
        let text = raw.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !text.isEmpty else { throw PairingPayloadError.empty }
        guard let components = URLComponents(string: text), let scheme = components.scheme?.lowercased() else {
            throw PairingPayloadError.notEggify
        }

        switch scheme {
        case Self.scheme:
            // eggify://pair?u=…&c=…  (host "pair"; also accept eggify:pair?…)
            let target = (components.host ?? components.path).lowercased()
            guard target == "pair" else { throw PairingPayloadError.notEggify }
            let items = components.queryItems ?? []
            guard let server = items.first(where: { $0.name == "u" })?.value, !server.isEmpty else {
                throw PairingPayloadError.missingServer
            }
            guard let code = items.first(where: { $0.name == "c" })?.value?
                .trimmingCharacters(in: .whitespacesAndNewlines), !code.isEmpty else {
                throw PairingPayloadError.missingCode
            }
            guard code.count <= 128, code.allSatisfy({ $0.isASCII && ($0.isLetter || $0.isNumber || $0 == "-" || $0 == "_") }) else {
                throw PairingPayloadError.missingCode
            }
            let (url, insecure) = try validateServer(server)
            var pin: String?
            if let p = items.first(where: { $0.name == "p" })?.value, !p.isEmpty {
                guard !insecure, let host = url.host, CertificatePins.pinMatchesHost(p, host: host) else {
                    throw PairingPayloadError.badPin
                }
                pin = p
            }
            return .pair(PairingLink(baseURL: url, code: code, isInsecureLocal: insecure, pin: pin))

        case "https", "http":
            let (url, insecure) = try validateServer(text)
            return .address(url, isInsecureLocal: insecure)

        default:
            throw PairingPayloadError.notEggify
        }
    }

    /// The server URL reduced to scheme://host[:port]. https always; http only
    /// for local/loopback/LAN addresses.
    static func validateServer(_ raw: String) throws -> (URL, isInsecureLocal: Bool) {
        guard var components = URLComponents(string: raw.trimmingCharacters(in: .whitespacesAndNewlines)),
              let scheme = components.scheme?.lowercased(),
              let host = components.host, !host.isEmpty,
              components.user == nil, components.password == nil else {
            throw PairingPayloadError.unsupportedServer
        }
        let insecure: Bool
        switch scheme {
        case "https":
            insecure = false
        case "http":
            guard isLocalHost(host) else { throw PairingPayloadError.insecureServer(host) }
            insecure = true
        default:
            throw PairingPayloadError.unsupportedServer
        }
        components.scheme = scheme
        components.path = ""
        components.query = nil
        components.fragment = nil
        guard let url = components.url else { throw PairingPayloadError.unsupportedServer }
        return (url, insecure)
    }

    /// Loopback, link-local, private (RFC 1918 / ULA) addresses and .local names.
    static func isLocalHost(_ rawHost: String) -> Bool {
        let host = rawHost.lowercased().trimmingCharacters(in: CharacterSet(charactersIn: "[]"))
        if host == "localhost" || host.hasSuffix(".local") || host == "::1" { return true }
        if host.hasPrefix("fe80:") || host.hasPrefix("fc") || host.hasPrefix("fd") {
            return host.contains(":")
        }
        let parts = host.split(separator: ".").compactMap { Int($0) }
        guard parts.count == 4, parts.allSatisfy({ (0...255).contains($0) }) else { return false }
        switch (parts[0], parts[1]) {
        case (127, _), (10, _), (192, 168), (169, 254): return true
        case (172, 16...31): return true
        default: return false
        }
    }

    static func hostLabel(_ url: URL) -> String {
        guard let host = url.host else { return url.absoluteString }
        if let port = url.port { return "\(host):\(port)" }
        return host
    }
}
