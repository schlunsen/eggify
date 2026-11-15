<template>
  <Teleport to="body">
    <Transition name="setup-fade">
      <div v-if="wizardOpen" class="setup-overlay" @mousedown.self="close">
        <div
          ref="dialogEl"
          class="setup-dialog"
          role="dialog"
          aria-modal="true"
          aria-labelledby="agent-setup-title"
          aria-describedby="agent-setup-desc"
        >
          <header class="setup-head">
            <div class="setup-head__text">
              <p class="setup-kicker">{{ wizardReason === 'first-run' ? 'Welcome to Eggify' : 'Agents & logins' }}</p>
              <h2 id="agent-setup-title" ref="titleEl" tabindex="-1">Set up your agents</h2>
              <p id="agent-setup-desc" class="setup-desc">{{ description }}</p>
            </div>
            <button type="button" class="setup-close" aria-label="Close" @click="close">
              <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" aria-hidden="true">
                <path d="M18 6 6 18M6 6l12 12" />
              </svg>
            </button>
          </header>

          <div class="setup-body">
            <AgentRuntimeList :autoload="false" @navigate="close" />
          </div>

          <footer class="setup-foot">
            <p class="setup-summary" role="status" aria-live="polite">
              <template v-if="!loaded">Checking your computer…</template>
              <template v-else-if="readyCount === 2">Both agents are ready.</template>
              <template v-else-if="readyCount === 1">{{ readyNames }} is ready. You can add the other one any time.</template>
              <template v-else>No agent is ready yet.</template>
            </p>
            <div class="setup-foot__actions">
              <span class="setup-later">Find this again in Settings → Agents &amp; logins</span>
              <button type="button" class="setup-done" :class="{ 'setup-done--primary': anyReady }" @click="close">
                {{ anyReady ? 'Done' : 'Skip for now' }}
              </button>
            </div>
          </footer>
        </div>
      </div>
    </Transition>
  </Teleport>
</template>

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue'
import AgentRuntimeList from './AgentRuntimeList.vue'
import { useAgentRuntimes } from '~/composables/useAgentRuntimes'

const { wizardOpen, wizardReason, closeWizard, runtimes, anyReady, loaded } = useAgentRuntimes()

const dialogEl = ref<HTMLElement | null>(null)
const titleEl = ref<HTMLElement | null>(null)
let returnFocus: HTMLElement | null = null

const readyList = computed(() => runtimes.value.filter(r => r.ready))
const readyCount = computed(() => readyList.value.length)
const readyNames = computed(() => readyList.value.map(r => r.name).join(' and '))

const description = computed(() => {
  if (wizardReason.value === 'nudge') {
    return 'No agent is ready to run yet. Log in to Claude Code or OpenAI Codex, or add an API key. Your sessions run on whichever one you pick.'
  }
  return 'Eggify runs your agents on Claude Code and OpenAI Codex. Get at least one ready: log in with your subscription, or use an API key.'
})

const close = () => closeWizard()

const focusables = () => Array.from(dialogEl.value?.querySelectorAll<HTMLElement>(
  'a[href], button:not([disabled]), input:not([disabled]), textarea:not([disabled]), select:not([disabled]), [tabindex]:not([tabindex="-1"])',
) || []).filter(el => el.offsetParent !== null || el === document.activeElement)

const onKeydown = (e: KeyboardEvent) => {
  if (e.key === 'Escape') {
    e.stopPropagation()
    close()
    return
  }
  if (e.key !== 'Tab') return
  const els = focusables()
  if (!els.length) return
  const first = els[0]!
  const last = els[els.length - 1]!
  const active = document.activeElement as HTMLElement | null
  if (e.shiftKey && (active === first || active === titleEl.value || !dialogEl.value?.contains(active))) {
    e.preventDefault()
    last.focus()
  } else if (!e.shiftKey && (active === last || !dialogEl.value?.contains(active))) {
    e.preventDefault()
    first.focus()
  }
}

// Listen on the document while open: focus can fall back to <body> (e.g. a
// button that disables itself while re-checking), and Escape / Tab must still
// work then.
const onDocKeydown = (e: KeyboardEvent) => {
  if (wizardOpen.value) onKeydown(e)
}

watch(wizardOpen, async (open) => {
  if (typeof document === 'undefined') return
  if (open) {
    returnFocus = document.activeElement as HTMLElement | null
    document.documentElement.classList.add('setup-open')
    document.addEventListener('keydown', onDocKeydown, true)
    await nextTick()
    titleEl.value?.focus()
  } else {
    document.documentElement.classList.remove('setup-open')
    document.removeEventListener('keydown', onDocKeydown, true)
    returnFocus?.focus?.()
    returnFocus = null
  }
}, { immediate: true })

