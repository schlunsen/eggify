<template>
  <div
    class="nt"
    :class="[`mood-${scene.mood}`, { open: expanded, bare: !notchWidth, still: reducedMotion }]"
    :style="vars"
    @mouseenter="hoverIn"
    @mouseleave="hoverOut"
    @keydown.esc="collapse"
  >
    <div class="nt-shell">
      <!-- The menu bar row: an ear on each side of the camera housing. -->
      <div class="nt-bar">
        <button type="button" class="nt-ear nt-ear-left" :aria-expanded="expanded" :aria-label="scene.headline" @click="toggle">
          <!-- A little lit stage: velvet swags, a spotlight while someone works, footlights. -->
          <span class="rig" :class="{ lit: spotlit }" aria-hidden="true">
            <span class="beam" />
            <span class="swag swag-l" />
            <span class="swag swag-r" />
            <span class="troupe">
              <template v-if="signedOut" />
              <span v-for="a in scene.stage" v-else-if="scene.stage.length" :key="a.id" class="mini" :class="`st-${a.state}`">
                <img :src="circusEggArt(a.kind)" alt="" draggable="false">
              </span>
              <span v-else class="mini st-asleep"><img :src="circusEggArt('egg')" alt="" draggable="false"><i class="zz">z</i></span>
            </span>
            <span class="footlights" />
          </span>
          <span v-if="scene.extra" class="more" aria-hidden="true">+{{ scene.extra }}</span>
        </button>
        <span class="nt-housing" aria-hidden="true" />
        <button type="button" class="nt-ear nt-ear-right" :aria-expanded="expanded" :aria-label="scene.headline" @click="toggle">
          <span class="lamp" :class="`lamp-${badgeMood}`" aria-hidden="true" />
          <span class="badge" :class="`badge-${badgeMood}`">{{ badgeText }}</span>
        </button>
      </div>

      <!-- The stage, dropped down on hover. -->
      <div v-if="expanded" class="nt-drop">
        <div class="proscenium" :class="{ closed: curtainsClosed }">
          <div class="valance" aria-hidden="true" />
          <div class="curtain curtain-l" aria-hidden="true" />
          <div class="curtain curtain-r" aria-hidden="true" />
          <div class="boards" aria-hidden="true" />
          <div v-if="!curtainsClosed" class="cast">
            <div v-for="a in castOnStage" :key="a.id" class="player" :class="`st-${a.state}`" :title="`${a.name}: ${a.line}`">
              <span v-if="a.state === 'needs'" class="placard">{{ placardFor(a) }}</span>
              <span v-else-if="a.state === 'sweating'" class="sweat" aria-hidden="true">💧</span>
              <span v-else-if="a.state === 'bowing'" class="confetti" aria-hidden="true" />
              <CircusFigure :id="a.id" :size="30" :status="figureStatus(a.state)" :talking="a.state === 'working' || a.state === 'sweating'" />
              <span v-if="a.subagents" class="wings" aria-hidden="true">
                <img v-for="i in Math.min(a.subagents, 3)" :key="i" :src="circusEggArt(subKind(a.id, i))" alt="">
              </span>
            </div>
          </div>
          <div v-else class="sign">
            <span v-if="scene.mood === 'intermission'">Intermission<br><small>{{ scene.badge.replace('⏸ ', 'resumes in ') }}</small></span>
            <span v-else-if="scene.mood === 'loop'">⌛ {{ scene.headline }}</span>
            <span v-else-if="signedOut">Sign in to Eggify<br><small>to see the troupe</small></span>
            <span v-else>Resting, not dead</span>
          </div>
          <img v-if="scene.mood === 'oops'" class="foot" src="/zen/circus/foot.webp" alt="" aria-hidden="true">
        </div>

        <div class="playbill" role="region" aria-label="Tonight's programme">
          <p class="playbill-title">Tonight's programme</p>
          <p class="sr-only" aria-live="polite">{{ scene.headline }}</p>
          <div v-if="scene.cues.length" class="cues">
            <NotchCueCard
              v-for="cue in scene.cues.slice(0, 3)"
              :key="cue.key"
              :cue="cue"
              :now="now"
              :answer="answer"
              @answered="$emit('answered', $event)"
              @open="$emit('open', $event)"
            />
            <p v-if="scene.cues.length > 3" class="more-cues">{{ scene.cues.length - 3 }} more waiting in Eggify</p>
          </div>
          <ul v-if="rows.length" class="rows">
            <li v-for="a in rows" :key="a.id" class="row" :class="`st-${a.state}`">
              <img class="row-egg" :src="circusEggArt(a.kind)" alt="" aria-hidden="true">
              <span class="row-text">
                <span class="row-name">{{ a.name }}</span>
                <span class="row-line">{{ a.line }}</span>
              </span>
              <button type="button" class="row-open" :aria-label="`Open ${a.name}`" @click="$emit('open', a.id)">Open</button>
            </li>
          </ul>
          <p v-else-if="!scene.cues.length" class="empty">
            {{ signedOut ? 'Sign in to Eggify in the main window.' : error || 'No performances tonight. The troupe is resting.' }}
          </p>
          <button type="button" class="footer-open" @click="$emit('open', '')">Open Eggify</button>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, ref } from 'vue'
