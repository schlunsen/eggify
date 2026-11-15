//
//  EggifyAPIClient+Machines.swift
//  eggify
//
//  The other Macs the current Mac's desktop app is paired with (ADR 014).
//  Both routes need a signed-in user, which the app is (Bearer session);
//  the server's own error text is shown as is.
//

import Foundation

extension EggifyAPIClient {
    /// The current Mac's paired Macs. Empty on servers without the desktop
    /// app (the CLI) and on older servers (404).
    func getPairedMachines() async throws -> [PairedMachine] {
        do {
            return PairedMachine.parseList(try await json("api/machines/paired"))
        } catch EggifyAPIError.notFound {
            return []
        }
    }

    /// Has `machine` mint a pair link for this phone (through the current
    /// Mac, which signs in there with its own paired session). Returns the
    /// eggify://pair link; the phone redeems it at that Mac directly.
    func createPairCode(on machine: PairedMachine, deviceName: String) async throws -> String {
        let id = machine.id.addingPercentEncoding(withAllowedCharacters: .urlPathAllowed) ?? machine.id
        let response = try await json("api/machines/paired/\(id)/pair-code", method: "POST",
                                      body: ["device_name": deviceName])
        guard let url = response.nonEmptyString("pair_url") else { throw EggifyAPIError.invalidResponse }
        return url
    }
}
