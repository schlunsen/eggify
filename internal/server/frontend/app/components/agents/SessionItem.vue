<template>
  <div
    ref="sessionItemEl"
    class="session-item"
    :class="[`variant-${variant}`, {
      active: isActive,
      focused: isFocused,
      ended: session.status === 'ended',
      'needs-you': !!waitingFor,
      working: isWorking,
      'sheen-pulse': isWorking && sheenPulse,
      selectable,
      selected: selectable && selected,
    }]"
    :data-session-id="session.id"
    :style="{ '--avatar-color': effectiveAvatar?.color || 'var(--accent-purple, #8b5cf6)' }"
    :aria-selected="selectable ? selected : undefined"
    @click="onRowClick"
    @contextmenu.prevent="onContextMenu"
    @mouseenter="handleTooltipShow"
    @mouseleave="handleTooltipHide"
  >
    <!-- Select mode: the row is a checkbox (shift-click selects a range) -->
    <span
      v-if="selectable"
      class="session-select-box"
      :class="{ checked: selected }"
      role="checkbox"
      :aria-checked="selected"
      :aria-label="`Select ${effectiveAvatar?.name || 'session'}`"
      data-testid="session-select-box"
    >
      <svg v-if="selected" width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="3.5" aria-hidden="true">
        <polyline points="20 6 9 17 4 12" />
      </svg>
    </span>
    <div
      v-if="effectiveAvatar"
      class="session-avatar"
      :style="{ '--avatar-color': effectiveAvatar.color }"
      :class="{ resting: !isWorking && !waitingFor }"
    >
      <img
        v-if="!avatarBroken"
        :src="effectiveAvatar.image"
        :alt="effectiveAvatar.name"
        class="session-avatar-img"
        @error="avatarBroken = true"
      />
      <span v-else class="session-avatar-fallback">{{ effectiveAvatar.name?.charAt(0) || '?' }}</span>
      <!-- The one "working" signal on the row; the group header says the rest. -->
      <span
        v-if="isWorking"
        class="session-status-dot working"
        :class="{ subagents: idleWithSubagents || idleWithWorkers }"
        :title="idleWithSubagents ? `Idle, ${subagentsWorkingLabel(subagentsWorking)}` : idleWithWorkers ? `Idle, ${workersWorkingLabel(workersWorking)}` : 'Working'"
      ></span>
    </div>
    <div class="session-info">
      <div class="session-name-row">
        <span class="session-name">{{ effectiveAvatar?.name || `Session ${session.id.slice(0, 8)}` }}</span>
        <span v-if="variant === 'compact' && tagList.length && !isEditingTag" class="session-tags" data-testid="session-tags">
          <span
            v-for="tag in fittedTags.shown"
            :key="tag"
            class="tag-chip"
            :style="tagChipStyle(tag)"
            title="Your tag — click to edit"
            @click.stop="onTagClick"
          >{{ tag }}</span>
          <span
            v-if="fittedTags.hidden.length"
            class="tag-chip tag-more"
            :title="fittedTags.hidden.join(', ')"
            @click.stop="onTagClick"
          >+{{ fittedTags.hidden.length }}</span>
        </span>
        <span
          v-if="loopShort"
          class="session-loop"
          :title="loopTitle"
        >↻ {{ loopShort }}</span>
        <span
          v-if="limitShort"
          class="session-limit-wait"
          :title="limitTitle"
          data-testid="session-limit-wait"
        >{{ limitShort }}</span>
        <span class="session-time-slot">
          <span v-if="timeLabel" class="session-time" :title="timeTitle">{{ timeLabel }}</span>
          <span v-if="!selectable" class="session-row-actions">
            <button
              v-if="session.status !== 'ended'"
              @click.stop="$emit('end', session.id)"
              class="btn-end-session"
              title="End session"
            >
              <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                <circle cx="12" cy="12" r="10"></circle>
                <line x1="15" y1="9" x2="9" y2="15"></line>
                <line x1="9" y1="9" x2="15" y2="15"></line>
              </svg>
            </button>
            <button
              @click.stop="$emit('delete', session.id)"
              class="btn-delete-session"
              title="Delete session"
            >
              <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                <polyline points="3 6 5 6 21 6"></polyline>
                <path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6m3 0V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2"></path>
                <line x1="10" y1="11" x2="10" y2="17"></line>
                <line x1="14" y1="11" x2="14" y2="17"></line>
              </svg>
            </button>
          </span>
        </span>
      </div>

      <template v-if="variant === 'full'">
        <!-- What it waits for, or that its subagents still work -->
        <div v-if="waitingFor" class="session-waiting" data-testid="session-waiting">
          <span class="session-waiting-text" :title="waitingFor">{{ waitingFor }}</span>
          <button class="btn-review" @click.stop="$emit('select', session.id)">Review</button>
        </div>
        <div v-else-if="idleWithSubagents" class="session-subagents">Idle · {{ subagentsWorkingLabel(subagentsWorking) }}</div>
        <div v-else-if="idleWithWorkers" class="session-subagents" data-testid="session-workers-working">Idle · {{ workersWorkingLabel(workersWorking) }}</div>

        <!-- What the session is about: AI summary (falls back to the first prompt) -->
        <div
          v-if="descriptionText"
          class="session-description"
          :class="{ 'is-prompt': !digest?.summary, 'one-line': hasStatusLine }"
          data-testid="session-description"
        >{{ descriptionText }}</div>
        <div v-else-if="!hasStatusLine" class="session-description is-prompt">{{ session.id.slice(0, 8) }}</div>

        <div v-if="(tagList.length && !isEditingTag) || contextPercent > 0" class="session-meta">
          <span v-if="tagList.length && !isEditingTag" class="session-tags" data-testid="session-tags">
            <span
              v-for="tag in fittedTags.shown"
              :key="tag"
              class="tag-chip"
              :style="tagChipStyle(tag)"
              title="Your tag — click to edit"
              @click.stop="onTagClick"
            >{{ tag }}</span>
            <span
              v-if="fittedTags.hidden.length"
              class="tag-chip tag-more"
              :title="fittedTags.hidden.join(', ')"
              @click.stop="onTagClick"
            >+{{ fittedTags.hidden.length }}</span>
          </span>
          <span
            v-if="contextPercent > 0"
            class="context-meter"
            :style="{ '--progress-color': progressBarColor }"
            :title="contextTitle"
            data-testid="context-meter"
          >
            <span class="context-meter-track"><span class="context-meter-fill" :style="{ width: Math.min(100, contextPercent) + '%' }"></span></span>
            <span class="context-meter-label">{{ Math.round(contextPercent) }}%</span>
          </span>
        </div>
      </template>

      <input
        v-if="isEditingTag"
        ref="tagInputEl"
        v-model="tagInputValue"
        class="tag-input"
        placeholder="e.g. refactor, bug fix..."
        maxlength="200"
        @click.stop
        @keydown.enter.stop="saveTag"
        @keydown.escape.stop="cancelEditTag"
        @blur="saveTag"
      />
    </div>

    <!-- Session Info Tooltip -->
    <Teleport to="body">
      <div v-if="showTooltip && tooltipPosition" class="session-tooltip" data-testid="session-tooltip" :style="{ top: tooltipPosition.top + 'px', left: tooltipPosition.left + 'px' }">
        <div v-if="digest?.summary" class="tooltip-summary">{{ digest.summary }}</div>
        <div v-else-if="variant === 'compact' && initialPrompt" class="tooltip-summary is-prompt">{{ initialPrompt.length > 280 ? initialPrompt.slice(0, 279) + '…' : initialPrompt }}</div>
        <div v-if="digest?.tags?.length" class="tooltip-topics">
          <span v-for="topic in digest.tags" :key="topic" class="session-topic">{{ topic }}</span>
        </div>
        <div v-if="session.options?.model" class="tooltip-row">
          <span class="tooltip-label">Model:</span>
          <span class="tooltip-value">{{ session.options.model }}</span>
        </div>
        <div v-if="workspaceBranch" class="tooltip-row">
          <span class="tooltip-label">Branch:</span>
          <span class="tooltip-value">{{ workspaceBranch }}<template v-if="workspacePath"> · worktree {{ workspacePath.split('/').filter(Boolean).pop() }}</template></span>
        </div>
        <div class="tooltip-row">
          <span class="tooltip-label">Status:</span>
          <span class="tooltip-value">{{ session.status }} · {{ session.id.slice(0, 8) }}</span>
        </div>
        <div v-if="session.cost_usd && session.cost_usd > 0" class="tooltip-row">
          <span class="tooltip-label">Cost:</span>
          <span class="tooltip-value">${{ session.cost_usd.toFixed(4) }}</span>
        </div>
        <div class="tooltip-row">
          <span class="tooltip-label">Context:</span>
          <span class="tooltip-value">{{ Math.round(contextPercent) }}%</span>
        </div>
        <div v-if="contextUsage?.total_tokens" class="tooltip-row">
          <span class="tooltip-label">Tokens:</span>
          <span class="tooltip-value">{{ formatTokens(contextUsage.total_tokens) }} / {{ formatTokens(contextUsage.context_window) }}</span>
        </div>
        <div class="tooltip-row">
          <span class="tooltip-label">Messages:</span>
          <span class="tooltip-value">{{ session.message_count }}</span>
        </div>
        <div v-if="extraFlags.length" class="tooltip-row">
          <span class="tooltip-label">Mode:</span>
          <span class="tooltip-value">{{ extraFlags.join(' · ') }}</span>
        </div>
        <div v-if="n0Binding" class="tooltip-row">
          <span class="tooltip-label">n0:</span>
          <span class="tooltip-value">{{ n0BindingLabel }}</span>
        </div>
      </div>
    </Teleport>

    <!-- Context Menu -->
    <Teleport to="body">
      <div v-if="showMenu" class="context-menu-overlay" @click="hideContextMenu"></div>
      <div v-if="showMenu" class="context-menu" :style="{ top: menuY + 'px', left: menuX + 'px' }">
        <button @click="editTagFromMenu" class="context-menu-item">
          {{ tagList.length ? 'Edit tags' : 'Add tag' }}
        </button>
        <button @click="deleteAllButThis" class="context-menu-item danger">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
            <polyline points="3 6 5 6 21 6"></polyline>
            <path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6m3 0V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2"></path>
            <line x1="10" y1="11" x2="10" y2="17"></line>
            <line x1="14" y1="11" x2="14" y2="17"></line>
          </svg>
          Delete All Sessions But This
        </button>
      </div>
    </Teleport>
  </div>
