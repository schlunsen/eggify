//
//  EggifyAPIClient+Skills.swift
//  eggify
//
//  A running session's skills: what it gets and why, and the skills added to
//  it. Changing them needs a signed-in user, which the app is.
//

import Foundation

extension EggifyAPIClient {
    /// Every approved skill, with whether (and why) the session gets it.
    func getSessionSkills(sessionID: String) async throws -> SessionSkills {
        SessionSkills.parse(try await json("api/agent/sessions/\(sessionID)/skills"))
    }

    /// Replaces the session's added skills; returns the new list.
    func setSessionSkills(sessionID: String, addedIDs: [String]) async throws -> SessionSkills {
        SessionSkills.parse(try await json("api/agent/sessions/\(sessionID)/skills", method: "PUT",
                                           body: ["skill_ids": addedIDs]))
    }
}
