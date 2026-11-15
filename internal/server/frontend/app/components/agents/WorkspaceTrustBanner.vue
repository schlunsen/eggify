<template>
  <div
    v-if="needsTrust && state"
    class="workspace-trust-banner"
    role="region"
    aria-label="Folder config not loaded"
  >
    <div class="trust-row">
      <span class="trust-icon" aria-hidden="true">🛡️</span>
      <div class="trust-text">
        <strong v-if="state.status === 'changed'">This folder's Claude config changed since it was trusted.</strong>
        <strong v-else>This folder has its own Claude config ({{ state.summary || 'settings' }}).</strong>
        <span class="trust-sub">
          Eggify is not loading it{{ state.status === 'changed' ? ' until you trust it again' : '' }}:
          no hooks, no pre-approved tools, no project MCP servers, and no CLAUDE.md or project slash commands.
          Trust it?
        </span>
      </div>
      <div class="trust-actions">
        <button type="button" class="trust-btn secondary" @click="expanded = !expanded">
          {{ expanded ? 'Hide details' : 'Review' }}
        </button>
        <button
          type="button"
          class="trust-btn primary"
          :disabled="busy || !expanded"
          :title="expanded ? 'Load this config from the next message on' : 'Review the config first'"
          @click="trust"
        >
          Trust folder
        </button>
      </div>
    </div>

    <p v-if="error" class="trust-error" role="alert">{{ error }}</p>

    <div v-if="expanded && inv" class="trust-details">
      <p class="trust-path"><code>{{ state.path }}</code></p>

      <section v-if="inv.hooks.length">
        <h4>Hooks — shell commands the CLI runs ({{ inv.hooks.length }})</h4>
        <ul>
          <li v-for="(h, i) in inv.hooks" :key="'h' + i">
            <span class="meta">{{ h.file }} · {{ h.event }}<template v-if="h.matcher"> · {{ h.matcher }}</template></span>
            <pre>{{ h.command }}</pre>
          </li>
        </ul>
      </section>

      <section v-if="inv.allowed_tools.length">
        <h4>Pre-approved tools — run without asking ({{ inv.allowed_tools.length }})</h4>
        <ul>
          <li v-for="(r, i) in inv.allowed_tools" :key="'a' + i">
            <span class="meta">{{ r.file }}</span> <code>{{ r.rule }}</code>
          </li>
        </ul>
      </section>

      <section v-if="inv.mcp_servers.length">
        <h4>MCP servers — programs started for the agent ({{ inv.mcp_servers.length }})</h4>
        <ul>
          <li v-for="(m, i) in inv.mcp_servers" :key="'m' + i">
            <span class="meta">{{ m.file }} · {{ m.name }}</span>
            <pre>{{ m.url || [m.command, ...(m.args || [])].join(' ') }}</pre>
          </li>
        </ul>
      </section>

      <section v-if="inv.scripts?.length">
        <h4>Script files — what the hooks and servers above run ({{ inv.scripts.length }})</h4>
        <ul>
          <li v-for="(s, i) in inv.scripts" :key="'s' + i">
            <code>{{ s.path }}</code>
            <span class="meta">{{ s.sha256 === 'missing' ? ' (does not exist yet)' : '' }}</span>
          </li>
        </ul>
      </section>

      <section v-if="inv.env.length">
        <h4>Environment variables ({{ inv.env.length }})</h4>
        <ul>
          <li v-for="(e, i) in inv.env" :key="'e' + i">
            <span class="meta">{{ e.file }}</span> <code>{{ e.name }}={{ e.value }}</code>
          </li>
        </ul>
      </section>

      <section v-if="inv.other.length">
        <h4>Other settings ({{ inv.other.length }})</h4>
        <ul>
          <li v-for="(o, i) in inv.other" :key="'o' + i">
            <span class="meta">{{ o.file }}</span> <code>{{ o.key }}: {{ o.value }}</code>
          </li>
        </ul>
      </section>

      <p class="trust-note">
        Only trust folders whose config you understand. If the agent or anything else changes
        these files later, Eggify stops loading them and asks again.
      </p>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useAuthenticatedFetch } from '~/composables/useAuthenticatedFetch'

interface Inventory {
  path: string
  files: string[]
  hooks: { file: string; event: string; matcher?: string; command?: string }[]
  allowed_tools: { file: string; rule: string }[]
  mcp_servers: { file: string; name: string; command?: string; args?: string[]; url?: string }[]
  env: { file: string; name: string; value: string }[]
  other: { file: string; key: string; value: string }[]
  scripts?: { path: string; sha256: string }[]
  fingerprint: string
}

