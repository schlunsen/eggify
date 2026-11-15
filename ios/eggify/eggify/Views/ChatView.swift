//
//  ChatView.swift
//  eggify
//
//  A live agent session: messages, tool activity, approvals and questions,
//  and the composer.
//

import SwiftUI
import PhotosUI

/// A chat, and Zen pushed on top of it. Zen pages through the active
/// sessions; the chat underneath follows the page that settles on screen, so
/// the back swipe out of Zen lands in that session's chat.
struct ChatView: View {
    let sessionID: String
    let sessionTitle: String
    private let store: SessionsWebSocketViewModel

    /// The session the chat shows (changes only while Zen covers it).
    @State private var shownID: String
    @State private var showZen = false

    init(sessionID: String, sessionTitle: String, viewModel: SessionsWebSocketViewModel? = nil) {
        self.sessionID = sessionID
        self.sessionTitle = sessionTitle
        self.store = viewModel ?? .shared
        _shownID = State(initialValue: sessionID)
    }

    private var shownTitle: String {
        shownID == sessionID ? sessionTitle : (store.session(shownID)?.displayTitle ?? sessionTitle)
    }

    var body: some View {
        ChatScreen(sessionID: shownID, sessionTitle: shownTitle, store: store, onOpenZen: { showZen = true })
            .id(shownID)
            .navigationDestination(isPresented: $showZen) {
                ZenView(sessionID: shownID, sessionTitle: shownTitle, viewModel: store) { settled in
                    if settled != shownID { shownID = settled }
                }
            }
    }
}

private struct ChatScreen: View {
    let sessionID: String
    let sessionTitle: String
    let onOpenZen: () -> Void

    @StateObject private var viewModel: ChatViewModel
    @State private var isInputFocused = false
    @State private var isNearBottom = true
    @State private var unseenCount = 0
    @State private var presentedQuestion: UserQuestion?
    @State private var confirmDelete = false
    /// A worker (from the workers sheet) or the parent (from a worker's
    /// header line) to open on top of this chat.
    @State private var linkedChat: ChatRoute?
    @State private var workerToOpen: ChatRoute?
    @Environment(\.dismiss) private var dismiss
    @Environment(\.scenePhase) private var scenePhase

    init(sessionID: String, sessionTitle: String, store: SessionsWebSocketViewModel, onOpenZen: @escaping () -> Void) {
        self.sessionID = sessionID
        self.sessionTitle = sessionTitle
        self.onOpenZen = onOpenZen
        _viewModel = StateObject(wrappedValue: ChatViewModel(
            sessionID: sessionID, sessionTitle: sessionTitle, store: store))
    }

