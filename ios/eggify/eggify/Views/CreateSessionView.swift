//
//  CreateSessionView.swift
//  eggify
//
//  Start a new agent session (optionally with its first message).
//

import SwiftUI

struct CreateSessionView: View {
    let projectId: String?
    let workingDirectory: String?
    /// When set, called with the new session instead of posting navigation notifications.
    let onCreated: ((AgentSession) -> Void)?

    @StateObject private var viewModel: CreateSessionViewModel
    @Environment(\.dismiss) private var dismiss
    @FocusState private var promptFocused: Bool
    @State private var showAdvanced = false

    init(projectId: String? = nil, workingDirectory: String? = nil, onCreated: ((AgentSession) -> Void)? = nil) {
        self.projectId = projectId
        self.workingDirectory = workingDirectory
        self.onCreated = onCreated
        _viewModel = StateObject(wrappedValue: CreateSessionViewModel(
            preSelectedProjectId: projectId,
            workingDirectory: workingDirectory
        ))
    }

    var body: some View {
        NavigationStack {
            Form {
                Section {
                    TextField("What should the agent work on?", text: $viewModel.firstPrompt, axis: .vertical)
                        .lineLimit(3...8)
                        .focused($promptFocused)
                } header: {
                    Text("First message")
                } footer: {
                    Text("Optional. Sent as soon as the session starts.")
                }

                Section("Where") {
                    if !viewModel.availableProjects.isEmpty {
                        Picker("Project", selection: $viewModel.selectedProject) {
                            Text("None").tag(Project?.none)
                            ForEach(viewModel.availableProjects) { project in
                                Text(project.name).tag(Optional(project))
                            }
                        }
                    }
                    TextField("Working directory", text: $viewModel.workingDirectory)
                        .font(.callout.monospaced())
                        .textInputAutocapitalization(.never)
                        .autocorrectionDisabled()
                    TextField("Name (optional)", text: $viewModel.agentName)
                }

                Section("Model") {
                    if viewModel.availableProviders.isEmpty {
                        if viewModel.isLoading {
                            ProgressView()
                        } else {
                            Text("No providers configured on the server")
                                .foregroundStyle(.secondary)
                        }
                    } else {
                        Picker("Provider", selection: $viewModel.selectedProvider) {
                            ForEach(viewModel.availableProviders, id: \.id) { provider in
                                Text(provider.name).tag(provider.id)
                            }
                        }
                        Picker("Model", selection: $viewModel.selectedModel) {
                            ForEach(viewModel.availableModels, id: \.self) { model in
                                Text(model).tag(model)
                            }
                        }
                        Picker("Effort", selection: $viewModel.effortLevel) {
                            ForEach(CreateSessionViewModel.effortLevels, id: \.self) { level in
                                Text(level.isEmpty ? "Default" : level.capitalized).tag(level)
                            }
                        }
                    }
                }

                Section {
                    Toggle(isOn: $viewModel.sandboxed) {
                        Label("Sandboxed", systemImage: "shield.lefthalf.filled")
                    }
                    Toggle(isOn: $viewModel.autoApprove) {
                        Label("Auto-approve tools (YOLO)", systemImage: "bolt.shield")
                    }
                } header: {
                    Text("Safety")
                } footer: {
                    Text(viewModel.autoApprove
                         ? "The agent runs tools without asking. Use with care."
                         : "You'll approve tool calls from this phone as they come up.")
                }

                Section {
                    DisclosureGroup("Advanced", isExpanded: $showAdvanced) {
                        ForEach(CreateSessionViewModel.allTools, id: \.self) { tool in
                            Toggle(tool, isOn: Binding(
                                get: { viewModel.selectedTools.contains(tool) },
                                set: { on in
                                    if on { viewModel.selectedTools.insert(tool) } else { viewModel.selectedTools.remove(tool) }
                                }
                            ))
                        }
                        VStack(alignment: .leading, spacing: 6) {
                            Text("System prompt").font(.footnote).foregroundStyle(.secondary)
                            TextField("Optional instructions", text: $viewModel.systemPrompt, axis: .vertical)
                                .lineLimit(2...6)
                        }
                    }
                }

                if let error = viewModel.errorMessage {
                    Section {
                        Label(error, systemImage: "exclamationmark.triangle.fill")
                            .foregroundStyle(EggifyColors.error)
                    }
                }
            }
            .scrollContentBackground(.hidden)
            .background(EggifyColors.background)
            .navigationTitle("New Session")
            .dismissesKeyboardOnScroll()
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Cancel") { dismiss() }
                }
                ToolbarItem(placement: .confirmationAction) {
                    Button {
                        Task { await create() }
                    } label: {
                        if viewModel.isCreating {
                            ProgressView()
                        } else {
                            Text("Start").fontWeight(.semibold)
                        }
                    }
                    .disabled(!viewModel.isFormValid || viewModel.isCreating)
                }
            }
            .task { await viewModel.load() }
            .disabled(viewModel.isCreating)
        }
    }

    private func create() async {
        guard let session = await viewModel.createSession() else { return }
        if let onCreated {
            dismiss()
            try? await Task.sleep(for: .milliseconds(350))
            onCreated(session)
            return
        }
        if let projectId = session.project_id ?? viewModel.selectedProject?.id {
            NotificationCenter.default.post(
                name: NSNotification.Name("SessionCreated"), object: nil,
                userInfo: ["projectId": projectId])
        }
        dismiss()
        // Give the sheet a moment to go away before pushing the chat.
        try? await Task.sleep(for: .milliseconds(350))
        NotificationCenter.default.post(
            name: NSNotification.Name("NavigateToSession"), object: nil,
            userInfo: ["sessionId": session.id, "sessionTitle": session.displayTitle])
    }
}

#Preview {
    CreateSessionView()
}
