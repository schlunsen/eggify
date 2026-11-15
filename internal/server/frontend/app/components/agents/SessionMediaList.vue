<template>
  <div class="session-media">
    <div v-if="!state || (!state.loaded && state.loading)" class="session-media-empty">
      <p>Loading media…</p>
    </div>
    <div v-else-if="state.error && items.length === 0" class="session-media-empty">
      <p>{{ state.error }}</p>
      <button type="button" class="session-media-retry" @click="media.load(sessionId)">Try again</button>
    </div>
    <div v-else-if="items.length === 0" class="session-media-empty">
      <svg width="40" height="40" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" opacity="0.5" aria-hidden="true">
        <rect x="2" y="5" width="14" height="14" rx="2"></rect>
        <path d="M16 10l6-4v12l-6-4"></path>
      </svg>
      <p class="session-media-empty-title">No video or audio yet</p>
      <p>
        Videos and sound the agent makes in this session (an ffmpeg render, generated music
        or speech), and files it shows you with show_media, appear here.
      </p>
    </div>
    <template v-else>
      <div class="session-media-summary">
        {{ items.length }} {{ items.length === 1 ? 'file' : 'files' }}, newest first
      </div>
      <div class="session-media-list">
        <MediaPlayerCard
          v-for="item in items"
          :key="item.id"
          :item="item"
          :session-id="sessionId"
          :ffmpeg="state.ffmpeg"
        />
      </div>
    </template>
  </div>
</template>

<script setup lang="ts">
/** The chat header's Media tab: every video and audio file of the session. */
import { computed, watch } from 'vue'
import MediaPlayerCard from '~/components/agents/MediaPlayerCard.vue'
import { useSessionMedia } from '~/composables/agents/useSessionMedia'

const props = defineProps<{ sessionId: string }>()

const media = useSessionMedia()
const state = computed(() => media.stateFor(props.sessionId))
const items = computed(() => state.value?.items ?? [])

// Reload whenever the tab is shown for a session (the cache may be a while old)
watch(() => props.sessionId, id => { if (id) void media.load(id) }, { immediate: true })
</script>

<style scoped>
.session-media {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  padding: 16px;
}

.session-media-summary {
  margin: 0 auto 12px;
  max-width: 640px;
  font-size: 0.8125rem;
  color: var(--text-secondary);
}

.session-media-list {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 12px;
}

.session-media-empty {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 8px;
  max-width: 420px;
  margin: 48px auto;
  text-align: center;
  font-size: 0.875rem;
  color: var(--text-secondary);
}

.session-media-empty p {
  margin: 0;
}

.session-media-empty-title {
  font-weight: 600;
  color: var(--text-primary);
}

.session-media-retry {
  padding: 4px 12px;
  border: 1px solid var(--border-color);
  border-radius: 6px;
  background: transparent;
  color: var(--text-primary);
  font: inherit;
  cursor: pointer;
}

.session-media-retry:hover {
  background: var(--overlay-bg-hover);
}

@media (max-width: 768px) {
  .session-media {
    padding: 8px;
  }
}
</style>
