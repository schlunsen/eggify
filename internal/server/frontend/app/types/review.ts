// Review queue for memories and skills that agents wrote (migration 50).
// Agent-written items start pending and are kept out of every prompt until a
// user approves them.

import { shortenHome } from '~/utils/homeDir'

export type ReviewStatus = 'pending' | 'approved' | 'rejected'
export type ReviewKind = 'memory' | 'skill'

export interface PendingMemoryItem {
  memory: {
    id: string
    project_id: string
    memory_type: string
    title: string
    content: string
    tags: string
    importance: number
    created_by: string
    status: ReviewStatus
    created_at: string
  }
  created_by_session_id: string
  agent_authored: boolean
}

/** One file next to a skill's SKILL.md (the review covers all of them). */
export interface SkillFolderFile {
  path: string
  size: number
  executable?: boolean
  /** Link target; links are listed but never copied into the skill. */
  symlink?: string
}

/** The files of a skill discovered from a folder (SKILL.md excluded). */
export interface SkillFolder {
  files: SkillFolderFile[]
  total_bytes: number
  over_limit?: boolean
  problem?: string
}

export interface PendingSkillItem {
  /** Set for skills discovered from a folder. */
  folder?: SkillFolder
  skill: {
    id: string
    name: string
    description?: string
    scope: string
    body?: string
    /** SKILL.md frontmatter as JSON (hooks, allowed-tools, model, ...). */
    frontmatter_json?: string
    created_by: string
    status: ReviewStatus
    created_at: string
  }
  created_by_session_id: string
  agent_authored: boolean
}

export interface PendingCounts {
  memories: number
  skills: number
  total: number
  memories_by_project: Record<string, number>
}

/** One row of the review queue, whatever its kind. */
export interface ReviewQueueItem {
  id: string
  kind: ReviewKind
  title: string
  /** Memory content or skill body. */
  content: string
  /** Memory type or skill description. */
  subtitle: string
  importance?: number
  createdBy: string
  sessionId: string
  createdAt: string
  /** Skills from a folder: the other files the reviewer approves. */
  files?: SkillFolderFile[]
  filesTotalBytes?: number
  filesProblem?: string
  /** Skills: frontmatter settings beyond name/description (hooks first). */
  settings?: SkillSetting[]
}

/** One frontmatter setting of a skill, as the reviewer sees it. */
export interface SkillSetting {
  key: string
  value: string
  /** Hooks run commands without asking; allowed-tools skip approval. */
  risky: boolean
  note?: string
  /** For hooks: every shell command they run. */
  commands?: string[]
}

const RISKY_SETTINGS: Record<string, string> = {
  hooks: 'Runs these commands automatically while the skill is active.',
  'allowed-tools': 'These tools run without asking while the skill is active.',
  allowed_tools: 'These tools run without asking while the skill is active.',
  shell: 'Shell used for the skill\'s commands.'
}

/** Every "command" string inside a hooks setting. */
function hookCommands(v: unknown, out: string[] = []): string[] {
  if (Array.isArray(v)) v.forEach(x => hookCommands(x, out))
  else if (v && typeof v === 'object') {
    for (const [k, x] of Object.entries(v as Record<string, unknown>)) {
      if (k === 'command' && typeof x === 'string') out.push(x)
      else hookCommands(x, out)
    }
  }
  return out
}

function settingValue(v: unknown): string {
  if (typeof v === 'string') return v
  if (Array.isArray(v) && v.every(x => typeof x !== 'object')) return v.join(', ')
  return JSON.stringify(v, null, 2)
}

/**
 * The frontmatter settings a reviewer must see before approving: everything
 * except name and description, risky ones (hooks, allowed-tools) first.
 * Unreadable frontmatter is shown as one risky raw entry, never hidden.
 */
export function skillSettings(frontmatterJSON?: string): SkillSetting[] {
  if (!frontmatterJSON || !frontmatterJSON.trim()) return []
  let fm: unknown
  try {
    fm = JSON.parse(frontmatterJSON)
  } catch {
    return [{ key: 'frontmatter', value: frontmatterJSON, risky: true, note: 'Could not be read; check it in the file.' }]
  }
  if (!fm || typeof fm !== 'object' || Array.isArray(fm)) return []
  const out: SkillSetting[] = []
  for (const [key, v] of Object.entries(fm as Record<string, unknown>)) {
    if (key === 'name' || key === 'description' || v === null || v === undefined || v === '') continue
    const setting: SkillSetting = { key, value: settingValue(v), risky: key in RISKY_SETTINGS, note: RISKY_SETTINGS[key] }
    if (key === 'hooks') setting.commands = hookCommands(v)
    out.push(setting)
  }
  return out.sort((a, b) => Number(b.risky) - Number(a.risky))
}

