<template>
  <article class="sd" :class="`sd-${item.status}`" :data-agent-id="item.id" :aria-label="`${item.title}, ${statusLabel}`">
    <header v-if="!headless" class="sd-head">
      <div class="sd-meta">
        <span class="sd-pill"><span class="sd-pill-mark" aria-hidden="true" />{{ statusLabel }}</span>
        <span class="sd-time" :title="item.status === 'running' ? 'Running for' : 'Took'">{{ elapsed }}</span>
        <span class="sd-sep" aria-hidden="true">·</span>
        <span>{{ item.type }}</span>
        <SubagentModelChip :label="item.model" :title="item.modelTitle" :requested="item.modelRequested" />
        <template v-if="item.background"><span class="sd-sep" aria-hidden="true">·</span><span>background</span></template>
        <template v-if="item.parentTitle"><span class="sd-sep" aria-hidden="true">·</span><span class="sd-parent" :title="`Launched by ${item.parentTitle}`">↳ {{ item.parentTitle }}</span></template>
      </div>
      <h2 class="sd-title">{{ item.title }}</h2>
    </header>

    <!-- Running: what it is doing right now -->
    <section v-if="item.status === 'running'" class="sd-now" :class="{ 'sd-stale': stale }" aria-label="Now">
      <div class="sd-now-top">
        <span class="sd-label"><span class="sd-live" aria-hidden="true" />Now</span>
        <span class="sd-ago">
          <time v-if="since" class="sd-since" :datetime="since.iso" :title="whenTitle(since, '')">since {{ since.clock }}</time><span v-if="since" aria-hidden="true"> · </span>
          <template v-if="stale">quiet for {{ quietFor }}, may be waiting on a long command</template>
          <template v-else>{{ ago }}</template>
        </span>
      </div>
      <template v-if="item.now.kind === 'tool' && item.latest">
        <div v-if="item.latest.detailKind === 'command'" class="sd-cmd sd-cmd-big"><span class="sd-prompt" aria-hidden="true">$</span><code>{{ item.latest.detail }}</code></div>
        <div v-else class="sd-now-text">{{ item.latest.text }}</div>
        <div v-if="item.latest.cwd" class="sd-cwd">in {{ item.latest.cwd }}</div>
      </template>
      <div v-else class="sd-now-text">{{ item.now.text }}</div>
      <p v-if="item.now.kind === 'thinking' && item.now.full" class="sd-thought">“{{ item.now.full }}”</p>
    </section>

    <!-- Done: the report -->
    <section v-else-if="item.status === 'done'" class="sd-report" aria-label="Result">
      <span class="sd-label">Result</span>
      <div v-if="item.resultHtml" class="sd-md" v-html="showReport && item.reportHtml ? item.reportHtml : item.resultHtml" />
      <p v-else class="sd-muted">Finished without a report.</p>
      <button v-if="item.hasMoreReport" type="button" class="sd-link" :aria-expanded="showReport" @click="showReport = !showReport">{{ showReport ? 'Show less' : 'Show full report' }}</button>
    </section>

    <!-- Failed / interrupted -->
    <section v-else class="sd-problem" :title="item.rawError" :aria-label="item.status === 'interrupted' ? 'Interrupted' : 'Error'">
      <span class="sd-label">{{ item.status === 'interrupted' ? 'Interrupted' : 'Error' }}</span>
      <p>{{ item.problem }}</p>
      <pre v-if="item.rawError && item.rawError !== item.problem" class="sd-raw">{{ item.rawError }}</pre>
    </section>

    <!-- What it was asked to do -->
    <section v-if="item.prompt" class="sd-task" aria-label="Task">
      <span class="sd-label sd-label-quiet">Task</span>
      <p class="sd-task-text" :class="{ 'sd-task-open': showTask }">{{ item.prompt }}</p>
      <button v-if="longTask" type="button" class="sd-link" :aria-expanded="showTask" @click="showTask = !showTask">{{ showTask ? 'Show less' : 'Show the whole task' }}</button>
    </section>

    <!-- What it produced -->
    <section v-if="producedAnything" class="sd-products" aria-label="Produced">
      <span class="sd-label sd-label-quiet">Produced</span>
      <dl class="sd-prod">
        <template v-if="item.products.prs.length">
          <dt>Opened PRs</dt>
          <dd><a v-for="pr in item.products.prs" :key="pr.url" class="sd-tag sd-tag-link" :href="pr.url" target="_blank" rel="noopener noreferrer" :title="pr.url">{{ pr.label }}</a></dd>
        </template>
        <template v-if="item.products.commits.length">
          <dt>Commits</dt>
          <dd class="sd-commits">
            <span v-for="commit in shownCommits" :key="commit.sha" class="sd-commit"><code class="sd-sha">{{ commit.sha.slice(0, 7) }}</code><span class="sd-commit-msg">{{ commit.message }}</span></span>
            <button v-if="item.products.commits.length > COMMITS_SHOWN" type="button" class="sd-link" @click="showAllCommits = !showAllCommits">{{ showAllCommits ? 'Show less' : `${item.products.commits.length - COMMITS_SHOWN} more` }}</button>
          </dd>
        </template>
        <template v-if="item.products.branches.length">
          <dt>Pushed</dt>
          <dd><code v-for="branch in item.products.branches" :key="branch" class="sd-tag">{{ branch }}</code></dd>
        </template>
        <template v-if="item.products.files.length">
          <dt>Files changed</dt>
          <dd class="sd-files">
            <button type="button" class="sd-link" :aria-expanded="showFiles" @click="showFiles = !showFiles">{{ item.products.files.length }} {{ item.products.files.length === 1 ? 'file' : 'files' }}</button>
            <ul v-if="showFiles" class="sd-file-list">
              <li v-for="file in item.products.files" :key="file" class="sd-path"><span class="sd-path-dir">{{ splitPath(file).dir }}</span><span class="sd-path-name">{{ splitPath(file).name }}</span></li>
            </ul>
          </dd>
        </template>
      </dl>
    </section>

    <!-- Key steps (edits, commits, pushes, PRs, failures), or every step -->
    <section class="sd-activity" aria-labelledby="sd-activity-title">
      <div class="sd-activity-head">
        <h3 id="sd-activity-title">
          <template v-if="showAll">All steps <span>{{ item.stepCount }}</span></template>
          <template v-else>Key steps <span>{{ item.keyStepCount }} of {{ item.stepCount }}</span></template>
        </h3>
        <div class="sd-activity-tools">
          <template v-if="showAll">
            <button type="button" class="sd-chip" :aria-pressed="onlyCommands" @click="onlyCommands = !onlyCommands">Shell only</button>
            <button type="button" class="sd-chip" :aria-pressed="allOutput" @click="toggleAllOutput">{{ allOutput ? 'Hide output' : 'Show output' }}</button>
          </template>
          <button v-if="item.timeline.length" type="button" class="sd-chip" :aria-pressed="showAll" @click="showAll = !showAll">{{ showAll ? 'Key steps only' : 'All steps' }}</button>
        </div>
      </div>
      <p v-if="!entries.length" class="sd-muted sd-empty">{{ emptyText }}</p>
      <ol v-else class="sd-steps">
        <li v-for="entry in entries" :key="entry.key" class="sd-step" :class="[`sd-step-${entry.kind}`, { 'sd-step-failed': entry.kind === 'tool' && entry.failed, 'sd-step-current': entry.current }]">
          <template v-if="entry.kind === 'tool'">
            <button
              type="button"
              class="sd-step-row"
              :aria-expanded="entry.result.length ? isOpen(entry.n) : undefined"
              :disabled="!entry.result.length"
              :title="entry.result.length ? (isOpen(entry.n) ? 'Hide output' : 'Show output') : 'No output kept'"
              @click="toggle(entry.n)"
            >
              <span class="sd-step-n">{{ entry.n }}</span>
              <span class="sd-step-tool" :class="{ 'sd-tool-shell': entry.detailKind === 'command' }" :title="entry.tool">{{ entry.label }}</span>
              <span class="sd-step-body">
                <code v-if="entry.detailKind === 'command'" class="sd-cmd"><span class="sd-prompt" aria-hidden="true">$</span>{{ entry.detail }}</code>
                <span v-else-if="entry.detailKind === 'file'" class="sd-path"><span class="sd-path-dir">{{ splitPath(entry.detail).dir }}</span><span class="sd-path-name">{{ splitPath(entry.detail).name }}</span></span>
                <span v-else class="sd-step-text">{{ entry.detail || entry.text }}</span>
                <span v-if="entry.cwd" class="sd-cwd">in {{ entry.cwd }}</span>
              </span>
              <span v-if="entry.when" class="sd-step-when" :title="whenTitle(entry.when, tookOf(entry))">
                <time :datetime="entry.when.iso">{{ entry.when.clock }}</time>
                <span v-if="tookOf(entry)" class="sd-step-took">{{ tookOf(entry) }}</span>
              </span>
              <span v-if="entry.current" class="sd-step-flag sd-flag-now">now</span>
              <span v-else-if="entry.failed" class="sd-step-flag sd-flag-failed">failed</span>
              <span v-if="entry.result.length" class="sd-step-caret" aria-hidden="true">{{ isOpen(entry.n) ? '▾' : '▸' }}</span>
            </button>
            <pre v-if="entry.result.length && isOpen(entry.n)" class="sd-result">{{ entry.result.join('\n') }}</pre>
          </template>
          <template v-else>
            <time v-if="entry.when" class="sd-note-time" :datetime="entry.when.iso" :title="whenTitle(entry.when, '')">{{ entry.when.clock }}</time>
            <p class="sd-note" :class="{ 'sd-note-open': openNotes.has(entry.key), 'sd-note-timed': entry.when }" @click="toggleNote(entry.key)">{{ entry.text }}</p>
          </template>
        </li>
      </ol>
      <p v-if="truncated" class="sd-muted sd-kept">Only the latest output is kept; earlier steps are not shown.</p>
    </section>
  </article>
