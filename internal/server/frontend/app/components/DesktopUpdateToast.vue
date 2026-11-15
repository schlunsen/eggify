<template>
  <!--
    Wails desktop app only. When a new version is out, a dialog says so right
    away; "Update & restart" downloads, verifies, reinstalls and relaunches in
    one go. "Later" folds it into a small pill that stays visible.
  -->
  <Teleport to="body">
    <Transition name="update-dialog">
      <div v-if="open" class="update-backdrop" @keydown.esc.prevent="later">
        <section
          ref="dialog"
          class="update-dialog"
          role="dialog"
          aria-modal="true"
          aria-labelledby="update-title"
          aria-describedby="update-desc"
          tabindex="-1"
        >
          <img class="update-egg" src="/favicon.svg" alt="" aria-hidden="true">

          <template v-if="mode === 'progress'">
            <h2 id="update-title">Updating Eggify</h2>
            <p id="update-desc" class="update-desc">{{ progressLabel }}</p>
            <div class="update-bar" role="progressbar" :aria-valuenow="Math.round(percent)" aria-valuemin="0" aria-valuemax="100">
              <div class="update-fill" :class="{ indeterminate: state?.phase !== 'downloading' }" :style="{ width: `${state?.phase === 'downloading' ? percent : 100}%` }" />
            </div>
            <p class="update-note">Eggify restarts by itself when it's done. Your sessions are kept.</p>
          </template>

          <template v-else-if="mode === 'error' && blockedByAppManagement">
            <h2 id="update-title">macOS blocked the update</h2>
            <p id="update-desc" class="update-desc">
              App Management stopped Eggify from replacing itself, so nothing changed.
              Allow Eggify under <strong>System Settings → Privacy &amp; Security → App Management</strong>, then try again.
              Or download the installer and drag Eggify to Applications.
            </p>
            <div class="update-actions">
              <button ref="primary" type="button" class="update-primary" @click="openAppManagement">Open App Management settings</button>
              <button type="button" class="update-secondary" @click="install">Try again</button>
              <button type="button" class="update-secondary" @click="download">Download the installer</button>
              <button type="button" class="update-link" @click="later">Later</button>
            </div>
          </template>

          <template v-else-if="mode === 'error'">
            <h2 id="update-title">The update didn't go through</h2>
            <p id="update-desc" class="update-desc">{{ state?.error }}</p>
            <div class="update-actions">
              <button type="button" class="update-primary" @click="install">Try again</button>
              <button type="button" class="update-secondary" @click="download">Download the installer</button>
              <button type="button" class="update-link" @click="later">Later</button>
            </div>
          </template>

          <template v-else-if="mode === 'manual'">
            <h2 id="update-title">Eggify {{ state?.latest_version }} is available</h2>
            <p id="update-desc" class="update-desc">
              You have {{ state?.current_version }}. Eggify is running from the disk image, so it can't update itself.
              Move it to Applications, or download the new version.
            </p>
            <div class="update-actions">
              <button type="button" class="update-primary" @click="download">Download {{ state?.latest_version }}</button>
              <button type="button" class="update-link" @click="later">Later</button>
            </div>
          </template>

          <template v-else>
            <h2 id="update-title">Eggify {{ state?.latest_version }} is ready</h2>
            <p id="update-desc" class="update-desc">
              You have {{ state?.current_version }}. Eggify downloads the update, checks it's signed by us, reinstalls itself and restarts.
            </p>
            <p v-if="workingNote" class="update-working">{{ workingNote }}</p>
            <a v-if="state?.release_url" class="update-notes" href="#" @click.prevent="openNotes">What's new</a>
            <div class="update-actions">
              <button ref="primary" type="button" class="update-primary" @click="install">Update &amp; restart</button>
              <button type="button" class="update-link" @click="later">Later</button>
            </div>
          </template>
        </section>
      </div>
    </Transition>

    <button v-if="pill" type="button" class="update-pill" @click="reopen">
      <span class="update-pill-dot" aria-hidden="true" />
      Eggify {{ state?.latest_version }} is ready <strong>Update</strong>
    </button>
  </Teleport>
</template>

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useAuthenticatedFetch } from '~/composables/useAuthenticatedFetch'
import {
  desktopInstallUpdate,
  desktopOpenAppManagementSettings,
  desktopOpenExternal,
  desktopShell,
  desktopUpdateState,
  onDesktopUpdate,
  onDesktopUpdateShow,
  onLocalUpdateShow,
  updateToastMode,
  type DesktopUpdateState,
} from '~/utils/desktop'

const POLL_MS = 15 * 60 * 1000

