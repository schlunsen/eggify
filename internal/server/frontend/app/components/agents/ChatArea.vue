<template>
  <div class="chat-main-area">
    <div v-if="!hasActiveSession" class="no-session-selected" :class="{ 'has-nudge': props.showFirstProjectNudge }">
      <slot v-if="props.showFirstProjectNudge" name="first-project-nudge" />
      <div v-else class="empty-state">
        <svg width="64" height="64" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" opacity="0.5">
          <path d="M21 11.5a8.38 8.38 0 0 1-.9 3.8 8.5 8.5 0 0 1-7.6 4.7 8.38 8.38 0 0 1-3.8-.9L3 21l1.9-5.7a8.38 8.38 0 0 1-.9-3.8 8.5 8.5 0 0 1 4.7-7.6 8.38 8.38 0 0 1 3.8-.9h.5a8.48 8.48 0 0 1 8 8v.5z"/>
        </svg>
        <p>Select a session or create a new one to start</p>
      </div>
    </div>

    <div v-else class="chat-content" :class="{ 'zen-composer-open': displayMode === 'zen' && activeTab === 'chat' && showZenInput }" :style="displayMode === 'zen' && composerHeight > 0 ? { '--zen-composer-h': `${composerHeight}px` } : undefined">
      <!-- Terminal/Chat Toggle Header -->
      <ChatHeader
        v-if="!zenFullscreen"
        v-model:active-tab="activeTab"
        :view-mode="viewMode"
        :chat-width="chatWidth"
        @update:chat-width="setChatWidth"
        :active-filter="props.activeFilter"
        :message-filters="props.messageFilters"
        :current-model="props.currentModel"
        :model-switch-disabled-reason="props.modelSwitchDisabledReason"
        :media-count="mediaCount"
        @change-model="$emit('change-model')"
        @update:view-mode="$emit('update:view-mode', $event)"
        @update:active-filter="$emit('update:active-filter', $event)"
      />

      <!-- Initial Prompt Banner -->
      <InitialPromptBanner
        v-if="props.initialPrompt"
        :prompt-text="props.initialPrompt"
        :images="props.initialPromptImages || []"
      />

      <!-- Folder has its own Claude config that is not loaded until trusted -->
      <WorkspaceTrustBanner
        v-if="props.sessionId"
        :session-id="props.sessionId"
        :refresh-key="props.messageCount"
      />

      <!-- Waiting for the usage limit to reset; continues by itself -->
      <LimitWaitBanner
        v-if="props.sessionId && props.limitWait"
        :session-id="props.sessionId"
        :limit-wait="props.limitWait"
      />

      <!-- A worker's own chat: whose worker it is. Not in Zen. -->
      <WorkerNote
        v-if="props.workerNote && displayMode !== 'zen'"
        :note="props.workerNote"
        :parent-id="props.workerParentId"
        @open="$emit('open-session', $event)"
      />

      <!-- This session's workers: Plan → Build → Review → Merge. Not in Zen. -->
      <TeamStrip
        v-if="props.team && displayMode !== 'zen'"
        :progress="props.team"
        @open="$emit('open-session', $event)"
      />

      <!-- Tool Overlays -->
      <slot name="tool-overlays"></slot>

      <!-- TodoWrite Box -->
      <slot name="todo-box"></slot>

      <!-- Zen Mode View — outside KeepAlive for proper singleton lifecycle.
           The Three.js scene persists at module level; mount/unmount just
           attaches/detaches the canvas and pauses/resumes animation. -->
      <ZenMode
        v-if="displayMode === 'zen' && activeTab === 'chat'"
        ref="zenModeRef"
        :class="{ 'zen-leaving': zenLeaving }"
        :session-id="props.sessionId || ''"
        :session-status="(props.isProcessing || props.isThinking) ? 'processing' : props.sessionStatus === 'ended' ? 'ended' : 'idle'"
        :awaiting-user="!!props.awaitingUser"
        :context-percent="props.contextPercent"
        :message-count="props.messageCount"
        :token-count="props.tokenCount"
        :last-assistant-message="props.lastAssistantMessage"
        :last-user-message="props.lastUserMessage"
        :project-id="props.projectId"
        :project-name="props.projectName"
        :project-color="props.projectColor"
        :git-branch="props.gitBranch"
        :git-stats="props.gitStats"
        :avatar-image="props.avatarImage"
        :avatar-name="props.avatarName"
        :avatar-color="props.avatarColor"
        :recent-sessions="props.recentSessions"
        :session-notices="props.sessionNotices"
        :working-sessions="props.workingSessions"
        @dismiss-notice="emit('dismiss-notice', $event)"
        :recent-assistant-messages="props.recentAssistantMessages"
        :is-dictating="isDictating && !showZenInput"
        :input-message="isDictating ? props.inputMessage : ''"
        :composing="!!props.inputMessage"
        :turn-images="props.turnImages"
        @update:fullscreen="handleZenFullscreen"
        @select-session="emit('select-session', $event)"
        @open-lightbox="emit('open-lightbox', $event)"
      />

      <!-- Zen: prompts that block the agent (permission requests) must stay
           answerable in zen — windowed and fullscreen. The chat view that
           normally hosts them is not rendered in zen. -->
      <Transition name="zen-attention-fade">
        <div
          v-if="displayMode === 'zen' && activeTab === 'chat' && (props.pendingPermissionCount ?? 0) > 0"
          class="zen-attention"
          :class="{ 'zen-attention-fullscreen': zenFullscreen }"
          role="alertdialog"
          aria-label="The agent needs your approval"
        >
          <slot name="permissions" variant="zen"></slot>
        </div>
      </Transition>

      <!-- Terminal, Chat, and Files Views with KeepAlive to preserve state.
           Nothing but the v-if chain may sit inside it: in dev, comments count
           as children and KeepAlive wants exactly one. -->
      <KeepAlive>
        <div v-if="activeTab === 'terminal'" class="terminal-view">
          <SessionTerminal
            ref="sessionTerminalRef"
            :session-id="props.sessionId || ''"
            :project-id="props.projectId"
            @close="activeTab = 'chat'"
          />
        </div>

        <!-- File Explorer View -->
        <div v-else-if="activeTab === 'files'" class="files-view">
          <FileExplorer
            ref="fileExplorerRef"
            :project-id="props.projectId || ''"
          />
        </div>

        <!-- Image Gallery View -->
        <div v-else-if="activeTab === 'images'" class="images-view">
          <ImageGallery
            :messages="props.messages || []"
          />
        </div>

        <!-- Media View: the session's video and audio files -->
        <div v-else-if="activeTab === 'media'" class="media-view">
          <SessionMediaList :session-id="props.sessionId || ''" />
        </div>

        <!-- Chat View (only when not in zen mode) -->
        <div v-else-if="displayMode !== 'zen'" class="chat-view" :class="{ 'chat-view-transitioning': isTransitioning, 'chat-view-leaving': chatLeaving }" :style="{ opacity: chatViewOpacity }">
          <!-- Messages Container -->
          <ChatMessages
            ref="chatMessagesRef"
            :chat-width="chatWidth"
            :is-thinking="props.isThinking"
            :is-processing="props.isProcessing"
            :is-generating-summary="props.isGeneratingSummary"
            :show-scroll-button="props.showScrollButton"
            :chat-opacity="chatOpacity"
            :is-transitioning="isTransitioning"
            :show-loading-spinner="showLoadingSpinner"
            @scroll="onMessagesScroll"
            @scroll-to-bottom="handleScrollToBottomClick"
          >
            <!-- Pass through messages slot -->
            <template #messages>
              <slot name="messages"></slot>
            </template>
          </ChatMessages>

          <!-- Floating subagents card (PiP) -->
          <slot name="subagents-pip"></slot>

          <!-- Permission Requests -->
          <slot name="permissions"></slot>

          <!-- Tool Execution Bar -->
          <slot name="tool-execution"></slot>
        </div>
      </KeepAlive>

      <!-- Chat Input — shared between Chat and Zen modes (not terminal or files).
           In fullscreen zen it floats over the scene as a collapsible bar, so you
           can keep working without leaving fullscreen. -->
      <div
        v-if="activeTab === 'chat'"
        ref="composerWrapRef"
        class="chat-input-wrapper"
        :class="{ 'zen-input': displayMode === 'zen', 'zen-input-fullscreen': displayMode === 'zen' && zenFullscreen, 'zen-input-collapsed': displayMode === 'zen' && !showZenInput, 'zen-input-paper': zenPaper }"
      >
        <!-- Zen: input toggle + mic orb. While voice is live the orb expands into
             the listening bar and takes the toggle's place. -->
        <div v-if="displayMode === 'zen'" class="zen-input-row">
        <button
          v-if="!zenVoiceLive"
          class="zen-input-toggle"
          @click="toggleZenInput"
          :title="showZenInput ? 'Hide input' : 'Show input (or just start typing)'"
          :aria-expanded="showZenInput"
        >
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
            <path v-if="showZenInput" d="M6 9l6 6 6-6"/>
            <path v-else d="M6 15l6-6 6 6"/>
          </svg>
          <span>{{ showZenInput ? 'Hide input' : 'Show input — or just start typing' }}</span>
        </button>
        <ZenVoiceBar
          :live="isDictating || voiceRecording.isRecording.value"
          :transcribing="whisperTranscription.isTranscribing.value"
          :duration="isDictating ? parakeetTranscription.audioDuration.value : voiceRecording.duration.value"
          :hint="isDictating ? 'Enter sends' : 'Space finishes'"
          :disabled="!(props.connected || props.hasActiveSession) || isAnyModelLoading"
          @start="startVoiceRecording"
          @finish="isDictating ? stopDictation() : finishRecording()"
          @cancel="cancelVoiceRecording"
        />
        </div>

        <div class="zen-input-collapsible" :class="{ collapsed: displayMode === 'zen' && !showZenInput }">
          <ChatInput
            ref="chatInputRef"
            v-model:input-message="localInputMessage"
            :connected="props.connected"
            :has-active-session="props.hasActiveSession"
            :is-dictating="isDictating"
            :dictation-duration="parakeetTranscription.audioDuration.value"
            :autocomplete-handlers="autocompleteHandlers"
            :variant="displayMode === 'zen' ? 'zen' : 'default'"
            :paper="zenPaper"
            :busy="!!(props.isProcessing || props.isThinking)"
            :activity="activityKey"
            :subagents-working="props.subagentsWorking"
            @open-subagents="emit('open-subagents')"
            @send="handleSend"
            @interrupt="emit('interrupt')"
            @search="$emit('search')"
            @open-fullscreen="openFullscreenEditor"
            @start-recording="startVoiceRecording"
            @stop-dictation="stopDictation"
            @images-attached="handleImagesAttached"
          />
        </div>
      </div>
    </div>

    <!-- Slash Command Autocomplete -->
    <SlashCommandAutocomplete
      :visible="showAutocomplete"
      :query="autocompleteQuery"
      :position="autocompletePosition"
      :is-feature-mode="autocompleteFeatureMode"
      :available-features="autocompleteFeatureNames"
      :variant="displayMode === 'zen' && activeTab === 'chat' ? 'zen' : 'default'"
      @select="selectAutocompleteCommand"
      @select-feature="selectAutocompleteFeature"
      @dismiss="dismissAutocomplete"
    />

    <!-- Fullscreen Editor Modal -->
    <FullscreenEditorModal
      :show="showFullscreenEditor"
      v-model:text="fullscreenText"
      @close="closeFullscreenEditor"
      @save="saveFullscreenText"
    />

    <!-- Recording Modal (Whisper batch mode + Parakeet model loading) -->
    <RecordingModal
      :show="showRecordingModal || (displayMode === 'zen' && isDictating)"
      :is-recording="voiceRecording.isRecording.value || isDictating"
      :is-transcribing="whisperTranscription.isTranscribing.value"
      :is-model-loading="isAnyModelLoading"
      :progress="activeModelProgress"
      :duration="isDictating ? parakeetTranscription.audioDuration.value : voiceRecording.duration.value"
      :error="voiceRecording.error.value || whisperTranscription.error.value || parakeetTranscription.error.value"
      :model-name="sttEngine === 'parakeet' ? 'Parakeet v3' : 'Whisper'"
      :model-cached="sttEngine === 'parakeet' ? parakeetTranscription.modelFromCache.value : null"
      :variant="displayMode === 'zen' ? 'zen' : 'default'"
      @finish="isDictating ? stopDictation() : finishRecording()"
      @cancel="cancelVoiceRecording"
    />
  </div>
