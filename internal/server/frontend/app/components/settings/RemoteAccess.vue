<template>
  <div class="ra" data-testid="remote-access">
    <!-- Hero: big switch + live status -->
    <div class="ra-hero" :class="`ra-hero--${kind}`">
      <div class="ra-hero__text">
        <div class="ra-hero__title">
          <span class="ra-badge" :class="`ra-badge--${kind}`" data-testid="ra-status">
            <span class="ra-badge__dot" />
            {{ statusLabel }}
          </span>
        </div>
        <p class="ra-hero__line" data-testid="ra-status-line">{{ statusLine }}</p>
      </div>
      <label class="ra-switch" :class="{ 'ra-switch--disabled': switchDisabled }" :title="switchTitle">
        <input
          type="checkbox"
          role="switch"
          data-testid="ra-toggle"
          :checked="isOn"
          :disabled="switchDisabled"
          :aria-label="isOn ? 'Turn remote access off' : 'Turn remote access on'"
          @change="onToggle"
        />
        <span class="ra-switch__track" />
      </label>
    </div>

    <SettingsNote v-if="actionError" variant="error" data-testid="ra-action-error">{{ actionError }}</SettingsNote>
    <!-- ERR_NGROK_108: the account's agent sessions are all in use -->
    <section v-if="agentLimit" class="ra-limit" data-testid="ra-limit" aria-labelledby="ra-limit-title">
      <div class="ra-limit__head">
        <SettingsGlyph name="alert" :size="16" />
        <h4 id="ra-limit-title" class="ra-limit__title">All of your ngrok agent sessions are in use</h4>
      </div>
      <p class="ra-limit__text">
        ngrok only lets an account have a few agents connected at once (3 on the free plan), and other
        ngrok agents are using them: ngrok on another computer or server, or sessions that ended
        recently and haven't expired yet (that takes about a minute). Stop one, then try again.
      </p>
      <div class="ra-row">
        <button type="button" class="ra-btn ra-btn--primary" :disabled="busy" data-testid="ra-retry" @click="retry">
          Try again
        </button>
        <button type="button" class="ra-btn ra-btn--secondary" data-testid="ra-open-agents" @click="openExternal(NGROK_AGENTS_URL)">
          <SettingsGlyph name="external" /> See your agents on ngrok.com
        </button>
      </div>
      <p v-if="retryNote" class="ra-hint" data-testid="ra-retry-note">{{ retryNote }}</p>
      <div class="ra-limit__agents">
        <NgrokAgents
          :api-key-set="!!settings?.api_key_set"
          :api-key-hint="settings?.api_key_hint"
          @stopped="onAgentStopped"
          @changed="fetchStatus"
          @open="openExternal"
        />
      </div>
      <details class="ra-limit__raw">
        <summary>ngrok's message</summary>
        <code>{{ status?.error }}</code>
      </details>
    </section>
    <SettingsNote v-else-if="status?.status === 'error' && status?.error" variant="error" data-testid="ra-error">
      {{ status.error }}
    </SettingsNote>
    <SettingsNote v-if="blockedNotice" variant="error" data-testid="ra-blocked">{{ blockedNotice }}</SettingsNote>
    <SettingsNote v-if="settings?.load_error" variant="error">{{ settings.load_error }}</SettingsNote>

    <!-- Connected: URL, copy, QR -->
    <div v-if="status?.status === 'connected' && status.public_url" class="ra-live" data-testid="ra-live">
      <div class="ra-live__info">
        <span class="ra-label">Public URL</span>
        <div class="ra-url">
          <a :href="status.public_url" class="ra-url__link" data-testid="ra-url" @click.prevent="openExternal(status.public_url!)">
            {{ status.public_url }}
          </a>
          <button
            type="button"
            class="ra-icon-btn"
            :title="urlCopied ? 'Copied!' : 'Copy URL'"
            :aria-label="urlCopied ? 'Copied' : 'Copy public URL'"
            data-testid="ra-copy"
            @click="copyUrl()"
          >
            <SettingsGlyph :name="urlCopied ? 'check' : 'copy'" :size="14" />
          </button>
        </div>
        <dl class="ra-facts">
          <template v-if="connectedFor">
            <dt>Connected</dt><dd>{{ connectedFor }}</dd>
          </template>
          <template v-if="status.region">
            <dt>Region</dt><dd>{{ status.region }}</dd>
          </template>
          <template v-if="protectionLabel">
            <dt>Edge protection</dt><dd>{{ protectionLabel }}</dd>
          </template>
        </dl>
        <p v-if="relayMode" class="ra-hint" data-testid="ra-relay-hint">
          For the iOS app: pair it with the code below. The app checks this Eggify's own certificate, so the relay
          can't read anything. Browsers show a certificate warning at this address.
        </p>
        <p v-else class="ra-hint">Address only (sign in with password): open it on another device and sign in with your Eggify account.</p>
      </div>
      <div class="ra-qr">
        <SettingsQrCode :value="status.public_url" :size="152" label="QR code for the public URL (address only, sign in with password)" />
      </div>
    </div>

    <SettingsPairDevice :base-url="status?.status === 'connected' ? status.public_url : undefined" />

    <!-- How to connect: Eggify relay or ngrok -->
    <div v-if="settings?.relay_available" class="ra-choices ra-choices--mode" role="radiogroup" aria-label="How to connect" data-testid="ra-mode">
      <label v-for="m in MODES" :key="m.id" class="ra-choice" :class="{ 'ra-choice--on': mode === m.id }">
        <input type="radio" name="ra-mode" :value="m.id" :checked="mode === m.id" :disabled="busy" :data-testid="`ra-mode-${m.id}`" @change="saveMode(m.id)" />
        <span class="ra-choice__title">{{ m.title }}</span>
        <span class="ra-choice__desc">{{ m.desc }}</span>
      </label>
    </div>

    <ol class="ra-steps">
      <!-- Relay: nothing to set up -->
      <li v-if="relayMode" class="ra-step ra-step--done" data-testid="ra-relay-step">
        <span class="ra-step__num" aria-hidden="true"><SettingsGlyph name="check" :size="13" /></span>
        <div class="ra-step__body">
          <h4 class="ra-step__title">Eggify relay</h4>
          <p v-if="relayAddrShown" class="ra-step__desc">
            No account needed. This Eggify connects out to <code>{{ relayAddrShown }}</code>, which passes encrypted
            connections through without being able to open them. Your phone checks this Eggify's own certificate.
          </p>
          <p v-else class="ra-step__desc" data-testid="ra-relay-needed">
            This build has no default relay. A relay address is needed: enter the address of an Eggify relay below
            (a self-hosted relay runs <code>eggify-relay</code>).
          </p>
          <dl v-if="settings?.relay_pin" class="ra-facts">
            <dt>Certificate pin</dt><dd><code class="ra-token__hint" data-testid="ra-relay-pin">{{ settings.relay_pin }}</code></dd>
          </dl>
          <details class="ra-more ra-more--inline" :open="!relayAddrShown">
            <summary v-if="relayAddrShown">Use another relay <span class="ra-optional">optional</span></summary>
            <summary v-else>Relay address <span class="ra-optional">required</span></summary>
            <form class="ra-row ra-row--form" @submit.prevent="saveRelayAddr">
              <label class="sr-only" for="ra-relay-input">Relay address</label>
              <input
                id="ra-relay-input"
                v-model="relayAddrInput"
                type="text"
                class="ra-input ra-input--mono"
                :placeholder="settings?.default_relay_addr || 'relay.example.com:443'"
                autocomplete="off"
                spellcheck="false"
                data-testid="ra-relay-input"
              />
              <button type="submit" class="ra-btn ra-btn--secondary" :disabled="!relayDirty || busy" data-testid="ra-relay-save">
                Save
              </button>
            </form>
            <p v-if="settings?.default_relay_addr" class="ra-hint">Leave empty for this build's relay. A self-hosted relay runs <code>eggify-relay</code>.</p>
            <p v-else class="ra-hint">Host and port, for example <code>relay.example.com:8443</code>.</p>
          </details>
        </div>
      </li>

      <!-- Step 1: token -->
      <li v-if="!relayMode" class="ra-step" :class="{ 'ra-step--done': settings?.token_set }">
        <span class="ra-step__num" aria-hidden="true">
          <SettingsGlyph v-if="settings?.token_set" name="check" :size="13" />
          <template v-else>1</template>
        </span>
        <div class="ra-step__body">
          <h4 class="ra-step__title">Connect your ngrok account</h4>
          <p class="ra-step__desc">
            Eggify uses <strong>ngrok</strong> (free account) to give this machine a secure https address.
            Copy your authtoken from the ngrok dashboard and paste it here.
          </p>
          <template v-if="settings?.token_set && !replacingToken">
            <div class="ra-token" data-testid="ra-token-set">
              <span class="ra-token__ok"><SettingsGlyph name="checkCircle" :size="14" /> Token saved</span>
              <code class="ra-token__hint">{{ settings.token_hint }}</code>
              <span v-if="settings.token_source === 'env'" class="ra-token__src">from the environment</span>
            </div>
            <div class="ra-row">
              <button type="button" class="ra-btn ra-btn--secondary" data-testid="ra-token-replace" @click="replacingToken = true">
                <SettingsGlyph name="key" /> Replace token
              </button>
              <button
                v-if="settings.token_source === 'settings'"
                type="button"
                class="ra-btn ra-btn--ghost"
                data-testid="ra-token-remove"
                :disabled="busy"
                @click="removeToken"
              >
                Remove
              </button>
            </div>
          </template>
          <template v-else>
            <div class="ra-row">
              <button type="button" class="ra-btn ra-btn--secondary" data-testid="ra-open-ngrok" @click="openExternal(NGROK_TOKEN_URL)">
                <SettingsGlyph name="external" /> Get your authtoken
              </button>
            </div>
            <form class="ra-row ra-row--form" @submit.prevent="saveToken">
              <label class="sr-only" for="ra-token-input">ngrok authtoken</label>
              <input
                id="ra-token-input"
                v-model="tokenInput"
                type="password"
                class="ra-input ra-input--mono"
                placeholder="Paste your ngrok authtoken"
                autocomplete="off"
                spellcheck="false"
                data-testid="ra-token-input"
              />
              <button type="submit" class="ra-btn ra-btn--primary" :disabled="!tokenInput.trim() || busy" data-testid="ra-token-save">
                Save token
              </button>
              <button v-if="replacingToken" type="button" class="ra-btn ra-btn--ghost" @click="replacingToken = false; tokenInput = ''">
                Cancel
              </button>
            </form>
            <p class="ra-hint">Stored encrypted on this machine and never shown again, only its last 4 characters.</p>
          </template>
        </div>
      </li>

      <!-- Step 2: domain -->
      <li v-if="!relayMode" class="ra-step" :class="{ 'ra-step--done': !!settings?.domain }">
        <span class="ra-step__num" aria-hidden="true">2</span>
        <div class="ra-step__body">
          <h4 class="ra-step__title">Pick an address <span class="ra-optional">optional</span></h4>
          <p class="ra-step__desc">
            Leave empty for a new random address each time, or use your free static domain
            (ngrok dashboard → Domains) so the URL and QR code never change.
          </p>
          <form class="ra-row ra-row--form" @submit.prevent="saveDomain">
            <label class="sr-only" for="ra-domain-input">Reserved domain</label>
            <input
              id="ra-domain-input"
              v-model="domainInput"
              type="text"
              class="ra-input ra-input--mono"
              :placeholder="settings?.env_domain || 'e.g. my-eggify.ngrok-free.app'"
              autocomplete="off"
              spellcheck="false"
              data-testid="ra-domain-input"
            />
            <button type="submit" class="ra-btn ra-btn--secondary" :disabled="!domainDirty || busy" data-testid="ra-domain-save">
              Save
            </button>
          </form>
        </div>
      </li>

      <!-- Step 3: switch on -->
      <li class="ra-step" :class="{ 'ra-step--done': isOn }">
        <span class="ra-step__num" aria-hidden="true">{{ relayMode ? 2 : 3 }}</span>
        <div class="ra-step__body">
          <h4 class="ra-step__title">Turn it on</h4>
          <p class="ra-step__desc">Use the switch above. Eggify keeps running locally either way; turning remote access off closes only the public address.</p>
          <label class="ra-check">
            <input type="checkbox" :checked="settings?.autostart" :disabled="busy || !status?.settings" data-testid="ra-autostart" @change="onAutostart" />
            <span>Start remote access when Eggify starts</span>
          </label>
        </div>
      </li>
    </ol>

    <!-- Edge protection -->
    <details v-if="!relayMode" class="ra-more" :open="protectionMode !== 'none'">
      <summary>Extra protection at ngrok's edge <span class="ra-optional">optional</span></summary>
      <p class="ra-step__desc">
        ngrok can check visitors before they reach Eggify. Your Eggify login stays required either way.
      </p>
      <div class="ra-choices" role="radiogroup" aria-label="Edge protection">
        <label v-for="m in PROTECTION_MODES" :key="m.id" class="ra-choice" :class="{ 'ra-choice--on': protectionMode === m.id }">
          <input v-model="protectionMode" type="radio" name="ra-protection" :value="m.id" />
          <span class="ra-choice__title">{{ m.title }}</span>
          <span class="ra-choice__desc">{{ m.desc }}</span>
        </label>
      </div>
      <div v-if="protectionMode === 'oauth'" class="ra-grid">
        <label class="ra-field">
          <span class="ra-label">Sign in with</span>
          <select v-model="oauthProvider" class="ra-input">
            <option value="google">Google</option>
            <option value="github">GitHub</option>
            <option value="microsoft">Microsoft</option>
            <option value="gitlab">GitLab</option>
          </select>
        </label>
        <label class="ra-field ra-field--wide">
          <span class="ra-label">Allowed emails or domains (one per line)</span>
          <textarea v-model="oauthAllow" class="ra-input ra-input--mono" rows="3" placeholder="you@example.com&#10;example.com" spellcheck="false" />
        </label>
      </div>
      <div v-if="protectionMode === 'basic'" class="ra-grid">
        <label class="ra-field">
          <span class="ra-label">User name</span>
          <input v-model="basicUser" class="ra-input" autocomplete="off" />
        </label>
        <label class="ra-field">
          <span class="ra-label">Password</span>
          <input
            v-model="basicPassword"
            type="password"
            class="ra-input"
            autocomplete="new-password"
            :placeholder="settings?.protection?.basic_password_set ? 'Saved (leave empty to keep)' : '8-128 characters'"
          />
        </label>
        <SettingsNote class="ra-field--wide">Basic auth uses the Authorization header, so the iOS app can't connect through it. Prefer OAuth.</SettingsNote>
      </div>
      <div class="ra-row">
        <button type="button" class="ra-btn ra-btn--secondary" :disabled="busy || !protectionDirty" data-testid="ra-protection-save" @click="saveProtection">
          Save protection
        </button>
        <span v-if="isOn && protectionDirty" class="ra-hint">Saving reconnects the tunnel.</span>
      </div>
    </details>

    <!-- Connected ngrok agents (optional API key) -->
    <details v-if="!relayMode && !agentLimit && status?.settings" class="ra-more" data-testid="ra-agents-section" @toggle="agentsOpen = ($event.target as HTMLDetailsElement).open">
      <summary>Connected ngrok agents <span v-if="!settings?.api_key_set" class="ra-optional">optional</span></summary>
      <p class="ra-step__desc">
        Each connected ngrok agent uses one of your account's agent sessions (3 at once on the free plan).
        With an ngrok API key Eggify can list them and stop the ones you don't need.
      </p>
      <NgrokAgents
        v-if="agentsOpen"
        :api-key-set="!!settings?.api_key_set"
        :api-key-hint="settings?.api_key_hint"
        @changed="fetchStatus"
        @open="openExternal"
      />
    </details>

    <ul class="ra-notes">
      <li><SettingsGlyph name="lock" :size="13" /> Anyone who has the URL sees your Eggify sign-in page. Use a strong password and turn on two-factor authentication.</li>
      <li v-if="relayMode"><SettingsGlyph name="info" :size="13" /> The relay sees only which address is connected and when, never what is sent.</li>
      <li v-else><SettingsGlyph name="info" :size="13" /> Free ngrok addresses show an ngrok notice page on the first visit from a browser. That's expected.</li>
      <li><SettingsGlyph name="bolt" :size="13" /> Agents and terminals are reachable through the URL once you're signed in. Turn remote access off when you don't need it.</li>
    </ul>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, watch, onMounted, onUnmounted } from 'vue'
