//
//  ProjectsListView.swift
//  eggify
//

import SwiftUI

struct ProjectsListView: View {
    @Binding var externalNavigationPath: NavigationPath
    @StateObject private var viewModel = DashboardViewModel()
    @State private var searchText = ""
    @State private var showNewProject = false
    /// "Start a session here" from New project: opened once that sheet is gone.
    @State private var pendingSessionProject: Project?
    @State private var sessionProject: Project?

    enum Sort: String, CaseIterable, Identifiable {
        case recent = "Recent activity"
        case running = "Running first"
        case name = "Name"
        var id: String { rawValue }
    }
    @AppStorage("eggify.projectsSort") private var sortRaw = Sort.recent.rawValue
    private var sort: Sort { Sort(rawValue: sortRaw) ?? .recent }

    private func activity(_ p: Project) -> String { p.last_activity ?? p.updated_at }

    private var filteredProjects: [Project] {
        let query = searchText.trimmingCharacters(in: .whitespaces).lowercased()
        let matches = query.isEmpty ? viewModel.projects : viewModel.projects.filter {
            $0.name.lowercased().contains(query) ||
            ($0.description?.lowercased().contains(query) ?? false) ||
            $0.path.lowercased().contains(query) ||
            ($0.git?.branch?.lowercased().contains(query) ?? false) ||
            ($0.default_provider?.lowercased().contains(query) ?? false)
        }
        switch sort {
        case .recent:
            return matches.sorted { activity($0) > activity($1) }
        case .running:
            return matches.sorted {
                let l = $0.running_count ?? 0, r = $1.running_count ?? 0
                return l != r ? l > r : activity($0) > activity($1)
            }
        case .name:
            return matches.sorted { $0.name.localizedCaseInsensitiveCompare($1.name) == .orderedAscending }
        }
    }

    var body: some View {
        NavigationStack(path: $externalNavigationPath) {
            VStack(spacing: 0) {
                if viewModel.isLoading && viewModel.projects.isEmpty {
                    VStack {
                        ProgressView()
                            .tint(EggifyColors.accent)
                    }
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
                } else if viewModel.projects.isEmpty, let error = viewModel.errorMessage {
                    ContentUnavailableView {
                        Label("Couldn't load projects", systemImage: "wifi.exclamationmark")
                    } description: {
                        Text(error)
                    } actions: {
                        Button("Try again") { Task { await viewModel.refresh() } }
                            .buttonStyle(.borderedProminent)
                    }
                } else if viewModel.projects.isEmpty {
                    ContentUnavailableView {
                        Label("No projects yet", systemImage: "folder")
                    } description: {
                        Text("Clone a repository, start an empty folder or add one that's already on your computer.")
                    } actions: {
                        Button("New project") { showNewProject = true }
                            .buttonStyle(.borderedProminent)
                    }
                } else if filteredProjects.isEmpty {
                    ContentUnavailableView.search(text: searchText)
                } else {
                    List(filteredProjects, id: \.id) { project in
                        NavigationLink(value: project.id) {
                            projectRow(project)
                        }
                        .listRowBackground(Color(.secondarySystemGroupedBackground))
                    }
                    .listStyle(.insetGrouped)
                    .scrollContentBackground(.hidden)
                    .refreshable {
                        await viewModel.refresh()
                    }
                }
            }
            .navigationDestination(for: String.self) { projectId in
                ProjectDetailView(projectID: projectId)
            }
            .navigationDestination(for: ChatRoute.self) { route in
                ChatView(sessionID: route.sessionId, sessionTitle: route.title)
            }
            .background(EggifyColors.background)
            .searchable(
                text: $searchText,
                placement: .navigationBarDrawer(displayMode: .always),
                prompt: "Search projects..."
            )
            .machineChipInNavigationBar()
            .toolbar {
                ToolbarItem(placement: .topBarTrailing) {
                    Button {
                        showNewProject = true
                    } label: {
                        Image(systemName: "plus")
                            .font(.system(size: 14, weight: .semibold))
                    }
                    .accessibilityLabel("New project")
                }
                ToolbarItem(placement: .topBarTrailing) {
                    Menu {
                        Picker("Sort by", selection: $sortRaw) {
                            ForEach(Sort.allCases) { Text($0.rawValue).tag($0.rawValue) }
                        }
                    } label: {
                        Image(systemName: "arrow.up.arrow.down")
                            .font(.system(size: 14, weight: .semibold))
                    }
                    .accessibilityLabel("Sort projects")
                }
                ToolbarItem(placement: .topBarTrailing) {
                    Button(action: {
                        Task { await viewModel.refresh() }
                    }) {
                        Image(systemName: "arrow.clockwise")
                            .font(.system(size: 14, weight: .semibold))
                    }
                    .accessibilityLabel("Refresh")
                }
            }
            .sheet(isPresented: $showNewProject, onDismiss: {
                sessionProject = pendingSessionProject
                pendingSessionProject = nil
            }) {
                NewProjectSheet(
                    onCreated: { _ in Task { await viewModel.refresh() } },
                    onStartSession: { pendingSessionProject = $0 }
                )
            }
            .sheet(item: $sessionProject) { project in
                CreateSessionView(projectId: project.id, workingDirectory: project.path) { session in
                    // Back from the chat lands on the new project's page.
                    externalNavigationPath.append(project.id)
                    externalNavigationPath.append(ChatRoute(sessionId: session.id, title: session.displayTitle))
                }
            }
        }
        .task {
            await viewModel.loadData()
        }
    }

