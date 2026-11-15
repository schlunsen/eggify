//
//  ProjectSetupTests.swift
//  eggifyTests
//
//  New project: what is sent to POST /api/projects/setup for each starting
//  point, how answers and clone jobs are read, and the names filled in from
//  a repository address.
//

import Foundation
import Testing
@testable import eggify

@Suite("New project")
@MainActor
struct ProjectSetupTests {
    // MARK: Request bodies

    @Test func cloneBodyCarriesSourceLocationAndDepth() {
        var request = ProjectSetupRequest(mode: .clone, name: " app ", description: "",
                                          parent: "~/projects/", folder: "app ", source: " owner/app ")
        var body = request.body
        #expect(body["mode"] as? String == "clone")
        #expect(body["source"] as? String == "owner/app")
        #expect(body["parent"] as? String == "~/projects/")
        #expect(body["folder"] as? String == "app")
        #expect(body["name"] as? String == "app")
        #expect(body["description"] == nil)
        #expect(body["depth"] == nil)
        #expect(body["path"] == nil)
        #expect(body["git_init"] == nil)

        request.shallow = true
        body = request.body
        #expect(body["depth"] as? Int == 1)
    }

    @Test func emptyBodyCarriesGitInit() {
        let body = ProjectSetupRequest(mode: .empty, name: "My App", description: "Notes",
                                       parent: "~/projects", folder: "my-app", gitInit: true).body
        #expect(body["mode"] as? String == "empty")
        #expect(body["folder"] as? String == "my-app")
        #expect(body["parent"] as? String == "~/projects")
        #expect(body["git_init"] as? Bool == true)
        #expect(body["description"] as? String == "Notes")
        #expect(body["source"] == nil)
        #expect(ProjectSetupRequest(mode: .empty, folder: "x").body["git_init"] as? Bool == false)
    }

    @Test func existingBodyCarriesOnlyThePath() {
        let body = ProjectSetupRequest(mode: .existing, path: "~/code/app/ ", parent: "~/projects",
                                       folder: "ignored", source: "ignored").body
        #expect(body["mode"] as? String == "existing")
        #expect(body["path"] as? String == "~/code/app/")
        #expect(body["parent"] == nil)
        #expect(body["folder"] == nil)
        #expect(body["source"] == nil)
        #expect(body["name"] == nil)
        #expect(JSONSerialization.isValidJSONObject(body))
    }

    // MARK: Answers

