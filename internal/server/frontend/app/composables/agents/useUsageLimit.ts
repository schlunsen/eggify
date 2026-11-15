/**
 * The account's latest usage reports (GET /api/agent/usage-limit), shared by
 * every component that shows them: Claude's rate-limit report, and Codex's
 * plan usage while some component asks for it (`wantCodex`), so installs that
 * never open a Codex session never start codex for it. Polled every minute
 * while one is mounted and the tab is visible.
 */
import { onBeforeUnmount, onMounted, ref, watch, type Ref } from 'vue'
import { useAuthenticatedFetch } from '~/composables/useAuthenticatedFetch'
import type { CodexUsageInfo, UsageLimitInfo } from '~/utils/agents/limitWait'

const POLL_MS = 60_000

const info = ref<UsageLimitInfo | null>(null)
const codexInfo = ref<CodexUsageInfo | null>(null)
// How many mounted components want Codex's usage.
const codexWanted = ref(0)
let users = 0
let timer: ReturnType<typeof setInterval> | null = null
let load: (() => Promise<void>) | null = null

/** Claude's report. Pass `wantCodex` (true while a Codex session is shown) to also get Codex's. */
export function useUsageLimit(wantCodex?: () => boolean): Ref<UsageLimitInfo | null> {
  const { fetchWithAuth } = useAuthenticatedFetch()
  let wanting = false
  const setWanting = (want: boolean) => {
    if (want === wanting) return
    wanting = want
    codexWanted.value += want ? 1 : -1
    if (want && !codexInfo.value) load?.()
  }
  onMounted(() => {
    if (wantCodex) watch(wantCodex, want => setWanting(!!want), { immediate: true })
    if (users++ > 0) return
    load = async () => {
      if (typeof document !== 'undefined' && document.hidden) return
      try {
        const res = await fetchWithAuth(codexWanted.value > 0 ? '/api/agent/usage-limit?codex=1' : '/api/agent/usage-limit')
        if (!res.ok) return
        const body = await res.json()
        info.value = body?.limit ?? null
        if ('codex' in (body || {})) codexInfo.value = body.codex ?? null
      } catch { /* offline: next poll */ }
    }
    load()
    timer = setInterval(() => { load?.() }, POLL_MS)
  })
  onBeforeUnmount(() => {
    setWanting(false)
    if (--users === 0 && timer) { clearInterval(timer); timer = null; load = null }
  })
  return info
}

/** Codex's plan usage, filled while some component passes `wantCodex` to useUsageLimit. */
export function useCodexUsage(): Ref<CodexUsageInfo | null> {
  return codexInfo
}