import { useTunnel, isAgentLimitError, type ProtectionMode, type TunnelMode } from '~/composables/useTunnel'
import { desktopOpenExternal } from '~/utils/desktop'
import SettingsGlyph from './Glyph.vue'
import NgrokAgents from './NgrokAgents.vue'
import SettingsNote from './Note.vue'
import SettingsQrCode from './QrCode.vue'
import SettingsPairDevice from './PairDevice.vue'

const NGROK_TOKEN_URL = 'https://dashboard.ngrok.com/get-started/your-authtoken'
const NGROK_AGENTS_URL = 'https://dashboard.ngrok.com/agents'

const PROTECTION_MODES: { id: ProtectionMode, title: string, desc: string }[] = [
  { id: 'none', title: 'Eggify login only', desc: 'Default. Works with the iOS app.' },
  { id: 'oauth', title: 'OAuth allow list', desc: 'Visitors sign in with Google/GitHub first.' },
  { id: 'basic', title: 'Basic auth', desc: 'A shared password in front. Breaks the iOS app.' },
]

const MODES: { id: TunnelMode, title: string, desc: string }[] = [
  { id: 'relay', title: 'Eggify relay', desc: 'No account. End-to-end encrypted, for the iOS app.' },
  { id: 'ngrok', title: 'Your ngrok account', desc: 'Works in any browser. ngrok decrypts traffic at its edge.' },
]

