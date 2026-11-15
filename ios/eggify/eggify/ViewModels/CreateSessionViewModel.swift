//
//  CreateSessionViewModel.swift
//  eggify
//
//  Form state for starting a new agent session.
//

import SwiftUI
import Combine

@MainActor
final class CreateSessionViewModel: ObservableObject {
    // MARK: Form

    @Published var firstPrompt = ""
    @Published var agentName = ""
    @Published var workingDirectory = ""
    @Published var selectedProvider = "" {
        didSet { if oldValue != selectedProvider { updateModelsForProvider(selectedProvider) } }
    }
    @Published var selectedModel = ""
    @Published var selectedProject: Project? = nil {
        didSet {
            // Follow the project's folder unless the user typed their own.
            if let path = selectedProject?.path, workingDirectory.isEmpty || workingDirectory == oldValue?.path {
                workingDirectory = path
            }
        }
    }
    @Published var autoApprove = false
    @Published var sandboxed = false {
        didSet { if !applyingSandboxDefault { sandboxTouched = true } }
    }
    /// The user flipped the toggle; the server default must not undo that.
    private var sandboxTouched = false
    private var applyingSandboxDefault = false
    @Published var effortLevel = ""
    @Published var systemPrompt = ""
    @Published var selectedTools: Set<String> = ["Read", "Write", "Edit", "Bash"]

    // MARK: Data

    @Published private(set) var availableProviders: [Provider] = []
    @Published private(set) var availableModels: [String] = []
    @Published private(set) var availableProjects: [Project] = []
    @Published private(set) var sandboxDefault = false
    @Published var selectedAvatarId: Int64? = nil

    @Published private(set) var isLoading = false
    @Published private(set) var isCreating = false
    @Published var errorMessage: String?

    static let effortLevels = ["", "low", "medium", "high", "xhigh", "max"]
    static let allTools = ["Read", "Write", "Edit", "Bash", "Glob", "Grep", "WebFetch", "WebSearch"]

    private let apiClient = EggifyAPIClient.shared
    private let store: SessionsWebSocketViewModel
    private let preSelectedProjectId: String?

    init(preSelectedProjectId: String? = nil, workingDirectory: String? = nil,
         store: SessionsWebSocketViewModel? = nil) {
        self.preSelectedProjectId = preSelectedProjectId
        self.store = store ?? .shared
        if let workingDirectory { self.workingDirectory = workingDirectory }
    }

    var isFormValid: Bool {
        !workingDirectory.trimmingCharacters(in: .whitespaces).isEmpty
            && !selectedProvider.isEmpty
            && !selectedModel.isEmpty
    }

    // MARK: Loading

    func load() async {
        isLoading = true
        defer { isLoading = false }
        async let projects: Void = loadProjects()
        async let providers: Void = loadProviders()
        async let sandbox: Void = loadSandboxDefault()
        async let avatar: Void = selectRandomAvatar()
        _ = await (projects, providers, sandbox, avatar)
    }

    private func loadProjects() async {
        do {
            availableProjects = try await apiClient.getProjects()
            if let id = preSelectedProjectId, let project = availableProjects.first(where: { $0.id == id }) {
                selectedProject = project
            } else if selectedProject == nil, workingDirectory.isEmpty {
                selectedProject = availableProjects.first(where: { $0.is_active }) ?? availableProjects.first
            }
        } catch {
            errorMessage = "Couldn't load projects: \(error.localizedDescription)"
        }
    }

    private func loadProviders() async {
        do {
            let response = try await apiClient.json("api/providers")
            availableProviders = response.dicts("providers").compactMap { dict in
                guard let id = dict.nonEmptyString("id"), dict.bool("enabled") ?? true else { return nil }
                let models = dict.strings("models")
                guard !models.isEmpty else { return nil }
                return Provider(id: id, name: dict.nonEmptyString("name") ?? id, models: models,
                                defaultModel: dict.nonEmptyString("default_model"),
                                isCurrent: dict.bool("is_current") ?? false)
            }
            let preferred = availableProviders.first(where: \.isCurrent) ?? availableProviders.first
            if let preferred, selectedProvider.isEmpty {
                selectedProvider = preferred.id
            }
        } catch {
            errorMessage = "Couldn't load providers: \(error.localizedDescription)"
        }
    }

    private func loadSandboxDefault() async {
        if let response = try? await apiClient.json("api/agent/sandbox") {
            sandboxDefault = response.bool("default_enabled") ?? false
            if !sandboxTouched {
                applyingSandboxDefault = true
                sandboxed = sandboxDefault
                applyingSandboxDefault = false
            }
        }
    }

    func updateModelsForProvider(_ providerId: String) {
        guard let provider = availableProviders.first(where: { $0.id == providerId }) else { return }
        availableModels = provider.models
        if !provider.models.contains(selectedModel) {
            selectedModel = provider.defaultModel.flatMap { provider.models.contains($0) ? $0 : nil }
                ?? provider.models.first(where: { $0.lowercased().contains("sonnet") })
                ?? provider.models.first ?? ""
        }
    }

    private func selectRandomAvatar() async {
        guard let themes = try? await apiClient.getAvatarThemes(), let theme = themes.randomElement(),
              let avatars = try? await apiClient.getThemeAvatars(themeId: theme.id),
              let avatar = avatars.randomElement() else { return }
        selectedAvatarId = avatar.id
    }

    // MARK: Create

    /// Creates the session (and sends the first message, if any).
    func createSession() async -> AgentSession? {
        guard isFormValid else {
            errorMessage = "Choose a folder, provider and model."
            return nil
        }
        isCreating = true
        errorMessage = nil
        defer { isCreating = false }

        let prompt = systemPrompt.trimmingCharacters(in: .whitespacesAndNewlines)
        do {
            let session = try await store.createSession(
                workingDirectory: workingDirectory.trimmingCharacters(in: .whitespaces),
                provider: selectedProvider,
                model: selectedModel,
                permissionMode: autoApprove ? "yolo" : "default",
                tools: Array(selectedTools).sorted(),
                systemPrompt: prompt.isEmpty ? nil : prompt,
                agentName: agentName.trimmingCharacters(in: .whitespaces),
                projectId: selectedProject?.id,
                avatarId: selectedAvatarId,
                yoloMode: autoApprove,
                sandboxed: sandboxed,
                effortLevel: effortLevel.isEmpty ? nil : effortLevel
            )
            let first = firstPrompt.trimmingCharacters(in: .whitespacesAndNewlines)
            if !first.isEmpty {
                store.sendPrompt(first, to: session.id)
            }
            EggifyHaptics.success()
            return session
        } catch {
            EggifyHaptics.error()
            errorMessage = error.localizedDescription
            return nil
        }
    }
}

struct Provider {
    let id: String
    let name: String
    let models: [String]
    var defaultModel: String? = nil
    var isCurrent: Bool = false
}