    var body: some View {
        messageList
            .environment(\.mediaSessionID, sessionID)
            // Swipe in from the right edge to open Zen; the system back
            // swipe from the left edge brings you back to the chat.
            .overlay(alignment: .trailing) {
                ZenSwipeEdge { viewModel.navigate(to: .zen) }
            }
            .onChange(of: viewModel.navigateToZen) { _, open in
                guard open else { return }
                viewModel.navigateToZen = false
                onOpenZen()
            }
            .safeAreaInset(edge: .top, spacing: 0) {
                VStack(spacing: 0) {
                    connectionBanner
                    if let line = viewModel.workerLine {
                        WorkerChatLine(
                            text: line,
                            mode: viewModel.session?.worker?.mode ?? .build,
                            onOpenParent: viewModel.parentSession.map { parent in
                                { linkedChat = ChatRoute(sessionId: parent.id, title: parent.displayTitle) }
                            })
                    }
                    if let tags = viewModel.session?.tags, !tags.isEmpty {
                        ChatTagStrip(tags: tags) { viewModel.showTags = true }
                    }
                    // The plan usage of the runtime this session bills to.
                    ChatUsageLine(provider: viewModel.session?.provider,
                                  model: viewModel.session?.model_name)
                }
            }
            .safeAreaInset(edge: .bottom, spacing: 0) { bottomBar }
            .background(EggifyColors.background)
            .navigationTitle(viewModel.session?.displayTitle ?? sessionTitle)
            .dismissesKeyboardOnScroll()
            .navigationBarTitleDisplayMode(.inline)
            .toolbar(.hidden, for: .tabBar)
            .toolbar {
                ToolbarItem(placement: .principal) {
                    ChatTitleView(viewModel: viewModel, fallbackTitle: sessionTitle)
                }
                ToolbarItem(placement: .topBarTrailing) {
                    ChatMenu(viewModel: viewModel, onDelete: { confirmDelete = true })
                }
            }
            .task { await viewModel.onAppear() }
            .confirmationDialog("Delete this session?", isPresented: $confirmDelete, titleVisibility: .visible) {
                Button("Delete", role: .destructive) {
                    if viewModel.deleteSession() { dismiss() }
                }
            } message: {
                Text("This deletes the session and its messages on the server.")
            }
            .onChange(of: viewModel.questions.first?.id) { _, _ in
                if presentedQuestion == nil, let next = viewModel.questions.first {
                    presentedQuestion = next
                }
            }
            .onChange(of: viewModel.allQuestions.map(\.id)) { _, ids in
                // Answered elsewhere, auto-answered or cancelled: close the sheet.
                if let shown = presentedQuestion, !ids.contains(shown.id) {
                    presentedQuestion = nil
                }
            }
            .sheet(item: $presentedQuestion) { question in
                UserQuestionSheet(
                    question: viewModel.liveQuestion(question.id) ?? question,
                    onAnswer: { answers in
                        viewModel.answer(question, answers)
                        presentedQuestion = nil
                    },
                    onLater: {
                        viewModel.later(question)
                        presentedQuestion = nil
                    },
                    onDismiss: {
                        viewModel.dismiss(question)
                        presentedQuestion = nil
                    },
                    onHold: { viewModel.hold(question) }
                )
                .presentationDetents([.medium, .large])
                .presentationDragIndicator(.visible)
            }
            .sheet(item: $viewModel.detailMessage) { message in
                MessageDetailView(message: message)
                    .presentationDetents([.medium, .large])
                    .presentationDragIndicator(.visible)
            }
            .sheet(isPresented: $viewModel.showSubagents) {
                SubagentsSheet(sessionID: sessionID, store: viewModel.store)
            }
            .sheet(isPresented: $viewModel.showMedia) {
                MediaSheet(sessionID: sessionID)
            }
            .sheet(isPresented: $viewModel.showSkills, onDismiss: {
                // Use put "/<name> " in the composer: type the rest there.
                guard viewModel.focusComposerAfterSkills else { return }
                viewModel.focusComposerAfterSkills = false
                isInputFocused = true
            }) {
                SessionSkillsSheet(sessionID: sessionID, onUse: viewModel.useSkill)
            }
            .sheet(isPresented: $viewModel.showLoopDetails) {
                if let loop = viewModel.session?.loop {
                    LoopDetailSheet(loop: loop, onStop: viewModel.stopLoop)
                }
            }
            .sheet(isPresented: $viewModel.showTeam, onDismiss: {
                // Push the worker's chat once the sheet is gone.
                if let route = workerToOpen {
                    workerToOpen = nil
                    linkedChat = route
                }
            }) {
                if let team = viewModel.team {
                    TeamWorkersSheet(progress: team, team: viewModel.session?.team) { worker in
                        workerToOpen = ChatRoute(sessionId: worker.id, title: worker.title)
                    }
                }
            }
            .navigationDestination(item: $linkedChat) { route in
                ChatView(sessionID: route.sessionId, sessionTitle: route.title, viewModel: viewModel.store)
            }
            // Workers are other sessions, and live events only arrive for the
            // open chat: keep the list fresh while the team strip shows.
            .task(id: TeamPoll(on: viewModel.team != nil, active: scenePhase == .active)) {
                guard viewModel.team != nil, scenePhase == .active else { return }
                var failures = 0
                while !Task.isCancelled {
                    let wait = PowerState.pollInterval(anyActive: viewModel.team?.isWorking == true || viewModel.status.isActive,
                                                       lowPower: PowerState.shared.lowPower,
                                                       failures: failures)
                    try? await Task.sleep(for: .seconds(wait))
                    guard !Task.isCancelled else { break }
                    await viewModel.store.loadSessions(quietly: true)
                    failures = viewModel.store.errorMessage == nil ? 0 : failures + 1
                }
            }
            .sheet(isPresented: $viewModel.showTags) {
                SessionTagSheet(sessionId: sessionID)
                    .presentationDetents([.medium, .large])
                    .presentationDragIndicator(.visible)
            }
            .sheet(isPresented: $viewModel.showModelPicker) {
                ModelPickerSheet(
                    currentProvider: viewModel.session?.provider,
                    currentModel: viewModel.session?.model_name,
                    isBusy: viewModel.status.isActive
                ) { provider, model in
                    viewModel.changeModel(provider: provider, model: model)
                }
            }
            .fullScreenCover(item: $viewModel.viewerImage) { block in
                ImageViewerOverlay(dataUrl: block.dataUrl) { viewModel.viewerImage = nil }
            }
            .navigationDestination(isPresented: $viewModel.navigateToJust) {
                if let pid = viewModel.projectId {
                    JustCommandsView(projectID: pid)
                }
            }
            .navigationDestination(isPresented: $viewModel.navigateToGit) {
                GitStatusView(sessionID: sessionID)
            }
            .navigationDestination(isPresented: $viewModel.navigateToStats) {
                SessionStatsView(sessionID: sessionID)
            }
    }

