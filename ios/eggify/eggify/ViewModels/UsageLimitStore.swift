//
//  UsageLimitStore.swift
//  eggify
//
//  The account's plan usage, shared by everything that shows it (the chat's
//  usage line, Settings → Usage). Polled every minute while one of them is on
//  screen and the app is in the foreground (`.usesPlanUsage(…)`). Codex's
//  usage is only asked for while one of them wants it (a Codex session or
//  Settings), since asking starts codex on the computer.
//

import SwiftUI
import Combine

@MainActor
final class UsageLimitStore: ObservableObject {
    static let shared = UsageLimitStore()

    @Published private(set) var claude: UsageLimitInfo?
    @Published private(set) var codex: CodexUsageInfo?
    /// Codex's usage was asked for at least once (so "Not reported yet" is the answer, not "Loading…").
    @Published private(set) var codexAsked = false
    /// The first load finished (or failed).
    @Published private(set) var loaded = false

    private static let pollInterval: Duration = .seconds(60)

    private let apiClient = EggifyAPIClient.shared
    private var users = 0
    private var codexUsers = 0
    private var pollTask: Task<Void, Never>?
    private var isLoading = false
    /// Codex's usage was wanted while a load without it ran: load again after it.
    private var reloadQueued = false

    private init() {}

    /// Something that shows the usage appeared (and the app is in the foreground).
    func begin(wantsCodex: Bool) {
        users += 1
        if wantsCodex {
            codexUsers += 1
            // Asked for the first time while the poll already runs: don't wait a minute.
            if !codexAsked, pollTask != nil { Task { await load() } }
        }
        guard pollTask == nil else { return }
        pollTask = Task { [weak self] in
            while !Task.isCancelled {
                await self?.load()
                try? await Task.sleep(for: Self.pollInterval)
            }
        }
    }

    func end(wantsCodex: Bool) {
        users = max(0, users - 1)
        if wantsCodex { codexUsers = max(0, codexUsers - 1) }
        if users == 0 {
            pollTask?.cancel()
            pollTask = nil
        }
    }

    func load() async {
        guard !isLoading else {
            if codexUsers > 0 { reloadQueued = true }
            return
        }
        isLoading = true
        let askCodex = codexUsers > 0
        defer {
            isLoading = false
            loaded = true
            // Answered, failed, or a server too old to know Codex: stop saying "Loading…".
            if askCodex { codexAsked = true }
            if reloadQueued {
                reloadQueued = false
                if !askCodex { Task { await load() } }
            }
        }
        do {
            let report = try await apiClient.getUsageLimit(codex: askCodex)
            if claude != report.claude { claude = report.claude }
            if report.hasCodex, codex != report.codex { codex = report.codex }
        } catch {
            // Offline, or a server without the endpoint: keep what we have.
        }
    }

    /// Forget everything (sign-out, host switch).
    func reset() {
        claude = nil
        codex = nil
        codexAsked = false
        loaded = false
    }
}

private struct UsesPlanUsage: ViewModifier {
    let wantsCodex: Bool
    @Environment(\.scenePhase) private var scenePhase

    func body(content: Content) -> some View {
        content.task(id: TaskKey(active: scenePhase == .active, wantsCodex: wantsCodex)) {
            guard scenePhase == .active else { return }
            let store = UsageLimitStore.shared
            store.begin(wantsCodex: wantsCodex)
            // Hold the registration until the view goes away, the app leaves
            // the foreground or `wantsCodex` changes.
            while !Task.isCancelled {
                try? await Task.sleep(for: .seconds(3600))
            }
            store.end(wantsCodex: wantsCodex)
        }
    }

    private struct TaskKey: Equatable {
        var active: Bool
        var wantsCodex: Bool
    }
}

extension View {
    /// Keeps the account's plan usage fresh while this view is on screen.
    /// `wantsCodex` also asks for Codex's usage.
    func usesPlanUsage(wantsCodex: Bool = false) -> some View {
        modifier(UsesPlanUsage(wantsCodex: wantsCodex))
    }
}
