//
//  AuthenticationViewModel+PairedMachines.swift
//  eggify
//
//  Add the other Macs the current Mac knows with one tap (ADR 014). The Mac
//  you're signed in to has its desktop app paired with them; it asks the one
//  you pick for a pair link (with its own paired session there), and this
//  phone redeems the link at that Mac directly: its own session, no token
//  passes through the first Mac. See Models/PairedMachines.swift.
//

import SwiftUI

extension AuthenticationViewModel {
    private static let announcedKey = "eggify.knownMacsAnnounced"

    /// Reload `knownMacs` from the current host. Quietly empty on errors,
    /// older servers and the CLI server.
    func refreshKnownMacs() async {
        guard isAuthenticated, let host = currentHost else {
            knownMacs = []
            return
        }
        let list = (try? await apiClient.getPairedMachines()) ?? []
        guard currentHost?.id == host.id else { return } // switched meanwhile
        let unsaved = PairedMachines.unsaved(list, savedURLs: savedHosts.map(\.url) + [host.url])
        if unsaved != knownMacs { knownMacs = unsaved }
        announceNewKnownMacs(unsaved, on: host)
    }

    /// Once per Mac: a toast saying it can be added from the server menu.
    private func announceNewKnownMacs(_ macs: [PairedMachine], on host: Host) {
        let defaults = UserDefaults.standard
        var announced = Set(defaults.stringArray(forKey: Self.announcedKey) ?? [])
        let fresh = macs.filter { !announced.contains("\(host.id)|\($0.id)") }
        guard !fresh.isEmpty else { return }
        fresh.forEach { announced.insert("\(host.id)|\($0.id)") }
        defaults.set(Array(announced.suffix(100)), forKey: Self.announcedKey)
        SessionsWebSocketViewModel.shared.notice = SessionsWebSocketViewModel.Notice(
            message: PairedMachines.summary(macName: host.name, machines: fresh) + ". Add it from the server menu at the top.",
            kind: .info)
    }

    /// Add `machine` to this phone: the current Mac has it mint a pair link,
    /// which is checked (that Mac's address and key pin) and redeemed there.
    /// The current server stays on screen; the overlay offers to switch.
    func addKnownMac(_ machine: PairedMachine) async {
        guard addingMacID == nil, let via = currentHost?.name else { return }
        addingMacID = machine.id
        defer { addingMacID = nil }
        pairingState = .connecting(host: machine.name, insecure: false)
        let deviceName = PairedMachines.deviceName(phone: PairingClient.deviceName, via: via)
        let link: PairingLink
        do {
            let pairURL = try await apiClient.createPairCode(on: machine, deviceName: deviceName)
            link = try PairedMachines.checkedLink(pairURL, for: machine)
        } catch is CancellationError {
            pairingState = .idle
            return
        } catch {
            EggifyHaptics.error()
            pairingState = .failed(host: machine.name, message: error.localizedDescription, isExpired: false)
            return
        }
        await redeemAndSave(link, name: machine.name, deviceName: deviceName)
    }
}
