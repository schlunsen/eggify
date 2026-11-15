<template>
  <TransitionGroup name="needs-you" tag="div" class="needs-you-stack" aria-live="polite">
    <div
      v-for="item in items"
      :key="item.key"
      class="needs-you-toast"
      role="status"
      data-testid="needs-you-toast"
    >
      <span class="needs-you-dot" :class="item.kind" aria-hidden="true" />
      <span class="needs-you-text">
        <strong class="needs-you-name">{{ item.sessionName }}</strong>
        <template v-if="item.kind === 'question'"> asks: {{ item.subject }}</template>
        <template v-else> wants to run {{ item.subject }}<template v-if="item.detail">: <code class="needs-you-detail">{{ item.detail }}</code></template></template>
        <span v-if="countdownOf(item)" class="needs-you-timer"> · auto-picks in {{ countdownOf(item) }}</span>
      </span>
      <NuxtLink
        :to="{ path: '/agents', query: { session: item.sessionId } }"
        class="needs-you-action"
        @click="remove(item.key)"
      >{{ item.kind === 'question' ? 'Open' : 'Review' }}</NuxtLink>
      <button type="button" class="needs-you-close" aria-label="Dismiss" @click="remove(item.key)">×</button>
    </div>
  </TransitionGroup>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import { useSessionStore } from '~/stores/session/sessionStore'
import { AGENT_ATTENTION_EVENT, clip, permissionDetail, questionSubject } from '~/utils/agents/needsYou'
import { formatCountdown, msUntil } from '~/utils/agents/questionPolicy'
import { desktopNotify } from '~/utils/desktop'

// Tells you when a session you are not looking at asks a question or wants
// approval for a tool (another session, another page, or a hidden tab). The
// card itself only shows on the agents page, for the session on screen.

interface NeedsYouItem {
  key: string
  id: string
  kind: 'question' | 'permission'
  sessionId: string
  sessionName: string
  subject: string
  detail?: string
  autoAnswerAt?: string | null
  held?: boolean
  requiresUser?: boolean
}

const MAX_ITEMS = 3
/** Permission requests have no "resolved" event for other tabs; let them go. */
const PERMISSION_TTL_MS = 120_000

const items = ref<NeedsYouItem[]>([])
const notified = new Set<string>()
// Questions answered for the user within this long get no toast.
const AUTO_ANSWER_GRACE_MS = 3000
const timers = new Map<string, ReturnType<typeof setTimeout>>()
const now = ref(Date.now())
let ticker: ReturnType<typeof setInterval> | null = null

const route = useRoute()
const sessionStore = useSessionStore()

const pageHidden = () => typeof document !== 'undefined' && document.visibilityState === 'hidden'
const pageUnfocused = () => typeof document !== 'undefined' && (pageHidden() || (typeof document.hasFocus === 'function' && !document.hasFocus()))

/** The session's own card is showing right now. */
function onScreen(sessionId: string): boolean {
  if (pageHidden()) return false
  const path = (route.path || '').replace(/\/+$/, '')
  return path === '/agents' && sessionStore.activeSessionId === sessionId
}

function sessionNameFor(sessionId: string, fromServer?: string): string {
  if (fromServer && fromServer.trim()) return clip(fromServer, 40)
  const s: any = (sessionStore.sessions || []).find((x: any) => x.id === sessionId)
  return clip(s?.avatarName || s?.options?.agent_name || `Session ${sessionId.slice(0, 8)}`, 40)
}

function isYolo(sessionId: string): boolean {
  const s: any = (sessionStore.sessions || []).find((x: any) => x.id === sessionId)
  return s?.options?.dangerously_skip_permissions === true || s?.options?.allow_dangerously_skip_permissions === true
}

function countdownOf(item: NeedsYouItem): string {
  if (item.kind !== 'question' || item.held || item.requiresUser) return ''
  const ms = msUntil(item.autoAnswerAt, now.value)
  return ms === null ? '' : formatCountdown(ms)
}

function remove(key: string) {
  items.value = items.value.filter(i => i.key !== key)
  const t = timers.get(key)
  if (t) { clearTimeout(t); timers.delete(key) }
}

function notifyDesktop(item: NeedsYouItem) {
  if (notified.has(item.key) || !pageUnfocused()) return
  notified.add(item.key)
  const body = item.kind === 'question'
    ? `Asks: ${item.subject}${countdownOf(item) ? ` (auto-picks in ${countdownOf(item)})` : ''}`
    : `Wants to run ${item.subject}${item.detail ? `: ${item.detail}` : ''}`
  desktopNotify(item.sessionName, body).catch(() => {})
}

function upsert(item: NeedsYouItem) {
  const existing = items.value.findIndex(i => i.key === item.key)
  if (existing >= 0) {
    items.value.splice(existing, 1, item)
  } else {
    items.value = [...items.value, item]
    while (items.value.length > MAX_ITEMS) remove(items.value[0]!.key)
  }
  if (item.kind === 'permission' && !timers.has(item.key)) {
    timers.set(item.key, setTimeout(() => remove(item.key), PERMISSION_TTL_MS))
  }
}

function onQuestion(m: any) {
  if (!m.session_id || !m.question_id) return
  const loop = m.kind === 'loop_session'
  const worker = m.kind === 'worker_session'
  const item: NeedsYouItem = {
    key: `q:${m.question_id}`,
    id: m.question_id,
    kind: 'question',
    sessionId: m.session_id,
    sessionName: sessionNameFor(m.session_id, m.session_name),
    subject: loop ? 'start a loop session?' : worker ? 'start a worker session?' : questionSubject(m),
    autoAnswerAt: m.auto_answer_at ?? null,
    held: !!m.held,
    requiresUser: !!m.requires_user,
  }
  // "Auto-pick now" (or a deadline about to pass): answered before anyone
  // could act on a toast or notification.
  if (answeredMomentarily(item)) return
  notifyDesktop(item)
  if (onScreen(m.session_id)) return
  upsert(item)
}

