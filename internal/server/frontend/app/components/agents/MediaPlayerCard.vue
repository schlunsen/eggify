<template>
  <!-- Clicks and Enter/Space stay in the card, so using the player never opens the message -->
  <div
    class="media-card"
    :class="`media-card-${item.kind}`"
    @click.stop
    @keydown.enter.stop
    @keydown.space.stop
  >
    <div class="media-card-header">
      <svg v-if="item.kind === 'video'" class="media-card-icon" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
        <rect x="2" y="5" width="14" height="14" rx="2"></rect>
        <path d="M16 10l6-4v12l-6-4"></path>
      </svg>
      <svg v-else class="media-card-icon" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
        <path d="M9 18V5l12-2v13"></path>
        <circle cx="6" cy="18" r="3"></circle>
        <circle cx="18" cy="16" r="3"></circle>
      </svg>
      <div class="media-card-titles">
        <span class="media-card-title" :title="title">{{ title }}</span>
        <span class="media-card-meta" :title="item.path">{{ item.path }}<template v-if="sizeLabel"> · {{ sizeLabel }}</template></span>
      </div>
      <button
        type="button"
        class="media-card-button"
        :disabled="downloading"
        :aria-label="`Download ${item.name}`"
        title="Download"
        @click="download"
      >
        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
          <path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4"></path>
          <polyline points="7 10 12 15 17 10"></polyline>
          <line x1="12" y1="15" x2="12" y2="3"></line>
        </svg>
        <span>{{ downloading ? 'Downloading…' : 'Download' }}</span>
      </button>
    </div>

    <div v-if="playError" class="media-card-error">
      <p>{{ playError }}</p>
      <button type="button" class="media-card-button" @click="download">Download instead</button>
    </div>
    <video
      v-else-if="item.kind === 'video'"
      :key="src"
      class="media-card-video"
      :src="src"
      :poster="item.poster_url || undefined"
      controls
      playsinline
      preload="metadata"
      @error="handlePlayError"
    ></video>
    <audio
      v-else
      :key="src"
      class="media-card-audio"
      :src="src"
      controls
      preload="metadata"
      @error="handlePlayError"
    ></audio>

    <div v-if="!item.ios_plays" class="media-card-ios">
      <span class="media-card-note">iPhone can't play {{ extLabel }} files.</span>
      <template v-if="ffmpeg">
        <span v-if="item.compatible === 'running'" class="media-card-status">Converting…</span>
        <span v-else-if="item.compatible === 'ready'" class="media-card-status ready">iPhone copy ready</span>
        <template v-else>
          <span v-if="item.compatible === 'failed'" class="media-card-status failed">
            Couldn't convert{{ item.compatible_error ? `: ${item.compatible_error}` : '.' }}
          </span>
          <button type="button" class="media-card-button" :disabled="converting" @click="startConvert">
            {{ item.compatible === 'failed' ? 'Try again' : 'Convert for iPhone' }}
          </button>
        </template>
        <span v-if="convertError" class="media-card-status failed">{{ convertError }}</span>
      </template>
    </div>
  </div>
</template>

<script setup lang="ts">
/**
 * One video or audio file the agent made or showed: a native player, Download,
 * and (for formats iPhone can't play) a button that makes an H.264/AAC copy.
 * If the browser can't play the file, it tries the iPhone copy when there is
 * one, then the file fetched with the API key (the desktop app may not send
 * the session cookie with a media request), then says so and offers Download.
 */
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useAuthenticatedFetch } from '~/composables/useAuthenticatedFetch'
import { useSessionMedia } from '~/composables/agents/useSessionMedia'
import { formatMediaSize, mediaExtension, type MediaItem } from '~/utils/agents/sessionMedia'

const props = defineProps<{
  item: MediaItem
  sessionId: string
  /** ffmpeg is installed, so an iPhone copy can be made */
  ffmpeg?: boolean
}>()

// Larger files are not pulled into memory as a fallback
const MAX_BLOB_FALLBACK_BYTES = 300 * 1024 * 1024

const { fetchWithAuth } = useAuthenticatedFetch()
const media = useSessionMedia()

const title = computed(() => props.item.title || props.item.name)
const sizeLabel = computed(() => formatMediaSize(props.item.size))
const extLabel = computed(() => (mediaExtension(props.item.name) || props.item.kind).toUpperCase())

const src = ref(props.item.url)
const playError = ref('')
const downloading = ref(false)
const converting = ref(false)
const convertError = ref('')
let triedCompatible = false
let triedBlob = false
let blobUrl = ''

function revokeBlob() {
  if (blobUrl) URL.revokeObjectURL(blobUrl)
  blobUrl = ''
}

