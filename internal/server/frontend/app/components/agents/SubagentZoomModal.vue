<template>
  <Teleport to="body">
    <Transition :css="false" @enter="onEnter" @after-enter="onAfterEnter" @leave="onLeave" @after-leave="onAfterLeave">
      <div v-if="item" class="szm-root">
        <div ref="backdrop" class="szm-backdrop" data-testid="szm-backdrop" @click="close" />
        <section
          ref="dialog"
          class="szm"
          :class="`szm-${item.status}`"
          role="dialog"
          aria-modal="true"
          aria-labelledby="szm-title"
          tabindex="-1"
          @keydown="onKeydown"
        >
          <header class="szm-head">
            <div class="szm-head-clip">
              <Transition :css="false" mode="out-in" @enter="slideIn" @leave="slideOut">
                <div :key="item.id" class="szm-who">
                  <CircusFigure :id="item.id" :status="item.status" :talking="item.status === 'running'" :size="30" class="szm-figure" />
                  <div class="szm-who-text">
                    <h2 id="szm-title" class="szm-title">{{ item.title }}</h2>
                    <p class="szm-meta">
                      <span class="szm-pill"><span class="szm-pill-mark" aria-hidden="true" />{{ STATUS_LABELS[item.status] }}</span>
                      <span v-if="elapsed" class="szm-time" :title="item.status === 'running' ? 'Running for' : 'Took'">{{ elapsed }}</span>
                      <span class="szm-sep" aria-hidden="true">·</span><span>{{ item.type }}</span>
                      <SubagentModelChip :label="item.model" :title="item.modelTitle" :requested="item.modelRequested" />
                      <template v-if="item.background"><span class="szm-sep" aria-hidden="true">·</span><span>background</span></template>
                      <template v-if="item.parentTitle"><span class="szm-sep" aria-hidden="true">·</span><span class="szm-parent" :title="`Launched by ${item.parentTitle}`">↳ {{ item.parentTitle }}</span></template>
                      <template v-if="removed"><span class="szm-sep" aria-hidden="true">·</span><span data-testid="szm-removed">no longer in the list</span></template>
                    </p>
                  </div>
                </div>
              </Transition>
            </div>
            <nav v-if="!removed" class="szm-nav" aria-label="Switch subagent">
              <button type="button" class="szm-btn" data-testid="szm-prev" :disabled="index <= 0" :title="prevTitle" aria-label="Previous subagent" @click="step(-1)">
                <Icon name="mdi:chevron-left" size="18" />
              </button>
              <span class="szm-pos" aria-live="polite">{{ index + 1 }} of {{ items.length }}</span>
              <button type="button" class="szm-btn" data-testid="szm-next" :disabled="index >= items.length - 1" :title="nextTitle" aria-label="Next subagent" @click="step(1)">
                <Icon name="mdi:chevron-right" size="18" />
              </button>
            </nav>
            <div class="szm-actions">
              <button v-if="item.sessionLink" type="button" class="szm-open" data-testid="szm-open-session" title="Open this worker's session" @click="openSession">
                <span class="szm-open-label">Open session</span><span aria-hidden="true">→</span>
              </button>
              <button v-else type="button" class="szm-open" data-testid="szm-open-view" :title="openViewTitle" @click="openView">
                <Icon name="mdi:arrow-expand" size="14" /><span class="szm-open-label">Open in Subagents view</span>
              </button>
              <button ref="closeBtn" type="button" class="szm-btn" data-testid="szm-close" title="Close (Esc)" aria-label="Close" @click="close">
                <Icon name="mdi:close" size="18" />
              </button>
            </div>
          </header>
          <div ref="body" class="szm-body">
            <Transition :css="false" mode="out-in" @enter="slideIn" @leave="slideOut">
              <SubagentDetail :key="item.id" :item="item" :now="now" headless />
            </Transition>
          </div>
        </section>
      </div>
    </Transition>
  </Teleport>
</template>