onBeforeUnmount(() => {
  if (typeof document !== 'undefined') document.removeEventListener('keydown', onDocKeydown, true)
})
</script>

<style scoped>
.setup-overlay {
  position: fixed; inset: 0; z-index: 2000;
  display: flex; align-items: center; justify-content: center;
  padding: 24px 16px;
  background: color-mix(in srgb, var(--bg-primary, #000) 55%, rgba(0, 0, 0, 0.55));
  backdrop-filter: blur(6px);
}
.setup-dialog {
  width: min(880px, 100%); max-height: calc(100vh - 48px);
  display: flex; flex-direction: column;
  background: var(--bg-secondary); color: var(--text-primary);
  border: 1px solid var(--border-color); border-radius: 16px;
  box-shadow: 0 24px 64px var(--shadow-color, rgba(0, 0, 0, 0.35));
  overflow: hidden;
}
.setup-head {
  display: flex; align-items: flex-start; gap: 16px;
  padding: 24px 26px 18px;
  border-bottom: 1px solid var(--border-color);
  background:
    radial-gradient(120% 140% at 0% 0%, rgba(var(--accent-purple-rgb, 138, 108, 255), 0.12), transparent 60%),
    var(--bg-secondary);
}
.setup-head__text { flex: 1; min-width: 0; }
.setup-kicker {
  margin: 0 0 4px; font-size: 0.72rem; font-weight: 700; letter-spacing: 0.12em; text-transform: uppercase;
  color: var(--accent-purple);
}
.setup-head h2 {
  margin: 0; font-size: 1.6rem; line-height: 1.15; letter-spacing: -0.02em;
  font-family: var(--font-serif, inherit);
}
.setup-head h2:focus, .setup-head h2:focus-visible { outline: none; } /* programmatic focus anchor only */
.setup-desc { margin: 8px 0 0; max-width: 62ch; font-size: 0.9rem; line-height: 1.5; color: var(--text-secondary); }
.setup-close {
  display: inline-flex; align-items: center; justify-content: center; flex-shrink: 0;
  width: 34px; height: 34px; border-radius: 8px; border: 1px solid transparent;
  background: transparent; color: var(--text-secondary); cursor: pointer;
}
.setup-close:hover { color: var(--text-primary); border-color: var(--border-color); }
.setup-close:focus-visible { outline: 2px solid var(--accent-purple); outline-offset: 2px; }

.setup-body { padding: 20px 26px; overflow-y: auto; }

.setup-foot {
  display: flex; flex-wrap: wrap; align-items: center; justify-content: space-between; gap: 10px 16px;
  padding: 14px 26px; border-top: 1px solid var(--border-color); background: var(--bg-primary);
}
.setup-summary { margin: 0; font-size: 0.86rem; font-weight: 600; }
.setup-foot__actions { display: flex; align-items: center; gap: 14px; flex-wrap: wrap; }
.setup-later { font-size: 0.76rem; color: var(--text-muted, var(--text-secondary)); }
.setup-done {
  min-height: 36px; padding: 7px 18px; border-radius: 8px; cursor: pointer;
  font: inherit; font-size: 0.86rem; font-weight: 600;
  border: 1px solid var(--border-color); background: transparent; color: var(--text-primary);
}
.setup-done--primary { background: var(--accent-purple); border-color: var(--accent-purple); color: var(--bg-primary, #fff); }
.setup-done:focus-visible { outline: 2px solid var(--accent-purple); outline-offset: 2px; }

.setup-fade-enter-active, .setup-fade-leave-active { transition: opacity 0.18s ease; }
.setup-fade-enter-active .setup-dialog, .setup-fade-leave-active .setup-dialog { transition: transform 0.22s cubic-bezier(0.2, 0.8, 0.2, 1); }
.setup-fade-enter-from, .setup-fade-leave-to { opacity: 0; }
.setup-fade-enter-from .setup-dialog, .setup-fade-leave-to .setup-dialog { transform: translateY(10px) scale(0.98); }

@media (prefers-reduced-motion: reduce) {
  .setup-fade-enter-active, .setup-fade-leave-active,
  .setup-fade-enter-active .setup-dialog, .setup-fade-leave-active .setup-dialog { transition: none; }
}

@media (max-width: 600px) {
  .setup-overlay { padding: 0; align-items: stretch; }
  .setup-dialog { max-height: 100vh; border-radius: 0; }
  .setup-head, .setup-body, .setup-foot { padding-left: 16px; padding-right: 16px; }
  .setup-head h2 { font-size: 1.35rem; }
}
</style>