    // MARK: - Messages

    @ViewBuilder
    private var messageList: some View {
        if viewModel.items.isEmpty {
            emptyState
        } else {
            ScrollViewReader { proxy in
                ScrollView {
                    LazyVStack(alignment: .leading, spacing: 14) {
                        if viewModel.hasMore {
                            LoadEarlierButton(isLoading: viewModel.isLoadingMessages) {
                                Task {
                                    if let anchor = await viewModel.loadEarlier() {
                                        proxy.scrollTo(anchor, anchor: .top)
                                    }
                                }
                            }
                            .frame(maxWidth: .infinity)
                        }

                        ForEach(viewModel.items) { item in
                            ChatItemView(
                                item: item,
                                onShowDetails: { viewModel.detailMessage = $0 },
                                onImageTap: { viewModel.viewerImage = $0 }
                            )
                            .equatable()
                            .id(item.id)
                        }

                        if viewModel.status.isActive {
                            WorkingIndicator()
                                .id("working")
                        }

                        Color.clear.frame(height: 1).id("bottom")
                    }
                    .padding(.horizontal, 16)
                    .padding(.vertical, 12)
                    // Readable line length on iPad and in landscape.
                    .frame(maxWidth: 760)
                    .frame(maxWidth: .infinity)
                }
                .defaultScrollAnchor(.bottom)
                .scrollDismissesKeyboard(.interactively)
                // Tapping the conversation puts the keyboard away; the
                // gesture runs alongside buttons and links in the rows.
                .simultaneousGesture(TapGesture().onEnded {
                    if isInputFocused { isInputFocused = false }
                })
                .refreshable { await viewModel.refresh() }
                .onScrollGeometryChange(for: Bool.self) { geometry in
                    geometry.contentOffset.y + geometry.containerSize.height >= geometry.contentSize.height - 120
                } action: { _, nearBottom in
                    isNearBottom = nearBottom
                    if nearBottom { unseenCount = 0 }
                }
                .onChange(of: viewModel.items.last?.id) { oldValue, _ in
                    guard oldValue != nil else { return }
                    if isNearBottom {
                        withAnimation(.snappy) { proxy.scrollTo("bottom", anchor: .bottom) }
                    } else {
                        unseenCount += 1
                    }
                }
                .onChange(of: viewModel.sendCount) { _, _ in
                    // The user just sent a prompt: jump to it and keep
                    // following the reply, even if they had scrolled up or
                    // the keyboard moved the view off the bottom.
                    isNearBottom = true
                    unseenCount = 0
                    withAnimation(.snappy) { proxy.scrollTo("bottom", anchor: .bottom) }
                }
                .onChange(of: viewModel.status.isActive) { _, _ in
                    if isNearBottom {
                        withAnimation(.snappy) { proxy.scrollTo("bottom", anchor: .bottom) }
                    }
                }
                .onChange(of: isInputFocused) { _, focused in
                    if focused {
                        Task {
                            try? await Task.sleep(for: .milliseconds(250))
                            withAnimation(.snappy) { proxy.scrollTo("bottom", anchor: .bottom) }
                        }
                    }
                }
                .overlay(alignment: .bottomTrailing) {
                    if !isNearBottom {
                        JumpToBottomButton(unseen: unseenCount) {
                            withAnimation(.snappy) { proxy.scrollTo("bottom", anchor: .bottom) }
                            unseenCount = 0
                        }
                        .padding(16)
                        .transition(.scale.combined(with: .opacity))
                    }
                }
                .animation(.snappy, value: isNearBottom)
            }
        }
    }