</template>

<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import type { SubagentItem } from '~/utils/agents/subagentItem'
import SubagentModelChip from '~/components/agents/SubagentModelChip.vue'
import { activityAgo, formatDuration, STALE_AFTER_MS, STATUS_LABELS } from '~/utils/agents/subagentActivity'
import { splitPath, type TimelineNote, type TimelineTool } from '~/utils/agents/subagentTimeline'
import { hasProducts, isKeyStep } from '~/utils/agents/subagentBrief'
import { clockTime, stepDuration, stepTimeTitle } from '~/utils/agents/stepTime'

// headless: the caller shows the title, status and time itself (the subagent modal).
const props = withDefaults(defineProps<{ item: SubagentItem; now?: number; headless?: boolean }>(), { now: 0, headless: false })

const showReport = ref(false)
// Key steps by default; every step (with notes and the shell filters) on demand.
const showAll = ref(false)
const showTask = ref(false)
const showFiles = ref(false)
const showAllCommits = ref(false)
const COMMITS_SHOWN = 4
const onlyCommands = ref(false)
const allOutput = ref(false)
// Steps whose output was toggled away from the allOutput default.
const flipped = reactive(new Set<number>())
const openNotes = reactive(new Set<string>())

// A new agent starts collapsed.
watch(() => props.item.id, () => {
  showReport.value = false
  showTask.value = false
  showFiles.value = false
  showAllCommits.value = false
  flipped.clear()
  openNotes.clear()
})

