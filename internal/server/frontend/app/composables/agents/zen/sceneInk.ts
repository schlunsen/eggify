/**
 * Ink — the session paints itself.
 *
 * Luminous ink in dark water, simulated as a real fluid (stable fluids:
 * advection, vorticity confinement, pressure projection) on the GPU, rendered
 * with a painterly finish: silk grain, pigment granulation at the edges and
 * soft embossed light.
 *
 *  - Edits are calligraphic brush strokes that taper as they lift.
 *  - Shell commands draw an ensō; sending a message draws a large one.
 *  - Reads let a drop of ink bloom; searches blow a gust across the water;
 *    web calls drip gold from above; errors stamp a vermilion hanko seal.
 *  - Subagents are ink wells that keep bleeding colour while they run.
 *  - While the agent works, slow currents stir the water. Your voice is breath
 *    rising from the bottom.
 *
 * Over a session the strokes mix into a painting that is unique to it.
 */
import type { FrameInfo, SceneContext, ZenGpuScene, ZenToolEvent, ToolCategory } from './types'
import { hash01 } from './types'

const SIM_RES = 128
const PRESSURE_ITERATIONS = 20
const CURL = 22
const VELOCITY_DISSIPATION = 0.32
const DYE_DISSIPATION = 0.07

// sRGB hex; converted to linear for the shaders
const INK: Record<ToolCategory | 'moon' | 'breath', number> = {
  read: 0x3f8cff,
  edit: 0xff3f8f,
  bash: 0xe9e2ff,
  search: 0x21e3a8,
  web: 0xffb43a,
  agent: 0x9c6bff,
  message: 0xb49cff,
  error: 0xff3a1c,
  other: 0x8a7dff,
  moon: 0xf1ecff,
  breath: 0x7fdcff,
}

interface Splat {
  x: number
  y: number
  dx: number
  dy: number
  color: [number, number, number]
  radius: number
}

interface Stroke {
  points: (t: number) => { x: number; y: number; dx: number; dy: number }
  duration: number
  start: number
  emitted: number
  steps: number
  color: [number, number, number]
  width: (t: number) => number
  strength: number
}

interface Well {
  id: string
  x: number
  y: number
  color: [number, number, number]
  angle: number
  ending: number // < 0 while running
}

