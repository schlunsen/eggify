<template>
  <section class="gcp" aria-label="Changes">
    <header class="gcp-head">
      <div class="gcp-title">
        <span class="gcp-kicker">{{ kicker }}</span>
        <h2>
          <span class="mono">{{ selection.branch || (worktree ? 'detached HEAD' : '—') }}</span>
          <span v-if="worktree && !worktree.is_main" class="gcp-where" :title="worktree.path">in {{ displayPath(worktree.path, root, home) }}</span>
        </h2>
      </div>
      <div class="gcp-tabs" role="tablist">
        <button v-for="s in scopes" :key="s" role="tab" :aria-selected="scope === s" :class="{ on: scope === s }" @click="$emit('update:scope', s)">
          <template v-if="s === 'uncommitted'">Uncommitted <b v-if="worktree">{{ dirtyTotal(worktree.dirty) }}</b></template>
          <template v-else>vs {{ base }} <b v-if="branchInfo">{{ branchInfo.base_ahead }} {{ branchInfo.base_ahead === 1 ? 'commit' : 'commits' }}</b></template>
        </button>
      </div>
    </header>

    <div v-if="!scopes.length" class="gcp-empty">
      <p>This is the default branch and it isn't checked out anywhere, so there is nothing to compare.</p>
    </div>
    <div v-else-if="loading && !changes" class="gcp-empty"><EggLoader compact /> Loading changes…</div>
    <div v-else-if="error" class="gcp-empty gcp-error">
      <p>{{ error }}</p>
      <button class="gcp-btn" @click="load()">Try again</button>
    </div>
    <template v-else-if="changes">
      <div v-if="changes.commits?.length" class="gcp-commits">
        <button class="gcp-commits-toggle" @click="showCommits = !showCommits">
          {{ showCommits ? '▾' : '▸' }} {{ changes.commits.length }} {{ changes.commits.length === 1 ? 'commit' : 'commits' }} on top of {{ changes.base }}
        </button>
        <ol v-if="showCommits">
          <li v-for="c in changes.commits" :key="c.sha">
            <a v-if="remoteWebUrl" class="mono sha" :href="`${remoteWebUrl}/commit/${c.sha}`" target="_blank" rel="noopener">{{ c.short }}</a>
            <span v-else class="mono sha">{{ c.short }}</span>
            <span class="subj">{{ c.subject }}</span>
            <span class="meta">{{ c.author }} · {{ relativeTime(unixToIso(c.at)) }}</span>
          </li>
        </ol>
      </div>

      <div v-if="!changes.files.length" class="gcp-empty gcp-clean">
        <span class="gcp-clean-mark" aria-hidden="true">✓</span>
        <p v-if="scope === 'uncommitted'">Nothing uncommitted here. The working tree is clean.</p>
        <p v-else>{{ selection.branch }} has no changes on top of {{ changes.base }}.</p>
      </div>

      <template v-else>
        <div class="gcp-toolbar">
          <span class="gcp-stats">
            <b>{{ changes.stats.files_changed }}</b> {{ changes.stats.files_changed === 1 ? 'file' : 'files' }}
            <span class="add">+{{ changes.stats.additions }}</span>
            <span class="del">−{{ changes.stats.deletions }}</span>
            <span v-if="viewedCount" class="gcp-viewed-count">· {{ viewedCount }}/{{ changes.files.length }} viewed</span>
          </span>
          <span class="gcp-tools">
            <input v-model="fileQuery" class="gcp-filter" type="search" placeholder="Filter files" aria-label="Filter files">
            <button class="gcp-btn ghost" :aria-pressed="wrap" @click="wrap = !wrap">Wrap</button>
            <button class="gcp-btn ghost" @click="setAll(!allCollapsed)">{{ allCollapsed ? 'Expand all' : 'Collapse all' }}</button>
            <span v-if="loading" class="gcp-refreshing"><EggLoader compact /></span>
          </span>
        </div>

        <div class="gcp-body">
          <nav class="gcp-files" aria-label="Changed files">
            <button v-for="f in shownFiles" :key="f.path" class="gcp-file" :class="{ viewed: viewed.has(f.path), active: activeFile === f.path }" :title="f.path" @click="jumpTo(f.path)">
              <span class="dot" :class="f.status" aria-hidden="true" />
              <span class="name">{{ splitPath(f.path).name }}</span>
              <span class="dir"><bdi>{{ splitPath(f.path).dir.replace(/\/$/, '') }}</bdi></span>
              <span class="n"><span v-if="f.additions" class="add">+{{ f.additions }}</span> <span v-if="f.deletions" class="del">−{{ f.deletions }}</span></span>
            </button>
            <p v-if="!shownFiles.length" class="gcp-none">No file matches.</p>
          </nav>
          <div ref="diffsEl" class="gcp-diffs">
            <GitDiffFile
              v-for="f in shownFiles" :key="f.path" :file="f" :wrap="wrap"
              :collapsed="collapsed.has(f.path)" :viewed="viewed.has(f.path)"
              :loading="fileLoading.has(f.path)" :load-error="fileErrors[f.path]"
              @toggle="toggle(f.path)" @viewed="v => setViewed(f.path, v)" @load="loadFile(f.path)"
            />
          </div>
        </div>
      </template>
    </template>
  </section>