const state = ref<DesktopUpdateState | null>(null)
/** The version you said "Later" to. In memory only: it asks again next launch. */
const later_ = ref<string | null>(null)
const dialog = ref<HTMLElement | null>(null)
const primary = ref<HTMLButtonElement | null>(null)
let unsubscribe: (() => void) | null = null
let unsubscribeShow: (() => void) | null = null
let unsubscribeLocalShow: (() => void) | null = null
let pollTimer: ReturnType<typeof setInterval> | null = null

const { fetchWithAuth } = useAuthenticatedFetch()
/** Sessions working right now, and whether they continue by themselves after the update. */
const working = ref(0)
const autoResume = ref(true)

const workingNote = computed(() => {
  const n = working.value
  if (n <= 0) return ''
  const sessions = n === 1 ? '1 working session' : `${n} working sessions`
  return autoResume.value
    ? `Updating restarts Eggify. ${sessions} will continue after the restart.`
    : `Updating restarts Eggify. ${sessions} will stop; you can continue ${n === 1 ? 'it' : 'them'} afterwards.`
})

async function loadWorking() {
  try {
    const res = await fetchWithAuth('/api/agent/resume-after-restart')
    if (!res.ok) return
    const body = await res.json()
    working.value = Number(body?.working_now) || 0
    autoResume.value = body?.auto_resume !== false
  } catch { /* the note is optional */ }
}

const mode = computed(() => updateToastMode(state.value, null))
const putOff = computed(() => !!later_.value && later_.value === state.value?.latest_version)
/** The dialog shows for a new version, and always while updating or after a failure. */
const open = computed(() => mode.value === 'progress' || mode.value === 'error' || ((mode.value === 'available' || mode.value === 'manual') && !putOff.value))
const pill = computed(() => (mode.value === 'available' || mode.value === 'manual') && putOff.value)
const percent = computed(() => Math.min(Math.max(state.value?.percent || 0, 0), 100))

const progressLabel = computed(() => {
  switch (state.value?.phase) {
    case 'downloading': return `Downloading Eggify ${state.value.latest_version}… ${Math.round(percent.value)}%`
    case 'verifying': return 'Checking the signature…'
    case 'installing': return 'Installing…'
    case 'restarting': return 'Restarting Eggify…'
    default: return 'Updating…'
  }
})

async function refresh() {
  try {
    const s = await desktopUpdateState()
    if (s) state.value = s
  } catch { /* runtime not ready yet */ }
}

async function install() {
  if (state.value) state.value = { ...state.value, phase: 'downloading', percent: 0, error: undefined, error_code: undefined }
  try {
    await desktopInstallUpdate()
  } catch (err: any) {
    const message = err?.message || String(err)
    if (state.value) state.value = { ...state.value, phase: 'error', error: message }
  }
  await refresh()
}

/** macOS App Management refused the bundle swap (updater error_code). */
const blockedByAppManagement = computed(() => state.value?.error_code === 'app_management')

/** The installer for users who can't self-update: the update status carries the
 *  URL (the build's download URL, or the manifest's DMG); else the release page. */
function download() {
  const url = state.value?.download_url || state.value?.release_url
  if (url) desktopOpenExternal(url).catch(() => {})
}

function openAppManagement() {
  desktopOpenAppManagementSettings().catch(() => {})
}

function openNotes() {
  if (state.value?.release_url) desktopOpenExternal(state.value.release_url).catch(() => {})
}

function later() {
  if (mode.value === 'progress') return // can't stop half way
  later_.value = state.value?.latest_version || null
  if (state.value?.phase === 'error') state.value = { ...state.value, phase: 'idle', error: undefined, error_code: undefined }
}

function reopen() {
  later_.value = null
}

// Focus the main button when the dialog opens, so Enter updates.
watch(open, async (isOpen) => {
  if (!isOpen) return
  if (mode.value === 'available') loadWorking()
  await nextTick()
  ;(primary.value || dialog.value)?.focus()
})

onMounted(async () => {
  if (desktopShell() !== 'wails') return
  await refresh()
  unsubscribeLocalShow = onLocalUpdateShow(() => { later_.value = null; refresh() })
  try {
    unsubscribe = await onDesktopUpdate((s) => { state.value = s })
    unsubscribeShow = await onDesktopUpdateShow(() => { later_.value = null; refresh() })
  } catch { /* events unavailable: polling still works */ }
  pollTimer = setInterval(refresh, POLL_MS)
})

onBeforeUnmount(() => {
  unsubscribe?.()
  unsubscribeShow?.()
  unsubscribeLocalShow?.()
  if (pollTimer) clearInterval(pollTimer)
})
</script>