</template>

<script setup lang="ts">
import { ref, computed, watch, nextTick, onMounted, onUnmounted } from 'vue'
import { useVoiceRecording } from '~/composables/useVoiceRecording'
import { describeMicError } from '~/utils/micCapture'
import { useChatWidth } from '~/composables/useChatWidth'
import { useWhisperTranscription } from '~/composables/useWhisperTranscription'
import { useParakeetTranscription, preloadParakeetModel } from '~/composables/useParakeetTranscription'
import { useSlashCommandAutocomplete } from '~/composables/useSlashCommandAutocomplete'
import { type SlashCommand, registerSkillCommands, clearSkillCommands } from '~/composables/useSlashCommands'
import SlashCommandAutocomplete from '~/components/SlashCommandAutocomplete.vue'
import { defineAsyncComponent } from 'vue'
import ChatHeader from '~/components/agents/ChatHeader.vue'
import type ZenModeComponent from '~/components/agents/ZenMode.vue'
import { preloadZenScene } from '~/composables/agents/useZenAnimation'
import InitialPromptBanner from '~/components/agents/InitialPromptBanner.vue'
import WorkspaceTrustBanner from '~/components/agents/WorkspaceTrustBanner.vue'
import LimitWaitBanner from '~/components/agents/LimitWaitBanner.vue'
import TeamStrip from '~/components/agents/TeamStrip.vue'
import WorkerNote from '~/components/agents/WorkerNote.vue'
import ChatMessages from '~/components/agents/ChatMessages.vue'
import ChatInput from '~/components/agents/ChatInput.vue'
import FullscreenEditorModal from '~/components/agents/FullscreenEditorModal.vue'
import RecordingModal from '~/components/agents/RecordingModal.vue'
import ZenVoiceBar from '~/components/agents/ZenVoiceBar.vue'
import { useZenViz } from '~/composables/agents/useZenViz'
import { useSessionMedia } from '~/composables/agents/useSessionMedia'
import { useShortcut, useClosable, useEscapeAction } from '~/composables/useKeyboardShortcuts'