function answeredMomentarily(item: NeedsYouItem): boolean {
  if (item.requiresUser || item.held || !item.autoAnswerAt) return false
  const at = Date.parse(item.autoAnswerAt)
  return Number.isFinite(at) && at - Date.now() < AUTO_ANSWER_GRACE_MS
}

function onPermission(m: any) {
  const id = m.permission_id || m.request_id
  if (!m.session_id || !id || isYolo(m.session_id)) return
  const item: NeedsYouItem = {
    key: `p:${id}`,
    id,
    kind: 'permission',
    sessionId: m.session_id,
    sessionName: sessionNameFor(m.session_id, m.session_name),
    subject: clip(m.tool || 'a tool', 40),
    detail: permissionDetail(m) || undefined,
  }
  notifyDesktop(item)
  if (onScreen(m.session_id)) return
  upsert(item)
}

function onAttention(event: Event) {
  const m = (event as CustomEvent<any>).detail
  if (!m || typeof m !== 'object') return
  switch (m.type) {
    case 'user_question':
      onQuestion(m)
      break
    case 'permission_request':
      onPermission(m)
      break
    case 'user_question_timer': {
      const i = items.value.findIndex(x => x.key === `q:${m.question_id}`)
      if (i >= 0) items.value.splice(i, 1, { ...items.value[i]!, autoAnswerAt: m.auto_answer_at ?? null, held: !!m.held })
      break
    }
    case 'user_question_resolved':
    case 'user_question_acknowledged':
      if (m.question_id) remove(`q:${m.question_id}`)
      break
    case 'user_questions_sync': {
      const waiting = new Set<string>(Array.isArray(m.question_ids) ? m.question_ids : [])
      for (const i of [...items.value]) {
        if (i.kind === 'question' && i.sessionId === m.session_id && !waiting.has(i.id)) remove(i.key)
      }
      break
    }
  }
}

// Once you look at a session, its card takes over.
function dropOnScreen() {
  for (const i of [...items.value]) if (onScreen(i.sessionId)) remove(i.key)
}
watch(() => [route.fullPath, sessionStore.activeSessionId], dropOnScreen)

// A permission answered on the agents page leaves the store's pending list.
watch(() => sessionStore.permissions, (perms: Record<string, any[]>) => {
  for (const i of [...items.value]) {
    if (i.kind !== 'permission') continue
    const p = (perms?.[i.sessionId] || []).find((x: any) => x.id === i.id)
    if (p && p.status !== 'pending') remove(i.key)
  }
}, { deep: true })

const hasCountdown = computed(() => items.value.some(i => i.kind === 'question' && !i.held && !i.requiresUser && !!i.autoAnswerAt))
watch(hasCountdown, (active) => {
  if (ticker) { clearInterval(ticker); ticker = null }
  if (active) {
    now.value = Date.now()
    ticker = setInterval(() => { now.value = Date.now() }, 1000)
  }
}, { immediate: true })

const onVisibility = () => { if (!pageHidden()) dropOnScreen() }

onMounted(() => {
  window.addEventListener(AGENT_ATTENTION_EVENT, onAttention)
  document.addEventListener('visibilitychange', onVisibility)
})
onBeforeUnmount(() => {
  window.removeEventListener(AGENT_ATTENTION_EVENT, onAttention)
  document.removeEventListener('visibilitychange', onVisibility)
  if (ticker) clearInterval(ticker)
  for (const t of timers.values()) clearTimeout(t)
  timers.clear()
})
</script>

<style scoped>
.needs-you-stack {
  /* Top right, below the header: clear of the composer and the update pill */
  position: fixed;
  right: 16px;
  top: 64px;
  z-index: 10000;
  display: flex;
  flex-direction: column;
  gap: 8px;
  max-width: min(460px, calc(100vw - 32px));
  pointer-events: none;
}
.needs-you-toast {
  pointer-events: auto;
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 10px 12px;
  border-radius: 10px;
  background: var(--bg-primary);
  border: 1px solid var(--border-color);
  color: var(--text-primary);
  box-shadow: 0 8px 24px rgba(0, 0, 0, 0.18);
  font-size: 13px;
  line-height: 1.4;
}
.needs-you-dot {
  flex-shrink: 0;
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background: var(--accent-purple);
}
.needs-you-dot.permission { background: var(--status-warning, var(--accent-purple)); }
.needs-you-text {
  flex: 1;
  min-width: 0;
  color: var(--text-secondary);
  overflow-wrap: anywhere;
}
.needs-you-name { color: var(--text-primary); font-weight: 600; }
.needs-you-detail {
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 12px;
  color: var(--text-primary);
}
.needs-you-timer { font-variant-numeric: tabular-nums; }
.needs-you-action {
  flex-shrink: 0;
  color: var(--accent-purple);
  font-weight: 600;
  text-decoration: none;
  white-space: nowrap;
}
.needs-you-action:hover { text-decoration: underline; }
.needs-you-close {
  flex-shrink: 0;
  background: none;
  border: none;
  color: var(--text-secondary);
  font-size: 18px;
  line-height: 1;
  cursor: pointer;
  padding: 0 2px;
}
.needs-you-enter-active, .needs-you-leave-active { transition: opacity 0.2s, transform 0.2s; }
.needs-you-enter-from, .needs-you-leave-to { opacity: 0; transform: translateY(-8px); }
@media (prefers-reduced-motion: reduce) {
  .needs-you-enter-active, .needs-you-leave-active { transition: none; }
}
</style>
