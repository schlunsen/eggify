<template>
  <!-- Zen voice control: a glowing mic orb that expands into a listening bar -->
  <div
    class="zen-voice"
    :class="{ live: live || transcribing, transcribing }"
    :role="live || transcribing ? 'status' : undefined"
    :aria-label="transcribing ? 'Transcribing' : live ? 'Listening' : undefined"
  >
    <button
      class="zen-voice-orb"
      type="button"
      :style="{ '--lvl': live && !transcribing ? level.toFixed(3) : 0 }"
      :disabled="disabled || transcribing"
      :title="live ? `Done (${dictateKey})` : `Speak (${dictateKey})`"
      :aria-label="live ? 'Stop and use what I said' : 'Start voice input'"
      @click="live ? $emit('finish') : $emit('start')"
    >
      <svg width="16" height="16" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true">
        <path d="M12 1a3 3 0 0 0-3 3v8a3 3 0 0 0 6 0V4a3 3 0 0 0-3-3z"></path>
        <path d="M19 10v2a7 7 0 0 1-14 0v-2" fill="none" stroke="currentColor" stroke-width="2"></path>
      </svg>
    </button>

    <Transition name="zen-voice-expand">
      <div v-if="live || transcribing" class="zen-voice-details">
        <span class="zen-voice-label">{{ transcribing ? 'Transcribing…' : 'Listening' }}</span>
        <span v-if="!transcribing" class="zen-voice-meter" aria-hidden="true">
          <i
            v-for="(w, i) in METER_WEIGHTS"
            :key="i"
            :style="{ transform: `scaleY(${(0.18 + Math.min(1, level * w) * 0.82).toFixed(3)})` }"
          ></i>
        </span>
        <span v-else class="zen-voice-spinner" aria-hidden="true"></span>
        <span class="zen-voice-time">{{ formattedDuration }}</span>
        <span v-if="hint && !transcribing" class="zen-voice-hint">{{ hint }}</span>
        <button
          v-if="live"
          class="zen-voice-btn primary"
          type="button"
          :disabled="transcribing"
          :title="`Stop and use what I said (${dictateKey})`"
          @click="$emit('finish')"
        >Done</button>
        <button class="zen-voice-btn icon" type="button" title="Cancel (Esc)" aria-label="Cancel" @click="$emit('cancel')">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" aria-hidden="true">
            <line x1="18" y1="6" x2="6" y2="18"></line>
            <line x1="6" y1="6" x2="18" y2="18"></line>
          </svg>
        </button>
      </div>
    </Transition>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useMicLevel } from '~/composables/useMicLevel'
import { useShortcutLabels } from '~/composables/useKeyboardShortcuts'
const dictateKey = useShortcutLabels().label('voice.dictate')

const props = withDefaults(defineProps<{
  /** Mic is live (Whisper recording or Parakeet dictation). */
  live: boolean
  /** Whisper is turning the recording into text. */
  transcribing?: boolean
  /** Seconds recorded so far. */
  duration?: number
  /** Short keyboard hint shown while live, e.g. "Enter sends". */
  hint?: string
  disabled?: boolean
}>(), { transcribing: false, duration: 0, hint: '', disabled: false })

defineEmits<{ start: []; finish: []; cancel: [] }>()

const { level } = useMicLevel()

// Bar heights relative to the level — a little uneven so it reads as a meter
const METER_WEIGHTS = [0.7, 1.15, 1.5, 1.05, 0.8]

const formattedDuration = computed(() => {
  const total = Math.floor(props.duration)
  return `${String(Math.floor(total / 60)).padStart(2, '0')}:${String(total % 60).padStart(2, '0')}`
})
</script>

<style scoped>
.zen-voice {
  display: inline-flex;
  align-items: center;
  border-radius: 999px;
  transition: background 0.25s ease, border-color 0.25s ease, box-shadow 0.25s ease, padding 0.25s ease;
  border: 1px solid transparent;
}

.zen-voice.live {
  gap: 12px;
  padding: 5px 6px 5px 5px;
  background: rgba(14, 8, 28, 0.66);
  border-color: rgba(255, 107, 209, 0.28);
  box-shadow: 0 10px 40px rgba(0, 0, 0, 0.45), 0 0 32px rgba(255, 107, 209, 0.14);
  backdrop-filter: blur(14px) saturate(1.3);
  -webkit-backdrop-filter: blur(14px) saturate(1.3);
}

/* ── The orb ─────────────────────────────────────────────────────────── */
.zen-voice-orb {
  --lvl: 0;
  position: relative;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 32px;
  height: 32px;
  padding: 0;
  border-radius: 50%;
  border: 1px solid rgba(255, 140, 215, 0.35);
  color: rgba(255, 200, 238, 0.9);
  background: rgba(20, 10, 36, 0.55);
  backdrop-filter: blur(10px);
  -webkit-backdrop-filter: blur(10px);
  box-shadow: 0 0 14px rgba(255, 79, 184, 0.18), inset 0 0 10px rgba(255, 79, 184, 0.12);
  cursor: pointer;
  transition: transform 0.08s linear, box-shadow 0.2s ease, background 0.25s ease, color 0.2s ease, border-color 0.2s ease;
}

