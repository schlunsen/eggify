<template>
  <div class="zen-stage" :class="[`stage-${stage}`, { 'lead-working': leadWorking }]" role="list" aria-label="Subagents at work">
    <template v-if="circus">
      <!-- Flying Circus: the session's own engraved backdrop (the one its Zen
           shows) as the scenery, with the stage boards running on below it. -->
      <!-- eslint-disable-next-line vue/no-v-html -- built from fixed art in circusBackdrops.ts -->
      <div class="zen-circus cc-shown cc-reduced circus-scenery" :class="`cc-bd-${backdrop}`" aria-hidden="true" v-html="backdropHtml" />
      <div class="circus-floor" :style="{ '--board': backdropBoard }" aria-hidden="true" />
      <img v-if="leadWorking" class="circus-cherub" src="/zen/circus/cherub.webp" alt="" aria-hidden="true">
      <div v-if="restingCount" class="circus-bench" aria-hidden="true"><span>Resting, not dead</span></div>
      <img
        class="circus-foot"
        :class="{ stomping: foot.down }"
        src="/zen/circus/foot.webp"
        alt=""
        aria-hidden="true"
        :style="{ left: `${foot.x}%`, '--land': `${foot.y}%` }"
      >
    </template>
    <template v-else>
      <div class="zen-sky" aria-hidden="true" />
      <div v-if="lead" class="zen-sun" :style="{ '--lead': leadColor }" aria-hidden="true" />
      <div class="zen-city" aria-hidden="true" />
      <div class="zen-floor" aria-hidden="true" />
      <div v-if="restingCount" class="zen-bench" aria-hidden="true"><span>Chill zone</span></div>
    </template>

    <!-- Who reports to whom: the main agent to its subagents, a subagent to the ones it launched. -->
    <svg v-if="lead || tethered.length" class="tethers" viewBox="0 0 100 100" preserveAspectRatio="none" aria-hidden="true">
      <line
        v-for="t in tethered"
        :key="t.id"
        :ref="el => setTether(t.id, el)"
        class="tether"
        :class="{ nested: t.nested }"
        :style="{ '--c': look(t.id).color }"
        vector-effect="non-scaling-stroke"
      />
    </svg>

    <div
      v-if="lead"
      class="lead-slot"
      :style="{ left: `${LEAD.x}%`, top: `${LEAD.y}%`, '--lead': leadColor }"
      role="listitem"
    >
      <button
        type="button"
        class="lead"
        :class="[`is-${leadWorking ? 'working' : 'idle'}`, { talking: talking(LEAD_ID) }]"
        :aria-label="`${lead.name}, the main agent, ${leadWorking ? 'working' : 'idle'}: ${leadSpeech.text}. Back to its chat.`"
        @click="emit('lead')"
      >
        <span :key="`lead:${leadSpeech.text}`" class="bubble lead-bubble" aria-hidden="true">
          <span class="say"><span class="glyph">{{ leadSpeech.glyph }}</span>{{ leadSpeech.text }}</span>
        </span>
        <template v-if="circus">
          <!-- The main agent addresses the crowd from a soapbox. -->
          <img class="circus-lead" src="/zen/circus/egg.webp" alt="" aria-hidden="true">
          <span class="soapbox" aria-hidden="true">
            <img v-if="lead.avatarImage && !avatarBroken" class="cameo" :src="lead.avatarImage" alt="" @error="avatarBroken = true">
            <span class="crate-label">Main agent</span>
          </span>
        </template>
        <template v-else>
          <svg class="crown" viewBox="0 0 40 22" aria-hidden="true">
            <path d="M3 20 L6 6 L14 13 L20 2 L26 13 L34 6 L37 20 Z" />
            <circle cx="6" cy="5" r="2.2" /><circle cx="20" cy="2.2" r="2.4" /><circle cx="34" cy="5" r="2.2" />
          </svg>
          <span class="portrait" aria-hidden="true">
            <span class="halo" />
            <img v-if="lead.avatarImage && !avatarBroken" :src="lead.avatarImage" alt="" @error="avatarBroken = true">
            <span v-else class="initial">{{ leadInitial }}</span>
            <svg class="phones" viewBox="0 0 100 100"><path d="M10 58 C10 18 90 18 90 58" /><rect x="2" y="50" width="16" height="26" rx="7" /><rect x="82" y="50" width="16" height="26" rx="7" /></svg>
          </span>
          <!-- The main agent runs the decks; its subagents move to the beat. -->
          <span class="deck" aria-hidden="true">
            <span class="vinyl" /><span class="eq"><i /><i /><i /><i /><i /></span><span class="vinyl" />
            <span v-if="leadWorking" class="notes"><i>♪</i><i>♫</i><i>♪</i></span>
          </span>
        </template>
        <span class="plaque"><span class="role">Main agent</span><span class="lead-name">{{ lead.name }}</span></span>
      </button>
    </div>

    <p v-if="!agents.length" class="zen-hint">No subagents yet. They step out from {{ lead?.name || 'the main agent' }} when launched.</p>

    <div
      v-for="agent in agents"
      :key="agent.id"
      :ref="el => setElement(agent.id, el)"
      class="critter-slot"
      :class="{ 'is-talking': talking(agent.id) }"
      role="listitem"
    >
      <button
        type="button"
        class="critter"
        :class="[`is-${agent.status}`, `body-${look(agent.id).body}`, { talking: talking(agent.id), nested: !!agent.parentId }]"
        :style="{ '--c': look(agent.id).color }"
        :aria-label="`${agent.name}, ${statusWord(agent.status)}: ${speech(agent).text}. Show its activity.`"
        @click="emit('select', agent.id)"
      >
        <span :key="`${agent.id}:${speech(agent).text}`" class="bubble" aria-hidden="true">
          <span class="say"><span class="glyph">{{ speech(agent).glyph }}</span>{{ speech(agent).text }}</span>
        </span>
        <span v-if="circus" class="cfig">
          <CircusFigure :id="agent.id" :status="agent.status" :talking="talking(agent.id)" :size="agent.parentId ? 54 : 72" />
        </span>
        <template v-else>
          <svg class="figure" viewBox="0 0 64 72" aria-hidden="true" :style="{ '--acc': look(agent.id).accent }">
            <ellipse class="shadow" cx="32" cy="68" rx="17" ry="3.5" />
            <g class="bob">
              <!-- body, twice: a thick dark outline under the fill gives a sticker look -->
              <g v-for="layer in LAYERS" :key="layer" :class="layer">
                <rect v-if="look(agent.id).body === 'bean'" x="13" y="12" width="38" height="52" rx="19" />
                <template v-else-if="look(agent.id).body === 'boombox'">
                  <rect x="7" y="20" width="50" height="42" rx="7" /><rect x="17" y="12" width="30" height="4" rx="2" />
                  <rect x="17" y="12" width="4" height="9" rx="1" /><rect x="43" y="12" width="4" height="9" rx="1" />
                </template>
                <rect v-else-if="look(agent.id).body === 'cassette'" x="6" y="20" width="52" height="42" rx="5" />
                <template v-else-if="look(agent.id).body === 'acid'">
                  <circle cx="32" cy="38" r="24" /><path d="M17 52 C17 62 24 62 24 55 Z" /><path d="M40 57 C40 67 47 66 46 55 Z" />
                </template>
                <template v-else-if="look(agent.id).body === 'alien'">
                  <path d="M32 12 C50 12 58 24 56 38 C54 52 42 64 32 64 C22 64 10 52 8 38 C6 24 14 12 32 12 Z" />
                  <path d="M21 16 L14 4" class="stalk" /><circle cx="14" cy="4" r="3" /><path d="M43 16 L50 4" class="stalk" /><circle cx="50" cy="4" r="3" />
                </template>
                <template v-else>
                  <path d="M13 30 L15 12 L27 22 Z" /><path d="M51 30 L49 12 L37 22 Z" />
                  <circle cx="32" cy="40" r="23" />
                </template>
              </g>
              <ellipse class="sheen" cx="22" :cy="look(agent.id).body === 'cassette' || look(agent.id).body === 'boombox' ? 26 : 24" rx="7" ry="3.5" transform-origin="22 24" transform="rotate(-28)" />

              <!-- body details -->
              <g v-if="look(agent.id).body === 'boombox'" class="boombox">
                <line class="aerial" x1="52" y1="21" x2="60" y2="3" /><circle cx="60" cy="3" r="2" class="tip" />
                <g class="speaker"><circle cx="18" cy="51" r="7.5" /><circle cx="18" cy="51" r="3.5" class="cone" /></g>
                <g class="speaker"><circle cx="46" cy="51" r="7.5" /><circle cx="46" cy="51" r="3.5" class="cone" /></g>
              </g>
              <g v-else-if="look(agent.id).body === 'cassette'" class="cassette">
                <rect class="label" x="11" y="25" width="42" height="21" rx="3" /><rect class="stripe" x="11" y="25" width="42" height="5" rx="2" />
                <path class="foot" d="M17 62 L20 54 H44 L47 62" />
              </g>

              <!-- headwear and gear -->
              <g class="acc" :transform="`translate(0 ${headOffset(agent.id)})`">
                <g v-if="look(agent.id).accessory === 'cap'" class="cap">
                  <path class="brim" d="M46 15 L60 17 Q61 20 58 20 L45 18 Z" /><path d="M16 17 C16 3 48 3 48 17 Z" /><path class="band" d="M16 16 H48" /><circle cx="32" cy="5" r="2" />
                </g>
                <g v-else-if="look(agent.id).accessory === 'beanie'" class="beanie">
                  <path d="M15 18 C15 2 49 2 49 18 Z" /><rect class="cuff" x="14" y="13" width="36" height="6" rx="3" /><circle class="pom" cx="32" cy="2" r="3.5" />
                </g>
                <g v-else-if="look(agent.id).accessory === 'mohawk'" class="mohawk">
                  <path d="M23 17 L22 5 L27 13 L30 1 L33 12 L37 3 L39 13 L44 7 L42 17 Z" />
                </g>
              </g>
              <g v-if="look(agent.id).accessory === 'headphones'" class="phones-acc">
                <path class="band" d="M8 40 C8 6 56 6 56 40" /><rect x="3" y="32" width="10" height="15" rx="4" /><rect x="51" y="32" width="10" height="15" rx="4" />
              </g>
              <g v-else-if="look(agent.id).accessory === 'chain'" class="chain">
                <path d="M20 54 Q32 63 44 54" /><circle cx="32" cy="60" r="3.2" />
              </g>

              <!-- face -->
              <g class="face">
                <!-- a cassette's eyes are its reels -->
                <template v-if="look(agent.id).body === 'cassette' && agent.status !== 'failed'">
                  <g v-for="cx in [24, 40]" :key="cx" class="reel"><circle :cx="cx" cy="37" r="5.5" /><path :d="`M${cx - 4} 37 H${cx + 4} M${cx} 33 V41`" /></g>
                </template>
                <template v-else-if="agent.status === 'completed'">
                  <!-- shades pushed up for a nap -->
                  <g v-if="look(agent.id).eyes === 'shades' || look(agent.id).eyes === 'aviators'" transform="translate(0 -11)" class="pushed-up">
                    <path class="lens" d="M15 32 H49 V35 Q48 42 41 42 Q35 42 34 36 H30 Q29 42 23 42 Q16 42 15 35 Z" />
                  </g>
                  <path class="lid" d="M20 38 Q24 41 28 38" /><path class="lid" d="M36 38 Q40 41 44 38" />
                </template>
                <template v-else-if="agent.status === 'failed'">
                  <path class="lid" d="M21 34 L27 40 M27 34 L21 40" /><path class="lid" d="M37 34 L43 40 M43 34 L37 40" />
                </template>
                <template v-else-if="look(agent.id).eyes === 'shades'">
                  <path class="lens" d="M15 32 H49 V35 Q48 42 41 42 Q35 42 34 36 H30 Q29 42 23 42 Q16 42 15 35 Z" />
                  <path class="glint" d="M18 35 L22 33 M37 35 L41 33" />
                </template>
                <template v-else-if="look(agent.id).eyes === 'aviators'">
                  <path class="aviator" d="M16 33 H30 Q30 43 23 43 Q16 43 16 33 Z" /><path class="aviator" d="M34 33 H48 Q48 43 41 43 Q34 43 34 33 Z" />
                  <path class="bridge" d="M30 34 Q32 32 34 34" />
                </template>
                <template v-else-if="look(agent.id).eyes === 'visor'">
                  <rect class="visor" x="14" y="31" width="36" height="10" rx="5" /><rect class="scan" x="17" y="34.5" width="9" height="3" rx="1.5" />
                </template>
                <template v-else>
                  <circle class="eye-white" cx="24" cy="37" r="5.5" /><circle class="eye-white" cx="40" cy="37" r="5.5" />
                  <circle class="pupil" cx="25" cy="38" r="2.6" /><circle class="pupil" cx="41" cy="38" r="2.6" />
                </template>
                <circle v-if="look(agent.id).body !== 'boombox'" class="cheek" cx="17" cy="46" r="3" /><circle v-if="look(agent.id).body !== 'boombox'" class="cheek" cx="47" cy="46" r="3" />
                <path v-if="agent.status === 'running' && !talking(agent.id)" class="smirk" :d="look(agent.id).body === 'cassette' ? 'M27 52 Q33 55 37 51' : 'M27 48 Q33 51 37 47'" />
                <ellipse v-else class="mouth" cx="32" :cy="look(agent.id).body === 'cassette' ? 52 : 48" rx="4" ry="2.2" />
              </g>
            </g>
          </svg>
        </template>
        <span v-if="agent.status === 'completed'" class="zzz" aria-hidden="true">z<span>z</span></span>
        <span v-if="agent.status === 'completed' && talking(agent.id)" class="cheer" aria-hidden="true"><i v-for="n in 8" :key="n" :style="{ '--a': `${n * 45}deg` }" /></span>
        <span class="name">{{ agent.name }}</span>
      </button>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from 'vue'
