<template>
  <div ref="scrollEl" class="settings-page">
    <div class="settings-shell">
      <header class="settings-header">
        <h1>Settings</h1>
        <p>Configure your Eggify experience. Changes are saved automatically.</p>
      </header>

      <div class="settings-layout">
        <!-- Section navigation (sticky sidebar on wide screens, chip bar on narrow ones) -->
        <nav class="settings-nav" aria-label="Settings sections">
          <a
            v-for="item in navItems"
            :key="item.id"
            :href="`#${item.id}`"
            class="settings-nav__item"
            :class="{ 'settings-nav__item--active': activeSection === item.id }"
            :aria-current="activeSection === item.id ? 'true' : undefined"
            @click.prevent="scrollToSection(item.id)"
          >
            <SettingsGlyph :name="item.icon" :size="15" />
            <span>{{ item.label }}</span>
          </a>
        </nav>

        <div class="settings-content">
          <!-- Account & Security -->
          <SettingsSection id="account" icon="lock" title="Account & security" subtitle="Credentials, two-factor authentication and users">
            <SettingsRow
              title="Password"
              description="Update your password to keep your account secure. Must be at least 8 characters."
            >
              <button
                type="button"
                class="btn btn--secondary"
                :aria-expanded="showPasswordForm"
                aria-controls="password-form"
                @click="togglePasswordForm"
              >
                <SettingsGlyph name="key" />
                {{ showPasswordForm ? 'Cancel' : 'Change password' }}
              </button>
              <template #below>
                <form
                  v-if="showPasswordForm"
                  id="password-form"
                  class="password-form"
                  @submit.prevent="handleChangePassword"
                >
                  <!-- Lets password managers update the right saved login -->
                  <input
                    type="text"
                    name="username"
                    autocomplete="username"
                    :value="user?.username ?? ''"
                    readonly
                    style="position: absolute; width: 1px; height: 1px; opacity: 0; overflow: hidden; pointer-events: none"
                    tabindex="-1"
                    aria-hidden="true"
                  />
                  <div class="field field--wide">
                    <label for="current-password" class="field__label">Current password</label>
                    <input
                      id="current-password"
                      v-model="currentPassword"
                      type="password"
                      class="input"
                      placeholder="Enter your current password"
                      :disabled="changePasswordLoading"
                      autocomplete="current-password"
                    />
                  </div>
                  <div class="field">
                    <label for="new-password" class="field__label">New password</label>
                    <input
                      id="new-password"
                      v-model="newPassword"
                      type="password"
                      class="input"
                      placeholder="At least 8 characters"
                      :disabled="changePasswordLoading"
                      autocomplete="new-password"
                    />
                  </div>
                  <div class="field">
                    <label for="confirm-password" class="field__label">Confirm new password</label>
                    <input
                      id="confirm-password"
                      v-model="confirmPassword"
                      type="password"
                      class="input"
                      placeholder="Repeat the new password"
                      :disabled="changePasswordLoading"
                      autocomplete="new-password"
                    />
                  </div>
                  <div class="password-form__actions">
                    <button type="submit" class="btn btn--primary" :disabled="!canChangePassword || changePasswordLoading">
                      {{ changePasswordLoading ? 'Updating…' : 'Update password' }}
                    </button>
                    <span class="hint">Minimum 8 characters, must match confirmation</span>
                  </div>
                </form>
                <SettingsNote v-if="passwordError" variant="error">{{ passwordError }}</SettingsNote>
                <SettingsNote v-if="passwordSuccess" variant="success">{{ passwordSuccess }}</SettingsNote>
              </template>
            </SettingsRow>

            <SettingsRow stacked>
              <MFAStatus
                :key="mfaStatusKey"
                @enable-mfa="showMFASetup = true"
                @audit-log="showMFAAuditLog = true"
                @status-changed="refreshMFAStatus"
              />
            </SettingsRow>

            <SettingsRow
              v-if="user?.isAdmin"
              title="User management"
              description="Create, view and delete user accounts. Admin only."
            >
              <NuxtLink to="/admin/users" class="btn btn--secondary">
                <SettingsGlyph name="users" />
                Manage users
              </NuxtLink>
            </SettingsRow>

            <SettingsRow stacked>
              <SettingsPairDevice />
            </SettingsRow>

            <SettingsRow
              stacked
              title="Signed-in devices"
              description="Browsers and phones signed in as you. Sign out any you don't recognise."
            >
              <SettingsSignedInDevices />
            </SettingsRow>
          </SettingsSection>

          <!-- Appearance -->
          <SettingsSection id="appearance" icon="palette" title="Appearance" subtitle="Theme and project color indicator">
            <SettingsRow
              title="Theme"
              description="Choose a pre-built theme or create your own color scheme."
            >
              <NuxtLink to="/themes" class="btn btn--secondary">
                <SettingsGlyph name="palette" />
                Open theme editor
              </NuxtLink>
            </SettingsRow>

            <SettingsRow
              title="Project color"
              description="How the current project's color is shown on the sidebar and navbar."
              stacked
            >
              <SettingsChoiceGroup
                v-model="projectColorStyle"
                name="project-color-style"
                label="Project color style"
                :options="projectColorStyleOptions"
                @change="saveProjectColorStyle"
              />
            </SettingsRow>

            <SettingsRow
              v-if="projectColorStyle !== 'off'"
              title="Glow animation"
              description="Add a subtle pulsing glow to the project color."
            >
              <label class="switch">
                <input
                  type="checkbox"
                  :checked="projectColorGlow === 'on'"
                  aria-label="Glow animation"
                  @change="setProjectColorGlow(($event.target as HTMLInputElement).checked)"
                />
                <span class="switch__track" />
              </label>
            </SettingsRow>
          </SettingsSection>

          <!-- Keyboard shortcuts -->
          <SettingsSection id="shortcuts" icon="keyboard" title="Keyboard shortcuts" subtitle="Change the keys of any shortcut, or remove ones you don't use">
            <SettingsKeyboardShortcuts />
          </SettingsSection>

          <!-- Agents -->
          <SettingsSection id="agents" icon="bot" title="Agents" subtitle="Defaults for new sessions and how results are shown">
            <SettingsRow
              title="Default permission mode"
              description="Used for Codex and other non-Claude-CLI sessions. For Claude sessions the “Create new session” dialog starts on your last choice of Autopilot, Ask first or Full access, and on Autopilot the first time."
              stacked
            >
              <SettingsChoiceGroup
                v-model="defaultPermissionMode"
                name="permission-mode"
                label="Default permission mode"
                :columns="2"
                :options="permissionModeOptions"
                @change="saveDefaultPermissionMode"
              />
            </SettingsRow>

            <SettingsRow
              title="Diff display"
              description="Where file edit diffs appear: inline in the conversation or in a collapsible overlay."
              stacked
            >
              <SettingsChoiceGroup
                v-model="diffDisplayLocation"
                name="diff-location"
                label="Diff display location"
                :options="diffLocationOptions"
                @change="saveDiffDisplayLocation"
              />
            </SettingsRow>

            <SettingsRow
              title="Auto-tag sessions"
              description="Generate tags automatically after 3 messages using a local in-browser LLM (~360MB, WebGPU)."
            >
              <label class="switch">
                <input
                  v-model="autoTagSessions"
                  type="checkbox"
                  aria-label="Auto-tag sessions"
                  @change="saveAutoTagSessions"
                />
                <span class="switch__track" />
              </label>
              <template v-if="autoTagSessions" #below>
                <SettingsNote>Requires WebGPU (Chrome 113+). The SmolLM2-360M model is downloaded once and cached locally.</SettingsNote>
              </template>
            </SettingsRow>

            <SettingsRow
              title="Continue sessions after an update"
              description="When Eggify restarts to install an update, sessions that were working pick up where they left off. After any other restart you're asked first."
            >
              <label class="switch">
                <input
                  v-model="resumeAfterUpdate"
                  type="checkbox"
                  aria-label="Continue sessions after an update"
                  @change="saveResumeAfterUpdate"
                />
                <span class="switch__track" />
              </label>
            </SettingsRow>

            <SettingsRow
              title="Continue sessions after a usage limit"
              description="When your Claude usage limit is reached, sessions that were working (or whose subagents were) wait and continue by themselves once the limit resets."
            >
              <label class="switch">
                <input
                  v-model="resumeAfterUsageLimit"
                  type="checkbox"
                  aria-label="Continue sessions after a usage limit"
                  @change="saveResumeAfterUsageLimit"
                />
                <span class="switch__track" />
              </label>
            </SettingsRow>

            <SettingsRow
              title="Agent questions"
              description="When an agent asks you a question and you don't answer, Eggify can pick the option the agent recommends. Tool approvals and loop proposals always wait for you."
              stacked
            >
              <SettingsChoiceGroup
                v-model="questionMode"
                name="question-policy"
                label="Agent questions"
                :options="questionModeOptions"
                @change="saveQuestionPolicyChoice"
              />
              <template #below>
                <SettingsChoiceGroup
                  v-if="questionMode === 'auto_after'"
                  v-model="questionWait"
                  name="question-wait"
                  label="Auto-pick after"
                  :options="questionWaitOptions"
                  @change="saveQuestionPolicyChoice"
                />
                <SettingsNote v-if="questionPolicyError" variant="error">{{ questionPolicyError }}</SettingsNote>
                <SettingsNote v-else>Claude sessions only. A session can override this in its sidebar; only a signed-in user can change it.</SettingsNote>
              </template>
            </SettingsRow>
          </SettingsSection>

          <!-- Agent CLIs: installed / logged in -->
          <SettingsSection id="agent-logins" icon="bolt" title="Agents &amp; logins" subtitle="Claude Code and OpenAI Codex on this computer: installed, logged in, ready to run">
            <div class="agent-logins">
              <OnboardingAgentRuntimeList />
            </div>
          </SettingsSection>

          <!-- Sandbox -->
          <SettingsSection id="sandbox" icon="key" title="Sandbox" subtitle="Keep Eggify's secrets out of agent reach and limit network access">
            <SettingsRow
              title="Sandbox new sessions"
              description="On unless you turn it off. Autopilot sessions (permission prompts off) run inside it: Eggify's secrets stay unreadable, file changes stay inside the project and what the session was given access to, and Claude commands only reach the domains below. Turning it off makes new Claude sessions start unsandboxed unless they choose otherwise. Codex command networking is off unless explicitly authorized when creating a session."
            >
              <label class="switch">
                <input
                  v-model="sandboxDefault"
                  type="checkbox"
                  aria-label="Sandbox new sessions"
                  :disabled="!sandboxLoaded || sandboxSaving"
                  @change="saveSandboxDefault"
                />
                <span class="switch__track" />
              </label>
            </SettingsRow>

            <SettingsRow
              title="Allowed domains (Claude only)"
              description="Hosts Claude sandboxed commands may reach, one per line (*.example.com for subdomains). This list does not restrict Codex. To authorize Codex networking, sign in as an admin, create a new Codex session, enable Filesystem sandbox and Allow unrestricted network for this session. Existing sessions retain their permissions across restarts."
              stacked
            >
              <textarea
                v-model="sandboxDomainsText"
                class="input input--mono sandbox-domains"
                rows="8"
                spellcheck="false"
                aria-label="Allowed domains"
                :disabled="!sandboxLoaded || sandboxSaving"
              />
              <template #below>
                <div class="sandbox-actions">
                  <button class="btn btn--primary" type="button" :disabled="!sandboxLoaded || sandboxSaving" @click="saveSandboxDomains">
                    {{ sandboxSaving ? 'Saving…' : 'Save allowlist' }}
                  </button>
                  <button class="btn btn--secondary" type="button" :disabled="!sandboxLoaded || sandboxSaving" @click="resetSandboxDomains">
                    Reset to defaults
                  </button>
                </div>
                <SettingsNote v-if="sandboxInvalid.length" variant="error">Not a host name: {{ sandboxInvalid.join(', ') }}</SettingsNote>
                <SettingsNote v-else-if="sandboxError" variant="error">{{ sandboxError }}</SettingsNote>
                <SettingsNote v-else-if="sandboxSavedAt" variant="success">Saved. Applies to sessions started from now on.</SettingsNote>
                <SettingsNote v-else>Only a signed-in user can change these settings — an agent holding the API key cannot.</SettingsNote>
              </template>
            </SettingsRow>
          </SettingsSection>

          <!-- Voice input -->
          <SettingsSection id="voice" icon="mic" title="Voice input" subtitle="Speech recognition runs in your browser">
            <SettingsRow
              title="Transcription engine"
              description="Which speech recognition engine to use for voice input."
              stacked
            >
              <SettingsChoiceGroup
                v-model="sttEngine"
                name="stt-engine"
                label="Transcription engine"
                :options="sttEngineOptions"
                @change="saveSttEngine"
              />
              <SettingsNote v-if="sttEngine === 'parakeet'">
                Parakeet needs a one-time model download, cached locally: ~700MB (WASM), or ~2.5GB in Chrome/Edge with WebGPU.
              </SettingsNote>
            </SettingsRow>

            <SettingsRow
              v-if="sttEngine === 'whisper'"
              title="Whisper model size"
              description="Larger models are more accurate but slower. Downloaded once and cached in your browser."
              stacked
            >
              <SettingsChoiceGroup
                v-model="whisperModel"
                name="whisper-model"
                label="Whisper model size"
                :options="whisperModelOptions"
                @change="saveWhisperModel"
              />
              <SettingsNote>Changing models triggers a download on the next recording. The old model cache is kept.</SettingsNote>
            </SettingsRow>
          </SettingsSection>

          <!-- Avatars -->
          <SettingsSection id="avatars" icon="user" title="Avatars" subtitle="AI-generated avatars for your sessions">
            <SettingsRow
              title="Generate avatars"
              description="Create unique avatars with Hugging Face image models. Generated avatars are saved as a new theme in your library."
              stacked
            >
              <AvatarsAvatarGenerator />
            </SettingsRow>

            <SettingsRow
              title="Avatar library"
              description="View, edit and delete avatar themes and individual avatars."
            >
              <NuxtLink to="/avatar-manager" class="btn btn--secondary">
                <SettingsGlyph name="grid" />
                Manage library
              </NuxtLink>
            </SettingsRow>
          </SettingsSection>

          <!-- Git -->
          <SettingsSection id="git" icon="git" title="Git" subtitle="Repository search and cloning">
            <SettingsRow
              title="GitHub organization"
              description="Default organization for the Organization search tab. Leave empty to disable."
              label-for="github-organization"
            >
              <input
                id="github-organization"
                v-model="githubOrganization"
                type="text"
                class="input input--inline"
                placeholder="e.g. golang, facebook"
                spellcheck="false"
                @change="saveGithubOrganization"
              />
            </SettingsRow>

            <SettingsRow
              title="Default clone path"
              description="Directory for cloning repositories. Use ~ for your home directory."
              label-for="default-clone-path"
            >
              <input
                id="default-clone-path"
                v-model="defaultClonePath"
                type="text"
                class="input input--inline input--mono"
                placeholder="~/.claude/projects"
                spellcheck="false"
                @change="saveDefaultClonePath"
              />
            </SettingsRow>
          </SettingsSection>

          <!-- Remote access (ngrok) -->
          <SettingsSection id="access" icon="globe" title="Remote access" subtitle="Reach this Eggify from your phone or another computer through a secure ngrok address">
            <SettingsRemoteAccess />
          </SettingsSection>

          <!-- Performance monitor -->
          <SettingsSection id="performance" icon="gauge" title="Performance" subtitle="How Eggify and this page behave over time, with a warning when something keeps growing">
            <SettingsPerformanceMonitor />
          </SettingsSection>

          <!-- What's new -->
          <SettingsSection id="whats-new" icon="bolt" title="What's new" subtitle="Release notes for the version you run and the ones before it">
            <button type="button" class="btn btn--secondary" data-testid="open-changelog" @click="openChangelog">Open the changelog</button>
          </SettingsSection>
        </div>
      </div>
    </div>

    <!-- Save confirmation -->
    <div class="save-toast" :class="{ 'save-toast--visible': showSaveStatus }" role="status" aria-live="polite">
      <SettingsGlyph name="check" />
      <span>Settings saved</span>
      <button type="button" class="save-toast__close" title="Close" aria-label="Close" @click="showSaveStatus = false">
        <SettingsGlyph name="x" :size="14" />
      </button>
    </div>

    <!-- MFA modals -->
    <MFASetup v-model="showMFASetup" :can-close="true" @setup-complete="refreshMFAStatus" />
    <MFAAuditLog v-model="showMFAAuditLog" :can-close="true" />
  </div>
