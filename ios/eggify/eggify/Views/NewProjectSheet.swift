//
//  NewProjectSheet.swift
//  eggify
//
//  New project (Projects → +): clone a repository, create an empty folder or
//  add a folder that is already on the computer. Ends with "Start a session
//  here", like the web's dialog.
//

import SwiftUI

struct NewProjectSheet: View {
    /// Called once the project exists (the list reloads).
    var onCreated: (Project) -> Void = { _ in }
    /// "Start a session here": the sheet closes first, then this runs.
    var onStartSession: (Project) -> Void = { _ in }

    @StateObject private var model = NewProjectViewModel()
    @Environment(\.dismiss) private var dismiss
    @FocusState private var focused: Field?

    private enum Field { case source, name, parent, folder, path, description }

    var body: some View {
        NavigationStack {
            Group {
                if let project = model.created {
                    doneView(project)
                } else {
                    form
                }
            }
            .background(EggifyColors.background)
            .navigationTitle(model.created == nil ? "New Project" : "Project Ready")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar { toolbar }
        }
        .interactiveDismissDisabled(model.isBusy)
        .task { await model.loadDefaults() }
        .onChange(of: model.created) { _, project in
            if let project { onCreated(project) }
        }
        .onDisappear { model.stop() }
    }

    // MARK: Form

    private var form: some View {
        Form {
            Section {
                Picker("Start from", selection: $model.mode) {
                    ForEach(ProjectSetupMode.allCases) { Text($0.title).tag($0) }
                }
                .pickerStyle(.segmented)
                .listRowBackground(Color.clear)
                .listRowInsets(EdgeInsets())
            } footer: {
                Text(modeFooter)
            }
            .disabled(model.isBusy)

            Group {
                switch model.mode {
                case .clone: cloneSections
                case .empty: emptySections
                case .existing: existingSections
                }
            }
            .disabled(model.isBusy)

            Section {
                if model.mode != .empty {
                    TextField(namePlaceholder, text: $model.name)
                        .focused($focused, equals: .name)
                        .submitLabel(.next)
                }
                TextField("Description (optional)", text: $model.description, axis: .vertical)
                    .lineLimit(1...4)
                    .focused($focused, equals: .description)
            } header: {
                Text(model.mode == .empty ? "Description" : "Project")
            } footer: {
                if model.mode != .empty {
                    Text("The name is optional; without one the project is named after its folder.")
                }
            }
            .disabled(model.isBusy)

            if let job = model.job {
                progressSection(job)
            }

            if let error = model.errorMessage {
                Section {
                    Label(error, systemImage: "exclamationmark.triangle.fill")
                        .foregroundStyle(EggifyColors.error)
                        .textSelection(.enabled)
                }
            }
        }
        .scrollContentBackground(.hidden)
        .dismissesKeyboardOnScroll()
        .task(id: model.mode) {
            if model.mode == .clone { focused = .source }
        }
        .task(id: model.mode == .existing ? "" : model.targetPath) {
            guard model.mode != .existing else { return }
            try? await Task.sleep(for: .milliseconds(300))
            guard !Task.isCancelled else { return }
            await model.inspectTarget()
        }
        .task(id: model.mode == .existing ? model.path : "") {
            guard model.mode == .existing else { return }
            try? await Task.sleep(for: .milliseconds(250))
            guard !Task.isCancelled else { return }
            async let suggestions: Void = model.refreshSuggestions()
            async let inspect: Void = model.inspectPath()
            _ = await (suggestions, inspect)
        }
    }

    private var modeFooter: String {
        switch model.mode {
        case .clone: return "Eggify clones the repository into a new folder on your computer."
        case .empty: return "Eggify creates a new folder for you."
        case .existing: return "A folder already on your computer. Its files stay where they are."
        }
    }

    private var namePlaceholder: String {
        switch model.mode {
        case .clone: return ProjectSetupNaming.repoName(from: model.source) ?? "Name"
        case .existing: return model.path.isEmpty ? "Name" : ((model.path as NSString).lastPathComponent)
        case .empty: return "Name"
        }
    }

    @ViewBuilder
    private var cloneSections: some View {
        Section {
            TextField("https://github.com/owner/repo", text: $model.source)
                .font(.callout.monospaced())
                .keyboardType(.URL)
                .textContentType(.URL)
                .textInputAutocapitalization(.never)
                .autocorrectionDisabled()
                .focused($focused, equals: .source)
                .submitLabel(.next)
                .onSubmit { focused = .folder }
        } header: {
            Text("Repository")
        } footer: {
            if let status = model.sourceStatus {
                statusLine(status)
            } else {
                Text("An HTTPS address, or owner/repo for GitHub.")
            }
        }
        locationSection
        Section {
            Toggle("Shallow clone", isOn: $model.shallow)
        } footer: {
            Text("Only the latest commit. Faster for big repositories.")
        }
    }

    @ViewBuilder
    private var emptySections: some View {
        Section("Name") {
            TextField("My new project", text: $model.name)
                .focused($focused, equals: .name)
                .submitLabel(.next)
                .onSubmit { focused = .folder }
        }
        locationSection
        Section {
            Toggle("Initialise git", isOn: $model.gitInit)
                .disabled(model.defaults?.gitAvailable == false)
        } footer: {
            if model.defaults?.gitAvailable == false {
                Text("Git isn't installed on the computer running Eggify.")
            }
        }
    }

