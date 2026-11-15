/**
 * Galaxy — the session as a living spiral galaxy.
 *
 *  - The agent is the galactic core. While it works the core brightens, the
 *    disk spins up and streams of light drift inward along the arms.
 *  - Every file the agent touches becomes a named star. Its directory picks the
 *    spiral arm and the distance from the core, so related files cluster.
 *    Reads send a cyan comet from the core to the star; edits a magenta one that
 *    ignites the star when it lands.
 *  - Shell commands send a shockwave through the disk, searches sweep it like a
 *    radar, web calls fire a comet out past the rim, errors flash red.
 *  - Subagents are satellite clusters orbiting the core; when one finishes it
 *    spirals in and merges.
 *  - The user's voice rolls out from the core as rings of light.
 *  - Fly mode (galaxyFlight.ts): pilot a glider through it all; file stars
 *    you pass light up with their names.
 *
 * Everything moves on the GPU (TSL on WebGPURenderer): particles hold static
 * orbital elements and the vertex stage computes positions from a handful of
 * uniforms, so a frame costs the CPU next to nothing.
 */
import type { FrameInfo, SceneContext, ZenGpuScene, ZenToolEvent, ToolCategory } from './types'
import { hash01, hexToRgb01 } from './types'
import { createGalaxyFlight } from './galaxyFlight'
import { zenFlyState } from './flyState'
import { getZenAudio } from '~/composables/useZenAudio'

// ── Layout ──────────────────────────────────────────────────────────────────
const ARMS = 4
const DISK_RADIUS = 56
const SCALE_LENGTH = 15
const PITCH = 2.3 // 1 / tan(pitch angle) — arm winding (lower = more open spiral)
const MAX_FILES = 192
const MAX_COMETS = 14
const TRAIL = 42
const MAX_SATS = 6
const SAT_PARTICLES = 520
const SHOCKS = 4
const VOICE_SAMPLES = 48
const VOICE_DT = 1 / 30
const VOICE_SPEED = 42
const COMET_TIME = 1.05

const CATEGORY_COLOR: Record<ToolCategory, number> = {
  read: 0x5ee7ff,
  edit: 0xff4fd8,
  bash: 0xffb347,
  search: 0x4dffc3,
  web: 0xffe066,
  agent: 0xb18cff,
  message: 0xcbb8ff,
  error: 0xff3b3b,
  other: 0xa99bff,
}

const SAT_COLORS = [0xb18cff, 0x5ee7ff, 0xff8fd8, 0x7dffb0, 0xffc56e, 0x8fb4ff]

/** Angle of spiral arm `arm` at radius r (logarithmic spiral). */
function armAngle(arm: number, r: number) {
  return (arm / ARMS) * Math.PI * 2 + Math.log(Math.max(r, 1) / 4) * PITCH
}

function gauss(rand: () => number) {
  // Box–Muller
  const u = Math.max(1e-6, rand())
  return Math.sqrt(-2 * Math.log(u)) * Math.cos(2 * Math.PI * rand())
}

function mulberry32(seed: number) {
  let a = seed >>> 0
  return () => {
    a = (a + 0x6d2b79f5) >>> 0
    let t = a
    t = Math.imul(t ^ (t >>> 15), t | 1)
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61)
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296
  }
}

interface FileStar {
  path: string
  slot: number
  r: number
  theta: number
  y: number
  lastTouch: number
  category: ToolCategory
}

interface Label {
  el: HTMLDivElement
  star: FileStar
  born: number
}

interface PendingArrival {
  at: number
  star: FileStar | null
  category: ToolCategory
}

