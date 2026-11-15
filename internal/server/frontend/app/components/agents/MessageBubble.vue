<template>
  <div
    class="message"
    :class="messageClasses"
    :data-message-id="message.id"
    @click="handleMessageClick"
    role="button"
    tabindex="0"
    @keydown.enter="handleMessageClick"
    @keydown.space.prevent="handleMessageClick"
    :aria-label="`${roleName} message`"
  >
    <!-- Message Header: Role name, time, TTS, click hint -->
    <MessageHeader :message="message" :formatted-time="formattedTime" :message-text="textContent" />

    <!-- Plan Message (special formatting) -->
    <PlanMessage
      v-if="isPlan"
      :content="planContent || textContent"
      :format-message="formatMessage"
    />

    <!-- Text Content (regular messages) -->
    <MessageContent
      v-else-if="hasText"
      :content="textContent"
      :format-message="formatMessage"
      :message-role="message.role"
    />

    <ModelSwitchContextButton
      v-if="message.role === 'system' && message.sessionId && textContent.startsWith('🔀 Switched model: ')"
      :session-id="message.sessionId"
      :message-id="message.id"
    />

    <!-- Thinking Section (collapsible) -->
    <ThinkingSection
      v-if="hasThinking"
      :thinking="message.thinking || ''"
      :tool-uses="displayToolUses"
      :format-message="formatMessage"
      @tool-click="handleToolClick"
    />

    <!-- Images -->
    <MessageImages v-if="hasImages" :image-blocks="imageBlocks" @open-lightbox="$emit('open-lightbox', $event)" />

    <!-- Tool Use Indicators -->
    <ToolUseIndicators
      v-if="hasToolUses"
      :tool-uses="displayToolUses"
      :message-timestamp="props.message.timestamp"
      :media="media"
      @tool-click="handleToolClick"
      @open-lightbox="$emit('open-lightbox', $event)"
    />

    <!-- Expandable Edit Diff (when diffDisplayLocation is 'chat') -->
    <!-- Only prevent click bubbling for non-historical messages with inline diff -->
    <div :class="{ 'no-click-bubble': !message.isHistorical }" @click="handleDiffClick">
      <slot name="edit-diff">
        <EditDiffMessage
          v-if="showEditDiff && editData"
          :file-path="editData.filePath"
          :old-string="editData.oldString"
          :new-string="editData.newString"
          :replace-all="editData.replaceAll"
          :status="editData.status"
        />
      </slot>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import type { Message, DisplayToolUse, ImageBlock } from '@/types/message'
import type { SessionMediaLookup } from '@/utils/agents/sessionMedia'
import {
  extractTextContent,
  extractImageBlocks,
  extractPlanContent,
  extractDisplayToolUses,
  formatRoleName,
  getMessageClasses
} from '@/types/message'
import { useMessageClassification } from '@/composables/useMessageClassification'

// Sub-components
import MessageHeader from './MessageBubble/MessageHeader.vue'
import ModelSwitchContextButton from './ModelSwitchContextButton.vue'
import MessageContent from './MessageBubble/MessageContent.vue'
import ThinkingSection from './MessageBubble/ThinkingSection.vue'
import ToolUseIndicators from './MessageBubble/ToolUseIndicators.vue'
import MessageImages from './MessageBubble/MessageImages.vue'
import PlanMessage from './PlanMessage.vue'
import EditDiffMessage from './EditDiffMessage.vue'

interface Props {
  message: Message
  formatTime: (date: Date) => string
  formatMessage: (content: string) => string
  /** Render the message's Edit diff inline (diffDisplayLocation === 'chat') */
  showEditDiff?: boolean
  /** Video/audio players to show under the tool calls that named the files */
  media?: SessionMediaLookup | null
}

const props = defineProps<Props>()

// Inline Edit diff, rendered here rather than through a scoped slot from the page:
// a slot that uses the v-for's `message` is "dynamic" to Vue, which then re-renders
// every bubble whenever the list re-renders.
const editData = computed(() => (props.message as any).editToolData as
  { filePath: string; oldString: string; newString: string; replaceAll?: boolean; status: 'running' | 'completed' | 'error' } | undefined)