import CircusFigure from '~/components/agents/CircusFigure.vue'
import NotchCueCard from './NotchCueCard.vue'
import { circusEggArt, CIRCUS_KINDS, type CircusKind } from '~/utils/agents/circusCharacters'
import { hashString } from '~/utils/agents/subagentCharacters'
import type { ActorState, NotchActor, NotchScene } from '~/utils/notch/notchScene'
import type { AnswerResult, NotchAnswer } from '~/utils/notch/notchAnswer'

/**
 * The Notch Theatre: a black pill around the camera housing whose left ear
 * holds the troupe (one costume egg per busy session) and whose right ear
 * says what is going on; on hover it drops down into a little proscenium
 * stage and a paper playbill with approval cards.
 *
 * The window is resized by the desktop app (`resize`): grown before the
 * stage opens, shrunk after it has closed, so the pill never jumps.
 */
const props = withDefaults(defineProps<{
  scene: NotchScene
  now: number
  notchWidth: number
  menuHeight: number
  ear: number
  signedOut?: boolean
  error?: string
  answer: (a: NotchAnswer) => Promise<AnswerResult>
  resize?: (expanded: boolean) => Promise<unknown> | void
}>(), { signedOut: false, error: '', resize: undefined })

defineEmits<{ answered: [key: string]; open: [sessionId: string] }>()

/** Hover this long before the stage opens (crossing the menu bar does nothing). */
const HOVER_OPEN_MS = 180
/** Leave this long before it closes. */
const HOVER_CLOSE_MS = 450
/** The close transition, after which the window shrinks back. */
const CLOSE_ANIM_MS = 220

const expanded = ref(false)
let openTimer: ReturnType<typeof setTimeout> | undefined
let closeTimer: ReturnType<typeof setTimeout> | undefined
let shrinkTimer: ReturnType<typeof setTimeout> | undefined

const reducedMotion = typeof window !== 'undefined' && !!window.matchMedia?.('(prefers-reduced-motion: reduce)').matches

const vars = computed(() => ({
  '--nt-housing': `${props.notchWidth}px`,
  '--nt-bar-h': `${props.menuHeight}px`,
  '--nt-ear': `${props.ear}px`,
}))

const curtainsClosed = computed(() => props.signedOut || ['asleep', 'intermission', 'loop'].includes(props.scene.mood))
const castOnStage = computed(() => props.scene.actors.filter(a => a.state !== 'waiting' && a.state !== 'loop').slice(0, 5))
const rows = computed(() => props.scene.actors.filter(a => a.state !== 'needs' || !props.scene.cues.some(c => c.sessionId === a.id)))

const badgeMood = computed(() => props.signedOut ? 'asleep' : props.scene.mood)
const badgeText = computed(() => {
  if (props.signedOut) return 'Sign in'
  switch (props.scene.mood) {
    case 'needs': return 'Needs you'
    case 'oops': return 'Oops'
    case 'bow': return 'Bravo!'
    case 'working': return `${props.scene.badge} working`
    case 'asleep': return 'Resting'
    default: return props.scene.badge
  }
})
/** The spotlight is on while someone is on stage doing something. */
const spotlit = computed(() => !props.signedOut && ['needs', 'working', 'bow', 'oops'].includes(props.scene.mood))

