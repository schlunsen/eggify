<template>
  <div class="git-page">
    <header class="gp-head">
      <NuxtLink :to="`/projects/${projectId}`" class="gp-back" title="Back to the project" aria-label="Back to the project">
        <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><polyline points="15 18 9 12 15 6" /></svg>
      </NuxtLink>
      <div class="gp-title">
        <span class="gp-kicker">{{ project?.name || 'Project' }}</span>
        <h1>Git</h1>
      </div>
      <div v-if="ov" class="gp-facts">
        <span class="gp-chip" title="Current branch of the project folder">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><circle cx="6" cy="5" r="2.5" /><circle cx="6" cy="19" r="2.5" /><circle cx="18" cy="7" r="2.5" /><path d="M6 7.5v9M18 9.5c0 4-6 3-11.5 7" /></svg>
          <span class="mono">{{ ov.current_branch || 'detached' }}</span>
        </span>
        <span class="gp-chip muted">{{ ov.worktrees.length }} {{ ov.worktrees.length === 1 ? 'worktree' : 'worktrees' }}</span>
        <span class="gp-chip muted">{{ ov.branches.length }}{{ ov.branches_capped ? '+' : '' }} branches</span>
        <a v-if="ov.remote_web_url" class="gp-chip link" :href="ov.remote_web_url" target="_blank" rel="noopener" :title="ov.remote">{{ remoteLabel }} ↗</a>
      </div>
      <button class="gp-refresh" :disabled="refreshing" title="Refresh" @click="loadOverview(true)">
        <svg :class="{ spin: refreshing }" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M21 12a9 9 0 1 1-2.64-6.36" /><polyline points="21 3 21 9 15 9" /></svg>
        <span>Refresh</span>
      </button>
    </header>

    <div v-if="loading" class="gp-state"><EggLoader /><p>Reading the repository…</p></div>
    <div v-else-if="error" class="gp-state error"><p>{{ error }}</p><button class="gp-btn" @click="loadOverview()">Try again</button></div>
    <div v-else-if="notRepo" class="gp-state">
      <h2>Not a Git repository</h2>
      <p>{{ project?.path }} isn't under version control yet.</p>
    </div>

    <div v-else-if="ov" class="gp-layout">
      <aside class="gp-rail">
        <section class="gp-section">
          <h2 class="gp-h">Worktrees <span>{{ ov.worktrees.length }}</span></h2>
          <div class="gp-worktrees">
            <article
              v-for="wt in ov.worktrees" :key="wt.path"
              class="wt" :class="{ selected: selection.worktree === wt.path, missing: wt.missing }"
              tabindex="0" role="button" :aria-pressed="selection.worktree === wt.path"
              @click="selectWorktree(wt)" @keydown.enter.prevent="selectWorktree(wt)" @keydown.space.prevent="selectWorktree(wt)"
            >
              <div class="wt-top">
                <span class="wt-branch mono">{{ wt.branch || `@${wt.head.slice(0, 7)}` }}</span>
                <span v-if="wt.is_main" class="tag">main checkout</span>
                <span v-if="wt.locked" class="tag" title="Locked: git won't prune or move it">locked</span>
                <span v-if="wt.missing || wt.prunable" class="tag warn" title="The folder is gone; `git worktree prune` removes it">missing</span>
              </div>
              <div v-if="!wt.is_main" class="wt-path mono" :title="wt.path">{{ displayPath(wt.path, ov.root) }}</div>
              <div v-if="!wt.missing" class="wt-state">
                <span :class="['wt-dirty', { clean: !dirtyTotal(wt.dirty) }]">{{ dirtySummary(wt.dirty) }}</span>
                <span v-if="wt.branch && wt.branch !== ov.default_branch" class="ab" :title="`${wt.base_ahead} ahead of, ${wt.base_behind} behind ${ov.default_branch}`">
                  <span class="up">↑{{ wt.base_ahead }}</span><span class="down">↓{{ wt.base_behind }}</span>
                </span>
                <span v-if="wt.upstream && (wt.ahead || wt.behind)" class="ab push" :title="`vs ${wt.upstream}: ${wt.ahead} to push, ${wt.behind} to pull`">⇡{{ wt.ahead }} ⇣{{ wt.behind }}</span>
              </div>
              <div v-if="wt.last_commit" class="wt-commit">
                <span class="subj">{{ wt.last_commit.subject }}</span>
                <span class="when">{{ relativeTime(unixToIso(wt.last_commit.at)) }}</span>
              </div>
              <ul v-if="sessionsFor(wt.path).length" class="wt-sessions" @click.stop>
                <li v-for="s in sessionsFor(wt.path)" :key="s.id">
                  <NuxtLink :to="`/agents?session=${s.id}`" :title="`Open ${s.name}`">
                    <span class="sdot" :class="s.status" aria-hidden="true" />
                    <span class="sname">{{ s.name }}</span>
                    <span class="sstatus">{{ s.status === 'processing' ? 'working' : s.status }}</span>
                  </NuxtLink>
                </li>
              </ul>
              <div class="wt-actions" @click.stop>
                <NuxtLink v-if="!wt.missing" class="wt-act" :to="newSessionLink(wt.path)">New session here</NuxtLink>
                <button class="wt-act" @click="copy(wt.path)">{{ copied === wt.path ? 'Copied' : 'Copy path' }}</button>
              </div>
            </article>
          </div>
        </section>

        <section class="gp-section">
          <h2 class="gp-h">Branches <span>{{ shownBranches.length }}</span></h2>
          <div class="br-tools">
            <input v-model="branchQuery" type="search" placeholder="Find a branch" aria-label="Find a branch" class="br-search">
            <div class="br-filters" role="group" aria-label="Show branches">
              <button v-for="f in branchFilters" :key="f.id" :class="{ on: branchFilter === f.id }" :aria-pressed="branchFilter === f.id" @click="branchFilter = f.id">{{ f.label }}</button>
            </div>
          </div>
          <ul class="br-list">
            <li v-for="b in shownBranches" :key="b.name">
              <button class="br" :class="{ selected: selection.branch === b.name && (!b.worktree || selection.worktree === b.worktree) }" @click="selectBranch(b)">
                <span class="br-line1">
                  <span class="br-name mono" :title="b.name">{{ b.name }}</span>
                  <span v-if="!b.is_default" class="ab" :title="`${b.base_ahead} ahead of, ${b.base_behind} behind ${ov.default_branch}`">
                    <span class="up">↑{{ b.base_ahead }}</span><span class="down">↓{{ b.base_behind }}</span>
                  </span>
                </span>
                <span class="br-line2">
                  <span v-if="b.current" class="tag accent">current</span>
                  <span v-else-if="b.is_default" class="tag">default</span>
                  <span v-if="b.worktree && !b.current" class="tag" :title="b.worktree">worktree</span>
                  <span v-if="b.merged" class="tag ok">merged</span>
                  <span v-if="b.upstream_gone" class="tag warn" title="Its remote branch was deleted">remote gone</span>
                  <span class="subj" :title="`${b.subject} (${b.author})`">{{ b.subject }}</span>
                  <span class="when">{{ relativeTime(unixToIso(b.at)) }}</span>
                </span>
              </button>
            </li>
            <li v-if="!shownBranches.length" class="br-none">{{ branchQuery ? 'No branch matches.' : branchFilter === 'merged' ? 'No merged branches.' : 'No branches.' }}</li>
          </ul>
        </section>

        <section v-if="ov.recent_commits.length" class="gp-section">
          <h2 class="gp-h">Latest on {{ ov.default_branch }}</h2>
          <ol class="recent">
            <li v-for="c in ov.recent_commits" :key="c.sha">
              <a v-if="ov.remote_web_url" class="mono sha" :href="`${ov.remote_web_url}/commit/${c.sha}`" target="_blank" rel="noopener">{{ c.short }}</a>
              <span v-else class="mono sha">{{ c.short }}</span>
              <span class="subj" :title="c.subject">{{ c.subject }}</span>
              <span class="when">{{ relativeTime(unixToIso(c.at)) }}</span>
            </li>
          </ol>
        </section>
      </aside>

      <main ref="mainEl" class="gp-main">
        <GitChangesPanel
          :project-id="projectId" :overview="ov" :selection="selection" :scope="scope"
          @update:scope="setScope"
        />
      </main>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import GitChangesPanel from '~/components/git/GitChangesPanel.vue'
