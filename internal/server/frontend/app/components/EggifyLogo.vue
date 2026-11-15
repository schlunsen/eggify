<template>
  <div
    class="eggify-egg"
    :class="[activity, { 'eggify-egg-hover': isHovered }]"
    :style="{ width: size + 'px', height: size + 'px' }"
    @mouseenter="isHovered = true"
    @mouseleave="isHovered = false"
  >
    <!-- The Eggify gentleman: an egg in a bowler hat with a ">" eye,
         a monocle (the blinking cursor) and a handlebar moustache. -->
    <svg
      :width="size"
      :height="size"
      viewBox="0 0 512 512"
      fill="none"
      xmlns="http://www.w3.org/2000/svg"
    >
      <!-- Egg -->
      <path
        class="egg"
        d="M256 112 C352 112 404 262 404 340 C404 420 340 472 256 472 C172 472 108 420 108 340 C108 262 160 112 256 112 Z"
        stroke="currentColor"
        stroke-width="16"
        stroke-linejoin="round"
        fill="none"
      />

      <!-- Crack -->
      <path
        class="crack"
        d="M312 140 L330 172 L310 192 L336 214"
        stroke="currentColor"
        stroke-width="10"
        stroke-linecap="round"
        stroke-linejoin="round"
        fill="none"
      />

      <!-- Bowler hat (tilted) -->
      <g class="hat">
        <g transform="rotate(-12 210 150)">
          <path d="M150 150 C150 62 270 62 270 150 Z" fill="currentColor" />
          <ellipse cx="210" cy="150" rx="96" ry="16" fill="currentColor" />
        </g>
      </g>

      <!-- Left eye: > prompt chevron -->
      <path
        class="face-prompt"
        d="M180 262 L212 288 L180 314"
        stroke="currentColor"
        stroke-width="16"
        stroke-linecap="round"
        stroke-linejoin="round"
        fill="none"
      />

      <!-- Right eye: monocle with the cursor as pupil -->
      <circle class="monocle" cx="316" cy="288" r="36" stroke="currentColor" stroke-width="12" fill="none" />
      <circle class="cursor-dot" cx="316" cy="288" r="11" fill="currentColor" />
      <path
        class="monocle-chain"
        d="M346 310 C372 340 360 380 380 420"
        stroke="currentColor"
        stroke-width="6"
        stroke-linecap="round"
        stroke-dasharray="2 12"
        fill="none"
      />

      <!-- Handlebar moustache -->
      <path
        class="moustache moustache-left"
        d="M256 360 C236 334 194 334 176 356 C162 374 136 376 128 356 C124 344 134 334 146 340"
        stroke="currentColor"
        stroke-width="16"
        stroke-linecap="round"
        stroke-linejoin="round"
        fill="none"
      />
      <path
        class="moustache moustache-right"
        d="M256 360 C276 334 318 334 336 356 C350 374 376 376 384 356 C388 344 378 334 366 340"
        stroke="currentColor"
        stroke-width="16"
        stroke-linecap="round"
        stroke-linejoin="round"
        fill="none"
      />

      <!-- Mouth: _ underscore -->
      <line
        class="face-underscore"
        x1="232" y1="410" x2="280" y2="410"
        stroke="currentColor"
        stroke-width="14"
        stroke-linecap="round"
      />
    </svg>
  </div>
</template>

<script setup lang="ts">
import { useCatActivity } from '~/composables/useCatActivity'

const props = withDefaults(defineProps<{
  size?: number
}>(), {
  size: 32
})

const isHovered = ref(false)
const { activity } = useCatActivity()
</script>

<style scoped>
.eggify-egg {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  cursor: pointer;
  flex-shrink: 0;
  color: var(--text-primary);
  transition: color 0.3s ease, opacity 0.3s ease, filter 0.3s ease;
  border-radius: 8px;
  position: relative;
}