</template>

<script setup lang="ts">
import { computed, ref, watch, nextTick } from 'vue'
import { fetchAvatarById, getAvatarImageUrl, type Avatar } from '~/composables/useAvatarThemes'
import { useCharacterAvatar } from '~/composables/useCharacterAvatar'
import { useN0Bindings } from '~/composables/useN0Bindings'
import { useSessionDigests } from '~/composables/agents/useSessionDigests'
import { useClosable } from '~/composables/useKeyboardShortcuts'
import { readSessionTags, sessionTagsVersion, splitTags, writeSessionTagList } from '~/utils/sessionTags'
import { fitTags, tagChipStyle } from '~/utils/tagColor'
import { useBackgroundAgents } from '~/composables/agents/useBackgroundAgents'
import { subagentsWorkingCount, subagentsWorkingLabel } from '~/utils/agents/subagentsWorking'
import { loopLabel, loopShortLabel, useLoopClock, type ScheduledLoop } from '~/utils/agents/scheduledLoop'
import { isCodexRuntime, limitWaitDetail, limitWaitLabel, limitWaitShortLabel, type LimitWait } from '~/utils/agents/limitWait'
import { relativeTime, sessionActivityAt, workersWorkingLabel } from '~/utils/agents/sessionGroups'
import { useActivityPulse } from '~/composables/useActivityPulse'