import EggLoader from '~/components/ui/EggLoader.vue'
import { relativeTime } from '~/utils/projects/projectSetup'
import {
  branchSelection, defaultSelection, dirtySummary, dirtyTotal, displayPath, filterBranches, scopesFor, unixToIso,
  type BranchFilter, type GitBranch, type GitOverview, type GitOverviewResponse, type GitScope, type GitSelection,
  type GitWorktree, type GitWorktreeSession,
} from '~/utils/git/gitOverview'

const route = useRoute()
const router = useRouter()
const { fetchWithAuth } = useAuthenticatedFetch()
const projectId = computed(() => route.params.id as string)

useHead({ title: 'Git' })

/** The overview is re-read this often while the tab is visible, so agents' work shows up. */
const POLL_MS = 10_000

const project = ref<{ id: string, name: string, path: string } | null>(null)
const ov = ref<GitOverview | null>(null)
const sessions = ref<Record<string, GitWorktreeSession[]>>({})
const loading = ref(true)
const refreshing = ref(false)
const error = ref('')
const notRepo = ref(false)
const selection = ref<GitSelection>({})
const scope = ref<GitScope>('uncommitted')
const branchQuery = ref('')
const branchFilter = ref<BranchFilter>('active')
const copied = ref('')
const mainEl = ref<HTMLElement>()

