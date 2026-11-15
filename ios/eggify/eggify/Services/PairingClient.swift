//
//  PairingClient.swift
//  eggify
//
//  Redeems a one-time pairing code (POST /api/auth/pairing/redeem). The
//  server answers with an ordinary user session token, used as
//  `Authorization: Bearer` from then on, exactly like a password login.
//

import Foundation
import UIKit

nonisolated struct PairingRedeemResult: Decodable, Equatable {
    let token: String
    let expiresAt: Date?
    let username: String
    let isAdmin: Bool
    let sessionId: String?

    enum CodingKeys: String, CodingKey {
        case token, username
        case expiresAt = "expires_at"
        case isAdmin = "is_admin"
        case sessionId = "session_id"
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        token = try c.decode(String.self, forKey: .token)
        username = try c.decode(String.self, forKey: .username)
        isAdmin = try c.decodeIfPresent(Bool.self, forKey: .isAdmin) ?? false
        sessionId = try c.decodeIfPresent(String.self, forKey: .sessionId)
        expiresAt = EggifyDate.parse(try c.decodeIfPresent(String.self, forKey: .expiresAt))
    }
}

nonisolated enum PairingError: LocalizedError, Equatable {
    /// Unknown, used, replaced or expired code (the server doesn't say which).
    case invalidOrExpired
    case httpsRequired
    case rateLimited
    case notSupported
    case server(Int, String?)
    case network(String)

    var isExpired: Bool { self == .invalidOrExpired }

    var errorDescription: String? {
        switch self {
        case .invalidOrExpired:
            return "This code has expired or was already used."
        case .httpsRequired:
            return "The server only accepts pairing over https."
        case .rateLimited:
            return "Too many attempts. Wait a few minutes and try again."
        case .notSupported:
            return "This Eggify server doesn't support QR pairing yet. Update Eggify, or sign in with your password."
        case .server(let status, let message):
            return message ?? "The server answered with an error (\(status))."
        case .network(let message):
            return "Couldn't reach the server. \(message)"
        }
    }
}

struct PairingClient {
    var session: URLSession

    init(session: URLSession = EggifyAPIClient.shared.session) {
        self.session = session
    }

    /// The name shown in Eggify → Signed-in devices.
    static var deviceName: String {
        let name = UIDevice.current.name
        return name.isEmpty ? UIDevice.current.model : name
    }

    /// e.g. "iPhone17,1" (the simulator reports its simulated model).
    static var deviceModel: String {
        if let sim = ProcessInfo.processInfo.environment["SIMULATOR_MODEL_IDENTIFIER"] { return sim }
        var info = utsname()
        uname(&info)
        return withUnsafePointer(to: &info.machine) {
            $0.withMemoryRebound(to: CChar.self, capacity: 1) { String(cString: $0) }
        }
    }

    func redeem(_ link: PairingLink, deviceName: String = PairingClient.deviceName,
                deviceModel: String = PairingClient.deviceModel) async throws -> PairingRedeemResult {
        var request = URLRequest(url: link.baseURL.appendingPathComponent("api/auth/pairing/redeem"))
        request.httpMethod = "POST"
        request.timeoutInterval = 20
        request.setValue("application/json", forHTTPHeaderField: "Content-Type")
        request.httpBody = try JSONSerialization.data(withJSONObject: [
            "code": link.code,
            "device_name": deviceName,
            "device_model": deviceModel,
        ])

        let data: Data
        let response: URLResponse
        do {
            (data, response) = try await session.data(for: request)
        } catch let error as URLError where error.code == .cancelled {
            throw CancellationError()
        } catch {
            throw PairingError.network(error.localizedDescription)
        }
        guard let http = response as? HTTPURLResponse else { throw PairingError.server(0, nil) }

        let body = (try? JSONSerialization.jsonObject(with: data)) as? [String: Any] ?? [:]
        let message = (body["error"] as? String).flatMap { $0.isEmpty ? nil : $0 }
        switch http.statusCode {
        case 200...299:
            do {
                return try JSONDecoder().decode(PairingRedeemResult.self, from: data)
            } catch {
                throw PairingError.server(http.statusCode, "The server sent an unexpected answer.")
            }
        case 401:
            throw PairingError.invalidOrExpired
        case 403 where (body["code"] as? String) == "https_required":
            throw PairingError.httpsRequired
        case 404, 405:
            throw PairingError.notSupported
        case 429:
            throw PairingError.rateLimited
        default:
            throw PairingError.server(http.statusCode, message)
        }
    }
}