    @ViewBuilder
    private var emptyState: some View {
        if !viewModel.didInitialLoad || viewModel.isLoadingMessages {
            ProgressView("Loading conversation…")
                .frame(maxWidth: .infinity, maxHeight: .infinity)
        } else if let error = viewModel.loadError {
            ContentUnavailableView {
                Label("Couldn't load messages", systemImage: "wifi.exclamationmark")
            } description: {
                Text(error)
            } actions: {
                Button("Try again") { Task { await viewModel.refresh() } }
                    .buttonStyle(.borderedProminent)
            }
        } else {
            ContentUnavailableView {
                Label("No messages yet", systemImage: "sparkles")
            } description: {
                Text("Tell the agent what to work on.")
            }
            .onTapGesture { isInputFocused = true }
        }
    }

    // MARK: - Banners & bottom bar

    @ViewBuilder
    private var connectionBanner: some View {
        if viewModel.didInitialLoad, !viewModel.connection.isUsable {
            ConnectionBanner(state: viewModel.connection) {
                viewModel.store.reconnect()
            }
            .transition(.move(edge: .top).combined(with: .opacity))
        }
    }

    private var bottomBar: some View {
        VStack(spacing: 8) {
            if let request = viewModel.permissions.first {
                PermissionRequestCard(
                    request: request,
                    position: 1,
                    total: viewModel.permissions.count,
                    isConnected: viewModel.connection.isUsable,
                    onAlwaysAllow: { similar in
                        withAnimation(.snappy) { viewModel.alwaysAllow(request, similar: similar) }
                    }
                ) { approved in
                    withAnimation(.snappy) { viewModel.respond(request, approved: approved) }
                }
                .padding(.horizontal, 12)
                .transition(.move(edge: .bottom).combined(with: .opacity))
                .id(request.id)
            }

            if viewModel.waitingQuestionCount > 0, presentedQuestion == nil {
                Button {
                    viewModel.showDeferredQuestions()
                    presentedQuestion = viewModel.questions.first
                        ?? viewModel.store.pendingQuestions[sessionID]?.first
                } label: {
                    Label(viewModel.waitingQuestionCount == 1 ? "The agent asked you a question"
                          : "The agent asked \(viewModel.waitingQuestionCount) questions",
                          systemImage: "questionmark.bubble.fill")
                        .font(.subheadline.weight(.semibold))
                        .frame(maxWidth: .infinity)
                }
                .buttonStyle(.borderedProminent)
                .tint(EggifyColors.accent)
                .padding(.horizontal, 12)
            }

            if let loop = viewModel.session?.loop, viewModel.session?.loop_mode == true {
                LoopStrip(loop: loop, onStop: viewModel.stopLoop) { viewModel.showLoopDetails = true }
                    .padding(.horizontal, 12)
                    .transition(.move(edge: .bottom).combined(with: .opacity))
            }

            if let team = viewModel.team {
                TeamStrip(progress: team) { viewModel.showTeam = true }
                    .padding(.horizontal, 12)
                    .transition(.move(edge: .bottom).combined(with: .opacity))
            }

            if viewModel.subagentsRunning > 0 {
                SubagentChip(running: viewModel.subagentsRunning) { viewModel.showSubagents = true }
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .padding(.horizontal, 14)
                    .transition(.move(edge: .bottom).combined(with: .opacity))
            }

            Composer(
                model: viewModel.composer,
                isWorking: viewModel.status.isActive,
                isFocused: $isInputFocused,
                onSend: viewModel.send,
                onStop: viewModel.interrupt
            )
        }
        .padding(.top, 6)
        .frame(maxWidth: 760)
        .frame(maxWidth: .infinity)
        .animation(.snappy, value: viewModel.permissions.first?.id)
        .animation(.snappy, value: viewModel.subagentsRunning > 0)
        .animation(.snappy, value: viewModel.session?.loop?.phase)
        .animation(.snappy, value: viewModel.team != nil)
        .background(alignment: .bottom) {
            Rectangle().fill(.bar).ignoresSafeArea(edges: .bottom)
                .frame(height: 80)
        }
    }
}

/// What the team poll restarts on.
private struct TeamPoll: Equatable {
    let on: Bool
    let active: Bool
}

// MARK: - Title

struct ChatTitleView: View {
    @ObservedObject var viewModel: ChatViewModel
    let fallbackTitle: String
    /// With 2+ saved Macs the subtitle ends with "● Studio" (which Mac this
    /// chat runs on). HostManager, not the environment, so it works anywhere.
    @ObservedObject private var hosts = HostManager.shared

