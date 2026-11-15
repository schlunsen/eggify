<template>
  <div v-if="hasMore" class="load-older-messages-wrapper">
    <button class="load-older-messages-btn" @click="$emit('load-older')">
      Load older messages
    </button>
  </div>
  <MessageBubble
    v-for="message in messages"
    :key="message.id"
    :message="message"
    :format-time="formatTime"
    :format-message="formatMessage"
    :show-edit-diff="diffInChat"
    :media="media"
    @open-lightbox="$emit('open-lightbox', $event)"
    @message-click="$emit('message-click', $event)"
    @tool-click="$emit('tool-click', $event)"
  />
</template>

<script setup lang="ts">
import MessageBubble from '~/components/agents/MessageBubble.vue'
import type { SessionMediaLookup } from '~/utils/agents/sessionMedia'

/**
 * The chat's message list, as its own component.
 *
 * It used to be rendered straight into ChatArea's messages slot from the agents
 * page, so every re-render of ChatArea (each keystroke in the composer, every
 * context/subagent/status update) re-ran the slot and re-rendered every
 * MessageBubble: about 700 component updates per keystroke on a long session.
 * As a component with its own props it only updates when the messages, the
 * filter or the diff setting change, and a bubble only when its message does
 * (no scoped slot per bubble, which Vue always treats as changed).
 */
defineProps<{
  messages: any[]
  formatTime: (date: Date) => string
  formatMessage: (content: any) => string
  /** Show Edit diffs inline (diffDisplayLocation === 'chat') */
  diffInChat?: boolean
  /** More, older messages can be loaded */
  hasMore?: boolean
  /**
   * The session's video/audio files by the tool call that named them. One
   * object for every bubble, replaced only when the list changes, so bubbles
   * don't re-render on unrelated updates.
   */
  media?: SessionMediaLookup | null
}>()

defineEmits<{
  'load-older': []
  'open-lightbox': [data: any]
  'message-click': [data: any]
  'tool-click': [data: any]
}>()
</script>

<style scoped>
.load-older-messages-wrapper {
  display: flex;
  justify-content: center;
  padding: 8px 0;
}

.load-older-messages-btn {
  font-size: 0.75rem;
  color: var(--text-secondary);
  background: var(--bg-secondary);
  border: 1px solid var(--border-color);
  padding: 4px 12px;
  border-radius: 6px;
  cursor: pointer;
  transition: all 0.2s ease;
}

.load-older-messages-btn:hover {
  color: var(--text-primary);
  border-color: var(--accent-purple);
  background: var(--card-bg);
}
</style>
