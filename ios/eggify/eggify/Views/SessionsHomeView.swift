//
//  SessionsHomeView.swift
//  eggify
//
//  The app's home: every agent session, live, grouped as the web sidebar:
//  Needs you (an approval or a question waits), Working, Today and Earlier
//  (both collapsible). Rules in `SessionGroups.swift`.
//

import SwiftUI

struct ChatRoute: Hashable {
    let sessionId: String
    let title: String
}

struct SessionsHomeView: View {
    @EnvironmentObject private var authViewModel: AuthenticationViewModel
    /// The list's data only; actions go to the store.
    @ObservedObject private var list = SessionListModel.shared
    private var store: SessionsWebSocketViewModel { list.store }
    @StateObject private var projectsModel = DashboardViewModel()
    @Environment(\.scenePhase) private var scenePhase

    @Binding var path: NavigationPath
    @State private var searchText = ""
    @State private var showCreate = false
    /// Collapsible groups showing every row, not just the first `groupLimit`.
    @State private var showAll: Set<SessionGroupID> = []
    @State private var pollFailures = 0
    @State private var sessionToDelete: AgentSession?
    @State private var sessionToTag: AgentSession?
    /// Project to show, or "" for all (kept across launches).
    @AppStorage("eggify.homeProjectFilter") private var projectFilter = ""
    /// Collapsed groups (Today open, Earlier closed by default, as the web).
    @AppStorage("eggify.homeTodayCollapsed") private var todayCollapsed = false
    @AppStorage("eggify.homeEarlierCollapsed") private var earlierCollapsed = true

    private let groupLimit = 25

    var body: some View {
        NavigationStack(path: $path) {
            List {
                if !list.connectionState.isUsable, list.connectionState != .connecting {
                    Section {
                        ConnectionBanner(state: list.connectionState) { store.reconnect() }
                            .listRowInsets(EdgeInsets())
                    }
                }

                if sessionProjects.count >= 2 {
                    Section {
                        ProjectFilterBar(projects: sessionProjects, selection: $projectFilter)
                            .listRowInsets(EdgeInsets())
                            .listRowBackground(Color.clear)
                    }
                }

                let now = Date()
                ForEach(homeGroups(now: now)) { group in
                    if !group.rows.isEmpty {
                        groupSection(group, now: now)
                    }
                }
            }
            .listStyle(.insetGrouped)
            .scrollContentBackground(.hidden)
            .background(EggifyColors.background)
            .overlay {
                if list.sessions.isEmpty {
                    if list.isLoading {
                        ProgressView()
                    } else if let error = list.errorMessage {
                        ContentUnavailableView {
                            Label("Can't load sessions", systemImage: "wifi.exclamationmark")
                        } description: {
                            Text(error)
                        } actions: {
                            Button("Try again") { Task { await store.loadSessions() } }
                                .buttonStyle(.borderedProminent)
                        }
                    } else {
                        ContentUnavailableView {
                            Label("No sessions yet", systemImage: "sparkles")
                        } description: {
                            Text("Start an agent and follow it from here.")
                        } actions: {
                            Button("New Session") { showCreate = true }
                                .buttonStyle(.borderedProminent)
                        }
                    }
                } else if filtered.isEmpty, !searchText.trimmingCharacters(in: .whitespaces).isEmpty {
                    ContentUnavailableView.search(text: searchText)
                }
            }
            .searchable(text: $searchText, prompt: "Search sessions")
            .refreshable {
                await store.loadSessions()
                await projectsModel.refresh()
            }
            .navigationTitle("Agents")
            .machineChipInNavigationBar()
            // Live events only arrive for open chats, so keep the list fresh
            // while it is on screen (the task stops when a chat is pushed or
            // the app leaves the foreground).
            .task(id: scenePhase) {
                guard scenePhase == .active else { return }
                // Adaptive: 6 s while any agent works or needs you, 20 s when all
                // idle, doubled in Low Power Mode, backed off after errors.
                while !Task.isCancelled {
                    let anyActive = list.sessions.contains {
                        list.isWorking($0)
                    } || !list.sessionsNeedingAttention.isEmpty
                    let wait = PowerState.pollInterval(anyActive: anyActive,
                                                       lowPower: PowerState.shared.lowPower,
                                                       failures: pollFailures)
                    try? await Task.sleep(for: .seconds(wait))
                    guard !Task.isCancelled else { break }
                    await store.loadSessions(quietly: true)
                    pollFailures = store.errorMessage == nil ? 0 : pollFailures + 1
                }
            }
            .toolbar {
                // With 2+ Macs the chip sits in the centre instead
                // (machineChipInNavigationBar()).
                if !MachineChipModel.shows(hostCount: authViewModel.savedHosts.count) {
                    ToolbarItem(placement: .topBarLeading) {
                        HostMenu()
                    }
                }
                ToolbarItem(placement: .topBarTrailing) {
                    Button {
                        showCreate = true
                    } label: {
                        Image(systemName: "square.and.pencil")
                    }
                    .accessibilityLabel("New session")
                }
            }
            .navigationDestination(for: ChatRoute.self) { route in
                ChatView(sessionID: route.sessionId, sessionTitle: route.title)
            }
            .sheet(item: $sessionToTag) { session in
                SessionTagSheet(sessionId: session.id)
                    .presentationDetents([.medium, .large])
                    .presentationDragIndicator(.visible)
            }
            .sheet(isPresented: $showCreate) {
                CreateSessionView { session in
                    path.append(ChatRoute(sessionId: session.id, title: session.displayTitle))
                }
            }
            .confirmationDialog(
                "Delete \(sessionToDelete?.displayTitle ?? "session")?",
                isPresented: Binding(get: { sessionToDelete != nil }, set: { if !$0 { sessionToDelete = nil } }),
                titleVisibility: .visible
            ) {
                Button("Delete", role: .destructive) {
                    if let session = sessionToDelete { store.deleteSession(session.id) }
                    sessionToDelete = nil
                }
            } message: {
                Text("This deletes the session and its messages on the server.")
            }
        }
        .task {
            await projectsModel.loadData()
        }
    }

