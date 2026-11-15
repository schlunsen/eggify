<template>
  <section v-if="items.length > 0" class="review-queue" :aria-label="heading">
    <header class="rq-header">
      <div>
        <h2 class="rq-title">
          {{ heading }}
          <span class="rq-count" data-test="rq-count">{{ items.length }}</span>
        </h2>
        <p class="rq-hint" data-test="rq-hint">
          {{ originHint }} They are not used in any prompt until you approve them.
          Read them as you would text from an untrusted source.
        </p>
      </div>
    </header>

    <p v-if="error" class="rq-error" role="alert">{{ error }}</p>

    <ul v-if="folderGroups.length" class="rq-folders" data-test="rq-folders">
      <li v-for="group in folderGroups" :key="group.folder" class="rq-folder" data-test="rq-folder">
        <span class="rq-folder-path" :title="group.folder">{{ shortenHome(group.folder) }}</span>
        <span class="rq-folder-count">{{ group.ids.length }} skills</span>
        <template v-if="confirmFolder !== group.folder">
          <button
            class="rq-btn"
            :disabled="!!busyId"
            data-test="rq-approve-folder"
            @click="confirmFolder = group.folder"
          >
            Approve all from this folder
          </button>
        </template>
        <template v-else>
          <span class="rq-folder-confirm">Approve {{ group.ids.length }} skills without reading each?</span>
          <button
            class="rq-btn rq-approve"
            :disabled="!!busyId"
            data-test="rq-approve-folder-confirm"
            @click="approveFolder(group)"
          >
            Approve {{ group.ids.length }}
          </button>
          <button class="rq-btn" data-test="rq-approve-folder-cancel" @click="confirmFolder = null">Cancel</button>
        </template>
      </li>
    </ul>

    <ul class="rq-list">
      <li v-for="item in items" :key="item.id" class="rq-item" data-test="rq-item">
        <div class="rq-meta">
          <span class="rq-badge">Pending</span>
          <span v-if="item.subtitle && kind === 'memory'" class="rq-chip">{{ item.subtitle }}</span>
          <span v-if="item.importance !== undefined" class="rq-chip">importance {{ item.importance }}/10</span>
          <span class="rq-origin">
            {{ describeCreator(item.createdBy) }}
            <a v-if="item.sessionId" class="rq-session" :href="`/agents?session=${item.sessionId}`" data-test="rq-session">
              {{ item.sessionId.slice(0, 8) }}
            </a>
          </span>
          <span v-if="item.createdAt" class="rq-date">{{ formatDate(item.createdAt) }}</span>
        </div>

        <template v-if="editingId !== item.id">
          <h3 class="rq-item-title">{{ item.title }}</h3>
          <p v-if="kind === 'skill' && item.subtitle" class="rq-subtitle">{{ item.subtitle }}</p>
          <pre class="rq-content" data-test="rq-content">{{ shownContent(item) }}</pre>
          <button
            v-if="lineCount(item.content) > COLLAPSED_LINES"
            class="rq-more"
            data-test="rq-toggle-content"
            @click="toggleExpanded(item.id)"
          >
            {{ expanded.has(item.id) ? 'Show less' : `Show all (${lineCount(item.content)} lines)` }}
          </button>
          <div v-if="item.settings && item.settings.length" class="rq-settings" data-test="rq-settings">
            <p class="rq-files-title">Settings in SKILL.md</p>
            <ul class="rq-file-list">
              <li
                v-for="st in item.settings"
                :key="st.key"
                class="rq-setting"
                :class="{ 'rq-setting-risky': st.risky }"
                :data-test="st.risky ? 'rq-setting-risky' : 'rq-setting'"
              >
                <code class="rq-file-path">{{ st.key }}</code>
                <span v-if="st.note" class="rq-file-note">{{ st.note }}</span>
                <ul v-if="st.commands && st.commands.length" class="rq-hook-commands">
                  <li v-for="(cmd, i) in st.commands" :key="i"><code data-test="rq-hook-command">{{ cmd }}</code></li>
                </ul>
                <pre v-else class="rq-setting-value">{{ st.value }}</pre>
              </li>
            </ul>
          </div>
          <div v-if="item.files" class="rq-files" data-test="rq-files">
            <p class="rq-files-title">
              <template v-if="item.files.length">
                Files in this skill ({{ item.files.length }}, {{ formatBytes(item.filesTotalBytes || 0) }}).
                Approving covers all of them; scripts run with your permissions.
              </template>
              <template v-else>No other files besides SKILL.md.</template>
            </p>
            <p v-if="item.filesProblem" class="rq-error" role="alert" data-test="rq-files-problem">{{ item.filesProblem }}</p>
            <ul v-if="item.files.length" class="rq-file-list">
              <li v-for="f in item.files" :key="f.path" class="rq-file" data-test="rq-file">
                <code class="rq-file-path">{{ f.path }}</code>
                <span v-if="f.symlink" class="rq-file-note">link to {{ f.symlink }} (not copied)</span>
                <template v-else>
                  <span class="rq-file-size">{{ formatBytes(f.size) }}</span>
                  <span v-if="f.executable" class="rq-chip" data-test="rq-file-exec">executable</span>
                </template>
              </li>
            </ul>
          </div>
          <div class="rq-actions">
            <button class="rq-btn rq-approve" :disabled="busyId === item.id" data-test="rq-approve" @click="emit('approve', item.id)">
              Approve
            </button>
            <button class="rq-btn" :disabled="busyId === item.id" data-test="rq-edit" @click="startEdit(item)">
              Edit + Approve
            </button>
            <button class="rq-btn rq-reject" :disabled="busyId === item.id" data-test="rq-reject" @click="emit('reject', item.id)">
              Reject
            </button>
          </div>
        </template>

        <form v-else class="rq-edit" data-test="rq-edit-form" @submit.prevent="submitEdit(item)">
          <label class="rq-label">
            {{ kind === 'memory' ? 'Title' : 'Description' }}
            <input v-model="draft.title" class="rq-input" data-test="rq-edit-title" />
          </label>
          <label class="rq-label">
            {{ kind === 'memory' ? 'Content' : 'Instructions' }}
            <textarea v-model="draft.content" class="rq-input rq-textarea" rows="8" data-test="rq-edit-content" />
          </label>
          <label v-if="kind === 'memory'" class="rq-label rq-label-inline">
            Importance
            <input v-model.number="draft.importance" type="number" min="1" max="10" class="rq-input rq-number" data-test="rq-edit-importance" />
          </label>
          <div class="rq-actions">
            <button type="submit" class="rq-btn rq-approve" :disabled="busyId === item.id" data-test="rq-save-approve">
              Save + Approve
            </button>
            <button type="button" class="rq-btn" data-test="rq-cancel" @click="editingId = null">Cancel</button>
          </div>
        </form>
      </li>
    </ul>
  </section>