const {
  status, urlCopied, busy, copyUrl, startPolling, stopPolling,
  saveSettings, start, stop, deleteToken, fetchStatus,
} = useTunnel()

const settings = computed(() => status.value?.settings)
const actionError = ref('')
const tokenInput = ref('')
const replacingToken = ref(false)
const domainInput = ref('')
const protectionMode = ref<ProtectionMode>('none')
const oauthProvider = ref('google')
const oauthAllow = ref('')
const basicUser = ref('')
const basicPassword = ref('')
const now = ref(Date.now())
const agentsOpen = ref(false)
const retryNote = ref('')
const relayAddrInput = ref('')
let autoRetried = false
let clock: ReturnType<typeof setInterval> | null = null

// Sync the form from the server once (and after saves), not while typing.
const syncedFrom = ref('')
watch(settings, (s) => {
  if (!s) return
  const key = JSON.stringify([s.domain, s.protection, s.relay_addr])
  if (key === syncedFrom.value) return
  syncedFrom.value = key
  domainInput.value = s.domain || ''
  relayAddrInput.value = s.relay_addr || ''
  protectionMode.value = s.protection?.mode || 'none'
  oauthProvider.value = s.protection?.oauth_provider || 'google'
  oauthAllow.value = [...(s.protection?.allow_emails || []), ...(s.protection?.allow_domains || [])].join('\n')
  basicUser.value = s.protection?.basic_user || ''
  basicPassword.value = ''
}, { immediate: true })

