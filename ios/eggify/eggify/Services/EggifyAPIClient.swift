//
//  EggifyAPIClient.swift
//  eggify
//
//  API client for communicating with Eggify backend
//

import Foundation

// MARK: - API Models

struct LoginRequest: Codable {
    let username: String
    let password: String
}

struct LoginResponse: Codable {
    let username: String
    let expires_at: String?
    let mfa_required: Bool?
    let temporary_token: String?
    let temporary_expires: String?
}

// Empty response for endpoints that return no data
struct EmptyResponse: Codable {}

struct SessionMetadata: Codable, Identifiable {
    let id: String
    let status: String
    let created_at: String
    let updated_at: String
    let ended_at: String?
    let message_count: Int
    let cost_usd: Double
    let num_turns: Int
    let duration_ms: Int64
    let error_message: String?
    let model_name: String?
    let git_branch: String?
    let provider: String?
    let project_id: String?
    let selected_avatar_id: Int64?
    let selected_avatar: Avatar?

    /// Shortened session ID (first 8 characters)
    var shortID: String {
        return String(id.prefix(8))
    }
}

struct HealthResponse: Codable {
    let status: String
    let version: String?
    let uptime: Int?
}

struct TranscriptionResponse: Codable {
    let text: String
    let language: String?
    let duration: Double?
}

struct ConversationData: Codable {
    let id: String
    let status: String
    let total_commands: Int?
    let created_at: String
    let updated_at: String?
}

struct Project: Codable, Identifiable, Hashable {
    let id: String
    let name: String
    let path: String
    let description: String?
    let default_model: String?
    let default_provider: String?
    let settings: String?
    let is_active: Bool
    let created_at: String
    let updated_at: String
    // Filled by GET api/projects/overview only (nil from api/projects).
    var session_count: Int? = nil
    var running_count: Int? = nil
    var last_activity: String? = nil
    var display_path: String? = nil
    var missing: Bool? = nil
    var git: ProjectGit? = nil

    func hash(into hasher: inout Hasher) {
        hasher.combine(id)
    }

    static func == (lhs: Project, rhs: Project) -> Bool {
        lhs.id == rhs.id
            && lhs.updated_at == rhs.updated_at
            && lhs.session_count == rhs.session_count
            && lhs.running_count == rhs.running_count
            && lhs.last_activity == rhs.last_activity
            && lhs.missing == rhs.missing
            && lhs.git == rhs.git
    }
}

struct ProjectGit: Codable, Hashable {
    let is_repo: Bool
    let branch: String?
    let remote: String?
    let dirty: Bool?
}

struct SiteProject: Codable, Identifiable {
    let id: String
    let user_description: String
    let category: String?
    let style_preferences: String?
    let additional_notes: String?
    let provider: String
    let model: String
    let status: String
    let current_step: String?
    let orchestrator_plan: String?
    let workspace_path: String?
    let created_at: String
    let updated_at: String
    let completed_at: String?
    let error_message: String?
}

struct APIResponse<T: Codable>: Codable {
    let data: T?
    let error: String?
    let message: String?
}

// MARK: - API Client

/// Result of a password login: either signed in, or a second factor is needed.
enum LoginOutcome {
    case signedIn
    case mfaRequired(temporaryToken: String)
}

class EggifyAPIClient: NSObject {
    static let shared = EggifyAPIClient()

    private var urlSession: URLSession
    private let secureStorage = SecureStorage.shared
    private let hostManager = HostManager.shared

    // Configuration
    private(set) var baseURL: URL?
    private(set) var sessionToken: String?
    private(set) var currentHostId: String?

    /// Shared session with the Eggify TLS policy (use it for any server request).
    var session: URLSession { urlSession }

    /// Same TLS policy (relay pin included), for media downloads, which may
    /// take longer than the 5 minutes other requests get.
    private(set) lazy var mediaSession: URLSession = createEggifyURLSession(resourceTimeout: 3 * 3600)

    override private init() {
        self.urlSession = createEggifyURLSession()
        super.init()
        loadStoredCredentials()
    }

    // MARK: - Configuration

