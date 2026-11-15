/**
 * Pure helpers for the Projects page and the new-project dialog.
 * The server (internal/server/handlers/project_setup.go) is the authority on
 * paths and clone URLs; these only derive friendly defaults and format data.
 */

import { homeDir, shortenHome } from '../homeDir'

export type ProjectSetupMode = 'existing' | 'clone' | 'empty'
export type ProjectSort = 'recent' | 'name'

/** The three ways to add a project: the dialog's first step and the first-run nudge. */
export const PROJECT_START_OPTIONS: { mode: ProjectSetupMode, icon: string, title: string, desc: string }[] = [
  { mode: 'existing', icon: 'folderOpen', title: 'Open an existing folder', desc: 'A project you already have on this computer.' },
  { mode: 'clone', icon: 'download', title: 'Clone a repository', desc: 'Paste a git URL or owner/repo and Eggify clones it for you.' },
  { mode: 'empty', icon: 'folderPlus', title: 'Start a new empty project', desc: 'Create a fresh folder, optionally with git.' },
]

/** Colors offered in the dialog (a subset of the server's palette). */
export const PROJECT_COLORS = [
  '#ef4444', '#f97316', '#eab308', '#22c55e', '#14b8a6',
  '#0ea5e9', '#6366f1', '#8b5cf6', '#d946ef', '#f43f5e',
]

export function randomProjectColor(): string {
  return PROJECT_COLORS[Math.floor(Math.random() * PROJECT_COLORS.length)]!
}

/** /Users/me/code/app → ~/code/app when home is /Users/me. */
export function shortenPath(path: string, home?: string | null): string {
  return shortenHome(path, home ?? homeDir.value)
}

/** Last segment of a path ("~/code/my-app/" → "my-app"). */
export function folderNameFromPath(path: string): string {
  const trimmed = (path || '').trim().replace(/[\\/]+$/, '')
  const seg = trimmed.split(/[\\/]/).pop() || ''
  return seg === '~' ? '' : seg
}

/** "My new App!" → "my-new-app": a folder name the server accepts. */
export function folderNameFromProjectName(name: string): string {
  return (name || '')
    .normalize('NFKD')
    .replace(/[̀-ͯ]/g, '')
    .toLowerCase()
    .replace(/[^a-z0-9._-]+/g, '-')
    .replace(/\.{2,}/g, '.')
    .replace(/^[-.]+|[-.]+$/g, '')
    .slice(0, 80)
}

/** Mirrors the server's folder-name rule, for inline validation. */
export function folderNameError(name: string): string | null {
  if (!name) return 'Enter a folder name'
  if (name.length > 100) return 'Folder name is too long'
  if (name.includes('..')) return 'Folder name must not contain ".."'
  if (!/^[A-Za-z0-9_][A-Za-z0-9 ._-]*$/.test(name) || /[ .]$/.test(name)) {
    return 'Use letters, numbers, spaces, dots, dashes and underscores'
  }
  return null
}

export interface RepoGuess {
  name: string
  /** owner/repo for GitHub shorthand or a recognisable URL. */
  slug?: string
}

/**
 * Guesses the repository name from what the user typed, for auto-filling the
 * project name and folder. Returns null when it does not look like a repo.
 */
export function guessRepo(input: string): RepoGuess | null {
  const s = (input || '').trim().replace(/\/+$/, '')
  if (!s) return null
  let m = s.match(/^([A-Za-z0-9][A-Za-z0-9._-]*)\/([A-Za-z0-9._-]+?)(?:\.git)?$/)
  if (m && !m[1]!.includes('.')) return { name: m[2]!, slug: `${m[1]}/${m[2]}` }
  m = s.match(/^[A-Za-z0-9._-]+@[A-Za-z0-9.-]+:([A-Za-z0-9._/-]+?)(?:\.git)?$/)
  if (m) {
    const parts = m[1]!.split('/')
    return { name: parts[parts.length - 1]!, slug: parts.slice(-2).join('/') }
  }
  try {
    const u = new URL(s.includes('://') ? s : `https://${s}`)
    const parts = u.pathname.split('/').filter(Boolean)
    if (parts.length < 2) return null
    const name = parts[parts.length - 1]!.replace(/\.git$/, '')
    if (!name) return null
    return { name, slug: parts.slice(-2).join('/').replace(/\.git$/, '') }
  } catch {
    return null
  }
}

