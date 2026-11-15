//
//  SettingsView.swift
//  eggify
//

import SwiftUI

struct SettingsView: View {
    @ObservedObject var authViewModel: AuthenticationViewModel
    @ObservedObject private var store = SessionsWebSocketViewModel.shared
    @State private var confirmLogout = false
    @Environment(\.dismiss) var dismiss
    @State private var showHostsManagement = false
    @AppStorage("appearanceMode") private var appearanceMode: String = AppearanceMode.system.rawValue

    private var selectedMode: AppearanceMode {
        AppearanceMode(rawValue: appearanceMode) ?? .system
    }

    var body: some View {
        NavigationStack {
            List {
                // Connection status header
                Section {
                    HStack(spacing: 12) {
                        ZStack {
                            RoundedRectangle(cornerRadius: 10)
                                .fill(EggifyColors.accent.opacity(0.15))
                                .frame(width: 40, height: 40)
                            Image(systemName: "server.rack")
                                .font(.system(size: 18))
                                .foregroundStyle(EggifyColors.accent)
                        }

                        VStack(alignment: .leading, spacing: 2) {
                            Text(authViewModel.currentHost?.name ?? "No Host")
                                .font(.subheadline.weight(.semibold))
                            HStack(spacing: 4) {
                                Circle()
                                    .fill(store.connectionState.isUsable ? EggifyColors.success : EggifyColors.warning)
                                    .frame(width: 6, height: 6)
                                Text(store.connectionState.label)
                                    .font(.caption)
                                    .foregroundStyle(EggifyColors.textSecondary)
                            }
                        }

                        Spacer()

                        if let currentHost = authViewModel.currentHost {
                            Text(currentHost.url.replacingOccurrences(of: "https://", with: ""))
                                .font(.caption)
                                .foregroundStyle(EggifyColors.textTertiary)
                                .lineLimit(1)
                        }
                    }
                    .padding(.vertical, 4)
                }

                Section {
                    NavigationLink(destination: HostsManagementView(authViewModel: authViewModel)) {
                        Label("Manage Hosts", systemImage: "server.rack")
                            .foregroundStyle(.primary)
                    }
                    Button {
                        authViewModel.showScanner = true
                    } label: {
                        Label("Add server by QR", systemImage: "qrcode.viewfinder")
                            .foregroundStyle(.primary)
                    }
                    .accessibilityIdentifier("add-server-qr")
                } header: {
                    Text("Server")
                }

                Section {
                    NavigationLink(destination: ProvidersView()) {
                        Label("AI Providers", systemImage: "cpu")
                            .foregroundStyle(.primary)
                    }
                } header: {
                    Text("Providers")
                } footer: {
                    Text("Configure API keys and models for AI providers.")
                        .font(.caption)
                }

                Section {
                    VStack(alignment: .leading, spacing: 12) {
                        Label {
                            Text("Appearance")
                        } icon: {
                            Image(systemName: selectedMode.icon)
                                .foregroundStyle(EggifyColors.accent)
                        }

                        Picker("Appearance", selection: $appearanceMode) {
                            ForEach(AppearanceMode.allCases) { mode in
                                Label(mode.label, systemImage: mode.icon)
                                    .tag(mode.rawValue)
                            }
                        }
                        .pickerStyle(.segmented)
                    }
                    .padding(.vertical, 4)
                } header: {
                    Text("Preferences")
                }

                Section {
                    AgentQuestionsSetting(store: store)
                } header: {
                    Text("Agents")
                } footer: {
                    Text("When an agent asks a question and nobody answers, it can pick the recommended option for you. Approvals and loop proposals always wait for you. Sessions can override this from their menu.")
                        .font(.caption)
                }

                // Plan usage; Settings also asks for Codex's (`usesPlanUsage` below).
                ClaudeUsageSection(header: "Usage · Claude")
                CodexUsageSection(header: "Usage · Codex",
                                  footer: "Claude's numbers come from the Claude CLI's last request. Codex's come from Codex signed in with ChatGPT.")

                Section {
                    LabeledContent {
                        Text(Bundle.main.object(forInfoDictionaryKey: "CFBundleShortVersionString") as? String ?? "—")
                    } label: {
                        Label("App Version", systemImage: "info.circle")
                    }
                    LabeledContent {
                        Text(Bundle.main.object(forInfoDictionaryKey: "CFBundleVersion") as? String ?? "—")
                    } label: {
                        Label("Build", systemImage: "hammer")
                    }
                } header: {
                    Text("About")
                }

                Section {
                    Button(role: .destructive, action: {
                        confirmLogout = true
                    }) {
                        HStack {
                            Spacer()
                            Label("Logout", systemImage: "arrow.left.circle.fill")
                                .font(.subheadline.weight(.medium))
                            Spacer()
                        }
                    }
                }
            }
            .scrollContentBackground(.hidden)
            .background(EggifyColors.background)
            .navigationTitle("Settings")
            .machineChipInNavigationBar()
            .usesPlanUsage(wantsCodex: true)
            .confirmationDialog("Sign out of \(authViewModel.currentHost?.name ?? "this server")?", isPresented: $confirmLogout, titleVisibility: .visible) {
                Button("Sign Out", role: .destructive) {
                    Task { await authViewModel.logout() }
                }
            }
        }
    }
}

/// The global policy for agent questions (`GET/PUT /api/agent/question-policy`).
struct AgentQuestionsSetting: View {
    @ObservedObject var store: SessionsWebSocketViewModel
    @State private var isSaving = false
    @State private var unavailable = false

    var body: some View {
        if let current = store.questionPolicyDefault {
            Picker(selection: Binding(
                get: { current },
                set: { policy in
                    guard policy != current else { return }
                    isSaving = true
                    Task {
                        await store.setQuestionPolicyDefault(policy)
                        isSaving = false
                    }
                }
            )) {
                ForEach(QuestionPolicy.choices, id: \.self) { policy in
                    Text(policy.label).tag(policy)
                }
                if !QuestionPolicy.choices.contains(current) {
                    Text(current.label).tag(current)
                }
            } label: {
                Label("Agent questions", systemImage: "questionmark.bubble")
            }
            .disabled(isSaving)
            .accessibilityIdentifier("agent-questions-policy")
        } else {
            LabeledContent {
                if unavailable {
                    Text("Needs a newer server").foregroundStyle(.secondary)
                } else {
                    ProgressView()
                }
            } label: {
                Label("Agent questions", systemImage: "questionmark.bubble")
            }
            .task { unavailable = !(await store.loadQuestionPolicyDefault()) }
        }
    }
}
