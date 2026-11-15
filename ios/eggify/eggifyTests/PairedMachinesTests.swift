//
//  PairedMachinesTests.swift
//  eggifyTests
//
//  Adding the other Macs the current Mac knows (ADR 014): parsing
//  GET /api/machines/paired, matching against saved hosts, the device name
//  and the check of the pair link the first Mac hands on. The relay pin/id
//  fixture is the Go key used in CertificatePinsTests.
//

import Foundation
import Testing
@testable import eggify

@Suite("Paired machines (add your other Macs)")
@MainActor
struct PairedMachinesTests {
    static let pin = CertificatePinsTests.pin
    static let base = "https://\(CertificatePinsTests.id).r.example.com"

    static func studio(_ url: String = base) -> PairedMachine {
        PairedMachine(id: CertificatePinsTests.id, name: "Studio", baseURL: url)
    }

    @Test func parsesTheListAndSkipsNonHTTPS() {
        let list = PairedMachine.parseList([
            "machines": [
                ["id": CertificatePinsTests.id, "name": "Studio", "base_url": Self.base + "/"],
                ["id": "x", "name": "Plain", "base_url": "http://example.com"],
                ["id": "", "name": "No id", "base_url": Self.base],
                ["id": "y", "base_url": "https://mini.r.example.com"],
            ] as [Any],
            "available": true,
        ])
        #expect(list.map(\.name) == ["Studio", "mini"])
        #expect(list.first?.baseURL == Self.base)
        #expect(PairedMachine.parseList([:]).isEmpty)
    }

    @Test func unsavedSkipsSavedHostsAndDuplicates() {
        let mini = PairedMachine(id: "m", name: "Mini", baseURL: "https://mini.r.example.com")
        let list = [Self.studio(), mini, Self.studio()]
        #expect(PairedMachines.unsaved(list, savedURLs: []) == [Self.studio(), mini])
        // Saved under a slightly different spelling still counts as saved.
        #expect(PairedMachines.unsaved(list, savedURLs: [Self.base + "/"]) == [mini])
        #expect(PairedMachines.unsaved(list, savedURLs: [Self.base, "mini.r.example.com"]).isEmpty)
    }

    @Test func summaryNamesTheMacs() {
        let mini = PairedMachine(id: "m", name: "Mini", baseURL: "https://mini.r.example.com")
        #expect(PairedMachines.summary(macName: "MacBook", machines: [Self.studio()]) == "MacBook knows 1 more Mac: Studio")
        #expect(PairedMachines.summary(macName: "MacBook", machines: [Self.studio(), mini]) == "MacBook knows 2 more Macs: Studio, Mini")
    }

    @Test func deviceNameSaysWhereItCameFrom() {
        #expect(PairedMachines.deviceName(phone: "Dev’s iPhone", via: "MacBook") == "Dev’s iPhone (added via MacBook)")
        let long = PairedMachines.deviceName(phone: String(repeating: "x", count: 100), via: "MacBook")
        #expect(long.count == 64)
        #expect(long.hasSuffix(" (added via MacBook)"))
    }

    @Test func acceptsOnlyAPinnedLinkForThatMac() throws {
        let good = "eggify://pair?u=\(Self.base.addingPercentEncoding(withAllowedCharacters: .alphanumerics)!)&c=fresh-code&p=\(Self.pin)"
        let link = try PairedMachines.checkedLink(good, for: Self.studio())
        #expect(link.code == "fresh-code")
        #expect(link.pin == Self.pin)

        let noPin = "eggify://pair?u=\(Self.base)&c=fresh-code"
        #expect(throws: PairedMachinesError.badLink) { try PairedMachines.checkedLink(noPin, for: Self.studio()) }

        // A link for another address than the Mac the user tapped.
        let other = PairedMachine(id: "m", name: "Mini", baseURL: "https://mini.r.example.com")
        #expect(throws: PairedMachinesError.badLink) { try PairedMachines.checkedLink(good, for: other) }

        // A pin that doesn't belong to the address is refused while parsing.
        #expect(throws: (any Error).self) {
            try PairedMachines.checkedLink("eggify://pair?u=https://mini.r.example.com&c=x&p=\(Self.pin)", for: other)
        }
        #expect(throws: (any Error).self) { try PairedMachines.checkedLink("https://evil.example", for: Self.studio()) }
    }
}
