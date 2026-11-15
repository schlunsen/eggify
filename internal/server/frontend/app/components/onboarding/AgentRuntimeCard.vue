<template>
  <article
    class="rt-card"
    :class="`rt-card--${tone}`"
    :aria-labelledby="`rt-${runtimeId}-name`"
    :aria-busy="checking || undefined"
    :data-state="stateKey"
  >
    <header class="rt-card__head">
      <span class="rt-card__icon" aria-hidden="true">
        <svg v-if="runtimeId === 'claude'" viewBox="0 0 24 24" width="22" height="22" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round">
          <path d="M12 3v18M3 12h18M5.6 5.6l12.8 12.8M18.4 5.6 5.6 18.4" />
        </svg>
        <svg v-else viewBox="0 0 24 24" width="22" height="22" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
          <path d="m5 8 4 4-4 4M12 17h7" />
        </svg>
      </span>
      <div class="rt-card__title">
        <h3 :id="`rt-${runtimeId}-name`">{{ title }}</h3>
        <p class="rt-card__sub">{{ subtitle }}</p>
      </div>
      <span class="rt-badge" :class="`rt-badge--${tone}`">
        <span class="rt-badge__dot" aria-hidden="true">{{ badgeIcon }}</span>{{ badgeText }}
      </span>
    </header>

    <div class="rt-card__body">
      <!-- Checking -->
      <div v-if="checking" class="rt-skeleton" aria-label="Checking…">
        <span /><span />
      </div>

      <!-- A login is running -->
      <template v-else-if="flow && !flowFinished">
        <p class="rt-wait" role="status" aria-live="polite">
          <span class="rt-spinner" aria-hidden="true" />
          {{ flow.user_code ? 'Enter this code on the verification page…' : 'Waiting for you to finish in the browser…' }}
        </p>

        <div v-if="flow.user_code" class="rt-device">
          <span class="rt-device__label">Your code</span>
          <code class="rt-device__code">{{ flow.user_code }}</code>
          <button type="button" class="rt-btn rt-btn--ghost rt-btn--sm" @click="copy(flow.user_code!, 'code')">
            {{ copied === 'code' ? 'Copied' : 'Copy' }}
          </button>
        </div>

        <div class="rt-actions">
          <a
            v-if="signInUrl"
            :href="signInUrl"
            target="_blank"
            rel="noopener noreferrer"
            class="rt-btn rt-btn--primary"
          >{{ flow.user_code ? 'Open verification page' : (flow.browser_opened ? 'Open the sign-in page again' : 'Open sign-in page') }} ↗</a>
          <button type="button" class="rt-btn rt-btn--ghost" :disabled="busy" @click="$emit('cancel')">Cancel</button>
        </div>

        <form v-if="flow.accepts_code" class="rt-code" @submit.prevent="sendCode">
          <label :for="`rt-${runtimeId}-code`">Shown a code instead? Paste it here</label>
          <div class="rt-code__row">
            <input
              :id="`rt-${runtimeId}-code`"
              v-model="code"
              type="text"
              autocomplete="one-time-code"
              spellcheck="false"
              class="rt-input"
              placeholder="Paste the code from the sign-in page"
            >
            <button type="submit" class="rt-btn rt-btn--secondary" :disabled="!code.trim() || sending">Continue</button>
          </div>
        </form>
        <p v-else-if="flow.browser_opened" class="rt-hint">
          A browser window opened on this computer. Finish signing in there; this card updates by itself.
        </p>
      </template>

      <!-- Status -->
      <template v-else-if="runtime">
        <p v-if="flowFinished && flow?.state !== 'succeeded' && flow?.state !== 'cancelled'" class="rt-alert rt-alert--error" role="alert">
          {{ flow?.message || 'The login did not finish.' }}
        </p>
        <p v-else-if="flow?.state === 'succeeded' && runtime.ready" class="rt-alert rt-alert--ok" role="status">
          Signed in. You're good to go.
        </p>

        <!-- Not installed -->
        <template v-if="!runtime.installed">
          <p class="rt-text">{{ title }} isn't installed on this computer. Install it with one of these commands, then re-check:</p>
          <ul class="rt-cmds">
            <li v-for="cmd in runtime.install.commands" :key="cmd" class="rt-cmd">
              <code>{{ cmd }}</code>
              <button type="button" class="rt-btn rt-btn--ghost rt-btn--sm" :aria-label="`Copy ${cmd}`" @click="copy(cmd, cmd)">
                {{ copied === cmd ? 'Copied' : 'Copy' }}
              </button>
            </li>
          </ul>
          <div class="rt-actions">
            <button type="button" class="rt-btn rt-btn--primary" @click="$emit('recheck')">I've installed it, re-check</button>
            <a :href="runtime.install.docs_url" target="_blank" rel="noopener noreferrer" class="rt-link">Install guide ↗</a>
          </div>
        </template>

        <!-- Installed -->
        <template v-else>
          <dl v-if="runtime.ready" class="rt-facts">
            <div v-if="authSummary"><dt>Signed in</dt><dd>{{ authSummary }}</dd></div>
            <div v-if="runtime.path"><dt>Location</dt><dd class="rt-path">{{ runtime.path }}</dd></div>
          </dl>

          <p v-for="step in warnSteps" :key="step.kind + step.text" class="rt-note">
            {{ step.text }}
            <span v-if="step.command" class="rt-cmd rt-cmd--inline">
              <code>{{ step.command }}</code>
              <button type="button" class="rt-btn rt-btn--ghost rt-btn--sm" :aria-label="`Copy ${step.command}`" @click="copy(step.command!, step.command!)">
                {{ copied === step.command ? 'Copied' : 'Copy' }}
              </button>
            </span>
          </p>
          <p v-if="runtime.error && !runtime.ready" class="rt-note">{{ runtime.error }}</p>

          <template v-if="!runtime.ready && runtime.version_ok">
            <p class="rt-text">{{ loginBlurb }}</p>
            <div class="rt-actions">
              <button type="button" class="rt-btn rt-btn--primary" :disabled="busy" @click="login()">
                {{ busy ? 'Starting…' : loginLabel }}
              </button>
              <NuxtLink to="/providers" class="rt-btn rt-btn--ghost" @click="$emit('navigate')">Use an API key instead</NuxtLink>
            </div>
            <p class="rt-alt">
              <button v-if="runtimeId === 'claude'" type="button" class="rt-link" @click="login('console')">Sign in with an Anthropic Console account</button>
              <button v-else type="button" class="rt-link" @click="login(preferDevice ? 'browser' : 'device')">
                {{ preferDevice ? 'Sign in in this browser instead' : 'Browser on another device? Use a device code' }}
              </button>
              <span aria-hidden="true"> · </span>
              <span>or run <code>{{ runtime.login_command }}</code> in a terminal</span>
            </p>
          </template>
        </template>
        <p v-if="actionError" class="rt-alert rt-alert--error" role="alert">{{ actionError }}</p>
      </template>
    </div>
    <span class="rt-sr" aria-live="polite">{{ copied ? 'Copied to clipboard' : '' }}</span>
  </article>
