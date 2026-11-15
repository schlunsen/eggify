<template>
  <div class="lic" data-testid="settings-license">
    <div v-if="!status && loading" class="lic-loading"><EggLoader compact /></div>

    <template v-else-if="status">
      <div class="lic-head">
        <span class="lic-badge" :class="`lic-badge--${badgeKind}`" data-testid="license-state">
          <span class="lic-badge__dot" />{{ stateLabel }}
        </span>
        <p class="lic-line" data-testid="license-line">{{ summary }}</p>
      </div>

      <SettingsNote v-if="status.error" variant="error" data-testid="license-error-note">{{ status.error }}</SettingsNote>
      <SettingsNote v-else-if="status.renew_error" variant="info" data-testid="license-renew-note">
        The latest check didn't get through: {{ status.renew_error }} Eggify keeps working until {{ expiry }}.
      </SettingsNote>

      <dl v-if="status.state !== 'unlicensed'" class="lic-facts">
        <template v-if="status.serial_hint">
          <dt>Serial</dt><dd><code data-testid="license-serial-hint">{{ status.serial_hint }}</code></dd>
        </template>
        <template v-if="status.machine">
          <dt>This Mac</dt><dd data-testid="license-machine">{{ status.machine }}</dd>
        </template>
        <template v-if="status.plan">
          <dt>Plan</dt><dd>{{ planLabel }}</dd>
        </template>
        <template v-if="status.seats">
          <dt>Computers</dt><dd>{{ status.seats_used ?? '?' }} of {{ status.seats }} in use</dd>
        </template>
        <template v-if="lastChecked">
          <dt>Last checked</dt><dd>{{ lastChecked }}</dd>
        </template>
      </dl>

      <SettingsNote v-if="actionError" variant="error" data-testid="license-action-error">{{ actionError }}</SettingsNote>
      <p v-if="actionNote" class="lic-hint" data-testid="license-action-note">{{ actionNote }}</p>

      <div class="lic-row">
        <template v-if="status.state === 'unlicensed' || status.state === 'invalid'">
          <NuxtLink to="/activate" class="lic-btn lic-btn--primary" data-testid="license-go-activate">Enter serial number</NuxtLink>
        </template>
        <template v-else>
          <button type="button" class="lic-btn lic-btn--secondary" :disabled="busy" data-testid="license-check" @click="check">
            <EggLoader v-if="busy && pending === 'check'" compact />
            Check now
          </button>
          <button type="button" class="lic-btn lic-btn--ghost" :disabled="busy" ref="deactivateEl" data-testid="license-deactivate" @click="confirming = true">
            Deactivate this Mac
          </button>
        </template>
      </div>

      <dialog
        v-if="confirming"
        ref="dialogEl"
        class="lic-confirm"
        aria-labelledby="lic-confirm-title"
        data-testid="license-confirm"
        @cancel.prevent="closeConfirm"
        @click.self="closeConfirm"
      >
        <h4 id="lic-confirm-title" class="lic-confirm__title">Deactivate Eggify on this Mac?</h4>
        <p class="lic-confirm__text">
          This frees one of the serial's computers for another Mac. Eggify stops working here until you enter the
          serial number again.
        </p>
        <div class="lic-row">
          <button ref="cancelEl" type="button" class="lic-btn lic-btn--ghost" :disabled="busy" data-testid="license-confirm-cancel" @click="closeConfirm">Cancel</button>
          <button type="button" class="lic-btn lic-btn--danger" :disabled="busy" data-testid="license-confirm-deactivate" @click="deactivateNow">
            <EggLoader v-if="busy && pending === 'deactivate'" compact />
            Deactivate
          </button>
        </div>
      </dialog>
    </template>

    <SettingsNote v-else variant="error">Couldn't load the license status.</SettingsNote>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, watch, nextTick } from 'vue'
import { useLicense } from '~/composables/useLicense'
import { formatLicenseDate, goToActivation } from '~/utils/license'
import SettingsNote from './Note.vue'

const { status, loading, busy, refresh, deactivate, checkNow } = useLicense()

const confirming = ref(false)
const dialogEl = ref<HTMLDialogElement | null>(null)
const cancelEl = ref<HTMLButtonElement | null>(null)
const deactivateEl = ref<HTMLButtonElement | null>(null)

function closeConfirm() {
  if (busy.value) return
  confirming.value = false
}

// A real modal <dialog>: the page behind is inert, Tab stays inside, Escape
// cancels; focus starts on Cancel (the safe choice) and returns afterwards.
watch(confirming, async (open) => {
  await nextTick()
  if (open) {
    const d = dialogEl.value
    if (d && typeof d.showModal === 'function') {
      if (!d.open) d.showModal()
    } else {
      d?.setAttribute('open', '')
    }
    cancelEl.value?.focus()
  } else {
    deactivateEl.value?.focus()
  }
})
const actionError = ref('')
const actionNote = ref('')
const pending = ref<'' | 'check' | 'deactivate'>('')

