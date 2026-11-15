<template>
  <div class="shortcuts-list" :class="{ compact }">
    <section v-for="group in groups" :key="group.name" class="sl-group" :aria-labelledby="`sl-${group.name}`">
      <h3 :id="`sl-${group.name}`" class="sl-title">{{ group.name }}</h3>
      <ul class="sl-rows">
        <li v-for="row in group.rows" :key="row.id" class="sl-row">
          <span class="sl-text">
            <span class="sl-label">{{ row.label }}</span>
            <span v-if="row.description" class="sl-desc">{{ row.description }}</span>
          </span>
          <kbd class="sl-key" :aria-label="row.keysText">{{ row.keysText }}</kbd>
        </li>
      </ul>
    </section>
    <p v-if="note" class="sl-note">{{ note }}</p>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { SHORTCUTS, SHORTCUT_GROUPS, effectiveChord } from '~/utils/shortcuts/catalog'
import { chordParts, formatChord } from '~/utils/shortcuts/keys'
import { shortcutPlatform, shortcutShell } from '~/composables/useKeyboardShortcuts'
import { shortcutOverrides } from '~/utils/shortcuts/overrides'

/**
 * The keyboard shortcuts, generated from the registry's catalogue with the
 * keys that apply on this platform and in this shell (desktop app or
 * browser). Used by the shortcuts sheet (⌘/) and the Help page.
 */
withDefaults(defineProps<{ compact?: boolean }>(), { compact: false })

const platform = shortcutPlatform()
const shell = shortcutShell()

const groups = computed(() => {
  shortcutOverrides() // re-render when the user changes keys
  const rows = SHORTCUTS.map((def) => {
    const chord = effectiveChord(def, platform, shell)
    if (!chord) return null // the user removed its key
    // In the desktop app ⌃⌘F is the window's own full screen (View menu)
    const description = def.id === 'view.fullscreen' && shell === 'wails' ? 'The window (View menu)' : def.description
    return {
      id: def.id as string,
      group: def.group,
      label: def.label,
      description,
      parts: chordParts(chord, platform),
      keysText: formatChord(chord, platform)
    }
  }).filter(r => r !== null)
  rows.push({
    id: 'escape', group: 'General', label: 'Close', description: 'The topmost dialog, panel or menu',
    parts: ['Esc'], keysText: 'Esc'
  })
  return SHORTCUT_GROUPS
    .map(name => ({ name, rows: rows.filter(r => r.group === name) }))
    .filter(g => g.rows.length)
})

const note = computed(() => {
  if (shell !== 'web') return ''
  const alt = platform === 'mac' ? '⌥' : 'Alt'
  return `In a browser tab, shortcuts the browser keeps for itself (new window, private window, reopen tab, tab 1–9) add ${alt}. The Eggify desktop app uses the plain keys.`
})
</script>

<style scoped>
.shortcuts-list {
  columns: 2 300px;
  column-gap: 40px;
}
.shortcuts-list.compact { column-gap: 32px; }

.sl-group { min-width: 0; break-inside: avoid; margin-bottom: 20px; }
.sl-title {
  margin: 0 0 6px;
  font-size: 0.72rem;
  font-weight: 650;
  letter-spacing: 0.08em;
  text-transform: uppercase;
  color: var(--text-secondary);
}
.sl-rows { list-style: none; margin: 0; padding: 0; }
.sl-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  padding: 7px 0;
  border-bottom: 1px solid color-mix(in srgb, var(--border-color) 70%, transparent);
}
.sl-row:last-child { border-bottom: none; }
.sl-text { display: flex; flex-direction: column; min-width: 0; }
.sl-label { font-size: 0.875rem; color: var(--text-primary); }
.sl-desc { font-size: 0.75rem; color: var(--text-secondary); margin-top: 1px; }
.sl-key {
  flex-shrink: 0;
  min-width: 26px;
  height: 22px;
  padding: 0 7px;
  letter-spacing: 0.06em;
  white-space: nowrap;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  font: 500 0.75rem/1 ui-sans-serif, -apple-system, 'Segoe UI', system-ui, sans-serif;
  color: var(--text-primary);
  background: var(--bg-secondary);
  border: 1px solid var(--border-color);
  border-bottom-width: 2px;
  border-radius: 5px;
}
.sl-note {
  column-span: all;
  margin: 4px 0 0;
  font-size: 0.75rem;
  color: var(--text-secondary);
}
</style>