<style scoped>
.update-working {
  margin: 10px 0 0;
  font-size: 13px;
  color: var(--text-secondary, #b8b8cc);
}
.update-backdrop {
  position: fixed;
  inset: 0;
  z-index: 10000;
  display: grid;
  place-items: center;
  padding: 16px;
  background: rgba(20, 14, 8, 0.45);
  backdrop-filter: blur(3px);
}
.update-dialog {
  position: relative;
  width: min(440px, 100%);
  padding: 28px 28px 22px;
  border-radius: 14px;
  background: var(--card-bg, #1a1a2e);
  color: var(--text-primary, #e6e6f0);
  border: 1px solid var(--border-color, #33334d);
  box-shadow: 0 24px 60px rgba(0, 0, 0, 0.35), 6px 8px 0 color-mix(in srgb, var(--accent-purple, #a78bfa) 35%, transparent);
  text-align: center;
  outline: none;
}
.update-egg { width: 64px; height: 64px; margin: -60px auto 10px; display: block; filter: drop-shadow(0 6px 12px rgba(0, 0, 0, 0.25)); }
h2 { margin: 0 0 8px; font-size: 21px; font-weight: 700; font-family: var(--font-serif, inherit); }
.update-desc { margin: 0 0 6px; font-size: 14px; line-height: 1.5; color: var(--text-secondary, #b8b8c8); }
.update-note { margin: 10px 0 0; font-size: 12px; color: var(--text-muted, #9a9ab0); }
.update-notes { display: inline-block; margin: 2px 0 4px; font-size: 13px; color: var(--accent-purple, #a78bfa); }
.update-actions { display: flex; flex-direction: column; gap: 8px; margin-top: 18px; }
.update-primary {
  border: none;
  border-radius: 9px;
  padding: 12px 16px;
  font-size: 15px;
  font-weight: 700;
  background: var(--accent-purple, #a78bfa);
  color: #fff;
  cursor: pointer;
}
.update-primary:hover { background: var(--accent-purple-hover, var(--accent-purple, #a78bfa)); filter: brightness(1.05); }
.update-primary:focus-visible, .update-secondary:focus-visible, .update-link:focus-visible, .update-pill:focus-visible {
  outline: 2px solid var(--accent-purple, #a78bfa);
  outline-offset: 2px;
}
.update-secondary {
  border: 1px solid var(--border-color, #33334d);
  border-radius: 9px;
  padding: 10px 16px;
  font-size: 14px;
  background: transparent;
  color: var(--text-primary, #e6e6f0);
  cursor: pointer;
}
.update-link { border: none; background: none; padding: 6px; font-size: 13px; color: var(--text-muted, #9a9ab0); cursor: pointer; }
.update-link:hover { color: var(--text-primary, #e6e6f0); }

.update-bar { height: 8px; margin: 14px 0 4px; border-radius: 99px; overflow: hidden; background: var(--bg-tertiary, #2a2a3e); }
.update-fill { height: 100%; border-radius: 99px; background: var(--accent-purple, #a78bfa); transition: width 0.3s ease; }
.update-fill.indeterminate { animation: update-pulse 1.2s ease-in-out infinite; }
@keyframes update-pulse { 50% { opacity: 0.45; } }

.update-pill {
  position: fixed;
  left: 16px;
  bottom: 16px;
  z-index: 9999;
  display: inline-flex;
  align-items: center;
  gap: 8px;
  padding: 9px 14px;
  border-radius: 99px;
  border: 1px solid var(--border-color, #33334d);
  background: var(--card-bg, #1a1a2e);
  color: var(--text-primary, #e6e6f0);
  box-shadow: 0 8px 24px rgba(0, 0, 0, 0.25);
  font-size: 13px;
  cursor: pointer;
}
.update-pill strong { color: var(--accent-purple, #a78bfa); }
.update-pill-dot { width: 8px; height: 8px; border-radius: 50%; background: var(--accent-purple, #a78bfa); animation: update-pulse 1.6s ease-in-out infinite; }

.update-dialog-enter-active, .update-dialog-leave-active { transition: opacity 0.2s ease; }
.update-dialog-enter-active .update-dialog { transition: transform 0.25s cubic-bezier(0.34, 1.56, 0.64, 1); }
.update-dialog-enter-from, .update-dialog-leave-to { opacity: 0; }
.update-dialog-enter-from .update-dialog { transform: scale(0.94) translateY(8px); }

@media (prefers-reduced-motion: reduce) {
  .update-fill.indeterminate, .update-pill-dot { animation: none; }
  .update-dialog-enter-active, .update-dialog-leave-active, .update-dialog-enter-active .update-dialog { transition: none; }
}
</style>
