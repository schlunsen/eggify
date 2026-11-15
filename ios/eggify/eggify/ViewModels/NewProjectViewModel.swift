//
//  NewProjectViewModel.swift
//  eggify
//
//  Form state for New project: the three starting points, the server's
//  checks of the typed paths, and a clone followed to the end.
//

import SwiftUI
import Combine

@MainActor
final class NewProjectViewModel: ObservableObject {
    /// How a status line reads (colour and icon).
    struct Status: Equatable {
        enum Kind { case ok, info, warning, error, pending }
        var kind: Kind
        var text: String
        /// Blocks Create.
        var blocks: Bool { kind == .error }
    }

    // MARK: Form

    @Published var mode: ProjectSetupMode = .clone
    @Published var source = "" { didSet { if source != oldValue { sourceChanged() } } }
    @Published var name = "" { didSet { if name != oldValue { nameChanged() } } }
    @Published var description = ""
    @Published var parent = "~/projects"
    @Published var folder = ""
    @Published var path = ""
    @Published var gitInit = true
    @Published var shallow = false

    // MARK: Server answers

    @Published private(set) var defaults: ProjectSetupDefaults?
    @Published private(set) var suggestions: [PathSuggestion] = []
    /// The checks of the existing folder and of the new folder, each for the path it was asked about.
    @Published private(set) var pathInspect: (path: String, result: Result<ProjectSetupInspect, Error>)?
    @Published private(set) var targetInspect: (path: String, result: Result<ProjectSetupInspect, Error>)?

    // MARK: Progress

    @Published private(set) var isSubmitting = false
    @Published private(set) var job: CloneJob?
    @Published private(set) var created: Project?
    @Published private(set) var warning: String?
    @Published var errorMessage: String?

    /// Values filled in for the user; a field they changed is left alone.
    private var autoFolder = ""
    private var autoName = ""
    private var pollTask: Task<Void, Never>?
    private let apiClient = EggifyAPIClient.shared

    var isCloning: Bool { job?.status == .running }
    var isBusy: Bool { isSubmitting || isCloning }

    // MARK: Loading

    func loadDefaults() async {
        guard defaults == nil else { return }
        do {
            let d = try await apiClient.getProjectSetupDefaults()
            defaults = d
            if parent.isEmpty || parent == "~/projects" { parent = d.projectsDirDisplay }
            gitInit = d.gitAvailable
        } catch is CancellationError {
        } catch {
            errorMessage = error.localizedDescription
        }
    }

    // MARK: Filling in

    private func sourceChanged() {
        guard let repo = ProjectSetupNaming.repoName(from: source) else { return }
        if folder.isEmpty || folder == autoFolder {
            folder = repo
            autoFolder = repo
        }
        if name.isEmpty || name == autoName {
            autoName = repo
            name = repo
        }
    }

    private func nameChanged() {
        guard mode == .empty, name != autoName else { return }
        let suggested = ProjectSetupNaming.folderName(fromProjectName: name)
        if folder.isEmpty || folder == autoFolder {
            folder = suggested
            autoFolder = suggested
        }
    }

    /// A suggestion was picked: keep typing below it.
    func accept(_ suggestion: PathSuggestion) {
        path = suggestion.display + "/"
        suggestions = []
    }

    // MARK: Checking paths (debounced by the view's `.task(id:)`)

    var targetPath: String { ProjectSetupNaming.join(parent, folder) }

    func refreshSuggestions() async {
        let prefix = path
        let trimmed = prefix.trimmingCharacters(in: .whitespaces)
        guard trimmed.hasPrefix("~") || trimmed.hasPrefix("/") else {
            suggestions = []
            return
        }
        guard let found = try? await apiClient.completeProjectPath(prefix), path == prefix else { return }
        let typed = trimmed.hasSuffix("/") ? String(trimmed.dropLast()) : trimmed
        suggestions = found.filter { $0.display != typed && $0.path != typed }
    }

    func inspectPath() async {
        let p = path.trimmingCharacters(in: .whitespaces)
        guard !p.isEmpty else {
            pathInspect = nil
            return
        }
        let result: Result<ProjectSetupInspect, Error>
        do { result = .success(try await apiClient.inspectProjectPath(p)) } catch is CancellationError { return } catch { result = .failure(error) }
        guard path.trimmingCharacters(in: .whitespaces) == p else { return }
        pathInspect = (p, result)
        if case .success(let r) = result, r.exists, r.isDir, r.existingProjectName == nil,
           name.isEmpty || name == autoName {
            autoName = r.suggestedName
            name = r.suggestedName
        }
    }

    func inspectTarget() async {
        let target = targetPath
        guard !parent.trimmingCharacters(in: .whitespaces).isEmpty,
              ProjectSetupNaming.folderNameError(folder.trimmingCharacters(in: .whitespaces)) == nil else {
            targetInspect = nil
            return
        }
        let result: Result<ProjectSetupInspect, Error>
        do { result = .success(try await apiClient.inspectProjectPath(target)) } catch is CancellationError { return } catch { result = .failure(error) }
        guard targetPath == target else { return }
        targetInspect = (target, result)
    }

    // MARK: Status lines

    var sourceStatus: Status? {
        guard !source.trimmingCharacters(in: .whitespaces).isEmpty else { return nil }
        if let error = ProjectSetupNaming.sourceError(source) { return Status(kind: .error, text: error) }
        return nil
    }