const FileExplorer = defineAsyncComponent(() => import('./FileExplorer.vue'))
// Heavy views (xterm, the zen scene) are only loaded when they are shown.
const SessionTerminal = defineAsyncComponent(() => import('./SessionTerminal.vue'))
const ZenMode = defineAsyncComponent(() => import('./ZenMode.vue'))
const ImageGallery = defineAsyncComponent(() => import('./ImageGallery.vue'))
const SessionMediaList = defineAsyncComponent(() => import('./SessionMediaList.vue'))

interface AttachedImage {
  fileName: string
  mediaType: string
  size: number
  dataUrl: string
  base64Data: string
}

interface Props {
  showFirstProjectNudge?: boolean
  currentModel?: string
  modelSwitchDisabledReason?: string
  hasActiveSession: boolean
  inputMessage: string
  connected: boolean
  isThinking: boolean
  isProcessing: boolean
  isGeneratingSummary?: boolean
  hasModalOpen?: boolean
  showScrollButton?: boolean
  sessionId?: string
  /** Subagents still working; the composer shows them while the main agent is idle */
  subagentsWorking?: number
  /** The session waits for the usage limit to reset (utils/agents/limitWait.ts) */
  limitWait?: import('~/utils/agents/limitWait').LimitWait | null
  /** The session's build–review work, when it started workers or is a team (utils/agents/team.ts) */
  team?: import('~/utils/agents/team').TeamProgress | null
  /** The session is a worker: the line that says whose (workerNote in utils/agents/team.ts) */
  workerNote?: { lead: string; parentName: string; detail: string } | null
  /** The worker's parent session, '' when it is no longer in the list */
  workerParentId?: string
  projectId?: string
  messages?: any[]
  initialPrompt?: string
  initialPromptImages?: { dataUrl: string; mediaType: string }[]
  onScroll?: (container: HTMLElement | null) => void
  onScrollToBottom?: (container: HTMLElement | null, smooth?: boolean) => void
  viewMode?: 'live' | 'zen'
  contextPercent?: number | null
  messageCount?: number
  tokenCount?: number
  lastAssistantMessage?: string
  lastUserMessage?: string
  projectName?: string
  projectColor?: string
  gitBranch?: string
  gitStats?: { modified: number; untracked: number; staged: number; deleted: number; clean: boolean; ahead: number; behind: number } | null
  avatarImage?: string | null
  avatarName?: string | null
  avatarColor?: string | null
  recentSessions?: any[]
  /** Stored status of the active session (so zen can show 'ended') */
  sessionStatus?: string
  /** Zen: other sessions that finished or need the user */
  sessionNotices?: any[]
  /** Zen: other sessions currently working */
  workingSessions?: any[]
  recentAssistantMessages?: any[]
  activeFilter?: string
  messageFilters?: { label: string; value: string; count: number }[]
  /** Pending permission requests for this session (rendered via the permissions slot). */
  pendingPermissionCount?: number
  /** The agent is blocked on the user (permission request or question). */
  awaitingUser?: boolean
  /** Images the agent saw in the current turn — shown in zen mode. */
  turnImages?: { dataUrl: string; mediaType: string; label?: string }[]
}

const props = defineProps<Props>()

const emit = defineEmits<{
  'change-model': []
  'update:input-message': [value: string]
  'send': []
  'interrupt': []
  'images-attached': [images: AttachedImage[]]
  'slash-command': [command: SlashCommand]
  'search': []
  'update:view-mode': [value: 'live' | 'zen']
  'dismiss-notice': [key: string]
  'update:zen-fullscreen': [value: boolean]
  'select-session': [sessionId: string]
  /** Open another session as it is (a worker's chip, a worker's parent) */
  'open-session': [sessionId: string]
  'open-subagents': []
  'update:active-filter': [value: string]
  'open-lightbox': [data: { images: { dataUrl: string; mediaType: string }[]; startIndex: number }]
}>()

// Refs to child components
const chatMessagesRef = ref<InstanceType<typeof ChatMessages> | null>(null)
const chatInputRef = ref<InstanceType<typeof ChatInput> | null>(null)
const sessionTerminalRef = ref<{ focusTerminal: () => void } | null>(null)
const zenModeRef = ref<InstanceType<typeof ZenModeComponent> | null>(null)

// Local state
const activeTab = ref<'chat' | 'terminal' | 'files' | 'images' | 'media'>('chat')
// Badge on the Media tab (the list is loaded by the agents page when a session opens)
const sessionMedia = useSessionMedia()
const mediaCount = computed(() => sessionMedia.stateFor(props.sessionId)?.items.length ?? 0)
const { chatWidth, setChatWidth } = useChatWidth()
const viewMode = computed(() => props.viewMode || 'live')
// What is actually rendered. Lags viewMode by a short fade-out so Live ↔ Zen
// crossfades instead of cutting.
const displayMode = ref<'live' | 'zen'>(viewMode.value)
// The Circus scene prints the zen composer on paper, like its HUD cards
const { activeViz: zenViz } = useZenViz()
const zenPaper = computed(() => displayMode.value === 'zen' && zenViz.value === 'circus')
const zenLeaving = ref(false)
const chatLeaving = ref(false)
const VIEW_FADE_OUT_MS = 200
let viewSwitchToken = 0
const showZenInput = ref(true)  // Whether chat input is visible in zen mode
const zenFullscreen = ref(false)  // Whether zen mode is in fullscreen
const showRecordingModal = ref(false)
const showFullscreenEditor = ref(false)
const fullscreenText = ref('')

// Toggle zen input and auto-focus
function toggleZenInput() {
  showZenInput.value = !showZenInput.value
  if (showZenInput.value) {
    nextTick(() => chatInputRef.value?.messageInput?.focus())
  }
}

async function focusComposer() {
  activeTab.value = 'chat'
  showZenInput.value = true
  await nextTick()
  const input = chatInputRef.value?.messageInput
  if (input && !input.disabled) input.focus({ preventScroll: true })
}

// Reveal the zen input (optionally seeding it with a typed character) and focus it.
function revealZenInput(seed = '') {
  showZenInput.value = true
  if (seed) emit('update:input-message', (props.inputMessage || '') + seed)
  nextTick(() => {
    const el = chatInputRef.value?.messageInput as HTMLTextAreaElement | undefined
    if (!el) return
    el.focus()
    const end = el.value.length
    el.setSelectionRange?.(end, end)
    // Let slash-command autocomplete see the seeded character
    if (seed) el.dispatchEvent(new Event('input', { bubbles: true }))
  })
}