const state = computed(() => status.value?.status ?? 'disconnected')
const mode = computed<TunnelMode>(() => settings.value?.mode === 'relay' ? 'relay' : 'ngrok')
const relayMode = computed(() => mode.value === 'relay')
const relayAddrShown = computed(() => settings.value?.relay_addr || settings.value?.default_relay_addr || '')
const relayDirty = computed(() => relayAddrInput.value.trim() !== (settings.value?.relay_addr || ''))
const isOn = computed(() => !!status.value?.running || state.value === 'connected' || state.value === 'connecting')

const kind = computed(() => {
  switch (state.value) {
    case 'connected': return 'ok'
    case 'connecting': return 'pending'
    case 'error': return 'error'
    default: return 'off'
  }
})

const statusLabel = computed(() => ({
  connected: 'Online',
  connecting: 'Connecting…',
  error: 'Error',
  disabled: 'Unavailable',
  disconnected: 'Off',
} as Record<string, string>)[state.value] ?? 'Off')

const statusLine = computed(() => {
  const s = status.value
  if (!s) return 'Checking…'
  switch (s.status) {
    case 'connected': return 'This Eggify is reachable from anywhere at the address below.'
    case 'connecting': return s.message || (relayMode.value ? 'Connecting to the relay…' : 'Connecting to ngrok…')
    case 'error': return isAgentLimitError(s)
      ? 'ngrok refused the connection: your account has no free agent sessions.'
      : 'Remote access stopped because of an error.'
    case 'disabled': return 'Remote access is not available on this server.'
    default:
      if (!relayMode.value && !settings.value?.token_set) return 'Reach this Eggify from your phone or another computer. Start with step 1.'
      return 'Remote access is off. Only this machine can reach Eggify.'
  }
})

