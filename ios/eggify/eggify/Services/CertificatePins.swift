//
//  CertificatePins.swift
//  eggify
//
//  Certificate pins for servers reached through the Eggify relay.
//
//  In relay mode TLS ends in Eggify on the Mac, with its own key and a
//  self-signed certificate; the relay only forwards bytes. The pairing QR
//  code carries the key's pin (`p`: base64url of SHA-256 of the
//  SubjectPublicKeyInfo). For a pinned host the app accepts exactly that key,
//  whatever the certificate's name, issuer or dates say, and nothing else,
//  not even a certificate a public CA issued.
//
//  Relay addresses are derived from the key (<id>.<relay domain>, id = the
//  lower-case base32 of the first 20 bytes of the same hash), so a pin is
//  only stored for the address it belongs to: a scanned code can't plant a
//  pin for another server.
//

import CryptoKit
import Foundation
import Security

nonisolated enum CertificatePins {
    static let defaultsKey = "eggify.certificatePins"
    private static let lock = NSLock()

    /// "host:port", lower-cased; https's 443 when the port is missing.
    static func key(host: String, port: Int?) -> String {
        let p = (port ?? 0) > 0 ? port! : 443
        return "\(host.lowercased()):\(p)"
    }

    static func pin(host: String, port: Int?) -> String? {
        lock.lock()
        defer { lock.unlock() }
        let all = UserDefaults.standard.dictionary(forKey: defaultsKey) as? [String: String] ?? [:]
        return all[key(host: host, port: port)]
    }

    static func pin(for url: URL) -> String? {
        guard let host = url.host else { return nil }
        return pin(host: host, port: url.port)
    }

    /// Stores `pin` for the server at `url` (https only) if the pin belongs
    /// to that address. Returns whether it was stored.
    @discardableResult
    static func store(_ pin: String, for url: URL) -> Bool {
        guard url.scheme?.lowercased() == "https", let host = url.host, pinMatchesHost(pin, host: host) else {
            return false
        }
        lock.lock()
        defer { lock.unlock() }
        var all = UserDefaults.standard.dictionary(forKey: defaultsKey) as? [String: String] ?? [:]
        all[key(host: host, port: url.port)] = pin
        UserDefaults.standard.set(all, forKey: defaultsKey)
        return true
    }

    static func remove(for url: URL) {
        guard let host = url.host else { return }
        lock.lock()
        defer { lock.unlock() }
        var all = UserDefaults.standard.dictionary(forKey: defaultsKey) as? [String: String] ?? [:]
        all.removeValue(forKey: key(host: host, port: url.port))
        UserDefaults.standard.set(all, forKey: defaultsKey)
    }

    /// 43 base64url characters (a SHA-256).
    static func isValidPin(_ pin: String) -> Bool {
        pinBytes(pin) != nil
    }

    static func pinBytes(_ pin: String) -> Data? {
        guard pin.count == 43, pin.allSatisfy({ $0.isASCII && ($0.isLetter || $0.isNumber || $0 == "-" || $0 == "_") }) else {
            return nil
        }
        var b64 = pin.replacingOccurrences(of: "-", with: "+").replacingOccurrences(of: "_", with: "/")
        b64 += "="
        guard let data = Data(base64Encoded: b64), data.count == 32 else { return nil }
        return data
    }

    /// The relay name a pin belongs to.
    static func relayID(forPin pin: String) -> String? {
        guard let bytes = pinBytes(pin) else { return nil }
        return base32(bytes.prefix(20))
    }

    /// A pin is accepted only for the relay address derived from it: the
    /// host's first label must be the pin's relay id.
    static func pinMatchesHost(_ pin: String, host: String) -> Bool {
        guard let id = relayID(forPin: pin) else { return false }
        let first = host.lowercased().split(separator: ".", maxSplits: 1).first.map(String.init) ?? ""
        return first == id && host.contains(".")
    }

    /// Whether host looks like an Eggify relay address (<32-char id>.<domain>).
    static func looksLikeRelayHost(_ host: String) -> Bool {
        let parts = host.lowercased().split(separator: ".", maxSplits: 1)
        guard parts.count == 2, parts[0].count == 32 else { return false }
        return parts[0].allSatisfy { ("a"..."z").contains($0) || ("2"..."7").contains($0) }
    }

    /// Lower-case RFC 4648 base32 without padding.
    static func base32(_ data: Data) -> String {
        let alphabet = Array("abcdefghijklmnopqrstuvwxyz234567")
        var out = ""
        var buffer = 0
        var bits = 0
        for byte in data {
            buffer = (buffer << 8) | Int(byte)
            bits += 8
            while bits >= 5 {
                out.append(alphabet[(buffer >> (bits - 5)) & 31])
                bits -= 5
            }
        }
        if bits > 0 {
            out.append(alphabet[(buffer << (5 - bits)) & 31])
        }
        return out
    }

    // DER SubjectPublicKeyInfo header for an ECDSA P-256 key; the
    // uncompressed point (65 bytes) follows.
    private static let p256SPKIHeader: [UInt8] = [
        0x30, 0x59, 0x30, 0x13, 0x06, 0x07, 0x2a, 0x86, 0x48, 0xce, 0x3d, 0x02, 0x01,
        0x06, 0x08, 0x2a, 0x86, 0x48, 0xce, 0x3d, 0x03, 0x01, 0x07, 0x03, 0x42, 0x00,
    ]

    /// The pin of the server's leaf certificate key (P-256 only).
    static func serverPin(_ trust: SecTrust) -> String? {
        guard let chain = SecTrustCopyCertificateChain(trust) as? [SecCertificate],
              let leaf = chain.first,
              let key = SecCertificateCopyKey(leaf),
              let attrs = SecKeyCopyAttributes(key) as? [CFString: Any],
              (attrs[kSecAttrKeyType] as? String) == (kSecAttrKeyTypeECSECPrimeRandom as String),
              (attrs[kSecAttrKeySizeInBits] as? Int) == 256,
              let point = SecKeyCopyExternalRepresentation(key, nil) as Data?,
              point.count == 65 else {
            return nil
        }
        let digest = SHA256.hash(data: Data(p256SPKIHeader) + point)
        return Data(digest).base64EncodedString()
            .replacingOccurrences(of: "+", with: "-")
            .replacingOccurrences(of: "/", with: "_")
            .replacingOccurrences(of: "=", with: "")
    }

    /// Whether the server presented exactly the pinned key.
    static func trustMatches(_ trust: SecTrust, pin: String) -> Bool {
        serverPin(trust) == pin
    }
}
