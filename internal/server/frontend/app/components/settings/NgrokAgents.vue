<template>
  <div class="na" data-testid="ngrok-agents">
    <!-- No API key: a compact field to add one -->
    <template v-if="!apiKeySet || replacing">
      <p class="na-lead">
        <template v-if="replacing">Paste a new ngrok API key.</template>
        <template v-else>Add an ngrok API key to see and stop these sessions from Eggify.</template>
        It's not the authtoken: create one at
        <a :href="NGROK_API_KEYS_URL" class="na-link" data-testid="na-open-api-keys" @click.prevent="emit('open', NGROK_API_KEYS_URL)">dashboard.ngrok.com/api-keys</a>.
      </p>
      <form class="na-row" @submit.prevent="saveKey">
        <label class="sr-only" for="na-key-input">ngrok API key</label>
        <input
          id="na-key-input"
          v-model="keyInput"
          type="password"
          class="na-input"
          placeholder="Paste your ngrok API key"
          autocomplete="off"
          spellcheck="false"
          data-testid="na-key-input"
        />
        <button type="submit" class="na-btn na-btn--primary" :disabled="!keyInput.trim() || saving" data-testid="na-key-save">
          Save key
        </button>
        <button v-if="replacing" type="button" class="na-btn na-btn--ghost" @click="replacing = false; keyInput = ''">Cancel</button>
      </form>
      <p v-if="keyError" class="na-error" role="alert" data-testid="na-key-error">{{ keyError }}</p>
    </template>

    <!-- API key saved: the account's connected agents -->
    <template v-else>
      <div class="na-head">
        <span class="na-key" data-testid="na-key-set">
          <SettingsGlyph name="key" :size="13" /> API key saved <code>••••{{ (apiKeyHint || '').replace('…', '') }}</code>
        </span>
        <span class="na-head__actions">
          <button type="button" class="na-btn na-btn--small na-btn--secondary" :disabled="loading" data-testid="na-refresh" @click="load">
            {{ loading ? 'Loading…' : 'Refresh' }}
          </button>
          <button type="button" class="na-btn na-btn--small na-btn--ghost" data-testid="na-key-replace" @click="replacing = true">Replace</button>
          <button type="button" class="na-btn na-btn--small na-btn--ghost na-btn--danger" :disabled="saving" data-testid="na-key-remove" @click="removeKey">Remove</button>
        </span>
      </div>

      <p v-if="listError" class="na-error" role="alert" data-testid="na-list-error">{{ listError }}</p>
      <p v-else-if="loaded && sessions.length === 0" class="na-empty" data-testid="na-empty">
        No ngrok agents are connected to your account right now.
      </p>

      <ul v-if="sessions.length" class="na-list" data-testid="na-list">
        <li
          v-for="s in sessions"
          :key="s.id"
          class="na-item"
          :class="{ 'na-item--current': s.current, 'na-item--gone': stoppedIds.includes(s.id) }"
          data-testid="na-item"
        >
          <div class="na-item__main">
            <div class="na-item__title">
              <span class="na-item__name">{{ agentName(s) }}</span>
              <span v-if="s.current" class="na-tag na-tag--current" data-testid="na-current">This Eggify</span>
              <span v-else-if="s.this_install" class="na-tag">This Eggify, old session</span>
              <span v-else-if="s.eggify" class="na-tag">Eggify</span>
            </div>
            <div class="na-item__facts">
              <span v-if="s.os">{{ osLabel(s.os) }}</span>
              <span v-if="s.agent_version">agent {{ s.agent_version }}</span>
              <span v-if="s.region">region {{ s.region }}</span>
              <span v-if="s.ip" class="na-mono">{{ s.ip }}</span>
              <span v-if="s.started_at" :title="s.started_at">started {{ ago(s.started_at) }}</span>
            </div>
            <div class="na-item__meta">
              <code class="na-mono" :title="s.id">{{ s.id }}</code>
              <code v-if="s.metadata && !s.eggify" class="na-mono" :title="s.metadata">{{ s.metadata }}</code>
            </div>
          </div>
          <span v-if="stoppedIds.includes(s.id)" class="na-stopped" data-testid="na-stopped">Stopped</span>
          <span
            v-else-if="s.current"
            class="na-stopped"
            title="Remote access uses this session. Turn remote access off to end it."
            data-testid="na-in-use"
          >In use</span>
          <button
            v-else
            type="button"
            class="na-btn na-btn--small na-btn--stop"
            :disabled="stopping === s.id"
            title="Ask ngrok to stop this agent"
            data-testid="na-stop"
            @click="stopOne(s)"
          >
            {{ stopping === s.id ? 'Stopping…' : 'Stop' }}
          </button>
        </li>
      </ul>
      <p v-if="stopError" class="na-error" role="alert" data-testid="na-stop-error">{{ stopError }}</p>
    </template>
  </div>