import CircusFigure from '~/components/agents/CircusFigure.vue'
import '~/composables/agents/zen/circus.css'
import { backdropInfo, backdropSvg, type CircusBackdropId } from '~/composables/agents/zen/circusBackdrops'
import { resolveCircusBackdrop } from '~/composables/agents/zen/useCircusBackdrop'
import { circusLeadSpeech, circusSpeech } from '~/utils/agents/circusCharacters'
import { characterFor, speechFor, type SubagentCharacter } from '~/utils/agents/subagentCharacters'

export interface ZenSubagent {
  id: string
  name: string
  status: string
  lines: string[]
  error?: string
  parentId?: string
}

/** The session's own agent, which launched the subagents. */
export interface ZenLead {
  name: string
  /** The session status: 'processing' while it works. */
  status: string
  lines: string[]
  avatarImage?: string | null
  color?: string | null
}

export type ZenStage = 'circus' | 'synthwave'

const props = withDefaults(defineProps<{ agents: ZenSubagent[]; lead?: ZenLead | null; stage?: ZenStage; sessionId?: string | null }>(), { lead: null, stage: 'circus', sessionId: null })
const circus = computed(() => props.stage === 'circus')
// The same backdrop the session's Zen shows (auto per session, or the one you picked)
const backdrop = computed<CircusBackdropId>(() => resolveCircusBackdrop(props.sessionId || ''))
const backdropHtml = computed(() => (circus.value ? backdropSvg(backdrop.value) : ''))
const backdropBoard = computed(() => backdropInfo(backdrop.value).board)
const emit = defineEmits<{ select: [agentId: string]; lead: [] }>()