interface Session {
  id: string
  status: string
  message_count: number
  model_name?: string
  created_at?: string
  updated_at?: string
  scheduled_loop?: ScheduledLoop | null
  limit_wait?: LimitWait | null
  subagents_running?: number
  cost_usd?: number
  selected_avatar_id?: number | null
  git_branch?: string
  worktree_path?: string
  options?: {
    workspace?: { working_directory: string; worktree_path: string; branch: string }
    model?: string
    provider?: string
    enable_rtk?: boolean
    sandboxed?: boolean | null
    codex_network_access?: boolean
    enabled_skill_ids?: number[]
    mode?: string
  }
  project_area?: {
    id: string
    name: string
    icon: string
    color: string
  }
}

interface Props {
  session: Session
  isActive: boolean
  isFocused?: boolean
  initialPrompt?: string
  /** full: avatar, summary, tags and context (Needs you, Working); compact: one line (Today, Earlier). */
  variant?: 'full' | 'compact'
  /** What the session waits for from the user ("Allow gh pr merge 1124?"); '' when nothing. */
  waitingFor?: string
  /** Worker sessions it started that are working (their rows hang under this one). */
  workersWorking?: number
  /** The sidebar is wide (main nav collapsed): room for more tags. */
  wide?: boolean
  /** Current time for the row's timestamp (the sidebar's shared clock). */
  now?: number
  /** Select mode: a click toggles the row's checkbox instead of opening it. */
  selectable?: boolean
  selected?: boolean
  contextUsage?: {
    percentage?: number
    total_tokens?: number
    context_window?: number
    model?: string
  }
}

const props = withDefaults(defineProps<Props>(), {
  isFocused: false,
  variant: 'full',
  waitingFor: '',
  workersWorking: 0,
  wide: false,
  selectable: false,
  selected: false,
})

// The session's own turn has ended but subagents it launched are still working.
const { getAgentsForSession, hasFullList } = useBackgroundAgents()
const subagentsWorking = computed(() =>
  subagentsWorkingCount(getAgentsForSession(props.session.id), hasFullList(props.session.id), props.session.subagents_running))
const idleWithSubagents = computed(() => props.session.status === 'idle' && subagentsWorking.value > 0)
// ...or the worker sessions it started are.
const idleWithWorkers = computed(() => props.session.status === 'idle' && props.workersWorking > 0)
const isWorking = computed(() => props.session.status === 'processing' || idleWithSubagents.value || idleWithWorkers.value)