function figureStatus(state: ActorState): string {
  if (state === 'cracked') return 'failed'
  if (state === 'bowing') return 'completed'
  return 'running'
}

function placardFor(a: NotchActor): string {
  const cue = props.scene.cues.find(c => c.sessionId === a.id)
  if (!cue) return 'Psst!'
  return cue.type === 'question' ? 'A question!' : 'May I?'
}

/** The tiny eggs in the wings: one per running subagent, picked like any egg. */
function subKind(id: string, i: number): CircusKind {
  return CIRCUS_KINDS[hashString(`${id}:${i}`) % CIRCUS_KINDS.length]!
}

function clearTimers() {
  for (const t of [openTimer, closeTimer, shrinkTimer]) if (t) clearTimeout(t)
  openTimer = closeTimer = shrinkTimer = undefined
}

async function open() {
  clearTimers()
  if (expanded.value) return
  await props.resize?.(true)
  expanded.value = true
}

function collapse() {
  clearTimers()
  if (!expanded.value) return
  expanded.value = false
  shrinkTimer = setTimeout(() => { void props.resize?.(false) }, reducedMotion ? 0 : CLOSE_ANIM_MS)
}

function toggle() {
  if (expanded.value) collapse()
  else void open()
}

function hoverIn() {
  if (closeTimer) { clearTimeout(closeTimer); closeTimer = undefined }
  if (expanded.value || openTimer) return
  openTimer = setTimeout(() => { openTimer = undefined; void open() }, HOVER_OPEN_MS)
}

function hoverOut() {
  if (openTimer) { clearTimeout(openTimer); openTimer = undefined }
  if (!expanded.value) return
  // Keep the stage open while a card's button has focus (keyboard use).
  if (document.activeElement && (document.activeElement as HTMLElement).closest?.('.nt-drop')) return
  closeTimer = setTimeout(collapse, HOVER_CLOSE_MS)
}

onBeforeUnmount(clearTimers)

defineExpose({ open, collapse, expanded })
</script>

<style scoped>
.nt {
  --nt-black: #000;
  --nt-velvet: #8e1f2f;
  --nt-velvet-dark: #4c0d17;
  --nt-gold: #d4ac55;
  --nt-paper: #f2e8d2;
  --nt-paper-rule: #d8c6a0;
  --nt-ink: #2a1f16;
  --nt-dim: #6b5a45;
  --nt-error: #a0281c;
  --nt-amber: #f2b33d;
  --nt-green: #7fc28a;
  --nt-red: #ef6b5b;
  --nt-board: #6e4a2a;
  --nt-serif: 'Fraunces', 'EB Garamond', Georgia, serif;
  --nt-sans: -apple-system, BlinkMacSystemFont, 'Inter', sans-serif;
  --nt-mono: 'SF Mono', ui-monospace, Menlo, monospace;
  position: fixed;
  inset: 0;
  display: flex;
  justify-content: center;
  align-items: flex-start;
  font-family: var(--nt-sans);
  color: var(--nt-paper);
  user-select: none;
  -webkit-user-select: none;
}

/* The black shape: flush with the top of the screen, concave shoulders that
   melt into the menu bar (like the hardware notch), a capsule bottom. */
.nt-shell {
  --sh: 9px;
  position: relative;
  width: calc(var(--nt-housing) + 2 * var(--nt-ear) - 2 * var(--sh));
  background: var(--nt-black);
  border-radius: 0 0 calc(var(--nt-bar-h) * 0.5) calc(var(--nt-bar-h) * 0.5);
  transition: width 0.24s cubic-bezier(0.22, 1, 0.36, 1), border-radius 0.24s;
}
.nt-shell::before,
.nt-shell::after {
  content: '';
  position: absolute;
  top: 0;
  width: var(--sh);
  height: var(--sh);
  pointer-events: none;
}
.nt-shell::before { right: 100%; background: radial-gradient(circle at 0 100%, transparent calc(var(--sh) - 0.5px), var(--nt-black) var(--sh)); }
.nt-shell::after { left: 100%; background: radial-gradient(circle at 100% 100%, transparent calc(var(--sh) - 0.5px), var(--nt-black) var(--sh)); }
.bare .nt-shell { width: calc(220px - 2 * var(--sh)); }
.open .nt-shell { width: calc(100% - 2 * var(--sh)); border-radius: 0 0 24px 24px; }

