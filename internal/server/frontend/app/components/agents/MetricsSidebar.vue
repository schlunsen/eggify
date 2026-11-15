<template>
  <aside v-if="show" v-show="!collapsed" class="metrics-sidebar">
    <!-- Sidebar Content -->
    <div class="sidebar-content">
      <!-- Compact Header: Avatar + Identity -->
      <div v-if="session" class="compact-header" :style="{ '--avatar-color': avatar.color }">
        <div class="header-top-row">
          <!-- Small Avatar -->
          <div class="avatar-wrapper-sm" :class="avatarEnterAnimation" :key="session.id">
            <svg class="avatar-ring" viewBox="0 0 82 82" fill="none" xmlns="http://www.w3.org/2000/svg">
              <circle cx="41" cy="41" r="39" :stroke="avatar.color" stroke-width="2.5" />
            </svg>
            <div class="avatar-container-sm" :class="{ 'processing-glow': session.status === 'processing' }">
              <SnowEffect
                :is-processing="session.status === 'processing'"
                :size="72"
                :particle-count="15"
                :speed="0.6"
              />
              <img :src="avatar.avatar" :alt="avatar.name" class="session-avatar" />
            </div>
          </div>

          <!-- Identity Column -->
          <div class="identity-col">
            <!-- Name row -->
            <div
              v-if="!isEditingName"
              class="avatar-name clickable"
              @click="startEditName"
              title="Click to change avatar"
            >{{ avatar.name }}</div>
            <input
              v-else
              ref="nameInputEl"
              v-model="nameSearchQuery"
              class="avatar-name-input"
              placeholder="Search avatars..."
              @keydown.escape.stop="cancelEditName"
              @blur="handleNameBlur"
              @input="onNameSearch"
            />

            <!-- Status + Context % inline -->
            <div class="identity-meta">
              <span
                class="status-pill"
                :class="[session.status, { connected, subagents: idleWithSubagents }]"
                :title="idleWithSubagents ? `The main agent is idle; ${subagentsWorkingLabel(subagentsWorking)}` : undefined"
              >
                <span class="status-dot-sm"></span>
                {{ session.status }}
              </span>
              <button
                v-if="idleWithSubagents"
                type="button"
                class="subagents-working-pill"
                title="Open the subagents view"
                @click="$emit('open-subagents-view')"
              >{{ subagentsWorkingLabel(subagentsWorking) }}</button>

            <!-- Tag, inline with the status -->
            <span class="sidebar-tag-row">
              <span
                v-if="sessionTag && !isEditingTag"
                class="sidebar-tag"
                @click="startEditTag"
                title="Click to edit tag"
              >{{ sessionTag }}</span>
              <button
                v-if="!sessionTag && !isEditingTag"
                class="sidebar-add-tag-btn"
                @click="startEditTag"
                title="Add a tag to this session"
              >+ Tag</button>
              <input
                v-if="isEditingTag"
                ref="tagInputEl"
                v-model="tagInputValue"
                class="sidebar-tag-input"
                placeholder="e.g. refactor, bug fix..."
                maxlength="30"
                @keydown.enter.stop="saveTag"
                @keydown.escape.stop="cancelEditTag"
                @blur="saveTag"
              />
              <button
                v-if="sessionTag && !isEditingTag"
                class="sidebar-remove-tag-btn"
                @click="removeTag"
                title="Remove tag"
              >&times;</button>
            </span>
            </div>

            <!-- Plan usage (Claude, or Codex for a Codex session), on its own line so it can wrap -->
            <div
              v-if="usage.text"
              class="usage-limit-pill"
              :class="usage.level"
              :title="usage.title"
              data-testid="usage-limit-pill"
            >{{ usage.text }}</div>
          </div>
        </div>

        <!-- Avatar search results dropdown -->
        <div v-if="isEditingName && avatarSearchResults.length > 0" class="avatar-search-dropdown" ref="dropdownEl">
          <button
            v-for="result in avatarSearchResults"
            :key="result.id"
            class="avatar-search-item"
            @mousedown.prevent="selectAvatar(result)"
          >
            <img :src="getAvatarImageUrl(result)" :alt="result.name" class="avatar-search-img" />
            <span class="avatar-search-name">{{ result.name }}</span>
          </button>
        </div>
      </div>

      <!-- Stat tiles: duration, messages, context (opens the breakdown) -->
      <div v-if="session" class="stat-row">
        <div class="stat-tile">
          <span class="stat-value"><LiveText :text="durationLabel" /></span>
          <span class="stat-label">duration</span>
        </div>
        <div class="stat-tile">
          <span class="stat-value">{{ messageCount }}</span>
          <span class="stat-label">messages</span>
        </div>
        <button
          type="button"
          class="stat-tile stat-tile-button"
          :class="contextLevel"
          :aria-expanded="showContextDetail"
          aria-controls="sidebar-context-detail"
          :title="showContextDetail ? 'Hide context usage' : 'Show context usage'"
          @click="toggleContextDetail"
        >
          <svg class="context-ring" viewBox="0 0 36 36" aria-hidden="true">
            <circle class="ring-track" cx="18" cy="18" r="14" />
            <circle class="ring-fill" cx="18" cy="18" r="14" :stroke-dasharray="`${contextRingLength} ${RING_CIRCUMFERENCE}`" transform="rotate(-90 18 18)" />
          </svg>
          <span class="stat-text">
            <span class="stat-value">{{ contextPercentLabel }}</span>
            <span class="stat-label">context</span>
          </span>
        </button>
      </div>
      <div v-if="session && showContextDetail" id="sidebar-context-detail" class="context-detail">
        <ContextUsageBar
          :usage="contextUsage"
          :message-count="messageCount ?? session.message_count"
          :loading="contextLoading"
          @refresh="$emit('refresh-context')"
        />
      </div>

      <!-- Model and n0 app -->
      <div v-if="session" class="settings-box">
        <button
          type="button"
          class="model-row"
          :disabled="!canSwitchModel"
          :title="switchDisabledReason || 'Change provider / model for this session'"
          @click="openModelSwitcher"
        >
          <Icon name="mdi:creation-outline" size="18" class="row-icon" />
          <span class="model-copy">
            <span class="model-name">{{ currentModel || '—' }}</span>
            <span class="model-meta">{{ modelMeta }}</span>
          </span>
          <span class="model-action">{{ modelActionLabel }}</span>
        </button>
        <div v-if="showN0Switcher" class="n0-session-row">
          <span class="n0-label">n0 app</span>
          <CustomDropdown
            class="n0-dropdown"
            :model-value="n0SessionValue"
            :options="n0SessionOptions"
            :disabled="n0Saving"
            placeholder="Select n0 app"
            @update:model-value="onN0SessionChange"
          />
          <p v-if="n0SaveError" class="model-switcher-error">{{ n0SaveError }}</p>
        </div>
        <div v-if="showQuestionPolicy" class="n0-session-row question-policy-row" data-testid="question-policy-row">
          <span class="n0-label" title="What happens when the agent asks you a question and you don't answer. Tool approvals always wait.">Questions</span>
          <CustomDropdown
            class="n0-dropdown"
            :model-value="questionPolicyValue"
            :options="questionPolicyOptions"
            :disabled="questionPolicySaving"
            @update:model-value="onQuestionPolicyChange"
          />
          <p v-if="questionPolicyError" class="model-switcher-error" role="alert">{{ questionPolicyError }}</p>
        </div>
        <div class="n0-session-row skills-row" data-testid="skills-row">
          <span class="n0-label" title="Approved skills this session can use, and adding another project's skill to it">Skills</span>
          <button type="button" class="skills-open" @click="emit('open-skills')">{{ skillsLabel }}</button>
        </div>
      </div>

      <!-- Mid-session provider / model switcher -->
      <Teleport to="body">
      <dialog ref="modelDialog" class="model-switcher model-dialog" aria-labelledby="model-dialog-title" @keydown.stop @cancel.prevent="closeModelSwitcher" @close="handleModelDialogClosed">
        <div class="model-dialog-heading">
          <h2 id="model-dialog-title">Switch model</h2>
          <button type="button" aria-label="Close model switcher" :disabled="switching" @click="closeModelSwitcher">✕</button>
        </div>
        <input
          ref="modelSearchInput"
          v-model="modelSearch"
          class="model-search"
          type="search"
          role="combobox"
          aria-label="Search models across providers"
          aria-autocomplete="list"
          aria-expanded="true"
          aria-controls="model-search-results"
          :aria-activedescendant="!providersLoading && filteredModels[highlightedModel] ? `model-result-${highlightedModel}` : undefined"
          :disabled="switching"
          placeholder="Search models or providers…"
          autocomplete="off"
          @keydown.down.prevent="moveModelHighlight(1)"
          @keydown.up.prevent="moveModelHighlight(-1)"
          @keydown.enter.prevent="selectHighlightedModel"
        />
        <div v-if="providersLoading" class="model-switcher-hint" role="status">Loading models…</div>
        <div id="model-search-results" class="model-results" role="listbox" aria-label="Models" :aria-busy="providersLoading">
          <template v-if="!providersLoading">
            <button
              v-for="(result, index) in filteredModels"
              :id="`model-result-${index}`"
              :key="`${result.provider.id}/${result.model}`"
              type="button"
              role="option"
              tabindex="-1"
              class="model-result"
              :class="{ highlighted: index === highlightedModel }"
              :aria-selected="index === highlightedModel"
              :disabled="switching || !canSwitchModel"
              @mousemove="hoverModelResult($event, index)"
              @click="selectModelResult(result)"
            >
              <span class="model-result-copy"><strong>{{ result.model }}</strong><span>{{ result.provider.name }}</span></span>
              <span v-if="result.provider.id === currentProviderId && result.model === currentModel" class="model-current">Current</span>
              <span v-else aria-hidden="true">↵</span>
            </button>
          </template>
        </div>
        <p v-if="!providersLoading && !filteredModels.length" class="model-switcher-hint" role="status">No models found. Try another model or provider name.</p>
        <p v-if="switchError" class="model-switcher-error" role="alert">{{ switchError }}</p>
        <p v-if="switching" class="model-switcher-hint" role="status">Switching to {{ switchModel }}…</p>
        <p v-else-if="switchDisabledReason" class="model-switcher-hint" role="status">{{ switchDisabledReason }}</p>
          <p class="model-switcher-hint">
            Applies to your next reply. Earlier messages keep their original model labels.
            When changing providers, recent conversation history is carried over; older details may be omitted.
          </p>
          <div class="model-search-footer"><span>↑ ↓ Navigate</span><span>↵ Switch</span><span>Esc Close</span></div>
      </dialog>
      </Teleport>

      <!-- Project Area Section -->
      <div v-if="session?.project_area" class="area-section">
        <span class="area-label">PROJECT AREA</span>
        <div class="area-badge" :style="{ backgroundColor: session.project_area.color }">
          <span class="area-icon">{{ session.project_area.icon }}</span>
          <span class="area-name">{{ session.project_area.name }}</span>
        </div>
        <div class="area-path">{{ session.project_area.relative_path }}</div>
      </div>

      <SessionMetrics
        :session="session"
        :message-count="messageCount"
        :tool-executions="toolExecutions"
        :permission-stats="permissionStats"
        :project-permissions="projectPermissions"
        @refresh-permissions="$emit('refresh-permissions')"
      >
        <template #subagents>
          <SubagentTrackerSection
            :session-id="session.id"
            @view-agent="(agentId: string) => $emit('view-agent', agentId)"
            @open-view="$emit('open-subagents-view')"
          />
        </template>
        <template v-if="session?.project_id" #footer>
          <NuxtLink :to="`/projects/${session.project_id}/memories`" class="memory-palace-link">
            <Icon name="mdi:brain" size="18" class="row-icon" />
            <span class="memory-palace-text">Memory Palace</span>
            <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" class="memory-palace-arrow" aria-hidden="true">
              <polyline points="9 18 15 12 9 6"></polyline>
            </svg>
          </NuxtLink>
        </template>
      </SessionMetrics>
    </div>
  </aside>