function isEditableTarget(target: EventTarget | null): boolean {
  const el = target as HTMLElement | null
  if (!el || !el.tagName) return false
  const tag = el.tagName
  return tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT' || el.isContentEditable
}

// In zen, start typing anywhere to bring up the input (like a command palette).
// Enter alone just opens it.
function maybeSummonZenInput(event: KeyboardEvent): boolean {
  if (displayMode.value !== 'zen' || activeTab.value !== 'chat' || !props.hasActiveSession) return false
  if (showZenInput.value || event.metaKey || event.ctrlKey || event.altKey || event.isComposing) return false
  if (isEditableTarget(event.target) || props.hasModalOpen || showRecordingModal.value || showFullscreenEditor.value) return false
  const printable = event.key.length === 1 && event.key !== ' '
  if (!printable && event.key !== 'Enter') return false
  // Enter must keep activating focused buttons/links (e.g. "Approve Once")
  if (event.key === 'Enter' && (event.target as HTMLElement | null)?.closest?.('button, a, [role="button"], summary')) return false
  event.preventDefault()
  revealZenInput(printable ? event.key : '')
  return true
}

// When the agent finishes a turn in zen, bring the input back so you can reply
// straight away (it hides itself on send to keep the scene clean).
watch(
  () => !!(props.isProcessing || props.isThinking),
  (busy, wasBusy) => {
    if (busy || !wasBusy || displayMode.value !== 'zen' || showZenInput.value) return
    showZenInput.value = true
    // Only take focus if nothing else has it
    const active = document.activeElement
    if (!active || active === document.body) {
      nextTick(() => chatInputRef.value?.messageInput?.focus())
    }
  },
)

// Zen: the composer floats over the stage; its height (--zen-composer-h) keeps the
// response side panel above it. Observed only while Zen is shown.
const composerWrapRef = ref<HTMLElement | null>(null)
const composerHeight = ref(0)
let composerObserver: ResizeObserver | null = null
function stopComposerObserver() {
  composerObserver?.disconnect()
  composerObserver = null
}
watch(
  () => displayMode.value === 'zen' ? composerWrapRef.value : null,
  (el) => {
    stopComposerObserver()
    if (!el || typeof ResizeObserver === 'undefined') return
    composerHeight.value = Math.ceil(el.offsetHeight)
    composerObserver = new ResizeObserver((entries) => {
      const box = entries[0]?.borderBoxSize?.[0]
      const h = Math.ceil(box ? box.blockSize : el.offsetHeight)
      if (h !== composerHeight.value) composerHeight.value = h
    })
    composerObserver.observe(el)
  },
  { flush: 'post' },
)

// Chat area opacity and transition control
const chatOpacity = ref(1)
const chatViewOpacity = ref(1)  // Separate opacity for entire chat-view (used in zen→live transition)
const isTransitioning = ref(false)
const showLoadingSpinner = ref(false)

// Authenticated fetch — hoisted once at setup scope for reuse across all async calls
const { fetchWithAuth } = useAuthenticatedFetch()

// Voice recording composables
const voiceRecording = useVoiceRecording()
const whisperTranscription = useWhisperTranscription()
const parakeetTranscription = useParakeetTranscription()

// STT engine state
const sttEngine = ref<'whisper' | 'parakeet'>('whisper')
const isDictating = ref(false)
const dictationPrefix = ref('') // Text that was in input before dictation started (or after the user's last edit)
let dictationConsumed = 0 // Chars of the transcript already folded into dictationPrefix by a user edit
let dictationEdited = false // The user edited the input during this dictation
let dictationStopping = false // stopDictation() is finalising — ignore the external-stop watcher

/** Input text for a transcript: the prefix plus speech not yet folded into it. */
function dictationText(transcript: string): string {
  const tail = transcript.slice(dictationConsumed).trim()
  if (!tail) return dictationPrefix.value
  return dictationPrefix.value ? `${dictationPrefix.value.replace(/\s+$/, '')} ${tail}` : tail
}

function resetDictationState() {
  dictationPrefix.value = ''
  dictationConsumed = 0
  dictationEdited = false
}

// Computed: model loading state (unified across engines)
const isAnyModelLoading = computed(() =>
  whisperTranscription.isModelLoading.value || parakeetTranscription.isModelLoading.value
)

const activeModelProgress = computed(() =>
  sttEngine.value === 'parakeet'
    ? parakeetTranscription.modelProgress.value
    : whisperTranscription.transcriptionProgress.value
)

// Fetch STT engine setting on mount
async function fetchSttEngine() {
  try {
    const response = await fetchWithAuth('/api/settings/stt_engine', { method: 'GET' })
    if (response.ok) {
      const setting = await response.json()
      if (setting.value === 'parakeet' || setting.value === 'whisper') {
        sttEngine.value = setting.value
      }
      // Warm Parakeet up in the background so the mic is ready when pressed
      if (sttEngine.value === 'parakeet') preloadParakeetModel()
    }
  } catch (err) {
    console.warn('Failed to fetch STT engine setting, using default:', err)
  }
}

// Local input message (synced with prop)
const localInputMessage = computed({
  get: () => props.inputMessage,
  set: (value) => {
    if (isDictating.value && !dictationStopping) {
      // The user edited while dictating: their text wins, and speech from
      // here on is appended after it instead of rebuilding the whole input
      dictationPrefix.value = value
      dictationConsumed = parakeetTranscription.streamingText.value.length
      dictationEdited = true
    }
    emit('update:input-message', value)
  }
})

// Computed refs for accessing child component elements
const messagesContainer = computed(() => chatMessagesRef.value?.messagesContainer || null)
const messageInput = computed(() => chatInputRef.value?.messageInput || null)

// Slash command autocomplete
const inputMessageRef = computed(() => props.inputMessage)

const handleSlashCommandSelect = (command: SlashCommand) => {
  emit('slash-command', command)
}

// Handle feature selection — fetch context file and send it as a priming message
async function handleFeatureSelect(featureName: string) {
  if (!props.projectId) {
    console.warn('[feature] No projectId available, cannot load feature context')
    return
  }
  try {
    const response = await fetchWithAuth(`/api/projects/${props.projectId}/features/${featureName}`)
    if (!response.ok) {
      console.warn(`[feature] Feature "${featureName}" not found`)
      return
    }
    const data = await response.json()
    const contextMessage =
      `[Feature Context Loaded: ${featureName}]\n\n` +
      `${data.content}\n\n` +
      `Acknowledge that you have loaded the **${featureName}** feature context and briefly summarize the key points.`
    emit('update:input-message', contextMessage)
    await nextTick()
    emit('send')
  } catch (e) {
    console.warn('[feature] Failed to fetch feature content:', e)
  }
}

