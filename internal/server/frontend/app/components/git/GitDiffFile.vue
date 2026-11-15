<template>
  <section class="gdf" :class="{ collapsed, viewed }" :data-path="file.path">
    <header class="gdf-head" @click="$emit('toggle')">
      <span class="gdf-chevron" aria-hidden="true">{{ collapsed ? '▸' : '▾' }}</span>
      <span class="gdf-status" :class="file.status" :title="file.status">{{ statusLetter }}</span>
      <span class="gdf-path" :title="file.path">
        <span class="gdf-dir"><bdi>{{ parts.dir }}</bdi></span><span class="gdf-name">{{ parts.name }}</span>
        <span v-if="file.old_path" class="gdf-old" :title="`Renamed from ${file.old_path}`">← {{ file.old_path }}</span>
      </span>
      <span class="gdf-flags">
        <span v-if="file.untracked" class="gdf-flag">new</span>
        <span v-else-if="file.staged && file.unstaged" class="gdf-flag">partly staged</span>
        <span v-else-if="file.staged" class="gdf-flag staged">staged</span>
        <span v-if="file.binary" class="gdf-flag">binary</span>
      </span>
      <span class="gdf-counts">
        <span v-if="file.additions" class="add">+{{ file.additions }}</span>
        <span v-if="file.deletions" class="del">−{{ file.deletions }}</span>
        <span class="gdf-bar" aria-hidden="true"><i v-for="(c, i) in bar" :key="i" :class="c" /></span>
      </span>
      <label class="gdf-viewed" @click.stop>
        <input type="checkbox" :checked="viewed" @change="$emit('viewed', ($event.target as HTMLInputElement).checked)">
        Viewed
      </label>
    </header>

    <div v-if="!collapsed" class="gdf-body">
      <p v-if="file.binary" class="gdf-note">
        Binary file{{ file.size ? ` · ${formatBytes(file.size)}` : '' }}. Not shown.
      </p>
      <p v-else-if="file.omitted" class="gdf-note">
        <template v-if="loading">Loading diff…</template>
        <template v-else-if="loadError">{{ loadError }} <button class="gdf-link" @click="$emit('load')">Try again</button></template>
        <template v-else>
          {{ file.untracked && file.size ? `Large file (${formatBytes(file.size)}). ` : 'Diff not loaded yet. ' }}
          <button v-if="!(file.untracked && file.size && file.size > 1048576)" class="gdf-link" @click="$emit('load')">Load diff</button>
        </template>
      </p>
      <p v-else-if="!hunks.length" class="gdf-note">{{ file.status === 'added' ? 'Empty file.' : 'No content changes (mode or rename only).' }}</p>
      <div v-else class="gdf-code" :class="{ wrap }" role="table" :aria-label="`Changes in ${file.path}`">
        <template v-for="(h, hi) in hunks" :key="hi">
          <div class="gdf-hunk" role="row">
            <span class="gdf-hunk-range">{{ hunkRange(h) }}</span>
            <span v-if="h.section" class="gdf-hunk-section">{{ h.section }}</span>
          </div>
          <div v-for="(l, li) in h.lines" :key="`${hi}-${li}`" class="gdf-line" :class="l.kind" role="row">
            <span class="ln" aria-hidden="true">{{ l.old ?? '' }}</span>
            <span class="ln" aria-hidden="true">{{ l.new ?? '' }}</span>
            <span class="sign" aria-hidden="true">{{ sign(l.kind) }}</span>
            <span class="txt"><template v-if="l.segments"><span v-for="(s, si) in l.segments" :key="si" :class="{ hl: s.changed }">{{ s.text }}</span></template><template v-else>{{ l.text }}</template></span>
          </div>
        </template>
        <p v-if="file.truncated" class="gdf-note">Diff cut short: the file's changes are too large to show in full.</p>
      </div>
    </div>
  </section>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { parseUnifiedDiff, type DiffHunk, type DiffLineKind } from '~/utils/git/unifiedDiff'
import { splitPath, formatBytes, type GitChangeFile } from '~/utils/git/gitOverview'

const props = defineProps<{
  file: GitChangeFile
  collapsed: boolean
  viewed: boolean
  wrap?: boolean
  loading?: boolean
  loadError?: string
}>()
defineEmits<{ toggle: [], viewed: [value: boolean], load: [] }>()

// Parsed only while expanded: a collapsed 5000-line file costs nothing.
const hunks = computed<DiffHunk[]>(() => (props.collapsed ? [] : parseUnifiedDiff(props.file.diff)))
const parts = computed(() => splitPath(props.file.path))

const statusLetter = computed(() => ({
  added: 'A', modified: 'M', deleted: 'D', renamed: 'R', copied: 'C', typechange: 'T', conflicted: '!',
} as Record<string, string>)[props.file.status] || 'M')

// Five blocks split between additions and deletions, like GitHub's.
const bar = computed(() => {
  const a = props.file.additions, d = props.file.deletions, total = a + d
  if (!total) return ['n', 'n', 'n', 'n', 'n']
  const blocks = Math.min(5, Math.max(1, Math.ceil(Math.log2(total + 1))))
  const adds = Math.round((a / total) * blocks)
  return Array.from({ length: 5 }, (_, i) => (i < adds ? 'a' : i < blocks ? 'd' : 'n'))
})

function sign(kind: DiffLineKind) {
  return kind === 'add' ? '+' : kind === 'del' ? '−' : ''
}

function hunkRange(h: DiffHunk) {
  return h.header.replace(/ @@.*$/, ' @@')
}
</script>

<style scoped>
.gdf {
  border: 1px solid var(--border-color);
  border-radius: 10px;
  background: var(--card-bg);
  overflow: clip;
}
.gdf + .gdf { margin-top: 12px; }
.gdf.viewed .gdf-head { opacity: .72; }