</template>

<script setup lang="ts">
import { computed, reactive, ref } from 'vue'
import type { FolderGroup, ReviewEdits, ReviewKind, ReviewQueueItem } from '~/types/review'
import { describeCreator, formatBytes, groupFileItemsByFolder, queueOrigins, shortenHome } from '~/types/review'

const props = defineProps<{
  kind: ReviewKind
  items: ReviewQueueItem[]
  busyId?: string | null
  error?: string | null
}>()

const emit = defineEmits<{
  (e: 'approve', id: string, edits?: ReviewEdits): void
  (e: 'reject', id: string): void
  /** Approve every listed skill discovered in one folder. */
  (e: 'approve-many', ids: string[], folder: string): void
}>()

const heading = computed(() =>
  props.kind === 'memory' ? 'Memories waiting for review' : 'Skills waiting for review'
)

const originHint = computed(() => {
  switch (queueOrigins(props.items)) {
    case 'file':
      return 'These were found in SKILL.md files on disk, which agents can also write.'
    case 'mixed':
      return 'Some were written by an agent, others found in SKILL.md files on disk (which agents can also write).'
    default:
      return 'An agent wrote these.'
  }
})

// Skills discovered from files, by the folder they were found in.
const folderGroups = computed<FolderGroup[]>(() =>
  props.kind === 'skill' ? groupFileItemsByFolder(props.items) : []
)
const confirmFolder = ref<string | null>(null)