    // MARK: Sections

    private var collapsed: [SessionGroupID: Bool] {
        [.today: todayCollapsed, .earlier: earlierCollapsed]
    }

    private func collapsedBinding(_ id: SessionGroupID) -> Binding<Bool> {
        id == .today ? $todayCollapsed : $earlierCollapsed
    }

    @ViewBuilder
    private func groupSection(_ group: SessionGroup, now: Date) -> some View {
        let isCollapsed = SessionGroups.isCollapsed(group.id, collapsed: collapsed)
        let limited = group.collapsible && !showAll.contains(group.id) && group.rows.count > groupLimit
        Section {
            if !isCollapsed {
                ForEach(limited ? Array(group.rows.prefix(groupLimit)) : group.rows) { item in
                    row(item, now: now)
                    // The workers it started, one line each.
                    ForEach(item.workers) { workerRow($0, parent: item.session) }
                }
                if limited {
                    Button("Show all \(group.rows.count) sessions") { showAll.insert(group.id) }
                }
            }
        } header: {
            groupHeader(group, collapsed: isCollapsed)
        }
    }

    @ViewBuilder
    private func groupHeader(_ group: SessionGroup, collapsed isCollapsed: Bool) -> some View {
        switch group.id {
        case .needs:
            Label("\(group.label) · \(group.rows.count)", systemImage: "hand.raised.fill")
                .foregroundStyle(EggifyColors.warning)
        case .working:
            Text("\(group.label) · \(group.rows.count)")
        case .today, .earlier:
            HStack {
                Button {
                    withAnimation(.snappy) { collapsedBinding(group.id).wrappedValue.toggle() }
                    EggifyHaptics.tap()
                } label: {
                    HStack(spacing: 6) {
                        Image(systemName: "chevron.right")
                            .font(.caption.weight(.bold))
                            .rotationEffect(.degrees(isCollapsed ? 0 : 90))
                        Text("\(group.label) · \(group.rows.count)")
                    }
                    .contentShape(Rectangle())
                }
                .buttonStyle(.plain)
                .accessibilityLabel("\(group.label), \(group.rows.count) sessions")
                .accessibilityValue(isCollapsed ? "Collapsed" : "Expanded")
                .accessibilityHint(isCollapsed ? "Shows the sessions" : "Hides the sessions")
                Spacer()
                if group.id == .earlier, totalCost > 0 {
                    Text(String(format: "$%.2f total", totalCost))
                        .font(.caption)
                        .textCase(nil)
                }
            }
        }
    }

