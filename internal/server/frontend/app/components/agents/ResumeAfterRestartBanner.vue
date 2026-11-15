<template>
  <!--
    Sessions that were working when Eggify last stopped (a quit or a signal,
    not an update: those continue by themselves). One click continues them
    the same way; Dismiss drops the offer. Signed-in users only, since
    continuing sends prompts (POST/DELETE need a user).
  -->
  <div v-if="visible" class="resume-banner" role="status">
    <span class="resume-banner__text">
      {{ count === 1 ? '1 session was' : `${count} sessions were` }} interrupted by the restart
    </span>
    <button type="button" class="resume-banner__primary" :disabled="busy" @click="continueAll">Continue</button>
    <button type="button" class="resume-banner__link" :disabled="busy" @click="dismiss">Dismiss</button>
    <span v-if="error" class="resume-banner__error">{{ error }}</span>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useAuthenticatedFetch } from '~/composables/useAuthenticatedFetch'
import { useAuth } from '~/composables/useAuth'

const props = defineProps<{ connected?: boolean }>()

const { fetchWithAuth } = useAuthenticatedFetch()
const { authEnabled, isAuthenticated } = useAuth()

const count = ref(0)
const busy = ref(false)
const error = ref<string | null>(null)

/** With user auth on, only a signed-in user can continue them. */
const signedIn = computed(() => !authEnabled.value || isAuthenticated.value)
const visible = computed(() => signedIn.value && count.value > 0)

async function load() {
  if (!signedIn.value) return
  try {
    const res = await fetchWithAuth('/api/agent/resume-after-restart')
    if (!res.ok) return
    const body = await res.json()
    count.value = Array.isArray(body?.sessions) ? body.sessions.length : 0
  } catch { /* offline: try again on reconnect */ }
}

async function act(method: 'POST' | 'DELETE') {
  busy.value = true
  error.value = null
  try {
    const res = await fetchWithAuth('/api/agent/resume-after-restart', { method })
    if (!res.ok) {
      error.value = res.status === 403 ? 'Sign in to continue these sessions.' : `Failed (${res.status})`
      return
    }
    count.value = 0
  } catch (e: any) {
    error.value = e?.message || 'Failed'
  } finally {
    busy.value = false
  }
}

const continueAll = () => act('POST')
const dismiss = () => act('DELETE')

onMounted(load)
// After a reconnect (the server restarted) and once signed in.
watch(() => props.connected, (now, before) => { if (now && !before) load() })
watch(signedIn, (now) => { if (now) load() })
</script>

<style scoped>
.resume-banner {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 8px 12px;
  position: absolute;
  top: 18px;
  left: 50%;
  transform: translateX(-50%);
  z-index: 41;
  width: max-content;
  max-width: calc(100% - 32px);
  padding: 8px 12px;
  box-shadow: 0 6px 20px rgba(0, 0, 0, 0.15);
  border: 1px solid var(--border-color);
  border-radius: 10px;
  background: var(--bg-secondary);
  color: var(--text-primary);
  font-size: 13px;
}
.resume-banner__text { flex: 1 1 auto; min-width: 0; }
.resume-banner__primary {
  padding: 4px 12px;
  border: 0;
  border-radius: 8px;
  background: var(--accent-purple);
  color: var(--bg-primary);
  font: inherit;
  font-weight: 600;
  cursor: pointer;
}
.resume-banner__link {
  padding: 4px 6px;
  border: 0;
  background: none;
  color: var(--text-secondary);
  font: inherit;
  cursor: pointer;
}
.resume-banner button:disabled { opacity: 0.6; cursor: default; }
.resume-banner__error { flex-basis: 100%; color: var(--status-error); font-size: 12px; }
</style>
