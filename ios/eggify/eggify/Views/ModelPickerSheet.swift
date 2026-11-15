//
//  ModelPickerSheet.swift
//  eggify
//
//  Switch a live session to another provider/model.
//

import SwiftUI

struct SwitchableProvider: Identifiable, Equatable {
    let id: String
    let name: String
    let icon: String?
    let models: [String]
    let isConfigured: Bool
    let enabled: Bool

    static func parse(_ d: [String: Any]) -> SwitchableProvider? {
        guard let id = d.nonEmptyString("id") else { return nil }
        return SwitchableProvider(
            id: id,
            name: d.nonEmptyString("name") ?? id,
            icon: d.nonEmptyString("icon"),
            models: d.strings("models"),
            isConfigured: d.bool("is_configured") ?? true,
            enabled: d.bool("enabled") ?? true
        )
    }

    static func load() async throws -> [SwitchableProvider] {
        let response = try await EggifyAPIClient.shared.json("api/providers")
        return response.dicts("providers").compactMap(parse).filter { $0.enabled && !$0.models.isEmpty }
    }
}

struct ModelPickerSheet: View {
    let currentProvider: String?
    let currentModel: String?
    let isBusy: Bool
    let onSelect: (_ provider: String, _ model: String) -> Void

    @Environment(\.dismiss) private var dismiss
    @State private var providers: [SwitchableProvider] = []
    @State private var error: String?
    @State private var loading = true

    var body: some View {
        NavigationStack {
            List {
                if isBusy {
                    Section {
                        Label("The agent is working. Stop it or wait for the turn to finish before switching.",
                              systemImage: "hourglass")
                            .font(.footnote)
                            .foregroundStyle(.secondary)
                    }
                }
                if let error {
                    Section {
                        Label(error, systemImage: "exclamationmark.triangle")
                            .foregroundStyle(EggifyColors.error)
                    }
                }
                ForEach(providers) { provider in
                    Section {
                        ForEach(provider.models, id: \.self) { model in
                            Button {
                                onSelect(provider.id, model)
                                dismiss()
                            } label: {
                                HStack {
                                    Text(model)
                                        .font(.body.monospaced())
                                        .foregroundStyle(.primary)
                                    Spacer()
                                    if model == currentModel && (currentProvider == nil || currentProvider == provider.id) {
                                        Image(systemName: "checkmark").foregroundStyle(EggifyColors.accent)
                                    }
                                }
                            }
                            .disabled(isBusy)
                        }
                    } header: {
                        HStack {
                            if let icon = provider.icon { Text(icon) }
                            Text(provider.name)
                            if !provider.isConfigured {
                                Text("not configured").font(.caption2).foregroundStyle(.orange)
                            }
                        }
                    }
                }
            }
            .overlay {
                if loading { ProgressView() }
            }
            .scrollContentBackground(.hidden)
            .background(EggifyColors.background)
            .navigationTitle("Switch model")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Cancel") { dismiss() }
                }
            }
            .task {
                do {
                    providers = try await SwitchableProvider.load()
                } catch {
                    self.error = error.localizedDescription
                }
                loading = false
            }
        }
        .presentationDetents([.medium, .large])
    }
}