// Initialize autocomplete composable
const {
  isVisible: showAutocomplete,
  query: autocompleteQuery,
  position: autocompletePosition,
  isFeatureMode: autocompleteFeatureMode,
  availableFeatures: autocompleteFeatureNames,
  handleInput: handleAutocompleteInput,
  handleKeydown: handleAutocompleteKeydown,
  handlePaste: handleAutocompletePaste,
  selectCommand: selectAutocompleteCommand,
  selectFeature: selectAutocompleteFeature,
  dismiss: dismissAutocomplete,
  handleClickOutside: handleAutocompleteClickOutside
} = useSlashCommandAutocomplete(messageInput, inputMessageRef, handleSlashCommandSelect, handleFeatureSelect)

// Cache the project ID for which we last fetched the feature list, so repeated
// /feature activations within the same project don't trigger redundant API calls
const featureListCacheId = ref<string | null>(null)

watch(autocompleteFeatureMode, async (isFeature) => {
  if (!isFeature || !props.projectId) return
  // Skip fetch if we already have the list for this project
  if (featureListCacheId.value === props.projectId && autocompleteFeatureNames.value.length > 0) return
  try {
    const response = await fetchWithAuth(`/api/projects/${props.projectId}/features`)
    if (response.ok) {
      const data = await response.json()
      autocompleteFeatureNames.value = (data.features as { name: string }[]).map(f => f.name)
      featureListCacheId.value = props.projectId
    }
  } catch (e) {
    console.warn('[feature] Failed to fetch feature list:', e)
  }
})

// Fetch skills and register them as slash commands
const skillsCacheId = ref<string | null>(null)

async function fetchAndRegisterSkills() {
  const projectId = props.projectId
  if (!projectId) return
  if (skillsCacheId.value === projectId) return
  try {
    const response = await fetchWithAuth('/api/skills/')
    if (response.ok) {
      const data = await response.json()
      const skillList = data.skills || data || []
      const skills: SlashCommand[] = skillList.map((s: any) => {
        const argHint = s.argument_hint || s.argumentHint || ''
        return {
          name: s.name,
          description: s.description || `Run /${s.name} skill`,
          category: 'skill' as const,
          icon: '⚡',
          skillScope: s.scope || 'project',
          argumentHint: argHint,
          ...(argHint ? {
            params: [{
              name: argHint.replace(/[\[\]<>]/g, ''),
              description: argHint,
              required: false,
              type: 'string' as const,
            }]
          } : {})
        }
      })
      registerSkillCommands(projectId, skills)
      skillsCacheId.value = projectId
    }
  } catch (e) {
    console.warn('[skills] Failed to fetch skills for slash commands:', e)
  }
}

// Fetch skills on mount and when project changes
watch(() => props.projectId, (newId, oldId) => {
  if (oldId) clearSkillCommands(oldId)
  skillsCacheId.value = null
  fetchAndRegisterSkills()
}, { immediate: true })

// Autocomplete handlers to pass to ChatInput
const autocompleteHandlers = {
  handleInput: handleAutocompleteInput,
  handlePaste: handleAutocompletePaste
}

// Changes when the agent says or does something; lights the composer's working edge now and then.
const activityKey = computed(() => {
  const list = props.messages
  const last = list?.[list.length - 1]
  const size = typeof last?.content === 'string' ? last.content.length : (last?.content?.length ?? 0)
  return `${list?.length ?? 0}|${last?.id ?? ''}|${size}`
})

// Handle messages scroll
function onMessagesScroll() {
  if (props.onScroll) {
    props.onScroll(messagesContainer.value)
  }
}

// Handle scroll to bottom button click
function handleScrollToBottomClick() {
  if (props.onScrollToBottom) {
    props.onScrollToBottom(messagesContainer.value, true) // smooth scroll
  }
}

// Handle send
async function handleSend() {
  if (isDictating.value) {
    await stopDictation()
  }
  // Trigger zen mode animations and auto-hide input
  if (displayMode.value === 'zen' && zenModeRef.value) {
    const msg = props.inputMessage.trim()
    // Detect /context command — trigger avatar animation instead of send pulse
    if (msg.toLowerCase() === '/context') {
      zenModeRef.value.triggerContextAnimation()
    } else {
      zenModeRef.value.triggerSendPulse()
    }
    // Play send chime
    zenModeRef.value.playSendChime?.()
    showZenInput.value = false
    // Smoothly dismiss the messages overlay so the plane is clean
    zenModeRef.value.dismissMessages()
  }
  emit('send')
}

// Handle zen fullscreen toggle
function handleZenFullscreen(value: boolean) {
  zenFullscreen.value = value
  if (value) {
    // In fullscreen, hide the input to give zen mode the full screen
    showZenInput.value = false
  }
  emit('update:zen-fullscreen', value)
}

// Handle images attached
function handleImagesAttached(images: AttachedImage[]) {
  emit('images-attached', images)
}

// Expose attachedImages for parent access
const attachedImages = computed(() => chatInputRef.value?.attachedImages || [])

// Clear attachments (called from parent)
function clearAttachments() {
  chatInputRef.value?.clearAttachments()
}

// Restore attachments (called from parent on send failure)
function restoreAttachments(images: AttachedImage[]) {
  chatInputRef.value?.restoreAttachments(images)
}

// Fullscreen editor functions
function openFullscreenEditor() {
  fullscreenText.value = props.inputMessage
  showFullscreenEditor.value = true
}

function closeFullscreenEditor() {
  showFullscreenEditor.value = false
}

function saveFullscreenText() {
  emit('update:input-message', fullscreenText.value)
  showFullscreenEditor.value = false
  // Focus back to main textarea
  nextTick(() => {
    messageInput.value?.focus()
  })
}

// Voice recording functions — dual engine support
async function startVoiceRecording() {
  if (sttEngine.value === 'parakeet') {
    await startParakeetDictation()
  } else {
    await startWhisperRecording()
  }
}

// === Whisper (batch) flow ===
async function startWhisperRecording() {
  try {
    showRecordingModal.value = true
    await whisperTranscription.initializeModel()
    await voiceRecording.startRecording()
  } catch (error) {
    console.error('Failed to initialize Whisper recording:', error)
    // Keep the dialog open with the reason (it has a Cancel button), instead of closing silently
    if (!voiceRecording.error.value && !whisperTranscription.error.value) {
      voiceRecording.error.value = describeMicError(error)
    }
  }
}

function stopVoiceRecording() {
  voiceRecording.stopRecording()
}

function cancelVoiceRecording() {
  if (isDictating.value) {
    // Cancel Parakeet dictation
    parakeetTranscription.cancelStreaming()
    isDictating.value = false
    // Drop the speech since the last edit; keep what the user typed
    emit('update:input-message', dictationPrefix.value)
    resetDictationState()
    showRecordingModal.value = false
    return
  }

  voiceRecording.cancelRecording()
  showRecordingModal.value = false
}