</template>

<script setup lang="ts">
import { ref, computed, watch, onMounted, onUnmounted, nextTick } from 'vue'
import type { SettingsChoice } from '~/types/settings'
import { useSandboxSettings, type SandboxSettings } from '~/composables/useSandboxSettings'
import { parseSandboxDomains } from '~/utils/agents/sandbox'
import { useQuestionPolicy } from '~/composables/agents/useQuestionPolicy'
import { DEFAULT_QUESTION_POLICY, QUESTION_TIMEOUT_CHOICES, formatWait, type QuestionPolicy } from '~/utils/agents/questionPolicy'

const { fetchWithAuth } = useAuthenticatedFetch()
const { openChangelog } = useChangelog()
const { user } = useAuth()
const settingsStore = useSettings()

// ---------------------------------------------------------------------------
// Navigation (sticky section list with scroll-spy)
// ---------------------------------------------------------------------------
const navItems = [
  { id: 'account', label: 'Account & security', icon: 'lock' },
  { id: 'appearance', label: 'Appearance', icon: 'palette' },
  { id: 'shortcuts', label: 'Keyboard shortcuts', icon: 'keyboard' },
  { id: 'agents', label: 'Agents', icon: 'bot' },
  { id: 'agent-logins', label: 'Agents & logins', icon: 'bolt' },
  { id: 'sandbox', label: 'Sandbox', icon: 'key' },
  { id: 'voice', label: 'Voice input', icon: 'mic' },
  { id: 'avatars', label: 'Avatars', icon: 'user' },
  { id: 'git', label: 'Git', icon: 'git' },
  { id: 'access', label: 'Remote access', icon: 'globe' },
  { id: 'performance', label: 'Performance', icon: 'gauge' },
  { id: 'whats-new', label: "What's new", icon: 'bolt' },
]