    var body: some View {
        VStack(spacing: 1) {
            Text(viewModel.session?.displayTitle ?? fallbackTitle)
                .font(.eggifySerifSemiBold(19, relativeTo: .headline))
                .lineLimit(1)
            HStack(spacing: 5) {
                StatusDot(status: viewModel.status)
                Text(subtitle)
                    .font(.caption2)
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
                if let machine {
                    Text("·")
                        .font(.caption2)
                        .foregroundStyle(.secondary)
                        .accessibilityHidden(true)
                    MachineDot(name: machine)
                    Text(machine)
                        .font(.caption2)
                        .foregroundStyle(.secondary)
                        .lineLimit(1)
                        .truncationMode(.tail)
                        .frame(maxWidth: 110, alignment: .leading)
                        .layoutPriority(1)
                        .accessibilityLabel("Mac: \(machine)")
                }
            }
        }
        .accessibilityElement(children: .combine)
    }

    private var machine: String? {
        guard MachineChipModel.shows(hostCount: hosts.hosts.count) else { return nil }
        return MachineChipModel.name(hosts.currentHost?.name)
    }

    private var subtitle: String {
        var parts = [viewModel.status.label]
        if let model = viewModel.session?.model_name { parts.append(ModelNames.short(model)) }
        if viewModel.session?.yolo == true { parts.append("YOLO") }
        return parts.joined(separator: " · ")
    }
}

/// The session's own tags under the header, in their colours; tapping the
/// strip opens the tag sheet. Hidden while the session has none (chat menu →
/// Add tag).
struct ChatTagStrip: View {
    let tags: [String]
    let onEdit: () -> Void

    var body: some View {
        Button(action: onEdit) {
            HStack(spacing: 8) {
                ScrollView(.horizontal, showsIndicators: false) {
                    HStack(spacing: 6) {
                        ForEach(tags, id: \.self) { tag in
                            TagChip(tag: tag, font: .footnote.weight(.semibold))
                        }
                    }
                    .padding(.leading, 16)
                }
                .scrollBounceBehavior(.basedOnSize, axes: .horizontal)
                Image(systemName: "pencil")
                    .font(.footnote.weight(.semibold))
                    .foregroundStyle(.secondary)
                    .padding(.trailing, 16)
            }
            .padding(.vertical, 6)
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .background(.bar)
        .overlay(alignment: .bottom) {
            Rectangle().fill(EggifyColors.border).frame(height: 0.5)
        }
        .accessibilityLabel("Tags: \(tags.joined(separator: ", "))")
        .accessibilityHint("Edit the session's tags")
    }
}

struct StatusDot: View {
    let status: SessionStatus
    @State private var pulse = false
    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    var body: some View {
        Circle()
            .fill(status.color)
            .frame(width: 7, height: 7)
            .overlay {
                if status.isActive && !reduceMotion {
                    Circle()
                        .stroke(status.color, lineWidth: 1.5)
                        .scaleEffect(pulse ? 2.2 : 1)
                        .opacity(pulse ? 0 : 0.8)
                        .animation(.easeOut(duration: 1.2).repeatForever(autoreverses: false), value: pulse)
                }
            }
            .onAppear { pulse = status.isActive }
            .onChange(of: status.isActive) { _, active in pulse = active }
            .accessibilityHidden(true)
    }
}

nonisolated enum ModelNames {
    /// "claude-sonnet-4-5-20250929" → "sonnet-4-5"
    static func short(_ model: String) -> String {
        var name = model
        if name.hasPrefix("claude-") { name.removeFirst("claude-".count) }
        if let range = name.range(of: #"-\d{8}$"#, options: .regularExpression) {
            name.removeSubrange(range)
        }
        return name
    }
}

// MARK: - Menu

struct ChatMenu: View {
    @ObservedObject var viewModel: ChatViewModel
    let onDelete: () -> Void

