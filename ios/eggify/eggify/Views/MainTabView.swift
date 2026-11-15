//
//  MainTabView.swift
//  eggify
//
//  Root of the signed-in app: Agents, Projects, Settings, plus the global
//  toast for things that happen in other sessions.
//

import SwiftUI

struct MainTabView: View {
    @EnvironmentObject var authViewModel: AuthenticationViewModel
    @ObservedObject private var list = SessionListModel.shared
    private var store: SessionsWebSocketViewModel { list.store }
    @Environment(\.scenePhase) private var scenePhase

    @State private var selectedTab: AppTab = .agents
    @State private var agentsPath = NavigationPath()
    @State private var projectsPath = NavigationPath()

    enum AppTab: Hashable {
        case agents, projects, settings
    }

    private var attentionCount: Int {
        list.sessionsNeedingAttention.count
    }

    var body: some View {
        TabView(selection: $selectedTab) {
            Tab("Agents", systemImage: "bubble.left.and.text.bubble.right", value: AppTab.agents) {
                SessionsHomeView(path: $agentsPath)
            }
            .badge(attentionCount)

            Tab("Projects", systemImage: "folder", value: AppTab.projects) {
                ProjectsListView(externalNavigationPath: $projectsPath)
            }

            Tab("Settings", systemImage: "gear", value: AppTab.settings) {
                SettingsView(authViewModel: authViewModel)
            }
        }
        .overlay(alignment: .top) {
            NoticeToast(notice: list.noticeBinding)
        }
        .task {
            await store.startMonitoring()
        }
        .onChange(of: scenePhase) { _, phase in
            if phase == .active {
                Task { await store.loadSessions() }
            }
        }
    }
}

// MARK: - Toast

struct NoticeToast: View {
    @Binding var notice: SessionsWebSocketViewModel.Notice?

    var body: some View {
        ZStack {
            if let notice {
                HStack(alignment: .top, spacing: 10) {
                    Image(systemName: icon(notice.kind))
                        .foregroundStyle(color(notice.kind))
                    Text(notice.message)
                        .font(.subheadline)
                        .fixedSize(horizontal: false, vertical: true)
                    Spacer(minLength: 0)
                }
                .padding(.horizontal, 14)
                .padding(.vertical, 12)
                .background(.regularMaterial, in: RoundedRectangle(cornerRadius: 16, style: .continuous))
                .shadow(color: .black.opacity(0.15), radius: 10, y: 4)
                .padding(.horizontal, 12)
                .padding(.top, 4)
                .transition(.move(edge: .top).combined(with: .opacity))
                .onTapGesture { self.notice = nil }
                .task(id: notice.id) {
                    try? await Task.sleep(for: .seconds(notice.kind == .error || notice.kind == .signInRequired ? 6 : 3.5))
                    if self.notice?.id == notice.id { self.notice = nil }
                }
                .accessibilityAddTraits(.isStaticText)
                .onAppear {
                    UIAccessibility.post(notification: .announcement, argument: notice.message)
                }
            }
        }
        .animation(.snappy, value: notice?.id)
    }

    private func icon(_ kind: SessionsWebSocketViewModel.Notice.Kind) -> String {
        switch kind {
        case .info: return "info.circle.fill"
        case .warning: return "bell.badge.fill"
        case .error: return "exclamationmark.triangle.fill"
        case .signInRequired: return "person.badge.key.fill"
        }
    }

    private func color(_ kind: SessionsWebSocketViewModel.Notice.Kind) -> Color {
        switch kind {
        case .info: return EggifyColors.accent
        case .warning: return EggifyColors.warning
        case .error, .signInRequired: return EggifyColors.error
        }
    }
}

#Preview {
    MainTabView()
        .environmentObject(AuthenticationViewModel())
}
