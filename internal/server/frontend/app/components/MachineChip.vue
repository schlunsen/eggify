<template>
  <div
    v-if="showChip"
    ref="rootRef"
    class="machine-chip"
    :class="'machine-chip-' + mode"
    :data-mode="mode"
    :aria-hidden="mode === 'hidden' ? 'true' : undefined"
    @keydown="onMenuKey"
  >
    <button
      ref="buttonRef"
      type="button"
      class="machine-chip-button"
      :style="hueStyle(current.hue)"
      :title="title"
      :aria-label="`Showing ${label(current)}. Switch machine`"
      aria-haspopup="menu"
      :aria-expanded="open"
      aria-controls="machine-chip-menu"
      @click="toggle"
    >
      <span class="machine-dot" aria-hidden="true"></span>
      <span class="machine-name">{{ label(current) }}</span>
      <span
        v-if="currentUsage"
        class="machine-meter"
        :class="'usage-' + usageLevel(currentUsage)"
        :title="usageDetail(currentUsage, label(current))"
        data-testid="machine-meter"
      >{{ usageShort(currentUsage) }}</span>
      <span class="machine-caret" aria-hidden="true">⌄</span>
    </button>
    <div v-if="open" id="machine-chip-menu" ref="menuRef" class="machine-menu" role="menu" aria-label="Machines">
      <button
        v-for="m in machines"
        :key="m.id || 'here'"
        type="button"
        class="machine-item"
        role="menuitemradio"
        :aria-checked="m.id === current.id"
        :style="hueStyle(m.hue)"
        @click="choose(m)"
      >
        <span class="machine-check" aria-hidden="true">{{ m.id === current.id ? '✓' : '' }}</span>
        <span class="machine-dot" aria-hidden="true"></span>
        <span class="machine-item-name">{{ label(m) }}</span>
        <span
          v-if="byId[m.id]"
          class="machine-item-meter"
          :class="'usage-' + usageLevel(byId[m.id]!)"
          :title="usageDetail(byId[m.id]!, label(m))"
        >{{ usageShort(byId[m.id]!) }}</span>
        <span v-else-if="!m.here" class="machine-item-meter machine-item-meter-none" title="No reading: that Mac didn't answer or runs an older Eggify">—</span>
      </button>
      <div class="machine-sep" role="separator"></div>
      <button type="button" class="machine-item" role="menuitem" @click="add">
        <span class="machine-check" aria-hidden="true"></span>
        <span class="machine-item-name">Add a Mac from Clipboard…</span>
      </button>
    </div>
  </div>
  <div
    v-else-if="currentUsage"
    class="machine-chip machine-meter-only"
    :class="'usage-' + usageLevel(currentUsage)"
    :title="usageDetail(currentUsage)"
    data-testid="machine-meter"
  >
    <span class="machine-meter">{{ usageShort(currentUsage) }}</span>
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from 'vue'
import { machineChipMode, machineChipRoom, type MachineChipMode } from '~/utils/machineChipFit'
import { useDesktopMachines } from '~/composables/useDesktopMachines'
import { useClosable, useShortcutLabels } from '~/composables/useKeyboardShortcuts'
import { desktopAddMachineFromClipboard, desktopSwitchMachine, type DesktopMachine } from '~/utils/desktop'
import { useMachineUsage } from '~/composables/useMachineUsage'
import { usageDetail, usageLevel, usageShort } from '~/utils/systemUsage'

// Which Mac the desktop window shows (ADR 014), centred in the top bar. Only
// with paired Macs; nothing on the web, in Tauri, or with this Mac alone.

const { machines, current } = useDesktopMachines()
const labels = useShortcutLabels()

const open = ref(false)
const rootRef = ref<HTMLElement | null>(null)
const buttonRef = ref<HTMLButtonElement | null>(null)
const menuRef = ref<HTMLElement | null>(null)

const label = (m: DesktopMachine) => (m.here ? 'This Mac' : m.name)
const showChip = computed(() => !!current.value && machines.value.length > 1)

// CPU and memory next to the name, like a menu-bar meter; every Mac's in the
// menu. With one machine (or on the web) the meter stands alone.
const currentId = computed(() => current.value?.id ?? '')
const { byId, refresh: refreshUsage } = useMachineUsage(currentId)
const currentUsage = computed(() => byId.value[currentId.value] ?? null)
const hueStyle = (hue: number) => ({ '--machine-hue': String(((Math.round(hue) % 360) + 360) % 360) })

const title = computed(() => {
  if (!current.value) return ''
  const prev = labels.label('machine.previous')
  const next = labels.label('machine.switch')
  const keys = [prev, next].filter(Boolean).join(' / ')
  return `Showing ${label(current.value)}` + (keys ? ` · switch machines with ${keys}` : '')
})

