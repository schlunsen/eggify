<template>
  <div class="activate-card" data-testid="license-activation">
    <div class="activate-header">
      <div class="logo-area">
        <EggifyLogo :size="72" />
      </div>
      <h1 class="activate-title">Activate Eggify</h1>
      <p class="activate-subtitle">Enter the serial number from your purchase email to start using Eggify on this computer.</p>
    </div>

    <div v-if="loading && !status" class="activate-body activate-body--loading">
      <EggLoader />
    </div>

    <div v-else-if="activated" class="activate-body" data-testid="license-activated">
      <p class="activate-done">
        <span class="activate-done__mark" aria-hidden="true">✓</span>
        <template v-if="status?.required">Eggify is activated{{ status?.machine ? ` on ${status.machine}` : '' }}.</template>
        <template v-else>This build of Eggify doesn't need a license.</template>
      </p>
      <button type="button" class="btn-submit" data-testid="license-continue" @click="emit('done')">
        Continue
      </button>
    </div>

    <div v-else class="activate-body">
      <p v-if="status?.error" class="notice" data-testid="license-notice">{{ status.error }}</p>

      <form novalidate @submit.prevent="submit">
        <label for="serial" class="field-label">Serial number</label>
        <input
          id="serial"
          ref="inputEl"
          :value="serial"
          type="text"
          inputmode="text"
          class="serial-input"
          :class="{ 'serial-input--bad': showProblem && !!problem }"
          placeholder="EGG-XXXXX-XXXXX-XXXXX-XXXXX"
          autocomplete="off"
          autocapitalize="characters"
          spellcheck="false"
          maxlength="40"
          :readonly="busy"
          :aria-busy="busy"
          :aria-invalid="showProblem && !!problem"
          aria-describedby="serial-hint"
          data-testid="license-serial"
          @input="onInput"
          @blur="touched = true"
        >
        <p id="serial-hint" aria-live="polite" class="field-hint" :class="{ 'field-hint--bad': showProblem && !!problem }" data-testid="license-serial-hint">
          {{ hintText }}
        </p>

        <div v-if="error" class="error-message" role="alert" data-testid="license-error">
          <p>{{ error }}</p>
          <ul v-if="activations.length" class="machines" data-testid="license-machines">
            <li v-for="a in activations" :key="a.machine + (a.activated_at ?? '')">{{ a.machine }}</li>
          </ul>
        </div>

        <button type="submit" class="btn-submit" :disabled="busy || !valid" data-testid="license-activate">
          <EggLoader v-if="busy" compact />
          <span>{{ busy ? 'Activating…' : 'Activate' }}</span>
        </button>
      </form>

      <div class="activate-info">
        <p>
          No serial number yet?
          <a :href="BUY_URL" data-testid="license-buy" @click.prevent="openExternal(BUY_URL)">Buy a license</a>
        </p>
        <p class="activate-small">A serial works on up to 3 computers. Eggify checks it now and then in the background and keeps working offline for 30 days.</p>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, nextTick } from 'vue'
import { useLicense } from '~/composables/useLicense'
import { desktopOpenExternal } from '~/utils/desktop'
import {
  BUY_URL, caretAfterFormat, formatSerialInput, licenseBlocks, normalizeSerial, serialChars, serialProblem, SERIAL_LENGTH,
  type LicenseActivation as Activation,
} from '~/utils/license'

const emit = defineEmits<{ (e: 'done'): void }>()

const { status, loading, busy, refresh, activate } = useLicense()

const serial = ref('')
const touched = ref(false)
const error = ref('')
const activations = ref<Activation[]>([])
const inputEl = ref<HTMLInputElement | null>(null)

const activated = computed(() => !!status.value && !licenseBlocks(status.value))
const problem = computed(() => serialProblem(serial.value))
const valid = computed(() => !!normalizeSerial(serial.value))
// "N more characters" only after leaving the field or once all are typed;
// a wrong character is shown at once.
const showProblem = computed(() => {
  const s = serialChars(serial.value)
  return touched.value || s.length >= SERIAL_LENGTH || /[^0-9A-HJKMNP-TV-Z]/.test(s)
})
const hintText = computed(() => {
  if (valid.value) return 'Looks right.'
  if (showProblem.value && problem.value) return problem.value
  return 'Letters and numbers; dashes are added for you.'
})

function onInput(e: Event) {
  const el = e.target as HTMLInputElement
  const raw = el.value
  const caret = el.selectionStart ?? raw.length
  const formatted = formatSerialInput(raw)
  serial.value = formatted
  error.value = ''
  activations.value = []
  // The dashes move the text: put the caret after the same serial characters.
  if (raw !== formatted) {
    el.value = formatted
    const pos = caretAfterFormat(raw, caret, formatted)
    void nextTick(() => el.setSelectionRange(pos, pos))
  }
}

