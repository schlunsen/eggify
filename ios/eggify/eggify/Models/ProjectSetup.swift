//
//  ProjectSetup.swift
//  eggify
//
//  New project (Projects → +): clone a repository, create an empty folder or
//  add an existing one, as the web's NewProjectDialog does. The server
//  (`/api/projects/setup`) decides what is allowed; these models carry its
//  answers and derive friendly defaults (folder name from the repository).
//

import Foundation

enum ProjectSetupMode: String, CaseIterable, Identifiable {
    case clone, empty, existing

    var id: String { rawValue }

    var title: String {
        switch self {
        case .clone: return "Clone"
        case .empty: return "Empty folder"
        case .existing: return "Existing folder"
        }
    }
}

/// `GET /api/projects/setup/defaults`: where new folders go (the
/// `default_clone_path` setting, ~/projects unless changed) and whether git is there.
struct ProjectSetupDefaults: Equatable {
    var home: String
    var projectsDir: String
    var projectsDirDisplay: String
    var gitAvailable: Bool

    static func parse(_ d: [String: Any]) -> ProjectSetupDefaults {
        let dir = d.string("projects_dir") ?? ""
        return ProjectSetupDefaults(
            home: d.string("home") ?? "",
            projectsDir: dir,
            projectsDirDisplay: d.nonEmptyString("projects_dir_display") ?? (dir.isEmpty ? "~/projects" : dir),
            gitAvailable: d.bool("git_available") ?? true
        )
    }
}

/// `GET /api/projects/setup/inspect?path=`: what is at a path.
struct ProjectSetupInspect: Equatable {
    var path: String
    var displayPath: String
    var suggestedName: String
    var exists: Bool
    var isDir: Bool
    var writable: Bool?
    var empty: Bool?
    var canCreate: Bool?
    var createError: String?
    var git: ProjectGit?
    var existingProjectName: String?

    static func parse(_ d: [String: Any]) -> ProjectSetupInspect {
        var git: ProjectGit?
        if let g = d.dict("git") {
            git = ProjectGit(is_repo: g.bool("is_repo") ?? false, branch: g.nonEmptyString("branch"),
                             remote: g.nonEmptyString("remote"), dirty: g.bool("dirty"))
        }
        let path = d.string("path") ?? ""
        return ProjectSetupInspect(
            path: path,
            displayPath: d.nonEmptyString("display_path") ?? path,
            suggestedName: d.string("suggested_name") ?? "",
            exists: d.bool("exists") ?? false,
            isDir: d.bool("is_dir") ?? false,
            writable: d.bool("writable"),
            empty: d.bool("empty"),
            canCreate: d.bool("can_create"),
            createError: d.nonEmptyString("create_error"),
            git: git,
            existingProjectName: d.dict("existing_project")?.nonEmptyString("name")
        )
    }
}

/// One folder from `GET /api/projects/setup/complete?prefix=`.
struct PathSuggestion: Identifiable, Equatable {
    var path: String
    var display: String
    var name: String
    var id: String { path }

    static func parseList(_ d: [String: Any]) -> [PathSuggestion] {
        d.dicts("suggestions").compactMap { s in
            guard let path = s.nonEmptyString("path") else { return nil }
            return PathSuggestion(path: path, display: s.nonEmptyString("display") ?? path,
                                  name: s.nonEmptyString("name") ?? (path as NSString).lastPathComponent)
        }
    }
}

/// The body of `POST /api/projects/setup`. Empty name/description are left
/// out (the server names the project after its folder).
struct ProjectSetupRequest: Equatable {
    var mode: ProjectSetupMode
    var name = ""
    var description = ""
    /// existing: the folder.
    var path = ""
    /// empty/clone: where the new folder goes, and its name.
    var parent = ""
    var folder = ""
    /// clone: URL or owner/repo; shallow sends depth 1.
    var source = ""
    var shallow = false
    /// empty: run `git init`.
    var gitInit = false

    var body: [String: Any] {
        func trimmed(_ s: String) -> String { s.trimmingCharacters(in: .whitespacesAndNewlines) }
        var b: [String: Any] = ["mode": mode.rawValue]
        if !trimmed(name).isEmpty { b["name"] = trimmed(name) }
        if !trimmed(description).isEmpty { b["description"] = trimmed(description) }
        switch mode {
        case .existing:
            b["path"] = trimmed(path)
        case .empty:
            b["parent"] = trimmed(parent)
            b["folder"] = trimmed(folder)
            b["git_init"] = gitInit
        case .clone:
            b["parent"] = trimmed(parent)
            b["folder"] = trimmed(folder)
            b["source"] = trimmed(source)
            if shallow { b["depth"] = 1 }
        }
        return b
    }
}

/// A clone running on the server (`GET /api/projects/setup/jobs/:id`).
struct CloneJob: Equatable {
    enum Status: String {
        case running, done, error, cancelled
    }

    var id: String
    var status: Status
    var stage: String
    /// Overall progress, 0...100.
    var percent: Int
    /// git's last lines (not the progress counters).
    var output: [String]
    var error: String?
    var path: String
    var project: Project?

    static func parse(_ d: [String: Any]) -> CloneJob? {
        guard let id = d.nonEmptyString("id") else { return nil }
        return CloneJob(
            id: id,
            status: d.string("status").flatMap(Status.init(rawValue:)) ?? .running,
            stage: d.string("stage") ?? "",
            percent: min(100, max(0, d.int("percent") ?? 0)),
            output: d.strings("output"),
            error: d.nonEmptyString("error"),
            path: d.string("path") ?? "",
            project: d.dict("project").flatMap({ Project.parse($0) })
        )
    }

