/**
 * Video and audio files a session's agent made or showed
 * (GET /api/agent/sessions/:id/media), and the small rules the chat uses for
 * them: which live tool calls may have produced one (so the list is reloaded),
 * which tool call a file's player card sits under, and the list order.
 */

export type MediaKind = 'video' | 'audio'
export type MediaCompatibleState = 'running' | 'ready' | 'failed'

export interface MediaItem {
  id: string
  name: string
  title?: string
  /** Path for display (relative to the session's folder where possible) */
  path: string
  kind: MediaKind
  mime_type: string
  size: number
  /** RFC3339 */
  modified: string
  /** The agent presented it with the show_media tool */
  shown: boolean
  /** DB message id (not the live client message id) */
  message_id?: string | number
  /** The tool call that last named the file; its card sits under that call */
  tool_use_id?: string
  tool_use_ids?: string[]
  sequence?: number
  /** iPhone (Safari) can play the file as it is */
  ios_plays: boolean
  url: string
  /** A frame of the video (only when ffmpeg is installed) */
  poster_url?: string
  /** State of the H.264 / AAC copy made for iPhone */
  compatible?: MediaCompatibleState
  compatible_url?: string
  compatible_error?: string
}

export interface MediaListResponse {
  items: MediaItem[]
  ffmpeg: boolean
}

/** What the chat needs to put a player under the tool call that named a file. */
export interface SessionMediaLookup {
  sessionId: string
  ffmpeg: boolean
  /** tool_use id → the files whose card goes under that call */
  byToolUse: Map<string, MediaItem[]>
}

export const MEDIA_EXTENSIONS = [
  'mp4', 'm4v', 'mov', 'webm', 'mkv',
  'mp3', 'm4a', 'aac', 'wav', 'aif', 'aiff', 'flac', 'ogg', 'oga', 'opus',
] as const

// A file name ending in a media extension, not followed by more name characters
// ("out.mp4", "out.mp4\"", "out.mp4 &&"; not "out.mp4.part" or "x.mp3d").
const MEDIA_PATH_RE = new RegExp(`[\\w\\-.~/]\\.(?:${MEDIA_EXTENSIONS.join('|')})(?![\\w.-]*[\\w-])`, 'i')

/** True when the text names a file with a video or audio extension. */
export function mentionsMediaPath(text: string | null | undefined): boolean {
  return !!text && MEDIA_PATH_RE.test(text)
}

function stringifyInput(input: unknown): string {
  if (input == null) return ''
  if (typeof input === 'string') return input
  try { return JSON.stringify(input) } catch { return '' }
}

/** The show_media tool, under any MCP server prefix (mcp__eggify-tools__show_media). */
export function isShowMediaTool(name: string | null | undefined): boolean {
  return !!name && name.endsWith('show_media')
}

/**
 * True when a live message's tool calls may have made or named a media file,
 * so the session's media list should be reloaded.
 */
export function toolsMayTouchMedia(tools: Array<{ name?: string; input?: unknown } | null | undefined> | null | undefined): boolean {
  if (!Array.isArray(tools)) return false
  return tools.some(t => !!t && (isShowMediaTool(t.name) || mentionsMediaPath(stringifyInput(t.input))))
}

function modifiedMs(item: MediaItem): number {
  const t = Date.parse(item.modified)
  return Number.isFinite(t) ? t : 0
}

/** Newest first (by modification time, then by sequence, then by name). */
export function sortMedia(items: MediaItem[]): MediaItem[] {
  return [...items].sort((a, b) =>
    modifiedMs(b) - modifiedMs(a)
    || (b.sequence ?? 0) - (a.sequence ?? 0)
    || a.name.localeCompare(b.name))
}

/**
 * tool_use id → files for the inline cards. Each file shows once, under the
 * call that last named it (item.tool_use_id); files without one only show in
 * the Media tab. Within one call, files are in the list's order.
 */
export function mediaByToolUse(items: MediaItem[]): Map<string, MediaItem[]> {
  const map = new Map<string, MediaItem[]>()
  for (const item of items) {
    const id = item.tool_use_id
    if (!id) continue
    const list = map.get(id)
    if (list) list.push(item)
    else map.set(id, [item])
  }
  return map
}

/** Lower-case extension of a file name ("clip.WEBM" → "webm"), or ''. */
export function mediaExtension(name: string): string {
  const dot = name.lastIndexOf('.')
  return dot > 0 ? name.slice(dot + 1).toLowerCase() : ''
}

/** "1.4 MB" */
export function formatMediaSize(bytes: number): string {
  if (!Number.isFinite(bytes) || bytes < 0) return ''
  if (bytes < 1024) return `${bytes} B`
  const units = ['KB', 'MB', 'GB']
  let value = bytes / 1024
  let unit = 0
  while (value >= 1024 && unit < units.length - 1) { value /= 1024; unit++ }
  return `${value >= 10 ? Math.round(value) : value.toFixed(1)} ${units[unit]}`
}

/** True while some file's iPhone copy is being made (the list is polled then). */
export function anyConverting(items: MediaItem[]): boolean {
  return items.some(i => i.compatible === 'running')
}