async function finishRecording() {
  try {
    // Wait for the recorder's final chunk (a fixed delay is too short in Safari)
    const audioBlob = await voiceRecording.stopAndGetBlob()
    if (!audioBlob) {
      console.error('No audio recorded')
      voiceRecording.error.value = 'No audio recorded'
      return
    }

    const text = await whisperTranscription.transcribe(audioBlob)

    if (text) {
      const currentText = props.inputMessage
      const newText = currentText ? `${currentText} ${text}` : text
      emit('update:input-message', newText)

      await nextTick()
      if (messageInput.value) {
        messageInput.value.focus()
        const length = newText.length
        messageInput.value.setSelectionRange(length, length)
      }
    } else {
      console.warn('Transcription returned empty text')
      whisperTranscription.error.value = 'No speech detected in audio'
    }

    voiceRecording.reset()
    showRecordingModal.value = false
  } catch (error) {
    console.error('Failed to transcribe audio:', error)
    whisperTranscription.error.value = error instanceof Error ? error.message : 'Transcription failed'
  }
}

// === Parakeet (streaming dictation) flow ===
async function startParakeetDictation() {
  try {
    // If model not loaded yet, show modal for download progress
    if (!parakeetTranscription.isModelReady.value) {
      showRecordingModal.value = true
      await parakeetTranscription.initializeModel()
      showRecordingModal.value = false
    }

    // Save current input text as prefix (dictation appends after it)
    resetDictationState()
    dictationPrefix.value = props.inputMessage
    isDictating.value = true

    // Start streaming transcription
    await parakeetTranscription.startStreaming()
  } catch (error) {
    console.error('Failed to start Parakeet dictation:', error)
    isDictating.value = false
    // Show why in the dialog (it has a Cancel button) instead of failing silently
    if (!parakeetTranscription.error.value) parakeetTranscription.error.value = describeMicError(error)
    showRecordingModal.value = true
  }
}

async function stopDictation() {
  if (!isDictating.value || dictationStopping) return
  dictationStopping = true

  try {
    const finalText = await parakeetTranscription.stopStreaming()

    // Apply the final (more accurate) transcript — unless the user edited the
    // text while dictating: then what they see is what gets kept and sent.
    if (finalText && !dictationEdited) {
      emit('update:input-message', dictationText(finalText))
    }

    // Focus textarea for editing
    await nextTick()
    if (messageInput.value) {
      messageInput.value.focus()
      const length = (messageInput.value as HTMLTextAreaElement).value.length
      ;(messageInput.value as HTMLTextAreaElement).setSelectionRange(length, length)
    }
  } catch (error) {
    console.error('Failed to stop dictation:', error)
  } finally {
    isDictating.value = false
    dictationStopping = false
    resetDictationState()
  }
}

// Watch Parakeet streaming text to update input in real-time
watch(
  () => parakeetTranscription.streamingText.value,
  (streamingText) => {
    if (!isDictating.value || dictationStopping) return

    // Build the live text: prefix + speech not yet folded into it
    emit('update:input-message', dictationText(streamingText))
  }
)

// Sync isDictating when recording stops externally (e.g. auto-stop on visibility change or max duration)
watch(
  () => parakeetTranscription.isRecording.value,
  (recording) => {
    if (!recording && isDictating.value && !dictationStopping) {
      console.log('[ChatArea] Parakeet recording stopped externally, ending dictation')
      isDictating.value = false
      resetDictationState()
    }
  }
)

// Zen: voice input needs somewhere to land — open the input when recording or
// dictation starts, so the user sees they're live and watches the text arrive.
// Focus it (unless something else has focus) so Enter sends and edits work.
const zenVoiceLive = computed(() =>
  isDictating.value || voiceRecording.isRecording.value || whisperTranscription.isTranscribing.value
)
watch(
  () => isDictating.value || voiceRecording.isRecording.value,
  (live) => {
    if (!live || displayMode.value !== 'zen' || activeTab.value !== 'chat') return
    showZenInput.value = true
    const active = document.activeElement
    if (!active || active === document.body) {
      nextTick(() => chatInputRef.value?.messageInput?.focus())
    }
  }
)

// Typing behaviours that are not shortcuts: in zen, typing anywhere summons
// the input, and Enter sends what was dictated. (Shortcuts live in the
// registry below; Escape is the registry's Escape stack.)
function handleKeydown(event: KeyboardEvent) {
  if (event.defaultPrevented || event.isComposing) return
  // Zen, dictating, focus outside the input: Enter sends what was said
  if (
    event.key === 'Enter' && !event.shiftKey && !event.metaKey && !event.ctrlKey && !event.altKey &&
    isDictating.value && displayMode.value === 'zen' &&
    !isEditableTarget(event.target) &&
    !(event.target as HTMLElement | null)?.closest?.('button, a, [role="button"], summary')
  ) {
    event.preventDefault()
    handleSend()
    return
  }

  maybeSummonZenInput(event)
}

// ── Keyboard shortcuts (registry: composables/useKeyboardShortcuts.ts) ──
const sessionReady = () => props.hasActiveSession && !showRecordingModal.value

function toggleTab(tab: 'terminal' | 'files') {
  activeTab.value = activeTab.value === tab ? 'chat' : tab
}
useShortcut('tab.terminal', () => toggleTab('terminal'), { when: sessionReady })
useShortcut('tab.files', () => toggleTab('files'), { when: sessionReady })

// ⌘P: Files tab + focus its search (the explorer is an async component, so
// focus it once it has mounted)
const fileExplorerRef = ref<{ focusSearch: () => void } | null>(null)
let pendingFileSearch = false
useShortcut('file.goto', async () => {
  activeTab.value = 'files'
  pendingFileSearch = true
  await nextTick()
  if (fileExplorerRef.value) { pendingFileSearch = false; fileExplorerRef.value.focusSearch() }
}, { when: sessionReady })
watch(fileExplorerRef, (explorer) => {
  if (explorer && pendingFileSearch) { pendingFileSearch = false; explorer.focusSearch() }
})

// ⇧⌘Enter: Zen ↔ Live
useShortcut('view.zen', () => {
  activeTab.value = 'chat'
  emit('update:view-mode', viewMode.value === 'zen' ? 'live' : 'zen')
}, { when: () => props.hasActiveSession })

// ⌃⌘F in Zen: Zen full screen (outside Zen the layout's handler uses the
// browser's full screen)
function setZenFullscreen(value: boolean) {
  if (!zenModeRef.value) return
  zenModeRef.value.isFullscreen = value
  handleZenFullscreen(value)
}
useShortcut('view.fullscreen', () => setZenFullscreen(!zenModeRef.value?.isFullscreen), {
  scope: 'zen',
  when: () => props.hasActiveSession && displayMode.value === 'zen' && activeTab.value === 'chat' && !!zenModeRef.value
})

// ⌘D: dictate / voice recording toggle
// (hasActiveSession, not just connected, so it works during brief reconnects)
// The native menu item repeats while ⌘D is held and sends no "repeat" flag, which
// would start and immediately stop a recording. Menu events closer than 600 ms to the
// previous menu event are ignored (sliding window, so a whole hold is absorbed; the
// first repeat comes after the key-repeat delay). Applies to the start/stop handler
// and to the modal finish handler alike.
let lastMenuDictate = 0
function isMenuDictateRepeat(ctx: { source?: string }): boolean {
  if (ctx.source !== 'menu') return false
  const now = Date.now()
  const repeat = now - lastMenuDictate < 600
  lastMenuDictate = now
  return repeat
}
useShortcut('voice.dictate', (ctx) => {
  if (isMenuDictateRepeat(ctx)) return
  if (isDictating.value) stopDictation()
  else if (showRecordingModal.value && voiceRecording.isRecording.value) finishRecording()
  else if (!showRecordingModal.value) startVoiceRecording()
}, { when: () => props.connected || props.hasActiveSession })

