<template>
  <div class="ks">
    <div class="ks-toolbar">
      <input
        v-model="query"
        type="search"
        class="input ks-search"
        placeholder="Filter shortcuts"
        aria-label="Filter shortcuts"
        spellcheck="false"
      />
      <button
        type="button"
        class="btn btn--secondary"
        :disabled="!changedCount"
        :title="changedCount ? `Put back the default keys of ${changedCount} shortcut${changedCount === 1 ? '' : 's'}` : 'Every shortcut uses its default keys'"
        @click="resetAll"
      >
        Reset all
      </button>
    </div>

    <p class="ks-hint">
      Click a key to record a new one. Esc cancels. Your keys apply to {{ platformName }} wherever you use Eggify;
      shortcuts you haven't changed keep their defaults.
    </p>

    <section v-for="group in groups" :key="group.name" class="ks-group" :aria-labelledby="`ks-${group.name}`">
      <h3 :id="`ks-${group.name}`" class="ks-title">{{ group.name }}</h3>
      <ul class="ks-rows">
        <li v-for="row in group.rows" :key="row.id" class="ks-row" :class="{ 'ks-row--recording': recordingId === row.id }">
          <div class="ks-text">
            <span class="ks-label">
              {{ row.label }}
              <span v-if="row.changed" class="ks-badge">Changed</span>
            </span>
            <span v-if="row.description" class="ks-desc">{{ row.description }}</span>
            <span v-if="row.changed" class="ks-desc">Default: {{ row.defaultText }}</span>
            <span v-if="row.browserNote" class="ks-note">A browser may keep this key in a tab; the desktop app gets it.</span>
            <span v-if="recordingId === row.id && problem" class="ks-error" role="alert">{{ problem }}</span>
            <span v-if="recordingId === row.id && conflict" class="ks-conflict" role="alert">
              {{ conflict.keysText }} is used by <strong>{{ conflict.labels }}</strong>.
              <button type="button" class="ks-link" @click="confirmConflict">Use it here instead</button>
              <button type="button" class="ks-link" @click="stopRecording">Cancel</button>
            </span>
          </div>

          <div class="ks-actions">
            <template v-if="row.customizable">
              <button
                :ref="el => setKeyButton(row.id, el)"
                type="button"
                class="ks-key"
                :class="{ 'ks-key--none': !row.keysText, 'ks-key--recording': recordingId === row.id }"
                :aria-label="recordingId === row.id ? `Recording keys for ${row.label}. Press the new keys, or Escape to cancel.` : `${row.label}: ${row.keysText || 'no key'}. Change`"
                :aria-pressed="recordingId === row.id"
                @click="toggleRecording(row.id)"
                @blur="onKeyBlur(row.id)"
              >
                <template v-if="recordingId === row.id">Press keys…</template>
                <template v-else-if="row.keysText">{{ row.keysText }}</template>
                <template v-else>No key</template>
              </button>
              <button
                v-if="row.keysText"
                type="button"
                class="ks-icon"
                :title="`Remove the key of ${row.label}`"
                :aria-label="`Remove the key of ${row.label}`"
                @click="removeKey(row.id)"
              >
                <SettingsGlyph name="x" :size="14" />
              </button>
              <button
                v-if="row.changed"
                type="button"
                class="ks-icon"
                :title="`Back to ${row.defaultText}`"
                :aria-label="`Reset ${row.label} to ${row.defaultText}`"
                @click="resetOne(row.id)"
              >
                <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
                  <path d="M3 12a9 9 0 1 0 3-6.7L3 8" /><path d="M3 3v5h5" />
                </svg>
              </button>
            </template>
            <kbd v-else class="ks-key ks-key--fixed" :title="'This key can\'t be changed'">{{ row.keysText }}</kbd>
          </div>
        </li>
      </ul>
    </section>
    <p v-if="!groups.length" class="ks-hint">No shortcut matches “{{ query }}”.</p>
  </div>
