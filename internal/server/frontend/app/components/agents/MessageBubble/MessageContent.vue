<template>
  <CopyableMessageContent
    v-if="content"
    class="message-content"
    :class="[messageRole, { 'is-user': isUser, 'is-assistant': isAssistant }]"
    :html="formattedContent"
  />
</template>

<script setup lang="ts">
import { computed } from 'vue'
import CopyableMessageContent from '../CopyableMessageContent.vue'
import type { ContentBlock } from '@/types/message'

interface Props {
  content: string
  formatMessage: (content: string) => string
  messageRole: string
}

const props = defineProps<Props>()

const isUser = computed(() => props.messageRole === 'user')
const isAssistant = computed(() => props.messageRole === 'assistant')

const formattedContent = computed(() => {
  return props.formatMessage(props.content)
})
</script>

<style scoped>
.message-content {
  background: var(--card-bg);
  padding: 12px 16px;
  border-radius: 12px;
  border: 1px solid var(--border-color);
  font-family: 'Inter', -apple-system, BlinkMacSystemFont, 'Segoe UI', sans-serif;
  font-size: 1rem;
  line-height: 1.75;
  color: var(--text-primary);
  overflow-wrap: break-word;
  word-wrap: break-word;
  max-width: 100%;
}

.message-content.is-user {
  background: color-mix(in srgb, var(--accent-purple) 12%, var(--bg-primary));
  color: var(--text-primary);
  border-color: color-mix(in srgb, var(--accent-purple) 30%, var(--border-color));
  margin-left: 48px;
}

.message-content.is-assistant {
  background: transparent;
  border-color: transparent;
  margin-right: 48px;
}

.message-content :deep(code) {
  background: var(--bg-secondary);
  padding: 2px 6px;
  border-radius: 4px;
  font-family: 'Monaco', 'Menlo', monospace;
  font-size: 0.9em;
}

.message-content.is-user :deep(code) {
  background: var(--overlay-bg-active);
}

.message-content :deep(pre) {
  background: var(--bg-secondary);
  padding: 12px;
  border-radius: 8px;
  overflow-x: auto;
  margin: 8px 0;
  max-width: 100%;
  white-space: pre-wrap;
  word-wrap: break-word;
}

.message-content :deep(.chat-image-link) {
  display: inline-block;
  max-width: 100%;
  margin: 6px 0;
  line-height: 0;
}

.message-content :deep(.chat-image) {
  display: block;
  max-width: min(100%, 560px);
  max-height: 480px;
  object-fit: contain;
  border-radius: 8px;
  border: 1px solid var(--border-color);
  background: var(--bg-secondary);
}

.message-content :deep(.md-image-unavailable),
.message-content :deep(.md-remote-image) {
  color: var(--text-secondary);
}

.message-content :deep(.system-message) {
  color: var(--text-secondary);
  font-style: italic;
  opacity: 0.7;
}

.message-content :deep(.message-link) {
  color: var(--accent-purple);
  text-decoration: underline;
  transition: all 0.2s;
  word-break: break-all;
}

.message-content :deep(.message-link:hover) {
  color: var(--accent-purple-hover);
  text-decoration: none;
  opacity: 0.8;
}

.message-content.is-user :deep(.message-link) {
  color: var(--overlay-text-active);
  text-decoration: underline;
}

.message-content.is-user :deep(.message-link:hover) {
  color: white;
  text-decoration: none;
}

@media (max-width: 768px) {
  .message-content.is-user {
    margin-left: 24px;
  }

  .message-content.is-assistant {
    margin-right: 24px;
  }
}
</style>
