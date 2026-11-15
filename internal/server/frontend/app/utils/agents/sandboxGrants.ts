/**
 * Sandbox grants (backend: internal/server/agents/sandbox_grants.go,
 * docs/sandbox-grants.md): what a signed-in admin can give a sandboxed Claude
 * session at creation. The list, wording and availability come from
 * GET /api/agent/sandbox; only the ids and the remembered choice live here.
 */

export interface SandboxGrantInfo {
  id: string
  title: string
  description: string
  risk: string
  available: boolean
  /** Why it is unavailable ("No ssh-agent is running"). */
  reason?: string
}

export interface SandboxGrantPath {
  path: string
  write: boolean
}

export interface SandboxGrantChoice {
  grants: string[]
  paths: SandboxGrantPath[]
}

/** Mirrors maxSandboxGrantPaths in sandbox_grants.go. */
export const MAX_GRANT_PATHS = 10

/** localStorage key of the remembered choice, keyed by project id. */
export const GRANTS_STORAGE_KEY = 'eggify-sandbox-grants'

/** Titles for sessions listed before the availability API answered. */
const GRANT_TITLES: Record<string, string> = {
  ssh: 'SSH keys',
  github: 'GitHub',
  cloud: 'Cloud credentials',
  npm: 'npm login',
  localhost: 'Local servers',
}

export function grantTitle(id: string): string {
  return GRANT_TITLES[id] || id
}

function readAll(): Record<string, SandboxGrantChoice> {
  try {
    const raw = localStorage.getItem(GRANTS_STORAGE_KEY)
    const parsed = raw ? JSON.parse(raw) : {}
    return parsed && typeof parsed === 'object' ? parsed : {}
  } catch {
    return {}
  }
}

/** The choice remembered for a project, or null (never pre-check without one). */
export function readSavedGrants(projectKey: string): SandboxGrantChoice | null {
  if (!projectKey) return null
  const saved = readAll()[projectKey]
  if (!saved || typeof saved !== 'object') return null
  const grants = Array.isArray(saved.grants) ? saved.grants.filter((g) => typeof g === 'string') : []
  const paths = Array.isArray(saved.paths)
    ? saved.paths
        .filter((p) => p && typeof p.path === 'string' && p.path.trim())
        .map((p) => ({ path: p.path, write: p.write === true }))
        .slice(0, MAX_GRANT_PATHS)
    : []
  return { grants, paths }
}

export function saveGrants(projectKey: string, choice: SandboxGrantChoice): void {
  if (!projectKey) return
  try {
    const all = readAll()
    if (choice.grants.length === 0 && choice.paths.length === 0) delete all[projectKey]
    else all[projectKey] = choice
    localStorage.setItem(GRANTS_STORAGE_KEY, JSON.stringify(all))
  } catch {
    // Private window or blocked storage: the choice just isn't remembered.
  }
}

/** The create_session options for a choice ({} when nothing is granted). */
export function grantsPayload(choice: SandboxGrantChoice): { sandbox_grants?: string[]; sandbox_grant_paths?: SandboxGrantPath[] } {
  const out: { sandbox_grants?: string[]; sandbox_grant_paths?: SandboxGrantPath[] } = {}
  if (choice.grants.length > 0) out.sandbox_grants = [...choice.grants]
  const paths = choice.paths.map((p) => ({ path: p.path.trim(), write: p.write })).filter((p) => p.path)
  if (paths.length > 0) out.sandbox_grant_paths = paths.slice(0, MAX_GRANT_PATHS)
  return out
}

interface SessionWithGrants {
  options?: { sandbox_grants?: string[] | null; sandbox_grant_paths?: Array<{ path: string; write?: boolean }> | null } | null
}

/** "SSH keys, GitHub" for the Permissions panel; '' without grants. */
export function grantsSummary(session?: SessionWithGrants | null): string {
  const ids = session?.options?.sandbox_grants ?? []
  const names = ids.map(grantTitle)
  const n = session?.options?.sandbox_grant_paths?.length ?? 0
  if (n > 0) names.push(n === 1 ? '1 folder or file' : `${n} folders or files`)
  return names.join(', ')
}

export function grantPathsOf(session?: SessionWithGrants | null): SandboxGrantPath[] {
  return (session?.options?.sandbox_grant_paths ?? []).map((p) => ({ path: p.path, write: p.write === true }))
}