</template>

<script setup lang="ts">
import { computed, onUnmounted, ref } from 'vue'
import { SHORTCUTS, SHORTCUT_GROUPS, defaultChord, effectiveChord } from '~/utils/shortcuts/catalog'
import type { ShortcutDef } from '~/utils/shortcuts/catalog'
import { formatChord, resolveChord } from '~/utils/shortcuts/keys'
import type { Chord } from '~/utils/shortcuts/keys'
import {
  browserMayReserve, hasOverrides, overrideFor, overrideProblem, resetShortcutOverrides, setShortcutOverride,
  shortcutOverrides, specFromEvent
} from '~/utils/shortcuts/overrides'
import { shortcutPlatform, shortcutShell } from '~/composables/useKeyboardShortcuts'

/**
 * Settings → Keyboard shortcuts: every shortcut in the catalogue with its
 * keys on this platform. Click a key to record a new one; × removes it, ↺
 * goes back to the default. Taking a key another shortcut uses asks first and
 * then removes it there. Stored by utils/shortcuts/overrides.ts.
 */
const platform = shortcutPlatform()
const shell = shortcutShell()
const platformName = platform === 'mac' ? 'macOS' : 'Windows and Linux'

const query = ref('')
const recordingId = ref<string | null>(null)
const problem = ref('')
const conflict = ref<{ spec: string; ids: string[]; labels: string; keysText: string } | null>(null)

const keyButtons = new Map<string, HTMLElement>()
function setKeyButton(id: string, el: unknown) {
  if (el instanceof HTMLElement) keyButtons.set(id, el)
  else keyButtons.delete(id)
}

const sameChord = (a: Chord | null, b: Chord | null) =>
  !!a && !!b && a.meta === b.meta && a.ctrl === b.ctrl && a.alt === b.alt && a.shift === b.shift && a.key === b.key

/** Does chord `c` (a single key) collide with `other` (which may be the 1–9 range)? */
function collides(c: Chord, other: Chord | null): boolean {
  if (!other) return false
  if (other.key === '1-9') {
    return /^[1-9]$/.test(c.key) && c.meta === other.meta && c.ctrl === other.ctrl && c.alt === other.alt && c.shift === other.shift
  }
  return sameChord(c, other)
}

/**
 * Shortcuts that already answer to `c`. Your keys apply in the desktop app
 * and in browser tabs, whose defaults differ (a tab adds ⌥ to keys the
 * browser keeps), so both are checked.
 */
function shortcutsUsing(c: Chord, exceptId: string): ShortcutDef[] {
  return SHORTCUTS.filter(d => d.id !== exceptId &&
    (['web', 'wails'] as const).some(sh => collides(c, effectiveChord(d, platform, sh))))
}

const changedCount = computed(() => {
  shortcutOverrides() // reactive dependency
  return SHORTCUTS.filter(d => d.customizable !== false && overrideFor(d.id, platform) !== undefined).length
})

const groups = computed(() => {
  shortcutOverrides() // re-render when keys change
  const q = query.value.trim().toLowerCase()
  const rows = SHORTCUTS.map((def) => {
    const chord = effectiveChord(def, platform, shell)
    const keysText = chord ? formatChord(chord, platform) : ''
    const spec = overrideFor(def.id, platform)
    const changed = def.customizable !== false && spec !== undefined
    return {
      id: def.id as string,
      group: def.group,
      label: def.label,
      description: def.id === 'view.fullscreen' && shell === 'wails' ? 'The window (View menu)' : def.description,
      keysText,
      defaultText: formatChord(defaultChord(def, platform, shell), platform),
      changed,
      customizable: def.customizable !== false,
      browserNote: shell === 'web' && changed && !!spec && browserMayReserve(spec)
    }
  }).filter(r => !q || r.label.toLowerCase().includes(q) || (r.description || '').toLowerCase().includes(q) ||
    r.keysText.toLowerCase().includes(q) || r.group.toLowerCase().includes(q))
  return SHORTCUT_GROUPS
    .map(name => ({ name, rows: rows.filter(r => r.group === name) }))
    .filter(g => g.rows.length)
})