const emit = defineEmits<{
  'open-lightbox': [{ images: ImageBlock[]; startIndex: number }]
  'tool-click': [{ tool: any }]
  'message-click': [{ message: Message }]
}>()

// Use classification composable for computed properties
const classification = useMessageClassification(() => props.message)

// Computed properties for content extraction
const textContent = computed(() => extractTextContent(props.message.content))
const planContent = computed(() => extractPlanContent(props.message.toolUses))
const allImageBlocks = computed(() => extractImageBlocks(props.message.content))
const displayToolUses = computed(() => extractDisplayToolUses(props.message))

// Filter out images that are already shown as inline tool previews (to avoid duplicates)
const imageBlocks = computed(() => {
  const toolPreviewUrls = new Set(
    displayToolUses.value
      .filter(t => t.imagePreview)
      .map(t => t.imagePreview!.dataUrl)
  )
  return allImageBlocks.value.filter(img => !toolPreviewUrls.has(img.dataUrl))
})

// Formatting properties
// CRITICAL: Pass username for multi-user scenarios to show correct user name instead of "You"
const roleName = computed(() => formatRoleName(props.message.role, props.message.username as string | null))
const formattedTime = computed(() => props.formatTime(props.message.timestamp))

// Classification properties (from composable)
const hasText = computed(() => !!(textContent.value && textContent.value.trim()))
const hasThinking = computed(() => !!(props.message.thinking && props.message.thinking.trim()))
const hasImages = computed(() => imageBlocks.value.length > 0)
const hasToolUses = computed(() => displayToolUses.value.length > 0)
const isPlan = computed(() => classification.isPlan.value)

// CSS classes
const messageClasses = computed(() => getMessageClasses(props.message))

// Event handlers
const handleMessageClick = (event: Event) => {
  // Handle keyboard activation
  if (event instanceof KeyboardEvent && event.key !== 'Enter' && event.key !== ' ') {
    return
  }
  if (event instanceof KeyboardEvent) {
    event.preventDefault()
  }
  emit('message-click', { message: props.message })
}

const handleToolClick = (data: { tool: any; event: Event }) => {
  // Tool click is already handled by ToolUseIndicators component
  // It emits with the full tool data
  if (data.tool) {
    emit('tool-click', { tool: data.tool })
  }
}

const handleDiffClick = (event: Event) => {
  // For historical messages, allow click to bubble up to open modal
  // For real-time messages with inline diff, stop propagation
  if (!props.message.isHistorical) {
    event.stopPropagation()
  }
}
</script>

<style scoped>
.message {
  margin-bottom: 24px;
  cursor: pointer;
  transition: background-color 0.2s, transform 0.1s;
  padding: 8px;
  margin-left: -8px;
  margin-right: -8px;
  border-radius: 12px;
}

.message:hover {
  background-color: rgba(var(--accent-purple-rgb, 139, 92, 246), 0.05);
}

.message:active {
  transform: scale(0.995);
}

.message:focus {
  outline: 2px solid var(--accent-purple);
  outline-offset: 2px;
}

/* Role-specific styling */
.message.user {
  /* User messages styling */
}

.message.assistant {
  /* Assistant messages styling */
}

.message.system {
  /* System messages styling */
}

.message.error,
.message.isError {
  /* Error messages styling */
}

.message.isToolResult {
  /* Tool result styling */
}

.message.isExecutionStatus {
  /* Execution status styling */
}

.message.isPermissionDecision {
  /* Permission decision styling */
}

.message.isHistorical {
  opacity: 0.8;
}

.no-click-bubble {
  /* Prevents click bubbling for edit diffs */
}

/* Responsive design */
@media (max-width: 768px) {
  .message {
    margin-left: -4px;
    margin-right: -4px;
    padding: 4px;
  }
}
</style>