/* ─── Base element transitions ─── */
.hat,
.moustache,
.cursor-dot,
.face-prompt,
.face-underscore {
  transition: transform 0.3s ease, opacity 0.3s ease, fill 0.3s ease, stroke 0.3s ease;
}

.hat,
.moustache,
.cursor-dot {
  transform-box: fill-box;
}

.hat {
  transform-origin: center bottom;
}

.moustache {
  transform-origin: center center;
}

.cursor-dot {
  transform-origin: center center;
}

/* ─── IDLE — gentle slow blink ─── */
.eggify-egg.idle .cursor-dot {
  animation: blinkSlow 4s ease-in-out infinite;
}

/* ─── SLEEPING — hat over the eyes, dimmed, slow breathe ─── */
.eggify-egg.sleeping {
  opacity: 0.4;
  filter: grayscale(0.4);
  animation: breathe 4s ease-in-out infinite;
}

.eggify-egg.sleeping .hat {
  animation: earDroopLeft 1.2s ease forwards;
}

.eggify-egg.sleeping .hat {
  animation: earDroopRight 1.2s ease forwards;
}

.eggify-egg.sleeping .cursor-dot {
  animation: blinkVerySlow 6s ease-in-out infinite;
  opacity: 0.5;
}

.eggify-egg.sleeping .moustache {
  opacity: 0.4;
}

/* ─── ACTIVE — fast blink, tips the hat, color cycle ─── */
.eggify-egg.active {
  opacity: 1;
  animation: catColorCycle 2s ease-in-out infinite;
}

.eggify-egg.active .cursor-dot {
  animation: blinkFast 0.9s ease-in-out infinite;
}

.eggify-egg.active .hat {
  animation: earPerk 0.5s cubic-bezier(0.34, 1.56, 0.64, 1) forwards;
}

/* ─── STREAMING — moustache twitches, cursor pulses, hat wiggles, rainbow ─── */
.eggify-egg.streaming {
  opacity: 1;
  animation: catRainbow 1.5s linear infinite;
}

.eggify-egg.streaming .cursor-dot {
  animation: cursorPulse 0.6s ease-in-out infinite;
}

.eggify-egg.streaming .moustache-left,
.eggify-egg.streaming .moustache-right {
  animation: whiskerTwitch1 0.4s ease infinite alternate;
}

.eggify-egg.streaming .moustache-left,
.eggify-egg.streaming .moustache-right {
  animation: whiskerTwitch2 0.35s ease infinite alternate;
  animation-delay: 0.1s;
}

.eggify-egg.streaming .moustache-left,
.eggify-egg.streaming .moustache-right {
  animation: whiskerTwitch3 0.45s ease infinite alternate;
  animation-delay: 0.05s;
}

.eggify-egg.streaming .hat {
  animation: earWiggleLeft 1.2s ease-in-out infinite;
}

.eggify-egg.streaming .hat {
  animation: earWiggleRight 1.2s ease-in-out infinite;
  animation-delay: 0.15s;
}

