<template>
  <button class="context-button" type="button" @click.stop="open" @keydown.stop>
    View switch context
  </button>
  <Teleport to="body">
    <dialog ref="dialog" class="switch-context-dialog" aria-label="Model switch context"
      @cancel.prevent="close" @click.stop="onDialogClick" @keydown.stop>
      <header>
        <h2>Switch context</h2>
        <button type="button" aria-label="Close switch context" @click="close">Close</button>
      </header>
      <p v-if="loading" role="status">Loading context…</p>
      <p v-else-if="error" role="alert">{{ error }}</p>
      <template v-else-if="snapshot">
        <p class="context-description">{{ description }}</p>
        <p v-if="snapshot.omitted_records">{{ snapshot.omitted_records }} earlier conversation records omitted.</p>
        <p v-if="snapshot.context" class="context-size">{{ snapshot.context.length.toLocaleString() }} characters</p>
        <pre v-if="snapshot.context" tabindex="0">{{ snapshot.context }}</pre>
      </template>
      <footer>
        <span role="status">{{ copyStatus }}</span>
        <button v-if="snapshot?.context" type="button" @click="copyContext">Copy context</button>
        <button v-if="error || snapshot?.status === 'pending' || snapshot?.status === 'preview'" type="button" :disabled="loading" @click="load">Refresh</button>
      </footer>
    </dialog>
  </Teleport>
</template>

<script setup lang="ts">
import { copyText } from '~/utils/clipboard'
import { computed, onBeforeUnmount, ref } from 'vue'
import { useAuthenticatedFetch } from '@/composables/useAuthenticatedFetch'

const props = defineProps<{ sessionId: string; messageId: string }>()
interface SwitchContext {
  history_mode: string
  status: 'sent' | 'preview' | 'pending' | 'resumed' | 'empty' | 'superseded' | 'unavailable'
  context?: string
  format: string
  omitted_records?: number
}
const { fetchWithAuth } = useAuthenticatedFetch()
const dialog = ref<HTMLDialogElement>()
const snapshot = ref<SwitchContext>()
const loading = ref(false)
const error = ref('')
const copyStatus = ref('')
let request: AbortController | undefined

const description = computed(() => {
  switch (snapshot.value?.status) {
    case 'sent': return snapshot.value.format === 'json'
      ? 'The conversation included in the first request after this switch, including the system prompt and your new message. Tool definitions are not included here.'
      : 'Recorded handoff: the exact conversation transcript carried into the first request after this switch, including any omission markers. The new message and provider instructions are not included.'
    case 'preview': return snapshot.value.history_mode === 'rebuilt'
      ? 'Pending preview: the latest 200 user and assistant records, using the same history selection as the next request. Earlier records are omitted when this limit is reached. The system prompt and your next message are added when you send it.'
      : 'Pending preview: the conversation transcript that will be carried over with your next message. It is recorded now and replaced with the exact payload once that message is sent.'
    case 'pending': return 'Waiting for your next message. The handoff context will be saved when the next request is sent. Refresh to check again.'
    case 'resumed': return snapshot.value.context
      ? 'Local history approximation: the provider resumed its own stored conversation. This is a shortened reconstruction from Eggify records, not the actual provider context. Compaction, instructions, images and tool state may differ; omission markers describe only this reconstruction.'
      : 'The model resumed the existing provider conversation. No separate handoff transcript was sent.'
    case 'empty': return 'There was no earlier conversation to carry over.'
    case 'superseded': return 'Another switch happened before a handoff was recorded for this model.'
    default: return 'The exact context was not recorded for this older switch. It cannot be reconstructed reliably from the current conversation.'
  }
})

async function load() {
  request?.abort()
  const controller = new AbortController()
  request = controller
  loading.value = true
  error.value = ''
  copyStatus.value = ''
  snapshot.value = undefined
  try {
    const response = await fetchWithAuth(`/api/agent/sessions/${encodeURIComponent(props.sessionId)}/model-switches/${encodeURIComponent(props.messageId)}/context`, { signal: controller.signal })
    if (!response.ok) throw new Error('Could not load switch context. Please try again.')
    const data = await response.json()
    if (!controller.signal.aborted) snapshot.value = data
  } catch (err) {
    if (!controller.signal.aborted) error.value = err instanceof Error ? err.message : 'Could not load switch context.'
  } finally {
    if (!controller.signal.aborted) loading.value = false
  }
}
function open() {
  dialog.value?.showModal()
  load()
}
function close() {
  request?.abort()
  dialog.value?.close()
}
function onDialogClick(event: MouseEvent) {
  if (event.target === dialog.value) {
    const rect = dialog.value!.getBoundingClientRect()
    if (event.clientX < rect.left || event.clientX > rect.right || event.clientY < rect.top || event.clientY > rect.bottom) close()
  }
}
async function copyContext() {
  try {
    await copyText(snapshot.value?.context || '')
    copyStatus.value = 'Copied'
  } catch {
    copyStatus.value = 'Could not copy. Select the text to copy it manually.'
  }
}
onBeforeUnmount(() => request?.abort())
</script>

<style scoped>
.context-button, .switch-context-dialog button {
  padding: 8px 12px;
  border: 1px solid var(--border-color, #555);
  border-radius: 6px;
  background: var(--bg-tertiary, #303040);
  color: var(--text-primary, #eee);
  cursor: pointer;
}
.context-button { margin-top: 12px; }
.switch-context-dialog {
  width: min(900px, calc(100vw - 48px));
  max-height: 85vh;
  padding: 24px;
  border: 1px solid var(--border-color, #555);
  border-radius: 12px;
  background: var(--bg-secondary, #272936);
  color: var(--text-primary, #eee);
}
.switch-context-dialog::backdrop { background: rgb(0 0 0 / 65%); }
header, footer { display: flex; align-items: center; gap: 12px; justify-content: space-between; }
h2 { margin: 0; font-size: 1.2rem; }
.context-description { line-height: 1.5; }
.context-size { color: var(--text-secondary, #aaa); font-size: 0.85rem; }
pre { max-height: 55vh; overflow: auto; white-space: pre-wrap; overflow-wrap: anywhere; padding: 16px; background: var(--bg-primary, #1b1d27); border-radius: 8px; font-size: 0.85rem; line-height: 1.5; }
footer { margin-top: 16px; }
button:disabled { opacity: 0.5; cursor: wait; }
</style>