// The sheen plays now and then on activity (the session's or a subagent's), not on a loop.
const { pulsing: sheenPulse } = useActivityPulse(
  () => `${sessionActivityAt(props.session)}|${props.session.message_count ?? 0}|${getAgentsForSession(props.session.id)[0]?.updated_at ?? ''}`,
  isWorking,
)
const hasStatusLine = computed(() => !!props.waitingFor || idleWithSubagents.value || idleWithWorkers.value)

// A Claude Code /loop: when its next run is due.
const loopNow = useLoopClock()
const clockNow = computed(() => props.now ?? loopNow.value)
const loopShort = computed(() => loopShortLabel(props.session.scheduled_loop, props.session.status, clockNow.value))
const loopTitle = computed(() => {
  const label = loopLabel(props.session.scheduled_loop, props.session.status, clockNow.value)
  const reason = props.session.scheduled_loop?.reason
  return reason ? `${label}: ${reason}` : label
})

// Waiting for the usage limit to reset: when it continues.
const limitShort = computed(() => limitWaitShortLabel(props.session.limit_wait, clockNow.value))
const limitTitle = computed(() =>
  `${limitWaitLabel(props.session.limit_wait, clockNow.value)}. ${limitWaitDetail(props.session.limit_wait)}`)

// Last activity: "now", "4m", "2h", "Mon"
const activityAt = computed(() => sessionActivityAt(props.session))
const timeLabel = computed(() => relativeTime(activityAt.value, clockNow.value))
const timeTitle = computed(() => activityAt.value ? `Last activity ${new Date(activityAt.value).toLocaleString()}` : '')

// An empty observed path means the agent returned to the main checkout.
const workspacePath = computed(() => props.session.options?.workspace?.worktree_path ?? props.session.worktree_path ?? '')
const workspaceBranch = computed(() => props.session.options?.workspace?.branch ?? props.session.git_branch ?? '')

const emit = defineEmits<{
  (e: 'select', sessionId: string): void
  (e: 'end', sessionId: string): void
  (e: 'delete', sessionId: string): void
  (e: 'delete-all-but-this', sessionId: string): void
  (e: 'toggle-select', sessionId: string, range: boolean): void
}>()

function onRowClick(event: MouseEvent) {
  if (props.selectable) emit('toggle-select', props.session.id, event.shiftKey)
  else emit('select', props.session.id)
}

function onTagClick(event: MouseEvent) {
  if (props.selectable) emit('toggle-select', props.session.id, event.shiftKey)
  else startEditTag()
}

function onContextMenu(event: MouseEvent) {
  if (!props.selectable) showContextMenu(event)
}

// Tag state
const isEditingTag = ref(false)
const tagInputValue = ref('')
const tagInputEl = ref<HTMLInputElement | null>(null)

const sessionSkillCount = computed(() => props.session.options?.enabled_skill_ids?.length || 0)

// n0 binding (shared cache — fetched once for the whole list by SessionsSidebar)
const { getBinding: getN0Binding } = useN0Bindings()
const n0Binding = computed(() => getN0Binding(props.session.id))
const n0BindingLabel = computed(() => n0Binding.value?.app_subdomain || n0Binding.value?.app_name || 'new app')

// AI summary + topics (shared cache, fetched once for the whole list by SessionsSidebar)
const { getDigest } = useSessionDigests()
const digest = computed(() => getDigest(props.session.id))
const descriptionText = computed(() => digest.value?.summary || props.initialPrompt || '')

// Things that used to clutter the row; shown in the hover tooltip instead
const extraFlags = computed(() => {
  const flags: string[] = []
  if (props.session.options?.sandboxed === true) flags.push('Sandboxed')
  if (props.session.options?.sandboxed === true && isCodexRuntime(props.session.options?.provider, props.session.model_name || props.session.options?.model)) {
    flags.push(props.session.options?.codex_network_access === true ? 'Network unrestricted' : 'Network off')
  }
  if (props.session.options?.enable_rtk) flags.push('RTK')
  if (props.session.options?.mode === 'loop') flags.push('Loop')
  if (sessionSkillCount.value > 0) flags.push(`${sessionSkillCount.value} skill${sessionSkillCount.value === 1 ? '' : 's'}`)
  if (props.session.project_area) flags.push(props.session.project_area.name)
  return flags
})

// The user's tags (utils/sessionTags.ts), one chip each
const tagList = computed(() => {
  sessionTagsVersion.value // bumped by every write (utils/sessionTags.ts)
  return splitTags(readSessionTags()[props.session.id])
})
const fittedTags = computed(() => {
  const budget = props.variant === 'compact' ? 20 : 28
  return fitTags(tagList.value, props.wide ? Math.round(budget * 1.6) : budget, props.variant === 'compact' ? 3 : 4)
})

