/**
 * Zen fly mode — the renderer-independent part: input, flight model, chase
 * camera, the glide between orbit and chase views, the contrail buffer and
 * the orb chase game. Each scene supplies bounds and draws the ship/orbs with
 * its own renderer (Waves: WebGLRenderer, Galaxy: WebGPURenderer + TSL).
 *
 * The flight model follows Git City's fly mode (~/projects/git-city,
 * public/explore.js simFly/flyCam):
 *  - W/↑ nose down, S/↓ nose up; A/D ←/→ bank to turn; release and she levels
 *    out. Shift/E faster, Q slower. Dives gain speed, climbs bleed it.
 *  - A soft bound banks her back toward the middle; a hard bound stops her.
 *  - Springy chase camera: drag to look (drifts back), wheel to zoom, FOV
 *    widens with speed, the horizon banks a little with the ship.
 *
 * Orbs: glowing orbs drift around the flight area. Fly through one to collect
 * it: 10 points (gold: 50), multiplied by a combo that grows while you keep
 * collecting within a few seconds of each other (up to ×5).
 */
import { zenFlyState, saveFlyBest } from './flyState'

export const TRAIL = 72
export const ORBS = 7

const GAME_KEYS = new Set([
  'KeyW', 'KeyA', 'KeyS', 'KeyD', 'KeyQ', 'KeyE',
  'ArrowUp', 'ArrowDown', 'ArrowLeft', 'ArrowRight', 'ShiftLeft', 'ShiftRight', 'Space',
])
const COMBO_WINDOW = 5
const PICKUP_R = 2.8

const clamp = (v: number, a: number, b: number) => (v < a ? a : v > b ? b : v)
const damp = (dt: number, rate: number) => 1 - Math.exp(-dt * rate)
const wrapAngle = (a: number) => Math.atan2(Math.sin(a), Math.cos(a))
const easeInOut = (t: number) => (t < 0.5 ? 4 * t * t * t : 1 - Math.pow(-2 * t + 2, 3) / 2)

export interface FlightBounds {
  /** Centre of the flight area (x, z) */
  cx: number
  cz: number
  /** Past this radius she banks herself back toward the centre */
  softR: number
  /** She cannot go past this radius */
  hardR: number
  /** Lowest allowed altitude at (x, z) (e.g. just above the waves) */
  floor: (x: number, z: number) => number
  /** Highest altitude */
  ceil: number
  /** Symmetric soft vertical limit (Galaxy: keep near the disc); omit for none */
  softY?: number
  /** Keep out of a sphere this size around the origin (Galaxy core) */
  coreR?: number
  /** Where orbs may float at (x, z): [low, high] */
  orbY: (x: number, z: number) => [number, number]
}

export interface Orb {
  x: number; y: number; z: number
  vx: number; vy: number; vz: number
  gold: boolean
  born: number
  /** time it was collected (burst animation), -1 while alive */
  taken: number
  seed: number
}

export interface ShipPose {
  p: any
  yaw: number
  pitch: number
  roll: number
  speed: number
  throttle: number
}

export interface FlightView {
  pos: any
  look: any
  fov: number
  bank: number
  /** 0 = orbit camera, 1 = chase camera (eased) */
  blend: number
}

/** What a scene draws each frame */
export interface FlightVisual {
  setVisible(v: boolean): void
  draw(state: { ship: ShipPose; time: number; dt: number; trail: Float32Array; trailDirty: boolean; orbs: Orb[] }): void
}

