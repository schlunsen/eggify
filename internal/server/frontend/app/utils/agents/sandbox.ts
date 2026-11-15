/**
 * Sandboxed sessions (backend: internal/server/agents/sandbox.go).
 *
 * A sandboxed session runs Bash inside the Claude Code sandbox (or a Codex
 * permission profile): Eggify's secrets are unreadable and network egress is
 * limited to an allowlist. Sessions that run on Eggify's OpenAI-compatible loop
 * execute tools in Eggify's own process and cannot be sandboxed.
 */

export type SandboxSupport = 'supported' | 'unsupported' | 'depends'

/** Providers that run on the Claude CLI or the Codex CLI. */
const SANDBOX_PROVIDERS = new Set(['claude', 'anthropic', 'deepseek', 'glm', 'kimi', 'codex'])

/** Providers that always use the OpenAI-compatible loop. */
const DIRECT_PROVIDERS = new Set(['openai', 'ollama', 'openrouter', 'lmstudio', 'litellm'])

/**
 * Whether a provider can run sandboxed. "custom" depends on its URL: an
 * Anthropic-compatible endpoint runs on the Claude CLI, anything else on the
 * OpenAI-compatible loop (the backend refuses those prompts).
 */
export function sandboxSupportForProvider(provider?: string | null): SandboxSupport {
  const p = (provider || 'claude').trim().toLowerCase()
  if (SANDBOX_PROVIDERS.has(p)) return 'supported'
  if (DIRECT_PROVIDERS.has(p)) return 'unsupported'
  return 'depends'
}

/** Mirrors domainPattern in sandbox.go. */
const DOMAIN_RE = /^(\*\.)?([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z][a-z0-9-]{0,62}$/

export function isValidSandboxDomain(domain: string): boolean {
  return DOMAIN_RE.test(domain.trim().toLowerCase().replace(/\.$/, ''))
}

/**
 * Parses the allowlist editor text (one domain per line, commas or spaces also
 * accepted; "#" starts a comment). Returns normalised, de-duplicated domains
 * and the entries that are not valid host names.
 */
export function parseSandboxDomains(text: string): { domains: string[]; invalid: string[] } {
  const seen = new Set<string>()
  const domains: string[] = []
  const invalid: string[] = []
  for (const rawLine of text.split('\n')) {
    const line = rawLine.replace(/#.*$/, '')
    for (const raw of line.split(/[\s,]+/)) {
      const d = raw.trim().toLowerCase().replace(/\.$/, '')
      if (!d) continue
      if (!isValidSandboxDomain(d)) {
        invalid.push(raw.trim())
        continue
      }
      if (!seen.has(d)) {
        seen.add(d)
        domains.push(d)
      }
    }
  }
  domains.sort()
  return { domains, invalid }
}

/** True when the session was created in Sandboxed mode. */
export function isSessionSandboxed(session?: { options?: { sandboxed?: boolean | null } | null } | null): boolean {
  return session?.options?.sandboxed === true
}