    /// "Receiving objects…", or "Connecting…" before git reports anything.
    var label: String {
        switch status {
        case .error: return "Clone failed"
        case .cancelled: return "Clone cancelled"
        case .done: return "Cloned"
        case .running: return stage.isEmpty || stage == "Starting" ? "Connecting…" : "\(stage)…"
        }
    }
}

/// What `POST /api/projects/setup` answered: a project (201), or a clone job (202).
enum ProjectSetupResult: Equatable {
    case created(Project, warning: String?)
    case cloning(CloneJob)

    static func parse(_ d: [String: Any]) -> ProjectSetupResult? {
        if let job = d.dict("job").flatMap({ CloneJob.parse($0) }) { return .cloning(job) }
        if let project = d.dict("project").flatMap({ Project.parse($0) }) {
            return .created(project, warning: d.nonEmptyString("warning"))
        }
        return nil
    }
}

extension Project {
    /// A project row as the setup endpoints return it (a plain database row).
    static func parse(_ d: [String: Any]) -> Project? {
        guard let id = d.nonEmptyString("id"), let path = d.nonEmptyString("path") else { return nil }
        return Project(
            id: id,
            name: d.nonEmptyString("name") ?? (path as NSString).lastPathComponent,
            path: path,
            description: d.nonEmptyString("description"),
            default_model: d.nonEmptyString("default_model"),
            default_provider: d.nonEmptyString("default_provider"),
            settings: d.nonEmptyString("settings"),
            is_active: d.bool("is_active") ?? true,
            created_at: d.string("created_at") ?? "",
            updated_at: d.string("updated_at") ?? ""
        )
    }
}

// MARK: - Naming

/// Friendly defaults, ported from the web's `utils/projects/projectSetup.ts`.
/// The server's rules are the real check; these only pre-fill and warn early.
nonisolated enum ProjectSetupNaming {
    /// The repository's name from what was typed: `owner/repo`,
    /// `git@host:owner/repo.git` or a URL. Nil when it doesn't look like one.
    static func repoName(from input: String) -> String? {
        var s = input.trimmingCharacters(in: .whitespacesAndNewlines)
        while s.hasSuffix("/") { s.removeLast() }
        guard !s.isEmpty else { return nil }

        if let m = s.wholeMatch(of: #/([A-Za-z0-9][A-Za-z0-9._-]*)/([A-Za-z0-9._-]+?)(?:\.git)?/#),
           !m.1.contains(".") {
            return String(m.2)
        }
        if let m = s.wholeMatch(of: #/[A-Za-z0-9._-]+@[A-Za-z0-9.-]+:([A-Za-z0-9._/-]+?)(?:\.git)?/#) {
            return m.1.split(separator: "/").last.map(String.init)
        }
        guard let url = URL(string: s.contains("://") ? s : "https://\(s)"), url.host != nil else { return nil }
        let parts = url.path.split(separator: "/")
        guard parts.count >= 2, let last = parts.last else { return nil }
        let name = last.hasSuffix(".git") ? String(last.dropLast(4)) : String(last)
        return name.isEmpty ? nil : name
    }

    /// Why a repository entry can't be cloned, before asking the server.
    static func sourceError(_ input: String) -> String? {
        let s = input.trimmingCharacters(in: .whitespacesAndNewlines)
        if s.isEmpty { return "Enter a repository URL or owner/repo." }
        if repoName(from: s) == nil { return "That doesn't look like a repository URL." }
        if s.range(of: #"^(http|ssh|file|git)://"#, options: [.regularExpression, .caseInsensitive]) != nil {
            return "Only HTTPS addresses can be cloned (https://… or owner/repo)."
        }
        return nil
    }

    /// "My new App!" → "my-new-app": a folder name the server accepts.
    static func folderName(fromProjectName name: String) -> String {
        let plain = name.folding(options: [.diacriticInsensitive, .widthInsensitive], locale: nil).lowercased()
        var out = plain.replacingOccurrences(of: #"[^a-z0-9._-]+"#, with: "-", options: .regularExpression)
        out = out.replacingOccurrences(of: #"\.{2,}"#, with: ".", options: .regularExpression)
        out = out.replacingOccurrences(of: #"^[-.]+|[-.]+$"#, with: "", options: .regularExpression)
        return String(out.prefix(80))
    }

    /// The server's folder-name rule: letters, numbers, spaces, dots, dashes
    /// and underscores; no "..", no trailing space or dot.
    static func folderNameError(_ name: String) -> String? {
        if name.isEmpty { return "Enter a folder name." }
        if name.count > 100 { return "The folder name is too long." }
        if name.contains("..") { return "The folder name can't contain \"..\"." }
        if name.wholeMatch(of: #/[A-Za-z0-9_][A-Za-z0-9 ._-]*/#) == nil || name.hasSuffix(" ") || name.hasSuffix(".") {
            return "Use letters, numbers, spaces, dots, dashes and underscores."
        }
        return nil
    }

    /// "~/projects" + "app" → "~/projects/app".
    static func join(_ parent: String, _ folder: String) -> String {
        var p = parent.trimmingCharacters(in: .whitespacesAndNewlines)
        while p.count > 1, p.hasSuffix("/") { p.removeLast() }
        let f = folder.trimmingCharacters(in: .whitespacesAndNewlines)
        if f.isEmpty { return p }
        return p == "/" ? "/\(f)" : "\(p)/\(f)"
    }
}