</template>

<script setup lang="ts">
import { copyText } from '~/utils/clipboard'
import { computed, ref, watch } from 'vue'
import type { LoginFlow, RuntimeId, RuntimeStatus } from '~/composables/useAgentRuntimes'
import { flowDone } from '~/composables/useAgentRuntimes'

const props = defineProps<{
  runtimeId: RuntimeId
  runtime?: RuntimeStatus
  flow?: LoginFlow
  busy?: boolean
  actionError?: string
  checking?: boolean
}>()

const emit = defineEmits<{
  login: [mode?: string]
  cancel: []
  code: [code: string]
  recheck: []
  navigate: []
}>()

const title = computed(() => props.runtime?.name || (props.runtimeId === 'claude' ? 'Claude Code' : 'OpenAI Codex'))
const flowFinished = computed(() => flowDone(props.flow?.state))

const isLocalHost = () => {
  if (typeof window === 'undefined') return true
  return ['localhost', '127.0.0.1', '::1', '[::1]'].includes(window.location.hostname)
}
// Codex's browser login calls back to localhost on the Eggify machine; from
// another computer only the device code works.
const preferDevice = computed(() => props.runtimeId === 'codex' && !isLocalHost())

const stateKey = computed(() => {
  if (props.checking) return 'checking'
  if (props.flow && !flowFinished.value) return 'login'
  const r = props.runtime
  if (!r) return 'unknown'
  if (!r.installed) return 'missing'
  if (!r.version_ok) return 'outdated'
  if (r.ready) return 'ready'
  return 'logged-out'
})