.nt-bar { display: flex; height: var(--nt-bar-h); align-items: stretch; overflow: hidden; }
.nt-housing { width: var(--nt-housing); flex: none; }
.bare .nt-housing { width: 0; }
.nt-ear {
  position: relative;
  flex: 1;
  min-width: 0;
  display: flex;
  align-items: center;
  gap: 5px;
  border: 0;
  padding: 0 8px;
  background: transparent;
  color: inherit;
  cursor: pointer;
}
.nt-ear-left { justify-content: flex-end; }
.nt-ear-right { justify-content: flex-start; }
/* Without a housing the two ears sit together in the middle. */
.bare .nt-bar { justify-content: center; gap: 4px; }
.bare .nt-ear { flex: none; padding: 0 2px; }
.nt-ear:focus-visible { outline: 2px solid var(--nt-gold); outline-offset: -3px; border-radius: 8px; }

/* The little stage in the left ear. */
.rig {
  position: relative;
  display: flex;
  align-items: flex-end;
  height: calc(var(--nt-bar-h) - 7px);
  min-width: 34px;
  padding: 0 9px 2px;
  border-radius: 6px 6px 4px 4px;
  background: linear-gradient(#1b130d, #0b0806);
  box-shadow: inset 0 0 0 1px rgb(212 172 85 / 0.22);
  overflow: hidden;
}
.beam {
  position: absolute; inset: 0;
  background: radial-gradient(ellipse 55% 110% at 50% -10%, rgb(255 228 168 / 0.42), transparent 72%);
  opacity: 0; transition: opacity 0.4s;
}
.rig.lit .beam { opacity: 1; }
.swag { position: absolute; top: 0; width: 9px; height: 12px; }
.swag-l { left: 0; background: radial-gradient(circle at 0 0, var(--nt-velvet) 0 8px, transparent 8.5px); }
.swag-r { right: 0; background: radial-gradient(circle at 100% 0, var(--nt-velvet) 0 8px, transparent 8.5px); }
.footlights {
  position: absolute; left: 4px; right: 4px; bottom: 1px; height: 2px; border-radius: 2px;
  background: linear-gradient(90deg, transparent, var(--nt-gold) 20%, var(--nt-gold) 80%, transparent);
  box-shadow: 0 0 6px rgb(212 172 85 / 0.6);
  opacity: 0.55;
}
.rig.lit .footlights { opacity: 1; }

.troupe { position: relative; z-index: 1; display: flex; align-items: flex-end; gap: 2px; height: 100%; }
.mini { position: relative; display: block; height: calc(100% - 1px); }
.mini img { height: 100%; width: auto; display: block; filter: drop-shadow(0 1px 1px rgb(0 0 0 / 0.6)); }
.more { font: 600 10px/1 var(--nt-sans); color: var(--nt-gold); }
.zz { position: absolute; top: 0; right: -7px; font: italic 600 9px/1 var(--nt-serif); color: var(--nt-gold); animation: zz 2.4s steps(3) infinite; }
@keyframes zz { 0% { transform: translate(0, 4px); opacity: 0; } 50% { opacity: 1; } 100% { transform: translate(3px, -3px); opacity: 0; } }

/* Cut-out animation moves in steps. */
.st-working, .st-sweating { animation: work 0.9s steps(2) infinite; transform-origin: 50% 100%; }
@keyframes work { 0% { transform: rotate(-4deg); } 100% { transform: rotate(4deg) translateY(-1px); } }
.mini.st-needs { animation: wave 0.6s steps(2) infinite; transform-origin: 50% 100%; }
@keyframes wave { 0% { transform: translateY(0) rotate(-6deg); } 100% { transform: translateY(-2px) rotate(6deg); } }
.st-bowing { animation: bow 1.4s steps(4) 2; transform-origin: 50% 100%; }
@keyframes bow { 0%, 100% { transform: rotate(0); } 40%, 60% { transform: rotate(28deg); } }
.st-cracked { filter: saturate(0.6); transform: rotate(-12deg); transform-origin: 50% 100%; }
.mini.st-asleep { opacity: 0.7; transform: rotate(-16deg); transform-origin: 50% 100%; }
.mini.st-waiting, .mini.st-loop { opacity: 0.7; }

/* The right ear: a status lamp and a word. */
.lamp { width: 6px; height: 6px; border-radius: 50%; flex: none; background: var(--nt-dim); }
.lamp-working { background: var(--nt-gold); box-shadow: 0 0 6px var(--nt-gold); animation: glow 1.6s ease-in-out infinite; }
.lamp-needs { background: var(--nt-amber); box-shadow: 0 0 8px var(--nt-amber); animation: glow 0.7s ease-in-out infinite; }
.lamp-bow { background: var(--nt-green); box-shadow: 0 0 6px var(--nt-green); }
.lamp-oops { background: var(--nt-red); box-shadow: 0 0 6px var(--nt-red); }
.lamp-intermission, .lamp-loop { background: var(--nt-amber); opacity: 0.8; }
@keyframes glow { 50% { opacity: 0.45; } }
.badge {
  font: 600 11px/1 var(--nt-sans); letter-spacing: 0.01em; white-space: nowrap;
  font-variant-numeric: tabular-nums; color: var(--nt-paper); overflow: hidden; text-overflow: ellipsis;
  margin-top: -1px;
}
.badge-needs { color: var(--nt-amber); }
.badge-bow { color: var(--nt-green); }
.badge-oops { color: var(--nt-red); }
.badge-intermission, .badge-loop { color: var(--nt-amber); }
.badge-asleep { color: #8d7c66; font: italic 500 12px/1 var(--nt-serif); }

/* ---- The stage ---- */
.nt-drop { animation: drop 0.22s cubic-bezier(0.22, 1, 0.36, 1) both; }
@keyframes drop { from { opacity: 0; transform: translateY(-14px); } }

.proscenium {
  position: relative;
  height: 104px;
  margin: 2px 12px 0;
  border-radius: 6px 6px 0 0;
  background: radial-gradient(ellipse at 50% 30%, #2d241c 0%, #120d09 75%);
  overflow: hidden;
}
.valance {
  position: absolute; inset: 0 0 auto; height: 16px; z-index: 3;
  background:
    radial-gradient(circle at 10px 0, transparent 9px, var(--nt-velvet) 9.5px) 0 6px / 20px 12px repeat-x,
    linear-gradient(var(--nt-velvet), var(--nt-velvet)) 0 0 / 100% 7px no-repeat;
  border-bottom: 1px solid var(--nt-gold);
  -webkit-mask: linear-gradient(#000, #000);
}
.curtain {
  position: absolute; top: 0; bottom: 14px; width: 13%; z-index: 2;
  background: repeating-linear-gradient(90deg, var(--nt-velvet) 0 6px, var(--nt-velvet-dark) 6px 9px);
  transition: width 0.5s steps(5);
}
.curtain-l { left: 0; border-bottom-right-radius: 40% 20%; }
.curtain-r { right: 0; border-bottom-left-radius: 40% 20%; }
.closed .curtain { width: 50.5%; border-radius: 0; bottom: 0; }
.boards {
  position: absolute; left: 0; right: 0; bottom: 0; height: 16px; z-index: 1;
  background: repeating-linear-gradient(90deg, var(--nt-board) 0 38px, #5a3b20 38px 40px);
  border-top: 2px solid #8a6238;
}
.cast { position: absolute; inset: 18px 15% 12px; display: flex; justify-content: center; align-items: flex-end; gap: 14px; z-index: 1; }
.player { position: relative; display: flex; flex-direction: column; align-items: center; }
.player.st-needs { animation: hop 0.5s steps(2) infinite; }
@keyframes hop { 0% { transform: translateY(0); } 100% { transform: translateY(-4px); } }
.placard {
  position: absolute; bottom: 100%; margin-bottom: 2px; white-space: nowrap;
  font: 600 9px/1 var(--nt-serif); color: var(--nt-ink); background: var(--nt-paper);
  padding: 3px 5px; border-radius: 2px; box-shadow: 0 1px 0 rgb(0 0 0 / 0.5);
}
.sweat { position: absolute; top: 2px; right: -6px; font-size: 9px; animation: drip 1.1s steps(3) infinite; }
@keyframes drip { 0% { transform: translateY(0); opacity: 1; } 100% { transform: translateY(10px); opacity: 0; } }
.confetti {
  position: absolute; bottom: 100%; width: 40px; height: 22px;
  background:
    radial-gradient(circle, var(--nt-gold) 1.5px, transparent 2px) 0 0 / 9px 8px,
    radial-gradient(circle, var(--nt-red) 1.5px, transparent 2px) 4px 4px / 11px 9px;
  animation: confetti 1.2s steps(4) 3 both;
}
@keyframes confetti { from { transform: translateY(-6px); opacity: 1; } to { transform: translateY(8px); opacity: 0; } }
.wings { position: absolute; left: calc(100% + 1px); bottom: 0; display: flex; gap: 1px; }
.wings img { height: 18px; width: auto; animation: work 0.7s steps(2) infinite; }

.sign {
  position: absolute; inset: 0; z-index: 4; display: grid; place-items: center; text-align: center;
  font: italic 600 13px/1.25 var(--nt-serif); color: var(--nt-gold);
}
.sign span { background: rgb(0 0 0 / 0.55); padding: 6px 12px; border: 1px solid var(--nt-gold); border-radius: 3px; }
.sign small { font-style: normal; font-weight: 500; font-size: 11px; opacity: 0.85; font-variant-numeric: tabular-nums; }
.foot { position: absolute; z-index: 5; top: -10px; left: 50%; width: 80px; transform: translateX(-50%); animation: stomp 0.6s steps(3) both; }
@keyframes stomp { from { transform: translate(-50%, -110%); } to { transform: translate(-50%, 0); } }

/* ---- The playbill ---- */
.playbill {
  height: calc(300px - 104px - 2px - 12px);
  margin: 0 12px 12px;
  padding: 8px 10px 10px;
  overflow-y: auto;
  background: var(--nt-paper);
  color: var(--nt-ink);
  border-radius: 0 0 12px 12px;
  box-shadow: inset 0 1px 0 var(--nt-gold);
  user-select: text;
  -webkit-user-select: text;
}
.playbill-title {
  margin: 0 0 6px; text-align: center; text-transform: uppercase; letter-spacing: 0.18em;
  font: 600 10px/1 var(--nt-serif); color: var(--nt-dim);
}
.cues { display: flex; flex-direction: column; gap: 7px; margin-bottom: 8px; }
.more-cues { margin: 0; font-size: 11px; color: var(--nt-dim); text-align: center; }
.rows { list-style: none; margin: 0; padding: 0; }
.row { display: flex; align-items: center; gap: 8px; padding: 5px 0; border-top: 1px dashed var(--nt-paper-rule); }
.row:first-child { border-top: 0; }
.row-egg { height: 26px; width: auto; flex: none; }
.row-text { flex: 1; min-width: 0; display: flex; flex-direction: column; }
.row-name { font: 600 13px/1.2 var(--nt-serif); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.row-line { font-size: 11px; color: var(--nt-dim); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.row.st-needs .row-line { color: var(--nt-error); }
.row-open, .footer-open {
  font: 600 11px/1 var(--nt-sans); cursor: pointer; border-radius: 999px;
  background: transparent; color: var(--nt-ink); border: 1px solid var(--nt-paper-rule); padding: 5px 9px;
}
.row-open:hover, .footer-open:hover { border-color: var(--nt-ink); }
.row-open:focus-visible, .footer-open:focus-visible { outline: 2px solid var(--nt-velvet); outline-offset: 1px; }
.footer-open { display: block; margin: 8px auto 0; }
.empty { margin: 14px 4px; text-align: center; font: italic 13px/1.35 var(--nt-serif); color: var(--nt-dim); }
.sr-only { position: absolute; width: 1px; height: 1px; overflow: hidden; clip: rect(0 0 0 0); white-space: nowrap; }

.still *, .still { animation: none !important; transition: none !important; }
</style>
