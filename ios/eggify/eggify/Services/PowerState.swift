//
//  PowerState.swift
//  eggify
//
//  Low Power Mode, observable, plus the one place that decides how often
//  background-ish work (list polling, the Zen clock) is allowed to run.
//

import Foundation
import Combine

@MainActor
final class PowerState: ObservableObject {
    static let shared = PowerState()

    @Published private(set) var lowPower = ProcessInfo.processInfo.isLowPowerModeEnabled
    private var token: AnyCancellable?

    private init() {
        token = NotificationCenter.default
            .publisher(for: .NSProcessInfoPowerStateDidChange)
            .receive(on: DispatchQueue.main)
            .sink { [weak self] _ in
                self?.lowPower = ProcessInfo.processInfo.isLowPowerModeEnabled
            }
    }

    /// Seconds between session-list polls. Fast while something is happening,
    /// slower when everything is idle, slower again in Low Power Mode, and
    /// backing off after failures.
    nonisolated static func pollInterval(anyActive: Bool, lowPower: Bool, failures: Int) -> TimeInterval {
        var base: TimeInterval = anyActive ? 6 : 20
        if lowPower { base *= 2 }
        if failures > 0 { base *= min(pow(2, Double(failures)), 8) }
        return min(base, 120)
    }

    /// Zen stage frame rate: halved in Low Power Mode.
    nonisolated static func zenFPS(lowPower: Bool) -> Double {
        lowPower ? 6 : 12
    }
}