interface TrustState {
  session_id: string
  path: string
  status: 'no_config' | 'trusted' | 'untrusted' | 'changed'
  loads_config: boolean
  summary: string
  inventory?: Inventory
  trusted_at?: string
  trusted_by?: string
}

const props = defineProps<{
  sessionId: string
  /** Re-checked whenever this changes (e.g. the session's message count). */
  refreshKey?: number | string
}>()

const { fetchWithAuth } = useAuthenticatedFetch()
const state = ref<TrustState | null>(null)
const expanded = ref(false)
const busy = ref(false)
const error = ref('')

const needsTrust = computed(() => state.value?.status === 'untrusted' || state.value?.status === 'changed')
const inv = computed(() => state.value?.inventory)

const url = () => `/api/agent/sessions/${props.sessionId}/workspace-trust`

async function load() {
  if (!props.sessionId) {
    state.value = null
    return
  }
  try {
    const res = await fetchWithAuth(url())
    if (!res.ok) return
    const next = (await res.json()) as TrustState
    // A different fingerprint means different config: review it again.
    if (state.value?.inventory?.fingerprint !== next.inventory?.fingerprint) expanded.value = false
    state.value = next
  } catch {
    // Leave the last known state; the banner is advisory, the server enforces.
  }
}

async function trust() {
  if (!state.value?.inventory) return
  busy.value = true
  error.value = ''
  try {
    const res = await fetchWithAuth(url(), {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ fingerprint: state.value.inventory.fingerprint })
    })
    const body = await res.json().catch(() => ({}))
    if (res.status === 409 && body.current) {
      state.value = body.current
      expanded.value = true
      error.value = 'The config changed while you were reviewing it. Check the details again.'
      return
    }
    if (!res.ok) {
      error.value = body.error || `Could not trust the folder (${res.status})`
      return
    }
    state.value = body
    expanded.value = false
  } catch (e: any) {
    error.value = e?.message || 'Could not trust the folder'
  } finally {
    busy.value = false
  }
}

watch(() => props.sessionId, () => {
  state.value = null
  expanded.value = false
  error.value = ''
  load()
}, { immediate: true })

watch(() => props.refreshKey, () => load())
</script>

<style scoped>
.workspace-trust-banner {
  padding: 10px 16px;
  background: rgba(245, 158, 11, 0.08);
  border-bottom: 1px solid rgba(245, 158, 11, 0.35);
  color: var(--text-primary, inherit);
  font-size: 13px;
}
.trust-row {
  display: flex;
  gap: 10px;
  align-items: flex-start;
  flex-wrap: wrap;
}
.trust-icon { font-size: 16px; line-height: 1.3; }
.trust-text { flex: 1 1 280px; display: flex; flex-direction: column; gap: 2px; }
.trust-sub { color: var(--text-secondary, inherit); opacity: 0.9; }
.trust-actions { display: flex; gap: 8px; }
.trust-btn {
  padding: 4px 10px;
  border-radius: 6px;
  border: 1px solid rgba(245, 158, 11, 0.5);
  background: transparent;
  color: inherit;
  cursor: pointer;
  font-size: 12px;
}
.trust-btn.primary { background: rgba(245, 158, 11, 0.85); color: #1a1a1a; border-color: transparent; }
.trust-btn:disabled { opacity: 0.5; cursor: not-allowed; }
.trust-error { margin: 8px 0 0; color: #ef4444; }
.trust-details { margin-top: 10px; max-height: 50vh; overflow: auto; }
.trust-details h4 { margin: 10px 0 4px; font-size: 12px; font-weight: 600; }
.trust-details ul { margin: 0; padding-left: 16px; }
.trust-details li { margin: 2px 0; }
.trust-details pre {
  margin: 2px 0 6px;
  padding: 4px 8px;
  white-space: pre-wrap;
  word-break: break-all;
  background: rgba(0, 0, 0, 0.15);
  border-radius: 4px;
  font-size: 12px;
}
.trust-details code { word-break: break-all; }
.meta { opacity: 0.7; font-size: 11px; }
.trust-path { margin: 0; opacity: 0.8; }
.trust-note { margin: 10px 0 0; opacity: 0.8; font-size: 12px; }
</style>