const statusLabel = computed(() => STATUS_LABELS[props.item.status])
const longTask = computed(() => props.item.prompt.length > 280 || props.item.prompt.split('\n').length > 3)
const producedAnything = computed(() => hasProducts(props.item.products))
const shownCommits = computed(() => showAllCommits.value ? props.item.products.commits : props.item.products.commits.slice(0, COMMITS_SHOWN))
const emptyText = computed(() => {
  if (!props.item.timeline.length) return props.item.status === 'running' ? 'Waiting for the first step…' : 'No steps were kept for this subagent.'
  if (showAll.value) return 'No shell commands.'
  if (!props.item.stepCount) return 'No tool calls yet; its notes are under All steps.'
  return props.item.status === 'running'
    ? 'Nothing changed yet. Edits, commits, pushes and failed commands show up here.'
    : 'No edits, commits, pushes or failed commands.'
})
const clock = computed(() => props.now || Date.now())
const elapsed = computed(() => {
  const start = props.item.startedAt
  const end = props.item.status === 'running' ? clock.value : props.item.endedAt
  if (!Number.isFinite(start)) return ''
  return formatDuration((Number.isFinite(end) ? end : clock.value) - start)
})
const ago = computed(() => activityAgo(props.item.lastActivityAt, clock.value))
const stale = computed(() => props.item.status === 'running' && clock.value - props.item.lastActivityAt > STALE_AFTER_MS)
const quietFor = computed(() => formatDuration(clock.value - props.item.lastActivityAt))

