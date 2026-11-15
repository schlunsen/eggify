# Codex session network access

Eggify's Terminal starts a shell directly. A sandboxed Codex session instead
uses the `eggify_sandboxed` permission profile. Its command networking is off
by default, and `approvalPolicy="never"` prevents tools from requesting an
escape from that policy. Thus SSH working in Terminal does not imply that a
Codex command can resolve a host, connect to SSH, clone a repository, or install
packages.

## Enable networking for one session

Rebuild and run the updated checkout first: `just build` for the web/CLI app.
For the Wails desktop app, run `just build-frontend` followed by
`just desktop-wails-build` (see [desktop setup](desktop-wails.md)), then open the
new app. An already-running older binary will not show the new control.

1. Sign in to Eggify as an admin (or its only user). If user authentication is
   disabled, enable it and sign in first; an API key alone cannot authorize this.
2. Open **New Session**, select **Codex**, and choose the project directory (required:
   a sandboxed Codex session without one is refused).
3. Expand **Session settings** and enable **Filesystem sandbox**.
4. Check **Allow unrestricted network for this session**, then create the session.
5. The session's **Permissions** section shows **Network unrestricted** or
   **Network off**, separately from its filesystem sandbox status.

This permits direct command networking to any host, including local and private
services. Eggify applies **no domain allowlist** to this option. Existing host
firewalls and administrator-managed Codex restrictions may still limit access.
The domain list under **Settings → Sandbox** is for Claude sessions only.

Network access does not grant unrestricted filesystem access. The same protected
paths remain denied or read-only, writes remain limited to the workspace and
Codex's temporary directories, and read-only sessions stay read-only. Provider
and Eggify credential environment variables are excluded/cleared for command
subprocesses. The approval policy stays `never`, including in YOLO sessions
with the filesystem sandbox enabled.

SSH authentication still needs credentials available under those protections;
this option does not grant protected keys or SSH-agent socket access. Package
managers may need a workspace-local cache (for example, `npm install --cache
./.npm-cache`) if their normal cache lies outside the writable roots. Install
scripts run with the same session permissions.

## Existing sessions and authorization

The choice is fixed when creating the session. **Create a new session to enable
or disable networking**; stop the old session if it is still running commands.
Reopening a tab, restarting Eggify, or resuming the underlying Codex thread keeps
the stored choice. Sessions created before this feature have no network grant.
Handovers (including automatic handoffs) and other new sessions start with the
grant off. It is not a sticky creation-form preference or a global default.

The WebSocket creation handler requires a verified user principal with admin
rights even if legacy authentication enforcement is off. A server-only,
non-serialized authorization flag gates new grants in the session manager.
Generic session-option updates cannot change the grant; the REST endpoint
returns an error directing callers to create a new session. Both thread start
and resume explicitly select the permission profile and replace runtime workspace
roots with the session's project/worktree directory, without `danger-full-access`.

## Codex support and verification

The installed CLI examined for this change was **0.162.1**. Its generated
app-server schema supports `permissions` on both `thread/start` and
`thread/resume`, mutually exclusive with `sandbox`. Its sandbox command accepts
the named profile and separate `network.enabled` setting. `codex features list`
reported `network_proxy` as experimental and disabled.

[OpenAI's configuration reference](https://learn.chatgpt.com/docs/config-file/config-reference)
documents that `network.enabled` permits direct command networking but does not
start the network proxy. Domain restrictions require that proxy (or enabled
administrator-managed networking). Eggify does not configure a proxy for this
direct SSH/Git/package workflow, so this feature makes no domain-restriction
claim. It changes network permission, not the filesystem profile.

Focused checks:

```sh
go test ./internal/server/agents -run 'TestCodexNetwork|TestCodexSandbox|TestSandboxedOptions|TestSandboxPreferences'
go test ./internal/server/handlers -run 'TestSandboxSettings|TestSettings_ReservedSandboxKeys|TestSessionOptions_Network'
cd internal/server/frontend
npm exec vitest run app/__tests__/components/SandboxToggle.test.ts app/__tests__/components/CreateSessionQuestionPolicy.test.ts app/__tests__/components/SessionMetrics.test.ts
```

On a macOS/Linux host that permits launching the Codex OS sandbox, from the
repository root, with the checkout outside temporary directories (Codex permits
temporary-file writes independently of the workspace boundary):

```sh
EGGIFY_CODEX_SANDBOX_PROBE=1 go test ./internal/server/agents -run '^TestCodexNetworkOS$' -v
```

The OS test requires Codex, Python, OpenSSH and npm. It uses synthetic secrets,
a local HTTP package server, and a local SSH banner server. It checks networking
off/on, a real npm package download/install, SSH transport, workspace writes,
and denied secret reads/protected and out-of-workspace writes. It does not log
into a remote host, access production, use real credentials, or invoke a model.
An inability to launch the sandbox is a failure when this test is explicitly
enabled, not a successful security check.