</template>

<script setup lang="ts">
import { ref, watch, onMounted } from 'vue'
import { useTunnel, type NgrokAgentSession } from '~/composables/useTunnel'
import SettingsGlyph from './Glyph.vue'

const NGROK_API_KEYS_URL = 'https://dashboard.ngrok.com/api-keys'

const props = defineProps<{ apiKeySet: boolean, apiKeyHint?: string }>()
const emit = defineEmits<{
  /** An agent session was stopped. */
  (e: 'stopped', id: string): void
  /** The API key was saved or removed (refresh the status). */
  (e: 'changed'): void
  /** Open an external link (desktop adapter in the parent). */
  (e: 'open', url: string): void
}>()

const { setApiKey, deleteApiKey, listAgentSessions, stopAgentSession } = useTunnel()

const keyInput = ref('')
const keyError = ref('')
const replacing = ref(false)
const saving = ref(false)
const sessions = ref<NgrokAgentSession[]>([])
const loading = ref(false)
const loaded = ref(false)
const listError = ref('')
const stopping = ref('')
const stopError = ref('')
const stoppedIds = ref<string[]>([])

async function load() {
  if (!props.apiKeySet) return
  loading.value = true
  listError.value = ''
  const r = await listAgentSessions()
  loading.value = false
  loaded.value = true
  if (!r.ok) {
    listError.value = r.error || 'Could not list the ngrok agents'
    return
  }
  sessions.value = r.sessions || []
  stoppedIds.value = stoppedIds.value.filter(id => sessions.value.some(s => s.id === id))
}

async function saveKey() {
  saving.value = true
  keyError.value = ''
  const r = await setApiKey(keyInput.value.trim())
  saving.value = false
  if (!r.ok) {
    keyError.value = r.error || 'Could not save the API key'
    return
  }
  keyInput.value = ''
  replacing.value = false
  emit('changed')
  // A replaced key doesn't change apiKeySet, so the watcher won't reload.
  if (props.apiKeySet) {
    sessions.value = []
    await load()
  }
}

async function removeKey() {
  saving.value = true
  const r = await deleteApiKey()
  saving.value = false
  if (r.ok) {
    sessions.value = []
    loaded.value = false
    emit('changed')
  } else {
    listError.value = r.error || 'Could not remove the API key'
  }
}

async function stopOne(s: NgrokAgentSession) {
  stopping.value = s.id
  stopError.value = ''
  const r = await stopAgentSession(s.id)
  stopping.value = ''
  if (!r.ok && r.code !== 'not_found') {
    stopError.value = r.error || 'Could not stop the agent'
    return
  }
  stoppedIds.value = [...stoppedIds.value, s.id]
  emit('stopped', s.id)
}

function agentName(s: NgrokAgentSession) {
  if (s.eggify && s.eggify_host) return s.eggify_host
  if (s.eggify) return 'Eggify'
  return 'ngrok agent'
}

function osLabel(os: string) {
  return ({ darwin: 'macOS', linux: 'Linux', windows: 'Windows', freebsd: 'FreeBSD' } as Record<string, string>)[os] || os
}

function ago(iso: string) {
  const t = new Date(iso).getTime()
  if (!t) return iso
  const s = Math.max(0, Math.floor((Date.now() - t) / 1000))
  if (s < 60) return 'just now'
  const m = Math.floor(s / 60)
  if (m < 60) return `${m} min ago`
  const h = Math.floor(m / 60)
  if (h < 48) return `${h} h ago`
  return `${Math.floor(h / 24)} days ago`
}

watch(() => props.apiKeySet, (set) => { if (set) load() })
onMounted(load)

defineExpose({ load })
</script>