const scrollEl = ref<HTMLElement | null>(null)
const activeSection = ref(navItems[0]!.id)
let spyFrame = 0
let suppressSpyUntil = 0

function scrollToSection(id: string, smooth = true) {
  const el = document.getElementById(id)
  if (!el) return
  activeSection.value = id
  // Keep the clicked item highlighted while the (smooth) scroll settles.
  suppressSpyUntil = Date.now() + 900
  el.scrollIntoView({ behavior: smooth ? 'smooth' : 'auto', block: 'start' })
  history.replaceState(history.state, '', `#${id}`)
}

// Scroll-spy: the active section is the last one whose top has passed the
// upper part of the viewport. At the very bottom the final sections can never
// reach the top, so the last one wins there.
function updateActiveSection() {
  spyFrame = 0
  const root = scrollEl.value
  if (!root || Date.now() < suppressSpyUntil) return
  const atBottom = root.scrollTop + root.clientHeight >= root.scrollHeight - 4
  if (atBottom) {
    activeSection.value = navItems[navItems.length - 1]!.id
    return
  }
  const threshold = root.getBoundingClientRect().top + root.clientHeight * 0.3
  let current = navItems[0]!.id
  for (const item of navItems) {
    const el = document.getElementById(item.id)
    if (el && el.getBoundingClientRect().top <= threshold) current = item.id
  }
  activeSection.value = current
}