/**
 * When a step happened, for its row: the clock time and how long it took.
 * Entries don't depend on the ticking clock; the tooltip ("3m ago") and the
 * running step's duration are worked out at render time.
 */
interface When { at: number; clock: string; iso: string; took: string }
function whenOf(at: number | undefined, durationMs: number | undefined): When | null {
  if (!at) return null
  return { at, clock: clockTime(at), iso: new Date(at).toISOString(), took: stepDuration(durationMs) }
}
function whenTitle(when: When, took: string): string {
  const title = stepTimeTitle(when.at, clock.value)
  return took ? `${title} · took ${took}` : title
}
// The step under way has run for as long as it has been going.
function tookOf(entry: Entry): string {
  if (!entry.when) return ''
  return entry.current && !entry.when.took ? stepDuration(Math.max(0, clock.value - entry.when.at)) : entry.when.took
}
// The current step's time: "since 14:03:27" in the Now box.
const since = computed<When | null>(() => {
  const latest = props.item.latest
  if (props.item.status !== 'running' || props.item.now.kind !== 'tool' || !latest?.at) return null
  return whenOf(latest.at, undefined)
})

type Entry = (TimelineTool & { key: string; current: boolean; when: When | null }) | (TimelineNote & { key: string; current: false; when: When | null })
const entries = computed<Entry[]>(() => {
  const running = props.item.status === 'running'
  const latest = props.item.latest
  const out: Entry[] = []
  const list = props.item.timeline
  for (let i = list.length - 1; i >= 0; i--) {
    const e = list[i]!
    if (e.kind === 'tool') {
      if (!showAll.value && !isKeyStep(e)) continue
      if (showAll.value && onlyCommands.value && e.detailKind !== 'command') continue
      const current = running && props.item.now.kind === 'tool' && e === latest
      out.push({ ...e, key: `t${e.n}`, current, when: whenOf(e.at, e.durationMs) })
    } else if (showAll.value && !onlyCommands.value) {
      out.push({ ...e, key: `n${i}`, current: false, when: whenOf(e.at, undefined) })
    }
  }
  return out
})
// The server keeps the last 300 lines; the first step of a long run is gone.
const truncated = computed(() => props.item.lines.length >= 300 && props.item.stepCount > 0)

function isOpen(n: number) { return allOutput.value !== flipped.has(n) }
function toggle(n: number) { if (flipped.has(n)) flipped.delete(n); else flipped.add(n) }
function toggleAllOutput() { allOutput.value = !allOutput.value; flipped.clear() }
function toggleNote(key: string) {
  if (window.getSelection?.()?.toString()) return
  if (openNotes.has(key)) openNotes.delete(key); else openNotes.add(key)
}

/** Shows every step with its output (used when Zen asks to show this agent). */
function reveal() { showAll.value = true; allOutput.value = true; flipped.clear() }
defineExpose({ reveal })
</script>

<style scoped>
.sd {
  --sd-accent: var(--text-tertiary, var(--text-secondary));
  display: flex; flex-direction: column; gap: 16px; min-width: 0;
  color: var(--text-primary); font-size: 14px; line-height: 1.5;
}
.sd-running { --sd-accent: var(--status-success); }
.sd-done { --sd-accent: var(--accent-cyan, var(--accent-purple)); }
.sd-failed { --sd-accent: var(--status-error); }

