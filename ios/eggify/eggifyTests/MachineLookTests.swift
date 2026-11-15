//
//  MachineLookTests.swift
//  eggifyTests
//
//  Machine colours match the desktop app's (fixtures from Go's hash/fnv
//  New32a % 360, cmd/eggify-desktop machineHue), and relay placeholder names
//  are recognised so they can be replaced by the Mac's host name.
//

import Foundation
import Testing
@testable import eggify

@Suite("Machine look")
@MainActor
struct MachineLookTests {
    @Test func hueMatchesTheDesktopApp() {
        #expect(MachineLook.hue("Studio") == 155)
        #expect(MachineLook.hue("MacBook-Pro") == 139)
        #expect(MachineLook.hue("Dev’s Mac mini") == 92)
        #expect(MachineLook.hue("This Mac") == 14)
    }

    @Test func recognisesRelayPlaceholderNames() {
        let id = "ipnpvuzpv7hugwb35eeivjf5ifnplyma"
        let url = "https://\(id).r.example.com"
        #expect(MachineLook.isRelayPlaceholder(AuthenticationViewModel.hostName(for: URL(string: url)!), url: url))
        #expect(!MachineLook.isRelayPlaceholder("Studio", url: url))
        #expect(!MachineLook.isRelayPlaceholder("Example", url: "https://example.ngrok.app"))
    }

    @Test func cleansHostnames() {
        #expect(MachineLook.cleanedHostname("Studio.local") == "Studio")
        #expect(MachineLook.cleanedHostname("  ") == nil)
        #expect(MachineLook.cleanedHostname(nil) == nil)
        #expect(MachineLook.cleanedHostname(String(repeating: "a", count: 60))?.count == 40)
    }
}

@Suite("Machine chip")
@MainActor
struct MachineChipModelTests {
    @Test func showsOnlyWithTwoOrMoreHosts() {
        #expect(!MachineChipModel.shows(hostCount: 0))
        #expect(!MachineChipModel.shows(hostCount: 1))
        #expect(MachineChipModel.shows(hostCount: 2))
        #expect(MachineChipModel.shows(hostCount: 5))
    }

    @Test func nameAndVoiceOverLabel() {
        #expect(MachineChipModel.name("  Studio ") == "Studio")
        #expect(MachineChipModel.name(nil) == "Mac")
        #expect(MachineChipModel.name(" ") == "Mac")
        #expect(MachineChipModel.accessibilityLabel("Studio") == "Mac: Studio, double-tap to switch")
    }
}