    /// Configure the API client with server URL (for a new host or current host)
    func configureServer(baseURL: URL, hostId: String? = nil) throws {
        self.baseURL = baseURL
        if let hostId = hostId {
            self.currentHostId = hostId
        }
    }

    /// Set session token after successful login
    func setSessionToken(_ token: String, expiresAt: Date? = nil) throws {
        self.sessionToken = token
        if let hostId = currentHostId {
            try secureStorage.storeHostCredential(hostId: hostId, sessionToken: token, expiresAt: expiresAt)
        }
    }

    /// Switch to a different host
    func switchHost(hostId: String) async throws {
        guard let host = hostManager.hosts.first(where: { $0.id == hostId }) else {
            throw EggifyAPIError.notFound
        }

        guard let url = URL(string: host.url) else { throw EggifyAPIError.invalidURL }
        try configureServer(baseURL: url, hostId: hostId)

        // A keychain read error just means "not signed in on this host".
        if let credential = try? secureStorage.retrieveHostCredential(hostId: hostId),
           !isCredentialExpired(credential) {
            self.sessionToken = credential.sessionToken
        } else {
            self.sessionToken = nil
        }

        await hostManager.recordHostUse(id: hostId)
    }

    /// Load stored credentials from Keychain (synchronously, so the first
    /// screen already knows whether the user is signed in).
    private func loadStoredCredentials() {
        guard let host = hostManager.currentHost, let url = URL(string: host.url) else { return }
        self.baseURL = url
        self.currentHostId = host.id
        if let credential = try? secureStorage.retrieveHostCredential(hostId: host.id),
           !isCredentialExpired(credential) {
            self.sessionToken = credential.sessionToken
        }
    }

    /// Forget the session token for the current host (keeps the host itself).
    func clearCredentials() throws {
        if let hostId = currentHostId {
            try secureStorage.deleteHostCredential(hostId: hostId)
        }
        clearCookies()
        self.sessionToken = nil
    }

    /// Sign out on the server (revokes the token), then forget it locally.
    func logout() async {
        if let baseURL, sessionToken != nil {
            var request = authorizedRequest(url: baseURL.appendingPathComponent("api/auth/logout"), method: "POST")
            request.timeoutInterval = 8
            _ = try? await urlSession.data(for: request)
        }
        try? clearCredentials()
    }

    private func clearCookies() {
        guard let baseURL, let cookies = HTTPCookieStorage.shared.cookies(for: baseURL) else { return }
        for cookie in cookies where cookie.name == "session_token" {
            HTTPCookieStorage.shared.deleteCookie(cookie)
        }
    }

    /// Check if a credential has expired
    private func isCredentialExpired(_ credential: HostCredential) -> Bool {
        if let expiresAt = credential.expiresAt {
            return Date() > expiresAt
        }
        return false
    }

    // MARK: - Requests

    /// A request carrying the user's session token.
    func authorizedRequest(url: URL, method: String = "GET", body: Data? = nil) -> URLRequest {
        var request = URLRequest(url: url)
        request.httpMethod = method
        request.timeoutInterval = 30
        if body != nil {
            request.setValue("application/json", forHTTPHeaderField: "Content-Type")
            request.httpBody = body
        }
        if let token = sessionToken {
            request.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")
        }
        return request
    }

    func url(_ path: String) throws -> URL {
        guard let baseURL else { throw EggifyAPIError.notConfigured }
        return baseURL.appendingPathComponent(path)
    }

    /// Perform a request and return the raw body, mapping HTTP errors.
    @discardableResult
    func send(_ request: URLRequest) async throws -> Data {
        try await sendWithResponse(request).data
    }