const agentLimit = computed(() => state.value === 'error' && isAgentLimitError(status.value))

// Leaving the error state re-arms the one automatic retry after a stop.
watch(agentLimit, (on) => {
  if (!on) {
    autoRetried = false
    retryNote.value = ''
  }
})

async function retry() {
  retryNote.value = ''
  await run(start())
}

// After stopping an agent, try once more by itself: the freed session is
// usually available right away.
function onAgentStopped() {
  if (autoRetried) {
    retryNote.value = 'Stopped. Click "Try again" to connect.'
    return
  }
  autoRetried = true
  retryNote.value = 'Stopped. Trying again…'
  setTimeout(async () => {
    if (!agentLimit.value) return
    await run(start())
  }, 1200)
}

const blockedNotice = computed(() => {
  const b = status.value?.blocked
  if (!b || b.code === 'no_token' || b.code === 'no_relay_addr' || isOn.value) return ''
  return b.message
})

const switchDisabled = computed(() => {
  if (busy.value || !status.value || status.value.status === 'disabled') return true
  if (isOn.value) return false
  return status.value.can_start === false
})

const switchTitle = computed(() => {
  if (!status.value?.can_start && !isOn.value) return status.value?.blocked?.message || ''
  return ''
})

const connectedFor = computed(() => {
  const since = status.value?.connected_since
  if (!since) return ''
  const s = Math.max(0, Math.floor((now.value - new Date(since).getTime()) / 1000))
  if (s < 60) return 'just now'
  const m = Math.floor(s / 60)
  if (m < 60) return `${m} min`
  const h = Math.floor(m / 60)
  return `${h} h ${m % 60} min`
})

const protectionLabel = computed(() => {
  const p = settings.value?.protection
  if (!p || p.mode === 'none') return ''
  if (p.mode === 'oauth') return `OAuth (${p.oauth_provider})`
  return 'Basic auth'
})

const domainDirty = computed(() => domainInput.value.trim() !== (settings.value?.domain || ''))

const allowLines = computed(() => oauthAllow.value.split(/[\n,]/).map(s => s.trim()).filter(Boolean))

const protectionDirty = computed(() => {
  const p = settings.value?.protection
  if (!p) return false
  if (protectionMode.value !== p.mode) return true
  if (protectionMode.value === 'oauth') {
    const cur = [...(p.allow_emails || []), ...(p.allow_domains || [])].join('\n')
    return oauthProvider.value !== p.oauth_provider || allowLines.value.join('\n') !== cur
  }
  if (protectionMode.value === 'basic') return basicUser.value !== (p.basic_user || '') || basicPassword.value !== ''
  return false
})

async function run(p: Promise<{ ok: boolean, error?: string }>) {
  actionError.value = ''
  const r = await p
  if (!r.ok) actionError.value = r.error || 'Something went wrong'
  return r.ok
}

async function onToggle(e: Event) {
  const input = e.target as HTMLInputElement
  const want = input.checked
  input.checked = isOn.value // reflect the server's state, not the click
  await run(want ? start() : stop())
}

