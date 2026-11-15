<template>
  <!--
    The session waits for the Claude usage limit to reset and then continues by
    itself (server agents/usage_limit.go). Resume now / Don't continue send or
    stop a prompt, so they need a signed-in user (403 user_required otherwise).
  -->
  <div v-if="wait" class="limit-wait-banner" role="status" data-testid="limit-wait-banner">
    <span class="limit-wait-icon" aria-hidden="true">⏸</span>
    <span class="limit-wait-text">
      <strong>{{ label }}</strong>
      <span class="limit-wait-sub">{{ detail }}</span>
    </span>
    <span v-if="signedIn" class="limit-wait-actions">
      <button type="button" class="limit-wait-btn primary" :disabled="busy" @click="act('resume')">Resume now</button>
      <button type="button" class="limit-wait-btn" :disabled="busy" @click="act('cancel')">Don't continue</button>
    </span>
    <span v-if="error" class="limit-wait-error" role="alert">{{ error }}</span>
  </div>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useAuthenticatedFetch } from '~/composables/useAuthenticatedFetch'
import { useAuth } from '~/composables/useAuth'
import { useLoopClock } from '~/utils/agents/scheduledLoop'
import { hasLimitWait, limitWaitDetail, limitWaitLabel, type LimitWait } from '~/utils/agents/limitWait'

const props = defineProps<{
  sessionId: string
  limitWait?: LimitWait | null
}>()

const { fetchWithAuth } = useAuthenticatedFetch()
const { authEnabled, isAuthenticated } = useAuth()
const now = useLoopClock()

const busy = ref(false)
const error = ref<string | null>(null)
/** Hidden right after an action, until the server's update arrives. */
const dismissedFor = ref<string | null>(null)

const signedIn = computed(() => !authEnabled.value || isAuthenticated.value)
const wait = computed(() => {
  const w = props.limitWait
  if (!hasLimitWait(w) || dismissedFor.value === `${props.sessionId}|${w.since}|${w.resume_at}`) return null
  return w
})
const label = computed(() => limitWaitLabel(wait.value, now.value).replace(/^Usage limit · resumes/, 'Continues'))
const detail = computed(() => limitWaitDetail(wait.value))

watch(() => props.sessionId, () => { error.value = null })

async function act(kind: 'resume' | 'cancel') {
  const w = wait.value
  if (!w) return
  busy.value = true
  error.value = null
  try {
    const base = `/api/agent/sessions/${encodeURIComponent(props.sessionId)}/limit-wait`
    const res = kind === 'resume'
      ? await fetchWithAuth(`${base}/resume`, { method: 'POST' })
      : await fetchWithAuth(base, { method: 'DELETE' })
    if (!res.ok && res.status !== 404) {
      error.value = res.status === 403 ? 'Sign in to change this.' : `Failed (${res.status})`
      return
    }
    dismissedFor.value = `${props.sessionId}|${w.since}|${w.resume_at}`
  } catch (e: any) {
    error.value = e?.message || 'Failed'
  } finally {
    busy.value = false
  }
}
</script>

<style scoped>
.limit-wait-banner {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 8px 12px;
  margin: 8px 16px 0;
  padding: 8px 12px;
  border: 1px solid color-mix(in srgb, var(--accent-orange, #c2410c) 40%, transparent);
  border-radius: 10px;
  background: color-mix(in srgb, var(--accent-orange, #c2410c) 8%, var(--bg-secondary));
  color: var(--text-primary);
  font-size: 13px;
}
.limit-wait-icon { font-size: 15px; }
.limit-wait-text { flex: 1 1 260px; display: flex; flex-direction: column; gap: 2px; min-width: 0; }
.limit-wait-sub { color: var(--text-secondary); font-size: 12px; }
.limit-wait-actions { display: flex; gap: 8px; }
.limit-wait-btn {
  padding: 4px 10px;
  border: 1px solid var(--border-color);
  border-radius: 8px;
  background: transparent;
  color: var(--text-primary);
  font: inherit;
  cursor: pointer;
}
.limit-wait-btn.primary {
  border-color: transparent;
  background: var(--accent-orange, #c2410c);
  color: var(--bg-primary);
  font-weight: 600;
}
.limit-wait-btn:disabled { opacity: 0.6; cursor: default; }
.limit-wait-error { flex-basis: 100%; color: var(--status-error); font-size: 12px; }
</style>