    /// Like `send`, but also returns the HTTP response (headers).
    func sendWithResponse(_ request: URLRequest) async throws -> (data: Data, response: HTTPURLResponse) {
        let data: Data
        let response: URLResponse
        do {
            (data, response) = try await urlSession.data(for: request)
        } catch let error as URLError where error.code == .cancelled {
            throw CancellationError()
        } catch {
            throw EggifyAPIError.networkError(error)
        }
        guard let http = response as? HTTPURLResponse else { throw EggifyAPIError.invalidResponse }
        if (200...299).contains(http.statusCode) { return (data, http) }

        let body = (try? JSONSerialization.jsonObject(with: data)) as? [String: Any] ?? [:]
        let message = body.nonEmptyString("message") ?? body.nonEmptyString("error")
        switch http.statusCode {
        case 401:
            if sessionToken != nil, request.url?.path.hasSuffix("/auth/login") == false {
                // The token was revoked or expired on the server.
                try? clearCredentials()
                NotificationCenter.default.post(name: .sessionExpired, object: nil)
            }
            throw EggifyAPIError.unauthorized
        case 403 where body.string("code") == "user_required":
            throw EggifyAPIError.userRequired(message ?? "Sign in again to do this.")
        case 402 where LicenseGate.isLicenseRequired(statusCode: http.statusCode, body: body):
            // Eggify on the computer isn't activated: one full-screen state.
            LicenseGate.announce()
            throw EggifyAPIError.licenseRequired
        case 404:
            throw EggifyAPIError.notFound
        default:
            if let message { throw EggifyAPIError.server(http.statusCode, message) }
            if http.statusCode >= 500 { throw EggifyAPIError.serverError(http.statusCode) }
            throw EggifyAPIError.httpError(http.statusCode)
        }
    }

    /// GET/POST returning a loosely-typed JSON object.
    func json(_ path: String, method: String = "GET", query: [URLQueryItem] = [], body: [String: Any]? = nil) async throws -> [String: Any] {
        var components = URLComponents(url: try url(path), resolvingAgainstBaseURL: false)
        if !query.isEmpty { components?.queryItems = query }
        guard let url = components?.url else { throw EggifyAPIError.invalidURL }
        let bodyData = try body.map { try JSONSerialization.data(withJSONObject: $0) }
        let data = try await send(authorizedRequest(url: url, method: method, body: bodyData))
        if data.isEmpty { return [:] }
        guard let object = try JSONSerialization.jsonObject(with: data) as? [String: Any] else {
            throw EggifyAPIError.invalidResponse
        }
        return object
    }

    // MARK: - Authentication

    /// Login with username and password. The backend delivers the session
    /// token as an HttpOnly cookie; it is kept for Bearer auth afterwards.
    func login(username: String, password: String) async throws -> LoginOutcome {
        let body = try JSONEncoder().encode(LoginRequest(username: username, password: password))
        var request = URLRequest(url: try url("api/auth/login"))
        request.httpMethod = "POST"
        request.setValue("application/json", forHTTPHeaderField: "Content-Type")
        request.httpBody = body

        let (data, http) = try await sendWithResponse(request)
        let response = (try? JSONSerialization.jsonObject(with: data)) as? [String: Any] ?? [:]

        if response.bool("mfa_required") == true, let temp = response.nonEmptyString("temporary_token") {
            return .mfaRequired(temporaryToken: temp)
        }
        try storeSessionCookie(from: http, expiresAt: EggifyDate.parse(response.string("expires_at")))
        return .signedIn
    }

    /// Second step of an MFA login: a TOTP or backup code.
    func verifyMFA(temporaryToken: String, code: String) async throws {
        let body = try JSONSerialization.data(withJSONObject: [
            "temporary_token": temporaryToken,
            "code": code.replacingOccurrences(of: " ", with: "")
        ])
        var request = URLRequest(url: try url("api/auth/mfa/verify"))
        request.httpMethod = "POST"
        request.setValue("application/json", forHTTPHeaderField: "Content-Type")
        request.httpBody = body
        let (_, http) = try await sendWithResponse(request)
        try storeSessionCookie(from: http, expiresAt: Date().addingTimeInterval(7 * 24 * 3600))
    }