async function saveToken() {
  if (await run(saveSettings({ authtoken: tokenInput.value.trim() }))) {
    tokenInput.value = ''
    replacingToken.value = false
  }
}

async function removeToken() {
  await run(deleteToken())
}

async function saveMode(next: TunnelMode) {
  if (next === mode.value) return
  await run(saveSettings({ mode: next }))
}

async function saveRelayAddr() {
  await run(saveSettings({ relay_addr: relayAddrInput.value.trim() }))
}

async function saveDomain() {
  await run(saveSettings({ domain: domainInput.value.trim() }))
}

async function onAutostart(e: Event) {
  await run(saveSettings({ autostart: (e.target as HTMLInputElement).checked }))
}

async function saveProtection() {
  const mode = protectionMode.value
  const protection: any = { mode }
  if (mode === 'oauth') {
    protection.oauth_provider = oauthProvider.value
    protection.allow_emails = allowLines.value.filter(l => l.includes('@') && !l.startsWith('@'))
    protection.allow_domains = allowLines.value.filter(l => !l.includes('@') || l.startsWith('@'))
  } else if (mode === 'basic') {
    protection.basic_user = basicUser.value
    if (basicPassword.value) protection.basic_password = basicPassword.value
  }
  if (await run(saveSettings({ protection }))) basicPassword.value = ''
}

async function openExternal(url: string) {
  try {
    if (await desktopOpenExternal(url)) return
  } catch { /* fall back to the browser */ }
  window.open(url, '_blank', 'noopener,noreferrer')
}

onMounted(() => {
  startPolling(4000)
  clock = setInterval(() => { now.value = Date.now() }, 30000)
})
onUnmounted(() => {
  stopPolling()
  if (clock) clearInterval(clock)
})
</script>

<style scoped>
.ra {
  display: flex;
  flex-direction: column;
  gap: 14px;
  padding: 16px 0 6px;
}

.sr-only {
  position: absolute;
  width: 1px;
  height: 1px;
  overflow: hidden;
  clip: rect(0 0 0 0);
  white-space: nowrap;
}

/* ===== Hero ===== */
.ra-hero {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  padding: 16px 18px;
  border: 1px solid var(--border-color);
  border-radius: 12px;
  background: var(--bg-secondary);
  transition: border-color 0.2s, background 0.2s;
}