export function createGalaxyScene(): ZenGpuScene {
  let THREE: any
  let T: any
  let renderer: any
  let scene: any
  let camera: any
  let pipeline: any = null
  let reducedMotion = false

  // Uniforms
  let uTime: any, uPhase: any, uActivity: any, uSat: any, uCorePulse: any, uCoreTint: any
  let uFlow: any, uSweep: any, uSweepColor: any, uVoiceOn: any, uVoiceHist: any
  let uTint: any
  const shocks: any[] = []
  const shockColors: any[] = []
  let uSats: any, uSatsB: any

  // Dynamic buffers
  let fileA: any, fileB: any
  let cometA: any, cometB: any

  const files = new Map<string, FileStar>()
  let nextFileSlot = 0
  let cometCursor = 0
  let shockCursor = 0
  const pending: PendingArrival[] = []
  const labels: Label[] = []
  let labelLayer: HTMLDivElement | null = null
  const sats = new Map<string, number>()
  const satState = Array.from({ length: MAX_SATS }, () => ({ used: false, id: '' }))

  // Animated state
  let activity = 0.15, targetActivity = 0.15
  let sat = 1, targetSat = 1
  let phase = 0
  let corePulse = 0
  let coreRed = 0
  let camIntro = 0
  let voiceAcc = 0
  let width = 1, height = 1
  let now = 0
  let generation = 0 // bumped by reset(); stale timers check it
  let projV: any = null

  // Fly mode
  let flight: ReturnType<typeof createGalaxyFlight> | null = null
  let orbitFov = 42
  let lastFov = 42
  let flyReportAt = 0
  const orbitPos = { x: 0, y: 0, z: 0 }

  function linearColor(hex: number): [number, number, number] {
    const c = new THREE.Color(hex)
    return [c.r, c.g, c.b]
  }

  // ── Builders ──────────────────────────────────────────────────────────────

  function softDisc(power = 4) {
    // Soft round sprite: bright centre, smooth falloff, zero at the edge
    const d = T.uv().sub(0.5).length().mul(2)
    return T.exp(d.mul(d).mul(-power)).mul(T.float(1).sub(T.smoothstep(0.75, 1.0, d)))
  }

  /** Brightness of shockwaves at radius r (sum of SHOCKS travelling rings). */
  function shockAt(r: any) {
    let sum: any = T.float(0)
    let tint: any = T.vec3(0)
    for (let i = 0; i < SHOCKS; i++) {
      const s = shocks[i] // x=start, y=strength, z=speed, w=width
      const age = uTime.sub(s.x)
      const front = age.mul(s.z)
      const band = T.exp(r.sub(front).div(s.w).pow(2).negate())
      const k = band.mul(s.y).mul(T.exp(age.mul(-0.55))).mul(T.step(0, age))
      sum = sum.add(k)
      tint = tint.add(shockColors[i].mul(k))
    }
    return { sum, tint }
  }

  function voiceAt(r: any) {
    const idx = r.div(VOICE_SPEED * VOICE_DT)
    const i0 = T.int(T.clamp(idx, 0, VOICE_SAMPLES - 2))
    const v = T.mix(uVoiceHist.element(i0), uVoiceHist.element(i0.add(1)), T.fract(idx))
    return v.mul(T.float(1).sub(T.clamp(idx.div(VOICE_SAMPLES), 0, 1))).mul(uVoiceOn)
  }

  function sweepAt(theta: any) {
    // uSweep: x = start time, y = strength
    const age = uTime.sub(uSweep.x)
    const head = age.mul(3.6)
    const diff = T.fract(head.sub(theta).div(Math.PI * 2))
    const trail = T.exp(diff.mul(-9)).mul(T.step(0, age)).mul(T.float(1).sub(T.smoothstep(1.4, 1.9, age)))
    return trail.mul(uSweep.y)
  }

  function buildDisk(count: number) {
    const rand = mulberry32(7)
    const A = new Float32Array(count * 4) // r, theta0, y, seed
    const B = new Float32Array(count * 4) // rgb, size

    const core = new THREE.Color(0xfff1d6)
    const warm = new THREE.Color(0xffc98a)
    const young = new THREE.Color(0x9fc3ff)
    const pink = new THREE.Color(0xff6fb5)
    const violet = new THREE.Color(0x9b7bff)
    const c = new THREE.Color()

    for (let i = 0; i < count; i++) {
      const u = rand()
      let r: number, theta: number, y: number, size: number
      const bulge = u < 0.16
      if (bulge) {
        r = Math.abs(gauss(rand)) * 4.6
        theta = rand() * Math.PI * 2
        y = gauss(rand) * 1.4 * Math.exp(-r / 6)
        c.copy(core).lerp(warm, Math.min(1, r / 6))
        size = 0.2 + rand() * 0.2
      } else {
        // Exponential disk, stars concentrated along the arms
        r = Math.min(DISK_RADIUS * 1.15, -Math.log(1 - rand() * 0.985) * SCALE_LENGTH + 2.5)
        const arm = Math.floor(rand() * ARMS)
        const armWeight = arm % 2 === 0 ? 1 : 0.7
        const spread = (0.55 + 5.5 / (r + 2)) * (1.3 - armWeight * 0.3)
        const inArm = rand() < 0.86
        theta = armAngle(arm, r) + (inArm ? gauss(rand) * spread * 0.32 : rand() * Math.PI * 2)
        y = gauss(rand) * (0.35 + r * 0.012)
        const t = Math.min(1, r / DISK_RADIUS)
        c.copy(warm).lerp(young, Math.min(1, t * 1.6))
        if (inArm && rand() < 0.07) c.copy(pink) // emission nebulae along the arms
        else if (rand() < 0.08) c.copy(violet)
        size = 0.11 + Math.pow(rand(), 9) * 0.55
      }
      const bright = bulge ? 0.55 : 0.5 + rand() * 0.5
      A.set([r, theta, y, rand()], i * 4)
      B.set([c.r * bright, c.g * bright, c.b * bright, size], i * 4)
    }

    const attrA = T.instancedBufferAttribute(new THREE.InstancedBufferAttribute(A, 4))
    const attrB = T.instancedBufferAttribute(new THREE.InstancedBufferAttribute(B, 4))

    const r = attrA.x
    const theta = attrA.y.add(uPhase).add(T.sin(uTime.mul(0.25).add(attrA.w.mul(60))).mul(0.012))
    const shock = shockAt(r)
    const voice = voiceAt(r)
    const sweep = sweepAt(T.fract(theta.div(Math.PI * 2)).mul(Math.PI * 2))
    const lift = shock.sum.mul(0.9).add(voice.mul(0.8))

    const boost = T.vec4(shock.sum, sweep, voice, 0).toVarying('vDiskBoost')
    const tint = shock.tint.toVarying('vDiskTint')

    const material = new THREE.SpriteNodeMaterial({ transparent: true, depthWrite: false, blending: THREE.AdditiveBlending })
    material.positionNode = T.vec3(T.cos(theta).mul(r), attrA.z.add(lift), T.sin(theta).mul(r))
    material.scaleNode = attrB.w.mul(T.float(1).add(boost.x.mul(0.8)).add(boost.z.mul(0.6)))
    const base = attrB.xyz.mul(T.float(0.75).add(uActivity.mul(0.35)))
    const lit = base
      .mul(T.float(1).add(boost.z.mul(2.2)))
      .add(tint.mul(1.6))
      .add(uSweepColor.mul(boost.y.mul(1.4)))
      .add(uTint.mul(0.08))
    const lum = T.dot(lit, T.vec3(0.299, 0.587, 0.114))
    material.colorNode = T.mix(T.vec3(lum), lit, uSat)
    material.opacityNode = softDisc(4.5).mul(0.9)

    const sprite = new THREE.Sprite(material)
    sprite.count = count
    sprite.frustumCulled = false
    scene.add(sprite)
  }

  function buildDust(count: number) {
    const rand = mulberry32(11)
    const A = new Float32Array(count * 4)
    for (let i = 0; i < count; i++) {
      const r = 6 + Math.pow(rand(), 0.8) * DISK_RADIUS * 0.9
      const arm = Math.floor(rand() * ARMS)
      // Dust lanes run just inside the arms
      const theta = armAngle(arm, r) - 0.28 + gauss(rand) * 0.12
      A.set([r, theta, gauss(rand) * 0.25, 2.2 + rand() * 3.2], i * 4)
    }
    const attrA = T.instancedBufferAttribute(new THREE.InstancedBufferAttribute(A, 4))
    const theta = attrA.y.add(uPhase)
    const material = new THREE.SpriteNodeMaterial({ transparent: true, depthWrite: false, blending: THREE.NormalBlending })
    material.positionNode = T.vec3(T.cos(theta).mul(attrA.x), attrA.z, T.sin(theta).mul(attrA.x))
    material.scaleNode = attrA.w
    material.colorNode = T.vec3(0.012, 0.006, 0.02)
    material.opacityNode = softDisc(3).mul(0.2)
    const sprite = new THREE.Sprite(material)
    sprite.count = count
    sprite.frustumCulled = false
    sprite.renderOrder = 2
    scene.add(sprite)
  }

  function buildCore() {
    const makeGlow = (scale: number, color: number, opacity: number, pulse: number) => {
      const m = new THREE.SpriteNodeMaterial({ transparent: true, depthWrite: false, blending: THREE.AdditiveBlending })
      m.colorNode = T.mix(T.color(color), T.color(0xff4040), uCoreTint).mul(T.float(0.85).add(uActivity.mul(0.45)))
      m.opacityNode = softDisc(3.2).mul(opacity)
      m.scaleNode = T.float(scale).mul(T.float(1).add(uCorePulse.mul(pulse)))
      const s = new THREE.Sprite(m)
      s.renderOrder = 3
      scene.add(s)
    }
    makeGlow(30, 0x5a34e0, 0.13, 0.35)
    makeGlow(10, 0xffb36b, 0.3, 0.5)
    makeGlow(3.2, 0xfff6e8, 0.75, 0.7)
  }

  /** While working: motes of light drift inward along the arms to the core. */
  function buildInflow(count: number) {
    const rand = mulberry32(21)
    const A = new Float32Array(count * 4) // seed, arm, speed, lateral offset
    for (let i = 0; i < count; i++) {
      A.set([rand(), Math.floor(rand() * ARMS), 0.5 + rand() * 0.7, gauss(rand) * 0.12], i * 4)
    }
    const a = T.instancedBufferAttribute(new THREE.InstancedBufferAttribute(A, 4))
    const f = T.fract(uTime.mul(a.z).mul(0.035).add(a.x))
    const r = T.float(DISK_RADIUS * 0.95).mul(T.pow(T.float(1).sub(f), 1.35)).add(1.2)
    // Same logarithmic spiral as the arms: theta = arm*2π/ARMS + ln(r/4)*PITCH
    const theta = a.y.mul((Math.PI * 2) / ARMS).add(T.log(r.div(4)).mul(PITCH)).add(a.w).add(uPhase)
    const material = new THREE.SpriteNodeMaterial({ transparent: true, depthWrite: false, blending: THREE.AdditiveBlending })
    material.positionNode = T.vec3(T.cos(theta).mul(r), T.sin(uTime.add(a.x.mul(30))).mul(0.15), T.sin(theta).mul(r))
    material.scaleNode = T.float(0.32).add(f.mul(0.2))
    material.colorNode = T.mix(T.color(0xffc47a), T.color(0xfff4e6), f).mul(1.3)
    // Fade in at the rim, out at the core; only while the agent works
    const edge = T.smoothstep(0, 0.12, f).mul(T.float(1).sub(T.smoothstep(0.82, 1, f)))
    material.opacityNode = softDisc(4).mul(edge).mul(uFlow).mul(0.8)
    const sprite = new THREE.Sprite(material)
    sprite.count = count
    sprite.frustumCulled = false
    scene.add(sprite)
  }

  function buildBackground(count: number) {
    const rand = mulberry32(3)
    const A = new Float32Array(count * 4)
    for (let i = 0; i < count; i++) {
      const u = rand() * 2 - 1
      const t = rand() * Math.PI * 2
      const s = Math.sqrt(1 - u * u)
      A.set([Math.cos(t) * s * 420, u * 420, Math.sin(t) * s * 420, rand()], i * 4)
    }
    const a = T.instancedBufferAttribute(new THREE.InstancedBufferAttribute(A, 4))
    const material = new THREE.SpriteNodeMaterial({ transparent: true, depthWrite: false, blending: THREE.AdditiveBlending })
    material.positionNode = a.xyz
    material.scaleNode = T.float(0.9).add(a.w.mul(1.6))
    const twinkle = T.float(0.55).add(T.sin(uTime.mul(T.float(0.6).add(a.w.mul(2.2))).add(a.w.mul(40))).mul(0.45))
    material.colorNode = T.mix(T.color(0xbfc8ff), T.color(0xffe2c4), a.w).mul(twinkle.mul(0.55))
    material.opacityNode = softDisc(5)
    const sprite = new THREE.Sprite(material)
    sprite.count = count
    sprite.frustumCulled = false
    scene.add(sprite)

    // Faint nebula wash behind everything
    const uvn = T.screenUV
    const n = T.mx_fractal_noise_float(T.vec3(uvn.mul(T.vec2(2.4, 1.6)), uTime.mul(0.006)), 3, 2.0, 0.5)
    const glow = T.float(1).sub(uvn.sub(T.vec2(0.5, 0.52)).length().mul(1.5)).clamp(0, 1)
    const neb = T.mix(T.color(0x0a0420), T.color(0x2a0a3a), T.clamp(n.mul(0.9).add(0.35), 0, 1))
    scene.backgroundNode = neb.mul(glow.mul(0.9).add(0.12)).add(T.color(0x020106))
  }

  function buildFileStars() {
    const A = new Float32Array(MAX_FILES * 4).fill(0) // r, theta0, y, flareStart
    const B = new Float32Array(MAX_FILES * 4).fill(0) // rgb, size (0 = unused)
    for (let i = 0; i < MAX_FILES; i++) A[i * 4 + 3] = -1000
    const attrA = new THREE.InstancedBufferAttribute(A, 4)
    const attrB = new THREE.InstancedBufferAttribute(B, 4)
    attrA.setUsage(THREE.DynamicDrawUsage)
    attrB.setUsage(THREE.DynamicDrawUsage)
    fileA = attrA
    fileB = attrB
    const a = T.instancedBufferAttribute(attrA)
    const b = T.instancedBufferAttribute(attrB)
    const theta = a.y.add(uPhase)
    const age = uTime.sub(a.w)
    const flare = T.exp(age.mul(-1.6)).mul(T.step(0, age)).toVarying('vFlare')
    const twinkle = T.float(0.85).add(T.sin(uTime.mul(2.1).add(a.x.mul(3.7))).mul(0.15))
    const material = new THREE.SpriteNodeMaterial({ transparent: true, depthWrite: false, blending: THREE.AdditiveBlending })
    material.positionNode = T.vec3(T.cos(theta).mul(a.x), a.z, T.sin(theta).mul(a.x))
    material.scaleNode = b.w.mul(T.float(1).add(flare.mul(4.5)))
    material.colorNode = b.xyz.mul(twinkle.add(flare.mul(3.5))).mul(2.2)
    material.opacityNode = softDisc(3.6)
    const sprite = new THREE.Sprite(material)
    sprite.count = MAX_FILES
    sprite.frustumCulled = false
    sprite.renderOrder = 4
    scene.add(sprite)
  }

  function buildComets() {
    const n = MAX_COMETS * TRAIL
    const A = new Float32Array(n * 4) // targetR, targetTheta, targetY, start
    const B = new Float32Array(n * 4) // rgb, j/TRAIL
    for (let i = 0; i < n; i++) {
      A[i * 4 + 3] = -1000
      B[i * 4 + 3] = (i % TRAIL) / TRAIL
    }
    cometA = new THREE.InstancedBufferAttribute(A, 4)
    cometB = new THREE.InstancedBufferAttribute(B, 4)
    cometA.setUsage(THREE.DynamicDrawUsage)
    cometB.setUsage(THREE.DynamicDrawUsage)
    const a = T.instancedBufferAttribute(cometA)
    const b = T.instancedBufferAttribute(cometB)

    const age = uTime.sub(a.w)
    const s = age.div(COMET_TIME).sub(b.w.mul(0.42))
    const visible = T.step(0, s).mul(T.step(s, 1))
    const t = T.clamp(s, 0, 1)
    const eased = t.mul(t).mul(T.float(3).sub(t.mul(2)))
    // Quadratic bezier in the rotating disk frame: core → arc above → target
    const thetaT = a.y.add(uPhase)
    const thetaM = a.y.sub(0.7).add(uPhase)
    const p0 = T.vec3(0, 0.6, 0)
    const p1 = T.vec3(T.cos(thetaM).mul(a.x.mul(0.55)), T.float(9).add(a.x.mul(0.12)), T.sin(thetaM).mul(a.x.mul(0.55)))
    const p2 = T.vec3(T.cos(thetaT).mul(a.x), a.z, T.sin(thetaT).mul(a.x))
    const omt = T.float(1).sub(eased)
    const pos = p0.mul(omt.mul(omt)).add(p1.mul(omt.mul(eased).mul(2))).add(p2.mul(eased.mul(eased)))
    const fade = T.pow(T.float(1).sub(b.w), 1.6)

    const material = new THREE.SpriteNodeMaterial({ transparent: true, depthWrite: false, blending: THREE.AdditiveBlending })
    material.positionNode = pos
    material.scaleNode = T.float(1.25).mul(fade).mul(visible).add(0.0001)
    material.colorNode = b.xyz.mul(T.float(0.6).add(fade.mul(2.4)))
    material.opacityNode = softDisc(4).mul(visible).mul(fade)
    const sprite = new THREE.Sprite(material)
    sprite.count = n
    sprite.frustumCulled = false
    sprite.renderOrder = 5
    scene.add(sprite)
  }

  function buildSatellites() {
    const rand = mulberry32(99)
    const n = MAX_SATS * SAT_PARTICLES
    const A = new Float32Array(n * 4) // sat index, offset xyz
    for (let i = 0; i < n; i++) {
      const s = Math.floor(i / SAT_PARTICLES)
      const rr = Math.abs(gauss(rand)) * 1.3
      const u = rand() * 2 - 1
      const t = rand() * Math.PI * 2
      const q = Math.sqrt(1 - u * u)
      A.set([s, Math.cos(t) * q * rr, u * rr * 0.6, Math.sin(t) * q * rr], i * 4)
    }
    const a = T.instancedBufferAttribute(new THREE.InstancedBufferAttribute(A, 4))
    // uSats[i]: orbit radius, inclination, phase0, alpha. uSatsB[i]: spawn time, merge start (-1), angular speed, colour index
    const idx = T.int(a.x)
    const sa = uSats.element(idx)
    const sb = uSatsB.element(idx)
    const mergeT = T.select(sb.y.greaterThan(0), T.clamp(uTime.sub(sb.y).div(3.2), 0, 1), T.float(0))
    const R = sa.x.mul(T.float(1).sub(mergeT.mul(mergeT)))
    const ang = sa.z.add(uTime.sub(sb.x).mul(sb.z).mul(T.float(1).add(mergeT.mul(5))))
    const spawnT = T.clamp(uTime.sub(sb.x).div(1.2), 0, 1)
    const center = T.vec3(T.cos(ang).mul(R), T.sin(ang).mul(R).mul(T.sin(sa.y)), T.sin(ang).mul(R).mul(T.cos(sa.y)))
    const pos = center.add(a.yzw.mul(T.float(1).sub(mergeT.mul(0.7))).mul(spawnT))
    const palette = T.uniformArray(SAT_COLORS.map(h => new THREE.Color(h)), 'color')
    const col = palette.element(T.int(sb.w))

    const material = new THREE.SpriteNodeMaterial({ transparent: true, depthWrite: false, blending: THREE.AdditiveBlending })
    material.positionNode = pos
    material.scaleNode = T.float(0.38).mul(sa.w)
    material.colorNode = col.mul(T.float(1.6).add(spawnT.oneMinus().mul(3)))
    material.opacityNode = softDisc(4).mul(sa.w)
    const sprite = new THREE.Sprite(material)
    sprite.count = n
    sprite.frustumCulled = false
    scene.add(sprite)
  }

  // ── Events ────────────────────────────────────────────────────────────────

  function fireShock(color: number, strength = 1, speed = 38, width = 3.2) {
    const s = shocks[shockCursor]
    s.value.set(now, strength, speed, width)
    const c = new THREE.Color(color)
    shockColors[shockCursor].value.set(c.r, c.g, c.b)
    shockCursor = (shockCursor + 1) % SHOCKS
  }

  function starFor(path: string): FileStar {
    const existing = files.get(path)
    if (existing) return existing
    const slash = path.lastIndexOf('/')
    const dir = slash > 0 ? path.slice(0, slash) : '.'
    const name = slash >= 0 ? path.slice(slash + 1) : path
    const arm = Math.floor(hash01(dir) * ARMS)
    const band = 0.12 + hash01(dir, 1) * 0.72 + (hash01(name, 2) - 0.5) * 0.1
    const r = 7 + Math.min(1, Math.max(0, band)) * (DISK_RADIUS - 10)
    const theta = armAngle(arm, r) + (hash01(name, 3) - 0.5) * 0.35
    const y = (hash01(name, 4) - 0.5) * 1.1

    // Reuse the least recently touched slot when full
    let slot = nextFileSlot
    if (files.size >= MAX_FILES) {
      let oldest: FileStar | null = null
      for (const f of files.values()) if (!oldest || f.lastTouch < oldest.lastTouch) oldest = f
      files.delete(oldest!.path)
      slot = oldest!.slot
    } else {
      nextFileSlot++
    }
    const star: FileStar = { path, slot, r, theta, y, lastTouch: now, category: 'read' }
    files.set(path, star)
    fileA.array.set([r, theta, y, -1000], slot * 4)
    const [cr, cg, cb] = linearColor(0xdfe8ff)
    fileB.array.set([cr, cg, cb, 0.75 + hash01(name, 5) * 0.4], slot * 4)
    fileA.needsUpdate = true
    fileB.needsUpdate = true
    return star
  }

  function igniteStar(star: FileStar, category: ToolCategory, strength: number) {
    star.lastTouch = now
    star.category = category
    const [r, g, b] = linearColor(CATEGORY_COLOR[category])
    // Keep the flare colour on the star afterwards, softened toward white
    fileB.array.set([r * 0.6 + 0.35, g * 0.6 + 0.35, b * 0.6 + 0.35], star.slot * 4)
    fileA.array[star.slot * 4 + 3] = now - (1 - strength) * 1.2
    fileA.needsUpdate = true
    fileB.needsUpdate = true
    showLabel(star, category)
  }

  function launchComet(r: number, theta: number, y: number, category: ToolCategory) {
    const base = cometCursor * TRAIL
    const [cr, cg, cb] = linearColor(CATEGORY_COLOR[category])
    for (let j = 0; j < TRAIL; j++) {
      cometA.array.set([r, theta, y, now], (base + j) * 4)
      cometB.array.set([cr, cg, cb], (base + j) * 4)
    }
    cometA.needsUpdate = true
    cometB.needsUpdate = true
    cometCursor = (cometCursor + 1) % MAX_COMETS
  }

  function showLabel(star: FileStar, category: ToolCategory) {
    if (!labelLayer) return
    const existing = labels.find(l => l.star === star)
    if (existing) {
      existing.born = now
      existing.el.style.setProperty('--c', `#${CATEGORY_COLOR[category].toString(16).padStart(6, '0')}`)
      return
    }
    if (labels.length >= 6) {
      const old = labels.shift()!
      old.el.remove()
    }
    const el = document.createElement('div')
    el.className = 'zen-galaxy-label'
    const name = star.path.split('/').pop() || star.path
    el.textContent = name
    el.title = star.path
    el.style.setProperty('--c', `#${CATEGORY_COLOR[category].toString(16).padStart(6, '0')}`)
    labelLayer.appendChild(el)
    labels.push({ el, star, born: now })
  }

  function updateLabels() {
    if (!labelLayer || !labels.length) return
    const halfW = width / 2
    const halfH = height / 2
    for (let i = labels.length - 1; i >= 0; i--) {
      const l = labels[i]!
      const age = now - l.born
      if (age > 5.5) {
        l.el.remove()
        labels.splice(i, 1)
        continue
      }
      const th = l.star.theta + phase
      if (!projV) projV = new THREE.Vector3()
      const v = projV.set(Math.cos(th) * l.star.r, l.star.y, Math.sin(th) * l.star.r).project(camera)
      const x = v.x * halfW + halfW
      const y = -v.y * halfH + halfH
      const opacity = Math.min(1, age * 3) * Math.min(1, (5.5 - age) / 1.2)
      l.el.style.transform = `translate(${x.toFixed(1)}px, ${y.toFixed(1)}px)`
      l.el.style.opacity = v.z < 1 ? opacity.toFixed(3) : '0'
    }
  }

  /** While flying: name the file stars the ship passes, and report to the HUD. */
  function flyAwareness(time: number) {
    if (!flight?.active) return
    const sp = flight.position()
    let nearest: FileStar | null = null
    let best = 14 * 14
    for (const f of files.values()) {
      const th = f.theta + phase
      const dx = Math.cos(th) * f.r - sp.x, dy = f.y - sp.y, dz = Math.sin(th) * f.r - sp.z
      const d2 = dx * dx + dy * dy + dz * dz
      if (d2 < 10 * 10) showLabel(f, f.category)
      if (d2 < best) { best = d2; nearest = f }
    }
    if (time - flyReportAt > 0.12) {
      flyReportAt = time
      const info = flight.info()
      zenFlyState.speed = info.speed
      zenFlyState.throttle = info.throttle
      zenFlyState.near = nearest ? (nearest.path.split('/').pop() || nearest.path) : ''
    }
  }

  function setFlying(on: boolean) {
    if (!flight) return
    if (on) flight.start(camera.position)
    else flight.stop()
    zenFlyState.active = on
    zenFlyState.near = ''
  }

  // ── Scene API ─────────────────────────────────────────────────────────────

  return {
    id: 'galaxy',
    canFly: true,
    setFlying,

    async build(ctx: SceneContext) {
      THREE = ctx.THREE
      T = ctx.TSL as any
      renderer = ctx.renderer
      reducedMotion = ctx.reducedMotion

      scene = new THREE.Scene()
      camera = new THREE.PerspectiveCamera(42, 1, 0.5, 2000)

      uTime = T.uniform(0)
      uPhase = T.uniform(0)
      uActivity = T.uniform(0.15)
      uSat = T.uniform(1)
      uCorePulse = T.uniform(0)
      uCoreTint = T.uniform(0)
      uFlow = T.uniform(0)
      uSweep = T.uniform(new THREE.Vector2(-100, 0))
      uSweepColor = T.uniform(new THREE.Color(CATEGORY_COLOR.search))
      uVoiceOn = T.uniform(0)
      uVoiceHist = T.uniformArray(new Array(VOICE_SAMPLES).fill(0), 'float')
      uTint = T.uniform(new THREE.Color(0x000000))
      for (let i = 0; i < SHOCKS; i++) {
        shocks.push(T.uniform(new THREE.Vector4(-100, 0, 38, 3)))
        shockColors.push(T.uniform(new THREE.Color(0xffffff)))
      }
      uSats = T.uniformArray(Array.from({ length: MAX_SATS }, () => new THREE.Vector4(0, 0, 0, 0)), 'vec4')
      uSatsB = T.uniformArray(Array.from({ length: MAX_SATS }, () => new THREE.Vector4(0, -1, 0, 0)), 'vec4')

      const big = ctx.webgpu
      buildBackground(big ? 2600 : 1600)
      buildDisk(big ? 170000 : 80000)
      buildDust(big ? 9000 : 5000)
      buildInflow(big ? 5000 : 2500)
      buildSatellites()
      buildCore()
      buildFileStars()
      buildComets()

      flight = createGalaxyFlight({
        THREE, T, scene, canvas: renderer.domElement, reducedMotion,
        onExitRequest: () => setFlying(false),
        onPickup: (_points, gold) => {
          corePulse = Math.min(1.3, corePulse + (gold ? 0.6 : 0.25))
          try { getZenAudio().playToolBip(gold ? 'Edit' : 'Read') } catch { /* sound off */ }
        },
      })

      // Bloom
      try {
        const { bloom } = await import('three/addons/tsl/display/BloomNode.js')
        const scenePass = T.pass(scene, camera)
        const color = scenePass.getTextureNode('output')
        const glow = bloom(color, 0.7, 0.45, 0.14)
        pipeline = new THREE.RenderPipeline(renderer, color.add(glow))
      } catch (err) {
        console.warn('[galaxy] bloom unavailable, rendering directly:', err)
        pipeline = null
      }
    },

    enter(overlay: HTMLElement) {
      labelLayer = document.createElement('div')
      labelLayer.className = 'zen-galaxy-labels'
      overlay.appendChild(labelLayer)
      camIntro = 0
    },

    leave() {
      if (flight?.active) setFlying(false)
      labelLayer?.remove()
      labelLayer = null
      labels.length = 0
    },

    reset() {
      generation++
      files.clear()
      nextFileSlot = 0
      pending.length = 0
      for (const l of labels) l.el.remove()
      labels.length = 0
      if (fileA && fileB) {
        fileB.array.fill(0)
        fileA.array.fill(0)
        for (let i = 0; i < MAX_FILES; i++) fileA.array[i * 4 + 3] = -1000
        fileA.needsUpdate = true
        fileB.needsUpdate = true
      }
      if (cometA) {
        for (let i = 0; i < MAX_COMETS * TRAIL; i++) cometA.array[i * 4 + 3] = -1000
        cometA.needsUpdate = true
      }
      sats.clear()
      for (let i = 0; i < MAX_SATS; i++) {
        satState[i] = { used: false, id: '' }
        if (uSats) (uSats.array[i] as any).w = 0
      }
      if (uSweep) uSweep.value.set(-100, 0)
      for (const sh of shocks) sh.value.set(-100, 0, 38, 3)
      coreRed = 0
      corePulse = 0.6
    },

    resize(w: number, h: number) {
      width = w
      height = h
      camera.aspect = w / h
      // Keep the whole disk in view on narrow screens
      orbitFov = w / h < 1 ? 58 : 42
      camera.fov = orbitFov
      lastFov = orbitFov
      camera.updateProjectionMatrix()
    },

    frame(info: FrameInfo) {
      const { dt, time } = info
      now = time
      const motion = reducedMotion ? 0.3 : 1
      const ease = (k: number) => 1 - Math.exp(-k * dt)

      activity += (targetActivity - activity) * ease(1.4)
      sat += (targetSat - sat) * ease(1)
      corePulse = Math.max(0, corePulse - dt * 1.6)
      coreRed = Math.max(0, coreRed - dt * 0.9)
      phase += dt * (0.018 + activity * 0.05) * motion

      // Voice history → rings rolling out of the core
      const hist = uVoiceHist.array as number[]
      voiceAcc += dt
      while (voiceAcc >= VOICE_DT) {
        voiceAcc -= VOICE_DT
        for (let i = VOICE_SAMPLES - 1; i > 0; i--) hist[i] = hist[i - 1]!
        hist[0] = info.voice
      }

      uTime.value = time
      uPhase.value = phase
      uActivity.value = activity
      uSat.value = sat
      uVoiceOn.value = info.voiceOn
      uFlow.value = Math.max(0, activity - 0.3) / 0.7
      uCorePulse.value = Math.min(1.5, corePulse + info.voice * 0.9 + Math.pow(Math.max(0, Math.sin(time * 2.2)), 16) * activity * 0.4)
      uCoreTint.value = coreRed

      // Arrivals
      for (let i = pending.length - 1; i >= 0; i--) {
        const p = pending[i]!
        if (time >= p.at) {
          pending.splice(i, 1)
          if (p.star) igniteStar(p.star, p.category, p.category === 'edit' ? 1 : 0.55)
          else corePulse = Math.min(1.2, corePulse + 0.25)
        }
      }

      // Camera: fly-in, slow orbit, lean in while working
      camIntro = Math.min(1, camIntro + dt / 2.2)
      const intro = 1 - Math.pow(1 - camIntro, 3)
      const az = time * 0.022 * motion + 0.6
      const elev = 0.8 + Math.sin(time * 0.05) * 0.04 * motion + (1 - intro) * 0.35
      const dist = (126 - activity * 10) + (1 - intro) * 140
      orbitPos.x = Math.cos(az) * Math.cos(elev) * dist
      orbitPos.y = Math.sin(elev) * dist
      orbitPos.z = Math.sin(az) * Math.cos(elev) * dist
      // Aim below the core so it sits a little above centre, clear of the HUD cards
      const fv = flight?.update(dt, time) || null
      if (fv) {
        // Glide between the orbit view and the chase camera
        const b = fv.blend
        camera.position.set(
          orbitPos.x + (fv.pos.x - orbitPos.x) * b,
          orbitPos.y + (fv.pos.y - orbitPos.y) * b,
          orbitPos.z + (fv.pos.z - orbitPos.z) * b,
        )
        camera.lookAt(fv.look.x * b, -14 + (fv.look.y + 14) * b, fv.look.z * b)
        if (fv.bank) camera.rotateZ(fv.bank)
        const fov = orbitFov + (fv.fov - orbitFov) * b
        if (Math.abs(fov - lastFov) > 0.05) {
          camera.fov = fov
          lastFov = fov
          camera.updateProjectionMatrix()
        }
        flyAwareness(time)
      } else {
        camera.position.set(orbitPos.x, orbitPos.y, orbitPos.z)
        camera.lookAt(0, -14, 0)
        if (lastFov !== orbitFov) {
          camera.fov = orbitFov
          lastFov = orbitFov
          camera.updateProjectionMatrix()
        }
      }

      updateLabels()

      if (pipeline) pipeline.render()
      else renderer.render(scene, camera)
    },

    setStatus(status: string) {
      targetActivity = status === 'processing' ? 1 : status === 'ended' ? 0 : 0.15
      targetSat = status === 'ended' ? 0.3 : 1
    },

    setProjectColor(hex: string) {
      const rgb = hexToRgb01(hex)
      if (!uTint) return
      if (rgb) uTint.value.setRGB(rgb[0], rgb[1], rgb[2], THREE.SRGBColorSpace)
      else uTint.value.setRGB(0, 0, 0)
    },

    toolEvent(e: ZenToolEvent) {
      switch (e.category) {
        case 'read':
        case 'edit': {
          if (e.filePath) {
            const star = starFor(e.filePath)
            launchComet(star.r, star.theta, star.y, e.category)
            pending.push({ at: now + COMET_TIME * 0.92, star, category: e.category })
          } else {
            fireShock(CATEGORY_COLOR[e.category], 0.6)
          }
          break
        }
        case 'bash':
          fireShock(CATEGORY_COLOR.bash, 1)
          corePulse = Math.min(1.2, corePulse + 0.35)
          break
        case 'search':
          uSweep.value.set(now, 1)
          break
        case 'web': {
          const theta = hash01(e.label || String(now)) * Math.PI * 2 - phase
          launchComet(DISK_RADIUS * 1.35, theta, 4, 'web')
          pending.push({ at: now + COMET_TIME, star: null, category: 'web' })
          break
        }
        case 'error':
          coreRed = 1
          corePulse = 1
          fireShock(CATEGORY_COLOR.error, 1.2, 30)
          break
        case 'agent':
          corePulse = Math.min(1.2, corePulse + 0.4)
          break
        default:
          corePulse = Math.min(1.2, corePulse + 0.2)
      }
    },

    sendPulse() {
      corePulse = 1.3
      fireShock(0xd9ccff, 1.5, 46, 4.5)
    },

    agentSpawn(id: string) {
      if (sats.has(id)) return
      const slot = satState.findIndex(s => !s.used)
      if (slot < 0) return
      satState[slot] = { used: true, id }
      sats.set(id, slot)
      const R = 64 + hash01(id) * 22
      const incl = 0.25 + hash01(id, 1) * 0.9
      const speed = (0.09 + hash01(id, 2) * 0.05) * (hash01(id, 3) < 0.5 ? 1 : -1)
      ;(uSats.array[slot] as any).set(R, incl, hash01(id, 4) * Math.PI * 2, 1)
      ;(uSatsB.array[slot] as any).set(now, -1, speed, slot % SAT_COLORS.length)
      corePulse = Math.min(1.2, corePulse + 0.4)
    },

    agentComplete(id: string) {
      const slot = sats.get(id)
      if (slot === undefined) return
      ;(uSatsB.array[slot] as any).y = now
      sats.delete(id)
      const gen = generation
      setTimeout(() => {
        if (gen !== generation) return // the scene was reset meanwhile
        ;(uSats.array[slot] as any).w = 0
        satState[slot] = { used: false, id: '' }
        corePulse = 1.2
        fireShock(SAT_COLORS[slot % SAT_COLORS.length]!, 0.9, 34)
      }, 3200)
    },
  }
}