.sd-head { display: flex; flex-direction: column; gap: 6px; }
.sd-meta { display: flex; align-items: center; flex-wrap: wrap; gap: 6px 8px; font-size: 13px; color: var(--text-secondary); min-width: 0; }
.sd-sep { opacity: .5; }
.sd-parent { max-width: 40ch; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.sd-pill {
  display: inline-flex; align-items: center; gap: 6px; padding: 2px 10px 2px 8px; border-radius: 999px;
  font-size: 12px; font-weight: 700; color: var(--sd-accent);
  background: color-mix(in srgb, var(--sd-accent) 14%, transparent);
  border: 1px solid color-mix(in srgb, var(--sd-accent) 38%, transparent);
}
.sd-pill-mark::before { content: '●'; font-size: 9px; }
.sd-done .sd-pill-mark::before { content: '✓'; font-size: 11px; }
.sd-failed .sd-pill-mark::before { content: '✕'; font-size: 11px; }
.sd-interrupted .sd-pill-mark::before { content: '⏸'; font-size: 10px; }
.sd-time { font: 600 13px var(--font-mono, monospace); font-variant-numeric: tabular-nums; color: var(--text-primary); }
.sd-title { margin: 0; font-size: 22px; line-height: 1.25; font-weight: 650; letter-spacing: -.01em; overflow-wrap: anywhere; }

.sd-label { display: inline-flex; align-items: center; gap: 7px; font-size: 11px; font-weight: 750; letter-spacing: .09em; text-transform: uppercase; color: var(--sd-accent); }
.sd-live { width: 8px; height: 8px; border-radius: 50%; background: currentColor; animation: sd-pulse 1.4s ease-in-out infinite; }

.sd-now {
  display: flex; flex-direction: column; gap: 8px; padding: 14px 16px; border-radius: 12px;
  background: color-mix(in srgb, var(--status-success) 8%, var(--bg-secondary));
  border: 1px solid color-mix(in srgb, var(--status-success) 30%, transparent);
}
.sd-now-top { display: flex; align-items: center; justify-content: space-between; gap: 12px; }
.sd-ago { font-size: 12px; color: var(--text-secondary); }
.sd-stale { background: color-mix(in srgb, var(--status-warning) 9%, var(--bg-secondary)); border-color: color-mix(in srgb, var(--status-warning) 40%, transparent); }
.sd-stale .sd-ago { color: var(--status-warning); font-weight: 600; }
.sd-now-text { font-size: 16px; font-weight: 600; overflow-wrap: anywhere; }
.sd-thought { margin: 0; font-size: 13px; font-style: italic; color: var(--text-secondary); }

.sd-cmd { font: 13px/1.5 var(--font-mono, ui-monospace, monospace); color: var(--text-primary); white-space: pre-wrap; overflow-wrap: anywhere; word-break: break-word; }
.sd-cmd-big { display: flex; gap: 10px; padding: 10px 12px; border-radius: 8px; background: var(--code-bg, var(--bg-primary)); font-size: 14px; max-height: 9.5em; overflow: auto; }
.sd-cmd-big code { background: none; padding: 0; font: inherit; white-space: pre-wrap; overflow-wrap: anywhere; }
.sd-prompt { color: var(--status-success); font-weight: 700; margin-right: 8px; user-select: none; }
.sd-cmd-big .sd-prompt { margin-right: 0; }
.sd-cwd { font: 12px var(--font-mono, monospace); color: var(--text-secondary); overflow-wrap: anywhere; }

.sd-report, .sd-problem { display: flex; flex-direction: column; gap: 6px; padding: 14px 16px; border-radius: 12px; background: var(--bg-secondary); border: 1px solid var(--border-color); }
.sd-md { font-size: 14px; line-height: 1.6; overflow-wrap: anywhere; min-width: 0; }
.sd-md :deep(p), .sd-md :deep(ul), .sd-md :deep(ol) { margin: 0 0 .55em; }
.sd-md :deep(:last-child) { margin-bottom: 0; }
.sd-md :deep(ul), .sd-md :deep(ol) { padding-left: 1.3em; }
.sd-md :deep(code) { font-family: var(--font-mono, monospace); font-size: .88em; padding: 1px 4px; border-radius: 4px; background: var(--code-bg); }
.sd-md :deep(pre) { overflow: auto; padding: 10px; border-radius: 8px; background: var(--code-bg); font-size: 12.5px; }
.sd-md :deep(pre code) { padding: 0; background: none; }
.sd-md :deep(a) { color: var(--accent-purple); }
.sd-md :deep(h1), .sd-md :deep(h2), .sd-md :deep(h3), .sd-md :deep(h4) { font-size: 15px; margin: .8em 0 .35em; }
.sd-md :deep(table) { border-collapse: collapse; font-size: 13px; display: block; overflow-x: auto; }
.sd-md :deep(th), .sd-md :deep(td) { border: 1px solid var(--border-color); padding: 3px 8px; }
.sd-problem p { margin: 0; overflow-wrap: anywhere; }
.sd-failed .sd-problem p { color: var(--status-error); }
.sd-raw { margin: 0; padding: 8px 10px; border-radius: 8px; background: var(--code-bg); font: 12px/1.5 var(--font-mono, monospace); color: var(--text-secondary); white-space: pre-wrap; overflow-wrap: anywhere; max-height: 10em; overflow: auto; }

.sd-label-quiet { color: var(--text-secondary); }
.sd-task, .sd-products { display: flex; flex-direction: column; gap: 6px; }
.sd-task-text {
  margin: 0; font-size: 13.5px; line-height: 1.55; color: var(--text-primary); white-space: pre-line; overflow-wrap: anywhere;
  display: -webkit-box; -webkit-line-clamp: 3; line-clamp: 3; -webkit-box-orient: vertical; overflow: hidden;
}
.sd-task-open { display: block; -webkit-line-clamp: unset; line-clamp: unset; }
.sd-prod { display: grid; grid-template-columns: max-content minmax(0, 1fr); gap: 6px 14px; margin: 0; font-size: 13px; }
.sd-prod dt { color: var(--text-secondary); padding-top: 2px; }
.sd-prod dd { margin: 0; display: flex; flex-wrap: wrap; align-items: center; gap: 6px; min-width: 0; }
.sd-tag { font: 12px var(--font-mono, monospace); padding: 2px 8px; border-radius: 6px; border: 1px solid var(--border-color); background: var(--bg-secondary); color: var(--text-primary); overflow-wrap: anywhere; }
.sd-tag-link { color: var(--accent-purple); text-decoration: none; font-weight: 600; }
.sd-tag-link:hover { border-color: var(--accent-purple); }
.sd-commits { flex-direction: column; align-items: flex-start !important; }
.sd-commit { display: flex; gap: 8px; min-width: 0; max-width: 100%; }
.sd-sha { font: 12px var(--font-mono, monospace); color: var(--text-secondary); flex: none; }
.sd-commit-msg { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; min-width: 0; }
.sd-files { flex-direction: column; align-items: flex-start !important; }
.sd-file-list { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: 2px; }

.sd-activity { display: flex; flex-direction: column; gap: 8px; min-width: 0; }
.sd-activity-head { display: flex; align-items: center; justify-content: space-between; gap: 10px; flex-wrap: wrap; position: sticky; top: 0; z-index: 1; padding: 6px 0; background: var(--bg-primary); }
.sd-activity-head h3 { margin: 0; font-size: 12px; font-weight: 750; letter-spacing: .09em; text-transform: uppercase; color: var(--text-secondary); }
.sd-activity-head h3 span { margin-left: 4px; font-weight: 500; letter-spacing: 0; }
.sd-activity-tools { display: flex; gap: 6px; }
.sd-chip { padding: 3px 10px; border-radius: 999px; font-size: 12px; }
.sd-chip[aria-pressed="true"] { border-color: var(--accent-purple); color: var(--accent-purple); background: color-mix(in srgb, var(--accent-purple) 12%, transparent); }
.sd-empty, .sd-kept { margin: 4px 0; }

.sd-steps { list-style: none; margin: 0; padding: 0; border: 1px solid var(--border-color); border-radius: 12px; overflow: hidden; background: var(--bg-secondary); }
.sd-step + .sd-step { border-top: 1px solid color-mix(in srgb, var(--border-color) 70%, transparent); }
.sd-step-row {
  display: grid; grid-template-columns: 2.6em 4.6em minmax(0, 1fr) auto auto auto; align-items: baseline; gap: 10px;
  width: 100%; padding: 7px 12px; border: none; border-radius: 0; background: transparent; text-align: left;
}
.sd-step-row:not([disabled]):hover { background: color-mix(in srgb, var(--accent-purple) 6%, transparent); }
.sd-step-row[disabled] { cursor: default; opacity: 1; }
.sd-step-n { font: 11px var(--font-mono, monospace); color: var(--text-tertiary, var(--text-secondary)); text-align: right; font-variant-numeric: tabular-nums; }
.sd-step-tool { font-size: 11px; font-weight: 700; letter-spacing: .03em; text-transform: uppercase; color: var(--text-secondary); white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
.sd-tool-shell { color: var(--status-success); }
.sd-step-body { display: flex; flex-direction: column; gap: 1px; min-width: 0; }
.sd-step-body .sd-cmd { background: none; padding: 0; border: none; display: -webkit-box; -webkit-line-clamp: 3; line-clamp: 3; -webkit-box-orient: vertical; overflow: hidden; }
.sd-step-text { overflow-wrap: anywhere; color: var(--text-primary); }
.sd-path { font: 13px var(--font-mono, monospace); overflow-wrap: anywhere; }
.sd-path-dir { color: var(--text-secondary); }
.sd-path-name { color: var(--text-primary); font-weight: 600; }
.sd-step-when { display: inline-flex; align-items: baseline; gap: 6px; font: 11px var(--font-mono, monospace); font-variant-numeric: tabular-nums; color: var(--text-tertiary, var(--text-secondary)); white-space: nowrap; }
.sd-step-took { color: var(--text-secondary); }
.sd-since { font-variant-numeric: tabular-nums; }
.sd-step-note { position: relative; }
.sd-note-time { position: absolute; top: 8px; right: 14px; font: 11px var(--font-mono, monospace); font-variant-numeric: tabular-nums; color: var(--text-tertiary, var(--text-secondary)); }
.sd-note-timed { padding-right: 5.5em; }
.sd-step-flag { font-size: 10px; font-weight: 750; text-transform: uppercase; letter-spacing: .06em; padding: 1px 6px; border-radius: 4px; }
.sd-flag-now { color: var(--status-success); background: color-mix(in srgb, var(--status-success) 15%, transparent); }
.sd-flag-failed { color: var(--status-error); background: color-mix(in srgb, var(--status-error) 14%, transparent); }
.sd-step-caret { color: var(--text-secondary); font-size: 11px; width: 1em; }
.sd-step-current { background: color-mix(in srgb, var(--status-success) 7%, transparent); }
.sd-step-failed .sd-step-n { color: var(--status-error); }
.sd-result {
  margin: 0 12px 8px calc(2.6em + 4.6em + 32px); padding: 8px 10px; border-radius: 8px;
  background: var(--code-bg, var(--bg-primary)); color: var(--text-secondary);
  font: 12px/1.5 var(--font-mono, monospace); white-space: pre-wrap; overflow-wrap: anywhere; max-height: 18em; overflow: auto;
}
.sd-step-failed .sd-result { color: color-mix(in srgb, var(--status-error) 70%, var(--text-primary)); }
.sd-note {
  margin: 0; padding: 7px 14px 7px calc(2.6em + 4.6em + 32px); font-size: 13px; font-style: italic; color: var(--text-secondary); cursor: pointer;
  display: -webkit-box; -webkit-line-clamp: 2; line-clamp: 2; -webkit-box-orient: vertical; overflow: hidden; white-space: pre-line;
}
.sd-note-open { -webkit-line-clamp: unset; line-clamp: unset; display: block; }

button { color: inherit; cursor: pointer; border: 1px solid var(--border-color); border-radius: 8px; background: transparent; font: inherit; }
button:focus-visible { outline: 2px solid var(--accent-purple); outline-offset: -2px; }
.sd-link { align-self: flex-start; border: none; padding: 2px 0; font-size: 13px; color: var(--accent-purple); }
.sd-link:hover { text-decoration: underline; }
.sd-muted { color: var(--text-secondary); font-size: 13px; }

@media (max-width: 640px) {
  .sd-title { font-size: 19px; }
  .sd-prod { grid-template-columns: minmax(0, 1fr); gap: 2px; }
  .sd-prod dd { margin-bottom: 6px; }
  .sd-step-row { grid-template-columns: 4.2em minmax(0, 1fr) auto auto auto; padding: 8px 10px; }
  .sd-step-took { display: none; }
  .sd-step-n { display: none; }
  .sd-result, .sd-note { margin-left: 10px; padding-left: 10px; }
  .sd-note { margin-left: 0; }
}
@keyframes sd-pulse { 50% { opacity: .35; } }
@media (prefers-reduced-motion: reduce) { .sd-live { animation: none; } }
</style>