// While the recording dialog (a modal) is open, ⌘D and Space finish it
useShortcut('voice.dictate', (ctx) => {
  if (isMenuDictateRepeat(ctx)) return
  finishRecording()
}, {
  scope: 'modal',
  when: () => showRecordingModal.value && voiceRecording.isRecording.value
})
useShortcut('voice.finish', () => finishRecording(), {
  scope: 'modal',
  when: () => showRecordingModal.value && voiceRecording.isRecording.value
})

// ⌘.: stop the agent
useShortcut('agent.stop', () => emit('interrupt'), {
  when: () => props.hasActiveSession && props.connected && !showRecordingModal.value && !isDictating.value
})

// Escape: dictation and the recording dialog cancel; Zen full screen exits; with
// nothing open, it stops a turn that is running (like ⌘.).
// "Working" also counts the session list's status: this component's own flags lag after a
// reload or when the turn was started elsewhere, while the sidebar already says "processing".
const agentBusy = computed(() => !!(props.isProcessing || props.isThinking || props.sessionStatus === 'processing'))
useEscapeAction(() => emit('interrupt'), () =>
  props.hasActiveSession && props.connected && agentBusy.value && !showRecordingModal.value && !isDictating.value)
useClosable(isDictating, () => cancelVoiceRecording())
useClosable(showRecordingModal, () => cancelVoiceRecording(), { modal: true })
useClosable(zenFullscreen, () => setZenFullscreen(false))
useClosable(showFullscreenEditor, () => closeFullscreenEditor(), { modal: true })

// Live ↔ Zen: fade the current view out, swap, then fade the new one in.
// Zen fades itself in (CSS + canvas fade + camera fly-in); the chat view is
// hidden, scrolled to the bottom and then revealed so it never shows at the
// wrong scroll position.
watch(viewMode, async (newMode) => {
  const token = ++viewSwitchToken
  if (newMode === displayMode.value) {
    // Toggled back before the fade finished — undo the fade-out
    zenLeaving.value = false
    chatLeaving.value = false
    chatViewOpacity.value = 1
    return
  }

  const reduceMotion = typeof window !== 'undefined' &&
    window.matchMedia?.('(prefers-reduced-motion: reduce)').matches
  const fadeMs = reduceMotion ? 0 : VIEW_FADE_OUT_MS

  if (newMode === 'zen') {
    if (activeTab.value === 'chat' && fadeMs) {
      chatLeaving.value = true
      chatViewOpacity.value = 0
      await new Promise(resolve => setTimeout(resolve, fadeMs))
      if (token !== viewSwitchToken) return
    }
    chatLeaving.value = false
    displayMode.value = 'zen'
    return
  }

  // zen → live
  if (activeTab.value === 'chat' && fadeMs) {
    zenLeaving.value = true
    await new Promise(resolve => setTimeout(resolve, fadeMs))
    if (token !== viewSwitchToken) return
  }
  zenLeaving.value = false

  chatViewOpacity.value = 0
  isTransitioning.value = false
  displayMode.value = 'live'

  // Wait for the chat-view DOM to mount, then keep it pinned to the latest
  // message (instantly, while still invisible) until the layout settles —
  // markdown, code blocks and images keep growing the list for a moment.
  await nextTick()
  await nextTick()
  if (token !== viewSwitchToken) return
  await settleAtBottom(messagesContainer.value, () => token !== viewSwitchToken)
  if (token !== viewSwitchToken) return

  isTransitioning.value = true
  chatViewOpacity.value = 1
})

/** Pin `el` to the bottom until its height stops changing (max ~900 ms). */
async function settleAtBottom(el: HTMLElement | null, cancelled: () => boolean) {
  if (!el) return
  const pin = () => { el.scrollTo({ top: el.scrollHeight, behavior: 'instant' }) }
  pin()
  let lastHeight = el.scrollHeight
  let stableFrames = 0
  const start = performance.now()
  while (performance.now() - start < 900) {
    await new Promise(resolve => requestAnimationFrame(() => resolve(null)))
    if (cancelled()) return
    if (el.scrollHeight !== lastHeight) {
      lastHeight = el.scrollHeight
      stableFrames = 0
      pin()
    } else if (++stableFrames >= 6) {
      break
    }
  }
  pin()
}

// Focus the terminal once its async component has loaded and mounted
watch(sessionTerminalRef, (terminal, previous) => {
  if (terminal && !previous && activeTab.value === 'terminal') {
    terminal.focusTerminal()
  }
})

// Watch for tab changes to focus the appropriate view
watch(() => activeTab.value, async (newValue, oldValue) => {
  if (newValue === 'terminal') {
    // Terminal tab was activated
    await nextTick()
    // The terminal is an async component: on its first open it may not be
    // loaded yet, in which case the ref watcher below focuses it on arrival.
    sessionTerminalRef.value?.focusTerminal()
  } else if (newValue === 'chat' && (oldValue === 'terminal' || oldValue === 'files')) {
    // Chat tab was activated (switched back from terminal or files)
    await nextTick()
    if (props.onScrollToBottom) {
      props.onScrollToBottom(messagesContainer.value, false)
    }
  }
})

// Add keyboard event listener and fetch settings
onMounted(() => {
  // Fetch three.js in the background so the first switch to Zen doesn't wait on it
  const idle = (window as any).requestIdleCallback || ((cb: () => void) => setTimeout(cb, 1500))
  idle(() => preloadZenScene())
  window.addEventListener('keydown', handleKeydown)
  document.addEventListener('click', handleAutocompleteClickOutside)
  fetchSttEngine()
})

onUnmounted(() => {
  stopComposerObserver()
  window.removeEventListener('keydown', handleKeydown)
  document.removeEventListener('click', handleAutocompleteClickOutside)
  if (props.projectId) clearSkillCommands(props.projectId)
})

/**
 * Chat area transition methods
 */
function hideChatAreaInstant() {
  isTransitioning.value = false
  chatOpacity.value = 0
}

function showChatAreaSmooth() {
  isTransitioning.value = true
  nextTick(() => {
    chatOpacity.value = 1
  })
}

function resetChatArea() {
  isTransitioning.value = false
  chatOpacity.value = 1
}

function showSpinner() {
  showLoadingSpinner.value = true
}

function hideSpinner() {
  showLoadingSpinner.value = false
}

defineExpose({
  focusComposer,
  messagesContainer,
  messageInput,
  attachedImages,
  clearAttachments,
  restoreAttachments,
  hideChatAreaInstant,
  showChatAreaSmooth,
  resetChatArea,
  showSpinner,
  hideSpinner
})
</script>