    /// The session token arrives as an HttpOnly `session_token` cookie.
    /// Cookie storage ignores cookies for some hosts (e.g. `localhost`), so
    /// the Set-Cookie header is read directly as well.
    private func storeSessionCookie(from response: HTTPURLResponse, expiresAt: Date?) throws {
        let url = response.url ?? baseURL ?? URL(string: "https://localhost")!
        let headers = response.allHeaderFields.reduce(into: [String: String]()) { result, pair in
            if let key = pair.key as? String, let value = pair.value as? String { result[key] = value }
        }
        let fromHeader = HTTPCookie.cookies(withResponseHeaderFields: headers, for: url)
        let fromStorage = HTTPCookieStorage.shared.cookies(for: url) ?? []
        guard let cookie = (fromHeader + fromStorage).first(where: { $0.name == "session_token" && !$0.value.isEmpty })
                ?? Self.parseSessionCookie(response.value(forHTTPHeaderField: "Set-Cookie")) else {
            throw EggifyAPIError.server(0, "The server did not return a session. Check the server URL.")
        }
        try setSessionToken(cookie.value, expiresAt: expiresAt ?? cookie.expiresDate)
    }

    private static func parseSessionCookie(_ header: String?) -> HTTPCookie? {
        guard let header,
              let part = header.components(separatedBy: ";").first(where: {
                  $0.trimmingCharacters(in: .whitespaces).hasPrefix("session_token=")
              }) else { return nil }
        let value = String(part.trimmingCharacters(in: .whitespaces).dropFirst("session_token=".count))
        guard !value.isEmpty else { return nil }
        return HTTPCookie(properties: [.name: "session_token", .value: value, .path: "/", .domain: "localhost"])
    }

    /// Test connectivity to the Eggify server
    func testConnection() async throws -> HealthResponse {
        let data = try await send(authorizedRequest(url: try url("api/health")))
        return try JSONDecoder().decode(HealthResponse.self, from: data)
    }

    // MARK: - Sessions & messages

    func getAgentSessions() async throws -> [AgentSession] {
        let response = try await json("api/agent/sessions")
        let rows = response.dicts("sessions")
        SessionCache.shared.storeSessions(rows, host: currentHostId)
        return rows.compactMap(AgentSession.parse)
    }

    /// A session's subagents with their recent output, oldest first.
    func getSubagents(sessionID: String) async throws -> [Subagent] {
        let response = try await json("api/agent/sessions/\(sessionID)/subagents")
        return response.dicts("subagents").compactMap(Subagent.parse)
    }

    /// The cached one-line AI summary of a session, if one was already written.
    /// Never asks the server to generate one (that costs money), as the web's Zen plate.
    func getCachedSessionSummary(sessionID: String, messageCount: Int, status: String) async -> String? {
        let body: [String: Any] = [
            "sessions": [["id": sessionID, "version": "\(messageCount):\(status)"]],
            "generate": false,
        ]
        guard let response = try? await json("api/agent/overview/digests", method: "POST", body: body),
              let digests = response["digests"] as? [String: Any],
              let digest = digests[sessionID] as? [String: Any],
              let summary = digest["summary"] as? String else { return nil }
        let trimmed = summary.trimmingCharacters(in: .whitespacesAndNewlines)
        return trimmed.isEmpty ? nil : trimmed
    }

    /// The cached AI digests of sessions (session id → topic tags and one-line
    /// summary), as the web's agent overview shows them. Never asks the server
    /// to generate any.
    func getCachedDigests(sessions: [(id: String, version: String)]) async -> [String: (tags: [String], summary: String)]? {
        guard !sessions.isEmpty else { return [:] }
        let body: [String: Any] = [
            "sessions": sessions.prefix(100).map { ["id": $0.id, "version": $0.version] },
            "generate": false,
        ]
        guard let response = try? await json("api/agent/overview/digests", method: "POST", body: body),
              let digests = response["digests"] as? [String: Any] else { return nil }
        var out: [String: (tags: [String], summary: String)] = [:]
        for (id, value) in digests {
            let digest = value as? [String: Any]
            let tags = (digest?["tags"] as? [Any])?.compactMap { $0 as? String } ?? []
            let summary = (digest?["summary"] as? String ?? "").trimmingCharacters(in: .whitespacesAndNewlines)
            out[id] = (tags.filter { !$0.trimmingCharacters(in: .whitespaces).isEmpty }, summary)
        }
        return out
    }

    // MARK: - Question policy