function startEditTag() {
  tagInputValue.value = tagList.value.join(', ')
  isEditingTag.value = true
  nextTick(() => {
    tagInputEl.value?.focus()
  })
}

function saveTag() {
  if (!isEditingTag.value) return
  writeSessionTagList(props.session.id, splitTags(tagInputValue.value))
  isEditingTag.value = false
}

function cancelEditTag() {
  isEditingTag.value = false
}

// Context menu state
const showMenu = ref(false)
const menuX = ref(0)
const menuY = ref(0)

// Avatar broken state - hides broken images and shows fallback
const avatarBroken = ref(false)

// Reset broken state when avatar changes
watch(() => props.session.selected_avatar_id, () => {
  avatarBroken.value = false
})

// Tooltip state
const showTooltip = ref(false)
const tooltipPosition = ref<{ top: number; left: number } | null>(null)

// Session item ref for tooltip positioning
const sessionItemEl = ref<HTMLElement | null>(null)

const handleTooltipShow = () => {
  showTooltip.value = true
  if (sessionItemEl.value) {
    const rect = sessionItemEl.value.getBoundingClientRect()
    tooltipPosition.value = {
      top: rect.top + window.scrollY + rect.height / 2, // vertically centered with the item
      left: rect.left + window.scrollX + rect.width + 16 // 16px to the right of the item
    }
  }
}

const handleTooltipHide = () => {
  showTooltip.value = false
}

const showContextMenu = (event: MouseEvent) => {
  menuX.value = event.clientX
  menuY.value = event.clientY
  showMenu.value = true
}

const editTagFromMenu = () => {
  showMenu.value = false
  startEditTag()
}

const hideContextMenu = () => {
  showMenu.value = false
}

const deleteAllButThis = () => {
  emit('delete-all-but-this', props.session.id)
  hideContextMenu()
}

// Escape closes the context menu (shortcut registry's Escape stack)
useClosable(showMenu, hideContextMenu)

const persistentAvatar = ref<Avatar | null>(null)

// Load persistent avatar when selected_avatar_id changes
const loadPersistentAvatar = async (avatarId: number | null | undefined) => {
  if (!avatarId) {
    persistentAvatar.value = null
    return
  }
  try {
    const avatar = await fetchAvatarById(avatarId)
    if (avatar) {
      persistentAvatar.value = avatar
    } else {
      console.warn(`Failed to fetch avatar ${avatarId} for session ${props.session.id}`)
      persistentAvatar.value = null
    }
  } catch (err) {
    console.error('Error loading avatar:', err)
    persistentAvatar.value = null
  }
}

// Load on mount if an avatar ID is already present, reload when it changes
watch(() => props.session.selected_avatar_id, (newAvatarId, oldAvatarId) => {
  if (newAvatarId !== oldAvatarId) {
    loadPersistentAvatar(newAvatarId)
  }
}, { immediate: true })

// Compute effective avatar (persistent or hash-based fallback)
const effectiveAvatar = computed(() => {
  if (persistentAvatar.value) {
    return {
      name: persistentAvatar.value.name,
      image: getAvatarImageUrl(persistentAvatar.value),
      color: persistentAvatar.value.color || '#95A5A6',
    }
  }
  // Fall back to hash-based cat avatar
  const character = useCharacterAvatar(props.session.id)
  return {
    name: character.name,
    image: character.avatar,
    color: character.color,
  }
})

// Context usage: a small labelled meter on full rows
const contextPercent = computed(() => props.contextUsage?.percentage || 0)
const progressBarColor = computed(() => {
  const percentage = contextPercent.value
  if (percentage < 25) return 'var(--progress-start)'
  if (percentage < 50) return 'var(--progress-low)'
  if (percentage < 75) return 'var(--progress-medium)'
  return 'var(--progress-high)'
})
const contextTitle = computed(() => {
  const u = props.contextUsage
  const pct = `Context ${Math.round(contextPercent.value)}% full`
  return u?.total_tokens ? `${pct} (${formatTokens(u.total_tokens)} / ${formatTokens(u.context_window)} tokens)` : pct
})

// Format token counts for display (e.g., 75000 -> 75.0k)
const formatTokens = (tokens: number | undefined): string => {
  if (!tokens) return '0'
  if (tokens >= 1000) {
    return (tokens / 1000).toFixed(1) + 'k'
  }
  return tokens.toString()
}
</script>

<style scoped>
.session-item {
  --row-pad-x: 0.6rem;
  display: flex;
  align-items: flex-start;
  gap: 0.6rem;
  padding: 0.55rem var(--row-pad-x);
  border-radius: 8px;
  border: 1px solid transparent;
  cursor: pointer;
  position: relative;
  transition: background-color 0.15s ease, border-color 0.15s ease;
}

.session-item.variant-compact {
  align-items: center;
  gap: 0.5rem;
  padding: 0.3rem var(--row-pad-x);
}