// The main agent stands on a podium up stage; its subagents work below it.
// On the Flying Circus stage the main agent stands on the hills, so it sits lower.
const LEAD = { x: 50, get y() { return circus.value ? 55 : 31 } }
const LEAD_ID = '\u0000lead'
const avatarBroken = ref(false)
watch(() => props.lead?.avatarImage, () => { avatarBroken.value = false })
const leadColor = computed(() => props.lead?.color || '#8B5CF6')
const leadWorking = computed(() => props.lead?.status === 'processing')
const leadInitial = computed(() => (props.lead?.name || '?').trim().charAt(0).toUpperCase() || '?')
const leadSpeech = computed(() => {
  const lead = props.lead
  if (!lead) return { glyph: '', text: '' }
  const running = props.agents.filter(a => a.status === 'running').length
  if (circus.value) return circusLeadSpeech(leadWorking.value, running, leadWorking.value ? speechFor(lead.lines, 'running') : undefined)
  if (leadWorking.value) {
    const said = speechFor(lead.lines, 'running')
    return said.text === 'Getting started…' ? { glyph: '✦', text: 'Thinking…' } : said
  }
  if (running) return { glyph: '◔', text: `Waiting on ${running} subagent${running === 1 ? '' : 's'}…` }
  return { glyph: '☾', text: 'All quiet. Waiting for you.' }
})

/** Running subagents hang on a line to whoever launched them. */
const tethered = computed(() => props.agents
  .filter(a => a.status === 'running')
  .map(a => ({ id: a.id, nested: !!a.parentId && props.agents.some(p => p.id === a.parentId && p.status === 'running') }))
  .filter(t => t.nested || !!props.lead))
const tethers = new Map<string, SVGLineElement>()
function setTether(id: string, el: any) {
  if (el) { tethers.set(id, el); const w = walkers.get(id); if (w) drawTether(id, w) } else tethers.delete(id)
}
function drawTether(id: string, walker: Walker) {
  const line = tethers.get(id)
  if (!line) return
  const agent = props.agents.find(a => a.id === id)
  const parent = agent?.parentId ? walkers.get(agent.parentId) : undefined
  const from = parent && props.agents.some(p => p.id === agent!.parentId && p.status === 'running')
    ? { x: parent.x, y: parent.y - 4 }
    : { x: LEAD.x, y: LEAD.y + 1 }
  line.setAttribute('x1', String(from.x))
  line.setAttribute('y1', String(from.y))
  line.setAttribute('x2', String(walker.x))
  line.setAttribute('y2', String(walker.y - 4))
}

const looks = new Map<string, SubagentCharacter>()
const look = (id: string) => {
  let character = looks.get(id)
  if (!character) looks.set(id, character = characterFor(id))
  return character
}
// Each body is drawn twice: an outline layer, then the fill.
const LAYERS = ['outline', 'skin'] as const
/** Headwear is drawn for a head whose top is at y=14; move it to this body's head. */
const HEAD_TOP: Record<string, number> = { bean: 12, boombox: 12, cassette: 20, acid: 14, alien: 12, cat: 16 }
const headOffset = (id: string) => (HEAD_TOP[look(id).body] ?? 14) - 14
const speech = (agent: ZenSubagent) => {
  const said = speechFor(agent.lines, agent.status, agent.error)
  return circus.value ? circusSpeech(said, agent.status, agent.error) : said
}
const statusWord = (status: string) => status === 'running' ? 'working' : status === 'completed' ? 'done' : status
const restingCount = computed(() => props.agents.filter(a => a.status !== 'running').length)

// Flying Circus: when a subagent fails, a giant foot comes down on it.
const foot = ref({ x: 70, y: 60, down: false })
let footTimer: ReturnType<typeof setTimeout> | undefined
function stomp(id: string) {
  if (!circus.value || reducedMotion || foot.value.down) return
  const walker = walkers.get(id)
  if (!walker) return
  foot.value = { x: walker.x, y: walker.y, down: true }
  footTimer = setTimeout(() => { foot.value = { ...foot.value, down: false } }, 900)
}
watch(() => props.agents.map(a => `${a.id}\u0000${a.status}`), (next, prev) => {
  if (!prev) return
  const was = new Map(prev.map(k => k.split('\u0000') as [string, string]))
  for (const key of next) {
    const [id, status] = key.split('\u0000') as [string, string]
    if (status === 'failed' && was.get(id) === 'running') { stomp(id); break }
  }
})

// A character "talks" (mouth moving) for a moment whenever it says something new.
const TALK_MS = 2200
const saidAt = ref<Record<string, number>>({})
const now = ref(Date.now())
const talking = (id: string) => now.value - (saidAt.value[id] || 0) < TALK_MS
function startTalking(id: string) {
  saidAt.value[id] = Date.now()
  now.value = Date.now()
  if (!ticker && mounted) ticker = setInterval(tick, 400)
}
// The clock only ticks while someone is talking, so a quiet stage doesn't
// re-render every 400 ms.
let ticker: ReturnType<typeof setInterval> | undefined
function tick() {
  now.value = Date.now()
  if (!Object.values(saidAt.value).some(at => now.value - at < TALK_MS)) stopTicker()
}
function stopTicker() {
  if (ticker) clearInterval(ticker)
  ticker = undefined
}
watch(() => props.agents.map(a => `${a.id}\u0000${speech(a).text}`), (next, prev) => {
  const before = new Set(prev || [])
  for (const key of next) if (!before.has(key)) startTalking(key.split('\u0000')[0]!)
})
watch(() => leadSpeech.value.text, (text, was) => { if (was !== undefined && text !== was) startTalking(LEAD_ID) })