    /// The global policy for agent questions (`is_default`: never set by the user).
    func getQuestionPolicyDefault() async throws -> (policy: QuestionPolicy, isDefault: Bool) {
        let response = try await json("api/agent/question-policy")
        guard let policy = QuestionPolicy.parse(response["policy"]) else { throw EggifyAPIError.invalidResponse }
        return (policy, response.bool("is_default") ?? false)
    }

    /// Sets the global policy (signed-in user only).
    func setQuestionPolicyDefault(_ policy: QuestionPolicy) async throws -> QuestionPolicy {
        let response = try await json("api/agent/question-policy", method: "PUT", body: policy.json)
        return QuestionPolicy.parse(response["policy"]) ?? policy
    }

    /// Sets (or with nil clears) a session's own policy. Returns the override
    /// and the policy that now applies.
    func setSessionQuestionPolicy(sessionID: String, policy: QuestionPolicy?) async throws
        -> (policy: QuestionPolicy?, effective: QuestionPolicy?) {
        let body: [String: Any] = ["policy": policy?.json ?? NSNull()]
        let response = try await json("api/agent/sessions/\(sessionID)/question-policy", method: "PUT", body: body)
        return (QuestionPolicy.parse(response["policy"]), QuestionPolicy.parse(response["effective"]))
    }

    // MARK: - Session tags

    /// Replaces a session's own tags (`[]` clears them); returns the list the
    /// server kept (normalised, at most 8).
    func setSessionTags(sessionID: String, tags: [String]) async throws -> [String] {
        let response = try await json("api/agent/sessions/\(sessionID)/tags", method: "PUT", body: ["tags": tags])
        return (response["tags"] as? [Any])?.compactMap { $0 as? String } ?? tags
    }

    /// The own tags of every session the caller may see, by session ID.
    func allSessionTags() async throws -> [String: [String]] {
        let response = try await json("api/agent/session-tags")
        let tags = response["tags"] as? [String: Any] ?? [:]
        return tags.mapValues { ($0 as? [Any])?.compactMap { $0 as? String } ?? [] }
    }

    /// The session list from the last successful load (instant launch).
    func cachedSessions() async -> [AgentSession] {
        await SessionCache.shared.sessions(host: currentHostId).compactMap(AgentSession.parse)
    }

    /// The latest messages of a chat from its last load.
    func cachedMessages(sessionID: String) async -> [AgentMessage] {
        await SessionCache.shared.messages(session: sessionID, host: currentHostId).compactMap(AgentMessage.parse)
    }

    /// Latest messages, oldest first. `beforeSequence` pages backwards.
    func getSessionMessages(sessionID: String, limit: Int = 60, beforeSequence: Int = 0) async throws -> (messages: [AgentMessage], hasMore: Bool) {
        var query = [URLQueryItem(name: "limit", value: String(limit))]
        if beforeSequence > 0 {
            query.append(URLQueryItem(name: "before_sequence", value: String(beforeSequence)))
        }
        let response = try await json("api/agent/sessions/\(sessionID)/messages", query: query)
        let rows = response.dicts("messages")
        if beforeSequence == 0 {
            SessionCache.shared.storeMessages(rows, session: sessionID, host: currentHostId)
        }
        let messages = rows.compactMap(AgentMessage.parse)
        return (messages, response.bool("has_more") ?? false)
    }

    /// Get main projects list
    func getProjects() async throws -> [Project] {
        struct ProjectsResponse: Codable { let projects: [Project]? }
        let response: ProjectsResponse = try await performRequest(url: try url("api/projects"), method: "GET", body: nil)
        return response.projects ?? []
    }

    /// Projects with session counts, last activity, missing-folder flag and
    /// (unless `git` is false) branch/dirty state, in one request.
    func getProjectsOverview(git: Bool = true) async throws -> [Project] {
        struct OverviewResponse: Codable { let projects: [Project]? }
        var components = URLComponents(url: try url("api/projects/overview"), resolvingAgainstBaseURL: false)
        if !git { components?.queryItems = [URLQueryItem(name: "git", value: "0")] }
        guard let overviewURL = components?.url else { throw EggifyAPIError.notConfigured }
        let response: OverviewResponse = try await performRequest(url: overviewURL, method: "GET", body: nil)
        return response.projects ?? []
    }