function approveFolder(group: FolderGroup) {
  confirmFolder.value = null
  emit('approve-many', [...group.ids], group.folder)
}

// Long bodies are collapsed to their first lines.
const COLLAPSED_LINES = 12
const expanded = reactive(new Set<string>())

function lineCount(text: string) {
  return text ? text.split('\n').length : 0
}

function shownContent(item: ReviewQueueItem) {
  if (expanded.has(item.id) || lineCount(item.content) <= COLLAPSED_LINES) return item.content
  return item.content.split('\n').slice(0, COLLAPSED_LINES).join('\n') + '\n…'
}

function toggleExpanded(id: string) {
  if (expanded.has(id)) expanded.delete(id)
  else expanded.add(id)
}

const editingId = ref<string | null>(null)
const draft = reactive<{ title: string; content: string; importance: number }>({ title: '', content: '', importance: 5 })

function startEdit(item: ReviewQueueItem) {
  editingId.value = item.id
  draft.title = props.kind === 'memory' ? item.title : item.subtitle
  draft.content = item.content
  draft.importance = item.importance ?? 5
}

function submitEdit(item: ReviewQueueItem) {
  const original = props.kind === 'memory' ? item.title : item.subtitle
  const edits: ReviewEdits = {}
  if (draft.title.trim() && draft.title !== original) edits.title = draft.title
  if (draft.content.trim() && draft.content !== item.content) edits.content = draft.content
  if (props.kind === 'memory' && draft.importance !== item.importance &&
      Number.isInteger(draft.importance) && draft.importance >= 1 && draft.importance <= 10) {
    edits.importance = draft.importance
  }
  editingId.value = null
  emit('approve', item.id, Object.keys(edits).length ? edits : undefined)
}

function formatDate(value: string) {
  const d = new Date(value)
  return Number.isNaN(d.getTime()) ? '' : d.toLocaleString()
}
</script>