</template>

<script setup lang="ts">
import { computed, nextTick, reactive, ref, watch } from 'vue'
import GitDiffFile from '~/components/git/GitDiffFile.vue'
import EggLoader from '~/components/ui/EggLoader.vue'
import { relativeTime } from '~/utils/projects/projectSetup'
import { parseUnifiedDiff, renderedLineCount } from '~/utils/git/unifiedDiff'
import {
  changesQuery, dirtyTotal, displayPath, scopesFor, selectionSignature, splitPath, unixToIso,
  type GitChanges, type GitOverview, type GitScope, type GitSelection,
} from '~/utils/git/gitOverview'

const props = defineProps<{
  projectId: string
  overview: GitOverview
  selection: GitSelection
  scope: GitScope
  home?: string
}>()
defineEmits<{ 'update:scope': [scope: GitScope] }>()

const { fetchWithAuth } = useAuthenticatedFetch()

/** Files longer than this start collapsed. */
const COLLAPSE_LINES = 400

const changes = ref<GitChanges | null>(null)
const loading = ref(false)
const error = ref('')
const showCommits = ref(true)
const fileQuery = ref('')
const wrap = ref(false)
const collapsed = reactive(new Set<string>())
const viewed = reactive(new Set<string>())
const fileLoading = reactive(new Set<string>())
const fileErrors = reactive<Record<string, string>>({})
const activeFile = ref('')
const diffsEl = ref<HTMLElement>()

const root = computed(() => props.overview.root)
const base = computed(() => props.overview.default_branch)
const remoteWebUrl = computed(() => props.overview.remote_web_url || '')
const scopes = computed(() => scopesFor(props.selection, base.value))
const worktree = computed(() => props.selection.worktree ? props.overview.worktrees.find(w => w.path === props.selection.worktree) : undefined)
const branchInfo = computed(() => props.selection.branch ? props.overview.branches.find(b => b.name === props.selection.branch) : undefined)
const kicker = computed(() => {
  if (worktree.value?.is_main) return 'Main checkout'
  if (worktree.value) return 'Worktree'
  return 'Branch'
})

const shownFiles = computed(() => {
  const files = changes.value?.files || []
  const q = fileQuery.value.trim().toLowerCase()
  return q ? files.filter(f => f.path.toLowerCase().includes(q)) : files
})
const allCollapsed = computed(() => shownFiles.value.length > 0 && shownFiles.value.every(f => collapsed.has(f.path)))
const viewedCount = computed(() => (changes.value?.files || []).filter(f => viewed.has(f.path)).length)

// "Viewed" marks last for the tab's life, per selection and file content.
const viewedKey = computed(() => `eggify-git-viewed:${props.projectId}:${props.selection.worktree || ''}:${props.selection.branch || ''}:${props.scope}`)
function fileFingerprint(path: string) {
  const f = changes.value?.files.find(x => x.path === path)
  return f ? `${f.path}|${f.additions}|${f.deletions}|${(f.diff || '').length}` : path
}
function loadViewed() {
  viewed.clear()
  try {
    const saved: string[] = JSON.parse(sessionStorage.getItem(viewedKey.value) || '[]')
    const prints = new Set(saved)
    for (const f of changes.value?.files || []) if (prints.has(fileFingerprint(f.path))) viewed.add(f.path)
  } catch { /* storage unavailable */ }
}
function saveViewed() {
  try { sessionStorage.setItem(viewedKey.value, JSON.stringify([...viewed].map(fileFingerprint))) } catch { /* storage unavailable */ }
}
function setViewed(path: string, v: boolean) {
  if (v) { viewed.add(path); collapsed.add(path) } else { viewed.delete(path); collapsed.delete(path) }
  saveViewed()
}

function toggle(path: string) {
  if (collapsed.has(path)) collapsed.delete(path)
  else collapsed.add(path)
}
function setAll(c: boolean) {
  for (const f of shownFiles.value) {
    if (c) collapsed.add(f.path)
    else collapsed.delete(f.path)
  }
}

async function jumpTo(path: string) {
  collapsed.delete(path)
  activeFile.value = path
  await nextTick()
  const el = diffsEl.value?.querySelector(`[data-path="${CSS.escape(path)}"]`)
  el?.scrollIntoView({ behavior: 'smooth', block: 'start' })
}