</template>

<script setup lang="ts">
import { ref, computed, watch, nextTick, onUnmounted, inject, defineAsyncComponent } from 'vue'
import { readSessionTags, sessionTagsVersion, splitTags, writeSessionTagList } from '~/utils/sessionTags'
import { useBackgroundAgents } from '~/composables/agents/useBackgroundAgents'
import { subagentsWorkingCount, subagentsWorkingLabel } from '~/utils/agents/subagentsWorking'
import CustomDropdown from '~/components/ui/CustomDropdown.vue'
import { useQuestionPolicy } from '~/composables/agents/useQuestionPolicy'
import { useSessionStore } from '~/stores/session/sessionStore'
import { parseQuestionPolicy, policyFromKey, policyKey, questionPolicyApplies, questionPolicyChoices } from '~/utils/agents/questionPolicy'
import { getEventBus } from '~/stores/events/eventBus'
import SnowEffect from '~/components/SnowEffect.vue'
import SubagentTrackerSection from '~/components/agents/SubagentTrackerSection.vue'
import { fetchAvatarById, fetchAvatarThemes, fetchThemeAvatars, getAvatarImageUrl, type Avatar, type AvatarTheme } from '~/composables/useAvatarThemes'
import { useCharacterAvatar } from '~/composables/useCharacterAvatar'
import { useN0Connections, n0ConnectionLabel } from '~/composables/useN0Connections'
import { useN0Bindings } from '~/composables/useN0Bindings'

import LiveText from '~/components/ui/LiveText.vue'
import ContextUsageBar from '~/components/agents/ContextUsageBar.vue'
import { useCodexUsage, useUsageLimit } from '~/composables/agents/useUsageLimit'
import { codexTightestWindow, codexUsageSummary, isCodexRuntime, usageLevel, usageLimitSummary } from '~/utils/agents/limitWait'
import { useLoopClock } from '~/utils/agents/scheduledLoop'
import { useUIStore } from '~/stores/ui/uiStore'
import { useSessionSkills } from '~/composables/agents/useSessionSkills'
import { activeSkillCount } from '~/utils/agents/sessionSkills'

// SessionMetrics pulls in vuedraggable/sortablejs; keep it off the page chunk.
const SessionMetrics = defineAsyncComponent(() => import('~/components/SessionMetrics.vue'))

interface Props {
  show: boolean
  /** Hidden with ⌥⌘B; stays mounted so the model switcher etc. keep working. */
  collapsed?: boolean
  session: any
  projectPermissions?: any
  messageCount: number
  toolExecutions: any
  permissionStats: any
  contextUsage?: {
    total_tokens?: number
    context_window?: number
    percentage?: number
    model?: string
    categories?: Array<{
      name: string
      tokens: number
      percentage: number
    }>
  }
  contextLoading?: boolean
  connected: boolean
}

const props = defineProps<Props>()

// Plan usage of the runtime this session bills to: Codex's for a Codex
// session, else Claude's. Shown whenever known; it turns orange from 75 %.
const sessionIsCodex = computed(() => isCodexRuntime(
  props.session?.options?.provider || props.session?.provider,
  props.session?.options?.model || props.session?.model_name))
const usageInfo = useUsageLimit(() => sessionIsCodex.value)
const codexUsage = useCodexUsage()
const usageNow = useLoopClock()
const usage = computed(() => {
  if (sessionIsCodex.value) {
    const info = codexUsage.value
    const w = codexTightestWindow(info)
    return {
      text: codexUsageSummary(info, usageNow.value),
      level: usageLevel(w?.used_percent, !!info?.limit_reached),
      title: `Your Codex usage${info?.plan_type ? ` (${info.plan_type} plan)` : ''}, as reported by codex`,
    }
  }
  const info = usageInfo.value
  return {
    text: usageLimitSummary(info, usageNow.value),
    level: usageLevel(typeof info?.utilization === 'number' ? info.utilization * 100 : (info?.status === 'allowed_warning' ? 75 : null), info?.status === 'rejected'),
    title: 'Your Claude usage, as last reported by the Claude CLI',
  }
})

// The main agent's turn has ended but subagents it launched are still working.
const { getAgentsForSession, hasFullList } = useBackgroundAgents()
const subagentsWorking = computed(() =>
  props.session ? subagentsWorkingCount(getAgentsForSession(props.session.id), hasFullList(props.session.id), props.session.subagents_running) : 0)
const idleWithSubagents = computed(() => props.session?.status === 'idle' && subagentsWorking.value > 0)

const emit = defineEmits<{
  (e: 'refresh-context'): void
  (e: 'avatar-changed', sessionId: string, avatarId: number): void
  (e: 'view-agent', agentId: string): void
  (e: 'open-subagents-view'): void
  (e: 'model-switch-completed', sessionId: string): void
  (e: 'open-skills'): void
}>()

// Skills row: how many approved skills the session gets; opens the Skills panel.
const sessionSkills = useSessionSkills()
watch(() => props.session?.id, (id) => { if (id) void sessionSkills.load(id) }, { immediate: true })
const skillsLabel = computed(() => {
  const state = sessionSkills.stateFor(props.session?.id)
  if (!state?.data) return state?.error ? 'Show' : '…'
  return `${activeSkillCount(state.data.skills)} active`
})

// ---------------------------------------------------------------------------
// Mid-session provider / model switching
// Sends `change_session_model` over the shared agent WebSocket (same channel
// the YOLO toggle uses). The backend answers with `session_model_changed`
// (handled in pages/agents/index.vue, which adds a chat notice and re-emits
// it on the event bus) and pushes the refreshed session via `session_updated`.
// ---------------------------------------------------------------------------
const agentWs = inject<any>('agentWs', null)
const eventBus = getEventBus()

interface SwitchProvider {
  id: string
  name: string
  icon?: string
  models?: string[]
  default_model?: string
  base_url?: string
}

