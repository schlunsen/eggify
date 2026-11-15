// Types and pure helpers for the project Git page
// (GET /api/projects/:id/git/overview and /git/changes).

export interface GitCommit { sha: string, short: string, subject: string, author: string, at: number, refs?: string[] }
export interface GitDirty { staged: number, unstaged: number, untracked: number, conflicted: number }

export interface GitWorktree {
  path: string
  branch?: string
  head: string
  is_main: boolean
  locked: boolean
  prunable: boolean
  missing: boolean
  dirty?: GitDirty
  upstream?: string
  ahead: number
  behind: number
  base_ahead: number
  base_behind: number
  last_commit?: GitCommit
}

export interface GitBranch {
  name: string
  head: string
  subject: string
  author: string
  at: number
  upstream?: string
  upstream_gone?: boolean
  ahead: number
  behind: number
  base_ahead: number
  base_behind: number
  merged: boolean
  current: boolean
  worktree?: string
  is_default: boolean
}

export interface GitOverview {
  root: string
  current_branch: string
  default_branch: string
  remote?: string
  remote_web_url?: string
  worktrees: GitWorktree[]
  branches: GitBranch[]
  branches_capped?: boolean
  recent_commits: GitCommit[]
}

export interface GitWorktreeSession { id: string, name: string, status: string, updated_at: string }

export interface GitOverviewResponse {
  is_repo: boolean
  error?: string
  overview?: GitOverview
  sessions_by_worktree?: Record<string, GitWorktreeSession[]>
}

export interface GitChangeFile {
  path: string
  old_path?: string
  status: 'added' | 'modified' | 'deleted' | 'renamed' | 'copied' | 'typechange' | 'conflicted'
  additions: number
  deletions: number
  binary?: boolean
  size?: number
  staged?: boolean
  unstaged?: boolean
  untracked?: boolean
  truncated?: boolean
  omitted?: boolean
  diff?: string
}

export interface GitChanges {
  scope: 'uncommitted' | 'branch'
  base?: string
  ref?: string
  stats: { files_changed: number, additions: number, deletions: number }
  files: GitChangeFile[]
  commits?: GitCommit[]
}

export type GitScope = 'uncommitted' | 'branch'

/** What the changes panel shows: a checkout, a branch, or a branch checked out in a checkout. */
export interface GitSelection { branch?: string, worktree?: string }

export function dirtyTotal(d?: GitDirty | null): number {
  return d ? d.staged + d.unstaged + d.untracked + d.conflicted : 0
}

/** "2 staged · 3 modified · 1 new" or "Clean". */
export function dirtySummary(d?: GitDirty | null): string {
  if (!d) return ''
  const parts: string[] = []
  if (d.conflicted) parts.push(`${d.conflicted} conflicted`)
  if (d.staged) parts.push(`${d.staged} staged`)
  if (d.unstaged) parts.push(`${d.unstaged} modified`)
  if (d.untracked) parts.push(`${d.untracked} new`)
  return parts.length ? parts.join(' · ') : 'Clean'
}

/** A path for display: relative to the project folder when inside it, else with ~ for the home folder. */
export function displayPath(path: string, root: string, home?: string): string {
  if (path === root) return '.'
  if (root && path.startsWith(root + '/')) return path.slice(root.length + 1)
  if (home && (path === home || path.startsWith(home + '/'))) return '~' + path.slice(home.length)
  return path
}

export type BranchFilter = 'active' | 'merged' | 'all'

/** Active: not merged into the default branch, the default branch itself, or checked out in a worktree. */
export function filterBranches(branches: GitBranch[], filter: BranchFilter, query: string): GitBranch[] {
  const q = query.trim().toLowerCase()
  return branches.filter((b) => {
    if (q && !b.name.toLowerCase().includes(q) && !b.subject.toLowerCase().includes(q)) return false
    if (filter === 'active') return b.is_default || !b.merged || !!b.worktree
    if (filter === 'merged') return b.merged
    return true
  })
}

/** The scopes a selection can show, in tab order. */
export function scopesFor(sel: GitSelection, defaultBranch: string): GitScope[] {
  const scopes: GitScope[] = []
  if (sel.worktree) scopes.push('uncommitted')
  if (sel.branch && sel.branch !== defaultBranch) scopes.push('branch')
  return scopes
}

/**
 * Where the page opens: the main checkout when it has uncommitted work, else
 * the first checkout with uncommitted work, else the current branch's commits
 * on top of the default branch, else the main checkout.
 */
export function defaultSelection(ov: GitOverview): { sel: GitSelection, scope: GitScope } {
  const main = ov.worktrees.find(w => w.is_main) || ov.worktrees[0]
  const asSel = (w: GitWorktree): GitSelection => ({ worktree: w.path, branch: w.branch || undefined })
  if (main && !main.missing && dirtyTotal(main.dirty) > 0) return { sel: asSel(main), scope: 'uncommitted' }
  const dirty = ov.worktrees.find(w => !w.missing && dirtyTotal(w.dirty) > 0)
  if (dirty) return { sel: asSel(dirty), scope: 'uncommitted' }
  if (main?.branch && main.branch !== ov.default_branch && main.base_ahead > 0) return { sel: asSel(main), scope: 'branch' }
  if (main) return { sel: asSel(main), scope: 'uncommitted' }
  return { sel: {}, scope: 'uncommitted' }
}

/** Selection for a branch row: its checkout too, when it is checked out. */
export function branchSelection(b: GitBranch): GitSelection {
  return { branch: b.name, worktree: b.worktree || undefined }
}

export function sameSelection(a: GitSelection, b: GitSelection): boolean {
  return (a.branch || '') === (b.branch || '') && (a.worktree || '') === (b.worktree || '')
}

/** Query string for /git/changes. */
export function changesQuery(sel: GitSelection, scope: GitScope, base: string, path?: string): string {
  const q = new URLSearchParams()
  q.set('scope', scope)
  if (scope === 'uncommitted' && sel.worktree) q.set('worktree', sel.worktree)
  if (scope === 'branch') {
    q.set('ref', sel.branch || '')
    if (base) q.set('base', base)
  }
  if (path) q.set('path', path)
  return q.toString()
}

/** Changes the page must reload for: head moved or the dirty counts changed. */
export function selectionSignature(ov: GitOverview | null, sel: GitSelection): string {
  if (!ov) return ''
  const wt = sel.worktree ? ov.worktrees.find(w => w.path === sel.worktree) : undefined
  const br = sel.branch ? ov.branches.find(b => b.name === sel.branch) : undefined
  const d = wt?.dirty
  return [wt?.head, d && `${d.staged}/${d.unstaged}/${d.untracked}/${d.conflicted}`, br?.head, ov.default_branch].join('|')
}

/** Splits "src/a/b.ts" into folder "src/a/" and name "b.ts". */
export function splitPath(path: string): { dir: string, name: string } {
  const i = path.lastIndexOf('/')
  return i < 0 ? { dir: '', name: path } : { dir: path.slice(0, i + 1), name: path.slice(i + 1) }
}

export function formatBytes(n?: number): string {
  if (!n) return ''
  if (n < 1024) return `${n} B`
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(n < 10 * 1024 ? 1 : 0)} KB`
  return `${(n / 1024 / 1024).toFixed(1)} MB`
}

/** ISO string for relativeTime() from unix seconds. */
export function unixToIso(at: number): string {
  return at ? new Date(at * 1000).toISOString() : ''
}
