//
//  DashboardViewModel.swift
//  eggify
//
//  The server's projects (Projects tab, and project names on the home list).
//

import SwiftUI
import Combine

@MainActor
class DashboardViewModel: ObservableObject {
    @Published var isLoading = false
    @Published var errorMessage: String?
    @Published var projects: [Project] = []

    private let apiClient = EggifyAPIClient.shared

    func loadData() async {
        await refresh()
    }

    /// Loads projects in two passes, like the web: a fast one without git
    /// state for the first paint, then the full overview with branch/dirty.
    /// Falls back to the plain project list on servers without the overview.
    func refresh() async {
        isLoading = true
        defer { isLoading = false }
        do {
            if projects.isEmpty {
                let quick = try await apiClient.getProjectsOverview(git: false)
                if quick != projects { projects = quick }
            }
            let full = try await apiClient.getProjectsOverview(git: true)
            if full != projects { projects = full }
            errorMessage = nil
        } catch is CancellationError {
        } catch {
            do {
                let fetched = try await apiClient.getProjects()
                if fetched != projects { projects = fetched }
                errorMessage = nil
            } catch is CancellationError {
            } catch {
                errorMessage = error.localizedDescription
            }
        }
    }
}