    /// Get sessions for a specific project
    func getProjectSessions(projectID: String) async throws -> [SessionMetadata] {
        struct ProjectSessionsResponse: Codable { let sessions: [SessionMetadata]? }
        let response: ProjectSessionsResponse = try await performRequest(url: try url("api/projects/\(projectID)/sessions"), method: "GET", body: nil)
        return response.sessions ?? []
    }

    // MARK: - Audio Transcription

    func transcribeAudio(audioData: Data) async throws -> String {
        guard let baseURL = baseURL else {
            throw EggifyAPIError.notConfigured
        }

        let url = baseURL.appendingPathComponent("api/transcribe")

        var request = URLRequest(url: url)
        request.httpMethod = "POST"
        request.timeoutInterval = 60  // Longer timeout for audio processing

        // Create multipart form data
        let boundary = UUID().uuidString
        request.setValue("multipart/form-data; boundary=\(boundary)", forHTTPHeaderField: "Content-Type")

        // Add authentication header if we have a session token
        if let token = sessionToken {
            request.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")
        }

        // Build multipart body
        var body = Data()

        // Add audio file
        body.append("--\(boundary)\r\n".data(using: .utf8)!)
        body.append("Content-Disposition: form-data; name=\"audio\"; filename=\"audio.m4a\"\r\n".data(using: .utf8)!)
        body.append("Content-Type: audio/mp4\r\n\r\n".data(using: .utf8)!)
        body.append(audioData)
        body.append("\r\n".data(using: .utf8)!)

        // End boundary
        body.append("--\(boundary)--\r\n".data(using: .utf8)!)

        request.httpBody = body


        let data = try await send(request)
        if let decoded = try? JSONDecoder().decode(TranscriptionResponse.self, from: data) {
            return decoded.text
        }
        if let jsonDict = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
           let text = jsonDict["text"] as? String {
            return text
        }
        throw EggifyAPIError.invalidResponse
    }

    // MARK: - Internal Methods

    private func performRequest<T: Decodable>(url: URL, method: String, body: Data?) async throws -> T {
        let data = try await send(authorizedRequest(url: url, method: method, body: body))
        do {
            return try JSONDecoder().decode(T.self, from: data.isEmpty ? Data("{}".utf8) : data)
        } catch {
            throw EggifyAPIError.decodingError(error)
        }
    }

    /// Get all avatar themes
    func getAvatarThemes() async throws -> [AvatarTheme] {
        guard let baseURL = baseURL else {
            throw EggifyAPIError.notConfigured
        }

        let url = baseURL.appendingPathComponent("api/avatars/themes")

        struct ThemesResponse: Codable {
            let themes: [AvatarTheme]?
        }

        let response: ThemesResponse = try await performRequest(url: url, method: "GET", body: nil)
        return response.themes ?? []
    }

    /// Get avatar theme by ID
    func getAvatarTheme(themeId: Int64) async throws -> AvatarTheme {
        guard let baseURL = baseURL else {
            throw EggifyAPIError.notConfigured
        }

        let url = baseURL.appendingPathComponent("api/avatars/themes/\(themeId)")

        struct ThemeResponse: Codable {
            let id: Int64
            let name: String
        }

        let response: ThemeResponse = try await performRequest(url: url, method: "GET", body: nil)
        return AvatarTheme(id: response.id, name: response.name)
    }

    /// Get avatars for a theme
    func getThemeAvatars(themeId: Int64) async throws -> [Avatar] {
        guard let baseURL = baseURL else {
            throw EggifyAPIError.notConfigured
        }

        let url = baseURL.appendingPathComponent("api/avatars/themes/\(themeId)/avatars")

        struct AvatarsResponse: Codable {
            let avatars: [Avatar]?
        }

        let response: AvatarsResponse = try await performRequest(url: url, method: "GET", body: nil)
        return response.avatars ?? []
    }

    /// Get avatar image by avatar ID using the /api/avatars/:id/image endpoint
    func getAvatarImage(avatarId: Int64) async throws -> Data {
        try await send(authorizedRequest(url: try url("api/avatars/\(avatarId)/image")))
    }