const expiry = computed(() => formatLicenseDate(status.value?.expires_at))
const lastChecked = computed(() => formatLicenseDate(status.value?.last_checked))
const planLabel = computed(() => {
  const p = status.value?.plan || ''
  return p ? p.charAt(0).toUpperCase() + p.slice(1) : ''
})

const badgeKind = computed(() => {
  switch (status.value?.state) {
    case 'active': return status.value.renew_error ? 'warn' : 'ok'
    case 'expired':
    case 'invalid': return 'error'
    default: return 'off'
  }
})

const stateLabel = computed(() => ({
  active: 'Activated',
  expired: 'Expired',
  invalid: 'Not valid',
  unlicensed: 'Not activated',
}[status.value?.state ?? 'unlicensed']))

const summary = computed(() => {
  const s = status.value
  if (!s) return ''
  switch (s.state) {
    case 'active':
      return expiry.value ? `Renews automatically; works offline until ${expiry.value}.` : 'Renews automatically.'
    case 'expired':
      return 'Connect to the internet and choose Check now to renew it.'
    case 'invalid':
      return 'Enter your serial number again to activate this Mac.'
    default:
      return s.required ? 'Enter your serial number to use Eggify on this Mac.' : 'This build of Eggify doesn\'t need a license.'
  }
})

async function check() {
  actionError.value = ''
  actionNote.value = ''
  pending.value = 'check'
  const res = await checkNow()
  pending.value = ''
  if (res.ok) actionNote.value = 'Checked with the license server just now.'
  else actionError.value = res.error || "Couldn't check the license."
}

async function deactivateNow() {
  actionError.value = ''
  actionNote.value = ''
  pending.value = 'deactivate'
  const res = await deactivate()
  pending.value = ''
  confirming.value = false
  if (!res.ok) {
    actionError.value = res.error || "Couldn't deactivate this Mac."
    return
  }
  if (res.status?.required) goToActivation()
}

onMounted(() => { void refresh() })
</script>

<style scoped>
.lic { display: flex; flex-direction: column; gap: 14px; }
.lic-loading { padding: 8px 0; }
.lic-head { display: flex; flex-direction: column; gap: 6px; }
.lic-line { margin: 0; color: var(--text-secondary); font-size: 0.9rem; }

.lic-badge {
  display: inline-flex;
  align-items: center;
  gap: 7px;
  align-self: flex-start;
  padding: 3px 10px;
  border-radius: 999px;
  border: 1px solid var(--border-color);
  font-size: 0.8rem;
  font-weight: 600;
  color: var(--text-primary);
}
.lic-badge__dot { width: 8px; height: 8px; border-radius: 50%; background: var(--text-muted); }
.lic-badge--ok .lic-badge__dot { background: var(--status-success, var(--accent-green)); }
.lic-badge--warn .lic-badge__dot { background: var(--status-warning, var(--accent-orange)); }
.lic-badge--error .lic-badge__dot { background: var(--status-error, var(--accent-red)); }

.lic-facts {
  display: grid;
  grid-template-columns: max-content 1fr;
  gap: 6px 16px;
  margin: 0;
  font-size: 0.88rem;
}
.lic-facts dt { color: var(--text-muted); }
.lic-facts dd { margin: 0; color: var(--text-primary); overflow-wrap: anywhere; }
.lic-facts code { font-family: var(--font-mono, monospace); }

.lic-row { display: flex; flex-wrap: wrap; gap: 8px; align-items: center; }
.lic-hint { margin: 0; font-size: 0.82rem; color: var(--text-muted); }

.lic-btn {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  padding: 7px 14px;
  border-radius: 6px;
  border: 1px solid var(--border-color);
  background: transparent;
  color: var(--text-primary);
  font: inherit;
  font-size: 0.88rem;
  cursor: pointer;
  text-decoration: none;
}
.lic-btn:disabled { opacity: 0.55; cursor: not-allowed; }
.lic-btn--primary { background: var(--accent-purple); border-color: var(--accent-purple); color: #fff; }
.lic-btn--secondary { background: var(--bg-secondary); }
.lic-btn--ghost { border-color: transparent; color: var(--text-secondary); }
.lic-btn--ghost:hover:not(:disabled) { color: var(--text-primary); background: var(--bg-secondary); }
.lic-btn--danger { background: var(--status-error, var(--accent-red)); border-color: var(--status-error, var(--accent-red)); color: #fff; }

.lic-confirm {
  position: static;
  display: block;
  color: inherit;
  padding: 14px 16px;
  border: 1px solid var(--border-color);
  border-radius: 8px;
  background: var(--bg-secondary);
}
.lic-confirm[open]:modal {
  position: fixed;
  inset: 0;
  margin: auto;
  max-width: min(440px, calc(100vw - 32px));
}
.lic-confirm::backdrop { background: rgba(0, 0, 0, 0.45); }
.lic-confirm__title { margin: 0 0 6px; font-size: 0.95rem; color: var(--text-primary); }
.lic-confirm__text { margin: 0 0 12px; font-size: 0.86rem; line-height: 1.5; color: var(--text-secondary); }
</style>