// Movement lives outside Vue's reactivity: each frame writes positions
// straight to the elements, so a crowd of subagents stays smooth.
interface Walker { x: number; y: number; tx: number; ty: number; turnAt: number }
const walkers = new Map<string, Walker>()
const elements = new Map<string, HTMLElement>()
const reducedMotion = typeof window !== 'undefined' && !!window.matchMedia?.('(prefers-reduced-motion: reduce)').matches
let frame = 0
let lastFrame = 0
let mounted = false

function setElement(id: string, el: any) {
  if (el) { elements.set(id, el); place(id); startLoop() } else { elements.delete(id); walkers.delete(id) }
}

/**
 * Run the animation loop. It stops by itself once every subagent has finished
 * and is resting in place, and is started again when subagents come, go or
 * change status.
 */
function startLoop() {
  if (!mounted || frame || reducedMotion || typeof requestAnimationFrame === 'undefined') return
  lastFrame = 0
  frame = requestAnimationFrame(step)
}

const rand = (min: number, max: number) => min + Math.random() * (max - min)

/** Where a finished subagent rests: a place on the bench along the bottom. */
function restSpot(id: string) {
  const resting = props.agents.filter(a => a.status !== 'running')
  const index = Math.max(0, resting.findIndex(a => a.id === id))
  // Spread one row across the bench; a second row sits further back.
  const perRow = Math.max(8, Math.min(14, resting.length))
  return { x: 11 + (index % perRow + 0.5) * (84 / perRow), y: 93 - Math.floor(index / perRow) * 9 }
}

/**
 * Each working subagent has a home area of its own, spread over the stage, and
 * wanders around it, so the crowd never piles up in one spot. Nested subagents
 * live next to the subagent that launched them. Also the still layout used
 * with reduced motion.
 */
function homeOf(id: string): { x: number; y: number; w: number; h: number } {
  const agent = props.agents.find(a => a.id === id)
  const parent = agent?.parentId ? props.agents.find(a => a.id === agent.parentId && a.status === 'running') : undefined
  if (parent && parent.id !== id) {
    const home = homeOf(parent.id)
    const siblings = props.agents.filter(a => a.status === 'running' && a.parentId === parent.id)
    const index = Math.max(0, siblings.findIndex(a => a.id === id))
    const side = index % 2 ? -1 : 1
    return { x: Math.min(92, Math.max(8, home.x + side * home.w * 0.32)), y: Math.min(80, home.y + 5 + Math.floor(index / 2) * 5), w: home.w * 0.3, h: home.h * 0.3 }
  }
  const leads = props.agents.filter(a => a.status === 'running' && !(a.parentId && props.agents.some(p => p.id === a.parentId && p.status === 'running')))
  const index = Math.max(0, leads.findIndex(a => a.id === id))
  const count = Math.max(1, leads.length)
  const cols = Math.min(count, Math.max(1, Math.ceil(Math.sqrt(count * 1.8))))
  const rows = Math.ceil(count / cols)
  // Below the main agent's podium when there is one.
  const top = props.lead ? (circus.value ? 66 : 50) : 36
  const w = 84 / cols
  const h = (82 - top) / rows
  return { x: 8 + (index % cols + 0.5) * w, y: top + (Math.floor(index / cols) + 0.5) * h, w, h }
}

function pickTarget(agent: ZenSubagent, walker: Walker, t: number) {
  const home = homeOf(agent.id)
  walker.tx = Math.min(93, Math.max(7, home.x + rand(-0.35, 0.35) * home.w))
  walker.ty = Math.min(84, Math.max(props.lead ? (circus.value ? 64 : 47) : 32, home.y + rand(-0.35, 0.35) * home.h))
  walker.turnAt = t + rand(2500, 7000)
}

/** Nudge working subagents apart when two end up on top of each other. */
function keepApart(dt: number) {
  const running = props.agents.filter(a => a.status === 'running').map(a => walkers.get(a.id)).filter((w): w is Walker => !!w)
  for (let i = 0; i < running.length; i++) {
    for (let j = i + 1; j < running.length; j++) {
      const a = running[i]!, b = running[j]!
      const dx = (b.x - a.x) / 9, dy = (b.y - a.y) / 7 // roughly a character's footprint
      const d = Math.hypot(dx, dy)
      if (d >= 1 || d === 0) continue
      const push = (1 - d) * 6 * dt
      a.x -= (dx / d) * push; a.y -= (dy / d) * push
      b.x += (dx / d) * push; b.y += (dy / d) * push
    }
  }
}

function place(id: string) {
  const agent = props.agents.find(a => a.id === id)
  const el = elements.get(id)
  if (!agent || !el) return
  let walker = walkers.get(id)
  if (!walker) {
    const parent = agent.parentId ? walkers.get(agent.parentId) : undefined
    // Subagents launched while you watch step out from whoever launched them.
    const start = reducedMotion
      ? (agent.status === 'running' ? homeOf(id) : restSpot(id))
      : parent ? { x: parent.x, y: parent.y }
        : agent.status === 'running' && mounted && props.lead ? { x: LEAD.x, y: LEAD.y + 3 }
          : agent.status === 'running' ? homeOf(id) : restSpot(id)
    walker = { ...start, tx: start.x, ty: start.y, turnAt: 0 }
    walkers.set(id, walker)
  }
  draw(id, el, walker, false)
}

function draw(id: string, el: HTMLElement, walker: Walker, moving: boolean, facing?: number) {
  drawTether(id, walker)
  el.style.left = `${walker.x}%`
  el.style.top = `${walker.y}%`
  el.style.zIndex = String(Math.round(walker.y * 10))
  el.classList.toggle('walking', moving)
  if (facing) el.classList.toggle('facing-left', facing < 0)
}

function step(t: number) {
  const dt = Math.min(0.1, (t - (lastFrame || t)) / 1000)
  lastFrame = t
  keepApart(dt)
  let anyMoving = false
  let anyRunning = false
  for (const agent of props.agents) {
    const walker = walkers.get(agent.id)
    const el = elements.get(agent.id)
    if (!walker || !el) continue
    if (agent.status === 'running') {
      if (t >= walker.turnAt) pickTarget(agent, walker, t)
    } else {
      const spot = restSpot(agent.id)
      walker.tx = spot.x
      walker.ty = spot.y
    }
    const dx = walker.tx - walker.x
    const dy = walker.ty - walker.y
    const distance = Math.hypot(dx, dy)
    // Resting subagents stroll to the bench a little faster than they wander.
    const pace = look(agent.id).pace * (agent.status === 'running' ? 1 : 2.5)
    const moving = distance > 0.3
    if (moving) anyMoving = true
    if (agent.status === 'running') anyRunning = true
    if (moving) {
      const move = Math.min(distance, pace * dt)
      walker.x += (dx / distance) * move
      walker.y += (dy / distance) * move
    } else if (agent.status === 'running') {
      walker.turnAt = Math.min(walker.turnAt, t + rand(600, 2200)) // pause, then wander on
    }
    draw(agent.id, el, walker, moving, Math.abs(dx) > 0.5 ? dx : undefined)
  }
  // Working subagents keep wandering; once everyone rests, stop the loop.
  frame = anyMoving || anyRunning ? requestAnimationFrame(step) : 0
}