<script setup lang="ts">
import { computed, nextTick, onUnmounted, ref, watch } from 'vue'
import CircusFigure from '~/components/agents/CircusFigure.vue'
import SubagentDetail from '~/components/agents/SubagentDetail.vue'
import { useClosable, useShortcut, useShortcutLabels } from '~/composables/useKeyboardShortcuts'
import { formatDuration, STATUS_LABELS } from '~/utils/agents/subagentActivity'
import type { SubagentItem } from '~/utils/agents/subagentItem'
import SubagentModelChip from '~/components/agents/SubagentModelChip.vue'
import { isEditableTarget } from '~/utils/shortcuts/keys'
import { playFade, playSlide, playZoom, prefersReducedMotion, usableRect } from '~/utils/rectTransition'

/**
 * One subagent in a modal that zooms out of the row it was opened from (the
 * Subagents PiP) and back into it on close. ‹ › (←/→, J/K, Home/End) step
 * through `items` in order; the content slides in the direction of travel.
 */
const props = defineProps<{
  /** The subagents to step through, in order; the open one is agentId. */
  items: SubagentItem[]
  agentId: string | null
  now: number
  /** The element a subagent's row is drawn in (zoom origin), or null when it is not shown. */
  originFor?: (agentId: string) => HTMLElement | null
}>()

const emit = defineEmits<{
  'update:agentId': [agentId: string | null]
  /** Open the full Subagents view on this agent. */
  'open-view': [agentId: string]
  /** Open a worker's own session (items with sessionLink). */
  'open-session': [sessionId: string]
}>()

const dialog = ref<HTMLElement | null>(null)
const backdrop = ref<HTMLElement | null>(null)
const body = ref<HTMLElement | null>(null)
const closeBtn = ref<HTMLElement | null>(null)

const index = computed(() => props.agentId ? props.items.findIndex(i => i.id === props.agentId) : -1)
// The agent last seen in the list: when it is removed while open, the modal
// stays and shows it as it was (its result), rather than vanishing.
let lastSeen: SubagentItem | null = null
const item = computed<SubagentItem | null>(() => {
  if (!props.agentId) { lastSeen = null; return null }
  if (index.value >= 0) { lastSeen = props.items[index.value]!; return lastSeen }
  return lastSeen && lastSeen.id === props.agentId ? lastSeen : null
})
const removed = computed(() => !!props.agentId && !!item.value && index.value < 0)
const isOpen = computed(() => !!item.value)

const labels = useShortcutLabels()
const prevTitle = labels.title('Previous subagent', 'subagent.prev')
const nextTitle = labels.title('Next subagent', 'subagent.next')
const openViewTitle = labels.title('Open in the Subagents view', 'subagents.toggle')

const elapsed = computed(() => {
  const i = item.value
  if (!i || !Number.isFinite(i.startedAt)) return ''
  const end = i.status === 'running' || !Number.isFinite(i.endedAt) ? props.now || Date.now() : i.endedAt
  return formatDuration(end - i.startedAt)
})

function close() { emit('update:agentId', null) }
function openView() {
  const id = props.agentId
  if (!id) return
  emit('update:agentId', null)
  emit('open-view', id)
}
function openSession() {
  const id = item.value?.sessionLink
  if (!id) return
  emit('update:agentId', null)
  emit('open-session', id)
}

// ── switching ───────────────────────────────────────────────────────────────
// Which way the last switch went: next slides content left (in from the right).
const travel = ref<1 | -1>(1)
function goTo(next: number) {
  const list = props.items
  if (!list.length || index.value < 0) return
  const target = Math.max(0, Math.min(list.length - 1, next))
  if (target === index.value) return
  travel.value = target > index.value ? 1 : -1
  emit('update:agentId', list[target]!.id)
}
function step(by: 1 | -1) { goTo(index.value + by) }
// New agent: start at the top of its detail.
watch(() => item.value?.id, (id, old) => { if (id && old && id !== old) body.value?.scrollTo?.({ top: 0 }) })

const when = () => isOpen.value
useShortcut('subagent.prev', () => step(-1), { scope: 'modal', when })
useShortcut('subagent.next', () => step(1), { scope: 'modal', when })
useShortcut('subagent.first', () => goTo(0), { scope: 'modal', when })
useShortcut('subagent.last', () => goTo(props.items.length - 1), { scope: 'modal', when })
useClosable(isOpen, close, { modal: true })