export function createFlightCore(opts: {
  THREE: any
  canvas: HTMLCanvasElement
  reducedMotion: boolean
  bounds: FlightBounds
  speed?: { min: number; max: number }
  /** Chase distance behind the ship */
  chaseBack?: number
  visual: FlightVisual
  onExitRequest: () => void
  onPickup?: (points: number, gold: boolean) => void
}) {
  const { THREE, canvas, reducedMotion, bounds, visual } = opts
  const MIN = opts.speed?.min ?? 6
  const MAX = opts.speed?.max ?? 42
  const BACK = opts.chaseBack ?? 9.5

  const ship: ShipPose = { p: new THREE.Vector3(), yaw: 0, pitch: 0, roll: 0, speed: 16, throttle: 0.45 }
  const chase = { pos: new THREE.Vector3(), look: new THREE.Vector3(), yaw: 0, pitch: 0, idle: 9, zoom: 1 }
  const keys = new Set<string>()
  let active = false
  let blend = 0
  let drag: { x: number; y: number } | null = null
  const trail = new Float32Array(TRAIL * 4) // x, y, z, birth
  let trailCursor = 0
  let trailAcc = 0
  let trailDirty = true
  const orbs: Orb[] = []
  let lastPickup = -99
  let flightTime = 0
  const tmp = new THREE.Vector3()
  const tmp2 = new THREE.Vector3()
  const euler = new THREE.Euler()
  const view: FlightView = { pos: new THREE.Vector3(), look: new THREE.Vector3(), fov: 60, bank: 0, blend: 0 }

  // ── Input ───────────────────────────────────────────────────────────────
  const k = (...codes: string[]) => (codes.some(c => keys.has(c)) ? 1 : 0)
  const isEditable = (t: EventTarget | null) => {
    const el = t as HTMLElement | null
    return !!el && (el.isContentEditable || /^(INPUT|TEXTAREA|SELECT)$/.test(el.tagName))
  }
  function onKeyDown(e: KeyboardEvent) {
    if (isEditable(e.target) || e.metaKey || e.ctrlKey) return
    if (e.code === 'Escape') {
      e.preventDefault()
      e.stopImmediatePropagation()
      opts.onExitRequest()
      return
    }
    // Swallow flight keys and plain printable keys so they don't summon the
    // zen input or trigger other shortcuts while flying (⌥ combos pass)
    if (GAME_KEYS.has(e.code) || (e.key.length === 1 && !e.altKey)) {
      e.preventDefault()
      e.stopImmediatePropagation()
      if (GAME_KEYS.has(e.code)) keys.add(e.code)
    }
  }
  const onKeyUp = (e: KeyboardEvent) => { keys.delete(e.code) }
  const onBlur = () => { keys.clear(); drag = null }
  function onPointerDown(e: PointerEvent) {
    if (e.target !== canvas || e.button !== 0) return
    drag = { x: e.clientX, y: e.clientY }
  }
  function onPointerMove(e: PointerEvent) {
    if (!drag) return
    const dx = e.clientX - drag.x
    const dy = e.clientY - drag.y
    drag = { x: e.clientX, y: e.clientY }
    chase.yaw -= dx * 0.005
    chase.pitch = clamp(chase.pitch + dy * 0.004, -0.35, 0.9)
    chase.idle = 0
  }
  const onPointerUp = () => { drag = null }
  function onWheel(e: WheelEvent) {
    if (e.target !== canvas) return
    chase.zoom = clamp(chase.zoom * Math.exp(e.deltaY * 0.0012), 0.55, 2.4)
  }
  function listen(on: boolean) {
    const m = on ? 'addEventListener' : 'removeEventListener'
    ;(window as any)[m]('keydown', onKeyDown, true)
    ;(window as any)[m]('keyup', onKeyUp, true)
    ;(window as any)[m]('blur', onBlur)
    ;(window as any)[m]('pointerdown', onPointerDown)
    ;(window as any)[m]('pointermove', onPointerMove)
    ;(window as any)[m]('pointerup', onPointerUp)
    ;(canvas as any)[m]('wheel', onWheel, { passive: true })
  }

  // ── Flight model ────────────────────────────────────────────────────────
  function simulate(dt: number) {
    const p = ship
    const up = k('KeyW', 'ArrowUp'), down = k('KeyS', 'ArrowDown')
    const inPitch = clamp(down - up, -1, 1)
    const inRoll = clamp(k('KeyD', 'ArrowRight') - k('KeyA', 'ArrowLeft'), -1, 1)
    const faster = k('ShiftLeft', 'ShiftRight', 'KeyE')
    const slower = k('KeyQ')

    p.throttle = clamp(p.throttle + (faster - slower) * 0.6 * dt, 0, 1)
    const target = MIN + 2 + p.throttle * (MAX - MIN - 4)
    p.speed += (target - p.speed) * damp(dt, 0.8) - Math.sin(p.pitch) * 6 * dt
    p.speed = clamp(p.speed, MIN, MAX)

    // Bank to turn; auto-level; past the soft bound, bank back to the middle
    let rollTarget = inRoll
    const dx = p.p.x - bounds.cx, dz = p.p.z - bounds.cz
    const r = Math.hypot(dx, dz)
    let homeTurn = 0
    if (r > bounds.softR) {
      const toC = Math.atan2(dz / r, -dx / r)
      const diff = wrapAngle(toC - p.yaw)
      const kk = clamp((r - bounds.softR) / 40, 0, 1)
      homeTurn = clamp(diff, -1, 1) * 1.4 * kk
      if (!inRoll) rollTarget = -Math.sign(diff) * 0.7 * kk
    }
    p.roll += (rollTarget - p.roll) * damp(dt, inRoll ? 3 : 2)
    if (inPitch) p.pitch += inPitch * 1.1 * dt
    else p.pitch += (0 - p.pitch) * damp(dt, 1.1)
    if (bounds.softY !== undefined && Math.abs(p.p.y) > bounds.softY) {
      const kk = clamp((Math.abs(p.p.y) - bounds.softY) / 20, 0, 1)
      p.pitch += (-Math.sign(p.p.y) * 0.35 - p.pitch) * damp(dt, 2) * kk
    }
    if (p.p.y > bounds.ceil) p.pitch += (-0.25 - p.pitch) * damp(dt, 2)
    p.pitch = clamp(p.pitch, -1, 1)
    p.yaw = wrapAngle(p.yaw + (-Math.sin(p.roll) * 1.25 - inRoll * 0.12) * dt + homeTurn * dt)

    const cp = Math.cos(p.pitch)
    p.p.x += cp * Math.cos(p.yaw) * p.speed * dt
    p.p.y += Math.sin(p.pitch) * p.speed * dt
    p.p.z += -cp * Math.sin(p.yaw) * p.speed * dt

    const dx2 = p.p.x - bounds.cx, dz2 = p.p.z - bounds.cz
    const r2 = Math.hypot(dx2, dz2)
    if (r2 > bounds.hardR) {
      p.p.x = bounds.cx + dx2 * bounds.hardR / r2
      p.p.z = bounds.cz + dz2 * bounds.hardR / r2
    }
    // Floor (skim, pull up) and ceiling
    const floor = bounds.floor(p.p.x, p.p.z)
    if (p.p.y < floor) {
      p.p.y = floor
      p.pitch = Math.max(p.pitch, 0.25)
    }
    p.p.y = Math.min(p.p.y, bounds.ceil + 20)
    if (bounds.coreR) {
      const d3 = p.p.length()
      if (d3 < bounds.coreR) p.p.multiplyScalar(bounds.coreR / Math.max(d3, 1e-3))
    }
  }

  function updateTrail(time: number, dt: number) {
    trailAcc += dt
    if (trailAcc < 0.02) return
    trailAcc = 0
    euler.set(ship.roll, ship.yaw, ship.pitch, 'YZX')
    tmp.set(-1.1, 0, 0).applyEuler(euler).add(ship.p)
    trail.set([tmp.x, tmp.y, tmp.z, time], trailCursor * 4)
    trailCursor = (trailCursor + 1) % TRAIL
    trailDirty = true
  }

  function chaseView(dt: number, snap: boolean) {
    chase.idle += dt
    if (chase.idle > 1.5 && !drag) {
      chase.yaw *= Math.exp(-dt * 2)
      chase.pitch *= Math.exp(-dt * 2)
    }
    const p = ship
    const yaw = p.yaw + chase.yaw
    const pp = p.pitch * 0.6
    const cp = Math.cos(pp)
    const back = BACK * chase.zoom
    const up = 2.6 * chase.zoom + chase.pitch * 7
    tmp.set(p.p.x - cp * Math.cos(yaw) * back, p.p.y - Math.sin(pp) * back + up, p.p.z + cp * Math.sin(yaw) * back)
    // Keep the camera above the floor too
    tmp.y = Math.max(tmp.y, bounds.floor(tmp.x, tmp.z) + 0.8)
    const cpf = Math.cos(p.pitch)
    tmp2.set(p.p.x + cpf * Math.cos(p.yaw) * 6, p.p.y + Math.sin(p.pitch) * 6 + 0.6, p.p.z - cpf * Math.sin(p.yaw) * 6)
    if (snap) {
      chase.pos.copy(tmp)
      chase.look.copy(tmp2)
    } else {
      chase.pos.lerp(tmp, damp(dt, 4.5))
      chase.look.lerp(tmp2, damp(dt, 8))
    }
  }

  // ── Orbs ────────────────────────────────────────────────────────────────
  function spawnOrb(o: Orb, time: number) {
    // Ahead of the ship, a little to the side, inside the flight area
    const dist = 35 + Math.random() * 45
    const side = (Math.random() - 0.5) * 60
    const fx = Math.cos(ship.yaw), fz = -Math.sin(ship.yaw)
    let x = ship.p.x + fx * dist + fz * side
    let z = ship.p.z + fz * dist - fx * side
    const dx = x - bounds.cx, dz = z - bounds.cz, r = Math.hypot(dx, dz), maxR = bounds.softR * 0.95
    if (r > maxR) { x = bounds.cx + dx * maxR / r; z = bounds.cz + dz * maxR / r }
    const [lo, hi] = bounds.orbY(x, z)
    o.x = x
    o.z = z
    o.y = lo + Math.random() * Math.max(0.5, hi - lo)
    const a = Math.random() * Math.PI * 2
    const sp = 2 + Math.random() * 4
    o.vx = Math.cos(a) * sp
    o.vz = Math.sin(a) * sp
    o.vy = (Math.random() - 0.5) * 1.2
    o.gold = Math.random() < 0.12
    o.born = time
    o.taken = -1
    o.seed = Math.random() * 100
  }

  function updateOrbs(time: number, dt: number) {
    if (orbs.length === 0) {
      for (let i = 0; i < ORBS; i++) {
        const o = {} as Orb
        spawnOrb(o, time)
        orbs.push(o)
      }
    }
    for (const o of orbs) {
      if (o.taken >= 0) {
        if (time - o.taken > 0.6) spawnOrb(o, time)
        continue
      }
      // Drift and weave — they don't sit still, you have to chase them
      const w = Math.sin(time * 0.7 + o.seed) * 0.8
      o.x += (o.vx + Math.cos(o.seed + time * 0.5) * w) * dt
      o.z += (o.vz + Math.sin(o.seed + time * 0.5) * w) * dt
      o.y += o.vy * dt
      const [lo, hi] = bounds.orbY(o.x, o.z)
      if (o.y < lo || o.y > hi) { o.vy = -o.vy; o.y = clamp(o.y, lo, hi) }
      const dx = o.x - bounds.cx, dz = o.z - bounds.cz
      if (Math.hypot(dx, dz) > bounds.softR) { o.vx = -o.vx; o.vz = -o.vz }
      // Too far behind or too old: a fresh one ahead
      const ox = o.x - ship.p.x, oy = o.y - ship.p.y, oz = o.z - ship.p.z
      const d2 = ox * ox + oy * oy + oz * oz
      if (time - o.born > 40 || d2 > 140 * 140) { spawnOrb(o, time); continue }
      if (d2 < PICKUP_R * PICKUP_R) collect(o, time)
    }
  }

  function collect(o: Orb, time: number) {
    o.taken = time
    const combo = time - lastPickup < COMBO_WINDOW ? Math.min(5, zenFlyState.combo + 1) : 1
    lastPickup = time
    const points = (o.gold ? 50 : 10) * combo
    zenFlyState.combo = combo
    zenFlyState.score += points
    zenFlyState.lastPoints = points
    zenFlyState.pickups++
    saveFlyBest(zenFlyState.score)
    opts.onPickup?.(points, o.gold)
  }

  // ── API ─────────────────────────────────────────────────────────────────
  return {
    get active() { return active },
    ship,
    orbs,

    /** Take off from `fromCam` scaled toward `origin`, heading `yaw`. */
    start(pos: any, yaw: number) {
      if (active) return
      active = true
      ship.p.copy(pos)
      ship.yaw = yaw
      ship.pitch = ship.roll = 0
      ship.speed = 16
      ship.throttle = 0.45
      chase.yaw = chase.pitch = 0
      chase.idle = 9
      chaseView(0, true)
      for (let i = 0; i < TRAIL; i++) trail[i * 4 + 3] = -1000
      trailDirty = true
      orbs.length = 0
      flightTime = 0
      lastPickup = -99
      zenFlyState.score = 0
      zenFlyState.combo = 1
      zenFlyState.lastPoints = 0
      keys.clear()
      listen(true)
      visual.setVisible(true)
    },

    stop() {
      if (!active) return
      active = false
      keys.clear()
      drag = null
      listen(false)
    },

    /**
     * Advance the flight and return the chase view, eased in/out with the
     * orbit camera. Null when fly mode is fully off.
     */
    update(dt: number, time: number): FlightView | null {
      const target = active ? 1 : 0
      const rate = reducedMotion ? 3 : 1 / 1.3
      blend = clamp(blend + (target > blend ? 1 : -1) * dt * rate, 0, 1)
      if (!active && blend <= 0.001) {
        blend = 0
        visual.setVisible(false)
        return null
      }
      if (active) {
        flightTime += dt
        simulate(dt)
        updateOrbs(time, dt)
      }
      updateTrail(time, dt)
      chaseView(dt, false)
      visual.draw({ ship, time, dt, trail, trailDirty, orbs: active ? orbs : [] })
      trailDirty = false
      const e = easeInOut(blend)
      view.pos.copy(chase.pos)
      view.look.copy(chase.look)
      view.fov = 56 + clamp((ship.speed - MIN) / (MAX - MIN), 0, 1) * 16
      view.bank = -ship.roll * 0.35 * e
      view.blend = e
      return view
    },

    dispose() {
      if (active) listen(false)
      active = false
    },
  }
}

/** The glider: a low-poly dart pointing along +X (forward at yaw 0). */
export function buildShipGeometry(THREE: any) {
  const v = [
    1.7, 0, 0, // 0 nose
    -0.9, 0.28, 0, // 1 tail top
    -0.9, -0.14, 0, // 2 tail bottom
    -0.7, 0, 1.35, // 3 right wing tip (+Z is right when facing +X)
    -0.7, 0, -1.35, // 4 left wing tip
    -1.05, 0, 0, // 5 tail centre
  ]
  const idx = [0, 1, 3, 0, 4, 1, 0, 3, 2, 0, 2, 4, 1, 5, 3, 1, 4, 5, 2, 3, 5, 2, 5, 4]
  const geo = new THREE.BufferGeometry()
  geo.setAttribute('position', new THREE.Float32BufferAttribute(v, 3))
  geo.setIndex(idx)
  geo.computeVertexNormals()
  return geo
}