    var body: some View {
        Menu {
            Section {
                Button {
                    viewModel.showModelPicker = true
                } label: {
                    Label("Model: \(viewModel.session?.model_name.map(ModelNames.short) ?? "—")", systemImage: "cpu")
                }
                Toggle(isOn: Binding(
                    get: { viewModel.session?.yolo ?? false },
                    set: { viewModel.setYOLO($0) }
                )) {
                    Label("Auto-approve tools (YOLO)", systemImage: "bolt.shield")
                }
                Button {
                    viewModel.showTags = true
                } label: {
                    let tags = viewModel.session?.tags ?? []
                    Label(tags.isEmpty ? "Add tag" : "Tags: \(tags.joined(separator: ", "))", systemImage: "tag")
                }
                QuestionPolicyMenu(
                    current: viewModel.session?.question_policy,
                    globalDefault: viewModel.questionPolicyDefault,
                    onSelect: viewModel.setQuestionPolicy
                )
            }
            Section {
                Button { viewModel.navigate(to: .git) } label: {
                    Label("Git", systemImage: "arrow.triangle.branch")
                }
                Button { viewModel.navigate(to: .just) } label: {
                    Label("Just Commands", systemImage: "terminal")
                }
                .disabled(viewModel.projectId == nil)
                if viewModel.session?.loop != nil {
                    Button { viewModel.showLoopDetails = true } label: {
                        Label("Loop", systemImage: "arrow.triangle.2.circlepath")
                    }
                }
                if let team = viewModel.team {
                    Button { viewModel.showTeam = true } label: {
                        Label(team.workers.isEmpty ? "Workers" : "Workers (\(team.workers.count))",
                              systemImage: "person.2.badge.gearshape")
                    }
                }
                Button { viewModel.showSubagents = true } label: {
                    Label(viewModel.subagentsTotal > 0 ? "Subagents (\(viewModel.subagentsTotal))" : "Subagents",
                          systemImage: "person.2")
                }
                Button { viewModel.showMedia = true } label: {
                    Label(viewModel.mediaCount > 0 ? "Media (\(viewModel.mediaCount))" : "Media",
                          systemImage: "play.rectangle")
                }
                Button { viewModel.showSkills = true } label: {
                    Label(viewModel.skillsCount > 0 ? "Skills (\(viewModel.skillsCount))" : "Skills",
                          systemImage: "wand.and.stars")
                }
                Button { viewModel.navigate(to: .stats) } label: {
                    Label("Session Stats", systemImage: "chart.bar")
                }
                Button { viewModel.navigate(to: .zen) } label: {
                    Label("Zen View", systemImage: "moon.stars")
                }
            }
            if viewModel.status.isActive {
                Section {
                    Button(role: .destructive) { viewModel.interrupt() } label: {
                        Label("Stop the agent", systemImage: "stop.circle")
                    }
                }
            }
            Section {
                // Built on tap, not on every render of the menu.
                Button { ShareSheet.present([viewModel.transcript]) } label: {
                    Label("Share transcript", systemImage: "square.and.arrow.up")
                }
                Button { Task { await viewModel.refresh() } } label: {
                    Label("Reload messages", systemImage: "arrow.clockwise")
                }
                if let id = viewModel.session?.id {
                    Button {
                        UIPasteboard.general.string = id
                    } label: {
                        Label("Copy session ID", systemImage: "number")
                    }
                }
            }
            Section {
                Button(role: .destructive, action: onDelete) {
                    Label("Delete session", systemImage: "trash")
                }
            }
        } label: {
            Image(systemName: "ellipsis.circle")
                .accessibilityLabel("Session options")
        }
    }
}

/// "Questions" in the chat menu: what happens when the agent asks a question
/// and nobody answers (this session only; Default follows the global setting).
struct QuestionPolicyMenu: View {
    let current: QuestionPolicy?
    let globalDefault: QuestionPolicy?
    let onSelect: (QuestionPolicy?) -> Void

    static func defaultLabel(_ global: QuestionPolicy?) -> String {
        global.map { "Default (\($0.label))" } ?? "Default"
    }

    var body: some View {
        Menu {
            Picker("Questions", selection: Binding<QuestionPolicy?>(
                get: { current },
                set: { if $0 != current { onSelect($0) } }
            )) {
                Text(Self.defaultLabel(globalDefault)).tag(QuestionPolicy?.none)
                ForEach(QuestionPolicy.choices, id: \.self) { policy in
                    Text(policy.label).tag(QuestionPolicy?.some(policy))
                }
                // A value set elsewhere (e.g. 45 s on the web) stays selectable.
                if let current, !QuestionPolicy.choices.contains(current) {
                    Text(current.label).tag(QuestionPolicy?.some(current))
                }
            }
        } label: {
            Label("Questions: \(current?.label ?? Self.defaultLabel(globalDefault))",
                  systemImage: "questionmark.bubble")
        }
    }
}

// MARK: - Composer

struct Composer: View {
    @ObservedObject var model: ComposerModel
    /// The agent is working, so an empty composer offers Stop instead of Send.
    let isWorking: Bool
    @Binding var isFocused: Bool
    let onSend: () -> Void
    let onStop: () -> Void

    private var showStop: Bool { isWorking && !model.canSend }