/* ─── ERROR — red accent, hat squashed ─── */
.eggify-egg.error .cursor-dot {
  fill: var(--color-error, #ef4444);
  stroke: var(--color-error, #ef4444);
}

.eggify-egg.error .hat {
  animation: earFlattenLeft 0.4s ease forwards;
}

.eggify-egg.error .hat {
  animation: earFlattenRight 0.4s ease forwards;
}

.eggify-egg.error .moustache {
  opacity: 0.5;
}

/* ─── BACKGROUND — cursor orbits subtly ─── */
.eggify-egg.background {
  opacity: 0.75;
}

.eggify-egg.background .cursor-dot {
  animation: cursorOrbit 2.5s linear infinite;
}

.eggify-egg.background .moustache {
  opacity: 0.6;
}

/* ─── ALERT — quick perk-up bounce ─── */
.eggify-egg.alert {
  animation: perkUp 0.5s cubic-bezier(0.34, 1.56, 0.64, 1);
}

.eggify-egg.alert .hat {
  animation: earPerk 0.3s cubic-bezier(0.34, 1.56, 0.64, 1);
}

.eggify-egg.alert .cursor-dot {
  animation: blinkFast 0.3s ease-in-out;
}

/* ─── HOVER — playful scale ─── */
.eggify-egg.eggify-egg-hover {
  filter: brightness(1.15);
}

/* ═══════════════════════════════ */
/* ═══ KEYFRAMES ═══════════════ */
/* ═══════════════════════════════ */

/* Blink animations */
@keyframes blinkSlow {
  0%, 90%, 100% { opacity: 1; }
  95% { opacity: 0; }
}

@keyframes blinkVerySlow {
  0%, 85%, 100% { opacity: 0.5; }
  90% { opacity: 0; }
}

@keyframes blinkFast {
  0%, 70%, 100% { opacity: 1; }
  80% { opacity: 0; }
}

/* Cursor pulse for streaming */
@keyframes cursorPulse {
  0%, 100% { r: 10; opacity: 1; }
  50% { r: 13; opacity: 0.7; }
}

/* Cursor orbit for background agents */
@keyframes cursorOrbit {
  0% { transform: translate(0, 0); }
  25% { transform: translate(3px, -3px); }
  50% { transform: translate(0, -5px); }
  75% { transform: translate(-3px, -3px); }
  100% { transform: translate(0, 0); }
}

/* Breathing for sleeping */
@keyframes breathe {
  0%, 100% { transform: scale(1); }
  50% { transform: scale(0.97); }
}

/* Ear animations */
/* Sleeping: hat slides down over the eyes */
@keyframes earDroopLeft {
  0% { transform: translateY(0) rotate(0deg); }
  100% { transform: translateY(26px) rotate(6deg); }
}

@keyframes earDroopRight {
  0% { transform: rotate(0deg); }
  100% { transform: rotate(-12deg); }
}

/* Tip of the hat */
@keyframes earPerk {
  0% { transform: translateY(0) rotate(0deg); }
  50% { transform: translateY(-18px) rotate(-8deg); }
  100% { transform: translateY(0) rotate(0deg); }
}

/* Error: hat squashed flat (by a giant foot, presumably) */
@keyframes earFlattenLeft {
  0% { transform: scaleY(1); }
  100% { transform: scaleY(0.5) translateY(14px); }
}

@keyframes earFlattenRight {
  0% { transform: scaleY(1); }
  100% { transform: scaleY(0.55) rotate(-15deg); }
}

@keyframes earWiggleLeft {
  0%, 100% { transform: rotate(0deg); }
  25% { transform: rotate(-3deg); }
  75% { transform: rotate(3deg); }
}

@keyframes earWiggleRight {
  0%, 100% { transform: rotate(0deg); }
  25% { transform: rotate(3deg); }
  75% { transform: rotate(-3deg); }
}

/* Whisker twitches */
@keyframes whiskerTwitch1 {
  0% { transform: translateY(0); }
  100% { transform: translateY(-2px); }
}

@keyframes whiskerTwitch2 {
  0% { transform: translateY(0); }
  100% { transform: translateY(1.5px); }
}

@keyframes whiskerTwitch3 {
  0% { transform: translateY(0); }
  100% { transform: translateY(-1px); }
}

/* Color animations for active/streaming */
@keyframes catColorCycle {
  0%, 100% { color: var(--accent-purple, #a855f7); }
  33% { color: var(--accent-cyan, #22d3ee); }
  66% { color: var(--accent-purple, #a855f7); }
}

@keyframes catRainbow {
  0% { color: #a855f7; }
  16% { color: #ec4899; }
  33% { color: #f43f5e; }
  50% { color: #f97316; }
  66% { color: #22d3ee; }
  83% { color: #3b82f6; }
  100% { color: #a855f7; }
}

/* Perk up bounce (alert) */
@keyframes perkUp {
  0% { transform: scale(1); }
  40% { transform: scale(1.18); }
  70% { transform: scale(0.95); }
  100% { transform: scale(1); }
}
</style>