function items(): HTMLButtonElement[] {
  return Array.from(menuRef.value?.querySelectorAll<HTMLButtonElement>('.machine-item') ?? [])
}

function close(focusButton = false) {
  open.value = false
  if (focusButton) buttonRef.value?.focus()
}

async function toggle() {
  open.value = !open.value
  if (open.value) {
    void refreshUsage()
    await nextTick()
    const list = items()
    ;(list.find(b => b.getAttribute('aria-checked') === 'true') ?? list[0])?.focus()
  }
}

useClosable(open, () => close(true))

function onMenuKey(e: KeyboardEvent) {
  if (!open.value) return
  const list = items()
  const i = list.indexOf(document.activeElement as HTMLButtonElement)
  let next = -1
  if (e.key === 'ArrowDown') next = (i + 1) % list.length
  else if (e.key === 'ArrowUp') next = (i - 1 + list.length) % list.length
  else if (e.key === 'Home') next = 0
  else if (e.key === 'End') next = list.length - 1
  else if (e.key === 'Tab') { close(); return }
  if (next >= 0) {
    e.preventDefault()
    e.stopPropagation()
    list[next]?.focus()
  }
}

async function choose(m: DesktopMachine) {
  close()
  if (m.id === current.value?.id) return
  try {
    await desktopSwitchMachine(m.id)
  } catch (err) {
    console.warn('Switching machine failed', err)
  }
}

async function add() {
  close()
  try {
    await desktopAddMachineFromClipboard()
  } catch (err) {
    console.warn('Adding a machine failed', err)
  }
}

// ── Fit: the chip sits on the bar's true middle (layouts/default.vue
// .nav-center); when the sides leave too little room there it shows the dot
// alone, then nothing, instead of covering the logo or the controls.
const mode = ref<MachineChipMode>('full')
let fullWidth = 0
let dotWidth = 0
const DOT_FALLBACK = 32

function fit() {
  const root = rootRef.value
  const bar = root?.closest('.navbar-container') as HTMLElement | null
  const left = bar?.querySelector('.nav-left') as HTMLElement | null
  const right = bar?.querySelector('.nav-right') as HTMLElement | null
  const btn = buttonRef.value
  if (!root || !bar || !left || !right || !btn) return
  const barBox = bar.getBoundingClientRect()
  if (barBox.width <= 0) return // not laid out
  const w = btn.getBoundingClientRect().width
  if (mode.value === 'full' && w > 0) fullWidth = w
  else if (mode.value !== 'full' && w > 0) dotWidth = w
  const room = machineChipRoom({
    center: barBox.left + barBox.width / 2,
    leftEnd: left.getBoundingClientRect().right,
    rightStart: right.getBoundingClientRect().left,
  })
  const next = machineChipMode(room, fullWidth, dotWidth || DOT_FALLBACK)
  if (next !== mode.value) {
    if (open.value && next === 'hidden') close()
    mode.value = next
  }
}

let observer: ResizeObserver | null = null
function observe() {
  observer?.disconnect()
  observer = null
  const bar = rootRef.value?.closest('.navbar-container')
  if (!bar) return
  if (typeof ResizeObserver !== 'undefined') {
    observer = new ResizeObserver(() => fit())
    observer.observe(bar)
    for (const el of bar.querySelectorAll('.nav-left, .nav-right')) observer.observe(el)
    // The meter's numbers change its width.
    if (buttonRef.value) observer.observe(buttonRef.value)
  }
  fit()
}

// A new name needs measuring again: start from the full chip.
watch(() => current.value && machines.value.length > 1 ? `${current.value.id}\u0000${current.value.name}` : '', async (key) => {
  fullWidth = 0
  mode.value = 'full'
  if (!key) {
    observer?.disconnect()
    observer = null
    return
  }
  await nextTick()
  observe()
})
onMounted(() => {
  if (typeof window !== 'undefined') window.addEventListener('resize', fit)
})
onUnmounted(() => {
  observer?.disconnect()
  if (typeof window !== 'undefined') window.removeEventListener('resize', fit)
})

defineExpose({ fit })

function onDocPointer(e: PointerEvent) {
  if (rootRef.value && !rootRef.value.contains(e.target as Node)) close()
}
watch(open, (v) => {
  if (typeof document === 'undefined') return
  if (v) document.addEventListener('pointerdown', onDocPointer, true)
  else document.removeEventListener('pointerdown', onDocPointer, true)
})
onUnmounted(() => {
  if (typeof document !== 'undefined') document.removeEventListener('pointerdown', onDocPointer, true)
})
</script>

