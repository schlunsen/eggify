<template>
  <div class="pair" :class="{ 'pair--open': !!pairing }" data-testid="pair-device">
    <div class="pair__head">
      <span class="pair__icon" aria-hidden="true">
        <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
          <rect x="6" y="2" width="12" height="20" rx="2.5" /><path d="M11 18h2" />
        </svg>
      </span>
      <div class="pair__title-wrap">
        <h4 class="pair__title">Pair the iOS app</h4>
        <p class="pair__sub">Sign the Eggify iPhone app in without typing a password.</p>
      </div>
      <button
        v-if="!pairing"
        type="button"
        class="pair-btn pair-btn--primary"
        :disabled="busy"
        data-testid="pair-create"
        @click="start"
      >
        {{ busy ? 'Creating…' : (done ? 'Pair another device' : 'Show pairing code') }}
      </button>
    </div>

    <p v-if="error" class="pair-note pair-note--error" role="alert" data-testid="pair-error">{{ error }}</p>
    <p v-if="done && !pairing" class="pair-note pair-note--ok" role="status" data-testid="pair-done">
      Signed in on {{ done }}. You can manage it under Signed-in devices.
    </p>
    <p v-if="expired && !pairing" class="pair-note" role="status" data-testid="pair-expired">
      That code expired. Show a new one when your phone is ready.
    </p>

    <div v-if="pairing" class="pair__ticket" data-testid="pair-ticket">
      <div class="pair__qr">
        <SettingsQrCode :value="pairing.pair_url" :size="184" label="Pairing QR code for the Eggify iOS app" />
      </div>
      <div class="pair__info">
        <p class="pair__lead">Point your iPhone camera at this code</p>
        <p class="pair__hint">
          Tap <strong>Open in Eggify</strong>, and the app signs in to
          <code class="pair__host">{{ hostLabel }}</code>. Or use <em>Scan QR code</em> in the app.
        </p>
        <div class="pair__countdown" :class="{ 'pair__countdown--low': left <= 30 }" aria-live="off">
          <span class="pair__dot" aria-hidden="true" />
          Waiting for your phone · expires in <strong data-testid="pair-countdown">{{ countdown }}</strong>
        </div>
        <p class="pair__warn">
          This code signs the phone in <strong>as you</strong>. Only show it to your own device, and don't share
          screenshots of it. It works once.
        </p>
        <p v-if="pairing.warning" class="pair-note pair-note--warn" data-testid="pair-warning">{{ pairing.warning }}</p>

        <div class="pair__actions">
          <button type="button" class="pair-btn pair-btn--secondary" data-testid="pair-show-link" @click="showLink = !showLink">
            {{ showLink ? 'Hide code/link' : 'Show code/link' }}
          </button>
          <button type="button" class="pair-btn pair-btn--ghost" data-testid="pair-cancel" @click="cancel">Cancel</button>
        </div>
        <div v-if="showLink" class="pair__link" data-testid="pair-link">
          <label class="pair__label" for="pair-link-input">Pairing link (paste it in the app under “Paste pairing link”)</label>
          <div class="pair__copyrow">
            <input id="pair-link-input" class="pair__input" :value="pairing.pair_url" readonly spellcheck="false" @focus="($event.target as HTMLInputElement).select()" />
            <button type="button" class="pair-btn pair-btn--secondary" data-testid="pair-copy" @click="copy">
              {{ copied ? 'Copied' : 'Copy' }}
            </button>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { copyText } from '~/utils/clipboard'
import { ref, computed, onUnmounted } from 'vue'
import SettingsQrCode from './QrCode.vue'
import {
  createPairing, pairingStatus, cancelPairing, secondsLeft, formatCountdown, usePairingEvents,
  type Pairing,
} from '~/composables/usePairing'