.zen-voice-orb:hover:not(:disabled) {
  color: #fff;
  border-color: rgba(255, 150, 220, 0.7);
  box-shadow: 0 0 22px rgba(255, 79, 184, 0.45), inset 0 0 12px rgba(255, 79, 184, 0.25);
  transform: scale(1.06);
}

.zen-voice-orb:focus-visible {
  outline: 2px solid rgba(255, 150, 220, 0.85);
  outline-offset: 3px;
}

.zen-voice-orb:disabled { cursor: default; }

/* Live: a hot orb that breathes with the voice */
.zen-voice.live .zen-voice-orb {
  color: #fff;
  border-color: transparent;
  background: radial-gradient(circle at 35% 30%, #ff9ee4, #ff4fb8 60%, #c2248a);
  box-shadow: 0 0 calc(8px + var(--lvl) * 26px) rgba(255, 90, 200, calc(0.35 + var(--lvl) * 0.5));
  transform: scale(calc(1 + var(--lvl) * 0.18));
}

.zen-voice.live .zen-voice-orb::after {
  content: '';
  position: absolute;
  inset: -5px;
  border-radius: 50%;
  border: 1.5px solid rgba(255, 120, 210, 0.55);
  animation: zen-voice-ring 1.8s ease-out infinite;
  pointer-events: none;
}

.zen-voice.transcribing .zen-voice-orb {
  background: radial-gradient(circle at 35% 30%, #c9b8ff, #8a6cff 60%, #5a3fd0);
  box-shadow: 0 0 14px rgba(138, 108, 255, 0.45);
  transform: none;
}

.zen-voice.transcribing .zen-voice-orb::after { border-color: rgba(160, 140, 255, 0.5); }

@keyframes zen-voice-ring {
  from { opacity: 0.8; transform: scale(0.9); }
  to { opacity: 0; transform: scale(1.5); }
}

/* ── Live details ───────────────────────────────────────────────────── */
.zen-voice-details {
  display: inline-flex;
  align-items: center;
  gap: 12px;
  color: rgba(255, 255, 255, 0.92);
  font-size: 0.85rem;
  white-space: nowrap;
  overflow: hidden;
}

.zen-voice-label { font-weight: 600; letter-spacing: 0.01em; }

.zen-voice-meter {
  display: inline-flex;
  align-items: center;
  gap: 3px;
  height: 20px;
}

.zen-voice-meter i {
  display: block;
  width: 3px;
  height: 100%;
  border-radius: 2px;
  background: linear-gradient(to top, #ff4fb8, #7fe8ff);
  transform-origin: center;
  transition: transform 0.07s linear;
}

.zen-voice-spinner {
  width: 16px;
  height: 16px;
  border-radius: 50%;
  border: 2px solid rgba(255, 255, 255, 0.2);
  border-top-color: #b9a6ff;
  animation: zen-voice-spin 0.8s linear infinite;
}

@keyframes zen-voice-spin { to { transform: rotate(360deg); } }

.zen-voice-time {
  font-variant-numeric: tabular-nums;
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 0.78rem;
  color: rgba(255, 255, 255, 0.55);
}

.zen-voice-hint {
  font-size: 0.72rem;
  color: rgba(255, 255, 255, 0.45);
}

.zen-voice-btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  height: 30px;
  min-width: 30px;
  padding: 0 12px;
  border-radius: 999px;
  border: 1px solid rgba(255, 255, 255, 0.14);
  background: rgba(255, 255, 255, 0.06);
  color: rgba(255, 255, 255, 0.8);
  font: inherit;
  font-size: 0.8rem;
  cursor: pointer;
  transition: background 0.15s ease, color 0.15s ease;
}

.zen-voice-btn.icon { padding: 0; }
.zen-voice-btn:hover:not(:disabled) { background: rgba(255, 255, 255, 0.12); color: #fff; }

.zen-voice-btn.primary {
  background: rgba(255, 79, 184, 0.22);
  border-color: rgba(255, 120, 210, 0.5);
  color: #fff;
  font-weight: 600;
}

.zen-voice-btn.primary:hover:not(:disabled) { background: rgba(255, 79, 184, 0.34); }
.zen-voice-btn:disabled { opacity: 0.5; cursor: default; }

.zen-voice-btn:focus-visible {
  outline: 2px solid rgba(255, 150, 220, 0.8);
  outline-offset: 2px;
}

/* Expand / collapse */
.zen-voice-expand-enter-active,
.zen-voice-expand-leave-active {
  transition: opacity 0.22s ease, max-width 0.3s cubic-bezier(0.2, 0.8, 0.2, 1);
  max-width: 420px;
}

.zen-voice-expand-enter-from,
.zen-voice-expand-leave-to {
  opacity: 0;
  max-width: 0;
}

@media (prefers-reduced-motion: reduce) {
  .zen-voice.live .zen-voice-orb::after,
  .zen-voice-spinner { animation: none; }
  .zen-voice-expand-enter-active,
  .zen-voice-expand-leave-active { transition: opacity 0.15s ease; }
}

@media (max-width: 520px) {
  .zen-voice.live { gap: 8px; }
  .zen-voice-details { gap: 8px; }
  .zen-voice-time, .zen-voice-hint { display: none; }
}
</style>