    // MARK: Rows

    private func row(_ row: SessionGroupRow, now: Date) -> some View {
        let session = row.session
        let full = row.full
        return NavigationLink(value: ChatRoute(sessionId: session.id, title: session.displayTitle)) {
            SessionRowView(
                session: session,
                status: list.status(of: session.id),
                row: row,
                variant: full ? .full : .compact,
                tags: list.tags(of: session),
                summary: list.digestSummaries[session.id],
                projectName: session.project_id.flatMap { id in projectsModel.projects.first { $0.id == id }?.name },
                now: now
            )
        }
        .listRowBackground(SessionRowBackground(
            working: row.group == .working,
            color: SessionRowView.avatarColor(for: session)))
        .swipeActions(edge: .leading) {
            Button {
                sessionToTag = session
            } label: {
                Label("Tag", systemImage: "tag")
            }
            .tint(EggifyColors.accent)
        }
        .swipeActions(edge: .trailing) {
            Button(role: .destructive) {
                sessionToDelete = session
            } label: {
                Label("Delete", systemImage: "trash")
            }
            if list.status(of: session.id).isActive {
                Button {
                    store.interrupt(session.id)
                } label: {
                    Label("Stop", systemImage: "stop.fill")
                }
                .tint(.orange)
            }
        }
        .contextMenu {
            Button {
                sessionToTag = session
            } label: {
                Label(session.tags.isEmpty ? "Add tag" : "Edit tags", systemImage: "tag")
            }
            Button {
                UIPasteboard.general.string = session.id
            } label: {
                Label("Copy session ID", systemImage: "number")
            }
            if list.status(of: session.id).isActive {
                Button {
                    store.interrupt(session.id)
                } label: {
                    Label("Stop", systemImage: "stop.fill")
                }
            }
            Button(role: .destructive) {
                sessionToDelete = session
            } label: {
                Label("Delete", systemImage: "trash")
            }
        }
    }

    private func workerRow(_ worker: SessionWorkerRow, parent: AgentSession) -> some View {
        let session = worker.session
        return NavigationLink(value: ChatRoute(sessionId: session.id, title: session.displayTitle)) {
            SessionWorkerRowView(worker: worker, parentTitle: parent.displayTitle)
        }
        .listRowBackground(EggifyColors.surface)
        .swipeActions(edge: .trailing) {
            Button(role: .destructive) {
                sessionToDelete = session
            } label: {
                Label("Delete", systemImage: "trash")
            }
            if worker.status.isActive {
                Button {
                    store.interrupt(session.id)
                } label: {
                    Label("Stop", systemImage: "stop.fill")
                }
                .tint(.orange)
            }
        }
    }

    // MARK: Grouping

    /// Projects that have sessions, most sessions first, with their names.
    private var sessionProjects: [(id: String, name: String)] {
        var counts: [String: Int] = [:]
        for session in list.sessions { if let id = session.project_id { counts[id, default: 0] += 1 } }
        return counts.sorted { $0.value != $1.value ? $0.value > $1.value : $0.key < $1.key }
            .compactMap { id, _ in
                projectsModel.projects.first { $0.id == id }.map { (id: id, name: $0.name) }
            }
    }