// ── recording ───────────────────────────────────────────────────────────────

function toggleRecording(id: string) {
  if (recordingId.value === id) stopRecording()
  else startRecording(id)
}

function startRecording(id: string) {
  stopRecording()
  recordingId.value = id
  window.addEventListener('keydown', onRecordKey, true)
}

function stopRecording() {
  recordingId.value = null
  problem.value = ''
  conflict.value = null
  window.removeEventListener('keydown', onRecordKey, true)
}

function onKeyBlur(id: string) {
  // Leaving the key button ends recording, unless a conflict question is open.
  if (recordingId.value === id && !conflict.value) stopRecording()
}

const PROBLEM_TEXT: Record<string, string> = {
  'needs-modifier': platform === 'mac'
    ? 'Use ⌘, ⌃ or ⌥ with the key (or an F-key), so it doesn\'t type text.'
    : 'Use Ctrl or Alt with the key (or an F-key), so it doesn\'t type text.',
  blocked: 'That key belongs to editing or the system. Pick another.',
  escape: 'Esc only closes things.'
}

function onRecordKey(e: KeyboardEvent) {
  const id = recordingId.value
  if (!id) return
  // Nothing else may act on keys while recording (shortcuts, the menu).
  e.preventDefault()
  e.stopPropagation()
  if (e.isComposing || e.repeat) return
  if (e.key === 'Escape' && !e.metaKey && !e.ctrlKey && !e.altKey && !e.shiftKey) {
    const btn = keyButtons.get(id)
    stopRecording()
    btn?.focus()
    return
  }
  if (e.key === 'Tab' && !e.metaKey && !e.ctrlKey && !e.altKey) {
    stopRecording()
    return
  }
  const spec = specFromEvent(e, platform)
  if (!spec) {
    if (platform === 'other' && e.metaKey) problem.value = 'The Windows key can\'t be part of a shortcut.'
    return
  }
  const p = overrideProblem(spec, platform)
  if (p) {
    problem.value = PROBLEM_TEXT[p.kind]!
    conflict.value = null
    return
  }
  problem.value = ''
  apply(id, spec)
}

function apply(id: string, spec: string, takeFrom: string[] = []) {
  const def = SHORTCUTS.find(d => d.id === id) as ShortcutDef
  const chord = resolveChord(spec, platform)
  if (!takeFrom.length) {
    const users = shortcutsUsing(chord, id)
    const fixed = users.filter(d => d.customizable === false)
    if (fixed.length) {
      problem.value = `${formatChord(chord, platform)} is used by ${fixed.map(d => d.label).join(', ')}, which can't change.`
      conflict.value = null
      return
    }
    if (users.length) {
      conflict.value = {
        spec,
        ids: users.map(d => d.id),
        labels: users.map(d => d.label).join(', '),
        keysText: formatChord(chord, platform)
      }
      return
    }
  }
  // The default keys again: drop the override rather than store a copy.
  const isDefault = sameChord(chord, defaultChord(def, platform, shell))
  setShortcutOverride(id, isDefault ? null : spec, platform, takeFrom)
  const btn = keyButtons.get(id)
  stopRecording()
  btn?.focus()
}

function confirmConflict() {
  const id = recordingId.value
  const c = conflict.value
  if (!id || !c) return
  apply(id, c.spec, c.ids)
}

function removeKey(id: string) {
  stopRecording()
  setShortcutOverride(id, '', platform)
}

function resetOne(id: string) {
  stopRecording()
  const def = SHORTCUTS.find(d => d.id === id)!
  // The default may be in use by another shortcut now; it loses it.
  const users = (['web', 'wails'] as const)
    .flatMap(sh => shortcutsUsing(defaultChord(def, platform, sh), id))
    .filter(d => d.customizable !== false)
  setShortcutOverride(id, null, platform, [...new Set(users.map(d => d.id))])
}

