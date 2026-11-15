//
//  EggifyAPIClient+Usage.swift
//  eggify
//
//  The account's plan usage. `codex: true` also asks for Codex's, which
//  starts codex on the computer (it caches the answer for 2 min there), so
//  only ask when something on screen shows it.
//

import Foundation

extension EggifyAPIClient {
    func getUsageLimit(codex: Bool) async throws -> UsageLimitReport {
        let query = codex ? [URLQueryItem(name: "codex", value: "1")] : []
        return UsageLimitReport.parse(try await json("api/agent/usage-limit", query: query))
    }
}