    /// The search applied to every project: sessions that need you show up
    /// whatever project is selected.
    private var searchedAllProjects: [AgentSession] {
        let query = searchText.trimmingCharacters(in: .whitespaces).lowercased()
        guard !query.isEmpty else { return list.sessions }
        return list.sessions.filter { matches($0, query) }
    }

    private func matches(_ session: AgentSession, _ query: String) -> Bool {
        session.displayTitle.lowercased().contains(query)
            || (session.working_directory ?? "").lowercased().contains(query)
            || (session.model_name ?? "").lowercased().contains(query)
            || (session.git_branch ?? "").lowercased().contains(query)
            || session.id.hasPrefix(query)
            || session.tags.contains { $0.lowercased().contains(query) }
            || (list.topicTags[session.id] ?? []).contains { $0.lowercased().contains(query) }
    }

    private var filtered: [AgentSession] {
        let query = searchText.trimmingCharacters(in: .whitespaces).lowercased()
        // A remembered project that no longer has sessions shows everything.
        let project = sessionProjects.contains { $0.id == projectFilter } ? projectFilter : ""
        let inProject = project.isEmpty ? list.sessions : list.sessions.filter { $0.project_id == project }
        guard !query.isEmpty else { return inProject }
        return inProject.filter { matches($0, query) }
    }

    /// Needs you / Working / Today / Earlier. "Needs you" comes from every
    /// project (the search still applies); the rest follow the project filter.
    private func homeGroups(now: Date) -> [SessionGroup] {
        let needs = list.groups(searchedAllProjects, now: now).first { $0.id == .needs }
            ?? SessionGroup(id: .needs, rows: [])
        let rest = list.groups(filtered, now: now).filter { $0.id != .needs }
        return [needs] + rest
    }

    private var totalCost: Double {
        list.sessions.compactMap(\.cost).reduce(0, +)
    }
}

// MARK: - Host menu

struct HostMenu: View {
    enum Style { case compact, chip }

    @EnvironmentObject private var authViewModel: AuthenticationViewModel
    /// `.compact`: dot + name (one saved host, leading item); `.chip`: the
    /// centred machine chip (2+ hosts, `machineChipInNavigationBar()`).
    var style: Style = .compact
    /// A Mac the current one knows, waiting for "Add" to be confirmed.
    @State private var confirmMac: PairedMachine?

    var body: some View {
        Group {
            if style == .chip {
                // Plain, so the chip keeps its own colours instead of the bar's tint.
                menu.menuStyle(.button).buttonStyle(.plain)
            } else {
                menu
            }
        }
        .accessibilityLabel(style == .chip
            ? MachineChipModel.accessibilityLabel(authViewModel.currentHost?.name)
            : "Server: \(authViewModel.currentHost?.name ?? "none")")
        .task(id: authViewModel.currentHost?.id) { await authViewModel.refreshKnownMacs() }
        .addKnownMacConfirmation($confirmMac)
    }

    private var menu: some View {
        Menu {
            ForEach(authViewModel.savedHosts) { host in
                Button {
                    guard host.id != authViewModel.currentHost?.id else { return }
                    Task {
                        do {
                            try await authViewModel.switchMachine(to: host.id)
                        } catch {
                            SessionsWebSocketViewModel.shared.notice = SessionsWebSocketViewModel.Notice(
                                message: "Couldn't switch to \(host.name): \(error.localizedDescription)", kind: .error)
                        }
                    }
                } label: {
                    if host.id == authViewModel.currentHost?.id {
                        Label(host.name, systemImage: "checkmark")
                    } else {
                        Label(host.name, systemImage: "desktopcomputer")
                    }
                }
            }
            if !authViewModel.knownMacs.isEmpty {
                // Macs the current Mac's desktop app is paired with (ADR 014).
                Section(PairedMachines.summary(macName: authViewModel.currentHost?.name ?? "This Mac",
                                               machines: authViewModel.knownMacs)) {
                    ForEach(authViewModel.knownMacs) { mac in
                        Button {
                            confirmMac = mac
                        } label: {
                            Label("Add \(mac.name)", systemImage: "plus.circle")
                        }
                        .disabled(authViewModel.addingMacID != nil)
                    }
                }
            }
            Divider()
            NavigationLink {
                HostsManagementView(authViewModel: authViewModel)
            } label: {
                Label("Manage Hosts", systemImage: "server.rack")
            }
        } label: {
            switch style {
            case .compact: compactLabel
            case .chip:
                MachineChip(name: MachineChipModel.name(authViewModel.currentHost?.name),
                            switchable: authViewModel.savedHosts.count > 1)
            }
        }
    }

