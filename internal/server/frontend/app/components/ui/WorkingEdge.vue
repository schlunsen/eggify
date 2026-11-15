<template>
  <Transition name="working-edge">
    <div
      v-if="active"
      ref="edgeEl"
      class="working-edge"
      :class="{ gpu: useGpu, pulse: pulsing }"
      aria-hidden="true"
      data-testid="working-edge"
    >
      <WorkingEdgeShader v-if="useGpu" :colors="colors" @unavailable="gpuFailed = true" />
    </div>
  </Transition>
</template>

<script setup lang="ts">
/**
 * A faint liquid edge around its parent while `active` (the parent needs
 * `position: relative` and a border-radius), in shades of the theme accent.
 * It brightens briefly when `activity` changes, at most every ~10 s
 * (useActivityPulse). WebGPU via the shaders library when available, else CSS.
 */
import { computed, defineAsyncComponent, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useActivityPulse } from '~/composables/useActivityPulse'
import { canUseShaderEdge, edgePalette, readEdgeColors, WORKING_EDGE_ACCENT, WORKING_EDGE_TINT, type EdgeColors } from '~/utils/workingEdge'

const WorkingEdgeShader = defineAsyncComponent(() => import('./WorkingEdgeShader.vue'))

const props = defineProps<{ active: boolean; activity?: string }>()

const { pulsing } = useActivityPulse(() => props.activity, () => props.active)

const edgeEl = ref<HTMLElement | null>(null)
const supported = ref(false)
const gpuFailed = ref(false)
const useGpu = computed(() => supported.value && !gpuFailed.value)
const colors = ref<EdgeColors>(edgePalette(WORKING_EDGE_ACCENT[1], WORKING_EDGE_TINT[1]))

function refreshColors() {
  if (!useGpu.value || !edgeEl.value) return
  colors.value = readEdgeColors(edgeEl.value)
}

onMounted(() => {
  supported.value = canUseShaderEdge()
})

// Follow theme switches while the edge shows (the theme lives on <html>).
let themeObserver: MutationObserver | null = null

function stopThemeObserver() {
  themeObserver?.disconnect()
  themeObserver = null
}

watch([() => props.active, useGpu], async ([on, gpu]) => {
  stopThemeObserver()
  if (!on || !gpu) return
  await nextTick()
  refreshColors()
  themeObserver = new MutationObserver(() => requestAnimationFrame(refreshColors))
  themeObserver.observe(document.documentElement, { attributes: true, attributeFilter: ['data-theme', 'class', 'style'] })
}, { immediate: true })

onBeforeUnmount(stopThemeObserver)
</script>

<style scoped>
@property --working-edge-angle {
  syntax: '<angle>';
  inherits: false;
  initial-value: 0deg;
}

.working-edge {
  position: absolute;
  inset: 0;
  z-index: 1;
  border-radius: inherit;
  padding: var(--working-edge-width, 1.5px);
  pointer-events: none;
  overflow: hidden;
  /* Resting: faint. A pulse brightens it, then it settles slowly. */
  opacity: 0.35;
  transition: opacity 1.6s ease-in-out;
  /* Only the ring between the padding box and the content box shows. */
  -webkit-mask: linear-gradient(#000 0 0) content-box, linear-gradient(#000 0 0);
  -webkit-mask-composite: xor;
  mask: linear-gradient(#000 0 0) content-box exclude, linear-gradient(#000 0 0);
  /* CSS edge: a slowly turning gradient in shades of the accent. */
  background: conic-gradient(
    from var(--working-edge-angle),
    var(--accent-purple, #8b5cf6),
    color-mix(in srgb, var(--accent-purple, #8b5cf6) 55%, white),
    color-mix(in srgb, var(--accent-purple, #8b5cf6) 60%, var(--accent-cyan, #06b6d4)),
    color-mix(in srgb, var(--accent-purple, #8b5cf6) 60%, black),
    var(--accent-purple, #8b5cf6)
  );
  animation: working-edge-turn 14s linear infinite;
}

.working-edge.pulse {
  opacity: 0.85;
  transition-duration: 0.6s;
}

.working-edge.gpu {
  background: none;
  animation: none;
}

@keyframes working-edge-turn {
  to { --working-edge-angle: 360deg; }
}

@media (prefers-reduced-motion: reduce) {
  .working-edge { animation: none; }
}

.working-edge-enter-active,
.working-edge-leave-active {
  transition: opacity 0.4s ease;
}
.working-edge-enter-from,
.working-edge-leave-to {
  opacity: 0 !important;
}
</style>