export function createInkScene(): ZenGpuScene {
  let THREE: any
  let T: any
  let renderer: any
  let reducedMotion = false
  let webgpu = false

  let velocity: { read: any; write: any; swap(): void }
  let dye: { read: any; write: any; swap(): void }
  let pressure: { read: any; write: any; swap(): void }
  let divergenceRT: any
  let curlRT: any
  let simW = SIM_RES, simH = SIM_RES
  let dyeW = 512, dyeH = 512
  let aspect = 1

  // Passes
  let advectVel: any, advectDye: any, divergencePass: any, curlPass: any, vorticityPass: any
  let pressurePass: any, gradientPass: any, clearPass: any, copyPass: any, splatVel: any, splatDye: any, display: any

  // Shared uniforms
  let uDt: any, uSimTexel: any, uDyeTexel: any, uAspect: any, uTime: any, uActivity: any, uSat: any, uVoiceOn: any

  const queue: Splat[] = []
  const strokes: Stroke[] = []
  const wells: Well[] = []
  let now = 0
  let activity = 0.15, targetActivity = 0.15
  let sat = 1, targetSat = 1
  let nextCurrent = 0
  let breathAcc = 0
  let viewW = 0, viewH = 0
  let resizeTimer: ReturnType<typeof setTimeout> | null = null

  function lin(hex: number, k = 1): [number, number, number] {
    const c = new THREE.Color(hex)
    return [c.r * k, c.g * k, c.b * k]
  }

  function makeTarget(w: number, h: number) {
    return new THREE.RenderTarget(w, h, {
      type: THREE.HalfFloatType,
      format: THREE.RGBAFormat,
      minFilter: THREE.LinearFilter,
      magFilter: THREE.LinearFilter,
      wrapS: THREE.ClampToEdgeWrapping,
      wrapT: THREE.ClampToEdgeWrapping,
      depthBuffer: false,
      generateMipmaps: false,
    })
  }

  function makeDouble(w: number, h: number) {
    const d = {
      read: makeTarget(w, h),
      write: makeTarget(w, h),
      swap() {
        const t = d.read
        d.read = d.write
        d.write = t
      },
    }
    return d
  }

  /** A full-screen pass: `inputs` are texture nodes whose .value is set before rendering. */
  function makePass(inputNames: string[], build: (inp: Record<string, any>) => any) {
    const placeholder = new THREE.Texture()
    const inputs: Record<string, any> = {}
    for (const n of inputNames) inputs[n] = T.texture(placeholder)
    const material = new THREE.NodeMaterial()
    material.fragmentNode = build(inputs)
    material.depthTest = false
    material.depthWrite = false
    const quad = new THREE.QuadMesh(material)
    return {
      inputs,
      run(target: any, textures: Record<string, any>) {
        for (const k in textures) inputs[k].value = textures[k]
        renderer.setRenderTarget(target)
        quad.render(renderer)
      },
    }
  }

  function buildPasses() {
    const vUv = T.uv()
    const L = (t: any, texel: any) => t.sample(vUv.sub(T.vec2(texel.x, 0)))
    const R = (t: any, texel: any) => t.sample(vUv.add(T.vec2(texel.x, 0)))
    const B = (t: any, texel: any) => t.sample(vUv.sub(T.vec2(0, texel.y)))
    const Tp = (t: any, texel: any) => t.sample(vUv.add(T.vec2(0, texel.y)))

    const advect = (dissipation: number) => makePass(['vel', 'src'], ({ vel, src }) => {
      const coord = vUv.sub(vel.sample(vUv).xy.mul(uDt).mul(uSimTexel))
      const d = T.float(1).add(uDt.mul(dissipation))
      return src.sample(coord).div(d)
    })
    advectVel = advect(VELOCITY_DISSIPATION)
    advectDye = advect(DYE_DISSIPATION)

    divergencePass = makePass(['vel'], ({ vel }) => {
      const div = R(vel, uSimTexel).x.sub(L(vel, uSimTexel).x).add(Tp(vel, uSimTexel).y).sub(B(vel, uSimTexel).y).mul(0.5)
      return T.vec4(div, 0, 0, 1)
    })

    curlPass = makePass(['vel'], ({ vel }) => {
      const c = R(vel, uSimTexel).y.sub(L(vel, uSimTexel).y).sub(Tp(vel, uSimTexel).x).add(B(vel, uSimTexel).x).mul(0.5)
      return T.vec4(c, 0, 0, 1)
    })

    vorticityPass = makePass(['vel', 'curl'], ({ vel, curl }) => {
      const cl = L(curl, uSimTexel).x
      const cr = R(curl, uSimTexel).x
      const cb = B(curl, uSimTexel).x
      const ct = Tp(curl, uSimTexel).x
      const c = curl.sample(vUv).x
      const f = T.vec2(T.abs(ct).sub(T.abs(cb)), T.abs(cr).sub(T.abs(cl))).mul(0.5)
      const force = f.div(T.length(f).add(1e-4)).mul(c).mul(T.vec2(1, -1)).mul(CURL)
      const v = vel.sample(vUv).xy.add(force.mul(uDt))
      return T.vec4(T.clamp(v, -1000, 1000), 0, 1)
    })

    pressurePass = makePass(['p', 'div'], ({ p, div }) => {
      const sum = L(p, uSimTexel).x.add(R(p, uSimTexel).x).add(B(p, uSimTexel).x).add(Tp(p, uSimTexel).x)
      return T.vec4(sum.sub(div.sample(vUv).x).mul(0.25), 0, 0, 1)
    })

    gradientPass = makePass(['p', 'vel'], ({ p, vel }) => {
      const g = T.vec2(R(p, uSimTexel).x.sub(L(p, uSimTexel).x), Tp(p, uSimTexel).x.sub(B(p, uSimTexel).x))
      return T.vec4(vel.sample(vUv).xy.sub(g), 0, 1)
    })

    clearPass = makePass(['src'], ({ src }) => src.sample(vUv).mul(0.8))
    copyPass = makePass(['src'], ({ src }) => src.sample(vUv))

    // Splats: uniforms set per splat
    const uPoint = T.uniform(new THREE.Vector2())
    const uColor = T.uniform(new THREE.Vector3())
    const uRadius = T.uniform(0.001)
    const splat = () => makePass(['src'], ({ src }) => {
      const p = vUv.sub(uPoint).mul(T.vec2(uAspect, 1))
      const s = T.exp(T.dot(p, p).negate().div(uRadius))
      return T.vec4(src.sample(vUv).xyz.add(uColor.mul(s)), 1)
    })
    splatVel = Object.assign(splat(), { uPoint, uColor, uRadius })
    splatDye = Object.assign(splat(), { uPoint, uColor, uRadius })

    // Display: luminous ink on dark silk
    display = makePass(['dye'], ({ dye }) => {
      const c = dye.sample(vUv).xyz
      const dl = dye.sample(vUv.sub(T.vec2(uDyeTexel.x, 0))).xyz
      const dr = dye.sample(vUv.add(T.vec2(uDyeTexel.x, 0))).xyz
      const db = dye.sample(vUv.sub(T.vec2(0, uDyeTexel.y))).xyz
      const dt = dye.sample(vUv.add(T.vec2(0, uDyeTexel.y))).xyz
      const lum = (v: any) => T.dot(v, T.vec3(0.3, 0.55, 0.15))
      const gx = lum(dr).sub(lum(dl))
      const gy = lum(dt).sub(lum(db))
      const edge = T.length(T.vec2(gx, gy))

      // Silk: fine woven grain + slow large-scale variation
      const px = vUv.mul(T.vec2(uAspect, 1))
      const grain = T.mx_noise_float(px.mul(620)).mul(0.5).add(0.5)
      const weave = T.sin(px.x.mul(1400)).mul(T.sin(px.y.mul(1400))).mul(0.5).add(0.5)
      const cloud = T.mx_fractal_noise_float(T.vec3(px.mul(1.6), uTime.mul(0.01)), 3, 2.0, 0.5).mul(0.5).add(0.5)
      const vign = T.float(1).sub(vUv.sub(0.5).mul(T.vec2(1.1, 1.25)).length().mul(1.15)).clamp(0, 1)
      const silk = T.mix(T.color(0x07050f), T.color(0x151028), cloud.mul(vign)).mul(T.float(0.9).add(grain.mul(0.08)).add(weave.mul(0.03)))

      // Ink: soft saturation, embossed light, darker granulated rims
      const ink = T.float(1).sub(T.exp(c.mul(-1.35)))
      const light = T.clamp(T.dot(T.normalize(T.vec3(gx.mul(-3), gy.mul(-3), 1)), T.normalize(T.vec3(-0.4, 0.6, 0.7))), 0, 1)
      const shade = T.mix(T.float(0.78), T.float(1.08), light)
      const granule = T.mx_noise_float(px.mul(260)).mul(0.5).add(0.5)
      const rim = T.clamp(edge.mul(7), 0, 1).mul(T.float(0.55).add(granule.mul(0.45)))
      const pigment = ink.mul(shade).mul(T.float(1).sub(rim.mul(0.35))).mul(T.float(0.92).add(uActivity.mul(0.12)))

      const outRgb = silk.mul(T.float(1).sub(T.clamp(lum(ink).mul(0.9), 0, 0.9))).add(pigment)
      const l = T.dot(outRgb, T.vec3(0.299, 0.587, 0.114))
      return T.vec4(T.mix(T.vec3(l), outRgb, uSat), 1)
    })
  }

  /** (Re)create the simulation targets for a w×h view. `keepDye` carries the
   *  painting over (resampled) instead of starting from a blank canvas. */
  function allocate(w: number, h: number, keepDye = true) {
    aspect = w / h
    const simScale = SIM_RES / Math.min(w, h)
    simW = Math.max(32, Math.round(w * simScale))
    simH = Math.max(32, Math.round(h * simScale))
    const dyeBase = webgpu ? 1024 : 640
    const dyeScale = dyeBase / Math.min(w, h)
    dyeW = Math.max(64, Math.round(w * dyeScale))
    dyeH = Math.max(64, Math.round(h * dyeScale))

    const oldDye = dye
    velocity?.read.dispose(); velocity?.write.dispose()
    pressure?.read.dispose(); pressure?.write.dispose()
    divergenceRT?.dispose(); curlRT?.dispose()
    velocity = makeDouble(simW, simH)
    pressure = makeDouble(simW, simH)
    divergenceRT = makeTarget(simW, simH)
    curlRT = makeTarget(simW, simH)
    dye = makeDouble(dyeW, dyeH)
    if (oldDye && keepDye) copyPass.run(dye.read, { src: oldDye.read.texture })
    oldDye?.read.dispose(); oldDye?.write.dispose()

    uSimTexel.value.set(1 / simW, 1 / simH)
    uDyeTexel.value.set(1 / dyeW, 1 / dyeH)
    uAspect.value = aspect
  }

  // ── Painting primitives ───────────────────────────────────────────────────

  function splatAt(x: number, y: number, dx: number, dy: number, color: [number, number, number], radius: number) {
    queue.push({ x, y, dx, dy, color, radius })
  }

  function applySplat(s: Splat) {
    const r = s.radius / 100 * (aspect > 1 ? aspect : 1)
    splatVel.uPoint.value.set(s.x, s.y)
    splatVel.uColor.value.set(s.dx, s.dy, 0)
    splatVel.uRadius.value = r
    splatVel.run(velocity.write, { src: velocity.read.texture })
    velocity.swap()
    splatDye.uPoint.value.set(s.x, s.y)
    splatDye.uColor.value.set(s.color[0], s.color[1], s.color[2])
    splatDye.uRadius.value = r
    splatDye.run(dye.write, { src: dye.read.texture })
    dye.swap()
  }

  function addStroke(stroke: Omit<Stroke, 'start' | 'emitted'>) {
    strokes.push({ ...stroke, start: now, emitted: 0 })
    if (strokes.length > 10) strokes.shift()
  }

  /** Calligraphic stroke: a gentle S-curve that thins as the brush lifts. */
  function brushStroke(seed: string, color: [number, number, number]) {
    const x0 = 0.15 + hash01(seed, 1) * 0.55
    const y0 = 0.2 + hash01(seed, 2) * 0.6
    const ang = (hash01(seed, 3) - 0.5) * 1.6
    const len = 0.22 + hash01(seed, 4) * 0.16
    const bend = (hash01(seed, 5) - 0.5) * 0.25
    addStroke({
      points: (t) => {
        const along = t * len
        const off = Math.sin(t * Math.PI) * bend
        const cx = Math.cos(ang), cy = Math.sin(ang)
        const x = x0 + (cx * along - cy * off) / aspect
        const y = y0 + cy * along + cx * off
        return { x, y, dx: cx * 900, dy: cy * 900 }
      },
      duration: 0.75,
      steps: 22,
      color,
      width: (t) => 0.55 * Math.pow(1 - t, 0.7) + 0.08,
      strength: 1,
    })
  }

  /** Ensō — an open circle drawn in one breath. */
  function enso(cx: number, cy: number, radius: number, color: [number, number, number], duration: number) {
    const start = Math.random() * Math.PI * 2
    const sweep = Math.PI * 2 * 0.9 // leave the gap
    addStroke({
      points: (t) => {
        const a = start + t * sweep
        const wobble = 1 + Math.sin(t * 9) * 0.02
        const x = cx + (Math.cos(a) * radius * wobble) / aspect
        const y = cy + Math.sin(a) * radius * wobble
        return { x, y, dx: -Math.sin(a) * 1100, dy: Math.cos(a) * 1100 }
      },
      duration,
      steps: Math.round(34 * duration),
      color,
      width: (t) => (0.35 + 0.45 * Math.sin(Math.min(1, t * 1.15) * Math.PI)) * (radius * 3.2),
      strength: 1,
    })
  }

  function bloomDrop(x: number, y: number, color: [number, number, number], size = 0.5) {
    splatAt(x, y, 0, 0, color, size)
    for (let i = 0; i < 6; i++) {
      const a = (i / 6) * Math.PI * 2
      splatAt(x + Math.cos(a) * 0.01 / aspect, y + Math.sin(a) * 0.01, Math.cos(a) * 320, Math.sin(a) * 320, [0, 0, 0], size * 0.6)
    }
  }

  /** Hanko seal: a small vermilion square stamped onto the painting. */
  function seal(x: number, y: number) {
    const col = lin(INK.error, 0.9)
    const n = 4
    const s = 0.012
    for (let i = 0; i < n; i++) {
      for (let j = 0; j < n; j++) {
        const cut = (i === 1 && j === 2) || (i === 2 && j === 1) // carved characters
        splatAt(x + ((i - 1.5) * s) / aspect, y + (j - 1.5) * s, 0, 0, cut ? [0, 0, 0] : col, 0.045)
      }
    }
  }

  function randomSpot(seed: string) {
    return { x: 0.12 + hash01(seed, 7) * 0.76, y: 0.15 + hash01(seed, 8) * 0.7 }
  }

  // ── Frame ────────────────────────────────────────────────────────────────

  function runStrokes() {
    for (let i = strokes.length - 1; i >= 0; i--) {
      const s = strokes[i]!
      const t = Math.min(1, (now - s.start) / s.duration)
      const target = Math.floor(t * s.steps)
      while (s.emitted <= target && s.emitted <= s.steps) {
        const u = s.emitted / s.steps
        const p = s.points(u)
        const w = s.width(u)
        const k = s.strength * (0.55 + 0.45 * (1 - u))
        splatAt(p.x, p.y, p.dx * 0.35, p.dy * 0.35, [s.color[0] * k, s.color[1] * k, s.color[2] * k], w * 0.12)
        s.emitted++
      }
      if (t >= 1) strokes.splice(i, 1)
    }
  }

  function runAmbient(info: FrameInfo) {
    // Slow currents while the agent works
    if (now >= nextCurrent) {
      nextCurrent = now + (activity > 0.5 ? 0.35 : 2.8) * (reducedMotion ? 2 : 1)
      const x = Math.random(), y = Math.random()
      const a = Math.random() * Math.PI * 2
      const f = 120 + activity * 380
      splatAt(x, y, Math.cos(a) * f, Math.sin(a) * f, [0, 0, 0], 1.6)
    }
    // Ink wells (running subagents)
    for (let i = wells.length - 1; i >= 0; i--) {
      const w = wells[i]!
      w.angle += info.dt * 1.4
      const k = w.ending < 0 ? 0.025 : Math.max(0, 0.08 * (1 - (now - w.ending) / 1.5))
      const r = 0.035
      splatAt(w.x + Math.cos(w.angle) * r / aspect, w.y + Math.sin(w.angle) * r,
        -Math.sin(w.angle) * 160, Math.cos(w.angle) * 160, [w.color[0] * k, w.color[1] * k, w.color[2] * k], 0.18)
      if (w.ending >= 0 && now - w.ending > 1.5) wells.splice(i, 1)
    }
    // Breath: the voice rises from the bottom of the water
    if (info.voiceOn > 0.02) {
      breathAcc += info.dt
      if (breathAcc > 1 / 30) {
        breathAcc = 0
        const v = info.voice
        if (v > 0.04) {
          const x = 0.5 + (Math.random() - 0.5) * 0.3
          const c = lin(INK.breath, 0.05 * v)
          splatAt(x, 0.04, (Math.random() - 0.5) * 120, 900 * v + 80, c, 0.35 + v * 0.5)
        }
      }
    }
  }

  function step(dt: number) {
    uDt.value = dt
    curlPass.run(curlRT, { vel: velocity.read.texture })
    vorticityPass.run(velocity.write, { vel: velocity.read.texture, curl: curlRT.texture })
    velocity.swap()
    divergencePass.run(divergenceRT, { vel: velocity.read.texture })
    clearPass.run(pressure.write, { src: pressure.read.texture })
    pressure.swap()
    const iters = webgpu ? PRESSURE_ITERATIONS : 14
    for (let i = 0; i < iters; i++) {
      pressurePass.run(pressure.write, { p: pressure.read.texture, div: divergenceRT.texture })
      pressure.swap()
    }
    gradientPass.run(velocity.write, { p: pressure.read.texture, vel: velocity.read.texture })
    velocity.swap()
    advectVel.run(velocity.write, { vel: velocity.read.texture, src: velocity.read.texture })
    velocity.swap()
    advectDye.run(dye.write, { vel: velocity.read.texture, src: dye.read.texture })
    dye.swap()
  }

  return {
    id: 'ink',

    async build(ctx: SceneContext) {
      THREE = ctx.THREE
      T = ctx.TSL as any
      renderer = ctx.renderer
      reducedMotion = ctx.reducedMotion
      webgpu = ctx.webgpu

      uDt = T.uniform(0.016)
      uSimTexel = T.uniform(new THREE.Vector2(1 / SIM_RES, 1 / SIM_RES))
      uDyeTexel = T.uniform(new THREE.Vector2(1 / 512, 1 / 512))
      uAspect = T.uniform(1)
      uTime = T.uniform(0)
      uActivity = T.uniform(0.15)
      uSat = T.uniform(1)
      uVoiceOn = T.uniform(0)
      buildPasses()
    },

    enter() {
      // The opening ensō is drawn on first resize, once the targets exist
    },

    leave() {
      strokes.length = 0
    },

    resize(w: number, h: number) {
      if (w === viewW && h === viewH) return
      viewW = w
      viewH = h
      if (!dye) {
        allocate(w, h)
        // Opening gesture
        queue.length = 0
        enso(0.5, 0.52, 0.2, lin(INK.moon, 0.5), 2.4)
        return
      }
      // Debounce: fullscreen toggles and window drags fire many resizes. The
      // old targets keep rendering (stretched) until the size settles, then
      // the painting is resampled into the new ones.
      if (resizeTimer) clearTimeout(resizeTimer)
      resizeTimer = setTimeout(() => {
        resizeTimer = null
        allocate(viewW, viewH)
      }, 160)
    },

    reset() {
      // A new session gets a fresh sheet
      queue.length = 0
      strokes.length = 0
      wells.length = 0
      if (dye && viewW && viewH) {
        allocate(viewW, viewH, false)
        enso(0.5, 0.52, 0.2, lin(INK.moon, 0.5), 2.4)
      }
    },

    frame(info: FrameInfo) {
      const dt = Math.min(info.dt, 1 / 30)
      now = info.time
      activity += (targetActivity - activity) * (1 - Math.exp(-1.2 * info.dt))
      sat += (targetSat - sat) * (1 - Math.exp(-1 * info.dt))
      uTime.value = info.time
      uActivity.value = activity
      uSat.value = sat
      uVoiceOn.value = info.voiceOn

      runStrokes()
      runAmbient(info)
      // Apply queued splats (bounded per frame)
      let n = 0
      while (queue.length && n < 48) {
        applySplat(queue.shift()!)
        n++
      }
      step(dt * (reducedMotion ? 0.6 : 1))

      display.run(null, { dye: dye.read.texture })
    },

    setStatus(status: string) {
      targetActivity = status === 'processing' ? 1 : status === 'ended' ? 0 : 0.15
      targetSat = status === 'ended' ? 0.35 : 1
    },

    setProjectColor() {
      // Ink keeps its own palette — the painting is the session's, not the project's
    },

    toolEvent(e: ZenToolEvent) {
      const seed = (e.filePath || e.label || e.tool) + ':' + Math.floor(now * 10)
      switch (e.category) {
        case 'edit':
          brushStroke(seed, lin(INK.edit, 0.34))
          break
        case 'read': {
          const p = randomSpot(e.filePath || seed)
          bloomDrop(p.x, p.y, lin(INK.read, 0.3), 0.45)
          break
        }
        case 'bash': {
          const p = randomSpot(seed)
          enso(p.x, p.y, 0.07 + hash01(seed, 9) * 0.05, lin(INK.bash, 0.22), 0.9)
          break
        }
        case 'search': {
          const y = 0.2 + hash01(seed, 10) * 0.6
          const dir = hash01(seed, 11) < 0.5 ? 1 : -1
          for (let i = 0; i < 7; i++) {
            const x = dir > 0 ? 0.05 + i * 0.12 : 0.95 - i * 0.12
            splatAt(x, y + Math.sin(i) * 0.02, dir * 700, 0, i % 2 ? [0, 0, 0] : lin(INK.search, 0.08), 0.5)
          }
          break
        }
        case 'web': {
          const x = 0.15 + hash01(seed, 12) * 0.7
          splatAt(x, 0.97, 0, -900, lin(INK.web, 0.32), 0.4)
          break
        }
        case 'error': {
          const p = randomSpot(seed)
          seal(p.x, p.y)
          break
        }
        case 'agent':
        case 'message': {
          const p = randomSpot(seed)
          bloomDrop(p.x, p.y, lin(INK.message, 0.14), 0.8)
          break
        }
        default: {
          const p = randomSpot(seed)
          bloomDrop(p.x, p.y, lin(INK.other, 0.18), 0.35)
        }
      }
    },

    sendPulse() {
      enso(0.5, 0.52, 0.24, lin(INK.moon, 0.42), 1.8)
    },

    agentSpawn(id: string) {
      if (wells.some(w => w.id === id)) return
      const p = randomSpot(id)
      wells.push({ id, x: p.x, y: p.y, color: lin(INK.agent), angle: hash01(id) * 6.28, ending: -1 })
      if (wells.length > 6) wells.shift()
    },

    agentComplete(id: string) {
      const w = wells.find(x => x.id === id)
      if (!w) return
      w.ending = now
      bloomDrop(w.x, w.y, lin(INK.agent, 0.4), 0.9)
    },
  }
}
