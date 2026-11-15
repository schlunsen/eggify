<template>
  <Teleport to="body">
    <Transition name="sheet">
      <div v-if="open" class="sheet-overlay" @click="closeSheet">
        <div
          ref="panel"
          class="sheet"
          role="dialog"
          aria-modal="true"
          aria-labelledby="shortcuts-sheet-title"
          tabindex="-1"
          @click.stop
        >
          <header class="sheet-header">
            <h2 id="shortcuts-sheet-title">Keyboard shortcuts</h2>
            <span class="sheet-hint">
              Everything else is in the command palette <kbd>{{ keys.label('palette.open') }}</kbd>
            </span>
            <NuxtLink to="/settings#shortcuts" class="sheet-customize" @click="openShortcutSettings">Change keys</NuxtLink>
            <button class="sheet-close" type="button" aria-label="Close" title="Close (Esc)" @click="closeSheet">
              <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
                <line x1="18" y1="6" x2="6" y2="18" /><line x1="6" y1="6" x2="18" y2="18" />
              </svg>
            </button>
          </header>
          <div class="sheet-body">
            <ShortcutsList compact />
          </div>
        </div>
      </div>
    </Transition>
  </Teleport>
</template>

<script setup lang="ts">
import { ref, watch, nextTick } from 'vue'
import ShortcutsList from './ShortcutsList.vue'
import { useShortcutsSheet, useClosable, useShortcut, useShortcutLabels } from '~/composables/useKeyboardShortcuts'

/** The shortcuts sheet (⌘/), generated from the shortcut registry. */
const { open, closeSheet } = useShortcutsSheet()
const keys = useShortcutLabels()
const panel = ref<HTMLElement | null>(null)
const route = useRoute()

/**
 * The Settings page moves its hash with replaceState as you scroll, which the
 * router doesn't see, so a second "Change keys" there would be a no-op: scroll
 * to the section directly when the page is already open.
 */
function openShortcutSettings() {
  closeSheet()
  if (route.path === '/settings') {
    document.getElementById('shortcuts')?.scrollIntoView({ behavior: 'smooth', block: 'start' })
  }
}

useClosable(open, closeSheet, { modal: true })
// ⌘/ again closes it (only modal-scope handlers run while it is open)
useShortcut('help.shortcuts', closeSheet, { scope: 'modal', when: () => open.value })

let previousFocus: Element | null = null
watch(open, async (isOpen) => {
  if (isOpen) {
    previousFocus = document.activeElement
    await nextTick()
    panel.value?.focus()
  } else if (previousFocus instanceof HTMLElement && previousFocus.isConnected) {
    previousFocus.focus()
  }
})
</script>

<style scoped>
.sheet-overlay {
  position: fixed;
  inset: 0;
  z-index: 9999;
  display: flex;
  align-items: flex-start;
  justify-content: center;
  padding: 10vh 16px 16px;
  background: rgba(10, 10, 20, 0.45);
  backdrop-filter: blur(3px);
}

.sheet {
  width: min(820px, 100%);
  max-height: 80vh;
  display: flex;
  flex-direction: column;
  overflow: hidden;
  background: var(--card-bg);
  border: 1px solid var(--border-color);
  border-radius: 14px;
  box-shadow: 0 24px 70px rgba(0, 0, 0, 0.35);
  outline: none;
}

.sheet-header {
  display: flex;
  align-items: center;
  gap: 16px;
  padding: 18px 22px 14px;
  border-bottom: 1px solid var(--border-color);
}
.sheet-header h2 {
  margin: 0;
  font-size: 1.05rem;
  font-weight: 650;
  color: var(--text-primary);
}
.sheet-customize {
  font-size: 0.8rem;
  color: var(--text-secondary);
  text-decoration: underline;
  white-space: nowrap;
}
.sheet-customize:hover { color: var(--text-primary); }
.sheet-hint {
  margin-left: auto;
  font-size: 0.78rem;
  color: var(--text-secondary);
  display: inline-flex;
  align-items: center;
  gap: 6px;
}
.sheet-hint kbd {
  font: 500 0.72rem/1 ui-sans-serif, -apple-system, 'Segoe UI', system-ui, sans-serif;
  padding: 3px 6px;
  border: 1px solid var(--border-color);
  border-bottom-width: 2px;
  border-radius: 5px;
  background: var(--bg-secondary);
  color: var(--text-primary);
}
.sheet-close {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 30px;
  height: 30px;
  border: none;
  border-radius: 7px;
  background: transparent;
  color: var(--text-secondary);
  cursor: pointer;
}
.sheet-close:hover { background: var(--bg-secondary); color: var(--text-primary); }

.sheet-body {
  padding: 18px 22px 22px;
  overflow-y: auto;
}

.sheet-enter-active, .sheet-leave-active { transition: opacity 0.15s ease; }
.sheet-enter-active .sheet, .sheet-leave-active .sheet { transition: transform 0.15s ease; }
.sheet-enter-from, .sheet-leave-to { opacity: 0; }
.sheet-enter-from .sheet, .sheet-leave-to .sheet { transform: translateY(-6px) scale(0.99); }

@media (max-width: 640px) {
  .sheet-overlay { padding-top: 16px; }
  .sheet-hint { display: none; }
  .sheet-close { margin-left: auto; }
}
@media (prefers-reduced-motion: reduce) {
  .sheet-enter-active, .sheet-leave-active, .sheet-enter-active .sheet, .sheet-leave-active .sheet { transition: none; }
}
</style>