// A new file (or the file changed): start over with its own URL
watch(() => [props.item.id, props.item.url, props.item.modified], () => {
  revokeBlob()
  src.value = props.item.url
  playError.value = ''
  triedCompatible = false
  triedBlob = false
})

onBeforeUnmount(revokeBlob)

async function handlePlayError() {
  const item = props.item
  if (!triedCompatible && item.compatible === 'ready' && item.compatible_url && src.value !== item.compatible_url) {
    triedCompatible = true
    src.value = item.compatible_url
    return
  }
  if (triedBlob) {
    playError.value = `This browser can't play this ${item.kind === 'video' ? 'video' : 'audio file'} (${extLabel.value}).`
    return
  }
  triedBlob = true
  if (item.size > MAX_BLOB_FALLBACK_BYTES) {
    playError.value = `Couldn't play this ${item.kind === 'video' ? 'video' : 'audio file'} here.`
    return
  }
  const id = item.id
  try {
    const res = await fetchWithAuth(item.url)
    if (res.status === 409) {
      playError.value = 'The file changed since the list was loaded. Reloading…'
      void media.load(props.sessionId)
      return
    }
    if (!res.ok) throw new Error(`HTTP ${res.status}`)
    const url = URL.createObjectURL(await res.blob())
    if (props.item.id !== id) { URL.revokeObjectURL(url); return }
    revokeBlob()
    blobUrl = url
    src.value = url
  } catch {
    if (props.item.id === id) playError.value = `Couldn't load this ${item.kind === 'video' ? 'video' : 'audio file'}.`
  }
}

async function download() {
  if (downloading.value) return
  downloading.value = true
  try {
    const res = await fetchWithAuth(props.item.url)
    if (res.status === 409) { void media.load(props.sessionId); return }
    if (!res.ok) return
    const url = URL.createObjectURL(await res.blob())
    const a = document.createElement('a')
    a.href = url
    a.download = props.item.name || 'download'
    document.body.appendChild(a)
    a.click()
    document.body.removeChild(a)
    URL.revokeObjectURL(url)
  } catch (error) {
    console.error('Download failed:', error)
  } finally {
    downloading.value = false
  }
}

async function startConvert() {
  if (converting.value) return
  converting.value = true
  convertError.value = ''
  try {
    convertError.value = await media.convert(props.sessionId, props.item)
  } finally {
    converting.value = false
  }
}
</script>

<style scoped>
.media-card {
  display: flex;
  flex-direction: column;
  gap: 8px;
  width: 100%;
  max-width: 640px;
  padding: 10px;
  border: 1px solid var(--border-color);
  border-radius: 10px;
  background: var(--card-bg);
  color: var(--text-primary);
  cursor: default;
  box-sizing: border-box;
}

.media-card-header {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
}

.media-card-icon {
  flex-shrink: 0;
  color: var(--accent-purple);
}

.media-card-titles {
  display: flex;
  flex-direction: column;
  min-width: 0;
  flex: 1;
}

.media-card-title {
  font-size: 0.875rem;
  font-weight: 600;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.media-card-meta {
  font-size: 0.75rem;
  color: var(--text-secondary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.media-card-button {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  flex-shrink: 0;
  padding: 4px 10px;
  border: 1px solid var(--border-color);
  border-radius: 6px;
  background: transparent;
  color: var(--text-primary);
  font: inherit;
  font-size: 0.75rem;
  cursor: pointer;
  transition: background 0.15s, border-color 0.15s;
}

.media-card-button:hover:not(:disabled) {
  background: var(--overlay-bg-hover);
  border-color: var(--accent-purple);
}

.media-card-button:disabled {
  opacity: 0.6;
  cursor: default;
}

.media-card-button:focus-visible {
  outline: 2px solid var(--accent-purple);
  outline-offset: 2px;
}

.media-card-video {
  display: block;
  width: 100%;
  max-height: 360px;
  border-radius: 6px;
  background: #000;
}

.media-card-audio {
  display: block;
  width: 100%;
}

.media-card-error {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  flex-wrap: wrap;
  padding: 10px;
  border: 1px dashed var(--border-color);
  border-radius: 6px;
  font-size: 0.8125rem;
  color: var(--text-secondary);
}

.media-card-error p {
  margin: 0;
}

.media-card-ios {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
  font-size: 0.75rem;
  color: var(--text-secondary);
}

.media-card-status {
  font-weight: 500;
  color: var(--text-primary);
}

.media-card-status.ready {
  color: var(--accent-purple);
}

.media-card-status.failed {
  color: var(--text-secondary);
}

@media (max-width: 768px) {
  .media-card-video {
    max-height: 240px;
  }

  .media-card-button span {
    display: none;
  }
}
</style>