// Without motion, re-lay the scene out whenever subagents come, go or finish.
// With motion, make sure the loop runs so they walk to their new places.
watch(() => props.agents.map(a => `${a.id}:${a.status}`).join('|'), () => {
  if (!reducedMotion) { startLoop(); return }
  for (const agent of props.agents) {
    const walker = walkers.get(agent.id)
    const el = elements.get(agent.id)
    if (!walker || !el) continue
    const spot = agent.status === 'running' ? homeOf(agent.id) : restSpot(agent.id)
    Object.assign(walker, { x: spot.x, y: spot.y, tx: spot.x, ty: spot.y })
    draw(agent.id, el, walker, false)
  }
  // Parents may have moved after their children were drawn.
  nextTick(() => { for (const [id, w] of walkers) drawTether(id, w) })
})

onMounted(() => {
  mounted = true
  startLoop()
})
onUnmounted(() => {
  mounted = false
  stopTicker()
  if (footTimer) clearTimeout(footTimer)
  if (frame) cancelAnimationFrame(frame)
  frame = 0
})
</script>

<style scoped>
.zen-stage { position: relative; flex: 1; min-height: 0; overflow: hidden; border-radius: 14px; background: #07040f; isolation: isolate; }
.zen-sky { position: absolute; inset: 0; background:
  radial-gradient(1px 1px at 12% 18%, #fff8 50%, transparent 51%), radial-gradient(1px 1px at 34% 8%, #fff6 50%, transparent 51%),
  radial-gradient(1.5px 1.5px at 58% 22%, #fff7 50%, transparent 51%), radial-gradient(1px 1px at 76% 12%, #fff5 50%, transparent 51%),
  radial-gradient(1px 1px at 88% 28%, #fff6 50%, transparent 51%), radial-gradient(1px 1px at 22% 30%, #fff4 50%, transparent 51%),
  radial-gradient(ellipse 70% 45% at 50% 28%, #8b5cf62e, transparent 70%), linear-gradient(#07040f, #140a2a 58%, #1b0d33); }
.zen-floor { position: absolute; left: -20%; right: -20%; bottom: 0; height: 70%; transform: perspective(420px) rotateX(58deg); transform-origin: bottom;
  background-image: linear-gradient(#ff4fd855 1px, transparent 1px), linear-gradient(90deg, #22d3ee40 1px, transparent 1px); background-size: 56px 56px;
  mask-image: linear-gradient(transparent, #000 45%); animation: floor-drift 12s linear infinite; }
@keyframes floor-drift { to { background-position: 0 56px; } }
.zen-bench { position: absolute; left: 3%; right: 3%; bottom: 2.5%; height: 12%; border-top: 1px dashed #c87dec55; pointer-events: none; }
.zen-bench span { position: absolute; top: -22px; left: 0; font: 700 10px 'JetBrains Mono', monospace; letter-spacing: .24em; text-transform: uppercase; color: #d9fbff;
  text-shadow: 0 0 4px #22d3ee, 0 0 12px #22d3ee; animation: neon 9s steps(1) infinite -3s; }
/* A whisper of CRT: scanlines and a vignette over the whole stage. */
.zen-stage::after { content: ''; position: absolute; inset: 0; z-index: 20000; pointer-events: none;
  background: repeating-linear-gradient(transparent 0 2px, #00000014 2px 3px), radial-gradient(ellipse at center, transparent 55%, #000a); }

.critter-slot { position: absolute; left: 50%; top: 60%; width: 0; height: 0; }
/* Depth order is set inline each frame; the one you point at comes to the front. */
.critter-slot.is-talking { z-index: 9000 !important; }
.critter-slot:hover, .critter-slot:focus-within { z-index: 10000 !important; }
.critter { position: absolute; left: 0; bottom: 0; transform: translateX(-50%); display: flex; flex-direction: column; align-items: center; background: none; border: 0; padding: 0; cursor: pointer; color: #fff; font: inherit; }
.critter:focus-visible { outline: 2px solid var(--c); outline-offset: 6px; border-radius: 12px; }
.figure { width: 68px; height: 76px; overflow: visible; }
.nested .figure { width: 50px; height: 56px; }
.facing-left .figure { transform: scaleX(-1); }
.shadow { fill: #000; opacity: .45; }
.outline > *, .outline path, .outline rect, .outline circle { fill: #12082a; stroke: #12082a; stroke-width: 5; stroke-linejoin: round; }
.skin > *, .skin path, .skin rect, .skin circle { fill: var(--c); }
.outline .stalk { fill: none; stroke-width: 6; stroke-linecap: round; } .skin .stalk { fill: none; stroke: var(--c); stroke-width: 2.5; stroke-linecap: round; }
.sheen { fill: #fff; opacity: .28; pointer-events: none; }
.aerial { stroke: #12082a; stroke-width: 2.5; stroke-linecap: round; } .tip { fill: var(--acc); }
.speaker circle { fill: #12082a; } .speaker .cone { fill: color-mix(in srgb, var(--acc) 70%, #12082a); }
.speaker { transform-box: fill-box; transform-origin: center; }
.cassette .label { fill: #f6f1ff; } .cassette .stripe { fill: var(--acc); } .cassette .foot { fill: color-mix(in srgb, var(--c) 70%, #12082a); stroke: #12082a; stroke-width: 1.5; }
.reel circle { fill: #12082a; } .reel path { stroke: #f6f1ff; stroke-width: 1.6; stroke-linecap: round; } .reel { transform-box: fill-box; transform-origin: center; }
.acc path, .acc circle, .acc rect { stroke: #12082a; stroke-width: 1.6; stroke-linejoin: round; }
.cap path { fill: #1b1030; } .cap .brim { fill: var(--acc); } .cap .band { fill: none; stroke: var(--acc); stroke-width: 2.2; } .cap circle { fill: var(--acc); }
.beanie path { fill: var(--acc); } .beanie .cuff { fill: color-mix(in srgb, var(--acc) 70%, #12082a); } .beanie .pom { fill: #fff; }
.mohawk path { fill: var(--acc); filter: drop-shadow(0 0 3px var(--acc)); }
.phones-acc .band { fill: none; stroke: #12082a; stroke-width: 4.5; stroke-linecap: round; }
.phones-acc rect { fill: #1b1030; stroke: var(--acc); stroke-width: 2; filter: drop-shadow(0 0 3px var(--acc)); }
.chain path { fill: none; stroke: #ffd36e; stroke-width: 2.4; stroke-dasharray: 2 1.6; stroke-linecap: round; } .chain circle { fill: #ffd36e; stroke: #b7791f; stroke-width: 1; }
.lens { fill: #0d0718; stroke: #12082a; stroke-width: 1; } .glint { stroke: #fff; stroke-width: 1.4; stroke-linecap: round; opacity: .85; }
.pushed-up .lens { opacity: .9; }
.aviator { fill: color-mix(in srgb, var(--acc) 55%, #12082a); stroke: #ffd36e; stroke-width: 1.4; } .bridge { fill: none; stroke: #ffd36e; stroke-width: 1.4; }
.visor { fill: #0d0718; stroke: #12082a; stroke-width: 1; } .scan { fill: var(--acc); filter: drop-shadow(0 0 3px var(--acc)); }
.is-running .scan { animation: scan 1.3s ease-in-out infinite alternate; }
@keyframes scan { to { transform: translateX(21px); } }
.is-running .reel { animation: spin 1.4s linear infinite; }
.lead-working .is-running .speaker { animation: thump .5s ease-in-out infinite alternate; }
@keyframes thump { to { transform: scale(1.12); } }
.eye-white { fill: #fff; } .pupil { fill: #140a24; } .cheek { fill: #fff; opacity: .28; }
.lid { fill: none; stroke: #140a24; stroke-width: 2.4; stroke-linecap: round; }
.smirk { fill: none; stroke: #140a24; stroke-width: 2.2; stroke-linecap: round; }
.mouth { fill: #140a24; transform-box: fill-box; transform-origin: center; transform: scaleY(.45); }
.talking .mouth { animation: chatter .24s ease-in-out infinite alternate; }
@keyframes chatter { to { transform: scaleY(1.25); } }

.bob { transform-box: fill-box; transform-origin: bottom; }
.walking .bob { animation: waddle .42s ease-in-out infinite alternate; }
@keyframes waddle { from { transform: translateY(0) rotate(-4deg); } to { transform: translateY(-3px) rotate(4deg); } }
.is-running:not(.walking) .bob { animation: breathe 2.4s ease-in-out infinite; }
@keyframes breathe { 50% { transform: scaleY(.96); } }
/* Neon: working subagents glow in their colour, and move to the main agent's beat. */
.is-running .figure { filter: drop-shadow(0 0 7px color-mix(in srgb, var(--c) 65%, transparent)); }
.lead-working .is-running:not(.walking) .bob { animation: groove .5s ease-in-out infinite alternate; }
@keyframes groove { from { transform: translateY(0) rotate(-3deg); } to { transform: translateY(-4px) rotate(3deg); } }
.is-completed .name { max-width: 84px; }

.is-completed .figure { filter: saturate(.55) brightness(.85); } .is-completed .bob { transform: scaleY(.9); }
.is-failed .figure { filter: grayscale(.85) brightness(.75); } .is-failed .bob { transform: rotate(-12deg) translateY(4px); }

.bubble { position: absolute; bottom: calc(100% + 6px); max-width: 220px; width: max-content; padding: 6px 9px; border-radius: 10px; background: #12082acc; color: #f3eaff;
  border: 1px solid color-mix(in srgb, var(--c, #c87dec) 75%, transparent); backdrop-filter: blur(6px);
  font: 10px/1.35 'JetBrains Mono', monospace; text-align: left; box-shadow: 0 0 14px color-mix(in srgb, var(--c, #c87dec) 35%, transparent), 0 4px 18px #0008;
  animation: pop .32s cubic-bezier(.3, 1.6, .5, 1); transform-origin: bottom center; pointer-events: none; transition: opacity .4s; }
/* Clamp on an unpadded inner box so no sliver of a third line shows; paths wrap anywhere. */
.say { display: -webkit-box; -webkit-line-clamp: 2; -webkit-box-orient: vertical; overflow: hidden; overflow-wrap: anywhere; }
/* What was said a while ago steps back a little, so fresh speech stands out. */
.is-running:not(.talking) .bubble { opacity: .72; }
.bubble::after { content: ''; position: absolute; left: 50%; bottom: -5px; margin-left: -5px; border: 5px solid transparent; border-bottom: 0; border-top-color: color-mix(in srgb, var(--c, #c87dec) 75%, transparent); }
.glyph { color: var(--c); text-shadow: 0 0 6px var(--c); font-weight: 700; margin-right: 6px; }
.lead-bubble { --c: #ff4fd8; }
/* Finished ones say "Done!" for a moment, then nod off. */
.is-completed:not(.talking) .bubble, .is-failed .bubble { opacity: 0; transition: opacity .3s; }
.critter:hover .bubble, .critter:focus-visible .bubble { opacity: 1; z-index: 1; }
.is-failed .bubble { --c: #ff5a6e; }
@keyframes pop { from { transform: scale(.4) translateY(8px); opacity: 0; } }

.name { margin-top: 2px; max-width: 110px; font: 9px 'JetBrains Mono', monospace; color: #e6dcff; opacity: .75; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
.zzz { position: absolute; top: -2px; right: -6px; font: 700 12px 'JetBrains Mono', monospace; color: #c87dec; animation: drift 2.6s ease-in infinite; }
.zzz span { font-size: 9px; margin-left: 2px; }
@keyframes drift { from { transform: translate(0, 4px); opacity: 0; } 30% { opacity: 1; } to { transform: translate(8px, -14px); opacity: 0; } }


/* A neon skyline on the horizon, its centre left open for the main agent. Black is see-through (screen). */
.zen-city { position: absolute; left: 50%; top: 42%; width: min(100%, 1500px); aspect-ratio: 1536 / 300; max-height: 30%; transform: translate(-50%, -100%);
  background: url('/zen/skyline.webp') center bottom / contain no-repeat; mix-blend-mode: screen; opacity: .75; pointer-events: none;
  -webkit-mask-image: linear-gradient(90deg, transparent, #000 8% 92%, transparent); mask-image: linear-gradient(90deg, transparent, #000 8% 92%, transparent); }
.lead-working .zen-city { animation: city-flicker 6s steps(1) infinite; }
@keyframes city-flicker { 0%, 100% { opacity: .75; } 48% { opacity: .68; } 50% { opacity: .8; } }
.zen-city::after { content: ''; position: absolute; left: 0; right: 0; bottom: -1px; height: 2px; background: linear-gradient(90deg, transparent, #ff4fd8, #22d3ee, #ff4fd8, transparent); box-shadow: 0 0 14px #ff4fd8; }

/* ── The main agent ─────────────────────────────────────────────── */
.zen-sun { position: absolute; left: 50%; top: 42%; width: min(320px, 50vh); aspect-ratio: 1; transform: translate(-50%, -100%); border-radius: 50%; pointer-events: none;
  background: linear-gradient(#ffd36e, #f59e43 40%, #ec609a 72%, var(--lead));
  -webkit-mask-image: linear-gradient(#000 52%, transparent 52% 56%, #000 56% 63%, transparent 63% 68%, #000 68% 74%, transparent 74% 80%, #000 80% 85%, transparent 85%);
  mask-image: linear-gradient(#000 52%, transparent 52% 56%, #000 56% 63%, transparent 63% 68%, #000 68% 74%, transparent 74% 80%, #000 80% 85%, transparent 85%);
  opacity: .22; filter: blur(.3px) drop-shadow(0 0 40px #ec609a); transition: opacity 1.2s; }
.lead-working .zen-sun { opacity: .42; animation: sun-glow 5s ease-in-out infinite; }
@keyframes sun-glow { 50% { opacity: .3; } }
.lead-slot { position: absolute; width: 0; height: 0; z-index: 400; }
/* Circus: the subagents walk on the floor in front of the main agent; keep its name plate on top. */
.stage-circus .lead-slot { z-index: 1200; }
.lead-slot:hover, .lead-slot:focus-within { z-index: 10001; }
.lead { position: absolute; left: 0; bottom: 0; transform: translateX(-50%); display: flex; flex-direction: column; align-items: center; background: none; border: 0; padding: 0; cursor: pointer; color: #fff; font: inherit; }
.lead:focus-visible { outline: 2px solid var(--lead); outline-offset: 8px; border-radius: 16px; }
.crown { width: 34px; height: 19px; margin-bottom: -7px; position: relative; z-index: 2; filter: drop-shadow(0 2px 6px #f59e4399); transform: rotate(-8deg) translateX(10px); transition: transform .4s; }
.crown path { fill: #ffd36e; stroke: #b7791f; stroke-width: 1; stroke-linejoin: round; } .crown circle { fill: #fff3c4; }
.lead:hover .crown { transform: rotate(0) translate(10px, -3px); }
.portrait { position: relative; width: 84px; height: 84px; border-radius: 50%; display: grid; place-items: center; background: radial-gradient(circle at 35% 30%, color-mix(in srgb, var(--lead) 70%, #fff), var(--lead) 60%, #1b0d33);
  box-shadow: 0 0 0 3px #07040f, 0 0 0 5px var(--lead), 0 0 34px color-mix(in srgb, var(--lead) 70%, transparent); }
.portrait img { width: 100%; height: 100%; border-radius: 50%; object-fit: cover; position: relative; }
.initial { font: 700 34px 'JetBrains Mono', monospace; color: #fff; text-shadow: 0 2px 10px #0006; position: relative; }
.halo { position: absolute; inset: -12px; border-radius: 50%; border: 2px dashed color-mix(in srgb, var(--lead) 80%, #fff); opacity: .35; }
.is-working .halo { opacity: .9; animation: spin 9s linear infinite; }
.is-working .portrait { animation: lead-breathe 2.6s ease-in-out infinite; }
@keyframes spin { to { transform: rotate(1turn); } }
@keyframes lead-breathe { 50% { transform: translateY(-3px); } }
.phones { position: absolute; inset: -9px; width: calc(100% + 18px); height: calc(100% + 18px); overflow: visible; }
.phones path { fill: none; stroke: #1a1030; stroke-width: 7; stroke-linecap: round; } .phones rect { fill: #1a1030; stroke: #ff4fd8; stroke-width: 2.5; filter: drop-shadow(0 0 4px #ff4fd8); }
.deck { position: relative; display: flex; align-items: center; justify-content: space-between; gap: 8px; width: 150px; height: 30px; margin-top: 10px; padding: 0 8px; border-radius: 8px;
  background: linear-gradient(#2a1748, #160b2b); border: 1px solid #ff4fd8aa; box-shadow: 0 0 18px #ff4fd855, inset 0 1px 0 #ffffff22; }
.vinyl { width: 24px; height: 24px; border-radius: 50%; flex-shrink: 0; background: radial-gradient(circle, #ffd36e 0 3px, #0a0612 3.5px 5px, #24183a 5.5px 6.5px, #0a0612 7px 9px, #24183a 9.5px 10px, #0a0612 10.5px); box-shadow: 0 0 0 1px #ffffff1a; }
.vinyl::after { content: ''; display: block; width: 100%; height: 100%; border-radius: 50%; background: conic-gradient(transparent 0 20%, #ffffff30 25%, transparent 30% 70%, #ffffff20 75%, transparent 80%); }
.is-working .vinyl { animation: spin 1.6s linear infinite; }
.eq { display: flex; align-items: flex-end; gap: 3px; height: 16px; }
.eq i { width: 4px; height: 30%; border-radius: 1px; background: linear-gradient(#22d3ee, #ff4fd8); box-shadow: 0 0 6px #22d3ee88; }
.is-working .eq i { animation: eq .5s ease-in-out infinite alternate; }
.eq i:nth-child(2) { animation-delay: -.2s !important; } .eq i:nth-child(3) { animation-delay: -.35s !important; } .eq i:nth-child(4) { animation-delay: -.1s !important; } .eq i:nth-child(5) { animation-delay: -.42s !important; }
@keyframes eq { from { height: 20%; } to { height: 100%; } }
.notes { position: absolute; inset: 0; pointer-events: none; }
.notes i { position: absolute; bottom: 60%; font-style: normal; font-size: 13px; color: #22d3ee; text-shadow: 0 0 8px #22d3ee; opacity: 0; animation: note 3s ease-out infinite; }
.notes i:nth-child(1) { left: 6%; } .notes i:nth-child(2) { left: 46%; color: #ff4fd8; text-shadow: 0 0 8px #ff4fd8; animation-delay: 1s; } .notes i:nth-child(3) { right: 6%; animation-delay: 2s; }
@keyframes note { 15% { opacity: 1; } to { transform: translate(var(--dx, 14px), -64px) rotate(18deg); opacity: 0; } }
.notes i:nth-child(1) { --dx: -22px; }
.plaque { text-shadow: 0 1px 6px #07040f, 0 0 2px #07040f; display: flex; flex-direction: column; align-items: center; gap: 2px; margin-top: 6px; }
.role { font: 700 9px 'JetBrains Mono', monospace; letter-spacing: .28em; text-transform: uppercase; color: #ffe1fa;
  text-shadow: 0 0 4px #ff4fd8, 0 0 10px #ff4fd8, 0 0 22px #ff4fd8; animation: neon 7s steps(1) infinite; }
@keyframes neon { 0%, 100% { opacity: 1; } 91% { opacity: .35; } 92% { opacity: 1; } 94% { opacity: .5; } 95% { opacity: 1; } }
.lead-name { max-width: 200px; font: 800 14px 'JetBrains Mono', monospace; letter-spacing: .02em; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; text-shadow: none;
  background: linear-gradient(#ffffff, #e3d7ff 45%, #7c5cff 52%, #22d3ee); -webkit-background-clip: text; background-clip: text; color: transparent; filter: drop-shadow(0 1px 4px #07040f); }
.lead-bubble { bottom: calc(100% + 4px); max-width: 280px; font-size: 11px; }
.lead.is-idle:not(.talking) .lead-bubble { opacity: .72; }
.zen-hint { position: absolute; left: 0; right: 0; top: 58%; margin: 0; text-align: center; font: 11px 'JetBrains Mono', monospace; color: #c87decaa; pointer-events: none; }

/* ── Tethers ─────────────────────────────────────────────────────── */
.tethers { position: absolute; inset: 0; width: 100%; height: 100%; pointer-events: none; z-index: 1; overflow: visible; }
.tether { stroke: var(--c); stroke-width: 1.4; stroke-dasharray: 2 6; stroke-linecap: round; opacity: .3; }
.tether.nested { stroke-width: 1; opacity: .4; }
.lead-working .tether { opacity: .55; animation: flow 1.1s linear infinite; }
@keyframes flow { to { stroke-dashoffset: -16; } }

/* ── A subagent that just finished cheers ──────────────────────── */
.cheer { position: absolute; left: 50%; top: 42%; width: 0; height: 0; }
.cheer i { position: absolute; width: 5px; height: 5px; border-radius: 1px; background: var(--c); transform: rotate(var(--a)) translateY(0); animation: burst .9s ease-out forwards; }
.cheer i:nth-child(odd) { background: #ffd36e; }
@keyframes burst { to { transform: rotate(var(--a)) translateY(-34px) scale(.3); opacity: 0; } }


/* ══ Flying Circus stage ═══════════════════════════════════════════
   A paper collage: teal sky, scalloped clouds, cut-out hills, aged paper
   ground. Figures move in steps, like cut-out animation. */
.stage-circus { background: #efe4c8; color: #2b1d10; }
.stage-circus::after { background: radial-gradient(ellipse at center, transparent 62%, #2b1d1026); }
/* Scenery: the session's backdrop (1600 x 900, bottom-anchored) over the top of
   the stage; its boards start at 756/900 of its height, and the floor carries
   them on down to the front of the stage. */
.circus-scenery { inset: 0 0 auto 0 !important; height: 58% !important; z-index: 0; contain: layout paint; }
.circus-scenery :deep(.cc-backdrop) { position: absolute; inset: 0; width: 100%; height: 100%; }
.circus-floor { position: absolute; inset: 58% 0 0 0; z-index: 0;
  background:
    linear-gradient(#2b1d1038, transparent 10px),
    repeating-linear-gradient(90deg, transparent 0 calc(10% - 2px), #2b1d1030 calc(10% - 2px) 10%),
    linear-gradient(color-mix(in srgb, var(--board) 92%, #2b1d10), var(--board));
  box-shadow: inset 0 -30px 40px -20px #2b1d1040; }
.circus-bench { position: absolute; left: 4%; right: 4%; bottom: 3%; height: 11%; pointer-events: none;
  border-top: 5px solid #8a6510; border-bottom: 5px solid #8a6510; background: repeating-linear-gradient(90deg, transparent 0 60px, #8a651022 60px 64px); }
.circus-bench span { position: absolute; top: -34px; left: 0; padding: 3px 10px; rotate: -2deg; background: #fbf5e6; border: 2px solid #2b1d10;
  box-shadow: 3px 4px 0 #2b1d1033; font: 700 11px 'Courier Prime', 'Courier New', monospace; letter-spacing: .12em; text-transform: uppercase; color: #2b1d10; }
.circus-cherub { position: absolute; top: 9%; left: -12%; width: 92px; z-index: 300; pointer-events: none; animation: cherub-fly 18s steps(72) infinite; }
@keyframes cherub-fly { 0% { left: -12%; translate: 0 0; } 50% { translate: 0 -14px; } 100% { left: 108%; translate: 0 0; } }
.circus-foot { position: absolute; top: -60%; width: 150px; translate: -55% 0; z-index: 15000; pointer-events: none;
  filter: drop-shadow(6px 8px 0 #2b1d1030); transition: top .55s cubic-bezier(.4, 0, .2, 1); }
.circus-foot.stomping { top: calc(var(--land) - 255px); transition: top .16s cubic-bezier(.7, 0, 1, .5); }

.stage-circus .tether { stroke: #2b1d10; stroke-width: 1.2; stroke-dasharray: 3 5; opacity: .35; }
.stage-circus.lead-working .tether { opacity: .5; }
.stage-circus .cfig { display: block; }
.stage-circus .facing-left .cfig { transform: scaleX(-1); }
.stage-circus .critter { color: #2b1d10; }
.stage-circus .bubble { background: #fbf5e6; color: #2b1d10; border: 2px solid #2b1d10; border-radius: 3px; backdrop-filter: none;
  box-shadow: 3px 4px 0 #2b1d1033; font: 11px/1.35 'Courier Prime', 'Courier New', monospace; }
.stage-circus .bubble::after { border-top-color: #2b1d10; }
.stage-circus .glyph { color: #c2185b; text-shadow: none; }
.stage-circus .name { color: #2b1d10; font: 10px 'Courier Prime', 'Courier New', monospace; background: #fbf5e6e6; padding: 0 4px; border: 1px solid #2b1d1055; }
.stage-circus .zzz { color: #2b1d10; font-family: 'Courier Prime', 'Courier New', monospace; }
.stage-circus .zen-hint { color: #5c4630; font-family: 'Courier Prime', 'Courier New', monospace; }
.stage-circus .cheer i { background: #e8458b; } .stage-circus .cheer i:nth-child(odd) { background: #d4a017; }

/* The main agent on its soapbox */
.circus-lead { width: 92px; display: block; margin-bottom: -6px; position: relative; z-index: 1;
  filter: drop-shadow(5px 6px 0 #2b1d1026); transform-origin: 50% 100%; }
.stage-circus .lead.is-working .circus-lead { animation: orate 1.2s steps(3) infinite; }
@keyframes orate { 0% { rotate: -2deg; } 50% { rotate: 2deg; translate: 0 -3px; } 100% { rotate: -1deg; } }
.soapbox { position: relative; display: grid; place-items: center; width: 150px; height: 46px; border: 2px solid #2b1d10;
  background: repeating-linear-gradient(0deg, #c9a66b 0 13px, #b8925a 13px 15px); box-shadow: 4px 5px 0 #2b1d1033; }
.crate-label { font: 700 11px 'Courier Prime', 'Courier New', monospace; letter-spacing: .14em; text-transform: uppercase; color: #2b1d10;
  background: #fbf5e6cc; padding: 1px 8px; border: 1.5px solid #2b1d10; rotate: -2deg; }
.cameo { position: absolute; left: -16px; top: -14px; width: 34px; height: 40px; border-radius: 50%; object-fit: cover; border: 2px solid #2b1d10; outline: 3px solid #d4a017; }
.stage-circus .plaque { text-shadow: none; }
.stage-circus .role { display: none; }
.stage-circus .lead-name { background: #fbf5e6; color: #2b1d10; filter: none; font: 800 15px 'Fraunces', 'EB Garamond', Georgia, serif; padding: 1px 10px; border: 1.5px solid #2b1d10; box-shadow: 3px 3px 0 #2b1d1026; margin-top: 4px; }
.stage-circus .lead-bubble { --c: #2b1d10; font-size: 12px; max-width: 300px; }

@media (prefers-reduced-motion: reduce) {
  .zen-city, .zen-floor, .circus-cherub, .circus-lead, .bob, .mouth, .scan, .reel, .speaker, .zzz, .bubble, .zen-sun, .halo, .portrait, .tether, .cheer, .vinyl, .eq i, .notes, .role, .zen-bench span { animation: none !important; }
  .notes { display: none; }
  .cheer { display: none; }
}
</style>