const branchFilters: { id: BranchFilter, label: string }[] = [
  { id: 'active', label: 'Active' }, { id: 'merged', label: 'Merged' }, { id: 'all', label: 'All' },
]
const shownBranches = computed(() => (ov.value ? filterBranches(ov.value.branches, branchFilter.value, branchQuery.value) : []))
const remoteLabel = computed(() => {
  const u = ov.value?.remote_web_url || ''
  return u.replace(/^https:\/\//, '').replace(/^github\.com\//, '')
})

function sessionsFor(path: string) {
  return sessions.value[path] || []
}

function newSessionLink(path: string) {
  const q = new URLSearchParams({ project: projectId.value, new_session: '1' })
  if (project.value && path !== project.value.path) q.set('cwd', path)
  return `/agents?${q}`
}

// ---- selection, kept in the URL so a reload or a shared link opens the same view
function applySelection(sel: GitSelection, sc: GitScope, push = true) {
  const scopes = ov.value ? scopesFor(sel, ov.value.default_branch) : []
  selection.value = sel
  scope.value = scopes.includes(sc) ? sc : (scopes[0] || 'uncommitted')
  if (push) {
    const query: Record<string, string> = { scope: scope.value }
    if (sel.worktree) query.wt = sel.worktree
    if (sel.branch) query.branch = sel.branch
    router.replace({ query })
  }
}

function selectWorktree(wt: GitWorktree) {
  if (wt.missing) return
  const sel = { worktree: wt.path, branch: wt.branch || undefined }
  const sc: GitScope = dirtyTotal(wt.dirty) > 0 || !wt.branch || wt.branch === ov.value?.default_branch || !wt.base_ahead ? 'uncommitted' : 'branch'
  applySelection(sel, sc)
  revealChanges()
}

function selectBranch(b: GitBranch) {
  const sel = branchSelection(b)
  const wt = b.worktree ? ov.value?.worktrees.find(w => w.path === b.worktree) : undefined
  const sc: GitScope = !b.is_default && b.base_ahead > 0 ? 'branch' : (wt && dirtyTotal(wt.dirty) > 0 ? 'uncommitted' : 'branch')
  applySelection(sel, sc)
  revealChanges()
}

function setScope(sc: GitScope) {
  applySelection(selection.value, sc)
}

// On narrow screens the changes sit below the lists.
async function revealChanges() {
  if (window.matchMedia('(max-width: 1100px)').matches) {
    await nextTick()
    mainEl.value?.scrollIntoView({ behavior: 'smooth', block: 'start' })
  }
}

function selectionFromRoute(o: GitOverview): { sel: GitSelection, scope: GitScope } | null {
  const wt = typeof route.query.wt === 'string' ? route.query.wt : ''
  const branch = typeof route.query.branch === 'string' ? route.query.branch : ''
  const sc = route.query.scope === 'branch' ? 'branch' : 'uncommitted'
  if (wt && o.worktrees.some(w => w.path === wt && !w.missing)) {
    return { sel: { worktree: wt, branch: o.worktrees.find(w => w.path === wt)?.branch || undefined }, scope: sc }
  }
  if (branch && o.branches.some(b => b.name === branch)) {
    return { sel: branchSelection(o.branches.find(b => b.name === branch)!), scope: sc }
  }
  return null
}

// ---- data
async function loadProject() {
  try {
    const res = await fetchWithAuth(`/api/projects/${projectId.value}`)
    if (res.ok) {
      project.value = (await res.json()).project
      if (project.value?.name) useHead({ title: `Git · ${project.value.name}` })
    }
  } catch { /* the header falls back to "Project" */ }
}

async function loadOverview(manual = false) {
  if (manual) refreshing.value = true
  try {
    const res = await fetchWithAuth(`/api/projects/${projectId.value}/git/overview`)
    const body = await res.json() as GitOverviewResponse & { error?: string }
    if (!res.ok) throw new Error(body?.error || `HTTP ${res.status}`)
    if (!body.is_repo || !body.overview) {
      notRepo.value = true
      ov.value = null
      return
    }
    notRepo.value = false
    error.value = ''
    const first = !ov.value
    ov.value = body.overview
    sessions.value = body.sessions_by_worktree || {}
    if (first) {
      const pick = selectionFromRoute(body.overview) || defaultSelection(body.overview)
      applySelection(pick.sel, pick.scope, false)
    } else if (selection.value.worktree && !body.overview.worktrees.some(w => w.path === selection.value.worktree)) {
      // The worktree was removed: fall back to where the page would open.
      const pick = defaultSelection(body.overview)
      applySelection(pick.sel, pick.scope)
    }
  } catch (e) {
    if (!ov.value) error.value = e instanceof Error ? e.message : 'Could not read the repository'
  } finally {
    loading.value = false
    refreshing.value = false
  }
}

async function copy(text: string) {
  try {
    await navigator.clipboard.writeText(text)
    copied.value = text
    setTimeout(() => { if (copied.value === text) copied.value = '' }, 1500)
  } catch { /* clipboard unavailable */ }
}

let timer: ReturnType<typeof setInterval> | undefined
function onVisibility() {
  if (document.visibilityState === 'visible') loadOverview()
}
onMounted(() => {
  loadProject()
  loadOverview()
  timer = setInterval(() => { if (document.visibilityState === 'visible') loadOverview() }, POLL_MS)
  document.addEventListener('visibilitychange', onVisibility)
})
onBeforeUnmount(() => {
  clearInterval(timer)
  document.removeEventListener('visibilitychange', onVisibility)
})
</script>

<style scoped>
.git-page { display: flex; flex-direction: column; height: 100vh; background: var(--bg-primary); color: var(--text-primary); overflow: hidden; }
.mono { font-family: var(--font-mono, ui-monospace, SFMono-Regular, Menlo, monospace); }

.gp-head {
  flex: none;
  display: flex; align-items: center; gap: 16px; flex-wrap: wrap;
  padding: 14px 24px;
  border-bottom: 1px solid var(--border-color);
  background: var(--card-bg);
}
.gp-back {
  display: grid; place-items: center; width: 34px; height: 34px; border-radius: 9px;
  border: 1px solid var(--border-color); color: var(--text-secondary);
}
.gp-back:hover { color: var(--accent-purple); border-color: var(--accent-purple); }
.gp-kicker { font-size: 12px; color: var(--text-muted); }
.gp-title h1 { margin: 0; font-size: 20px; line-height: 1.1; }
.gp-facts { display: flex; gap: 8px; flex-wrap: wrap; flex: 1; min-width: 0; }
.gp-chip {
  display: inline-flex; align-items: center; gap: 6px;
  padding: 4px 10px; border-radius: 999px;
  border: 1px solid var(--border-color); background: var(--bg-secondary);
  font-size: 12.5px; color: var(--text-primary); text-decoration: none; white-space: nowrap;
}
.gp-chip.muted { color: var(--text-secondary); }
.gp-chip.link:hover { border-color: var(--accent-purple); color: var(--accent-purple); }
.gp-refresh, .gp-btn {
  display: inline-flex; align-items: center; gap: 6px;
  padding: 7px 12px; border-radius: 9px; border: 1px solid var(--border-color);
  background: var(--bg-secondary); color: var(--text-primary); font: inherit; font-size: 13px; cursor: pointer;
}
.gp-refresh:hover, .gp-btn:hover { border-color: var(--accent-purple); }
.spin { animation: spin 0.9s linear infinite; }
@keyframes spin { to { transform: rotate(360deg); } }
@media (prefers-reduced-motion: reduce) { .spin { animation: none; } }

.gp-state { flex: 1; display: flex; flex-direction: column; align-items: center; justify-content: center; gap: 10px; color: var(--text-secondary); padding: 24px; text-align: center; }
.gp-state.error { color: var(--status-error); }
.gp-state h2 { margin: 0; color: var(--text-primary); }

.gp-layout { flex: 1; min-height: 0; display: grid; grid-template-columns: 380px minmax(0, 1fr); }
.gp-rail { overflow-y: auto; border-right: 1px solid var(--border-color); padding: 16px 16px 40px; display: flex; flex-direction: column; gap: 22px; }
.gp-main { overflow-y: auto; padding: 18px 24px 48px; min-width: 0; }

.gp-h {
  display: flex; align-items: baseline; gap: 8px;
  margin: 0 0 10px; font-size: 12px; font-weight: 650; letter-spacing: .08em; text-transform: uppercase; color: var(--text-secondary);
}
.gp-h span { font-weight: 500; color: var(--text-muted); letter-spacing: 0; }

.tag {
  padding: 1px 7px; border-radius: 999px; font-size: 11px; line-height: 16px;
  color: var(--text-secondary); border: 1px solid var(--border-color); white-space: nowrap;
}
.tag.accent { color: var(--accent-purple); border-color: color-mix(in srgb, var(--accent-purple) 45%, transparent); }
.tag.ok { color: var(--status-success); border-color: color-mix(in srgb, var(--status-success) 40%, transparent); }
.tag.warn { color: var(--status-warning); border-color: color-mix(in srgb, var(--status-warning) 45%, transparent); }
.ab { display: inline-flex; gap: 6px; font: 600 11.5px/1 var(--font-mono, ui-monospace, Menlo, monospace); white-space: nowrap; }
.ab .up { color: var(--status-success); }
.ab .down { color: var(--text-muted); }
.ab.push { color: var(--accent-cyan); }
.when { color: var(--text-muted); font-size: 12px; white-space: nowrap; }
.subj { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; min-width: 0; }

/* worktrees */
.gp-worktrees { display: flex; flex-direction: column; gap: 10px; }
.wt {
  padding: 12px 12px 10px; border-radius: 12px;
  border: 1px solid var(--border-color); background: var(--card-bg);
  cursor: pointer; display: flex; flex-direction: column; gap: 6px;
  transition: border-color .15s, box-shadow .15s;
}
.wt:hover { border-color: color-mix(in srgb, var(--accent-purple) 50%, var(--border-color)); }
.wt:focus-visible { outline: 2px solid var(--accent-purple); outline-offset: 2px; }
.wt.selected { border-color: var(--accent-purple); box-shadow: 0 0 0 1px var(--accent-purple), 0 6px 18px -10px var(--accent-purple); }
.wt.missing { opacity: .6; cursor: default; }
.wt-top { display: flex; align-items: center; gap: 6px; flex-wrap: wrap; }
.wt-branch { font-size: 14px; font-weight: 650; color: var(--text-primary); overflow-wrap: anywhere; }
.wt-path { font-size: 12px; color: var(--text-muted); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.wt-state { display: flex; align-items: center; gap: 10px; flex-wrap: wrap; }
.wt-dirty { font-size: 12.5px; color: var(--status-warning); }
.wt-dirty.clean { color: var(--status-success); }
.wt-commit { display: flex; gap: 8px; align-items: baseline; font-size: 12.5px; color: var(--text-secondary); }
.wt-commit .subj { flex: 1; }
.wt-sessions { list-style: none; margin: 2px 0 0; padding: 6px 0 0; border-top: 1px dashed var(--border-color); display: flex; flex-direction: column; gap: 2px; }
.wt-sessions a { display: flex; align-items: center; gap: 8px; padding: 3px 4px; border-radius: 6px; text-decoration: none; color: var(--text-primary); font-size: 12.5px; }
.wt-sessions a:hover { background: var(--bg-secondary); }
.sdot { width: 8px; height: 8px; border-radius: 50%; background: var(--text-muted); flex: none; }
.sdot.processing { background: var(--status-success); box-shadow: 0 0 0 3px color-mix(in srgb, var(--status-success) 22%, transparent); }
.sdot.idle { background: var(--accent-cyan); }
.sdot.error { background: var(--status-error); }
.sname { flex: 1; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.sstatus { color: var(--text-muted); font-size: 11.5px; }
.wt-actions { display: flex; gap: 6px; margin-top: 2px; }
.wt-act {
  padding: 3px 9px; border-radius: 7px; font-size: 12px;
  border: 1px solid var(--border-color); background: transparent; color: var(--text-secondary);
  text-decoration: none; cursor: pointer; font-family: inherit;
}
.wt-act:hover { color: var(--accent-purple); border-color: var(--accent-purple); }

/* branches */
.br-tools { display: flex; flex-direction: column; gap: 8px; margin-bottom: 8px; }
.br-search {
  width: 100%; box-sizing: border-box; padding: 7px 10px; border-radius: 9px;
  border: 1px solid var(--border-color); background: var(--bg-primary); color: var(--text-primary); font: inherit; font-size: 13px;
}
.br-search:focus { outline: none; border-color: var(--accent-purple); }
.br-filters { display: inline-flex; gap: 4px; }
.br-filters button {
  padding: 4px 10px; border-radius: 999px; border: 1px solid var(--border-color);
  background: transparent; color: var(--text-secondary); font: inherit; font-size: 12px; cursor: pointer;
}
.br-filters button.on { background: var(--accent-purple); border-color: var(--accent-purple); color: var(--bg-primary); }
.br-list { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: 2px; }
.br {
  width: 100%; text-align: left; border: 1px solid transparent; background: transparent;
  border-radius: 9px; padding: 7px 9px; cursor: pointer; font: inherit; color: inherit;
  display: flex; flex-direction: column; gap: 3px;
}
.br:hover { background: var(--bg-secondary); }
.br.selected { background: var(--bg-secondary); border-color: var(--accent-purple); }
.br-line1 { display: flex; align-items: center; gap: 6px; min-width: 0; }
.br-name { font-size: 13px; font-weight: 600; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; min-width: 0; }
.br-line1 .ab { margin-left: auto; }
.br-line2 { display: flex; gap: 6px; align-items: center; font-size: 12px; color: var(--text-secondary); min-width: 0; }
.br-line2 .subj { flex: 1; }
.br-none { color: var(--text-muted); font-size: 13px; padding: 6px 9px; }

/* recent */
.recent { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: 4px; }
.recent li { display: grid; grid-template-columns: 62px minmax(0, 1fr) auto; gap: 8px; align-items: baseline; font-size: 12.5px; }
.recent .sha { color: var(--accent-purple); text-decoration: none; font-size: 12px; }

@media (max-width: 1100px) {
  .git-page { height: auto; min-height: 100vh; overflow: visible; }
  .gp-layout { grid-template-columns: minmax(0, 1fr); }
  .gp-rail { border-right: 0; border-bottom: 1px solid var(--border-color); overflow: visible; }
  .gp-main { overflow: visible; }
}
@media (max-width: 600px) {
  .gp-head { padding: 12px 16px; }
  .gp-refresh span { display: none; }
  .gp-rail, .gp-main { padding-left: 16px; padding-right: 16px; }
}
</style>