let requestId = 0
async function load(keepState = false) {
  if (!scopes.value.includes(props.scope)) { changes.value = null; return }
  const id = ++requestId
  loading.value = true
  error.value = ''
  try {
    const res = await fetchWithAuth(`/api/projects/${props.projectId}/git/changes?${changesQuery(props.selection, props.scope, base.value)}`)
    const body = await res.json()
    if (id !== requestId) return
    if (!res.ok) throw new Error(body?.error || `HTTP ${res.status}`)
    const prevCollapsed = new Set(collapsed)
    const prevPaths = new Set((changes.value?.files || []).map(f => f.path))
    changes.value = body as GitChanges
    collapsed.clear()
    for (const f of changes.value.files) {
      const big = f.diff ? renderedLineCount(parseUnifiedDiff(f.diff)) > COLLAPSE_LINES : false
      const keep = keepState && prevPaths.has(f.path)
      if (keep ? prevCollapsed.has(f.path) : big) collapsed.add(f.path)
    }
    loadViewed()
    for (const p of viewed) collapsed.add(p)
  } catch (e) {
    if (id !== requestId) return
    error.value = e instanceof Error ? e.message : 'Could not load the changes'
  } finally {
    if (id === requestId) loading.value = false
  }
}

/** Loads the patch of a file the first response left out (large change sets). */
async function loadFile(path: string) {
  fileLoading.add(path)
  delete fileErrors[path]
  try {
    const res = await fetchWithAuth(`/api/projects/${props.projectId}/git/changes?${changesQuery(props.selection, props.scope, base.value, path)}`)
    const body = await res.json()
    if (!res.ok) throw new Error(body?.error || `HTTP ${res.status}`)
    const one = (body as GitChanges).files.find(f => f.path === path)
    const list = changes.value?.files
    const i = list?.findIndex(f => f.path === path) ?? -1
    if (one && list && i >= 0) list[i] = one
    else fileErrors[path] = 'The file has no changes any more.'
  } catch (e) {
    fileErrors[path] = e instanceof Error ? e.message : 'Could not load the diff'
  } finally {
    fileLoading.delete(path)
  }
}

// A new selection or scope starts fresh; the same one changing on disk
// (an agent committed or edited) reloads in place.
watch(() => [props.selection.worktree, props.selection.branch, props.scope], () => {
  changes.value = null
  fileQuery.value = ''
  showCommits.value = true
  load()
}, { immediate: true })
watch(() => selectionSignature(props.overview, props.selection), (now, before) => {
  if (before && now !== before && !loading.value) load(true)
})

defineExpose({ reload: () => load(true) })
</script>

<style scoped>
.gcp { display: flex; flex-direction: column; min-width: 0; min-height: 0; }
.gcp-head {
  display: flex; align-items: flex-end; justify-content: space-between; gap: 16px; flex-wrap: wrap;
  padding-bottom: 14px;
  border-bottom: 1px solid var(--border-color);
}
.gcp-kicker { font-size: 11px; letter-spacing: .08em; text-transform: uppercase; color: var(--text-muted); }
.gcp-title h2 { margin: 2px 0 0; font-size: 19px; font-weight: 650; color: var(--text-primary); display: flex; align-items: baseline; gap: 10px; flex-wrap: wrap; }
.gcp-where { font-size: 13px; font-weight: 400; color: var(--text-secondary); font-family: var(--font-mono, ui-monospace, Menlo, monospace); }
.mono { font-family: var(--font-mono, ui-monospace, Menlo, monospace); }
.gcp-tabs { display: inline-flex; padding: 3px; border-radius: 10px; background: var(--bg-secondary); border: 1px solid var(--border-color); }
.gcp-tabs button {
  border: 0; background: transparent; color: var(--text-secondary);
  padding: 6px 12px; border-radius: 7px; font: inherit; font-size: 13px; cursor: pointer;
}
.gcp-tabs button b { margin-left: 4px; font-weight: 600; color: var(--text-muted); }
.gcp-tabs button.on { background: var(--card-bg); color: var(--text-primary); box-shadow: 0 1px 2px var(--shadow-color, rgba(0,0,0,.15)); }
.gcp-tabs button.on b { color: var(--accent-purple); }