// A one-time QR code that signs the Eggify iOS app in as the current user.
// The code lives 5 minutes; the card polls its status and closes when the
// phone redeemed it or it expired. See internal/server/pairing.go.
const props = defineProps<{ baseUrl?: string }>()

const pairing = ref<Pairing | null>(null)
const busy = ref(false)
const error = ref('')
const done = ref('')
const expired = ref(false)
const showLink = ref(false)
const copied = ref(false)
const now = ref(Date.now())
const events = usePairingEvents()

let tick: ReturnType<typeof setInterval> | null = null
let poll: ReturnType<typeof setInterval> | null = null

const left = computed(() => (pairing.value ? secondsLeft(pairing.value.expires_at, now.value) : 0))
const countdown = computed(() => formatCountdown(left.value))
const hostLabel = computed(() => {
  try { return new URL(pairing.value?.base_url || '').host } catch { return pairing.value?.base_url || '' }
})

function stopTimers() {
  if (tick) clearInterval(tick)
  if (poll) clearInterval(poll)
  tick = poll = null
}

function close() {
  stopTimers()
  pairing.value = null
  showLink.value = false
  copied.value = false
}

async function start() {
  busy.value = true
  error.value = ''
  done.value = ''
  expired.value = false
  const res = await createPairing(props.baseUrl)
  busy.value = false
  if (!res.ok || !res.data) {
    error.value = res.code === 'user_required'
      ? 'Sign in to Eggify in this browser to pair a device.'
      : (res.error || 'Could not create a pairing code.')
    return
  }
  pairing.value = res.data
  now.value = Date.now()
  stopTimers()
  tick = setInterval(() => {
    now.value = Date.now()
    if (pairing.value && left.value <= 0) {
      close()
      expired.value = true
    }
  }, 1000)
  poll = setInterval(checkStatus, 2000)
}

async function checkStatus() {
  const p = pairing.value
  if (!p) return
  const res = await pairingStatus(p.id)
  if (!res.ok || !res.data || pairing.value?.id !== p.id) return
  switch (res.data.status) {
    case 'redeemed':
      close()
      done.value = res.data.device_name || 'your phone'
      events.value++
      break
    case 'expired':
      close()
      expired.value = true
      break
    case 'revoked':
      close()
      break
  }
}

async function cancel() {
  const p = pairing.value
  close()
  if (p) await cancelPairing(p.id)
}

async function copy() {
  if (!pairing.value) return
  try {
    await copyText(pairing.value.pair_url)
    copied.value = true
    setTimeout(() => { copied.value = false }, 2000)
  } catch {
    error.value = 'Copy failed. Select the link and copy it by hand.'
  }
}

// Leaving the page withdraws an unused code.
onUnmounted(() => {
  const p = pairing.value
  stopTimers()
  if (p) void cancelPairing(p.id)
})
</script>

<style scoped>
.pair {
  display: flex;
  flex-direction: column;
  gap: 12px;
  padding: 14px 16px;
  border: 1px solid var(--border-color);
  border-radius: 12px;
  background: var(--bg-secondary);
}

.pair--open {
  border-color: color-mix(in srgb, var(--accent-purple) 45%, var(--border-color));
}

.pair__head {
  display: flex;
  align-items: center;
  gap: 12px;
}

.pair__icon {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
  width: 34px;
  height: 34px;
  border-radius: 50%;
  color: var(--accent-purple);
  background: color-mix(in srgb, var(--accent-purple) 12%, transparent);
}

.pair__title-wrap {
  flex: 1;
  min-width: 0;
}

.pair__title {
  margin: 0;
  font-family: var(--font-serif, inherit);
  font-size: 0.95rem;
  font-weight: 650;
  color: var(--text-primary);
}

.pair__sub {
  margin: 2px 0 0;
  font-size: 0.8rem;
  color: var(--text-secondary);
}