const showModelSwitcher = ref(false)
const modelDialog = ref<HTMLDialogElement | null>(null)
const modelSearchInput = ref<HTMLInputElement | null>(null)
const modelSearch = ref('')
const highlightedModel = ref(0)
let focusSessionAfterClose: string | null = null
function handleModelDialogClosed() {
  showModelSwitcher.value = false
  const sessionId = focusSessionAfterClose
  focusSessionAfterClose = null
  if (sessionId && sessionId === props.session?.id) emit('model-switch-completed', sessionId)
}
watch(showModelSwitcher, async (visible) => {
  await nextTick()
  if (visible && showModelSwitcher.value) {
    modelDialog.value?.showModal()
    modelSearchInput.value?.focus()
  }
  else modelDialog.value?.close()
})
function closeModelSwitcher() {
  if (switching.value) return
  showModelSwitcher.value = false
}
function openModelSwitcher() {
  if (showModelSwitcher.value || !canSwitchModel.value) return
  toggleModelSwitcher()
}
const providersLoading = ref(false)
const switchProviders = ref<SwitchProvider[]>([])
const switchProvider = ref('')
const switchModel = ref('')
const switching = ref(false)
const switchError = ref<string | null>(null)
let switchTimeout: ReturnType<typeof setTimeout> | null = null

const currentProviderId = computed<string>(() => props.session?.options?.provider || props.session?.provider || '')
const currentModel = computed<string>(() => props.session?.options?.model || props.session?.model_name || '')

const switchDisabledReason = computed(() => {
  if (!props.session) return 'No active session'
  if (!props.connected) return 'Not connected to the server'
  if (props.session.status === 'processing') return 'Wait for the current turn to finish (or interrupt it) before switching'
  if (props.session.status === 'ended') return 'Session has ended'
  return ''
})
const canSwitchModel = computed(() => !switchDisabledReason.value)

// The model row shows why it can't be clicked right now in a word; the full
// reason is in its tooltip.
const modelActionLabel = computed(() => {
  if (canSwitchModel.value) return 'Switch'
  if (!props.connected) return 'Offline'
  if (props.session?.status === 'processing') return 'Busy'
  return ''
})
const formatTokenCount = (tokens: number) =>
  tokens >= 1_000_000 ? `${+(tokens / 1_000_000).toFixed(1)}M` : `${Math.round(tokens / 1000)}K`
const modelMeta = computed(() => {
  const provider = getProviderDisplay(currentProviderId.value)
  const contextWindow = props.contextUsage?.context_window
  return contextWindow ? `${provider} · ${formatTokenCount(contextWindow)} context` : provider
})

// Context tile: a ring with the share of the context window in use. The
// breakdown (ContextUsageBar) opens below the tiles; open/closed is remembered.
const uiStore = useUIStore()
const showContextDetail = computed(() => uiStore.expandedSections.contextDetail === true)
const toggleContextDetail = () => uiStore.setSectionExpanded('contextDetail', !showContextDetail.value)
const RING_CIRCUMFERENCE = 2 * Math.PI * 14
const contextPercent = computed<number | null>(() => {
  const usage = props.contextUsage
  if (!usage) return null
  if (typeof usage.percentage === 'number') return usage.percentage
  if (usage.total_tokens != null && usage.context_window) return (usage.total_tokens / usage.context_window) * 100
  return null
})
const contextPercentLabel = computed(() => contextPercent.value == null ? '—' : `${Math.round(contextPercent.value)}%`)
const contextRingLength = computed(() => Math.min(Math.max(contextPercent.value ?? 0, 0), 100) / 100 * RING_CIRCUMFERENCE)
const contextLevel = computed(() => {
  const pct = contextPercent.value ?? 0
  return pct >= 85 ? 'context-high' : pct >= 60 ? 'context-mid' : ''
})

interface ModelResult {
  provider: SwitchProvider
  model: string
}
const filteredModels = computed<ModelResult[]>(() => {
  const terms = modelSearch.value.toLowerCase().trim().split(/\s+/).filter(Boolean)
  return switchProviders.value.flatMap(provider =>
    [...new Set(provider.models || [])]
      .filter(model => terms.every(term => `${provider.name} ${provider.id} ${model}`.toLowerCase().includes(term)))
      .map(model => ({ provider, model }))
  )
})
watch(filteredModels, () => { highlightedModel.value = 0 })
async function moveModelHighlight(direction: number) {
  if (providersLoading.value || !filteredModels.value.length) return
  highlightedModel.value = (highlightedModel.value + direction + filteredModels.value.length) % filteredModels.value.length
  await nextTick()
  modelDialog.value?.querySelector(`#model-result-${highlightedModel.value}`)?.scrollIntoView({ block: 'nearest' })
}
// WebKit sends mousemove with no movement when the list scrolls or the dialog resizes under a resting pointer.
function hoverModelResult(event: MouseEvent, index: number) {
  if (!event.movementX && !event.movementY) return
  highlightedModel.value = index
}
function selectModelResult(result: ModelResult) {
  if (providersLoading.value || switching.value || !canSwitchModel.value) return
  switchProvider.value = result.provider.id
  switchModel.value = result.model
  applyModelSwitch()
}
function selectHighlightedModel(event: KeyboardEvent) {
  if (event.isComposing || event.repeat) return
  const result = filteredModels.value[highlightedModel.value]
  if (result) selectModelResult(result)
}
const canApplySwitch = computed(() =>
  !!switchProvider.value &&
  !!switchModel.value &&
  !switching.value &&
  canSwitchModel.value &&
  !(switchProvider.value === currentProviderId.value && switchModel.value === currentModel.value)
)

async function loadSwitchProviders() {
  providersLoading.value = true
  try {
    const response = await fetch('/api/providers')
    if (!response.ok) throw new Error(`HTTP ${response.status}`)
    const data = await response.json()
    switchProviders.value = data.providers || []
  } catch (err) {
    console.error('Failed to load providers:', err)
    switchError.value = 'Failed to load providers'
  } finally {
    providersLoading.value = false
  }
}

function resetSwitchState() {
  switching.value = false
  if (switchTimeout) {
    clearTimeout(switchTimeout)
    switchTimeout = null
  }
}

function toggleModelSwitcher() {
  if (showModelSwitcher.value) {
    showModelSwitcher.value = false
    resetSwitchState()
    return
  }
  if (!canSwitchModel.value) return
  switchError.value = null
  modelSearch.value = ''
  highlightedModel.value = 0
  switchProvider.value = currentProviderId.value
  switchModel.value = currentModel.value
  showModelSwitcher.value = true
  loadSwitchProviders()
}

function applyModelSwitch() {
  if (!props.session || !canApplySwitch.value) return
  if (!agentWs) {
    switchError.value = 'WebSocket not available'
    return
  }
  const p = switchProviders.value.find(x => x.id === switchProvider.value)
  const payload: Record<string, unknown> = {
    type: 'change_session_model',
    session_id: props.session.id,
    provider: switchProvider.value,
    model: switchModel.value
  }
  if (p?.base_url) payload.base_url = p.base_url

  switchError.value = null
  switching.value = true
  if (!agentWs.send(payload)) {
    switching.value = false
    switchError.value = 'Failed to send request - not connected'
    return
  }
  switchTimeout = setTimeout(() => {
    if (switching.value) {
      switching.value = false
      switchError.value = 'No response from the server - check the chat for details'
    }
  }, 15000)
}

const offModelChanged = eventBus.on('session:model-changed', (data: any) => {
  if (data?.sessionId !== props.session?.id) return
  if (showModelSwitcher.value && switching.value) focusSessionAfterClose = data.sessionId
  resetSwitchState()
  switchError.value = null
  showModelSwitcher.value = false
})

defineExpose({ openModelSwitcher, switchDisabledReason })

const offAgentError = eventBus.on('agent:error', (data: any) => {
  if (!switching.value) return
  if (data?.sessionId && data.sessionId !== props.session?.id) return
  resetSwitchState()
  switchError.value = data?.message || 'Failed to switch model'
})

// Switching sessions: drop any in-progress switcher state
watch(() => props.session?.id, () => {
  focusSessionAfterClose = null
  showModelSwitcher.value = false
  switchError.value = null
  resetSwitchState()
})

onUnmounted(() => {
  offModelChanged()
  offAgentError()
  resetSwitchState()
})

// ---------------------------------------------------------------------------
// n0 connection for a live session (PUT /api/connectors/n0/sessions/:id)
// ---------------------------------------------------------------------------
const {
  connections: n0Connections,
  loaded: n0Loaded,
  fetchConnections: fetchN0Connections,
  setSessionConnection: setN0SessionConnection,
} = useN0Connections()
const { getBinding: getN0Binding, refresh: refreshN0Bindings } = useN0Bindings()
const n0Saving = ref(false)
const n0SaveError = ref<string | null>(null)

const n0Binding = computed(() => (props.session?.id ? getN0Binding(props.session.id) : null))
const sessionHasN0 = computed(() => {
  const connectors = props.session?.options?.connectors
  return (Array.isArray(connectors) && connectors.includes('n0')) || !!n0Binding.value
})
const showN0Switcher = computed(() =>
  sessionHasN0.value && n0Connections.value.some(c => c.status !== 'revoked')
)
const n0SessionValue = computed<string | number>(() => n0Binding.value?.connection_id ?? '')
const n0SessionOptions = computed(() => [
  ...n0Connections.value
    .filter(c => c.status !== 'revoked')
    .map(c => ({
      value: c.id,
      label: `${n0ConnectionLabel(c)}${c.status === 'needs_reauth' ? ' (needs reauth)' : ''}`,
      description: c.workspace_name || undefined,
    })),
  { value: 'off', label: 'Off — disable n0 for this session' },
])

watch(() => [props.session?.id, sessionHasN0.value] as const, ([id, hasN0]) => {
  n0SaveError.value = null
  if (id && hasN0 && !n0Loaded.value) void fetchN0Connections()
}, { immediate: true })