.ra-hero--ok {
  border-color: color-mix(in srgb, var(--status-success, #10b981) 45%, var(--border-color));
  background: color-mix(in srgb, var(--status-success, #10b981) 7%, var(--bg-secondary));
}

.ra-hero--pending {
  border-color: color-mix(in srgb, var(--status-warning, #f59e0b) 45%, var(--border-color));
}

.ra-hero--error {
  border-color: color-mix(in srgb, var(--status-error, #ef4444) 45%, var(--border-color));
}

.ra-hero__text {
  min-width: 0;
}

.ra-hero__line {
  margin: 6px 0 0;
  font-size: 0.82rem;
  line-height: 1.45;
  color: var(--text-secondary);
}

/* Badge */
.ra-badge {
  display: inline-flex;
  align-items: center;
  gap: 7px;
  padding: 3px 11px;
  border-radius: 999px;
  font-size: 0.8rem;
  font-weight: 650;
  color: var(--text-secondary);
  border: 1px solid var(--border-color);
  background: var(--bg-primary);
}

.ra-badge__dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background: currentColor;
}

.ra-badge--ok { color: var(--status-success, #10b981); border-color: color-mix(in srgb, currentColor 35%, transparent); }
.ra-badge--pending { color: var(--status-warning, #f59e0b); border-color: color-mix(in srgb, currentColor 35%, transparent); }
.ra-badge--pending .ra-badge__dot { animation: ra-pulse 1.1s ease-in-out infinite; }
.ra-badge--error { color: var(--status-error, #ef4444); border-color: color-mix(in srgb, currentColor 35%, transparent); }

@keyframes ra-pulse {
  50% { opacity: 0.3; }
}

/* Big switch */
.ra-switch {
  position: relative;
  flex-shrink: 0;
  width: 58px;
  height: 32px;
  cursor: pointer;
}

.ra-switch input {
  position: absolute;
  inset: 0;
  width: 100%;
  height: 100%;
  margin: 0;
  opacity: 0;
  cursor: inherit;
  z-index: 1;
}

.ra-switch__track {
  position: absolute;
  inset: 0;
  border-radius: 16px;
  background: var(--border-color, #555);
  transition: background 0.2s;
  pointer-events: none;
}

.ra-switch__track::after {
  content: '';
  position: absolute;
  top: 4px;
  left: 4px;
  width: 24px;
  height: 24px;
  border-radius: 50%;
  background: #fff;
  box-shadow: 0 1px 3px rgba(0, 0, 0, 0.25);
  transition: transform 0.2s;
}

.ra-switch input:checked + .ra-switch__track {
  background: var(--status-success, #10b981);
}

.ra-hero--pending .ra-switch input:checked + .ra-switch__track {
  background: var(--status-warning, #f59e0b);
}

.ra-switch input:checked + .ra-switch__track::after {
  transform: translateX(26px);
}

.ra-switch input:focus-visible + .ra-switch__track {
  outline: 2px solid var(--accent-purple);
  outline-offset: 2px;
}

.ra-switch--disabled {
  cursor: not-allowed;
  opacity: 0.5;
}

/* ===== Live card ===== */
.ra-live {
  display: flex;
  gap: 20px;
  align-items: center;
  justify-content: space-between;
  padding: 16px 18px;
  border: 1px solid var(--border-color);
  border-radius: 12px;
  background: var(--bg-primary);
}

.ra-live__info {
  min-width: 0;
  flex: 1;
}

.ra-label {
  display: block;
  margin-bottom: 4px;
  font-size: 0.72rem;
  font-weight: 600;
  letter-spacing: 0.04em;
  text-transform: uppercase;
  color: var(--text-secondary);
}

.ra-url {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
}

.ra-url__link {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-family: var(--font-mono, ui-monospace, monospace);
  font-size: 0.9rem;
  font-weight: 600;
  color: var(--accent-purple);
  text-decoration: none;
}

.ra-url__link:hover {
  text-decoration: underline;
}

.ra-facts {
  display: grid;
  grid-template-columns: max-content 1fr;
  gap: 4px 14px;
  margin: 12px 0 0;
  font-size: 0.78rem;
}

.ra-facts dt { color: var(--text-secondary); }
.ra-facts dd { margin: 0; color: var(--text-primary); }

.ra-qr {
  flex-shrink: 0;
  padding: 6px;
  border-radius: 12px;
  background: #fff;
  box-shadow: 0 0 0 1px var(--border-color);
}

/* ===== Steps ===== */
.ra-steps {
  list-style: none;
  margin: 0;
  padding: 0;
  display: flex;
  flex-direction: column;
}

.ra-step {
  display: grid;
  grid-template-columns: 28px minmax(0, 1fr);
  gap: 14px;
  padding: 14px 0;
  border-top: 1px dashed var(--border-color);
}

.ra-step:first-child {
  border-top: none;
}

.ra-step__num {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 26px;
  height: 26px;
  border-radius: 50%;
  font-size: 0.8rem;
  font-weight: 700;
  color: var(--accent-purple);
  border: 1.5px solid var(--accent-purple);
  background: var(--bg-primary);
}

.ra-step--done .ra-step__num {
  color: var(--bg-primary, #fff);
  border-color: var(--status-success, #10b981);
  background: var(--status-success, #10b981);
}

.ra-step__title {
  margin: 2px 0 0;
  font-size: 0.9rem;
  font-weight: 600;
  color: var(--text-primary);
}

.ra-optional {
  margin-left: 6px;
  padding: 1px 7px;
  border-radius: 999px;
  font-size: 0.68rem;
  font-weight: 500;
  color: var(--text-secondary);
  border: 1px solid var(--border-color);
  vertical-align: 1px;
}

.ra-step__desc {
  margin: 4px 0 10px;
  font-size: 0.8rem;
  line-height: 1.5;
  color: var(--text-secondary);
}

.ra-row {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 8px;
  margin-top: 8px;
}

.ra-row--form .ra-input {
  flex: 1 1 260px;
}

.ra-hint {
  margin: 8px 0 0;
  font-size: 0.74rem;
  line-height: 1.45;
  color: var(--text-secondary);
}

.ra-token {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 10px;
  padding: 8px 12px;
  border-radius: 8px;
  background: var(--bg-secondary);
  font-size: 0.82rem;
}

.ra-token__ok {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  font-weight: 600;
  color: var(--status-success, #10b981);
}

.ra-token__hint {
  font-family: var(--font-mono, ui-monospace, monospace);
  color: var(--text-primary);
}

.ra-token__src {
  color: var(--text-secondary);
  font-size: 0.75rem;
}

.ra-check {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  font-size: 0.82rem;
  color: var(--text-primary);
  cursor: pointer;
}

.ra-check input {
  width: 16px;
  height: 16px;
  accent-color: var(--accent-purple);
}

/* ===== Buttons & inputs ===== */
.ra-btn {
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

.ra-btn:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

.ra-btn--primary {
  color: #fff;
  background: var(--accent-purple);
  border: 1px solid var(--accent-purple);
}

.ra-btn--primary:hover:not(:disabled) {
  filter: brightness(1.08);
}

.ra-btn--secondary {
  color: var(--text-primary);
  background: var(--bg-secondary);
  border: 1px solid var(--border-color);
}

.ra-btn--secondary:hover:not(:disabled) {
  color: var(--accent-purple);
  border-color: var(--accent-purple);
}

.ra-btn--ghost {
  color: var(--text-secondary);
  background: transparent;
  border: 1px solid transparent;
}

.ra-btn--ghost:hover:not(:disabled) {
  color: var(--status-error, #ef4444);
}

.ra-btn :deep(svg) {
  flex-shrink: 0;
  opacity: 0.85;
}

.ra-icon-btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
  width: 28px;
  height: 28px;
  padding: 0;
  border: 1px solid var(--border-color);
  border-radius: 6px;
  color: var(--text-secondary);
  background: var(--bg-secondary);
  cursor: pointer;
}

.ra-icon-btn:hover {
  color: var(--accent-purple);
  border-color: var(--accent-purple);
}

.ra-input {
  width: 100%;
  padding: 8px 12px;
  border: 1px solid var(--border-color);
  border-radius: 8px;
  font: inherit;
  font-size: 0.85rem;
  color: var(--text-primary);
  background: var(--bg-primary);
  transition: border-color 0.15s, box-shadow 0.15s;
}

.ra-input:focus {
  outline: none;
  border-color: var(--accent-purple);
  box-shadow: 0 0 0 3px color-mix(in srgb, var(--accent-purple) 18%, transparent);
}

.ra-input::placeholder {
  color: var(--text-muted, var(--text-secondary));
}

.ra-input--mono {
  font-family: var(--font-mono, ui-monospace, monospace);
  font-size: 0.8rem;
}

textarea.ra-input {
  resize: vertical;
  line-height: 1.5;
}

/* ===== Edge protection ===== */
.ra-more {
  padding: 12px 14px;
  border: 1px solid var(--border-color);
  border-radius: 10px;
}

.ra-more summary {
  cursor: pointer;
  font-size: 0.85rem;
  font-weight: 600;
  color: var(--text-primary);
}

.ra-more[open] summary {
  margin-bottom: 4px;
}

.ra-more--inline {
  padding: 6px 0 0;
  border: none;
}

.ra-choices {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(170px, 1fr));
  gap: 8px;
}

.ra-choice {
  position: relative;
  display: flex;
  flex-direction: column;
  gap: 2px;
  padding: 10px 12px;
  border: 1px solid var(--border-color);
  border-radius: 8px;
  cursor: pointer;
  background: var(--bg-primary);
}

.ra-choice input {
  position: absolute;
  opacity: 0;
  pointer-events: none;
}

.ra-choice:focus-within {
  outline: 2px solid var(--accent-purple);
  outline-offset: 1px;
}

.ra-choice--on {
  border-color: var(--accent-purple);
  box-shadow: inset 0 0 0 1px var(--accent-purple);
}

.ra-choice__title {
  font-size: 0.82rem;
  font-weight: 600;
  color: var(--text-primary);
}

.ra-choice__desc {
  font-size: 0.74rem;
  color: var(--text-secondary);
}

.ra-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 10px;
  margin-top: 12px;
}

.ra-field {
  display: flex;
  flex-direction: column;
}

.ra-field--wide {
  grid-column: 1 / -1;
}

/* ===== Agent limit (ERR_NGROK_108) ===== */
.ra-limit {
  padding: 14px 16px;
  border: 1px solid color-mix(in srgb, var(--status-error, #ef4444) 40%, var(--border-color));
  border-radius: 12px;
  background: color-mix(in srgb, var(--status-error, #ef4444) 5%, var(--bg-secondary));
}

.ra-limit__head {
  display: flex;
  align-items: center;
  gap: 8px;
  color: var(--status-error, #ef4444);
}

.ra-limit__title {
  margin: 0;
  font-size: 0.92rem;
  font-weight: 650;
  color: var(--text-primary);
}

.ra-limit__text {
  margin: 6px 0 0;
  font-size: 0.8rem;
  line-height: 1.55;
  color: var(--text-secondary);
}

.ra-limit__agents {
  margin-top: 12px;
  padding-top: 12px;
  border-top: 1px dashed var(--border-color);
}

.ra-limit__raw {
  margin-top: 10px;
  font-size: 0.72rem;
  color: var(--text-secondary);
}

.ra-limit__raw summary {
  cursor: pointer;
}

.ra-limit__raw code {
  display: block;
  margin-top: 4px;
  font-family: var(--font-mono, ui-monospace, monospace);
  line-height: 1.5;
  word-break: break-word;
}

/* ===== Notes ===== */
.ra-notes {
  list-style: none;
  margin: 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: 6px;
  font-size: 0.76rem;
  line-height: 1.45;
  color: var(--text-secondary);
}

.ra-notes li {
  display: flex;
  gap: 8px;
}

.ra-notes :deep(svg) {
  flex-shrink: 0;
  margin-top: 2px;
}

@media (max-width: 640px) {
  .ra-live {
    flex-direction: column-reverse;
    align-items: stretch;
  }
  .ra-qr {
    align-self: center;
  }
  .ra-grid {
    grid-template-columns: minmax(0, 1fr);
  }
}
</style>