function onScroll() {
  if (!spyFrame) spyFrame = requestAnimationFrame(updateActiveSection)
}

// ---------------------------------------------------------------------------
// Choice options
// ---------------------------------------------------------------------------
const permissionModeOptions: SettingsChoice[] = [
  { value: 'default', label: 'Ask for permissions', hint: 'Prompt before performing actions', icon: 'lock' },
  { value: 'allow-all', label: 'Full permissions', hint: 'Allow all actions without prompting', icon: 'checkCircle' },
  { value: 'read-only', label: 'Read only', hint: 'No file modifications allowed', icon: 'eye' },
  { value: 'yolo', label: 'YOLO mode', hint: 'Bypass all permission checks', icon: 'bolt' },
]

const diffLocationOptions: SettingsChoice[] = [
  { value: 'chat', label: 'In chat', hint: 'Show the full diff in the conversation', icon: 'chat' },
  { value: 'options', label: 'In options', hint: 'Show the diff in a collapsible overlay', icon: 'panel' },
]

const questionModeOptions: SettingsChoice[] = [
  { value: 'wait', label: 'Wait for me', hint: 'The agent waits until you answer', icon: 'user' },
  { value: 'auto_after', label: 'Auto-pick after a while', hint: 'Pick the recommended option if you don\'t answer', icon: 'clock' },
  { value: 'auto', label: 'Auto-pick now', hint: 'Answer with the recommended option at once', icon: 'bolt' },
]
const questionWaitOptions: SettingsChoice[] = QUESTION_TIMEOUT_CHOICES.map(s => ({ value: String(s), label: formatWait(s) }))

