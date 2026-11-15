//
//  EggifyAPIClient+ProjectSetup.swift
//  eggify
//
//  New project: defaults, path checks and autocomplete, creating the project
//  and following a clone. These routes need a signed-in user, which the app
//  is (session token as Bearer); the server's own error text is shown as is.
//

import Foundation

extension EggifyAPIClient {
    /// Where new folders go by default, and whether git is installed.
    func getProjectSetupDefaults() async throws -> ProjectSetupDefaults {
        ProjectSetupDefaults.parse(try await json("api/projects/setup/defaults"))
    }

    /// What is at `path` (`~/…` or absolute). Throws the server's message for
    /// paths it refuses (relative, "..").
    func inspectProjectPath(_ path: String) async throws -> ProjectSetupInspect {
        ProjectSetupInspect.parse(try await json("api/projects/setup/inspect",
                                                 query: [URLQueryItem(name: "path", value: path)]))
    }

    /// Folders matching a typed path, for the autocomplete.
    func completeProjectPath(_ prefix: String) async throws -> [PathSuggestion] {
        PathSuggestion.parseList(try await json("api/projects/setup/complete",
                                                query: [URLQueryItem(name: "prefix", value: prefix)]))
    }

    /// Creates the project, or starts a clone and returns its job.
    func setupProject(_ request: ProjectSetupRequest) async throws -> ProjectSetupResult {
        let response = try await json("api/projects/setup", method: "POST", body: request.body)
        guard let result = ProjectSetupResult.parse(response) else { throw EggifyAPIError.invalidResponse }
        return result
    }

    /// A clone's progress.
    func getProjectSetupJob(id: String) async throws -> CloneJob {
        guard let job = try await json("api/projects/setup/jobs/\(id)").dict("job").flatMap({ CloneJob.parse($0) }) else {
            throw EggifyAPIError.invalidResponse
        }
        return job
    }

    /// Stops a clone; the job then reports `cancelled`.
    func cancelProjectSetupJob(id: String) async throws {
        _ = try await json("api/projects/setup/jobs/\(id)", method: "DELETE")
    }
}