<style scoped>
.machine-chip {
  position: relative;
  min-width: 0;
  max-width: 100%;
  --wails-draggable: no-drag;
}

.machine-chip-button {
  --machine-color: hsl(var(--machine-hue) 70% 55%);
  display: inline-flex;
  align-items: center;
  gap: 0.45rem;
  max-width: 100%;
  min-width: 0;
  height: 30px;
  padding: 0 0.7rem 0 0.6rem;
  border-radius: 999px;
  border: 1.5px solid color-mix(in srgb, var(--machine-color) 65%, transparent);
  background: var(--bg-primary);
  color: var(--text-primary);
  font: inherit;
  font-size: 0.85rem;
  font-weight: 600;
  cursor: pointer;
  transition: border-color 0.15s ease, background-color 0.15s ease;
}

.machine-chip-button:hover,
.machine-chip-button[aria-expanded='true'] {
  border-color: var(--machine-color);
  background: var(--bg-tertiary, var(--bg-primary));
}

.machine-chip-button:focus-visible,
.machine-item:focus-visible {
  outline: 2px solid var(--machine-color, var(--text-secondary));
  outline-offset: 2px;
}

.machine-dot {
  flex-shrink: 0;
  width: 9px;
  height: 9px;
  border-radius: 50%;
  background: hsl(var(--machine-hue) 70% 55%);
  box-shadow: 0 0 0 2px color-mix(in srgb, hsl(var(--machine-hue) 70% 55%) 25%, transparent);
}

.machine-name {
  min-width: 0;
  max-width: 200px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.machine-meter,
.machine-item-meter {
  flex-shrink: 0;
  font-size: 0.75rem;
  font-weight: 500;
  font-variant-numeric: tabular-nums;
  color: var(--text-secondary);
  white-space: nowrap;
}

.machine-item-meter {
  margin-left: auto;
  padding-left: 0.75rem;
}

.machine-item-meter-none {
  opacity: 0.6;
}

.usage-warn.machine-meter,
.usage-warn.machine-item-meter,
.usage-warn .machine-meter {
  color: var(--warning-color, #d97706);
}

.usage-high.machine-meter,
.usage-high.machine-item-meter,
.usage-high .machine-meter {
  color: var(--error-color, #dc2626);
}

.machine-meter-only {
  display: inline-flex;
  align-items: center;
  height: 30px;
  padding: 0 0.7rem;
  border-radius: 999px;
  border: 1px solid var(--border-color);
  background: var(--bg-primary);
  cursor: default;
}

/* Too narrow for the meter without a chip to fold it into. */
@media (max-width: 900px) {
  .machine-meter-only {
    display: none;
  }
}

.machine-caret {
  flex-shrink: 0;
  color: var(--text-secondary);
  font-size: 0.8rem;
  line-height: 1;
  margin-top: -3px;
}

.machine-menu {
  position: absolute;
  top: calc(100% + 6px);
  left: 50%;
  transform: translateX(-50%);
  z-index: 1100;
  min-width: 260px;
  max-width: min(380px, calc(100vw - 24px));
  padding: 4px;
  border-radius: 10px;
  border: 1px solid var(--border-color);
  background: var(--card-bg, var(--bg-secondary));
  box-shadow: 0 10px 30px var(--shadow-color, rgba(0, 0, 0, 0.25));
}

.machine-item {
  --machine-color: hsl(var(--machine-hue, 0) 70% 55%);
  display: flex;
  align-items: center;
  gap: 0.5rem;
  width: 100%;
  padding: 0.45rem 0.6rem;
  border: 0;
  border-radius: 6px;
  background: transparent;
  color: var(--text-primary);
  font: inherit;
  font-size: 0.85rem;
  text-align: left;
  cursor: pointer;
}

.machine-item:hover,
.machine-item:focus-visible {
  background: var(--bg-tertiary, var(--overlay-bg-hover));
}

.machine-check {
  width: 1em;
  flex-shrink: 0;
  color: var(--text-secondary);
  text-align: center;
}

.machine-item-name {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.machine-sep {
  height: 1px;
  margin: 4px 6px;
  background: var(--border-color);
}

/* Little room on the middle: the dot alone (the tooltip still names the
   machine), then nothing. Hidden keeps the dot's box so it can be measured. */
.machine-chip-dot .machine-name,
.machine-chip-dot .machine-meter,
.machine-chip-hidden .machine-meter,
.machine-chip-dot .machine-caret,
.machine-chip-hidden .machine-name,
.machine-chip-hidden .machine-caret {
  display: none;
}

.machine-chip-dot .machine-chip-button,
.machine-chip-hidden .machine-chip-button {
  padding: 0 0.6rem;
}

.machine-chip-hidden {
  visibility: hidden;
}
</style>
