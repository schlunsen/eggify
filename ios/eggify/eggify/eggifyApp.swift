//
//  eggifyApp.swift
//  eggify
//
//  Created on 2/11/25.
//

import SwiftUI

// MARK: - Appearance Mode

enum AppearanceMode: String, CaseIterable, Identifiable {
    case system = "system"
    case light = "light"
    case dark = "dark"

    var id: String { rawValue }

    var label: String {
        switch self {
        case .system: return "System"
        case .light: return "Light"
        case .dark: return "Dark"
        }
    }

    var icon: String {
        switch self {
        case .system: return "circle.lefthalf.filled"
        case .light: return "sun.max.fill"
        case .dark: return "moon.fill"
        }
    }

    var colorScheme: ColorScheme? {
        switch self {
        case .system: return nil
        case .light: return .light
        case .dark: return .dark
        }
    }
}

@main
struct eggifyApp: App {
    @UIApplicationDelegateAdaptor(AppDelegate.self) var appDelegate
    @StateObject private var authViewModel = AuthenticationViewModel()
    @AppStorage("appearanceMode") private var appearanceMode: String = AppearanceMode.system.rawValue
    private let avatarManager = AvatarManager.shared

    init() {
        EggifyFonts.registerAll()
        #if DEBUG
        // UI tests start from a clean slate: no saved hosts or tokens.
        if ProcessInfo.processInfo.arguments.contains("-EggifyResetState") {
            for key in ["savedHosts", "currentHostId", "defaultHostId"] {
                UserDefaults.standard.removeObject(forKey: key)
            }
            try? SecureStorage.shared.deleteAll()
        }
        #endif
    }

    /// The signed-in app or the login screen, under the machine switching card.
    private var signedInOrLogin: some View {
        MachineSwitchHost(machineSwitch: authViewModel.machineSwitch) { signedInOrLoginContent }
    }

    @ViewBuilder
    private var signedInOrLoginContent: some View {
        if authViewModel.isAuthenticated {
            MainTabView()
                // A new server gets a fresh signed-in UI: its own
                // connection, lists and navigation.
                .id(authViewModel.currentHost?.id)
                .tint(EggifyColors.accent)
                .environmentObject(authViewModel)
                .environmentObject(avatarManager)
                .task {
                    // Load avatars when authenticated
                    await avatarManager.loadAvatars()
                }
        } else {
            LoginView()
                .tint(EggifyColors.accent)
                .environmentObject(authViewModel)
        }
    }

    private var selectedColorScheme: ColorScheme? {
        AppearanceMode(rawValue: appearanceMode)?.colorScheme
    }

    var body: some Scene {
        WindowGroup {
            ZStack {
                #if DEBUG
                // `-EggifyZenStage <session id>`: the Zen stage alone, without a server,
                // for screenshots of the backdrops and gags (with -EggifyDemoCircus).
                if let zenID = UserDefaults.standard.string(forKey: "EggifyZenStage") {
                    NavigationStack {
                        ZenView(sessionID: zenID, sessionTitle: "Demo session", subscriber: NoopZenSubscriber())
                    }
                } else if ProcessInfo.processInfo.arguments.contains("-EggifyZenPager") {
                    // The Zen pager over three demo sessions, without a server
                    // (`-EggifyZenPagerPeek 0.4` draws it part-way to the next page,
                    // `-EggifyZenPagerStart demo-pager-1` opens on the one that needs you).
                    NavigationStack {
                        ZenView(sessionID: UserDefaults.standard.string(forKey: "EggifyZenPagerStart") ?? "demo-pager-2",
                                sessionTitle: "Demo session",
                                subscriber: NoopZenSubscriber())
                    }
                    .onAppear { SessionsWebSocketViewModel.shared.injectZenPagerDemo() }
                } else {
                    signedInOrLogin
                }
                #else
                signedInOrLogin
                #endif

                // QR-code sign-in status, above whichever screen is showing.
                PairingStatusOverlay(onScanAgain: { authViewModel.showScanner = true })
                    .environmentObject(authViewModel)
                    .tint(EggifyColors.accent)
            }
            .preferredColorScheme(selectedColorScheme)
            // eggify://pair?u=…&c=… from the system Camera app (cold start too).
            .onOpenURL { url in authViewModel.handleIncomingURL(url) }
            #if DEBUG
            // `-EggifyOpenURL <eggify://…>` delivers a link at launch, like a cold
            // start from the Camera app (the Simulator asks "Open in …?" for
            // simctl openurl, which headless runs cannot answer); `-EggifyShowScanner YES` opens the scanner.
            .task {
                if let link = UserDefaults.standard.string(forKey: "EggifyOpenURL"), let url = URL(string: link) {
                    authViewModel.handleIncomingURL(url)
                }
                if UserDefaults.standard.bool(forKey: "EggifyShowScanner") { authViewModel.showScanner = true }
            }
            #endif
            .sheet(isPresented: $authViewModel.showScanner) {
                PairingScannerSheet { code in authViewModel.handleScanned(code) }
                    .tint(EggifyColors.accent)
                    .preferredColorScheme(selectedColorScheme)
            }
        }
    }
}