const sttEngineOptions: SettingsChoice[] = [
  { value: 'whisper', label: 'Whisper', hint: '~35–150MB · Record, then transcribe', icon: 'mic' },
  { value: 'parakeet', label: 'Parakeet v3', hint: '~700MB · Real-time streaming', icon: 'wave' },
]

const whisperModelOptions: SettingsChoice[] = [
  { value: 'tiny', label: 'Tiny', hint: '~35MB · Fast · Recommended', icon: 'dot1' },
  { value: 'base', label: 'Base', hint: '~75MB · Balanced', icon: 'dot2' },
  { value: 'small', label: 'Small', hint: '~150MB · Most accurate', icon: 'dot3' },
]

const projectColorStyleOptions: SettingsChoice[] = [
  { value: 'gradient', label: 'Gradient fade', hint: 'Subtle gradient fading from the edges', icon: 'gradient' },
  { value: 'solid', label: 'Solid tint', hint: 'Even, flat tint across sidebar and navbar', icon: 'solid' },
  { value: 'off', label: 'Off', hint: 'No color indicator', icon: 'off' },
]

// ---------------------------------------------------------------------------
// Settings state
// ---------------------------------------------------------------------------
const defaultPermissionMode = ref('default')
const diffDisplayLocation = ref('chat')
const sttEngine = ref('whisper')
const whisperModel = ref('tiny')
const githubOrganization = ref('')
const defaultClonePath = ref('')
const projectColorStyle = ref(localStorage.getItem('cct_project_color_style') || 'off')
const projectColorGlow = ref(localStorage.getItem('cct_project_color_glow') || 'off')
const autoTagSessions = ref(false)
const resumeAfterUpdate = ref(true)
const resumeAfterUsageLimit = ref(true)

// Save confirmation toast
const showSaveStatus = ref(false)
let saveToastTimer: ReturnType<typeof setTimeout> | undefined
function flashSaved() {
  showSaveStatus.value = true
  clearTimeout(saveToastTimer)
  saveToastTimer = setTimeout(() => { showSaveStatus.value = false }, 2000)
}

// Server-side settings: API key → description stored alongside the value.
const SETTING_DESCRIPTIONS = {
  default_permission_mode: 'Default permission mode for new sessions: "default", "allow-all", "read-only", or "yolo"',
  diff_display_location: 'Where to display file diffs: "chat" or "options"',
  stt_engine: 'Speech-to-text engine: "whisper" or "parakeet"',
  whisper_model: 'Whisper speech recognition model: "tiny", "base", or "small"',
  auto_tag_sessions: 'Automatically generate session tags using in-browser LLM (WebLLM) after 3 messages',
  resume_after_update: 'Continue the sessions that were working after Eggify restarts for an update',
  resume_after_usage_limit: 'Continue the sessions that were working once the Claude usage limit resets',
  github_organization: 'Default GitHub organization for repository search',
  default_clone_path: 'Default directory for cloning repositories',
} as const
type SettingKey = keyof typeof SETTING_DESCRIPTIONS

async function saveSetting(key: SettingKey, value: string, valueType: 'string' | 'boolean' = 'string'): Promise<boolean> {
  try {
    const response = await fetchWithAuth(`/api/settings/${key}`, {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ value, value_type: valueType, description: SETTING_DESCRIPTIONS[key] }),
    })
    if (!response.ok) {
      console.error(`Failed to save ${key} setting:`, await response.text())
      return false
    }
    flashSaved()
    return true
  } catch (error) {
    console.error(`Error saving ${key} setting:`, error)
    return false
  }
}

/** Returns the stored value ('' when unset), or null when the request failed. */
async function loadSetting(key: SettingKey): Promise<string | null> {
  const response = await fetchWithAuth(`/api/settings/${key}`, { method: 'GET' })
  if (!response.ok) return null
  const setting = await response.json()
  return setting.value ?? ''
}

async function fetchSettings() {
  const keys = Object.keys(SETTING_DESCRIPTIONS) as SettingKey[]
  const results = await Promise.allSettled(keys.map(loadSetting))
  const value = (key: SettingKey): string | null => {
    const r = results[keys.indexOf(key)]
    return r?.status === 'fulfilled' ? r.value : null
  }
  const failed = results.some(r => r.status === 'rejected')
  if (failed) console.error('Failed to fetch some settings:', results.filter(r => r.status === 'rejected'))

  diffDisplayLocation.value = value('diff_display_location') || 'chat'
  sttEngine.value = value('stt_engine') || 'whisper'
  whisperModel.value = value('whisper_model') || 'tiny'
  githubOrganization.value = value('github_organization') || ''
  defaultClonePath.value = value('default_clone_path') || ''
  autoTagSessions.value = value('auto_tag_sessions') === 'true'
  resumeAfterUpdate.value = value('resume_after_update') !== 'false'
  resumeAfterUsageLimit.value = value('resume_after_usage_limit') !== 'false'

  const permissionMode = value('default_permission_mode')
  defaultPermissionMode.value = permissionMode || 'default'
  if (permissionMode !== null) {
    settingsStore.setDefaultPermissionMode(defaultPermissionMode.value as any)
  }
}