    // MARK: - Just Commands API

    func getJustRecipes(projectID: String) async throws -> (recipes: [JustRecipe], hasJustfile: Bool) {
        guard let baseURL = baseURL else {
            throw EggifyAPIError.notConfigured
        }

        let url = baseURL.appendingPathComponent("api/projects/\(projectID)/just/recipes")

        struct RecipesResponse: Codable {
            let recipes: [JustRecipe]?
            let has_justfile: Bool?
        }

        let response: RecipesResponse = try await performRequest(url: url, method: "GET", body: nil)
        return (response.recipes ?? [], response.has_justfile ?? false)
    }

    func runJustRecipe(projectID: String, recipe: String, args: [String]? = nil) async throws -> JustJob {
        guard let baseURL = baseURL else {
            throw EggifyAPIError.notConfigured
        }

        let url = baseURL.appendingPathComponent("api/projects/\(projectID)/just/run")

        struct RunRequest: Codable {
            let recipe: String
            let args: [String]?
        }

        struct RunResponse: Codable {
            let job: JustJob?
        }

        let body = try JSONEncoder().encode(RunRequest(recipe: recipe, args: args))
        let response: RunResponse = try await performRequest(url: url, method: "POST", body: body)
        guard let job = response.job else {
            throw EggifyAPIError.decodingError(NSError(domain: "EggifyAPI", code: -1, userInfo: [NSLocalizedDescriptionKey: "Missing job in response"]))
        }
        return job
    }

    func getJustJobs(projectID: String) async throws -> [JustJob] {
        guard let baseURL = baseURL else {
            throw EggifyAPIError.notConfigured
        }

        let url = baseURL.appendingPathComponent("api/projects/\(projectID)/just/jobs")

        struct JobsResponse: Codable {
            let jobs: [JustJob]?
        }

        let response: JobsResponse = try await performRequest(url: url, method: "GET", body: nil)
        return response.jobs ?? []
    }

    func getJustJob(projectID: String, jobID: String) async throws -> JustJob {
        guard let baseURL = baseURL else {
            throw EggifyAPIError.notConfigured
        }

        let url = baseURL.appendingPathComponent("api/projects/\(projectID)/just/jobs/\(jobID)")

        struct JobResponse: Codable {
            let job: JustJob?
        }

        let response: JobResponse = try await performRequest(url: url, method: "GET", body: nil)
        guard let job = response.job else {
            throw EggifyAPIError.notFound
        }
        return job
    }

    func stopJustJob(projectID: String, jobID: String) async throws -> JustJob {
        guard let baseURL = baseURL else {
            throw EggifyAPIError.notConfigured
        }

        let url = baseURL.appendingPathComponent("api/projects/\(projectID)/just/jobs/\(jobID)/stop")

        struct JobResponse: Codable {
            let job: JustJob?
        }

        let response: JobResponse = try await performRequest(url: url, method: "POST", body: nil)
        guard let job = response.job else {
            throw EggifyAPIError.notFound
        }
        return job
    }

    func deleteJustJob(projectID: String, jobID: String) async throws {
        guard let baseURL = baseURL else {
            throw EggifyAPIError.notConfigured
        }

        let url = baseURL.appendingPathComponent("api/projects/\(projectID)/just/jobs/\(jobID)")
        _ = try await performRequest(url: url, method: "DELETE", body: nil) as EmptyResponse
    }

    // MARK: - Session Context API

    func getSessionContext(sessionID: String) async throws -> SessionContextInfo? {
        guard let baseURL = baseURL else {
            throw EggifyAPIError.notConfigured
        }

        let url = baseURL.appendingPathComponent("api/agent/sessions/\(sessionID)/context")
        return try await performRequest(url: url, method: "GET", body: nil)
    }

    // MARK: - Git API

    func getGitStatus(sessionID: String) async throws -> GitStatus {
        guard let baseURL = baseURL else {
            throw EggifyAPIError.notConfigured
        }

        let url = baseURL.appendingPathComponent("api/agent/sessions/\(sessionID)/git-status")
        return try await performRequest(url: url, method: "GET", body: nil)
    }