    var pathStatus: Status? {
        let p = path.trimmingCharacters(in: .whitespaces)
        guard !p.isEmpty else { return nil }
        guard let inspect = pathInspect, inspect.path == p else { return Status(kind: .pending, text: "Checking…") }
        switch inspect.result {
        case .failure(let error):
            return Status(kind: .error, text: error.localizedDescription)
        case .success(let r):
            if let existing = r.existingProjectName { return Status(kind: .error, text: "Already a project: \(existing).") }
            if !r.exists { return Status(kind: .error, text: "There's no folder at this path.") }
            if !r.isDir { return Status(kind: .error, text: "That's a file, not a folder.") }
            var parts: [String] = []
            if let git = r.git, git.is_repo {
                parts.append("Git repository" + (git.branch.map { " on \($0)" } ?? ""))
                if let remote = git.remote { parts.append(remote) }
                if git.dirty == true { parts.append("uncommitted changes") }
            } else {
                parts.append(r.empty == true ? "Empty folder" : "Folder (not a git repository)")
            }
            if r.writable == false { return Status(kind: .warning, text: parts.joined(separator: " · ") + " · Eggify can't write here") }
            return Status(kind: .ok, text: parts.joined(separator: " · "))
        }
    }

    var targetStatus: Status? {
        let f = folder.trimmingCharacters(in: .whitespaces)
        if parent.trimmingCharacters(in: .whitespaces).isEmpty { return Status(kind: .error, text: "Choose where the folder goes.") }
        if f.isEmpty {
            return Status(kind: .info, text: mode == .clone ? "The folder is named after the repository." : "The folder is named after the project.")
        }
        if let error = ProjectSetupNaming.folderNameError(f) { return Status(kind: .error, text: error) }
        guard let inspect = targetInspect, inspect.path == targetPath else {
            return Status(kind: .info, text: "Will create \(targetPath)")
        }
        switch inspect.result {
        case .failure(let error):
            return Status(kind: .error, text: error.localizedDescription)
        case .success(let r):
            if r.exists {
                if let existing = r.existingProjectName { return Status(kind: .error, text: "\(r.displayPath) is already the project \(existing).") }
                return Status(kind: .error, text: "\(r.displayPath) already exists. Pick another folder name.")
            }
            if r.canCreate == false { return Status(kind: .error, text: r.createError ?? "Eggify can't create a folder here.") }
            return Status(kind: .ok, text: "Will create \(r.displayPath)")
        }
    }

    var canSubmit: Bool {
        guard !isBusy else { return false }
        switch mode {
        case .clone:
            return ProjectSetupNaming.sourceError(source) == nil && !folder.trimmingCharacters(in: .whitespaces).isEmpty
                && targetStatus?.blocks != true
        case .empty:
            return !folder.trimmingCharacters(in: .whitespaces).isEmpty && targetStatus?.blocks != true
        case .existing:
            return !path.trimmingCharacters(in: .whitespaces).isEmpty && pathStatus?.blocks != true
        }
    }

    var submitTitle: String {
        switch mode {
        case .clone: return "Clone"
        case .empty: return "Create"
        case .existing: return "Add"
        }
    }

    // MARK: Create

    var request: ProjectSetupRequest {
        ProjectSetupRequest(mode: mode, name: name, description: description, path: path,
                            parent: parent, folder: folder, source: source, shallow: shallow, gitInit: gitInit)
    }

    func submit() async {
        guard canSubmit else { return }
        isSubmitting = true
        errorMessage = nil
        job = nil
        defer { isSubmitting = false }
        do {
            switch try await apiClient.setupProject(request) {
            case .created(let project, let warning):
                finish(project, warning: warning)
            case .cloning(let job):
                self.job = job
                follow(job)
            }
        } catch is CancellationError {
        } catch {
            EggifyHaptics.error()
            errorMessage = error.localizedDescription
        }
    }

    /// Polls the clone about once a second until it ends.
    private func follow(_ started: CloneJob) {
        pollTask?.cancel()
        pollTask = Task { [weak self] in
            var failures = 0
            while !Task.isCancelled {
                try? await Task.sleep(for: .seconds(1))
                guard !Task.isCancelled, let self else { return }
                do {
                    let current = try await self.apiClient.getProjectSetupJob(id: started.id)
                    failures = 0
                    self.job = current
                    if current.status != .running {
                        self.jobEnded(current)
                        return
                    }
                } catch is CancellationError {
                    return
                } catch EggifyAPIClient.EggifyAPIError.notFound {
                    self.jobLost("The server lost track of the clone (did it restart?). Try again.")
                    return
                } catch {
                    failures += 1
                    if failures >= 15 {
                        self.jobLost("Lost contact with the server while cloning: \(error.localizedDescription)")
                        return
                    }
                }
            }
        }
    }

    private func jobEnded(_ job: CloneJob) {
        switch job.status {
        case .done:
            if let project = job.project {
                finish(project, warning: nil)
            } else {
                errorMessage = "The clone finished, but the server didn't return the project."
            }
        case .error:
            EggifyHaptics.error()
            errorMessage = job.error ?? "The clone failed."
        case .cancelled, .running:
            break
        }
    }

    private func jobLost(_ message: String) {
        if var j = job { j.status = .error; job = j }
        errorMessage = message
    }

    func cancelClone() async {
        guard let id = job?.id, isCloning else { return }
        do {
            try await apiClient.cancelProjectSetupJob(id: id)
        } catch {
            errorMessage = error.localizedDescription
        }
    }

    private func finish(_ project: Project, warning: String?) {
        EggifyHaptics.success()
        self.warning = warning
        created = project
    }

    func stop() {
        pollTask?.cancel()
        pollTask = nil
    }
}