<style scoped>
.chat-main-area {
  flex: 1;
  display: flex;
  flex-direction: column;
  background: var(--bg-primary);
  overflow: hidden;
  min-height: 0;
}

.no-session-selected {
  flex: 1;
  display: flex;
  align-items: center;
  justify-content: center;
}

.no-session-selected.has-nudge {
  align-items: flex-start;
  overflow-y: auto;
  padding: 16px;
}

.empty-state {
  text-align: center;
  color: var(--text-secondary);
}

.empty-state svg {
  margin-bottom: 16px;
}

.empty-state p {
  font-size: 0.95rem;
}

.chat-content {
  flex: 1;
  display: flex;
  flex-direction: column;
  overflow: hidden;
  min-height: 0;
  position: relative;
}

.chat-view {
  flex: 1;
  display: flex;
  flex-direction: column;
  overflow: hidden;
  min-height: 0;
  /* The subagents PiP floats in its top-right corner. */
  position: relative;
}

.chat-view.chat-view-transitioning {
  transition: opacity 0.45s ease-out;
  animation: chat-view-settle 0.45s ease-out;
}

/* Coming back from zen: a soft rise into place, already at the latest message */
@keyframes chat-view-settle {
  from { translate: 0 10px; }
  to { translate: 0 0; }
}

@media (prefers-reduced-motion: reduce) {
  .chat-view.chat-view-transitioning { animation: none; }
}

.chat-view.chat-view-leaving {
  transition: opacity 0.2s ease-in, scale 0.2s ease-in, filter 0.2s ease-in;
  scale: 0.99;
  filter: blur(2px);
}

/* Chat input wrapper — sits below both chat-view and zen-mode */
.chat-input-wrapper {
  flex-shrink: 0;
}

/* Zen: the composer floats on the scene (windowed and fullscreen) as a dark
   glass bar; ChatInput's "zen" variant draws the bar itself. */
.chat-input-wrapper.zen-input {
  position: absolute;
  left: 50%;
  bottom: 14px;
  transform: translateX(-50%);
  z-index: 4;
  width: min(760px, calc(100% - 28px));
  display: flex;
  flex-direction: column;
  align-items: stretch;
  /* The wrapper box is wider than its visible parts; let clicks reach the
     zen controls underneath. */
  pointer-events: none;
}

.chat-input-wrapper.zen-input > * {
  pointer-events: auto;
}

.chat-input-wrapper.zen-input.zen-input-fullscreen {
  position: fixed;
  bottom: 22px;
  width: min(820px, calc(100vw - 32px));
  z-index: 10000;
}

.chat-input-wrapper.zen-input.zen-input-collapsed {
  width: auto;
}

/* Collapsed: only the pill handle remains (the input is re-rendered and
   focused when revealed). */
.chat-input-wrapper.zen-input.zen-input-collapsed .zen-input-collapsible {
  display: none;
}

.zen-input-row {
  align-self: center;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 8px;
  margin-bottom: 8px;
}

.zen-input-row .zen-input-toggle {
  margin-bottom: 0;
}

.zen-input-toggle {
  align-self: center;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 6px;
  margin-bottom: 8px;
  padding: 5px 14px;
  border-radius: 999px;
  font-size: 0.68rem;
  font-weight: 500;
  letter-spacing: 0.3px;
  color: rgba(255, 255, 255, 0.6);
  background: rgba(10, 8, 22, 0.45);
  border: 1px solid rgba(255, 255, 255, 0.1);
  backdrop-filter: blur(12px);
  -webkit-backdrop-filter: blur(12px);
  cursor: pointer;
  transition: color 0.2s, border-color 0.2s, background 0.2s;
}

.zen-input-toggle:hover,
.zen-input-toggle:focus-visible {
  color: #fff;
  border-color: rgba(255, 255, 255, 0.25);
  background: rgba(10, 8, 22, 0.6);
  outline: none;
}

.zen-input-collapsed .zen-input-toggle {
  margin-bottom: 0;
}

.zen-input-collapsible {
  transition: opacity 0.25s ease;
}

/* Circus: the handle is a paper tab */
.zen-input-paper .zen-input-toggle {
  color: #2b1d10;
  background: #f4e9d0;
  border: 1px solid #9a7b48;
  border-radius: 2px;
  font-family: 'Courier Prime', ui-monospace, monospace;
  letter-spacing: 0.08em;
  text-transform: uppercase;
  backdrop-filter: none;
  -webkit-backdrop-filter: none;
  box-shadow: 0 4px 10px rgba(10, 8, 20, 0.25);
}
.zen-input-paper .zen-input-toggle:hover,
.zen-input-paper .zen-input-toggle:focus-visible {
  color: #2b1d10;
  background: #f8efdb;
  border-color: #6b5638;
}

/* ===== Zen: blocking prompts (permission requests) =====
   Float centered over the scene, clear of the composer. The zen permission
   card itself carries the glass look. (.chat-content is position: relative.) */
.zen-attention {
  position: absolute;
  left: 50%;
  top: 44%;
  transform: translate(-50%, -50%);
  z-index: 5;
  width: min(600px, calc(100% - 32px));
  max-height: min(64%, 520px);
  overflow-y: auto;
  scrollbar-width: thin;
}

.zen-attention.zen-attention-fullscreen {
  position: fixed;
  z-index: 10001;
  top: 42%;
  max-height: 64vh;
}

.zen-attention :deep(.permission-requests) {
  display: flex;
  flex-direction: column;
  gap: 10px;
  padding: 0;
  margin: 0;
  background: transparent;
  border: none;
}

.zen-attention-fade-enter-active,
.zen-attention-fade-leave-active {
  transition: opacity 0.3s ease, transform 0.3s ease;
}

.zen-attention-fade-enter-from,
.zen-attention-fade-leave-to {
  opacity: 0;
  transform: translate(-50%, calc(-50% + 12px));
}

/* Keep zen's bottom metrics and controls clear of the open composer */
.chat-content.zen-composer-open :deep(.zen-overlay) {
  padding-bottom: 128px;
}

.chat-content.zen-composer-open :deep(.zen-bottom-right-controls) {
  bottom: 128px;
}

.chat-content.zen-composer-open :deep(.zen-mode.zen-fullscreen .zen-bottom-right-controls) {
  bottom: 112px;
}

/* Wide Zen (response side panel): the centred composer ends well left of the
   controls, so they stay at the bottom edge, under the panel. */
.chat-content.zen-composer-open :deep(.zen-mode.zen-side-layout:not(.zen-fullscreen) .zen-bottom-right-controls) {
  bottom: 12px;
}

.terminal-view {
  flex: 1;
  display: flex;
  flex-direction: column;
  overflow: hidden;
  min-height: 0;
  padding: 12px;
}

.files-view {
  flex: 1;
  display: flex;
  flex-direction: column;
  overflow: hidden;
  min-height: 0;
}

.images-view {
  flex: 1;
  display: flex;
  flex-direction: column;
  overflow: hidden;
  min-height: 0;
}

.media-view {
  flex: 1;
  display: flex;
  flex-direction: column;
  overflow: hidden;
  min-height: 0;
}
</style>