async function onN0SessionChange(value: string | number) {
  if (!props.session?.id || value === n0SessionValue.value) return
  n0Saving.value = true
  n0SaveError.value = null
  try {
    const result = await setN0SessionConnection(props.session.id, value === 'off' ? null : Number(value))
    if (!result.ok) n0SaveError.value = result.error || 'Failed to update n0 connection'
    await refreshN0Bindings()
  } finally {
    n0Saving.value = false
  }
}

// ---------------------------------------------------------------------------
// Agent questions: this session's policy (PUT /api/agent/sessions/:id/question-policy)
// ---------------------------------------------------------------------------
const sessionStore = useSessionStore()
const { globalPolicy: questionGlobalPolicy, loadGlobal: loadQuestionGlobal, saveSession: saveQuestionPolicy } = useQuestionPolicy()
const questionPolicySaving = ref(false)
const questionPolicyError = ref<string | null>(null)
/** The choice shown while saving (and after, until the session object catches up). */
const questionPolicyPending = ref<{ sessionId: string; key: string } | null>(null)

// Only Claude CLI sessions ask questions (Codex and the OpenAI-compatible loop don't).
const showQuestionPolicy = computed(() =>
  !!props.session && props.session.status !== 'ended' && questionPolicyApplies(currentProviderId.value, currentModel.value))
const sessionQuestionPolicyKey = computed(() => policyKey(parseQuestionPolicy(props.session?.question_policy)))
const questionPolicyValue = computed(() => {
  const pending = questionPolicyPending.value
  return pending && pending.sessionId === props.session?.id ? pending.key : sessionQuestionPolicyKey.value
})
const questionPolicyOptions = computed(() => {
  // A session without an override follows the global policy, which its
  // effective policy shows even before the global one is loaded.
  const global = questionGlobalPolicy.value ||
    (!props.session?.question_policy ? parseQuestionPolicy(props.session?.question_policy_effective) : null)
  return questionPolicyChoices(global, policyFromKey(questionPolicyValue.value))
    .map(({ value, label }) => ({ value, label }))
})

watch(() => [props.session?.id, showQuestionPolicy.value] as const, ([id, show]) => {
  questionPolicyError.value = null
  if (questionPolicyPending.value && questionPolicyPending.value.sessionId !== id) questionPolicyPending.value = null
  if (id && show) void loadQuestionGlobal()
}, { immediate: true })

// The server (or another tab) changed it: show what the session says now.
watch(sessionQuestionPolicyKey, () => {
  if (!questionPolicySaving.value) questionPolicyPending.value = null
})

async function onQuestionPolicyChange(value: string | number) {
  const sessionId = props.session?.id
  if (!sessionId || String(value) === questionPolicyValue.value) return
  const previous = questionPolicyPending.value
  const policy = policyFromKey(value)
  questionPolicyPending.value = { sessionId, key: String(value) }
  questionPolicySaving.value = true
  questionPolicyError.value = null
  try {
    const result = await saveQuestionPolicy(sessionId, policy)
    if (result.ok) {
      sessionStore.setSessionQuestionPolicy(
        sessionId,
        parseQuestionPolicy(result.data?.policy) ?? policy,
        parseQuestionPolicy(result.data?.effective) ?? undefined,
      )
    } else {
      questionPolicyPending.value = previous
      questionPolicyError.value = result.userRequired
        ? 'Only a signed-in user can change this.'
        : (result.error || 'Failed to update the question policy')
    }
  } finally {
    questionPolicySaving.value = false
  }
}

// Avatar name editing state
const isEditingName = ref(false)
const nameSearchQuery = ref('')
const nameInputEl = ref<HTMLInputElement | null>(null)
const dropdownEl = ref<HTMLElement | null>(null)
const avatarSearchResults = ref<Avatar[]>([])
const allAvatars = ref<Avatar[]>([])
let avatarsLoaded = false

async function loadAllAvatars() {
  if (avatarsLoaded) return
  try {
    const themes = await fetchAvatarThemes()
    const allResults: Avatar[] = []
    for (const theme of themes) {
      const avatars = await fetchThemeAvatars(theme.id)
      allResults.push(...avatars)
    }
    allAvatars.value = allResults
    avatarsLoaded = true
  } catch (err) {
    console.error('Error loading avatars:', err)
  }
}

function startEditName() {
  nameSearchQuery.value = ''
  isEditingName.value = true
  loadAllAvatars().then(() => {
    // Show all avatars initially
    avatarSearchResults.value = allAvatars.value.slice(0, 20)
  })
  nextTick(() => nameInputEl.value?.focus())
}

function onNameSearch() {
  const query = nameSearchQuery.value.toLowerCase().trim()
  if (!query) {
    avatarSearchResults.value = allAvatars.value.slice(0, 20)
    return
  }
  avatarSearchResults.value = allAvatars.value
    .filter(a => a.name.toLowerCase().includes(query))
    .slice(0, 20)
}

async function selectAvatar(selectedAvatar: Avatar) {
  if (!props.session?.id) return

  // Update in database via API
  try {
    const response = await fetch(`/api/agent/sessions/${props.session.id}/avatar`, {
      method: 'PATCH',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ avatar_id: selectedAvatar.id }),
    })
    if (response.ok) {
      persistentAvatar.value = selectedAvatar
      emit('avatar-changed', props.session.id, selectedAvatar.id)
    }
  } catch (err) {
    console.error('Error updating avatar:', err)
  }

  isEditingName.value = false
  nameSearchQuery.value = ''
  avatarSearchResults.value = []
}

function cancelEditName() {
  isEditingName.value = false
  nameSearchQuery.value = ''
  avatarSearchResults.value = []
}

function handleNameBlur() {
  // Small delay to allow click on dropdown items
  setTimeout(() => {
    if (isEditingName.value) {
      cancelEditName()
    }
  }, 200)
}

// Session tag (utils/sessionTags.ts, shared with SessionItem and the server)
const isEditingTag = ref(false)
const tagInputValue = ref('')
const tagInputEl = ref<HTMLInputElement | null>(null)

const sessionTag = computed(() => {
  sessionTagsVersion.value // bumped by every write
  return props.session?.id ? (readSessionTags()[props.session.id] || '') : ''
})

function startEditTag() {
  tagInputValue.value = sessionTag.value
  isEditingTag.value = true
  nextTick(() => tagInputEl.value?.focus())
}

function saveTag() {
  if (!props.session?.id) return
  writeSessionTagList(props.session.id, splitTags(tagInputValue.value))
  isEditingTag.value = false
}

function cancelEditTag() {
  isEditingTag.value = false
}

function removeTag() {
  if (!props.session?.id) return
  writeSessionTagList(props.session.id, [])
}


// Persistent avatar state
const persistentAvatar = ref<Avatar | null>(null)

watch(
  () => props.session?.id,
  () => {
    // Reset persistent avatar when session changes
    persistentAvatar.value = null
  }
)

// Load persistent avatar if available
watch(
  () => props.session?.selected_avatar_id,
  async (avatarId) => {
    if (avatarId) {
      try {
        const avatar = await fetchAvatarById(avatarId)
        persistentAvatar.value = avatar
      } catch (err) {
        console.error('Error loading persistent avatar:', err)
        persistentAvatar.value = null
      }
    } else {
      persistentAvatar.value = null
    }
  },
  { immediate: true }
)

// Get effective avatar (persistent or hash-based cat fallback)
const avatar = computed(() => {
  if (persistentAvatar.value) {
    return {
      name: persistentAvatar.value.name,
      avatar: getAvatarImageUrl(persistentAvatar.value),
      color: persistentAvatar.value.color || '#95A5A6',
    }
  }

  // Fall back to hash-based cat avatar
  const character = useCharacterAvatar(props.session?.id)
  return {
    name: character.name,
    avatar: character.avatar,
    color: character.color,
  }
})

// Pick a deterministic enter animation based on session ID
// Each variant animates the ring and avatar image as separate sequenced elements
const avatarAnimations = [
  'avatar-enter-stroke-fade',     // ring draws on, then image fades in
  'avatar-enter-stroke-scale',    // ring draws on, then image scales up
  'avatar-enter-ring-spin',       // ring rotates in, image fades up
  'avatar-enter-ring-expand',     // ring expands from center, image reveals
  'avatar-enter-clockwise-reveal',// ring sweeps clockwise, image slides in
  'avatar-enter-ring-pulse',      // ring pulses in with glow, image fades in
  'avatar-enter-ring-bounce',     // ring bounces in overshooting, image scales up
  'avatar-enter-ring-flicker',    // ring flickers on like neon, image fades up
]
const avatarEnterAnimation = computed(() => {
  const id = props.session?.id || ''
  let hash = 0
  for (let i = 0; i < id.length; i++) {
    hash = ((hash << 5) - hash + id.charCodeAt(i)) | 0
  }
  return avatarAnimations[Math.abs(hash) % avatarAnimations.length]
})

// Provider display function
const getProviderDisplay = (provider?: string): string => {
  const actualProvider = provider || 'anthropic'

  const providerMap: Record<string, string> = {
    'anthropic': 'Anthropic',
    'claude': 'Claude',
    'codex': 'OpenAI Codex',
    'glm': 'GLM',
    'deepseek': 'DeepSeek',
    'openai': 'OpenAI',
    'google': 'Google',
    'azure': 'Azure',
    'cohere': 'Cohere',
    'custom': 'Custom'
  }

  return providerMap[actualProvider.toLowerCase()] || actualProvider
}