    private var compactLabel: some View {
        let name = authViewModel.currentHost?.name ?? "Host"
        return HStack(spacing: 5) {
            // The machine's colour, as on the switching card and the Mac.
            Ellipse()
                .fill(MachineLook.color(hue: MachineLook.hue(name), saturation: 0.75, lightness: 0.58))
                .frame(width: 8, height: 10)
            Text(name)
                .lineLimit(1)
            if authViewModel.savedHosts.count > 1 {
                Image(systemName: "chevron.up.chevron.down")
                    .font(.caption2.weight(.bold))
                    .opacity(0.6)
            }
        }
        .font(.footnote.weight(.semibold))
    }
}

extension View {
    /// "Add Studio?" before a known Mac is added (HostMenu, Manage Hosts).
    func addKnownMacConfirmation(_ mac: Binding<PairedMachine?>) -> some View {
        modifier(AddKnownMacConfirmation(mac: mac))
    }
}

private struct AddKnownMacConfirmation: ViewModifier {
    @EnvironmentObject private var authViewModel: AuthenticationViewModel
    @Binding var mac: PairedMachine?

    func body(content: Content) -> some View {
        content.confirmationDialog(
            mac.map { "Add \($0.name)?" } ?? "",
            isPresented: Binding(get: { mac != nil }, set: { if !$0 { mac = nil } }),
            titleVisibility: .visible,
            presenting: mac
        ) { target in
            Button("Add \(target.name)") {
                Task { await authViewModel.addKnownMac(target) }
            }
            Button("Cancel", role: .cancel) {}
        } message: { target in
            Text("\(authViewModel.currentHost?.name ?? "This Mac") asks \(target.name) for a pair code, and this iPhone signs in to \(target.name) with its own login. You can sign it out there under Signed-in devices.")
        }
    }
}

// MARK: - Project filter

struct ProjectFilterBar: View {
    let projects: [(id: String, name: String)]
    @Binding var selection: String

    var body: some View {
        ScrollView(.horizontal, showsIndicators: false) {
            HStack(spacing: 8) {
                chip("All", id: "")
                ForEach(projects, id: \.id) { project in
                    chip(project.name, id: project.id)
                }
            }
            .padding(.horizontal, 4)
            .padding(.vertical, 2)
        }
    }

    private func chip(_ title: String, id: String) -> some View {
        let selected = selection == id || (id.isEmpty && !projects.contains { $0.id == selection })
        return Button {
            withAnimation(.snappy) { selection = id }
            EggifyHaptics.tap()
        } label: {
            Text(title)
                .font(.subheadline.weight(selected ? .semibold : .regular))
                .lineLimit(1)
                .padding(.horizontal, 12)
                .padding(.vertical, 6)
                .background(selected ? AnyShapeStyle(EggifyColors.accent) : AnyShapeStyle(EggifyColors.surface), in: Capsule())
                .foregroundStyle(selected ? Color.white : Color.primary)
                .overlay(Capsule().strokeBorder(EggifyColors.border, lineWidth: selected ? 0 : 0.5))
        }
        .buttonStyle(.plain)
        .accessibilityAddTraits(selected ? .isSelected : [])
    }
}
