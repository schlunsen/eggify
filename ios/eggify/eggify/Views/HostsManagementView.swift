//
//  HostsManagementView.swift
//  eggify
//
//  View for managing saved Eggify server hosts
//

import SwiftUI

struct HostsManagementView: View {
    @Environment(\.dismiss) var dismiss
    @ObservedObject var authViewModel: AuthenticationViewModel

    @State private var showAddHost = false
    @State private var editingHost: Host? = nil
    @State private var newHostName = ""
    @State private var newHostURL = ""
    @State private var errorMessage: String? = nil
    /// A Mac the current one knows, waiting for "Add" to be confirmed.
    @State private var confirmMac: PairedMachine?

    var body: some View {
        NavigationStack {
            VStack(spacing: 0) {
                if authViewModel.savedHosts.isEmpty {
                    emptyState
                } else {
                    hostsList
                }
            }
            .navigationTitle("Manage Hosts")
            .dismissesKeyboardOnScroll()
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .topBarTrailing) {
                    Button(action: { authViewModel.showScanner = true }) {
                        Image(systemName: "qrcode.viewfinder")
                            .font(.system(size: 16))
                    }
                    .accessibilityLabel("Add server by QR")
                }
                ToolbarItem(placement: .topBarTrailing) {
                    Button(action: { showAddHost = true }) {
                        Image(systemName: "plus.circle.fill")
                            .font(.system(size: 16))
                    }
                    .accessibilityLabel("Add server")
                }
            }
            .sheet(isPresented: $showAddHost) {
                addHostSheet
            }
            .task { await authViewModel.refreshKnownMacs() }
            .addKnownMacConfirmation($confirmMac)
            .environmentObject(authViewModel)
        }
    }

    /// The other Macs the current Mac knows (ADR 014), one tap each.
    @ViewBuilder
    private var knownMacsSection: some View {
        if !authViewModel.knownMacs.isEmpty {
            Section {
                ForEach(authViewModel.knownMacs) { mac in
                    HStack {
                        Image(systemName: "desktopcomputer")
                            .foregroundStyle(MachineLook.color(hue: MachineLook.hue(mac.name), saturation: 0.75, lightness: 0.58))
                        VStack(alignment: .leading, spacing: 2) {
                            Text(mac.name).font(.subheadline.bold())
                            Text(mac.baseURL).font(.caption).foregroundStyle(.secondary).lineLimit(1)
                        }
                        Spacer()
                        if authViewModel.addingMacID == mac.id {
                            ProgressView()
                        } else {
                            Button("Add") { confirmMac = mac }
                                .buttonStyle(.borderedProminent)
                                .tint(EggifyColors.accent)
                                .disabled(authViewModel.addingMacID != nil)
                        }
                    }
                }
            } header: {
                Text(PairedMachines.summary(macName: authViewModel.currentHost?.name ?? "This Mac",
                                            machines: authViewModel.knownMacs))
            }
        }
    }

    // MARK: - Subviews

    private var emptyState: some View {
        VStack(spacing: 12) {
            Image(systemName: "server.rack")
                .font(.system(size: 48))
                .foregroundStyle(.gray)

            Text("No Saved Hosts")
                .font(.headline)

            Text("Add a new Eggify server to get started")
                .font(.subheadline)
                .foregroundStyle(.secondary)
                .multilineTextAlignment(.center)

            Button(action: { showAddHost = true }) {
                HStack {
                    Image(systemName: "plus.circle.fill")
                    Text("Add Host")
                }
                .font(.subheadline.bold())
                .frame(maxWidth: .infinity)
                .padding(.vertical, 12)
                .background(EggifyColors.accent)
                .foregroundStyle(.white)
                .cornerRadius(8)
            }
            .padding(.top, 16)
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
        .padding(32)
        .multilineTextAlignment(.center)
    }

    private var hostsList: some View {
        List {
            knownMacsSection
            ForEach(authViewModel.savedHosts, id: \.id) { host in
                hostRow(host)
                    .swipeActions(edge: .trailing, allowsFullSwipe: false) {
                        Button(role: .destructive, action: {
                            deleteHost(host)
                        }) {
                            Label("Delete", systemImage: "trash.fill")
                        }
                    }
            }
        }
        .listStyle(.plain)
    }

    private func hostRow(_ host: Host) -> some View {
        VStack(alignment: .leading, spacing: 8) {
            HStack {
                VStack(alignment: .leading, spacing: 4) {
                    HStack(spacing: 8) {
                        Text(host.name)
                            .font(.subheadline.bold())

                        if host.id == authViewModel.currentHost?.id {
                            Label("Current", systemImage: "checkmark.circle.fill")
                                .font(.caption2)
                                .foregroundStyle(.green)
                        }
                    }

                    Text(host.url)
                        .font(.caption)
                        .foregroundStyle(.secondary)
                        .lineLimit(1)

                    if let lastUsed = host.lastUsedAt {
                        Text("Last used: \(formatDate(lastUsed))")
                            .font(.caption2)
                            .foregroundStyle(.secondary)
                    }
                }

                Spacer()

                VStack(spacing: 4) {
                    Button(action: { editingHost = host; updateFormFields(host) }) {
                        Image(systemName: "pencil.circle.fill")
                            .font(.system(size: 20))
                            .foregroundStyle(EggifyColors.accent)
                    }
                }
            }
            .padding(.vertical, 8)

            if host.id != authViewModel.currentHost?.id {
                Button(action: { switchToHost(host) }) {
                    HStack {
                        Image(systemName: "arrow.right.circle.fill")
                        Text("Switch to this host")
                    }
                    .font(.caption.bold())
                    .frame(maxWidth: .infinity)
                    .padding(.vertical, 8)
                    .background(EggifyColors.accentLight)
                    .foregroundStyle(EggifyColors.accent)
                    .cornerRadius(6)
                }
            }
        }
        .padding(.vertical, 8)
    }

    private var addHostSheet: some View {
        NavigationStack {
            Form {
                Section("Host Information") {
                    TextField("Host Name", text: $newHostName)
                        .textInputAutocapitalization(.words)

                    TextField("Server URL", text: $newHostURL)
                        .textInputAutocapitalization(.never)
                        .autocorrectionDisabled()
                        .keyboardType(.URL)
                        .placeholder(when: newHostURL.isEmpty) {
                            Text("https://localhost:3333")
                                .foregroundStyle(.secondary)
                        }
                }

                if let error = errorMessage {
                    Section {
                        Text(error)
                            .foregroundStyle(.red)
                            .font(.subheadline)
                    }
                }
            }
            .navigationTitle(editingHost != nil ? "Edit Host" : "Add New Host")
            .dismissesKeyboardOnScroll()
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .topBarLeading) {
                    Button("Cancel") {
                        resetForm()
                        showAddHost = false
                    }
                }

                ToolbarItem(placement: .topBarTrailing) {
                    Button("Save") {
                        saveHost()
                    }
                    .disabled(newHostName.trimmingCharacters(in: .whitespaces).isEmpty ||
                              newHostURL.trimmingCharacters(in: .whitespaces).isEmpty)
                }
            }
        }
    }

    // MARK: - Actions

    private func saveHost() {
        errorMessage = nil

        let name = newHostName.trimmingCharacters(in: .whitespaces)
        let url = newHostURL.trimmingCharacters(in: .whitespaces)

        guard !name.isEmpty else {
            errorMessage = "Host name cannot be empty"
            return
        }

        guard !url.isEmpty else {
            errorMessage = "Server URL cannot be empty"
            return
        }

        Task {
            do {
                if let existingHost = editingHost {
                    // Update existing host
                    let hostManager = HostManager.shared
                    try await hostManager.updateHost(id: existingHost.id, name: name, url: url)
                } else {
                    // Add new host
                    _ = try await authViewModel.addNewHost(name: name, url: url)
                }
                resetForm()
                showAddHost = false
            } catch {
                errorMessage = error.localizedDescription
            }
        }
    }

    private func switchToHost(_ host: Host) {
        Task {
            do {
                try await authViewModel.switchHost(hostId: host.id)
                dismiss()
            } catch {
                errorMessage = error.localizedDescription
            }
        }
    }

    private func deleteHost(_ host: Host) {
        Task {
            do {
                try await authViewModel.deleteHost(hostId: host.id)
            } catch {
                errorMessage = error.localizedDescription
            }
        }
    }

    private func resetForm() {
        newHostName = ""
        newHostURL = ""
        errorMessage = nil
        editingHost = nil
    }

    private func updateFormFields(_ host: Host) {
        newHostName = host.name
        newHostURL = host.url
        showAddHost = true
    }

    private func formatDate(_ date: Date) -> String {
        EggifyDate.relative(date)
    }
}

// MARK: - Placeholder Helper

extension View {
    func placeholder<Content: View>(when shouldShow: Bool, alignment: Alignment = .leading, @ViewBuilder placeholder: () -> Content) -> some View {
        ZStack(alignment: alignment) {
            placeholder().opacity(shouldShow ? 1 : 0)
            self
        }
    }
}

#Preview {
    HostsManagementView(authViewModel: AuthenticationViewModel())
}