<style scoped>
.na {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.sr-only {
  position: absolute;
  width: 1px;
  height: 1px;
  overflow: hidden;
  clip: rect(0 0 0 0);
  white-space: nowrap;
}

.na-lead {
  margin: 0;
  font-size: 0.8rem;
  line-height: 1.5;
  color: var(--text-secondary);
}

.na-link {
  color: var(--accent-purple);
  font-weight: 600;
  text-decoration: none;
}

.na-link:hover {
  text-decoration: underline;
}

.na-row {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
}

.na-input {
  flex: 1 1 240px;
  padding: 7px 11px;
  border: 1px solid var(--border-color);
  border-radius: 8px;
  font-family: var(--font-mono, ui-monospace, monospace);
  font-size: 0.8rem;
  color: var(--text-primary);
  background: var(--bg-primary);
}

.na-input:focus {
  outline: none;
  border-color: var(--accent-purple);
  box-shadow: 0 0 0 3px color-mix(in srgb, var(--accent-purple) 18%, transparent);
}

.na-error {
  margin: 0;
  font-size: 0.78rem;
  line-height: 1.45;
  color: var(--status-error, #ef4444);
}

.na-empty {
  margin: 0;
  font-size: 0.8rem;
  color: var(--text-secondary);
}

.na-head {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
}

.na-head__actions {
  display: inline-flex;
  flex-wrap: wrap;
  gap: 4px;
}

.na-key {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  font-size: 0.8rem;
  font-weight: 600;
  color: var(--status-success, #10b981);
}

.na-key code {
  font-family: var(--font-mono, ui-monospace, monospace);
  font-weight: 500;
  color: var(--text-primary);
}

.na-list {
  list-style: none;
  margin: 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  border: 1px solid var(--border-color);
  border-radius: 10px;
  overflow: hidden;
  background: var(--bg-primary);
}

.na-item {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 10px 12px;
  border-top: 1px solid var(--border-color);
  transition: opacity 0.2s;
}

.na-item:first-child {
  border-top: none;
}

.na-item--current {
  background: color-mix(in srgb, var(--status-success, #10b981) 7%, var(--bg-primary));
}

.na-item--gone {
  opacity: 0.55;
}

.na-item__main {
  flex: 1;
  min-width: 0;
}

.na-item__title {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 6px;
}

.na-item__name {
  font-size: 0.85rem;
  font-weight: 600;
  color: var(--text-primary);
}

.na-tag {
  padding: 1px 8px;
  border-radius: 999px;
  font-size: 0.68rem;
  font-weight: 600;
  color: var(--text-secondary);
  border: 1px solid var(--border-color);
}

.na-tag--current {
  color: var(--status-success, #10b981);
  border-color: color-mix(in srgb, var(--status-success, #10b981) 40%, transparent);
}

.na-item__facts {
  display: flex;
  flex-wrap: wrap;
  gap: 2px 12px;
  margin-top: 3px;
  font-size: 0.75rem;
  color: var(--text-secondary);
}

.na-item__meta {
  display: flex;
  flex-wrap: wrap;
  gap: 2px 12px;
  margin-top: 2px;
  min-width: 0;
}

.na-item__meta code {
  max-width: 100%;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: 0.7rem;
  color: var(--text-muted, var(--text-secondary));
}

.na-mono {
  font-family: var(--font-mono, ui-monospace, monospace);
}

.na-stopped {
  font-size: 0.75rem;
  font-weight: 600;
  color: var(--text-secondary);
}

.na-btn {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  padding: 7px 13px;
  border-radius: 8px;
  font: inherit;
  font-size: 0.8rem;
  font-weight: 500;
  white-space: nowrap;
  cursor: pointer;
  border: 1px solid transparent;
  transition: background 0.15s, border-color 0.15s, color 0.15s;
}

.na-btn:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

.na-btn--small {
  padding: 4px 10px;
  font-size: 0.76rem;
}

.na-btn--primary {
  color: #fff;
  background: var(--accent-purple);
  border-color: var(--accent-purple);
}

.na-btn--secondary {
  color: var(--text-primary);
  background: var(--bg-secondary);
  border-color: var(--border-color);
}

.na-btn--secondary:hover:not(:disabled) {
  color: var(--accent-purple);
  border-color: var(--accent-purple);
}

.na-btn--ghost {
  color: var(--text-secondary);
  background: transparent;
}

.na-btn--ghost:hover:not(:disabled) {
  color: var(--text-primary);
}

.na-btn--danger:hover:not(:disabled) {
  color: var(--status-error, #ef4444);
}

.na-btn--stop {
  flex-shrink: 0;
  color: var(--status-error, #ef4444);
  background: var(--bg-primary);
  border-color: color-mix(in srgb, var(--status-error, #ef4444) 45%, var(--border-color));
}

.na-btn--stop:hover:not(:disabled) {
  color: #fff;
  background: var(--status-error, #ef4444);
}
</style>