    private var locationSection: some View {
        Section {
            LabeledContent("In") {
                TextField("~/projects", text: $model.parent)
                    .font(.callout.monospaced())
                    .multilineTextAlignment(.trailing)
                    .textInputAutocapitalization(.never)
                    .autocorrectionDisabled()
                    .focused($focused, equals: .parent)
            }
            LabeledContent("Folder") {
                TextField(model.mode == .clone ? "repo" : "folder", text: $model.folder)
                    .font(.callout.monospaced())
                    .multilineTextAlignment(.trailing)
                    .textInputAutocapitalization(.never)
                    .autocorrectionDisabled()
                    .focused($focused, equals: .folder)
            }
        } header: {
            Text("Location")
        } footer: {
            if let status = model.targetStatus { statusLine(status) }
        }
    }

    @ViewBuilder
    private var existingSections: some View {
        Section {
            TextField("\(model.defaults?.projectsDirDisplay ?? "~/projects")/my-app", text: $model.path)
                .font(.callout.monospaced())
                .keyboardType(.URL)
                .textInputAutocapitalization(.never)
                .autocorrectionDisabled()
                .focused($focused, equals: .path)
            ForEach(model.suggestions) { suggestion in
                Button {
                    model.accept(suggestion)
                    focused = .path
                } label: {
                    Label {
                        Text(suggestion.display)
                            .font(.callout.monospaced())
                            .lineLimit(1)
                            .truncationMode(.head)
                    } icon: {
                        Image(systemName: "folder")
                    }
                }
                .tint(.primary)
            }
        } header: {
            Text("Folder")
        } footer: {
            if let status = model.pathStatus {
                statusLine(status)
            } else {
                Text("Type the full path, e.g. ~/code/my-app. Matching folders show up as you type.")
            }
        }
    }

    private func progressSection(_ job: CloneJob) -> some View {
        Section {
            VStack(alignment: .leading, spacing: 8) {
                HStack {
                    Text(job.label).font(.subheadline.weight(.semibold))
                    Spacer()
                    if job.status == .running {
                        Text("\(job.percent)%").font(.subheadline.monospacedDigit()).foregroundStyle(.secondary)
                    }
                }
                ProgressView(value: Double(job.status == .running ? max(job.percent, 3) : job.percent), total: 100)
                    .tint(job.status == .error ? EggifyColors.error : (job.status == .done ? EggifyColors.success : EggifyColors.accent))
                if job.status == .running, !job.output.isEmpty {
                    Text(job.output.suffix(3).joined(separator: "\n"))
                        .font(.caption.monospaced())
                        .foregroundStyle(.secondary)
                        .lineLimit(3)
                }
            }
            .padding(.vertical, 4)
            .accessibilityElement(children: .combine)
            if job.status == .running {
                Button("Cancel clone", role: .destructive) {
                    Task { await model.cancelClone() }
                }
            }
        }
    }

    private func statusLine(_ status: NewProjectViewModel.Status) -> some View {
        let (icon, color): (String, Color) = switch status.kind {
        case .ok: ("checkmark.circle.fill", EggifyColors.success)
        case .info: ("info.circle", .secondary)
        case .pending: ("ellipsis.circle", .secondary)
        case .warning: ("exclamationmark.triangle.fill", EggifyColors.warning)
        case .error: ("exclamationmark.triangle.fill", EggifyColors.error)
        }
        return Label(status.text, systemImage: icon)
            .foregroundStyle(color)
    }

    // MARK: Toolbar

    @ToolbarContentBuilder
    private var toolbar: some ToolbarContent {
        if model.created == nil {
            ToolbarItem(placement: .cancellationAction) {
                Button("Cancel") { dismiss() }
                    .disabled(model.isBusy)
            }
            ToolbarItem(placement: .confirmationAction) {
                Button {
                    focused = nil
                    Task { await model.submit() }
                } label: {
                    if model.isBusy {
                        ProgressView()
                    } else {
                        Text(model.submitTitle).fontWeight(.semibold)
                    }
                }
                .disabled(!model.canSubmit)
            }
        }
    }

    // MARK: Done

    private func doneView(_ project: Project) -> some View {
        VStack(spacing: 14) {
            Spacer(minLength: 20)
            Image(systemName: "checkmark.circle.fill")
                .font(.system(size: 52))
                .foregroundStyle(EggifyColors.success)
                .accessibilityHidden(true)
            Text("Created \(project.name)")
                .font(.eggifySerifSemiBold(24, relativeTo: .title2))
                .multilineTextAlignment(.center)
            Text(shortened(project.path))
                .font(.footnote.monospaced())
                .foregroundStyle(.secondary)
                .multilineTextAlignment(.center)
                .textSelection(.enabled)
            if let warning = model.warning {
                Label(warning, systemImage: "exclamationmark.triangle.fill")
                    .font(.footnote)
                    .foregroundStyle(EggifyColors.warning)
                    .multilineTextAlignment(.center)
            }
            Spacer(minLength: 20)
            Button {
                onStartSession(project)
                dismiss()
            } label: {
                Label("Start a session here", systemImage: "play.fill")
                    .frame(maxWidth: .infinity)
            }
            .buttonStyle(.borderedProminent)
            .controlSize(.large)
            Button("Done") { dismiss() }
                .controlSize(.large)
        }
        .padding(24)
        .frame(maxWidth: .infinity, maxHeight: .infinity)
    }

    /// /Users/me/projects/app → ~/projects/app.
    private func shortened(_ path: String) -> String {
        guard let home = model.defaults?.home, !home.isEmpty else { return path }
        if path == home { return "~" }
        return path.hasPrefix(home + "/") ? "~" + path.dropFirst(home.count) : path
    }
}

#Preview {
    NewProjectSheet()
}