const saveDefaultPermissionMode = async () => {
  if (await saveSetting('default_permission_mode', defaultPermissionMode.value)) {
    // Takes effect immediately in the "Create new session" dialog.
    settingsStore.setDefaultPermissionMode(defaultPermissionMode.value as any)
  }
}
const saveDiffDisplayLocation = () => saveSetting('diff_display_location', diffDisplayLocation.value)
const saveSttEngine = () => saveSetting('stt_engine', sttEngine.value)
const saveWhisperModel = () => saveSetting('whisper_model', whisperModel.value)
const saveAutoTagSessions = () => saveSetting('auto_tag_sessions', autoTagSessions.value ? 'true' : 'false', 'boolean')
const saveResumeAfterUpdate = () => saveSetting('resume_after_update', resumeAfterUpdate.value ? 'true' : 'false', 'boolean')
const saveResumeAfterUsageLimit = () => saveSetting('resume_after_usage_limit', resumeAfterUsageLimit.value ? 'true' : 'false', 'boolean')
const saveGithubOrganization = () => saveSetting('github_organization', githubOrganization.value)
const saveDefaultClonePath = () => saveSetting('default_clone_path', defaultClonePath.value)

// Project color lives in localStorage; other components listen for the event.
function notifyProjectColorChange() {
  window.dispatchEvent(new CustomEvent('cct-project-color-settings-changed'))
  flashSaved()
}
const saveProjectColorStyle = () => {
  localStorage.setItem('cct_project_color_style', projectColorStyle.value)
  notifyProjectColorChange()
}
const setProjectColorGlow = (on: boolean) => {
  projectColorGlow.value = on ? 'on' : 'off'
  localStorage.setItem('cct_project_color_glow', projectColorGlow.value)
  notifyProjectColorChange()
}

// ---------------------------------------------------------------------------
// Password
// ---------------------------------------------------------------------------
const showPasswordForm = ref(false)
const currentPassword = ref('')
const newPassword = ref('')
const confirmPassword = ref('')
const changePasswordLoading = ref(false)
const passwordError = ref('')
const passwordSuccess = ref('')

const canChangePassword = computed(() =>
  !!currentPassword.value &&
  !!newPassword.value &&
  !!confirmPassword.value &&
  newPassword.value === confirmPassword.value &&
  newPassword.value.length >= 8 &&
  !changePasswordLoading.value,
)

function resetPasswordForm() {
  currentPassword.value = ''
  newPassword.value = ''
  confirmPassword.value = ''
}

async function togglePasswordForm() {
  showPasswordForm.value = !showPasswordForm.value
  passwordError.value = ''
  if (!showPasswordForm.value) {
    resetPasswordForm()
    return
  }
  passwordSuccess.value = ''
  await nextTick()
  document.getElementById('current-password')?.focus()
}

const handleChangePassword = async () => {
  passwordError.value = ''
  passwordSuccess.value = ''

  if (!currentPassword.value) { passwordError.value = 'Please enter your current password'; return }
  if (!newPassword.value) { passwordError.value = 'Please enter a new password'; return }
  if (newPassword.value.length < 8) { passwordError.value = 'New password must be at least 8 characters long'; return }
  if (newPassword.value !== confirmPassword.value) { passwordError.value = 'Passwords do not match'; return }

  changePasswordLoading.value = true
  try {
    const response = await fetchWithAuth('/api/auth/change-password', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ old_password: currentPassword.value, new_password: newPassword.value }),
    })
    if (response.ok) {
      passwordSuccess.value = 'Password updated successfully!'
      resetPasswordForm()
      showPasswordForm.value = false
      setTimeout(() => { passwordSuccess.value = '' }, 3000)
    } else {
      const error = await response.json().catch(() => ({}))
      passwordError.value = error.error || 'Failed to update password'
    }
  } catch (error: any) {
    console.error('Error changing password:', error)
    passwordError.value = error.message || 'An error occurred while changing your password'
  } finally {
    changePasswordLoading.value = false
  }
}

// ---------------------------------------------------------------------------
// MFA
// ---------------------------------------------------------------------------
const showMFASetup = ref(false)
const showMFAAuditLog = ref(false)
const mfaStatusKey = ref(0) // bump to re-render MFAStatus
const refreshMFAStatus = () => { mfaStatusKey.value++ }

// ---------------------------------------------------------------------------
// Lifecycle
// ---------------------------------------------------------------------------
// ---------------------------------------------------------------------------
// Sandbox (global default + egress allowlist; saving needs a signed-in user)
// ---------------------------------------------------------------------------
const sandbox = useSandboxSettings()
const sandboxDefault = ref(false)
const sandboxDomainsText = ref('')
const sandboxLoaded = ref(false)
const sandboxInvalid = ref<string[]>([])
const sandboxSavedAt = ref<number | null>(null)
const sandboxSaving = computed(() => sandbox.saving.value)
const sandboxError = computed(() => sandbox.error.value)

const applySandboxSettings = (s: SandboxSettings | null) => {
  if (!s) return
  sandboxDefault.value = s.default_enabled
  sandboxDomainsText.value = (s.allowed_domains || []).join('\n')
  sandboxLoaded.value = true
}