.gcp-empty { display: flex; align-items: center; gap: 10px; padding: 36px 8px; color: var(--text-secondary); }
.gcp-error { flex-direction: column; align-items: flex-start; color: var(--status-error); }
.gcp-clean { flex-direction: column; text-align: center; justify-content: center; padding: 56px 8px; }
.gcp-clean-mark {
  width: 44px; height: 44px; border-radius: 50%; display: grid; place-items: center;
  font-size: 22px; color: var(--status-success);
  background: color-mix(in srgb, var(--status-success) 14%, transparent);
}
.gcp-btn {
  padding: 6px 12px; border-radius: 8px; border: 1px solid var(--border-color);
  background: var(--card-bg); color: var(--text-primary); font: inherit; font-size: 13px; cursor: pointer;
}
.gcp-btn.ghost { background: transparent; color: var(--text-secondary); }
.gcp-btn[aria-pressed="true"] { color: var(--accent-purple); border-color: var(--accent-purple); }
.gcp-btn:hover { border-color: var(--accent-purple); }

.gcp-commits { margin-top: 12px; border: 1px solid var(--border-color); border-radius: 10px; background: var(--card-bg); }
.gcp-commits-toggle { width: 100%; text-align: left; border: 0; background: none; padding: 9px 12px; color: var(--text-secondary); font: inherit; font-size: 13px; cursor: pointer; }
.gcp-commits ol { list-style: none; margin: 0; padding: 0 0 6px; max-height: 220px; overflow: auto; }
.gcp-commits li { display: grid; grid-template-columns: 70px 1fr auto; gap: 10px; align-items: baseline; padding: 4px 12px; font-size: 13px; }
.gcp-commits .sha { color: var(--accent-purple); text-decoration: none; font-size: 12px; }
.gcp-commits .subj { color: var(--text-primary); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.gcp-commits .meta { color: var(--text-muted); font-size: 12px; white-space: nowrap; }

.gcp-toolbar { display: flex; align-items: center; justify-content: space-between; gap: 12px; flex-wrap: wrap; padding: 12px 0; }
.gcp-stats { font-size: 13px; color: var(--text-secondary); display: flex; gap: 8px; align-items: baseline; }
.gcp-stats b { color: var(--text-primary); }
.add { color: var(--status-success); font-family: var(--font-mono, ui-monospace, Menlo, monospace); }
.del { color: var(--status-error); font-family: var(--font-mono, ui-monospace, Menlo, monospace); }
.gcp-tools { display: flex; gap: 8px; align-items: center; }
.gcp-filter {
  width: 180px; padding: 6px 10px; border-radius: 8px; border: 1px solid var(--border-color);
  background: var(--bg-primary); color: var(--text-primary); font: inherit; font-size: 13px;
}
.gcp-filter:focus { outline: none; border-color: var(--accent-purple); }

.gcp-body { display: grid; grid-template-columns: 240px minmax(0, 1fr); gap: 16px; align-items: start; }
.gcp-files {
  position: sticky; top: 0;
  max-height: calc(100vh - 220px); overflow: auto;
  display: flex; flex-direction: column; gap: 1px;
  padding-right: 4px;
}
.gcp-file {
  display: grid; grid-template-columns: 8px 1fr auto; grid-template-rows: auto auto; column-gap: 8px;
  text-align: left; border: 0; background: transparent; border-radius: 7px;
  padding: 5px 8px; cursor: pointer; font: inherit; color: var(--text-primary);
}
.gcp-file:hover { background: var(--card-hover, var(--bg-secondary)); }
.gcp-file.active { background: var(--bg-secondary); box-shadow: inset 2px 0 0 var(--accent-purple); }
.gcp-file .dot { grid-row: 1 / span 2; align-self: center; width: 8px; height: 8px; border-radius: 50%; background: var(--status-warning); }
.gcp-file .dot.added { background: var(--status-success); }
.gcp-file .dot.deleted, .gcp-file .dot.conflicted { background: var(--status-error); }
.gcp-file .dot.renamed, .gcp-file .dot.copied { background: var(--accent-cyan); }
.gcp-file .name { font-size: 13px; font-weight: 550; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.gcp-file .dir { grid-column: 2; font-size: 11px; color: var(--text-muted); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; direction: rtl; text-align: left; }
.gcp-file .n { grid-row: 1 / span 2; grid-column: 3; align-self: center; font-size: 11px; white-space: nowrap; }
.gcp-file.viewed .name { color: var(--text-muted); text-decoration: line-through; text-decoration-color: color-mix(in srgb, var(--text-muted) 50%, transparent); }
.gcp-none { color: var(--text-muted); font-size: 13px; padding: 6px 8px; }
.gcp-diffs { min-width: 0; }

@media (max-width: 1100px) {
  .gcp-body { grid-template-columns: minmax(0, 1fr); }
  .gcp-files { position: static; max-height: 220px; border: 1px solid var(--border-color); border-radius: 10px; padding: 4px; }
}
@media (max-width: 600px) {
  .gcp-filter { width: 120px; }
  .gcp-commits li { grid-template-columns: 60px 1fr; }
  .gcp-commits .meta { display: none; }
}
</style>
