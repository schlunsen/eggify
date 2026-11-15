<template>
  <div
    ref="plate"
    class="zen-plate"
    :class="{ 'zen-plate-ghost': ghost, 'zen-plate-dark': dark }"
    data-testid="zen-plate"
    role="group"
    aria-label="Current session"
  >
    <div class="zen-plate-name" data-testid="zen-plate-name" :title="name">{{ shownName }}</div>
    <p v-if="shownSummary" class="zen-plate-summary" data-testid="zen-plate-summary">{{ shownSummary }}</p>
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { plateName, plateSummary } from '~/utils/zenPlate'
import { getCircusScene } from '~/composables/agents/zen/sceneCircus'

/**
 * The session's title plate on the Zen stage: a theatre-programme card with the
 * session name and the AI summary (when one exists). It sways a little on the downbeat while music plays.
 */
const emit = defineEmits<{
  /** The plate's rendered height in px (0 on unmount), so the HUD column can sit below it */
  height: [px: number]
}>()

const props = defineProps<{
  name: string
  summary?: string
  /** Fade the plate out (typing, dictating) */
  ghost?: boolean
  /** The backdrop is a dark one */
  dark?: boolean
}>()

const shownName = computed(() => plateName(props.name))
const shownSummary = computed(() => plateSummary(props.summary || ''))

const plate = ref<HTMLElement | null>(null)
let unfollow: (() => void) | null = null
let observer: ResizeObserver | null = null
let lastHeight = -1
function reportHeight() {
  const h = Math.ceil(plate.value?.offsetHeight ?? 0)
  if (h !== lastHeight) { lastHeight = h; emit('height', h) }
}
onMounted(() => {
  try { if (plate.value) unfollow = getCircusScene().followDownbeat(plate.value) } catch { /* no scene */ }
  reportHeight()
  if (plate.value && typeof ResizeObserver !== 'undefined') {
    observer = new ResizeObserver(reportHeight)
    observer.observe(plate.value)
  }
})
onBeforeUnmount(() => {
  unfollow?.(); unfollow = null
  observer?.disconnect(); observer = null
  emit('height', 0)
})
</script>

<style scoped>
.zen-plate {
  position: absolute;
  /* Set on .zen-mode, which also reserves this much room above the status chips */
  top: var(--zen-plate-top, 58px);
  left: 50%;
  z-index: 2;
  width: min(520px, calc(100% - 520px));
  min-width: 220px;
  padding: 8px 18px 9px;
  text-align: center;
  color: #2b1d10;
  background: #fbf5e6;
  border: 3px double #2b1d10;
  border-radius: 4px;
  box-shadow: 3px 4px 0 rgba(43, 29, 16, 0.35);
  pointer-events: none;
  transform-origin: 50% 0;
  transform: translateX(-50%) rotate(calc(var(--cc-accent, 0) * 0.9deg));
  transition: opacity 0.25s ease;
}
.zen-plate-ghost { opacity: 0.12; }
.zen-plate-name {
  font: 700 clamp(1.05rem, 2vw, 1.5rem) / 1.15 'Rye', 'EB Garamond', Georgia, serif;
  letter-spacing: 0.01em;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.zen-plate-summary {
  margin: 6px 0 0;
  font: 500 clamp(1.05rem, 1.6vw, 1.25rem) / 1.35 'EB Garamond', Georgia, serif;
  color: #1c130a;
  display: -webkit-box;
  -webkit-line-clamp: 3;
  line-clamp: 3;
  -webkit-box-orient: vertical;
  overflow: hidden;
}
@media (max-width: 900px) {
  .zen-plate { width: min(360px, calc(100% - 28px)); }
}
@media (prefers-reduced-motion: reduce) {
  .zen-plate { transform: translateX(-50%); transition: none; }
}
</style>