const tone = computed(() => ({
  checking: 'muted', login: 'active', unknown: 'muted', missing: 'muted', outdated: 'warn', ready: 'ok', 'logged-out': 'warn',
} as Record<string, string>)[stateKey.value] || 'muted')

const badgeText = computed(() => ({
  checking: 'Checking…', login: 'Signing in…', unknown: 'Unknown', missing: 'Not installed', outdated: 'Update needed', ready: 'Ready', 'logged-out': 'Not logged in',
} as Record<string, string>)[stateKey.value])

const badgeIcon = computed(() => ({ ok: '✓', warn: '!', active: '•', muted: '–' } as Record<string, string>)[tone.value])

const subtitle = computed(() => {
  const r = props.runtime
  const who = props.runtimeId === 'claude' ? "Anthropic's coding agent" : "OpenAI's coding agent"
  return r?.version ? `${who} · v${r.version}` : who
})

const planLabel = (plan?: string) => {
  if (!plan) return ''
  const p = plan.replace(/[_-]+/g, ' ')
  return p.charAt(0).toUpperCase() + p.slice(1)
}

const authSummary = computed(() => {
  const a = props.runtime?.auth
  if (!a) return ''
  if (a.state !== 'logged_in' && (props.runtime?.provider_keys?.length || 0) > 0) {
    const keys = props.runtime!.provider_keys!.filter(k => k !== 'env')
    return keys.length ? `API key from Settings → Providers (${keys.join(', ')})` : 'API key from the environment'
  }
  const brand = props.runtimeId === 'claude'
    ? (a.method === 'subscription' ? 'Claude' : a.method === 'api_key' ? 'Anthropic API key' : a.detail || 'Signed in')
    : (a.method === 'subscription' ? 'ChatGPT' : a.method === 'api_key' ? 'OpenAI API key' : a.detail || 'Signed in')
  return [a.method === 'subscription' && a.plan ? `${brand} ${planLabel(a.plan)}` : brand, a.account].filter(Boolean).join(' · ')
})

const warnSteps = computed(() => (props.runtime?.next_steps || []).filter(s => s.kind === 'add_to_path' || s.kind === 'update'))

const loginLabel = computed(() => props.runtimeId === 'claude' ? 'Log in with Claude' : (preferDevice.value ? 'Log in with a device code' : 'Log in with ChatGPT'))
const loginBlurb = computed(() => props.runtimeId === 'claude'
  ? 'Sign in with your Claude subscription (Pro, Max, Team) to run Claude Code agents. The login happens in your browser; Eggify never sees your password or token.'
  : 'Sign in with your ChatGPT plan to run Codex agents. The login happens in your browser; Eggify never sees your password or token.')

const signInUrl = computed(() => props.flow?.verification_url || props.flow?.auth_url || '')

const login = (mode?: string) => {
  emit('login', mode ?? (preferDevice.value ? 'device' : undefined))
}

const code = ref('')
const sending = ref(false)
const sendCode = () => {
  if (!code.value.trim()) return
  sending.value = true
  emit('code', code.value.trim())
  code.value = ''
  setTimeout(() => { sending.value = false }, 800)
}
watch(() => props.flow?.id, () => { code.value = '' })

const copied = ref<string | null>(null)
let copiedTimer: ReturnType<typeof setTimeout> | null = null
const copy = async (text: string, key: string) => {
  try {
    await copyText(text)
    copied.value = key
    if (copiedTimer) clearTimeout(copiedTimer)
    copiedTimer = setTimeout(() => { copied.value = null }, 1600)
  } catch { /* clipboard blocked: the text is selectable */ }
}
</script>