// Session duration tracking
const sessionStartTime = ref<Date | null>(null)
// Last computed duration; shown as-is once the session has ended.
const sessionDuration = ref('')

const formatDuration = (startTime: Date): string => {
  const now = new Date()
  const diff = now.getTime() - startTime.getTime()
  const seconds = Math.floor(diff / 1000)
  const minutes = Math.floor(seconds / 60)
  const hours = Math.floor(minutes / 60)

  if (hours > 0) {
    return `${hours}h ${minutes % 60}m`
  } else if (minutes > 0) {
    return `${minutes}m ${seconds % 60}s`
  } else {
    return `${seconds}s`
  }
}

// Watch for session changes
watch(
  () => props.session?.created_at,
  (newVal) => {
    if (newVal) {
      sessionStartTime.value = new Date(newVal)
    }
  },
  { immediate: true }
)

// The running duration ticks inside <LiveText> (only while the sidebar is
// shown, paused in hidden tabs), so the clock doesn't re-render the sidebar.
const durationLabel = () => {
  if (sessionStartTime.value && props.session?.status !== 'ended') {
    return formatDuration(sessionStartTime.value)
  }
  return sessionDuration.value || '0s'
}

// Freeze the duration when the session ends
watch(() => props.session?.status, (status, previous) => {
  if (status === 'ended' && previous && previous !== 'ended' && sessionStartTime.value) {
    sessionDuration.value = formatDuration(sessionStartTime.value)
  }
})

// Initial duration calculation
watch(sessionStartTime, (newVal) => {
  if (newVal && props.session?.status !== 'ended') {
    sessionDuration.value = formatDuration(newVal)
  }
})


</script>

<style scoped>
.metrics-sidebar {
  position: relative;
  width: 320px;
  background: var(--card-bg);
  border: 1px solid var(--border-color);
  border-radius: 12px;
  overflow: hidden;
  min-height: 0;
  flex-shrink: 0;
  transition: all 0.3s cubic-bezier(0.4, 0, 0.2, 1);
  box-shadow: 0 2px 8px var(--shadow-color);
}

.metrics-sidebar:hover {
  box-shadow: 0 4px 16px rgba(var(--accent-purple-rgb, 139, 92, 246), 0.1);
}

/* Compact Header */
.compact-header {
  display: flex;
  flex-direction: column;
  padding: 12px;
  border-bottom: 1px solid var(--border-color);
  background: linear-gradient(180deg, var(--bg-secondary) 0%, var(--card-bg) 100%);
  position: relative;
}

.header-top-row {
  display: flex;
  align-items: flex-start;
  gap: 12px;
}

.identity-col {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 4px;
  padding-top: 4px;
}

.identity-meta {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 6px 8px;
}

.status-pill {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  padding: 2px 8px;
  border-radius: 10px;
  font-size: 0.7rem;
  font-weight: 600;
  text-transform: capitalize;
  background: color-mix(in srgb, var(--text-muted) 15%, transparent);
  color: var(--text-muted);
  border: 1px solid color-mix(in srgb, var(--text-muted) 25%, transparent);
}

.status-pill.idle {
  background: color-mix(in srgb, var(--status-success) 12%, transparent);
  color: var(--status-success);
  border-color: color-mix(in srgb, var(--status-success) 25%, transparent);
}