    func getGitDiff(sessionID: String) async throws -> GitDiffData {
        guard let baseURL = baseURL else {
            throw EggifyAPIError.notConfigured
        }

        let url = baseURL.appendingPathComponent("api/agent/sessions/\(sessionID)/git/diff")
        return try await performRequest(url: url, method: "GET", body: nil)
    }

    func getGitRemote(sessionID: String) async throws -> GitRemoteInfo {
        guard let baseURL = baseURL else {
            throw EggifyAPIError.notConfigured
        }

        let url = baseURL.appendingPathComponent("api/agent/sessions/\(sessionID)/git-remote")
        return try await performRequest(url: url, method: "GET", body: nil)
    }

    // MARK: - Error Types

    enum EggifyAPIError: LocalizedError {
        case notConfigured
        case invalidURL
        case invalidResponse
        case unauthorized
        case userRequired(String)
        case licenseRequired
        case notFound
        case decodingError(Error)
        case serverError(Int)
        case server(Int, String)
        case httpError(Int)
        case networkError(Error)

        var errorDescription: String? {
            switch self {
            case .notConfigured:
                return "No server configured. Sign in to a Eggify server first."
            case .invalidURL:
                return "Invalid URL"
            case .invalidResponse:
                return "Unexpected response from the server"
            case .unauthorized:
                return "Your session has expired. Sign in again."
            case .userRequired(let message):
                return message
            case .licenseRequired:
                return LicenseGate.message
            case .notFound:
                return "Not found on the server"
            case .decodingError:
                return "The server sent data this app could not read. Is the server up to date?"
            case .serverError(let code):
                return "Server error (\(code))"
            case .server(_, let message):
                return message
            case .httpError(let code):
                return "Request failed (\(code))"
            case .networkError(let error):
                if let urlError = error as? URLError {
                    switch urlError.code {
                    case .notConnectedToInternet: return "You're offline."
                    case .timedOut: return "The server took too long to answer."
                    case .cannotConnectToHost, .cannotFindHost: return "Can't reach the server."
                    case .serverCertificateUntrusted, .serverCertificateHasBadDate, .serverCertificateHasUnknownRoot, .secureConnectionFailed:
                        if let url = urlError.failingURL, let host = url.host,
                           CertificatePins.looksLikeRelayHost(host), CertificatePins.pin(for: url) == nil {
                            return "This is an Eggify relay address. Connect by scanning the \u{201C}Pair the iOS app\u{201D} QR code in Eggify on your Mac (Settings \u{2192} Remote access)."
                        }
                        return "The server's certificate isn't trusted on this device."
                    default: break
                    }
                }
                return error.localizedDescription
            }
        }
    }
}

// MARK: - Justfile Models

struct JustRecipe: Codable, Identifiable {
    let name: String
    let description: String
    let parameters: [String]?

    var id: String { name }
}

struct JustJob: Codable, Identifiable {
    let id: String
    let projectId: String
    let recipe: String
    let args: [String]?
    let status: String // "running", "completed", "failed"
    let exitCode: Int
    let output: String
    let startedAt: String
    let finishedAt: String?

    enum CodingKeys: String, CodingKey {
        case id
        case projectId = "project_id"
        case recipe, args, status
        case exitCode = "exit_code"
        case output
        case startedAt = "started_at"
        case finishedAt = "finished_at"
    }
}

// MARK: - Git Models

struct GitStatus: Codable {
    let branch: String?
    let ahead: Int?
    let behind: Int?
    let staged: [String]?
    let modified: [String]?
    let untracked: [String]?
    let deleted: [String]?
    let clean: Bool?
}

struct GitDiffFile: Codable {
    let path: String?
    let status: String? // "modified", "added", "deleted", "renamed"
    let additions: Int?
    let deletions: Int?
    let diff: String?
}

struct GitDiffStats: Codable {
    let filesChanged: Int?
    let additions: Int?
    let deletions: Int?
}

struct GitDiffData: Codable {
    let stats: GitDiffStats?
    let files: [GitDiffFile]?
}

struct GitRemoteInfo: Codable {
    let remote_url: String?
    let html_url: String?
    let owner: String?
    let repo: String?
}