function resetAll() {
  stopRecording()
  if (hasOverrides(platform)) resetShortcutOverrides(platform)
}

onUnmounted(stopRecording)
</script>

<style scoped>
.ks { display: flex; flex-direction: column; gap: 12px; padding: 16px 0 4px; }
.ks-toolbar { display: flex; gap: 10px; align-items: center; flex-wrap: wrap; }
.ks-search { flex: 1 1 220px; min-width: 0; }
.ks-hint { margin: 0; font-size: 0.8rem; line-height: 1.5; color: var(--text-secondary); }

.ks-group { margin-top: 6px; }
.ks-title {
  margin: 0 0 4px;
  font-size: 0.72rem;
  font-weight: 650;
  letter-spacing: 0.08em;
  text-transform: uppercase;
  color: var(--text-secondary);
}
.ks-rows { list-style: none; margin: 0; padding: 0; }
.ks-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  padding: 8px 0;
  border-bottom: 1px solid color-mix(in srgb, var(--border-color) 70%, transparent);
}
.ks-row:last-child { border-bottom: none; }
.ks-text { display: flex; flex-direction: column; gap: 1px; min-width: 0; }
.ks-label { font-size: 0.875rem; color: var(--text-primary); display: inline-flex; align-items: center; gap: 8px; flex-wrap: wrap; }
.ks-badge {
  font-size: 0.68rem;
  font-weight: 600;
  padding: 1px 6px;
  border-radius: 999px;
  color: var(--accent-purple);
  background: color-mix(in srgb, var(--accent-purple) 14%, transparent);
}
.ks-desc, .ks-note { font-size: 0.75rem; color: var(--text-secondary); }
.ks-note { font-style: italic; }
.ks-error, .ks-conflict { font-size: 0.78rem; color: var(--status-error); margin-top: 2px; }
.ks-conflict { color: var(--text-primary); }
.ks-link {
  background: none;
  border: none;
  padding: 0 0 0 6px;
  font: inherit;
  color: var(--accent-purple);
  text-decoration: underline;
  cursor: pointer;
}

.ks-actions { display: flex; align-items: center; gap: 4px; flex-shrink: 0; }
.ks-key {
  min-width: 64px;
  height: 26px;
  padding: 0 10px;
  white-space: nowrap;
  letter-spacing: 0.06em;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  font: 500 0.78rem/1 ui-sans-serif, -apple-system, 'Segoe UI', system-ui, sans-serif;
  color: var(--text-primary);
  background: var(--bg-secondary);
  border: 1px solid var(--border-color);
  border-bottom-width: 2px;
  border-radius: 5px;
  cursor: pointer;
}
.ks-key:hover { border-color: var(--text-secondary); }
.ks-key:focus-visible { outline: 2px solid var(--accent-purple); outline-offset: 2px; }
.ks-key--none { color: var(--text-secondary); font-style: italic; letter-spacing: 0; }
.ks-key--recording {
  letter-spacing: 0;
  border-color: var(--accent-purple);
  box-shadow: 0 0 0 3px color-mix(in srgb, var(--accent-purple) 22%, transparent);
}
.ks-key--fixed { cursor: default; opacity: 0.8; }
.ks-icon {
  width: 26px;
  height: 26px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  border: none;
  border-radius: 5px;
  background: none;
  color: var(--text-secondary);
  cursor: pointer;
}
.ks-icon:hover { color: var(--text-primary); background: var(--bg-secondary); }
.ks-icon:focus-visible { outline: 2px solid var(--accent-purple); outline-offset: 1px; }

@media (max-width: 560px) {
  .ks-row { flex-direction: column; align-items: stretch; gap: 6px; }
  .ks-actions { justify-content: flex-start; }
}
</style>