async function submit() {
  touched.value = true
  if (!valid.value || busy.value) return
  error.value = ''
  activations.value = []
  const res = await activate(serial.value)
  if (res.ok) {
    if (activated.value) emit('done')
    else {
      error.value = status.value?.error || 'Activation did not complete.'
      void nextTick(() => inputEl.value?.focus())
    }
    return
  }
  void nextTick(() => inputEl.value?.focus())
  error.value = res.error || 'Activation failed. Try again.'
  activations.value = res.code === 'seats_full' ? (res.activations || []) : []
}

async function openExternal(url: string) {
  try {
    if (await desktopOpenExternal(url)) return
  } catch { /* fall back to the browser */ }
  window.open(url, '_blank', 'noopener,noreferrer')
}

onMounted(async () => {
  await refresh()
  inputEl.value?.focus()
})

defineExpose({ serial, submit })
</script>

<style scoped>
.activate-card {
  background: var(--card-bg, var(--bg-tertiary));
  border: 1px solid var(--border-color);
  border-radius: 8px;
  overflow: hidden;
}

.activate-header {
  text-align: center;
  padding: 44px 40px 28px;
}

.logo-area {
  display: flex;
  justify-content: center;
  margin-bottom: 24px;
}

.activate-title {
  margin: 0 0 8px;
  font-family: Georgia, 'Times New Roman', serif;
  font-size: 1.8rem;
  font-weight: 400;
  letter-spacing: -0.02em;
  color: var(--text-primary);
}

.activate-subtitle {
  margin: 0;
  font-size: 0.9rem;
  color: var(--text-muted);
  line-height: 1.6;
}

.activate-body {
  padding: 28px 40px 36px;
  border-top: 1px solid var(--border-color);
}

.activate-body--loading {
  display: flex;
  justify-content: center;
  padding: 40px;
}

.field-label {
  display: block;
  margin-bottom: 6px;
  font-weight: 500;
  color: var(--text-primary);
  font-size: 0.85rem;
}

.serial-input {
  width: 100%;
  padding: 12px 14px;
  background: var(--bg-primary);
  border: 1px solid var(--border-color);
  border-radius: 4px;
  color: var(--text-primary);
  font-family: var(--font-mono, ui-monospace, 'SF Mono', Menlo, monospace);
  font-size: 1.05rem;
  letter-spacing: 0.06em;
  transition: border-color 0.2s ease;
}

.serial-input:focus {
  outline: none;
  border-color: var(--accent-purple);
}

.serial-input--bad {
  border-color: var(--status-error, var(--accent-red));
}

.serial-input::placeholder {
  color: var(--text-muted);
  letter-spacing: 0.04em;
}

.field-hint {
  margin: 6px 0 18px;
  min-height: 1.2em;
  font-size: 0.8rem;
  color: var(--text-muted);
}

.field-hint--bad {
  color: var(--status-error, var(--accent-red));
}

.notice {
  margin: 0 0 18px;
  padding: 10px 14px;
  border: 1px solid var(--border-color);
  border-radius: 4px;
  background: var(--bg-secondary);
  color: var(--text-secondary);
  font-size: 0.85rem;
  line-height: 1.5;
}

.error-message {
  padding: 10px 14px;
  margin-bottom: 18px;
  border: 1px solid color-mix(in srgb, var(--status-error, var(--accent-red)) 30%, transparent);
  background: color-mix(in srgb, var(--status-error, var(--accent-red)) 8%, transparent);
  border-radius: 4px;
  color: var(--status-error, var(--accent-red));
  font-size: 0.85rem;
  line-height: 1.5;
}

.error-message p {
  margin: 0;
}

.machines {
  margin: 8px 0 0;
  padding-left: 18px;
  color: var(--text-primary);
}

.btn-submit {
  width: 100%;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 8px;
  padding: 12px;
  background: var(--accent-purple);
  color: var(--text-on-accent, #fff);
  border: none;
  border-radius: 4px;
  font-size: 0.95rem;
  font-weight: 500;
  font-family: inherit;
  cursor: pointer;
  transition: opacity 0.2s ease;
}

.btn-submit:hover:not(:disabled) {
  opacity: 0.9;
}

.btn-submit:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

.activate-info {
  margin-top: 22px;
  font-size: 0.85rem;
  color: var(--text-muted);
  text-align: center;
  line-height: 1.6;
}

.activate-info p {
  margin: 0 0 6px;
}

.activate-info a {
  color: var(--accent-purple);
}

.activate-small {
  font-size: 0.78rem;
}

.activate-done {
  display: flex;
  align-items: center;
  gap: 10px;
  margin: 0 0 20px;
  color: var(--text-primary);
}

.activate-done__mark {
  color: var(--status-success, var(--accent-green));
  font-weight: 700;
}

@media (max-width: 480px) {
  .activate-header { padding: 32px 20px 20px; }
  .activate-body { padding: 22px 20px 28px; }
}
</style>
