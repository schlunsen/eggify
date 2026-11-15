<template>
  <span
    class="circus-figure"
    :class="[`kind-${look.kind}`, `is-${status}`, { talking }]"
    :style="{ height: `${size * 1.4}px`, '--w': `${size * 1.4 * EGG_ASPECT[look.kind]}px` }"
    aria-hidden="true"
  >
    <span class="bob">
      <!-- An engraved, hand-tinted egg in costume, like the main egg gentleman. -->
      <img class="egg" :src="src" alt="" draggable="false">
      <!-- Not dead, merely cracked. -->
      <svg v-if="status === 'failed'" class="crack" viewBox="0 0 100 100" preserveAspectRatio="none">
        <path d="M30 22 L38 28 L33 34 L44 38 L40 45 L52 48 L49 55 L62 58" />
        <path d="M44 38 L50 34 M52 48 L58 45" />
      </svg>
    </span>
  </span>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { circusCharacterFor, circusEggArt, EGG_ASPECT } from '~/utils/agents/circusCharacters'

/**
 * A subagent on the Flying Circus stages: one of six engraved eggs in costume
 * (gent, knight, hanky, explorer with a parrot, professor, lady), picked from
 * its id, so it looks the same everywhere it is shown. `size` is the old
 * figure width; the egg is size x 1.4 tall.
 */
const props = withDefaults(defineProps<{ id: string; status?: string; talking?: boolean; size?: number }>(), {
  status: 'running',
  talking: false,
  size: 64
})

const look = computed(() => circusCharacterFor(props.id))
const src = computed(() => circusEggArt(look.value.kind))
</script>

<style scoped>
.circus-figure {
  display: block;
  position: relative;
  width: var(--w);
  pointer-events: none;
}
.bob {
  position: relative;
  display: block;
  width: 100%;
  height: 100%;
  transform-origin: 50% 100%;
}
.egg { display: block; width: 100%; height: 100%; object-fit: contain; user-select: none; }

/* Cut-out animation moves in steps, never smoothly. */
.is-running .bob { animation: twitch 1.6s steps(1) infinite; }
@keyframes twitch { 0% { transform: rotate(0); } 33% { transform: rotate(-1.5deg); } 66% { transform: rotate(1deg) translateY(-0.5px); } }
.talking.is-running .bob { animation: natter .48s steps(2) infinite; }
@keyframes natter { 0% { transform: rotate(-2deg) translateY(0); } 100% { transform: rotate(2deg) translateY(-2px); } }
:global(.walking .circus-figure.is-running .bob) { animation: hop .36s steps(2) infinite; }
@keyframes hop { 0% { transform: translateY(0) rotate(-3deg); } 100% { transform: translateY(-3px) rotate(3deg); } }

/* Done: a little lean on the bench (the stage draws the snooze) */
.is-completed .bob { transform: rotate(-5deg); }
.is-completed .egg { filter: sepia(0.25); }

/* Failed: cracked, slumped and a little faded */
.is-failed .bob { transform: rotate(9deg) translateY(4%); }
.is-failed .egg { filter: grayscale(0.55) brightness(0.92); }
.crack {
  position: absolute;
  inset: 0;
  width: 100%;
  height: 100%;
  overflow: visible;
}
.crack path { fill: none; stroke: #2b1d10; stroke-width: 1.6; stroke-linejoin: round; stroke-linecap: round; vector-effect: non-scaling-stroke; }

@media (prefers-reduced-motion: reduce) {
  .bob { animation: none !important; }
}
</style>