/* The "ticket": a circus-poster admission stub with a perforated edge. */
.pair__ticket {
  display: flex;
  gap: 18px;
  align-items: flex-start;
  padding: 14px;
  border: 1.5px dashed color-mix(in srgb, var(--accent-purple) 55%, var(--border-color));
  border-radius: 12px;
  background: var(--bg-primary);
}

.pair__qr {
  flex-shrink: 0;
  padding: 8px;
  border-radius: 12px;
  background: #fff;
  box-shadow: 0 0 0 1px var(--border-color);
}

.pair__info {
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.pair__lead {
  margin: 0;
  font-family: var(--font-serif, inherit);
  font-size: 1.05rem;
  font-weight: 700;
  color: var(--text-primary);
}

.pair__hint,
.pair__warn {
  margin: 0;
  font-size: 0.8rem;
  line-height: 1.5;
  color: var(--text-secondary);
}

.pair__host {
  font-family: var(--font-mono, monospace);
  font-size: 0.76rem;
  color: var(--text-primary);
  word-break: break-all;
}

.pair__countdown {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  font-size: 0.82rem;
  color: var(--text-primary);
}

.pair__countdown strong {
  font-family: var(--font-mono, monospace);
  font-variant-numeric: tabular-nums;
}

.pair__countdown--low strong {
  color: var(--status-warning, #f59e0b);
}

.pair__dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background: var(--status-success, #10b981);
  animation: pair-pulse 1.2s ease-in-out infinite;
}

@keyframes pair-pulse {
  50% { opacity: 0.3; }
}

@media (prefers-reduced-motion: reduce) {
  .pair__dot { animation: none; }
}

.pair__actions,
.pair__copyrow {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
}

.pair__link {
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.pair__label {
  font-size: 0.76rem;
  color: var(--text-secondary);
}

.pair__input {
  flex: 1;
  min-width: 0;
  padding: 7px 10px;
  border: 1px solid var(--border-color);
  border-radius: 8px;
  font-family: var(--font-mono, monospace);
  font-size: 0.74rem;
  color: var(--text-primary);
  background: var(--bg-secondary);
}

.pair-note {
  margin: 0;
  padding: 8px 12px;
  border-radius: 8px;
  font-size: 0.78rem;
  line-height: 1.45;
  color: var(--text-secondary);
  background: var(--bg-primary);
}

.pair-note--error { color: var(--status-error, #ef4444); background: color-mix(in srgb, var(--status-error, #ef4444) 8%, transparent); }
.pair-note--ok { color: var(--status-success, #10b981); background: color-mix(in srgb, var(--status-success, #10b981) 8%, transparent); }
.pair-note--warn { color: var(--status-warning, #b45309); background: color-mix(in srgb, var(--status-warning, #f59e0b) 10%, transparent); }

.pair-btn {
  display: inline-flex;
  align-items: center;
  gap: 7px;
  padding: 8px 14px;
  border-radius: 8px;
  font: inherit;
  font-size: 0.82rem;
  font-weight: 500;
  white-space: nowrap;
  cursor: pointer;
  transition: background 0.15s, border-color 0.15s, color 0.15s, opacity 0.15s;
}

.pair-btn:disabled { opacity: 0.5; cursor: not-allowed; }
.pair-btn--primary { color: #fff; background: var(--accent-purple); border: 1px solid var(--accent-purple); }
.pair-btn--primary:hover:not(:disabled) { filter: brightness(1.08); }
.pair-btn--secondary { color: var(--text-primary); background: var(--bg-secondary); border: 1px solid var(--border-color); }
.pair-btn--secondary:hover:not(:disabled) { color: var(--accent-purple); border-color: var(--accent-purple); }
.pair-btn--ghost { color: var(--text-secondary); background: transparent; border: 1px solid transparent; }
.pair-btn--ghost:hover:not(:disabled) { color: var(--status-error, #ef4444); }

@media (max-width: 640px) {
  .pair__head { flex-wrap: wrap; }
  .pair__ticket { flex-direction: column; align-items: center; }
}
</style>