const loadSandboxSettings = async () => applySandboxSettings(await sandbox.load())

const saveSandboxDefault = async () => {
  sandboxSavedAt.value = null
  if (await sandbox.save({ default_enabled: sandboxDefault.value })) {
    sandboxSavedAt.value = Date.now()
  } else {
    applySandboxSettings(sandbox.settings.value) // revert the switch
    await loadSandboxSettings()
  }
}

const saveSandboxDomains = async () => {
  sandboxSavedAt.value = null
  const { domains, invalid } = parseSandboxDomains(sandboxDomainsText.value)
  sandboxInvalid.value = invalid
  if (invalid.length) return
  if (await sandbox.save({ allowed_domains: domains })) {
    applySandboxSettings(sandbox.settings.value)
    sandboxSavedAt.value = Date.now()
  }
}

const resetSandboxDomains = () => {
  sandboxInvalid.value = []
  sandboxDomainsText.value = (sandbox.settings.value?.default_allowed_domains || []).join('\n')
}

// ---------------------------------------------------------------------------
// Agent questions (GET/PUT /api/agent/question-policy; saving needs a user)
// ---------------------------------------------------------------------------
const questionPolicy = useQuestionPolicy()
const questionMode = ref<string>(DEFAULT_QUESTION_POLICY.mode)
const questionWait = ref<string>(String(DEFAULT_QUESTION_POLICY.timeout_seconds))
const questionPolicyError = ref<string | null>(null)

function applyQuestionPolicy(p: QuestionPolicy | null) {
  const policy = p || DEFAULT_QUESTION_POLICY
  questionMode.value = policy.mode
  if (policy.mode === 'auto_after' && policy.timeout_seconds) questionWait.value = String(policy.timeout_seconds)
}
// Changed elsewhere (another tab, the desktop app): follow it.
watch(questionPolicy.globalPolicy, applyQuestionPolicy)

async function saveQuestionPolicyChoice() {
  questionPolicyError.value = null
  const policy: QuestionPolicy = questionMode.value === 'auto_after'
    ? { mode: 'auto_after', timeout_seconds: Number(questionWait.value) || DEFAULT_QUESTION_POLICY.timeout_seconds }
    : { mode: questionMode.value as QuestionPolicy['mode'] }
  const result = await questionPolicy.saveGlobal(policy)
  if (result.ok) {
    flashSaved()
  } else {
    questionPolicyError.value = result.userRequired ? 'Only a signed-in user can change this.' : (result.error || 'Failed to save')
    applyQuestionPolicy(questionPolicy.globalPolicy.value)
  }
}

onMounted(async () => {
  fetchSettings()
  loadSandboxSettings()
  questionPolicy.loadGlobal(true).then(applyQuestionPolicy)
  await nextTick()
  scrollEl.value?.addEventListener('scroll', onScroll, { passive: true })
  const hash = window.location.hash.slice(1)
  if (hash && navItems.some(i => i.id === hash)) scrollToSection(hash, false)
})

// A link to a section while the page is open (the shortcuts sheet's "Change keys")
const route = useRoute()
watch(() => route.hash, (h) => {
  const id = h.slice(1)
  if (id && navItems.some(i => i.id === id)) scrollToSection(id)
})

onUnmounted(() => {
  scrollEl.value?.removeEventListener('scroll', onScroll)
  cancelAnimationFrame(spyFrame)
  clearTimeout(saveToastTimer)
})
</script>

<style scoped>
/* ===== Layout ===== */
.settings-page {
  height: 100%;
  width: 100%;
  overflow-y: auto;
  overflow-x: hidden;
  background: var(--bg-primary);
}

.settings-shell {
  max-width: 1120px;
  margin: 0 auto;
  padding: 40px 40px 96px;
}

.settings-header {
  margin-bottom: 28px;
}

.settings-header h1 {
  margin: 0 0 4px;
  font-size: 1.75rem;
  font-weight: 700;
  letter-spacing: -0.02em;
  color: var(--text-primary);
}

.settings-header p {
  margin: 0;
  font-size: 0.9rem;
  color: var(--text-secondary);
}

.settings-layout {
  display: grid;
  grid-template-columns: 200px minmax(0, 1fr);
  gap: 32px;
  align-items: start;
}

.settings-content {
  display: flex;
  flex-direction: column;
  gap: 20px;
  min-width: 0;
}