/** Edits a reviewer makes before approving (Edit + Approve). */
export interface ReviewEdits {
  title?: string
  content?: string
  importance?: number
}

export function memoryToQueueItem(item: PendingMemoryItem): ReviewQueueItem {
  const m = item.memory
  return {
    id: m.id,
    kind: 'memory',
    title: m.title,
    content: m.content,
    subtitle: m.memory_type,
    importance: m.importance,
    createdBy: m.created_by,
    sessionId: item.created_by_session_id,
    createdAt: m.created_at
  }
}

export function skillToQueueItem(item: PendingSkillItem): ReviewQueueItem {
  const s = item.skill
  return {
    id: s.id,
    kind: 'skill',
    title: s.name,
    content: s.body || '',
    subtitle: s.description || '',
    createdBy: s.created_by,
    sessionId: item.created_by_session_id,
    createdAt: s.created_at,
    settings: skillSettings(s.frontmatter_json),
    ...(item.folder
      ? {
          files: item.folder.files || [],
          filesTotalBytes: item.folder.total_bytes || 0,
          filesProblem: item.folder.problem || undefined
        }
      : {})
  }
}

/** A byte count for people: 512 B, 1.5 KB, 2.0 MB. */
export function formatBytes(n: number): string {
  if (n < 1024) return `${n} B`
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`
  return `${(n / (1024 * 1024)).toFixed(1)} MB`
}

/** Request body for an approve call, in the shape the API expects. */
export function approveBody(kind: ReviewKind, edits?: ReviewEdits): Record<string, unknown> | undefined {
  if (!edits) return undefined
  const body: Record<string, unknown> = {}
  if (kind === 'memory') {
    if (edits.title !== undefined) body.title = edits.title
    if (edits.content !== undefined) body.content = edits.content
    if (edits.importance !== undefined) body.importance = edits.importance
  } else {
    if (edits.title !== undefined) body.description = edits.title
    if (edits.content !== undefined) body.body = edits.content
  }
  return Object.keys(body).length ? body : undefined
}

/** Human label for who wrote an item. */
export function describeCreator(createdBy: string): string {
  if (createdBy === 'api_key') return 'API key (no logged-in user)'
  if (createdBy.startsWith('agent:')) return 'Agent session'
  // Skills discovered from a SKILL.md file, which an agent may have written.
  const file = fileOrigin(createdBy)
  if (file !== null) return `From ${shortenHome(file)}`
  return 'User'
}

/** The SKILL.md path of a skill discovered from a file, else null. */
export function fileOrigin(createdBy: string): string | null {
  return createdBy.startsWith('file:') ? createdBy.slice('file:'.length) : null
}

/** Replace the server's home folder at the start of a path with ~ (see utils/homeDir.ts); other paths stay absolute. */
export { shortenHome }

/** Parent folder of a path ('/a/b/c' -> '/a/b'). */
function parentDir(path: string): string {
  const i = Math.max(path.lastIndexOf('/'), path.lastIndexOf('\\'))
  return i > 0 ? path.slice(0, i) : path
}

/**
 * The folder a discovered skill's folder lives in: for
 * ~/.claude/skills/careful/SKILL.md that is ~/.claude/skills.
 */
export function skillSourceFolder(createdBy: string): string | null {
  const file = fileOrigin(createdBy)
  return file === null ? null : parentDir(parentDir(file))
}

export interface FolderGroup {
  folder: string
  ids: string[]
}

/**
 * Skills discovered from files, grouped by the folder their skill folders
 * live in, in first-seen order. Only groups with at least minSize items.
 */
export function groupFileItemsByFolder(items: ReviewQueueItem[], minSize = 2): FolderGroup[] {
  const groups = new Map<string, string[]>()
  for (const item of items) {
    const folder = skillSourceFolder(item.createdBy)
    if (folder === null) continue
    const ids = groups.get(folder)
    if (ids) ids.push(item.id)
    else groups.set(folder, [item.id])
  }
  return [...groups.entries()]
    .filter(([, ids]) => ids.length >= minSize)
    .map(([folder, ids]) => ({ folder, ids }))
}

/** Where the items in a queue came from. */
export function queueOrigins(items: ReviewQueueItem[]): 'agent' | 'file' | 'mixed' {
  const files = items.filter(i => fileOrigin(i.createdBy) !== null).length
  if (files === 0) return 'agent'
  return files === items.length ? 'file' : 'mixed'
}