    private func projectRow(_ project: Project) -> some View {
        let running = project.running_count ?? 0
        let missing = project.missing == true
        let summary = project.description?.trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
        return VStack(alignment: .leading, spacing: 8) {
            HStack(alignment: .firstTextBaseline, spacing: 8) {
                Circle()
                    .fill(missing ? EggifyColors.error : (running > 0 ? EggifyColors.success : Color.secondary.opacity(0.4)))
                    .frame(width: 10, height: 10)
                    .accessibilityHidden(true)
                Text(project.name)
                    .font(.eggifySerifSemiBold(21, relativeTo: .title3))
                    .lineLimit(2)
                    .foregroundStyle(.primary)
                Spacer(minLength: 4)
                Text(formatDateRelative(activity(project)))
                    .font(.footnote)
                    .foregroundStyle(.secondary)
            }

            if !summary.isEmpty {
                Text(summary)
                    .font(.body)
                    .foregroundStyle(.primary.opacity(0.85))
                    .lineLimit(4)
                    .fixedSize(horizontal: false, vertical: true)
            }

            FlowLayout(spacing: 6) {
                if running > 0 {
                    pill("\(running) running", color: EggifyColors.success, filled: true)
                }
                if let count = project.session_count, count > 0 {
                    pill(count == 1 ? "1 session" : "\(count) sessions", color: .secondary)
                }
                if missing {
                    pill("Folder not found", icon: "exclamationmark.triangle.fill", color: EggifyColors.error)
                } else if let git = project.git, git.is_repo, let branch = git.branch {
                    pill(branch, icon: "arrow.triangle.branch", color: .secondary)
                    if git.dirty == true {
                        pill("Uncommitted", color: EggifyColors.warning)
                    }
                } else if let provider = project.default_provider {
                    pill(provider, color: .secondary)
                }
            }

            Text(project.display_path ?? project.path)
                .font(.footnote.monospaced())
                .lineLimit(1)
                .truncationMode(.head)
                .foregroundStyle(.tertiary)
        }
        .padding(.vertical, 8)
        .accessibilityElement(children: .combine)
    }

    private func pill(_ text: String, icon: String? = nil, color: Color, filled: Bool = false) -> some View {
        HStack(spacing: 4) {
            if let icon { Image(systemName: icon).imageScale(.small) }
            Text(text).lineLimit(1)
        }
        .font(.footnote.weight(filled ? .semibold : .medium))
        .padding(.horizontal, 10)
        .padding(.vertical, 4)
        .foregroundStyle(color)
        .background(color.opacity(filled ? 0.16 : 0.10), in: Capsule())
    }

    private func formatDateRelative(_ dateString: String) -> String {
        EggifyDate.relative(dateString, fallback: dateString)
    }
}

