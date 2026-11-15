<template>
  <div class="devices" data-testid="signed-in-devices">
    <p v-if="error" class="devices__error" role="alert">{{ error }}</p>
    <p v-else-if="loading && !sessions.length" class="devices__empty">Loading…</p>
    <p v-else-if="!sessions.length" class="devices__empty">No signed-in devices.</p>
    <ul v-else class="devices__list">
      <li v-for="s in sessions" :key="s.id" class="devices__item" data-testid="device-row">
        <span class="devices__icon" aria-hidden="true">
          <svg v-if="isPhone(s)" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
            <rect x="6" y="2" width="12" height="20" rx="2.5" /><path d="M11 18h2" />
          </svg>
          <svg v-else width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
            <rect x="3" y="4" width="18" height="12" rx="2" /><path d="M8 20h8M12 16v4" />
          </svg>
        </span>
        <div class="devices__text">
          <div class="devices__name">
            {{ s.device_name }}
            <span v-if="s.current" class="devices__tag devices__tag--current">This device</span>
            <span class="devices__tag">{{ viaLabel(s.created_via) }}</span>
          </div>
          <div class="devices__meta">
            Signed in {{ ago(s.created_at) }}<template v-if="s.last_used_at"> · last used {{ ago(s.last_used_at) }}</template>
          </div>
        </div>
        <button
          type="button"
          class="devices__btn"
          :disabled="busyId === s.id"
          :data-testid="`device-signout-${s.id}`"
          @click="signOut(s)"
        >
          {{ busyId === s.id ? 'Signing out…' : 'Sign out' }}
        </button>
      </li>
    </ul>
  </div>
</template>

<script setup lang="ts">
import { ref, watch, onMounted } from 'vue'
import { listSessions, revokeSession, usePairingEvents, type SignedInSession } from '~/composables/usePairing'

// Every session signed in as the current user: browsers (password, OAuth)
// and phones paired by QR code. Sign out revokes the session's token.
const sessions = ref<SignedInSession[]>([])
const loading = ref(false)
const error = ref('')
const busyId = ref('')
const events = usePairingEvents()

async function load() {
  loading.value = true
  const res = await listSessions()
  loading.value = false
  if (!res.ok) {
    error.value = res.code === 'user_required' ? 'Sign in in this browser to see your devices.' : (res.error || 'Could not load devices.')
    return
  }
  error.value = ''
  sessions.value = res.data?.sessions ?? []
}

async function signOut(s: SignedInSession) {
  if (s.current && !confirm('Sign out this browser? You will need to sign in again.')) return
  busyId.value = s.id
  const res = await revokeSession(s.id)
  busyId.value = ''
  if (!res.ok) {
    error.value = res.error || 'Could not sign the device out.'
    return
  }
  if (s.current) {
    window.location.href = '/login'
    return
  }
  sessions.value = sessions.value.filter(x => x.id !== s.id)
}

function isPhone(s: SignedInSession) {
  return s.created_via === 'pairing' || /iphone|ipad|ios|android/i.test(`${s.device_name} ${s.device_model ?? ''}`)
}

function viaLabel(via: string) {
  return ({ pairing: 'QR pairing', password: 'Password', oauth: 'OAuth', setup: 'Setup' } as Record<string, string>)[via] ?? via
}

function ago(iso: string) {
  const t = Date.parse(iso)
  if (Number.isNaN(t)) return ''
  const s = Math.max(0, Math.round((Date.now() - t) / 1000))
  if (s < 60) return 'just now'
  if (s < 3600) return `${Math.round(s / 60)} min ago`
  if (s < 86400) return `${Math.round(s / 3600)} h ago`
  return new Date(t).toLocaleDateString()
}

watch(events, load)
onMounted(load)
</script>

<style scoped>
.devices__list {
  list-style: none;
  margin: 0;
  padding: 0;
  border: 1px solid var(--border-color);
  border-radius: 10px;
  overflow: hidden;
}

.devices__item {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 10px 14px;
  background: var(--bg-secondary);
}

.devices__item + .devices__item {
  border-top: 1px solid var(--border-color);
}

.devices__icon {
  display: inline-flex;
  flex-shrink: 0;
  color: var(--accent-purple);
}

.devices__text {
  flex: 1;
  min-width: 0;
}

.devices__name {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 6px;
  font-size: 0.85rem;
  font-weight: 600;
  color: var(--text-primary);
}

.devices__meta {
  margin-top: 2px;
  font-size: 0.76rem;
  color: var(--text-secondary);
}

.devices__tag {
  padding: 1px 7px;
  border-radius: 999px;
  font-size: 0.68rem;
  font-weight: 600;
  color: var(--text-secondary);
  border: 1px solid var(--border-color);
}

.devices__tag--current {
  color: var(--status-success, #10b981);
  border-color: color-mix(in srgb, var(--status-success, #10b981) 40%, transparent);
}

.devices__btn {
  flex-shrink: 0;
  padding: 6px 12px;
  border-radius: 8px;
  font: inherit;
  font-size: 0.78rem;
  color: var(--text-primary);
  background: var(--bg-primary);
  border: 1px solid var(--border-color);
  cursor: pointer;
}

.devices__btn:hover:not(:disabled) {
  color: var(--status-error, #ef4444);
  border-color: var(--status-error, #ef4444);
}

.devices__btn:disabled { opacity: 0.5; cursor: not-allowed; }

.devices__empty,
.devices__error {
  margin: 0;
  font-size: 0.8rem;
  color: var(--text-secondary);
}

.devices__error { color: var(--status-error, #ef4444); }
</style>