.gdf-head {
  position: sticky;
  top: 0;
  z-index: 2;
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 9px 12px;
  background: var(--bg-secondary);
  border-bottom: 1px solid var(--border-color);
  cursor: pointer;
  user-select: none;
  min-width: 0;
}
.gdf.collapsed .gdf-head { border-bottom-color: transparent; }
.gdf-chevron { width: 12px; color: var(--text-muted); font-size: 12px; }
.gdf-status {
  flex: none;
  width: 18px; height: 18px;
  display: inline-grid; place-items: center;
  border-radius: 4px;
  font: 700 11px/1 var(--font-mono, ui-monospace, Menlo, monospace);
  color: var(--bg-primary);
  background: var(--text-muted);
}
.gdf-status.added { background: var(--status-success); }
.gdf-status.deleted { background: var(--status-error); }
.gdf-status.modified { background: var(--status-warning); }
.gdf-status.renamed, .gdf-status.copied { background: var(--accent-cyan); }
.gdf-status.conflicted { background: var(--status-error); }
.gdf-path {
  flex: 1;
  min-width: 0;
  display: flex;
  align-items: baseline;
  white-space: nowrap;
  font: 13px/1.3 var(--font-mono, ui-monospace, Menlo, monospace);
}
/* The folder gives way first, cut at its start, so the file name stays readable. */
.gdf-dir {
  flex: 0 1 auto;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  direction: rtl;
  text-align: left;
  color: var(--text-muted);
}
.gdf-name { flex: none; color: var(--text-primary); font-weight: 600; }
.gdf-old { flex: 0 1 auto; min-width: 0; overflow: hidden; text-overflow: ellipsis; margin-left: 8px; color: var(--text-muted); font-size: 12px; }
.gdf-flags { display: flex; gap: 4px; flex: none; }
.gdf-flag {
  padding: 1px 6px;
  border-radius: 999px;
  font-size: 11px;
  color: var(--text-secondary);
  border: 1px solid var(--border-color);
}
.gdf-flag.staged { color: var(--status-success); border-color: color-mix(in srgb, var(--status-success) 45%, transparent); }
.gdf-counts {
  flex: none;
  display: flex; align-items: center; gap: 6px;
  font: 600 12px/1 var(--font-mono, ui-monospace, Menlo, monospace);
}
.add { color: var(--status-success); }
.del { color: var(--status-error); }
.gdf-bar { display: inline-flex; gap: 2px; }
.gdf-bar i { width: 7px; height: 7px; border-radius: 1.5px; background: var(--border-color); }
.gdf-bar i.a { background: var(--status-success); }
.gdf-bar i.d { background: var(--status-error); }
.gdf-viewed {
  flex: none;
  display: flex; align-items: center; gap: 5px;
  padding: 3px 8px;
  border: 1px solid var(--border-color);
  border-radius: 6px;
  font-size: 12px;
  color: var(--text-secondary);
  cursor: pointer;
}
.gdf-viewed input { margin: 0; accent-color: var(--accent-purple); }

.gdf-note { margin: 0; padding: 14px 16px; color: var(--text-secondary); font-size: 13px; }
.gdf-link {
  background: none; border: none; padding: 0;
  color: var(--accent-purple); font: inherit; font-weight: 600; cursor: pointer;
}
.gdf-link:hover { text-decoration: underline; }

.gdf-code {
  overflow-x: auto;
  font: 12.5px/1.55 var(--font-mono, ui-monospace, SFMono-Regular, Menlo, monospace);
  background: var(--code-bg, var(--bg-primary));
  tab-size: 4;
}
.gdf-hunk {
  display: flex; gap: 12px;
  padding: 4px 12px 4px 92px;
  color: var(--text-muted);
  background: color-mix(in srgb, var(--accent-cyan) 9%, var(--code-bg, var(--bg-primary)));
  border-block: 1px solid color-mix(in srgb, var(--accent-cyan) 18%, transparent);
  white-space: nowrap;
}
.gdf-hunk-section { color: var(--text-secondary); overflow: hidden; text-overflow: ellipsis; }
.gdf-line {
  display: grid;
  grid-template-columns: 44px 44px 18px 1fr;
  min-width: max-content;
  color: var(--text-primary);
}
.gdf-code.wrap .gdf-line { min-width: 0; }
.ln {
  padding: 0 8px;
  text-align: right;
  color: var(--text-muted);
  opacity: .75;
  user-select: none;
  border-right: 1px solid color-mix(in srgb, var(--border-color) 60%, transparent);
}
.sign { text-align: center; user-select: none; color: var(--text-muted); }
.txt { white-space: pre; padding-right: 16px; }
.gdf-code.wrap .txt { white-space: pre-wrap; word-break: break-word; }
.gdf-line.add { background: color-mix(in srgb, var(--status-success) 13%, transparent); }
.gdf-line.add .sign { color: var(--status-success); }
.gdf-line.del { background: color-mix(in srgb, var(--status-error) 12%, transparent); }
.gdf-line.del .sign { color: var(--status-error); }
.gdf-line.add .hl { background: color-mix(in srgb, var(--status-success) 34%, transparent); border-radius: 2px; }
.gdf-line.del .hl { background: color-mix(in srgb, var(--status-error) 32%, transparent); border-radius: 2px; }
.gdf-line.note .txt { color: var(--text-muted); font-style: italic; }

@media (max-width: 720px) {
  .gdf-flags, .gdf-bar { display: none; }
  .gdf-line { grid-template-columns: 34px 34px 14px 1fr; }
  .gdf-hunk { padding-left: 12px; }
}
</style>