// J / K (as in the Subagents view's list), never while typing.
const FOCUSABLE = 'a[href], button:not([disabled]), input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])'
function trapTab(event: KeyboardEvent) {
  const root = dialog.value
  if (!root) return
  const nodes = [...root.querySelectorAll<HTMLElement>(FOCUSABLE)].filter(el => !el.hasAttribute('hidden'))
  if (!nodes.length) { event.preventDefault(); root.focus(); return }
  const first = nodes[0]!, last = nodes[nodes.length - 1]!
  const active = document.activeElement
  if (event.shiftKey && (active === first || active === root || !root.contains(active))) { event.preventDefault(); last.focus() }
  else if (!event.shiftKey && (active === last || !root.contains(active))) { event.preventDefault(); first.focus() }
}
function onKeydown(event: KeyboardEvent) {
  if (event.key === 'Tab' && !event.isComposing) { trapTab(event); return }
  if (event.metaKey || event.ctrlKey || event.altKey || event.shiftKey || event.isComposing) return
  if (isEditableTarget(event.target)) return
  const key = event.key.toLowerCase()
  if (key !== 'j' && key !== 'k') return
  event.preventDefault()
  step(key === 'j' ? 1 : -1)
}

// Running animations, cancelled on unmount. start may finish at once (no
// element.animate, or reduced motion); done then runs a microtask later,
// since an out-in <Transition> must not be told "left" while it is patching.
const cancels = new Set<() => void>()
function track(start: (finish: () => void) => () => void, done: () => void = () => {}) {
  let cancel: (() => void) | null = null
  let sync = true
  cancel = start(() => {
    if (cancel) cancels.delete(cancel)
    if (sync) queueMicrotask(done)
    else done()
  })
  sync = false
  cancels.add(cancel)
}

function slideIn(el: Element, done: () => void) {
  track(finish => playSlide(el as HTMLElement, travel.value, 'in', finish), done)
}
function slideOut(el: Element, done: () => void) {
  track(finish => playSlide(el as HTMLElement, travel.value, 'out', finish), done)
}

// ── open / close: zoom out of the row and back into it ─────────────────────
let returnFocus: HTMLElement | null = null
// The agent shown last: the leave animation and focus go back to its row.
let shownId = ''
watch(() => item.value?.id, (id, old) => {
  if (id && !old) {
    const origin = props.originFor?.(id)
    returnFocus = origin || (document.activeElement as HTMLElement | null)
  }
  if (id) shownId = id
}, { immediate: true, flush: 'sync' })

function originRect(): DOMRect | null {
  const el = shownId ? props.originFor?.(shownId) : null
  const rect = el?.getBoundingClientRect?.() ?? null
  return usableRect(rect) ? rect : null
}

function animate(root: Element, phase: 'in' | 'out', done: () => void) {
  const panel = (root as HTMLElement).querySelector<HTMLElement>('.szm')
  const shade = (root as HTMLElement).querySelector<HTMLElement>('.szm-backdrop')
  if (shade) track(finish => playFade(shade, phase, finish))
  if (!panel) { done(); return }
  const rect = prefersReducedMotion() ? null : originRect()
  track(finish => rect ? playZoom(panel, rect, phase, finish) : playFade(panel, phase, finish), done)
}
function onEnter(el: Element, done: () => void) { animate(el, 'in', done) }
function onLeave(el: Element, done: () => void) { animate(el, 'out', done) }
function onAfterEnter() {
  // Focus moves into the modal (unless something inside already has it).
  if (!dialog.value?.contains(document.activeElement)) (closeBtn.value || dialog.value)?.focus({ preventScroll: true })
}
function onAfterLeave() {
  // Back to the row of the agent last shown, else where focus was.
  const row = shownId ? props.originFor?.(shownId) : null
  const target = row && row.isConnected ? row : returnFocus
  if (target?.isConnected) target.focus({ preventScroll: true })
  returnFocus = null
}
// Without a transition run (tests, reduced motion in old browsers) focus still moves.
watch(isOpen, open => { if (open) nextTick(onAfterEnter) })

onUnmounted(() => { for (const cancel of [...cancels]) cancel() })
</script>

