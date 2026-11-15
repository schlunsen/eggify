/**
 * "Needs you" events: agent questions and permission requests, for the toast
 * that tells you about sessions you are not looking at (NeedsYouToast.vue).
 *
 * The page-level WebSocket handlers are single-slot callbacks owned by the
 * agents page, so useAgentWebSocket also re-broadcasts these messages as a
 * window event that any component (on any page) can listen to.
 */

export const AGENT_ATTENTION_EVENT = 'eggify:agent-attention'

const ATTENTION_TYPES = new Set([
  'user_question',
  'user_question_timer',
  'user_question_resolved',
  'user_question_acknowledged',
  'user_questions_sync',
  'permission_request',
  'session_question_policy_changed',
  'question_policy_default_changed',
])

export function isAttentionMessage(type: unknown): boolean {
  return typeof type === 'string' && ATTENTION_TYPES.has(type)
}

/** Re-broadcast a server message as a window event. */
export function announceAgentAttention(message: Record<string, any>): void {
  if (typeof window === 'undefined' || !isAttentionMessage(message?.type)) return
  window.dispatchEvent(new CustomEvent(AGENT_ATTENTION_EVENT, { detail: message }))
}

export function clip(text: string, max: number): string {
  const t = (text || '').replace(/\s+/g, ' ').trim()
  return t.length > max ? `${t.slice(0, max - 1).trimEnd()}…` : t
}

/** What a question is about: its header, else its text. */
export function questionSubject(q: { header?: string; question?: string } | null | undefined): string {
  return clip(q?.header || q?.question || 'a question', 80)
}

/** A short detail of a permission request: the command, file or description. */
export function permissionDetail(p: { details?: any; description?: string; action?: string } | null | undefined): string {
  const d = p?.details
  if (d && typeof d === 'object') {
    const v = d.command ?? d.file_path ?? d.path ?? d.url ?? d.pattern
    if (typeof v === 'string' && v.trim()) return clip(v, 80)
  } else if (typeof d === 'string' && d.trim()) {
    return clip(d, 80)
  }
  return clip(p?.description || p?.action || '', 80)
}