<style scoped>
.review-queue {
  border: 1px solid rgba(245, 158, 11, 0.45);
  background: rgba(245, 158, 11, 0.06);
  border-radius: 12px;
  padding: 1rem 1.25rem;
  margin-bottom: 1.25rem;
}
.rq-title {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  font-size: 1rem;
  font-weight: 600;
  margin: 0;
  color: var(--text-primary);
}
.rq-count {
  background: #f59e0b;
  color: #111;
  border-radius: 999px;
  padding: 0 0.5rem;
  font-size: 0.75rem;
  line-height: 1.4rem;
}
.rq-hint {
  margin: 0.25rem 0 0.75rem;
  font-size: 0.85rem;
  color: var(--text-secondary);
}
.rq-error {
  color: #ef4444;
  font-size: 0.85rem;
  margin: 0 0 0.75rem;
}
.rq-list {
  list-style: none;
  margin: 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: 0.75rem;
}
.rq-item {
  background: var(--bg-secondary);
  border: 1px solid var(--border-color, rgba(255, 255, 255, 0.08));
  border-radius: 10px;
  padding: 0.75rem 1rem;
}
.rq-meta {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 0.5rem;
  font-size: 0.75rem;
  color: var(--text-secondary);
}
.rq-badge {
  background: rgba(245, 158, 11, 0.2);
  color: #f59e0b;
  border-radius: 6px;
  padding: 0.1rem 0.45rem;
  font-weight: 600;
}
.rq-chip {
  border: 1px solid var(--border-color, rgba(255, 255, 255, 0.12));
  border-radius: 6px;
  padding: 0.05rem 0.4rem;
}
.rq-session {
  font-family: ui-monospace, monospace;
  color: var(--accent-purple);
}
.rq-item-title {
  margin: 0.5rem 0 0.25rem;
  font-size: 0.95rem;
  color: var(--text-primary);
}
.rq-subtitle {
  margin: 0 0 0.25rem;
  font-size: 0.85rem;
  color: var(--text-secondary);
}
.rq-content {
  white-space: pre-wrap;
  word-break: break-word;
  max-height: 16rem;
  overflow: auto;
  font-size: 0.8rem;
  background: var(--bg-primary);
  border-radius: 8px;
  padding: 0.6rem 0.75rem;
  margin: 0.25rem 0 0.75rem;
  color: var(--text-primary);
}
.rq-more {
  background: none;
  border: none;
  padding: 0;
  margin: -0.5rem 0 0.75rem;
  font-size: 0.8rem;
  color: var(--accent-purple);
  cursor: pointer;
}
.rq-folders {
  list-style: none;
  margin: 0 0 0.75rem;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: 0.4rem;
}
.rq-folder {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 0.5rem;
  font-size: 0.8rem;
  color: var(--text-secondary);
}
.rq-folder-path {
  font-family: ui-monospace, monospace;
  color: var(--text-primary);
  overflow-wrap: anywhere;
}
.rq-folder-confirm {
  color: var(--text-primary);
}
.rq-actions {
  display: flex;
  flex-wrap: wrap;
  gap: 0.5rem;
}
.rq-btn {
  border: 1px solid var(--border-color, rgba(255, 255, 255, 0.15));
  background: transparent;
  color: var(--text-primary);
  border-radius: 8px;
  padding: 0.35rem 0.8rem;
  font-size: 0.8rem;
  cursor: pointer;
}
.rq-btn:disabled {
  opacity: 0.5;
  cursor: default;
}
.rq-approve {
  background: #16a34a;
  border-color: #16a34a;
  color: white;
}
.rq-reject {
  color: #ef4444;
  border-color: rgba(239, 68, 68, 0.5);
}
.rq-edit {
  display: flex;
  flex-direction: column;
  gap: 0.6rem;
  margin-top: 0.5rem;
}
.rq-label {
  display: flex;
  flex-direction: column;
  gap: 0.25rem;
  font-size: 0.8rem;
  color: var(--text-secondary);
}
.rq-label-inline {
  flex-direction: row;
  align-items: center;
  gap: 0.5rem;
}
.rq-input {
  background: var(--bg-primary);
  color: var(--text-primary);
  border: 1px solid var(--border-color, rgba(255, 255, 255, 0.15));
  border-radius: 8px;
  padding: 0.4rem 0.6rem;
  font: inherit;
}
.rq-textarea {
  font-family: ui-monospace, monospace;
  resize: vertical;
}
.rq-number {
  width: 4.5rem;
}
.rq-settings {
  margin-top: 10px;
}
.rq-setting {
  display: block;
  padding: 4px 0;
}
.rq-setting-risky {
  border-left: 3px solid var(--status-warning);
  padding-left: 8px;
}
.rq-setting-value,
.rq-hook-commands {
  margin: 4px 0 0;
  white-space: pre-wrap;
  word-break: break-word;
  font-size: 12px;
}
.rq-hook-commands {
  padding-left: 18px;
}
.rq-files {
  margin: 0.5rem 0;
  font-size: 0.85rem;
}
.rq-files-title {
  margin: 0 0 0.25rem;
  color: var(--text-secondary);
}
.rq-file-list {
  list-style: none;
  margin: 0;
  padding: 0;
  max-height: 14rem;
  overflow: auto;
}
.rq-file {
  display: flex;
  gap: 0.5rem;
  align-items: baseline;
  padding: 0.1rem 0;
}
.rq-file-path {
  overflow-wrap: anywhere;
}
.rq-file-size,
.rq-file-note {
  color: var(--text-secondary);
  white-space: nowrap;
}
</style>