/** Joins a parent folder and a child name for display. */
export function joinPath(parent: string, name: string): string {
  const p = (parent || '').trim().replace(/\/+$/, '')
  if (!name) return p
  return `${p || ''}/${name}`
}

/** "3 minutes ago"-style relative time; '' for missing/invalid input. */
export function relativeTime(iso: string | undefined | null, now: number = Date.now()): string {
  if (!iso) return ''
  const t = parseTimestamp(iso)
  if (!Number.isFinite(t)) return ''
  const sec = Math.max(0, Math.round((now - t) / 1000))
  if (sec < 45) return 'just now'
  const min = Math.round(sec / 60)
  if (min < 60) return `${min} min ago`
  const hr = Math.round(min / 60)
  if (hr < 24) return `${hr} h ago`
  const day = Math.round(hr / 24)
  if (day < 7) return day === 1 ? 'yesterday' : `${day} days ago`
  const wk = Math.round(day / 7)
  if (day < 31) return wk === 1 ? 'last week' : `${wk} weeks ago`
  const mo = Math.round(day / 30)
  if (mo < 12) return mo === 1 ? 'last month' : `${mo} months ago`
  const yr = Math.round(day / 365)
  return yr <= 1 ? 'last year' : `${yr} years ago`
}

/** SQLite timestamps come as "2026-01-02 03:04:05" (UTC) or RFC 3339. */
export function parseTimestamp(s: string): number {
  if (!s) return NaN
  let v = s.trim()
  if (/^\d{4}-\d{2}-\d{2} \d{2}:\d{2}/.test(v)) v = v.replace(' ', 'T')
  if (/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}(:\d{2}(\.\d+)?)?$/.test(v)) v += 'Z'
  return Date.parse(v)
}

export interface ProjectListItem {
  id: string
  name: string
  path: string
  display_path?: string
  description?: string
  color?: string
  created_at?: string
  updated_at?: string
  last_activity?: string
  session_count?: number
  running_count?: number
  missing?: boolean
  git?: { is_repo: boolean, branch?: string, dirty?: boolean } | null
  /** Logo found in the folder (app icon, favicon, logo file), served by the API. */
  icon_url?: string
  /** package.json description or the README's first paragraph. */
  summary?: string
}

/** Case-insensitive match of every search term against name, path, description and branch. */
export function filterProjects<T extends ProjectListItem>(list: T[], query: string): T[] {
  const terms = (query || '').toLowerCase().trim().split(/\s+/).filter(Boolean)
  if (!terms.length) return list
  return list.filter((p) => {
    const hay = `${p.name} ${p.path} ${p.display_path || ''} ${p.description || ''} ${p.summary || ''} ${p.git?.branch || ''}`.toLowerCase()
    return terms.every(t => hay.includes(t))
  })
}

function activityKey(p: ProjectListItem): number {
  const t = parseTimestamp(p.last_activity || '')
  if (Number.isFinite(t)) return t
  const c = parseTimestamp(p.created_at || '')
  return Number.isFinite(c) ? c : 0
}

/**
 * recent: running projects first, then most recent session activity (falling
 * back to creation time). name: alphabetical, natural number order.
 */
export function sortProjects<T extends ProjectListItem>(list: T[], sort: ProjectSort): T[] {
  const out = [...list]
  if (sort === 'name') {
    return out.sort((a, b) => a.name.localeCompare(b.name, undefined, { sensitivity: 'base', numeric: true }))
  }
  return out.sort((a, b) => {
    const ra = (a.running_count || 0) > 0 ? 1 : 0
    const rb = (b.running_count || 0) > 0 ? 1 : 0
    if (ra !== rb) return rb - ra
    return activityKey(b) - activityKey(a)
  })
}

/** Initials for a project avatar ("my-cool app" → "MC"). */
export function projectInitials(name: string): string {
  const words = (name || '?').replace(/[-_.]+/g, ' ').trim().split(/\s+/).filter(Boolean)
  if (!words.length) return '?'
  if (words.length === 1) return words[0]!.slice(0, 2).toUpperCase()
  return (words[0]![0]! + words[1]![0]!).toUpperCase()
}