    /// Image paste, while there is room for another attachment.
    private var pasteImages: (([NSItemProvider]) -> Void)? {
        guard model.remainingAttachmentSlots > 0 else { return nil }
        let model = model
        return { model.addPastedImages($0) }
    }

    var body: some View {
        VStack(spacing: 8) {
            if !model.attachments.isEmpty || model.preparingCount > 0 {
                ScrollView(.horizontal, showsIndicators: false) {
                    HStack(spacing: 8) {
                        ForEach(model.attachments) { attachment in
                            Image(uiImage: attachment.thumbnail)
                                .resizable()
                                .scaledToFill()
                                .frame(width: 64, height: 64)
                                .clipShape(RoundedRectangle(cornerRadius: 10, style: .continuous))
                                .overlay(alignment: .topTrailing) {
                                    Button {
                                        model.removeAttachment(attachment)
                                    } label: {
                                        Image(systemName: "xmark.circle.fill")
                                            .symbolRenderingMode(.palette)
                                            .foregroundStyle(.white, .black.opacity(0.6))
                                    }
                                    .offset(x: 6, y: -6)
                                    .accessibilityLabel("Remove image")
                                }
                        }
                        ForEach(0..<model.preparingCount, id: \.self) { _ in
                            RoundedRectangle(cornerRadius: 10, style: .continuous)
                                .fill(EggifyColors.surface)
                                .frame(width: 64, height: 64)
                                .overlay { ProgressView() }
                                .accessibilityLabel("Loading photo")
                        }
                    }
                    .padding(.top, 6)
                    .padding(.horizontal, 4)
                }
            }

            HStack(alignment: .bottom, spacing: 8) {
                PhotosPicker(selection: $model.photoSelection,
                             maxSelectionCount: max(1, model.remainingAttachmentSlots),
                             matching: .images) {
                    Image(systemName: "plus")
                        .font(.body.weight(.semibold))
                        .frame(width: 36, height: 36)
                        .background(EggifyColors.surface, in: Circle())
                        .overlay(Circle().strokeBorder(EggifyColors.border, lineWidth: 0.5))
                }
                .disabled(model.remainingAttachmentSlots == 0)
                .accessibilityLabel("Attach photos")

                ComposerTextView(text: $model.text, isFocused: $isFocused, placeholder: "Message",
                                 onPasteImages: pasteImages)
                    .padding(.horizontal, 14)
                    .padding(.vertical, 9)
                    .background(EggifyColors.surface, in: RoundedRectangle(cornerRadius: 20, style: .continuous))
                    .overlay(
                        RoundedRectangle(cornerRadius: 20, style: .continuous)
                            .strokeBorder(EggifyColors.border, lineWidth: 0.5)
                    )

                if showStop {
                    Button(action: onStop) {
                        Image(systemName: "stop.fill")
                            .font(.body.weight(.bold))
                            .foregroundStyle(.white)
                            .frame(width: 36, height: 36)
                            .background(EggifyColors.error, in: Circle())
                    }
                    .accessibilityLabel("Stop the agent")
                    .transition(.scale.combined(with: .opacity))
                } else {
                    Button {
                        onSend()
                        // Hide the keyboard so the reply has the screen, as Zen does.
                        isFocused = false
                    } label: {
                        Image(systemName: "arrow.up")
                            .font(.body.weight(.bold))
                            .foregroundStyle(.white)
                            .frame(width: 36, height: 36)
                            .background(model.canSend ? AnyShapeStyle(EggifyGradients.sendButton) : AnyShapeStyle(Color(.systemGray4)), in: Circle())
                    }
                    .disabled(!model.canSend)
                    .accessibilityLabel("Send")
                    .transition(.scale.combined(with: .opacity))
                }
            }
            .animation(.snappy(duration: 0.2), value: showStop)
        }
        .padding(.horizontal, 12)
        .padding(.bottom, 8)
    }
}

// MARK: - Small pieces

struct WorkingIndicator: View {
    @State private var animate = false
    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    var body: some View {
        HStack(spacing: 8) {
            HStack(spacing: 4) {
                ForEach(0..<3) { index in
                    Circle()
                        .fill(EggifyColors.accent.opacity(0.7))
                        .frame(width: 6, height: 6)
                        .offset(y: animate && !reduceMotion ? -3 : 2)
                        .animation(.easeInOut(duration: 0.5).repeatForever(autoreverses: true).delay(Double(index) * 0.15),
                                   value: animate)
                }
            }
            Text("Working…")
                .font(.footnote.weight(.medium))
                .foregroundStyle(.secondary)
        }
        .padding(.vertical, 6)
        .onAppear { animate = true }
        .accessibilityElement(children: .ignore)
        .accessibilityLabel("The agent is working")
    }
}

struct LoadEarlierButton: View {
    let isLoading: Bool
    let action: () -> Void