.session-item:hover {
  background: var(--bg-secondary);
}

/* The open session: one accent bar on the left */
.session-item.active {
  background: var(--overlay-bg-active);
}
.session-item.active::before {
  content: '';
  position: absolute;
  left: 0;
  top: 6px;
  bottom: 6px;
  width: 3px;
  border-radius: 0 3px 3px 0;
  background: var(--accent-purple);
}

.session-item.focused {
  box-shadow: inset 0 0 0 1px color-mix(in srgb, var(--accent-purple) 45%, transparent);
}

/* Waiting on you: amber tint and border */
.session-item.needs-you {
  background: color-mix(in srgb, var(--status-warning, #d97706) 11%, var(--card-bg));
  border-color: color-mix(in srgb, var(--status-warning, #d97706) 45%, transparent);
}
.session-item.needs-you:hover {
  background: color-mix(in srgb, var(--status-warning, #d97706) 17%, var(--card-bg));
}

.session-item.ended .session-info,
.session-item.ended .session-avatar {
  opacity: 0.6;
}

/* ── Avatar ── */
.session-avatar {
  width: 32px;
  height: 32px;
  border-radius: 50%;
  flex-shrink: 0;
  display: flex;
  align-items: center;
  justify-content: center;
  background: color-mix(in srgb, var(--avatar-color) 14%, transparent);
  border: 1px solid color-mix(in srgb, var(--avatar-color) 70%, transparent);
  position: relative;
}
.variant-compact .session-avatar {
  width: 22px;
  height: 22px;
}

.session-avatar-img {
  width: 100%;
  height: 100%;
  border-radius: 50%;
  object-fit: contain;
  object-position: center;
  display: block;
}

/* Resting agents: a little quieter, never grey */
.session-avatar.resting .session-avatar-img {
  filter: saturate(0.7);
}

.session-avatar-fallback {
  font-size: 0.8rem;
  font-weight: 700;
  color: var(--text-primary);
  text-transform: uppercase;
  user-select: none;
}
.variant-compact .session-avatar-fallback {
  font-size: 0.65rem;
}

/* Working: a small pulsing dot on the avatar's corner */
.session-status-dot {
  position: absolute;
  right: -2px;
  bottom: -2px;
  width: 10px;
  height: 10px;
  border-radius: 50%;
  background: var(--status-success, #22c55e);
  border: 2px solid var(--card-bg);
  animation: session-dot-pulse 1.8s ease-in-out infinite;
}
.session-status-dot.subagents {
  background: var(--status-processing, #c2185b);
}
.variant-compact .session-status-dot {
  width: 8px;
  height: 8px;
  right: -3px;
  bottom: -3px;
  border-width: 1.5px;
}

@keyframes session-dot-pulse {
  50% { opacity: 0.35; }
}

/* Working: now and then (on activity, see useActivityPulse) a faint sheen crosses the row once */
.session-item.sheen-pulse::after {
  content: '';
  position: absolute;
  inset: 0;
  border-radius: inherit;
  pointer-events: none;
  background: linear-gradient(
    105deg,
    transparent 40%,
    color-mix(in srgb, var(--avatar-color) 10%, transparent) 50%,
    transparent 60%
  ) no-repeat;
  background-size: 250% 100%;
  animation: session-row-sheen 2.2s ease-in-out both;
}

@keyframes session-row-sheen {
  from { background-position: 130% 0; }
  to { background-position: -30% 0; }
}

@media (prefers-reduced-motion: reduce) {
  .session-status-dot { animation: none; }
  .session-item.sheen-pulse::after { display: none; }
}

/* ── Text ── */
.session-info {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 0.2rem;
}

.session-name-row {
  display: flex;
  align-items: center;
  gap: 0.4rem;
  min-width: 0;
}

.session-name {
  /* Keeps its width; the tags beside it shorten first when the row is full */
  flex: 0 1 auto;
  min-width: 3.5em;
  font-weight: 600;
  font-size: 0.875rem;
  color: var(--text-primary);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.variant-compact .session-name {
  font-size: 0.84rem;
  font-weight: 500;
}

.session-loop {
  flex: 0 0 auto;
  font-size: 0.68rem;
  font-weight: 600;
  padding: 1px 6px;
  border-radius: 8px;
  color: var(--accent-cyan, #0891b2);
  background: color-mix(in srgb, var(--accent-cyan, #0891b2) 12%, transparent);
  white-space: nowrap;
}

.session-limit-wait {
  flex: 0 0 auto;
  font-size: 0.68rem;
  font-weight: 600;
  padding: 1px 6px;
  border-radius: 8px;
  color: var(--accent-orange, #c2410c);
  background: color-mix(in srgb, var(--accent-orange, #c2410c) 12%, transparent);
  white-space: nowrap;
}

/* Time on the right; on hover the row's actions take its place */
.session-time-slot {
  flex: 0 0 auto;
  margin-left: auto;
  min-width: 44px;
  display: flex;
  justify-content: flex-end;
  align-items: center;
  position: relative;
  height: 20px;
}

.session-time {
  font-size: 0.72rem;
  color: var(--text-muted, var(--text-secondary));
  font-variant-numeric: tabular-nums;
  white-space: nowrap;
}

.session-select-box {
  flex-shrink: 0;
  align-self: center;
  width: 16px;
  height: 16px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  border: 1.5px solid var(--text-secondary);
  border-radius: 4px;
  background: var(--card-bg);
  color: var(--card-bg);
  transition: background-color 0.12s ease, border-color 0.12s ease;
}
.session-select-box.checked {
  background: var(--accent-purple, #8b5cf6);
  border-color: var(--accent-purple, #8b5cf6);
}
.session-item.selectable { user-select: none; }
.session-item.selected {
  background: var(--overlay-bg-active, var(--bg-secondary));
}
@media (prefers-reduced-motion: reduce) {
  .session-select-box { transition: none; }
}

.session-row-actions {
  position: absolute;
  right: 0;
  top: 50%;
  transform: translateY(-50%);
  display: flex;
  gap: 2px;
  padding: 1px;
  border-radius: 6px;
  background: var(--bg-secondary);
  opacity: 0;
  pointer-events: none;
  transition: opacity 0.15s ease;
}

.session-item:hover .session-row-actions,
.session-item:focus-within .session-row-actions {
  opacity: 1;
  pointer-events: auto;
}
.session-item:hover .session-time,
.session-item:focus-within .session-time {
  visibility: hidden;
}

/* Touch screens have no hover: the open row shows its actions instead of its time */
@media (hover: none) {
  .session-item.active .session-row-actions { opacity: 1; pointer-events: auto; }
  .session-item.active .session-time { visibility: hidden; }
}

.btn-end-session,
.btn-delete-session {
  width: 20px;
  height: 20px;
  padding: 0;
  border: none;
  background: transparent;
  color: var(--text-secondary);
  cursor: pointer;
  border-radius: 4px;
  display: flex;
  align-items: center;
  justify-content: center;
}

.btn-end-session:hover,
.btn-delete-session:hover {
  background: color-mix(in srgb, var(--status-error, #dc3545) 12%, transparent);
  color: var(--status-error, #dc3545);
}

/* Status line: what it waits for, or its working subagents */
.session-waiting {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  min-width: 0;
}
.session-waiting-text {
  flex: 1 1 auto;
  min-width: 0;
  font-size: 0.8rem;
  font-weight: 600;
  color: var(--status-warning, #b45309);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.btn-review {
  flex: 0 0 auto;
  font: inherit;
  font-size: 0.72rem;
  font-weight: 600;
  padding: 2px 10px;
  border-radius: 999px;
  cursor: pointer;
  color: var(--text-primary);
  background: color-mix(in srgb, var(--status-warning, #d97706) 22%, var(--card-bg));
  border: 1px solid color-mix(in srgb, var(--status-warning, #d97706) 60%, transparent);
}
.btn-review:hover {
  background: color-mix(in srgb, var(--status-warning, #d97706) 32%, var(--card-bg));
}

.session-subagents {
  font-size: 0.78rem;
  font-weight: 600;
  color: var(--status-processing, #c2185b);
}

/* The session's description: AI summary, two lines at most */
.session-description {
  font-size: 0.8rem;
  color: var(--text-primary);
  line-height: 1.35;
  display: -webkit-box;
  -webkit-line-clamp: 2;
  line-clamp: 2;
  -webkit-box-orient: vertical;
  overflow: hidden;
  overflow-wrap: anywhere;
}
.session-description.one-line {
  -webkit-line-clamp: 1;
  line-clamp: 1;
}

/* No summary yet: the first prompt, quieter so it reads as a stand-in. */
.session-description.is-prompt {
  color: var(--text-secondary);
  font-style: italic;
}

/* Tags + context meter */
.session-meta {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  min-width: 0;
  margin-top: 0.1rem;
}

.session-tags {
  display: flex;
  align-items: center;
  gap: 4px;
  min-width: 0;
  flex: 0 20 auto;
  overflow: hidden;
}
.session-meta .session-tags {
  flex: 1 1 auto;
}

/*
 * A user tag. Only the hue comes from the tag (utils/tagColor.ts); text and
 * fill are mixed with theme colours, so the chip is dark-on-light in light
 * themes and light-on-dark in dark ones.
 */
.tag-chip {
  --tag-base: hsl(var(--tag-hue, 260) 75% 50%);
  /* Full text while the row has room; ellipsized only when it runs out */
  flex: 0 1 auto;
  min-width: 2.5em;
  font-size: 0.72rem;
  font-weight: 600;
  line-height: 1.3;
  padding: 1px 8px;
  border-radius: 999px;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  cursor: pointer;
  color: color-mix(in oklab, var(--tag-base) 45%, var(--text-primary));
  background: color-mix(in srgb, var(--tag-base) 15%, var(--card-bg));
  border: 1px solid color-mix(in srgb, var(--tag-base) 50%, var(--card-bg));
}
.tag-chip:hover {
  background: color-mix(in srgb, var(--tag-base) 24%, var(--card-bg));
}
.tag-chip.tag-more {
  flex: 0 0 auto;
  min-width: 0;
  color: var(--text-secondary);
  background: transparent;
  border-color: var(--border-color);
}

.context-meter {
  flex: 0 0 auto;
  margin-left: auto;
  display: inline-flex;
  align-items: center;
  gap: 5px;
}
.context-meter-track {
  width: 32px;
  height: 4px;
  border-radius: 2px;
  background: color-mix(in srgb, var(--text-primary) 12%, transparent);
  overflow: hidden;
}
.context-meter-fill {
  display: block;
  height: 100%;
  border-radius: 2px;
  background: var(--progress-color);
}
.context-meter-label {
  font-size: 0.7rem;
  color: var(--text-muted, var(--text-secondary));
  font-variant-numeric: tabular-nums;
}

.tag-input {
  font-size: 0.75rem;
  font-weight: 500;
  padding: 3px 8px;
  border-radius: 8px;
  background: var(--card-bg);
  color: var(--text-primary);
  border: 1px solid var(--accent-purple);
  outline: none;
  width: 100%;
  max-width: 240px;
  line-height: 1.4;
}

/* Tooltip extras */
.tooltip-summary {
  font-size: 0.85rem;
  line-height: 1.4;
  margin-bottom: 6px;
  color: var(--text-primary);
}
.tooltip-summary.is-prompt {
  color: var(--text-secondary);
  font-style: italic;
}
.tooltip-topics {
  display: flex;
  flex-wrap: wrap;
  gap: 4px;
  margin-bottom: 8px;
}
.session-topic {
  font-size: 0.65rem;
  line-height: 1;
  padding: 3px 7px;
  border-radius: 8px;
  color: var(--text-secondary);
  border: 1px solid var(--border-color, rgba(128, 128, 128, 0.35));
}

/* Context Menu */
.context-menu-overlay {
  position: fixed;
  top: 0;
  left: 0;
  width: 100%;
  height: 100%;
  z-index: 999;
}

.context-menu {
  position: fixed;
  background: var(--card-bg);
  border: 1px solid var(--border-color);
  border-radius: 8px;
  box-shadow: 0 4px 16px rgba(0, 0, 0, 0.15);
  z-index: 1000;
  min-width: 200px;
  overflow: hidden;
}

.context-menu-item {
  display: flex;
  align-items: center;
  gap: 8px;
  width: 100%;
  padding: 8px 12px;
  background: none;
  border: none;
  color: var(--text-primary);
  cursor: pointer;
  font-size: 0.9rem;
  text-align: left;
  transition: all 0.2s;
}

.context-menu-item:hover {
  background: var(--bg-secondary);
}

.context-menu-item.danger {
  color: var(--status-error, #dc3545);
}

.context-menu-item.danger:hover {
  background: rgba(220, 53, 69, 0.1);
}

/* Session Info Tooltip */
.session-tooltip {
  position: fixed;
  background: var(--card-bg);
  border: 1px solid var(--border-color);
  border-radius: 8px;
  padding: 10px 14px;
  font-size: 0.8rem;
  white-space: normal;
  max-width: 340px;
  z-index: 10000;
  pointer-events: none;
  box-shadow: 0 4px 16px rgba(0, 0, 0, 0.2);
  animation: tooltipFadeIn 0.2s ease forwards;
  transform: translateY(-50%);
}

@keyframes tooltipFadeIn {
  from {
    opacity: 0;
    transform: translateY(-50%) translateX(-4px);
  }
  to {
    opacity: 1;
    transform: translateY(-50%) translateX(0);
  }
}

.tooltip-row {
  display: flex;
  gap: 8px;
  align-items: center;
  color: var(--text-primary);
}

.tooltip-row:not(:last-child) {
  margin-bottom: 6px;
}

.tooltip-label {
  color: var(--text-secondary);
  font-weight: 500;
  min-width: 75px;
  text-transform: uppercase;
  font-size: 0.75rem;
  letter-spacing: 0.5px;
}

.tooltip-value {
  color: var(--text-primary);
  font-weight: 700;
  font-family: 'Monaco', 'Courier New', monospace;
  background: var(--bg-secondary);
  padding: 2px 6px;
  border-radius: 4px;
  display: inline-block;
}

@media (max-width: 768px) {
  .session-item {
    font-size: 0.85rem;
  }
}
</style>