.status-pill.idle.subagents {
  background: color-mix(in srgb, var(--status-processing, #c2185b) 10%, transparent);
  color: var(--status-processing, #c2185b);
  border-color: color-mix(in srgb, var(--status-processing, #c2185b) 25%, transparent);
}

.subagents-working-pill {
  display: inline-flex;
  align-items: center;
  padding: 2px 8px;
  border-radius: 10px;
  font-size: 0.7rem;
  font-weight: 600;
  cursor: pointer;
  color: var(--status-processing, #c2185b);
  background: transparent;
  border: 1px dashed color-mix(in srgb, var(--status-processing, #c2185b) 45%, transparent);
}

.subagents-working-pill:hover {
  background: color-mix(in srgb, var(--status-processing, #c2185b) 10%, transparent);
}

.usage-limit-pill {
  width: fit-content;
  max-width: 100%;
  margin-top: 6px;
  font-size: 0.68rem;
  font-weight: 600;
  line-height: 1.35;
  padding: 2px 8px;
  border-radius: 10px;
  color: var(--text-secondary);
  background: color-mix(in srgb, var(--text-secondary) 10%, transparent);
  /* Wraps inside the sidebar instead of running past its edge. */
  white-space: normal;
  overflow-wrap: anywhere;
}
.usage-limit-pill.warn {
  color: var(--accent-orange, #c2410c);
  background: color-mix(in srgb, var(--accent-orange, #c2410c) 12%, transparent);
}
.usage-limit-pill.limited {
  color: var(--status-error, #dc2626);
  background: color-mix(in srgb, var(--status-error, #dc2626) 12%, transparent);
}

.status-pill.processing {
  background: color-mix(in srgb, var(--accent-cyan) 12%, transparent);
  color: var(--accent-cyan);
  border-color: color-mix(in srgb, var(--accent-cyan) 25%, transparent);
  animation: pulse 2s infinite;
}

.status-pill.error {
  background: color-mix(in srgb, var(--status-error) 12%, transparent);
  color: var(--status-error);
  border-color: color-mix(in srgb, var(--status-error) 25%, transparent);
}

.status-dot-sm {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: currentColor;
}

.context-pct {
  font-size: 0.75rem;
  font-weight: 700;
  font-family: 'Monaco', 'Menlo', monospace;
  color: var(--text-secondary);
  opacity: 0.6;
}

.context-pct.has-data {
  color: var(--accent-purple);
  opacity: 1;
  text-shadow: 0 1px 3px rgba(var(--accent-purple-rgb, 139, 92, 246), 0.25);
}

/* Compact Info Grid */
.info-grid {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 1px;
  background: var(--border-color);
  border-bottom: 1px solid var(--border-color);
}

.info-cell {
  display: flex;
  flex-direction: column;
  gap: 2px;
  padding: 8px 10px;
  background: var(--card-bg);
}

.info-label {
  font-size: 0.6rem;
  color: var(--text-secondary);
  text-transform: uppercase;
  font-weight: 600;
  letter-spacing: 0.5px;
  opacity: 0.7;
}

.info-value {
  font-size: 0.8rem;
  font-weight: 600;
  color: var(--text-primary);
}

.info-value.mono {
  font-family: 'Monaco', 'Menlo', monospace;
  color: var(--accent-purple);
}

.info-value.accent {
  color: var(--accent-purple);
}

/* n0 session connection */
.n0-session-row {
  display: flex;
  flex-direction: column;
  gap: 6px;
  padding: 10px;
  border-bottom: 1px solid var(--border-color);
}

/* Model switcher */
.info-label-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 6px;
}

.model-switch-btn {
  background: none;
  border: none;
  padding: 0;
  font-size: 0.6rem;
  font-weight: 600;
  letter-spacing: 0.3px;
  text-transform: lowercase;
  color: var(--accent-purple);
  cursor: pointer;
  opacity: 0.9;
}

.model-switch-btn:hover:not(:disabled) {
  text-decoration: underline;
  opacity: 1;
}

.model-switch-btn:disabled {
  color: var(--text-muted);
  cursor: not-allowed;
  opacity: 0.6;
}

.model-switcher {
  display: flex;
  flex-direction: column;
  gap: 6px;
  padding: 10px;
  background: var(--card-bg);
  border-bottom: 1px solid var(--border-color);
}

.model-dialog {
  width: min(640px, calc(100vw - 32px));
  max-height: calc(100dvh - 48px);
  box-sizing: border-box;
  margin: min(16vh, 120px) auto auto;
  padding: 20px;
  overflow-y: auto;
  background: var(--bg-primary);
  color: var(--text-primary);
  border: 1px solid var(--border-color);
  border-radius: 16px;
  box-shadow: 0 24px 80px rgb(0 0 0 / 35%);
  font-family: 'Inter', -apple-system, BlinkMacSystemFont, 'Segoe UI', sans-serif;
}
.model-dialog::backdrop { background: rgb(0 0 0 / 55%); }
.model-dialog:not([open]) { display: none; }
.model-dialog-heading { display: flex; align-items: center; justify-content: space-between; gap: 16px; margin-bottom: 20px; }
.model-dialog-heading h2 { font-size: 1.125rem; margin: 0; }
.model-dialog-heading button { background: transparent; border: 0; color: var(--text-primary); padding: 8px; cursor: pointer; }
.model-search {
  box-sizing: border-box;
  width: 100%;
  padding: 16px;
  border: 1px solid var(--border-color);
  border-radius: 10px;
  background: var(--bg-secondary);
  color: var(--text-primary);
  font: inherit;
  font-size: 1.125rem;
}
.model-search:focus-visible { outline: 2px solid var(--accent-purple); outline-offset: 2px; }
.model-results { overflow-y: auto; max-height: min(360px, 40dvh); margin-top: 8px; }
.model-result {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  width: 100%;
  padding: 12px;
  border: 0;
  border-radius: 8px;
  background: transparent;
  color: var(--text-primary);
  text-align: left;
  font: inherit;
  cursor: pointer;
}
.model-result.highlighted { background: color-mix(in srgb, var(--accent-purple) 16%, var(--bg-primary)); }
.model-result:disabled { opacity: 0.55; cursor: not-allowed; }
.model-result-copy { display: flex; flex-direction: column; gap: 4px; min-width: 0; overflow-wrap: anywhere; }
.model-result-copy span, .model-current { font-size: 0.75rem; color: var(--text-secondary); }
.model-current { flex-shrink: 0; }
.model-search-footer { display: flex; flex-wrap: wrap; gap: 16px; border-top: 1px solid var(--border-color); padding-top: 12px; color: var(--text-secondary); font-size: 0.75rem; }

.model-switcher-label {
  font-size: 0.6rem;
  color: var(--text-secondary);
  text-transform: uppercase;
  font-weight: 600;
  letter-spacing: 0.5px;
  opacity: 0.7;
}

.model-switcher-hint {
  margin: 2px 0 0;
  font-size: 0.68rem;
  line-height: 1.35;
  color: var(--text-secondary);
}

.model-switcher-error {
  margin: 0;
  font-size: 0.68rem;
  line-height: 1.35;
  color: var(--status-error);
}

.model-switcher-actions {
  display: flex;
  justify-content: flex-end;
  margin-top: 4px;
}

.model-switcher-apply {
  padding: 5px 12px;
  border-radius: 6px;
  border: 1px solid var(--accent-purple);
  background: var(--accent-purple);
  color: #fff;
  font-size: 0.72rem;
  font-weight: 600;
  cursor: pointer;
}

.model-switcher-apply:hover:not(:disabled) {
  filter: brightness(1.1);
}

.model-switcher-apply:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}


/* Small Avatar wrapper */
.avatar-wrapper-sm {
  position: relative;
  width: 82px;
  height: 82px;
  flex-shrink: 0;
  display: flex;
  align-items: center;
  justify-content: center;
}

.avatar-wrapper-sm:hover .avatar-container-sm {
  transform: scale(1.05);
  box-shadow: 0 4px 14px color-mix(in srgb, var(--avatar-color) 40%, transparent);
}

/* SVG ring — sits on top of the container border area */
.avatar-ring {
  position: absolute;
  inset: 0;
  width: 82px;
  height: 82px;
  z-index: 3;
  pointer-events: none;
}

.avatar-ring circle {
  stroke-linecap: round;
}

.avatar-container-sm {
  --avatar-color: var(--accent-purple, #8b5cf6);
  width: 72px;
  height: 72px;
  border-radius: 50%;
  overflow: hidden;
  border: 2.5px solid transparent;
  box-shadow: 0 3px 10px color-mix(in srgb, var(--avatar-color) 30%, transparent);
  transition: all 0.3s ease;
  position: relative;
  display: flex;
  align-items: center;
  justify-content: center;
}

/* Processing Glow Animation - uses shared animation from main.css */
.avatar-container-sm.processing-glow {
  /* No box-shadow animation here - glow is handled by pseudo-element on parent */
}

/* Avatar Fill Overlay - Context Usage Indicator */
.avatar-fill-overlay {
  position: absolute;
  bottom: 0;
  left: 0;
  right: 0;
  width: 100%;
  background: linear-gradient(180deg, color-mix(in srgb, var(--avatar-color) 85%, transparent) 0%, color-mix(in srgb, var(--avatar-color) 65%, transparent) 100%);
  border-radius: 50%;
  transition: height 0.4s cubic-bezier(0.4, 0, 0.2, 1);
  z-index: 1;
  pointer-events: none;
  box-shadow: inset 0 2px 8px var(--shadow-color);
}

/* Estimated Fill - Gray background for all estimated usage */
.avatar-fill-estimated {
  /* Gray fill for estimated usage - sits behind actual */
  background: linear-gradient(180deg, rgba(108, 117, 125, 0.5) 0%, rgba(108, 117, 125, 0.3) 100%);
  z-index: 1;
  animation: estimateShimmer 3s ease-in-out infinite;
  opacity: 0.7;
}

/* Actual Fill - Solid, opaque, colored (sits on top of estimated) */
.avatar-fill-actual {
  /* Default: normal usage (0-60%) - green */
  background: linear-gradient(180deg, color-mix(in srgb, var(--status-success) 85%, transparent) 0%, color-mix(in srgb, var(--status-success) 65%, transparent) 100%);
  z-index: 2;
}

/* Shimmer animation for estimated fill */
@keyframes estimateShimmer {
  0%, 100% {
    opacity: 0.6;
  }
  50% {
    opacity: 0.85;
  }
}

/* Color variations for actual fill based on percentage */
.compact-header:has(.avatar-fill-actual[style*="height: 6"]) .avatar-fill-actual,
.compact-header:has(.avatar-fill-actual[style*="height: 7"]) .avatar-fill-actual,
.compact-header:has(.avatar-fill-actual[style*="height: 8"]) .avatar-fill-actual {
  background: linear-gradient(180deg, color-mix(in srgb, var(--status-warning) 85%, transparent) 0%, color-mix(in srgb, var(--status-warning) 65%, transparent) 100%);
}

/* Warning usage (80-95%) - orange/pink gradient */
.compact-header:has(.avatar-fill-actual[style*="height: 8"]) .avatar-fill-actual,
.compact-header:has(.avatar-fill-actual[style*="height: 9"]) .avatar-fill-actual {
  background: linear-gradient(180deg, color-mix(in srgb, var(--accent-orange) 85%, transparent) 0%, color-mix(in srgb, var(--accent-orange) 65%, transparent) 100%);
}

/* Critical usage (95%+) - red gradient */
.compact-header:has(.avatar-fill-actual[style*="height: 9"]) .avatar-fill-actual {
  background: linear-gradient(180deg, color-mix(in srgb, var(--status-error) 90%, transparent) 0%, color-mix(in srgb, var(--status-error) 70%, transparent) 100%);
  box-shadow: inset 0 2px 8px color-mix(in srgb, var(--status-error) 40%, transparent), 0 0 12px color-mix(in srgb, var(--status-error) 40%, transparent);
}

/* Estimated fill is always gray, no color variations */

.session-avatar {
  width: 100%;
  height: 100%;
  object-fit: cover;
  object-position: center center;
  position: relative;
  z-index: 2;
}

/* ===== Avatar enter animations =====
   Each variant sequences the ring and image as separate elements.
   Ring animates first (~1s), image follows with blur-to-sharp reveal.
   Total entrance: ~1.8-2s for a relaxed, cinematic feel.
*/

/* --- Variant 1: Stroke draw → blur fade in --- */
.avatar-enter-stroke-fade .avatar-ring circle {
  stroke-dasharray: 245;
  stroke-dashoffset: 245;
  animation: strokeDraw 1.1s cubic-bezier(0.4, 0, 0.2, 1) 0.15s forwards;
}
.avatar-enter-stroke-fade .session-avatar {
  opacity: 0;
  filter: blur(12px);
  animation: imgBlurFadeIn 0.9s cubic-bezier(0.25, 0.46, 0.45, 0.94) 0.7s forwards;
}

/* --- Variant 2: Stroke draw → blur scale pop --- */
.avatar-enter-stroke-scale .avatar-ring circle {
  stroke-dasharray: 245;
  stroke-dashoffset: 245;
  animation: strokeDraw 1s cubic-bezier(0.4, 0, 0.2, 1) 0.15s forwards;
}
.avatar-enter-stroke-scale .session-avatar {
  opacity: 0;
  filter: blur(14px);
  transform: scale(0.75);
  animation: imgBlurScaleIn 1s cubic-bezier(0.25, 0.46, 0.45, 0.94) 0.65s forwards;
}

/* --- Variant 3: Ring spin → blur fade up --- */
.avatar-enter-ring-spin .avatar-ring {
  opacity: 0;
  transform: rotate(-120deg);
  animation: ringSpinIn 1s cubic-bezier(0.25, 0.46, 0.45, 0.94) 0.15s forwards;
}
.avatar-enter-ring-spin .session-avatar {
  opacity: 0;
  filter: blur(10px);
  transform: translateY(12px);
  animation: imgBlurFadeUp 0.9s cubic-bezier(0.25, 0.46, 0.45, 0.94) 0.7s forwards;
}

/* --- Variant 4: Ring expand → blur reveal --- */
.avatar-enter-ring-expand .avatar-ring {
  transform: scale(0.2);
  opacity: 0;
  animation: ringExpand 1s cubic-bezier(0.25, 0.46, 0.45, 0.94) 0.15s forwards;
}
.avatar-enter-ring-expand .session-avatar {
  opacity: 0;
  filter: blur(16px);
  transform: scale(0.88);
  animation: imgBlurReveal 0.9s cubic-bezier(0.25, 0.46, 0.45, 0.94) 0.75s forwards;
}

/* --- Variant 5: Clockwise sweep → blur slide in --- */
.avatar-enter-clockwise-reveal .avatar-ring circle {
  stroke-dasharray: 245;
  stroke-dashoffset: 245;
  animation: strokeDraw 1.3s cubic-bezier(0.4, 0, 0.2, 1) 0.15s forwards;
}
.avatar-enter-clockwise-reveal .avatar-ring {
  transform: rotate(-90deg);
}
.avatar-enter-clockwise-reveal .session-avatar {
  opacity: 0;
  filter: blur(12px);
  transform: translateY(10px) scale(0.92);
  animation: imgBlurSlideReveal 0.9s cubic-bezier(0.25, 0.46, 0.45, 0.94) 0.85s forwards;
}

/* --- Variant 6: Ring pulse → blur fade in --- */
.avatar-enter-ring-pulse .avatar-ring {
  opacity: 0;
  transform: scale(0.85);
  filter: drop-shadow(0 0 0px var(--avatar-color, #888));
  animation: ringPulseIn 1.2s cubic-bezier(0.25, 0.46, 0.45, 0.94) 0.1s forwards;
}
.avatar-enter-ring-pulse .session-avatar {
  opacity: 0;
  filter: blur(12px);
  animation: imgBlurFadeIn 0.9s cubic-bezier(0.25, 0.46, 0.45, 0.94) 0.75s forwards;
}

/* --- Variant 7: Ring bounce → blur scale pop --- */
.avatar-enter-ring-bounce .avatar-ring {
  opacity: 0;
  transform: scale(0);
  animation: ringBounceIn 1s cubic-bezier(0.34, 1.56, 0.64, 1) 0.1s forwards;
}
.avatar-enter-ring-bounce .session-avatar {
  opacity: 0;
  filter: blur(14px);
  transform: scale(0.75);
  animation: imgBlurScaleIn 1s cubic-bezier(0.25, 0.46, 0.45, 0.94) 0.6s forwards;
}

/* --- Variant 8: Ring flicker → blur fade up --- */
.avatar-enter-ring-flicker .avatar-ring {
  opacity: 0;
  animation: ringFlickerIn 1.4s ease-out 0.1s forwards;
}
.avatar-enter-ring-flicker .session-avatar {
  opacity: 0;
  filter: blur(10px);
  transform: translateY(12px);
  animation: imgBlurFadeUp 0.9s cubic-bezier(0.25, 0.46, 0.45, 0.94) 0.8s forwards;
}

/* ===== Shared keyframes ===== */

/* Ring animations */
@keyframes strokeDraw {
  to { stroke-dashoffset: 0; }
}
@keyframes ringSpinIn {
  to { opacity: 1; transform: rotate(0deg); }
}
@keyframes ringExpand {
  to { opacity: 1; transform: scale(1); }
}
@keyframes ringPulseIn {
  0%   { opacity: 0; transform: scale(0.85); filter: drop-shadow(0 0 0px var(--avatar-color, #888)); }
  30%  { opacity: 0.7; transform: scale(1.06); filter: drop-shadow(0 0 12px var(--avatar-color, #888)); }
  55%  { opacity: 0.9; transform: scale(0.97); filter: drop-shadow(0 0 6px var(--avatar-color, #888)); }
  75%  { opacity: 1; transform: scale(1.02); filter: drop-shadow(0 0 8px var(--avatar-color, #888)); }
  100% { opacity: 1; transform: scale(1); filter: drop-shadow(0 0 0px var(--avatar-color, #888)); }
}
@keyframes ringBounceIn {
  0%   { opacity: 0; transform: scale(0); }
  50%  { opacity: 1; transform: scale(1.15); }
  70%  { transform: scale(0.92); }
  85%  { transform: scale(1.05); }
  100% { opacity: 1; transform: scale(1); }
}
@keyframes ringFlickerIn {
  0%   { opacity: 0; }
  12%  { opacity: 0.6; }
  18%  { opacity: 0.1; }
  30%  { opacity: 0.8; }
  38%  { opacity: 0.2; }
  50%  { opacity: 0.9; }
  60%  { opacity: 0.5; }
  72%  { opacity: 1; }
  80%  { opacity: 0.7; }
  100% { opacity: 1; }
}

/* Image animations — all include blur-to-sharp */
@keyframes imgBlurFadeIn {
  0%   { opacity: 0; filter: blur(12px); }
  40%  { opacity: 0.6; filter: blur(6px); }
  100% { opacity: 1; filter: blur(0px); }
}
@keyframes imgBlurScaleIn {
  0%   { opacity: 0; filter: blur(14px); transform: scale(0.75); }
  50%  { opacity: 0.7; filter: blur(5px); transform: scale(0.95); }
  100% { opacity: 1; filter: blur(0px); transform: scale(1); }
}
@keyframes imgBlurFadeUp {
  0%   { opacity: 0; filter: blur(10px); transform: translateY(12px); }
  50%  { opacity: 0.6; filter: blur(4px); transform: translateY(3px); }
  100% { opacity: 1; filter: blur(0px); transform: translateY(0); }
}
@keyframes imgBlurReveal {
  0%   { opacity: 0; filter: blur(16px); transform: scale(0.88); }
  40%  { opacity: 0.5; filter: blur(8px); transform: scale(0.96); }
  100% { opacity: 1; filter: blur(0px); transform: scale(1); }
}
@keyframes imgBlurSlideReveal {
  0%   { opacity: 0; filter: blur(12px); transform: translateY(10px) scale(0.92); }
  50%  { opacity: 0.6; filter: blur(4px); transform: translateY(2px) scale(0.98); }
  100% { opacity: 1; filter: blur(0px); transform: translateY(0) scale(1); }
}

/* Avatar Name */
.avatar-name {
  font-size: 0.82rem;
  font-weight: 600;
  color: var(--text-primary);
  text-align: left;
  letter-spacing: 0.02em;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  max-width: 100%;
}

.avatar-name.clickable {
  cursor: pointer;
  padding: 2px 8px;
  border-radius: 6px;
  transition: all 0.2s ease;
  border: 1px solid transparent;
}

.avatar-name.clickable:hover {
  background: rgba(var(--accent-purple-rgb, 139, 92, 246), 0.1);
  border-color: rgba(var(--accent-purple-rgb, 139, 92, 246), 0.3);
  color: var(--accent-purple);
}

.avatar-name-input {
  font-size: 0.82rem;
  font-weight: 600;
  color: var(--text-primary);
  text-align: left;
  padding: 3px 8px;
  border-radius: 6px;
  background: var(--bg-secondary);
  border: 1px solid rgba(var(--accent-purple-rgb, 139, 92, 246), 0.5);
  outline: none;
  width: 100%;
}

.avatar-name-input:focus {
  border-color: var(--accent-purple);
  box-shadow: 0 0 0 2px rgba(var(--accent-purple-rgb, 139, 92, 246), 0.2);
}

.avatar-search-dropdown {
  position: absolute;
  z-index: 100;
  background: var(--card-bg);
  border: 1px solid var(--border-color);
  border-radius: 8px;
  box-shadow: 0 8px 24px var(--shadow-color);
  max-height: 240px;
  overflow-y: auto;
  width: 90%;
  max-width: 280px;
  margin-top: 4px;
  left: 50%;
  transform: translateX(-50%);
  scrollbar-width: thin;
}

.avatar-search-item {
  display: flex;
  align-items: center;
  gap: 10px;
  width: 100%;
  padding: 8px 12px;
  background: none;
  border: none;
  color: var(--text-primary);
  cursor: pointer;
  font-size: 0.8rem;
  text-align: left;
  transition: background 0.15s ease;
}

.avatar-search-item:hover {
  background: rgba(var(--accent-purple-rgb, 139, 92, 246), 0.15);
}

.avatar-search-img {
  width: 32px;
  height: 32px;
  border-radius: 50%;
  object-fit: contain;
  background: var(--bg-secondary);
  flex-shrink: 0;
}

.avatar-search-name {
  font-weight: 500;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

/* Legacy styles removed — using compact layout */

@keyframes pulse-connected {
  0%, 100% {
    opacity: 1;
    box-shadow: 0 0 0 0 rgba(40, 167, 69, 0.7);
  }
  50% {
    opacity: 0.8;
    box-shadow: 0 0 0 4px rgba(40, 167, 69, 0);
  }
}

/* Session Tag in Sidebar */
.sidebar-tag-row {
  display: flex;
  align-items: center;
  gap: 0.375rem;
}

.sidebar-tag {
  display: inline-block;
  font-size: 0.75rem;
  padding: 2px 10px;
  border-radius: 12px;
  background: rgba(var(--accent-purple-rgb, 139, 92, 246), 0.15);
  color: var(--accent-purple, #8b5cf6);
  border: 1px solid rgba(var(--accent-purple-rgb, 139, 92, 246), 0.3);
  cursor: pointer;
  max-width: 180px;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  transition: all 0.2s;
}

.sidebar-tag:hover {
  background: rgba(var(--accent-purple-rgb, 139, 92, 246), 0.25);
  border-color: rgba(var(--accent-purple-rgb, 139, 92, 246), 0.5);
}

.sidebar-add-tag-btn {
  font-size: 0.7rem;
  padding: 2px 10px;
  border-radius: 12px;
  background: none;
  color: var(--text-secondary);
  border: 1px dashed var(--overlay-border-hover);
  cursor: pointer;
  opacity: 0.5;
  transition: all 0.2s;
}

.sidebar-add-tag-btn:hover {
  opacity: 1;
  border-color: rgba(var(--accent-purple-rgb, 139, 92, 246), 0.4);
  color: var(--accent-purple, #8b5cf6);
}

.sidebar-tag-input {
  font-size: 0.75rem;
  padding: 2px 8px;
  border-radius: 12px;
  background: var(--bg-secondary);
  color: var(--text-primary);
  border: 1px solid rgba(var(--accent-purple-rgb, 139, 92, 246), 0.4);
  outline: none;
  width: 140px;
  text-align: center;
}

.sidebar-tag-input:focus {
  border-color: var(--accent-purple, #8b5cf6);
}

.sidebar-remove-tag-btn {
  font-size: 0.8rem;
  background: none;
  border: none;
  color: var(--text-secondary);
  cursor: pointer;
  padding: 0 2px;
  opacity: 0.4;
  transition: opacity 0.2s;
  line-height: 1;
}

.sidebar-remove-tag-btn:hover {
  opacity: 1;
  color: var(--status-error, #dc3545);
}

/* Sidebar Content */
.sidebar-content {
  height: 100%;
  overflow-y: auto;
  overflow-x: hidden;
  scrollbar-width: thin;
  scrollbar-color: var(--accent-purple) transparent;
}

.sidebar-content::-webkit-scrollbar {
  width: 6px;
}

.sidebar-content::-webkit-scrollbar-track {
  background: transparent;
}

.sidebar-content::-webkit-scrollbar-thumb {
  background: var(--border-color);
  border-radius: 3px;
  transition: background 0.2s;
}

.sidebar-content::-webkit-scrollbar-thumb:hover {
  background: var(--accent-purple);
}

@keyframes pulse {
  0%, 100% {
    opacity: 1;
  }
  50% {
    opacity: 0.7;
  }
}

/* Memory Palace Link */
.memory-palace-section {
  padding: 6px 10px;
  border-bottom: 1px solid var(--border-color);
}

.memory-palace-link {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 8px 10px;
  border-radius: 8px;
  background: rgba(236, 72, 153, 0.08);
  border: 1px solid rgba(236, 72, 153, 0.2);
  color: var(--text-primary);
  text-decoration: none;
  transition: all 0.2s ease;
  cursor: pointer;
}

.memory-palace-link:hover {
  background: rgba(236, 72, 153, 0.15);
  border-color: rgba(236, 72, 153, 0.4);
  transform: translateY(-1px);
  box-shadow: 0 2px 8px rgba(236, 72, 153, 0.15);
}

.memory-palace-icon {
  font-size: 1rem;
  flex-shrink: 0;
}

.memory-palace-text {
  flex: 1;
  font-size: 0.8rem;
  font-weight: 600;
}

.memory-palace-arrow {
  flex-shrink: 0;
  opacity: 0.5;
  transition: all 0.2s;
}

.memory-palace-link:hover .memory-palace-arrow {
  opacity: 1;
  transform: translateX(2px);
  color: var(--accent-purple, #ec4899);
}

/* Area Section */
.area-section {
  display: flex;
  flex-direction: column;
  gap: 6px;
  padding: 8px 10px;
  border-bottom: 1px solid var(--border-color);
}

.area-label {
  font-size: 0.75rem;
  color: var(--text-secondary);
  text-transform: uppercase;
  font-weight: 600;
  letter-spacing: 0.5px;
  padding: 0 4px;
}

.area-badge {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 6px;
  padding: 6px 10px;
  border-radius: 6px;
  font-size: 0.85rem;
  font-weight: 600;
  color: white;
  box-shadow: 0 2px 6px var(--shadow-color);
  width: 100%;
}

.area-icon {
  font-size: 1.25rem;
}

.area-name {
  flex: 1;
}

.area-path {
  font-size: 0.8rem;
  color: var(--text-secondary);
  font-family: 'Monaco', 'Courier New', monospace;
  padding: 0 4px;
  word-break: break-all;
}

/* Responsive */
@media (max-width: 1200px) {
  .metrics-sidebar {
    width: 280px;
  }

  .metrics-sidebar.collapsed {
    width: 48px;
  }
}

@media (max-width: 768px) {
  .metrics-sidebar {
    display: none;
  }
}

/* Animation for smooth transitions */
@keyframes slideIn {
  from {
    opacity: 0;
    transform: translateX(20px);
  }
  to {
    opacity: 1;
    transform: translateX(0);
  }
}

.metrics-sidebar {
  animation: slideIn 0.3s ease-out;
}

/* Session summary stays compact; code-specific values retain monospace. */
.metrics-sidebar { font-family: 'Inter', -apple-system, BlinkMacSystemFont, 'Segoe UI', sans-serif; box-shadow: none; }
.metrics-sidebar:hover { box-shadow: none; }
.compact-header { background: transparent; padding: 16px; border-bottom: 0; }
.avatar-wrapper-sm, .avatar-ring { width: 48px; height: 48px; }
.avatar-container-sm { width: 42px; height: 42px; }
.avatar-name { font-size: 0.875rem; text-shadow: none; }

/* Stat tiles: duration, messages, context ring */
.sidebar-tag-row { display: inline-flex; }
.stat-row { display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); gap: 6px; padding: 0 16px 12px; }
.stat-tile { display: flex; flex-direction: column; justify-content: center; gap: 2px; min-width: 0; padding: 8px 10px; border-radius: 10px; background: var(--bg-secondary); border: 1px solid transparent; color: var(--text-primary); text-align: left; font: inherit; }
.stat-tile-button { flex-direction: row; align-items: center; justify-content: flex-start; gap: 8px; cursor: pointer; }
.stat-tile-button:hover { border-color: var(--border-color); }
.stat-tile-button[aria-expanded="true"] { border-color: var(--accent-purple); }
.stat-tile-button:focus-visible { outline: 2px solid var(--accent-purple); outline-offset: 2px; }
.stat-text { display: flex; flex-direction: column; gap: 2px; min-width: 0; }
.stat-value { font-size: 1rem; font-weight: 600; line-height: 1.2; white-space: nowrap; font-variant-numeric: tabular-nums; }
.stat-label { font-size: 0.6875rem; color: var(--text-secondary); }
.context-ring { width: 24px; height: 24px; flex-shrink: 0; }
.ring-track { fill: none; stroke: var(--border-color); stroke-width: 4; }
.ring-fill { fill: none; stroke: var(--accent-purple); stroke-width: 4; stroke-linecap: round; transition: stroke-dasharray 0.3s ease; }
.context-mid .ring-fill { stroke: var(--status-warning, #f59e0b); }
.context-high .ring-fill { stroke: var(--status-error, #ef4444); }
.context-detail { padding: 0 16px 12px; }

/* Model + n0 app in one box */
.settings-box { margin: 0 16px 12px; border: 1px solid var(--border-color); border-radius: 10px; }
/* No overflow: hidden on the box: it clipped the n0 / Questions dropdown lists.
   The rows round their own corners instead. */
.settings-box > :first-child { border-top-left-radius: 9px; border-top-right-radius: 9px; }
.settings-box > :last-child { border-bottom-left-radius: 9px; border-bottom-right-radius: 9px; }
.model-row { display: flex; align-items: center; gap: 10px; width: 100%; min-height: 52px; padding: 8px 12px; background: transparent; border: 0; color: var(--text-primary); text-align: left; font: inherit; cursor: pointer; }
.model-row:hover:not(:disabled) { background: var(--overlay-bg-hover); }
.model-row:disabled { cursor: default; }
.model-row:focus-visible { outline: 2px solid var(--accent-purple); outline-offset: -2px; }
.row-icon { flex-shrink: 0; color: var(--text-secondary); }
.model-copy { display: flex; flex-direction: column; gap: 2px; flex: 1; min-width: 0; }
.model-name { font-family: 'Monaco', 'Menlo', monospace; font-size: 0.75rem; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.model-meta { font-size: 0.6875rem; color: var(--text-secondary); }
.model-action { flex-shrink: 0; font-size: 0.75rem; font-weight: 600; color: var(--accent-purple); }
.model-row:disabled .model-action { color: var(--text-secondary); font-weight: 500; }
.n0-session-row { flex-direction: row; flex-wrap: wrap; align-items: center; gap: 4px 10px; padding: 6px 12px; border-top: 1px solid var(--border-color); border-bottom: 0; }
.n0-label { font-size: 0.75rem; color: var(--text-secondary); }
.n0-dropdown { flex: 1; min-width: 0; }
.n0-session-row .model-switcher-error { flex-basis: 100%; margin: 0; }
.skills-open { margin-left: auto; padding: 2px 0; background: none; border: 0; color: var(--accent-purple); font: inherit; font-size: 0.75rem; font-weight: 600; cursor: pointer; }
.skills-open:hover { text-decoration: underline; }
.skills-open:focus-visible { outline: 2px solid var(--accent-purple); outline-offset: 2px; }

/* Memory Palace: the last row of the section list (slotted into SessionMetrics) */
.memory-palace-link { gap: 10px; min-height: 44px; padding: 0 12px; border: 0; border-radius: 0; background: transparent; font-size: 0.8125rem; font-weight: 500; }
.memory-palace-link:hover { background: var(--overlay-bg-hover); }
.memory-palace-text { font-size: inherit; font-weight: 500; }
</style>