    var body: some View {
        Button(action: action) {
            if isLoading {
                ProgressView()
            } else {
                Label("Load earlier messages", systemImage: "arrow.up.circle")
                    .font(.footnote.weight(.medium))
            }
        }
        .buttonStyle(.bordered)
        .buttonBorderShape(.capsule)
        .disabled(isLoading)
        .padding(.bottom, 4)
    }
}

struct JumpToBottomButton: View {
    let unseen: Int
    let action: () -> Void

    var body: some View {
        Button(action: action) {
            HStack(spacing: 4) {
                Image(systemName: "arrow.down")
                if unseen > 0 {
                    Text("\(unseen)")
                }
            }
            .font(.footnote.weight(.bold))
            .padding(.horizontal, 12)
            .padding(.vertical, 10)
            .background(.regularMaterial, in: Capsule())
            .shadow(color: .black.opacity(0.15), radius: 6, y: 2)
        }
        .buttonStyle(.plain)
        .accessibilityLabel(unseen > 0 ? "Jump to latest, \(unseen) new" : "Jump to latest")
    }
}

struct ConnectionBanner: View {
    let state: WebSocketConnectionState
    let onRetry: () -> Void

    var body: some View {
        HStack(spacing: 8) {
            if case .disconnected = state {
                Image(systemName: "wifi.slash")
            } else {
                ProgressView().controlSize(.small)
            }
            Text(state == .disconnected ? "Offline — live updates paused" : state.label)
                .font(.footnote.weight(.medium))
            Spacer()
            Button("Retry", action: onRetry)
                .font(.footnote.weight(.semibold))
                .buttonStyle(.borderless)
        }
        .padding(.horizontal, 16)
        .padding(.vertical, 8)
        .background(EggifyColors.warning.opacity(0.15))
        .background(.bar)
    }
}

// MARK: - Preview

#Preview {
    NavigationStack {
        ChatView(sessionID: "example-id", sessionTitle: "Claude")
    }
}

// MARK: - Swipe to Zen

/// A thin strip along the trailing edge. Dragging left from it pulls in a
/// moon hint that follows the finger; letting go past the threshold opens Zen.
/// Kept to the edge so it never competes with scrolling or text selection.
private struct ZenSwipeEdge: View {
    let onTrigger: () -> Void

    @State private var dragDistance: CGFloat = 0
    @State private var armed = false

    private let threshold: CGFloat = 80

    private var progress: CGFloat { min(dragDistance / threshold, 1) }

    var body: some View {
        Color.clear
            .frame(width: 22)
            .contentShape(Rectangle())
            .overlay(alignment: .trailing) {
                if dragDistance > 0 {
                    Image(systemName: "moon.stars.fill")
                        .font(.system(size: 18, weight: .semibold))
                        .foregroundStyle(armed ? .white : EggifyColors.accent)
                        .frame(width: 44, height: 44)
                        .background(Circle().fill(armed ? EggifyColors.accent : Color(.secondarySystemBackground)))
                        .shadow(color: .black.opacity(0.15), radius: 6, y: 2)
                        .scaleEffect(0.6 + 0.4 * progress)
                        .opacity(Double(progress))
                        .offset(x: 44 - min(dragDistance, threshold + 20))
                        .allowsHitTesting(false)
                        .accessibilityHidden(true)
                }
            }
            .gesture(
                DragGesture(minimumDistance: 10, coordinateSpace: .local)
                    .onChanged { value in
                        // Only leftward, mostly horizontal drags count.
                        let dx = -value.translation.width
                        guard dx > 0, dx > abs(value.translation.height) else {
                            dragDistance = 0
                            if armed { armed = false }
                            return
                        }
                        dragDistance = dx
                        let nowArmed = dx >= threshold
                        if nowArmed != armed {
                            armed = nowArmed
                            if nowArmed { EggifyHaptics.tap() }
                        }
                    }
                    .onEnded { _ in
                        let fire = armed
                        withAnimation(.snappy) {
                            dragDistance = 0
                            armed = false
                        }
                        if fire { onTrigger() }
                    }
            )
            .accessibilityElement()
            .accessibilityLabel("Open Zen view")
            .accessibilityAddTraits(.isButton)
            .accessibilityAction { onTrigger() }
    }
}