<style scoped>
.szm-root {
  position: fixed;
  inset: 0;
  z-index: 10040;
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 24px;
}
.szm-backdrop {
  position: absolute;
  inset: 0;
  background: var(--modal-overlay-bg, rgba(0, 0, 0, 0.45));
}
.szm {
  --szm-accent: var(--text-secondary);
  position: relative;
  display: flex;
  flex-direction: column;
  width: min(760px, 100%);
  max-height: min(86vh, 900px);
  color: var(--text-primary);
  background: var(--bg-primary);
  border: 1px solid var(--border-color);
  border-radius: 14px;
  box-shadow: 0 24px 64px var(--shadow-color, rgba(0, 0, 0, 0.35));
  overflow: hidden;
  /* The zoom scales from the row's top-left corner. */
  transform-origin: 0 0;
  will-change: transform, opacity;
}
.szm:focus { outline: none; }
.szm-running { --szm-accent: var(--status-success); }
.szm-done { --szm-accent: var(--accent-cyan, var(--accent-purple)); }
.szm-failed { --szm-accent: var(--status-error); }
.szm-interrupted { --szm-accent: var(--status-warning); }

.szm-head {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 10px 10px 10px 14px;
  border-bottom: 1px solid var(--border-color);
  background: var(--bg-secondary);
}
.szm-head-clip { flex: 1; min-width: 0; overflow: hidden; }
.szm-who { display: flex; align-items: center; gap: 10px; min-width: 0; }
.szm-figure { flex-shrink: 0; }
.szm-who-text { min-width: 0; }
.szm-title {
  margin: 0;
  font-size: 16px;
  font-weight: 650;
  line-height: 1.3;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.szm-meta {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 2px 6px;
  margin: 2px 0 0;
  font-size: 12px;
  color: var(--text-secondary);
}
.szm-sep { opacity: 0.5; }
.szm-parent { max-width: 28ch; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.szm-pill {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  padding: 0 8px 0 6px;
  border-radius: 999px;
  font-size: 11px;
  font-weight: 700;
  color: var(--szm-accent);
  background: color-mix(in srgb, var(--szm-accent) 14%, transparent);
  border: 1px solid color-mix(in srgb, var(--szm-accent) 38%, transparent);
}
.szm-pill-mark::before { content: '●'; font-size: 8px; }
.szm-done .szm-pill-mark::before { content: '✓'; font-size: 10px; }
.szm-failed .szm-pill-mark::before { content: '✕'; font-size: 10px; }
.szm-interrupted .szm-pill-mark::before { content: '⏸'; font-size: 9px; }
.szm-time { font: 600 12px var(--font-mono, monospace); font-variant-numeric: tabular-nums; color: var(--text-primary); }

.szm-nav { display: flex; align-items: center; gap: 2px; flex-shrink: 0; }
.szm-pos { min-width: 4.5em; text-align: center; font-size: 12px; color: var(--text-secondary); font-variant-numeric: tabular-nums; }
.szm-actions { display: flex; align-items: center; gap: 4px; flex-shrink: 0; }
.szm-btn, .szm-open {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 6px;
  font: inherit;
  color: var(--text-secondary);
  background: transparent;
  border: 0;
  border-radius: 8px;
  cursor: pointer;
}
.szm-btn { width: 30px; height: 30px; }
.szm-open { height: 30px; padding: 0 10px; font-size: 12px; border: 1px solid var(--border-color); }
.szm-btn:hover:not(:disabled), .szm-open:hover { color: var(--text-primary); background: var(--overlay-bg-hover); }
.szm-btn:disabled { opacity: 0.35; cursor: default; }
.szm-btn:focus-visible, .szm-open:focus-visible { outline: 2px solid var(--accent-purple); outline-offset: -2px; }

.szm-body {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  overflow-x: hidden;
  padding: 16px 18px 20px;
}

/* Phones: a full-height sheet. */
@media (max-width: 640px) {
  .szm-root { padding: 0; align-items: stretch; }
  .szm { width: 100%; max-height: none; height: 100%; border-radius: 0; border: 0; }
  .szm-head { flex-wrap: wrap; padding-top: max(10px, env(safe-area-inset-top)); }
  .szm-head-clip { flex-basis: 100%; order: -1; }
  .szm-nav { margin-right: auto; }
  .szm-open-label { display: none; }
  .szm-body { padding: 12px 14px max(16px, env(safe-area-inset-bottom)); }
}
</style>
