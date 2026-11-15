<template>
  <span
    class="egg-loader"
    :class="[compact ? 'is-compact' : 'is-stage', `kind-${kind}`]"
    :style="{ '--egg-h': `${height}px`, '--egg-w': `${height * EGG_ASPECT[kind]}px` }"
    aria-hidden="true"
    data-test="egg-loader"
  >
    <span class="walker">
      <span class="flip">
        <span class="hop">
          <img class="egg" :src="circusEggArt(kind)" alt="" draggable="false">
        </span>
      </span>
      <span v-if="!compact" class="shadow" />
    </span>
    <span v-if="!compact" class="board" />
  </span>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { CIRCUS_KINDS, circusEggArt, EGG_ASPECT, type CircusKind } from '~/utils/agents/circusCharacters'

/**
 * Eggify's loader: one of the engraved Circus eggs in costume (the same art as
 * the subagents and Zen stage). The stage variant strolls along a paper board
 * in stop-motion steps; the compact one only hops in place, for inline
 * "working" indicators. Stands still with prefers-reduced-motion.
 */
const props = withDefaults(defineProps<{
  /** A costume; random (picked once per mount) when omitted. */
  kind?: CircusKind
  /** Inline variant: a small egg hopping in place, no board. */
  compact?: boolean
  /** Egg height in px (default 88 on the stage, 22 compact). */
  size?: number
}>(), { compact: false })

const picked = ref<CircusKind>(CIRCUS_KINDS[Math.floor(Math.random() * CIRCUS_KINDS.length)] ?? 'gent')
const kind = computed<CircusKind>(() => props.kind ?? picked.value)
const height = computed(() => props.size ?? (props.compact ? 22 : 88))
</script>

<style scoped>
.egg-loader {
  position: relative;
  display: inline-block;
  pointer-events: none;
  user-select: none;
}
.is-stage {
  /* A block of its own, so a caption below it starts on a new line. */
  display: block;
  margin-inline: auto;
  width: calc(var(--egg-h) * 2.8);
  height: calc(var(--egg-h) + 10px);
}
.is-compact {
  width: var(--egg-w);
  height: var(--egg-h);
  vertical-align: middle;
}

.walker {
  position: absolute;
  left: 50%;
  bottom: 6px;
  width: var(--egg-w);
  height: var(--egg-h);
  margin-left: calc(var(--egg-w) / -2);
}
.is-compact .walker { position: static; margin: 0; display: block; }

.flip, .hop { display: block; width: 100%; height: 100%; }
.hop { transform-origin: 50% 100%; }
.egg { display: block; width: 100%; height: 100%; object-fit: contain; }

/* A paper strip for the egg to stroll on, and its shadow. */
.board {
  position: absolute;
  left: 0;
  right: 0;
  bottom: 2px;
  height: 4px;
  border-radius: 2px;
  background: var(--border-color, currentColor);
  opacity: 0.55;
}
.shadow {
  position: absolute;
  left: 15%;
  right: 15%;
  bottom: -5px;
  height: 4px;
  border-radius: 50%;
  background: var(--text-primary, currentColor);
  opacity: 0.12;
}

@media (prefers-reduced-motion: no-preference) {
  /* Cut-out animation moves in steps, never smoothly. */
  .is-stage .walker { animation: stroll 4.8s steps(16) infinite; }
  .is-stage .flip { animation: turn 4.8s steps(1) infinite; }
  .hop { animation: hop 0.6s steps(2) infinite; }
  .is-compact .hop { animation-duration: 0.5s; }
}

@keyframes stroll {
  0% { transform: translateX(calc(var(--egg-h) * -0.85)); }
  50% { transform: translateX(calc(var(--egg-h) * 0.85)); }
  100% { transform: translateX(calc(var(--egg-h) * -0.85)); }
}
/* Faces the way it walks: right on the way out, left on the way back. */
@keyframes turn {
  0% { transform: scaleX(1); }
  50% { transform: scaleX(-1); }
}
@keyframes hop {
  0% { transform: translateY(0) rotate(-3deg); }
  100% { transform: translateY(calc(var(--egg-h) * -0.08)) rotate(3deg); }
}
</style>