    @Test func createdProjectWithWarning() throws {
        let result = try #require(ProjectSetupResult.parse([
            "project": ["id": "p1", "name": "app", "path": "/Users/me/projects/app", "is_active": true,
                        "color": "#22c55e", "created_at": "2026-10-11T10:00:00Z", "updated_at": "2026-10-11T10:00:00Z"],
            "warning": "Folder created, but git init failed: boom",
        ]))
        guard case .created(let project, let warning) = result else {
            Issue.record("expected a created project")
            return
        }
        #expect(project.id == "p1")
        #expect(project.name == "app")
        #expect(project.path == "/Users/me/projects/app")
        #expect(project.is_active)
        #expect(warning == "Folder created, but git init failed: boom")
    }

    @Test func cloneAnswerIsAJob() throws {
        let result = try #require(ProjectSetupResult.parse([
            "job": ["id": "j1", "status": "running", "stage": "Starting", "percent": 0, "output": [],
                    "path": "/Users/me/projects/app", "started_at": "2026-10-11T10:00:00Z"],
        ]))
        guard case .cloning(let job) = result else {
            Issue.record("expected a clone job")
            return
        }
        #expect(job.id == "j1")
        #expect(job.status == .running)
        #expect(job.label == "Connecting…")
        #expect(job.project == nil)
        #expect(ProjectSetupResult.parse(["status": "ok"]) == nil)
    }

    @Test func jobProgressAndEnd() throws {
        let running = try #require(CloneJob.parse([
            "id": "j1", "status": "running", "stage": "Receiving objects", "percent": 42,
            "output": ["Cloning into '/Users/me/projects/app'..."], "path": "/Users/me/projects/app",
        ]))
        #expect(running.percent == 42)
        #expect(running.label == "Receiving objects…")
        #expect(running.output == ["Cloning into '/Users/me/projects/app'..."])

        let done = try #require(CloneJob.parse([
            "id": "j1", "status": "done", "stage": "Done", "percent": 100, "output": [],
            "project": ["id": "p9", "name": "app", "path": "/Users/me/projects/app", "is_active": true,
                        "created_at": "2026-10-11T10:00:00Z", "updated_at": "2026-10-11T10:00:00Z"],
        ]))
        #expect(done.status == .done)
        #expect(done.project?.id == "p9")

        let failed = try #require(CloneJob.parse([
            "id": "j1", "status": "error", "percent": 130, "error": "Repository not found.",
        ]))
        #expect(failed.status == .error)
        #expect(failed.error == "Repository not found.")
        #expect(failed.percent == 100)
        #expect(failed.label == "Clone failed")

        #expect(CloneJob.parse(["id": "j2", "status": "cancelled"])?.label == "Clone cancelled")
        #expect(CloneJob.parse(["status": "running"]) == nil)
    }

    @Test func defaultsInspectAndSuggestions() {
        let defaults = ProjectSetupDefaults.parse([
            "home": "/Users/me", "projects_dir": "/Users/me/projects",
            "projects_dir_display": "~/projects", "git_available": false,
        ])
        #expect(defaults.projectsDirDisplay == "~/projects")
        #expect(defaults.gitAvailable == false)

        let inspect = ProjectSetupInspect.parse([
            "path": "/Users/me/code/app", "display_path": "~/code/app", "suggested_name": "app",
            "exists": true, "is_dir": true, "writable": true,
            "git": ["is_repo": true, "branch": "main", "dirty": true],
            "existing_project": ["id": "p1", "name": "App"],
        ])
        #expect(inspect.exists && inspect.isDir)
        #expect(inspect.git?.branch == "main")
        #expect(inspect.git?.dirty == true)
        #expect(inspect.existingProjectName == "App")

        let missing = ProjectSetupInspect.parse([
            "path": "/Users/me/.ssh/new", "exists": false, "can_create": false,
            "create_error": "new project folders cannot be created inside hidden folders",
        ])
        #expect(missing.canCreate == false)
        #expect(missing.createError == "new project folders cannot be created inside hidden folders")

        let suggestions = PathSuggestion.parseList(["suggestions": [
            ["path": "/Users/me/code", "display": "~/code", "name": "code"],
            ["display": "no path"],
        ]])
        #expect(suggestions.map(\.display) == ["~/code"])
    }

    // MARK: Names

    @Test func repoNameFromWhatWasTyped() {
        #expect(ProjectSetupNaming.repoName(from: "owner/repo") == "repo")
        #expect(ProjectSetupNaming.repoName(from: "owner/repo.git") == "repo")
        #expect(ProjectSetupNaming.repoName(from: "https://github.com/schlunsen/eggify") == "eggify")
        #expect(ProjectSetupNaming.repoName(from: "https://github.com/schlunsen/eggify.git/") == "eggify")
        #expect(ProjectSetupNaming.repoName(from: " https://gitlab.com/group/sub/my-lib.git ") == "my-lib")
        #expect(ProjectSetupNaming.repoName(from: "git@github.com:owner/my.repo.git") == "my.repo")
        #expect(ProjectSetupNaming.repoName(from: "codeberg.org/owner/tool") == "tool")
        #expect(ProjectSetupNaming.repoName(from: "") == nil)
        #expect(ProjectSetupNaming.repoName(from: "just-a-word") == nil)
        #expect(ProjectSetupNaming.repoName(from: "https://github.com/owner") == nil)
    }

    @Test func onlyHTTPSCanBeCloned() {
        #expect(ProjectSetupNaming.sourceError("owner/repo") == nil)
        #expect(ProjectSetupNaming.sourceError("https://github.com/owner/repo") == nil)
        #expect(ProjectSetupNaming.sourceError("git@github.com:owner/repo.git") == nil)
        #expect(ProjectSetupNaming.sourceError("http://github.com/owner/repo") != nil)
        #expect(ProjectSetupNaming.sourceError("ssh://git@github.com/owner/repo") != nil)
        #expect(ProjectSetupNaming.sourceError("  ") != nil)
    }

    @Test func folderNames() {
        #expect(ProjectSetupNaming.folderName(fromProjectName: "My new App!") == "my-new-app")
        #expect(ProjectSetupNaming.folderName(fromProjectName: "Café ..Déjà") == "cafe-.deja")
        #expect(ProjectSetupNaming.folderName(fromProjectName: "--x--") == "x")
        #expect(ProjectSetupNaming.folderNameError("my-app") == nil)
        #expect(ProjectSetupNaming.folderNameError("My App 2") == nil)
        #expect(ProjectSetupNaming.folderNameError("") != nil)
        #expect(ProjectSetupNaming.folderNameError("a/b") != nil)
        #expect(ProjectSetupNaming.folderNameError("a..b") != nil)
        #expect(ProjectSetupNaming.folderNameError(".hidden") != nil)
        #expect(ProjectSetupNaming.folderNameError("trailing.") != nil)
        #expect(ProjectSetupNaming.join("~/projects/", "app") == "~/projects/app")
        #expect(ProjectSetupNaming.join("/", "app") == "/app")
    }
}