/* ===== Section navigation ===== */
.settings-nav {
  position: sticky;
  top: 24px;
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.settings-nav__item {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 8px 12px;
  border-radius: 8px;
  font-size: 0.85rem;
  font-weight: 500;
  color: var(--text-secondary);
  text-decoration: none;
  white-space: nowrap;
  transition: background 0.15s, color 0.15s;
}

.settings-nav__item:hover {
  color: var(--text-primary);
  background: var(--bg-secondary);
}

.settings-nav__item--active {
  color: var(--accent-purple);
  background: rgba(var(--accent-purple-rgb, 138, 108, 255), 0.1);
}

.settings-nav__item:focus-visible {
  outline: 2px solid var(--accent-purple);
  outline-offset: 1px;
}

/* ===== Buttons ===== */
.btn {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  padding: 8px 14px;
  border-radius: 8px;
  font: inherit;
  font-size: 0.82rem;
  font-weight: 500;
  white-space: nowrap;
  text-decoration: none;
  cursor: pointer;
  transition: background 0.15s, border-color 0.15s, color 0.15s, opacity 0.15s;
}

.btn--primary {
  color: #fff;
  background: var(--accent-purple);
  border: 1px solid var(--accent-purple);
}

.btn--primary:hover:not(:disabled) {
  filter: brightness(1.08);
}

.btn--primary:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

.btn--secondary {
  color: var(--text-primary);
  background: var(--bg-secondary);
  border: 1px solid var(--border-color);
}

.btn--secondary:hover {
  color: var(--accent-purple);
  border-color: var(--accent-purple);
}

.btn :deep(svg) {
  flex-shrink: 0;
  opacity: 0.8;
}

/* ===== Inputs ===== */
.input {
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

.input:focus {
  outline: none;
  border-color: var(--accent-purple);
  box-shadow: 0 0 0 3px rgba(var(--accent-purple-rgb, 138, 108, 255), 0.15);
}

.input:disabled {
  opacity: 0.6;
  cursor: not-allowed;
}

.input::placeholder {
  color: var(--text-muted, var(--text-secondary));
}

.input--inline {
  width: 280px;
  max-width: 100%;
}

.input--mono {
  font-family: var(--font-mono, ui-monospace, monospace);
  font-size: 0.8rem;
}

/* ===== Agents & logins ===== */
.agent-logins { padding: 18px 0; }

/* ===== Sandbox ===== */
.sandbox-domains {
  width: 100%;
  min-height: 9rem;
  resize: vertical;
  line-height: 1.5;
}

.sandbox-actions {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  margin-bottom: 8px;
}

/* ===== Password form ===== */
.password-form {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 12px;
  padding: 16px;
  border: 1px solid var(--border-color);
  border-radius: 10px;
  background: var(--bg-secondary);
}

.field {
  display: flex;
  flex-direction: column;
  gap: 6px;
  min-width: 0;
}

.field--wide {
  grid-column: 1 / -1;
}

.field__label {
  font-size: 0.78rem;
  font-weight: 500;
  color: var(--text-primary);
}

.password-form__actions {
  grid-column: 1 / -1;
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 12px;
  padding-top: 4px;
}

.hint {
  font-size: 0.75rem;
  color: var(--text-secondary);
}

/* ===== Switch ===== */
.switch {
  position: relative;
  display: inline-block;
  width: 40px;
  height: 22px;
  flex-shrink: 0;
  cursor: pointer;
}

.switch input {
  position: absolute;
  opacity: 0;
  width: 100%;
  height: 100%;
  margin: 0;
  cursor: pointer;
}

.switch__track {
  position: absolute;
  inset: 0;
  border-radius: 11px;
  background: var(--border-color, #444);
  transition: background 0.2s;
  pointer-events: none;
}

.switch__track::after {
  content: '';
  position: absolute;
  top: 3px;
  left: 3px;
  width: 16px;
  height: 16px;
  border-radius: 50%;
  background: #fff;
  box-shadow: 0 1px 2px rgba(0, 0, 0, 0.2);
  transition: transform 0.2s;
}

.switch input:checked + .switch__track {
  background: var(--accent-purple);
}

.switch input:checked + .switch__track::after {
  transform: translateX(18px);
}

.switch input:focus-visible + .switch__track {
  outline: 2px solid var(--accent-purple);
  outline-offset: 2px;
}

/* ===== Save toast ===== */
.save-toast {
  position: fixed;
  right: 24px;
  bottom: 24px;
  z-index: 100;
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 9px 10px 9px 14px;
  border-radius: 8px;
  font-size: 0.84rem;
  font-weight: 500;
  color: #fff;
  background: var(--accent-green, #10b981);
  box-shadow: 0 6px 20px rgba(0, 0, 0, 0.18);
  opacity: 0;
  transform: translateY(12px);
  pointer-events: none;
  transition: opacity 0.2s, transform 0.2s;
}

.save-toast--visible {
  opacity: 1;
  transform: translateY(0);
  pointer-events: auto;
}

.save-toast__close {
  display: inline-flex;
  padding: 3px;
  border: none;
  border-radius: 4px;
  color: #fff;
  background: transparent;
  opacity: 0.75;
  cursor: pointer;
}

.save-toast__close:hover {
  opacity: 1;
  background: rgba(255, 255, 255, 0.15);
}

/* ===== Responsive ===== */
@media (max-width: 960px) {
  .settings-shell {
    padding: 24px 20px 88px;
  }

  .settings-layout {
    grid-template-columns: minmax(0, 1fr);
    gap: 16px;
  }

  .settings-nav {
    top: 0;
    z-index: 5;
    flex-direction: row;
    gap: 6px;
    margin: 0 -20px;
    padding: 8px 20px;
    overflow-x: auto;
    scrollbar-width: none;
    background: var(--bg-primary);
    border-bottom: 1px solid var(--border-color);
  }

  .settings-nav::-webkit-scrollbar {
    display: none;
  }

  .settings-nav__item {
    padding: 6px 12px;
    border: 1px solid var(--border-color);
    border-radius: 999px;
  }

  .settings-content :deep(.settings-section) {
    scroll-margin-top: 64px;
  }
}

@media (max-width: 640px) {
  .settings-header h1 {
    font-size: 1.5rem;
  }

  .password-form {
    grid-template-columns: minmax(0, 1fr);
  }

  .input--inline {
    width: 100%;
  }
}
</style>