<style scoped>
.rt-card {
  position: relative;
  display: flex;
  flex-direction: column;
  gap: 12px;
  padding: 18px 18px 16px;
  border: 1px solid var(--border-color);
  border-radius: 12px;
  background: var(--card-bg, var(--bg-secondary));
  color: var(--text-primary);
  transition: border-color 0.2s ease, box-shadow 0.2s ease;
}
.rt-card--ok { border-color: color-mix(in srgb, var(--status-success, #22c55e) 45%, var(--border-color)); }
.rt-card--active { border-color: var(--accent-purple); box-shadow: 0 0 0 3px var(--primary-subtle, rgba(138, 108, 255, 0.12)); }

.rt-card__head { display: flex; align-items: center; gap: 12px; }
.rt-card__icon {
  display: inline-flex; align-items: center; justify-content: center; flex-shrink: 0;
  width: 40px; height: 40px; border-radius: 10px;
  color: var(--accent-purple);
  background: rgba(var(--accent-purple-rgb, 138, 108, 255), 0.1);
  border: 1px solid rgba(var(--accent-purple-rgb, 138, 108, 255), 0.18);
}
.rt-card__title { min-width: 0; flex: 1; }
.rt-card__title h3 {
  margin: 0; font-size: 1.02rem; font-weight: 650; letter-spacing: -0.01em;
  font-family: var(--font-serif, inherit);
}
.rt-card__sub { margin: 2px 0 0; font-size: 0.78rem; color: var(--text-secondary); }

.rt-badge {
  display: inline-flex; align-items: center; gap: 6px; flex-shrink: 0;
  padding: 4px 10px; border-radius: 999px; font-size: 0.74rem; font-weight: 600; white-space: nowrap;
  border: 1px solid var(--border-color); color: var(--text-secondary); background: var(--bg-primary);
}
.rt-badge__dot { font-weight: 800; }
.rt-badge--ok { color: var(--status-success, #16a34a); border-color: color-mix(in srgb, var(--status-success, #16a34a) 40%, transparent); background: color-mix(in srgb, var(--status-success, #16a34a) 10%, transparent); }
.rt-badge--warn { color: var(--status-warning, #b45309); border-color: color-mix(in srgb, var(--status-warning, #b45309) 40%, transparent); background: color-mix(in srgb, var(--status-warning, #b45309) 10%, transparent); }
.rt-badge--active { color: var(--accent-purple); border-color: var(--primary-border, rgba(138, 108, 255, 0.35)); background: var(--primary-subtle, rgba(138, 108, 255, 0.08)); }

.rt-card__body { display: flex; flex-direction: column; gap: 10px; font-size: 0.86rem; line-height: 1.5; }
.rt-text { margin: 0; color: var(--text-secondary); }
.rt-hint { margin: 0; font-size: 0.8rem; color: var(--text-muted, var(--text-secondary)); }
.rt-note {
  margin: 0; padding: 8px 10px; border-radius: 8px; font-size: 0.8rem;
  color: var(--text-primary);
  background: color-mix(in srgb, var(--status-warning, #b45309) 9%, transparent);
  border: 1px solid color-mix(in srgb, var(--status-warning, #b45309) 25%, transparent);
}
.rt-alert { margin: 0; padding: 8px 10px; border-radius: 8px; font-size: 0.82rem; }
.rt-alert--error { color: var(--status-error, #dc2626); background: color-mix(in srgb, var(--status-error, #dc2626) 9%, transparent); border: 1px solid color-mix(in srgb, var(--status-error, #dc2626) 25%, transparent); }
.rt-alert--ok { color: var(--status-success, #16a34a); background: color-mix(in srgb, var(--status-success, #16a34a) 9%, transparent); border: 1px solid color-mix(in srgb, var(--status-success, #16a34a) 25%, transparent); }

.rt-facts { display: grid; grid-template-columns: auto 1fr; gap: 4px 14px; margin: 0; }
.rt-facts > div { display: contents; }
.rt-facts dt { color: var(--text-muted, var(--text-secondary)); font-size: 0.78rem; }
.rt-facts dd { margin: 0; font-size: 0.82rem; overflow-wrap: anywhere; }
.rt-facts .rt-path { font-family: var(--font-mono, monospace); font-size: 0.76rem; color: var(--text-secondary); }

.rt-cmds { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: 6px; }
.rt-cmd {
  display: flex; align-items: center; gap: 8px; justify-content: space-between;
  padding: 6px 6px 6px 10px; border-radius: 8px;
  background: var(--code-bg, var(--bg-primary)); border: 1px solid var(--border-color);
}
.rt-cmd code, .rt-alt code, .rt-device__code { font-family: var(--font-mono, ui-monospace, monospace); font-size: 0.8rem; overflow-wrap: anywhere; }
.rt-cmd--inline { margin-top: 6px; }

.rt-actions { display: flex; flex-wrap: wrap; align-items: center; gap: 8px; }
.rt-alt { margin: 0; font-size: 0.78rem; color: var(--text-muted, var(--text-secondary)); }

.rt-btn {
  display: inline-flex; align-items: center; justify-content: center; gap: 6px;
  min-height: 34px; padding: 7px 14px; border-radius: 8px; border: 1px solid transparent;
  font: inherit; font-size: 0.84rem; font-weight: 600; cursor: pointer; text-decoration: none;
  transition: background-color 0.15s ease, border-color 0.15s ease, color 0.15s ease;
}
.rt-btn:disabled { opacity: 0.55; cursor: not-allowed; }
.rt-btn--primary { background: var(--accent-purple); color: var(--bg-primary, #fff); }
.rt-btn--primary:hover:not(:disabled) { background: var(--accent-purple-hover, var(--accent-purple)); }
.rt-btn--secondary { background: var(--bg-primary); color: var(--text-primary); border-color: var(--border-color); }
.rt-btn--ghost { background: transparent; color: var(--text-secondary); border-color: var(--border-color); }
.rt-btn--ghost:hover:not(:disabled), .rt-btn--secondary:hover:not(:disabled) { color: var(--text-primary); border-color: var(--accent-purple); }
.rt-btn--sm { min-height: 28px; padding: 3px 10px; font-size: 0.76rem; }
.rt-btn:focus-visible, .rt-link:focus-visible, .rt-input:focus-visible {
  outline: 2px solid var(--accent-purple); outline-offset: 2px;
}
.rt-link {
  padding: 0; border: 0; background: none; font: inherit; color: var(--accent-purple);
  cursor: pointer; text-decoration: underline; text-underline-offset: 2px;
}

.rt-wait { display: flex; align-items: center; gap: 10px; margin: 0; font-weight: 600; }
.rt-spinner {
  width: 16px; height: 16px; flex-shrink: 0; border-radius: 50%;
  border: 2px solid rgba(var(--accent-purple-rgb, 138, 108, 255), 0.25); border-top-color: var(--accent-purple);
  animation: rt-spin 0.9s linear infinite;
}
@keyframes rt-spin { to { transform: rotate(360deg); } }

.rt-device { display: flex; align-items: center; gap: 10px; padding: 10px 12px; border-radius: 10px; background: var(--code-bg, var(--bg-primary)); border: 1px dashed var(--accent-purple); }
.rt-device__label { font-size: 0.76rem; color: var(--text-secondary); }
.rt-device__code { font-size: 1.15rem; font-weight: 700; letter-spacing: 0.12em; flex: 1; }

.rt-code { display: flex; flex-direction: column; gap: 6px; }
.rt-code label { font-size: 0.78rem; color: var(--text-secondary); }
.rt-code__row { display: flex; gap: 8px; }
.rt-input {
  flex: 1; min-width: 0; padding: 7px 10px; border-radius: 8px; border: 1px solid var(--border-color);
  background: var(--bg-primary); color: var(--text-primary); font-family: var(--font-mono, monospace); font-size: 0.82rem;
}

.rt-skeleton { display: flex; flex-direction: column; gap: 8px; }
.rt-skeleton span { display: block; height: 12px; border-radius: 6px; background: var(--border-color); opacity: 0.6; animation: rt-pulse 1.4s ease-in-out infinite; }
.rt-skeleton span:last-child { width: 60%; }
@keyframes rt-pulse { 50% { opacity: 0.25; } }

.rt-sr { position: absolute; width: 1px; height: 1px; overflow: hidden; clip: rect(0 0 0 0); white-space: nowrap; }

@media (prefers-reduced-motion: reduce) {
  .rt-card, .rt-btn { transition: none; }
  .rt-spinner { animation-duration: 2.4s; }
  .rt-skeleton span { animation: none; }
}

@media (max-width: 520px) {
  .rt-card__head { flex-wrap: wrap; }
  .rt-code__row { flex-direction: column; }
}
</style>
