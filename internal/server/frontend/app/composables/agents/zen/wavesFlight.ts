/**
 * Waves fly mode: the flight core (flightCore.ts) drawn with the classic
 * WebGLRenderer — a neon glider skimming the synthwave grid, with an engine
 * glow, a contrail and chase orbs.
 */
import { createFlightCore, buildShipGeometry, TRAIL, ORBS, type FlightBounds, type FlightVisual } from './flightCore'

function glowTexture(THREE: any) {
  const size = 64
  const c = document.createElement('canvas')
  c.width = c.height = size
  const g = c.getContext('2d')!
  const grad = g.createRadialGradient(size / 2, size / 2, 0, size / 2, size / 2, size / 2)
  grad.addColorStop(0, 'rgba(255,255,255,1)')
  grad.addColorStop(0.25, 'rgba(255,255,255,0.65)')
  grad.addColorStop(0.6, 'rgba(255,255,255,0.12)')
  grad.addColorStop(1, 'rgba(255,255,255,0)')
  g.fillStyle = grad
  g.fillRect(0, 0, size, size)
  const tex = new THREE.CanvasTexture(c)
  tex.colorSpace = THREE.SRGBColorSpace
  return tex
}

export function createWavesFlight(opts: {
  THREE: any
  scene: any
  canvas: HTMLCanvasElement
  reducedMotion: boolean
  bounds: FlightBounds
  onExitRequest: () => void
  onPickup?: (points: number, gold: boolean, x: number, z: number) => void
}) {
  const { THREE, scene } = opts
  let root: any = null
  let engine: any = null
  let trailPoints: any = null
  let trailPos: Float32Array
  let trailCol: Float32Array
  const orbSprites: { core: any; halo: any }[] = []
  const cyan = new THREE.Color(0x7fe8ff)
  const violet = new THREE.Color(0xb07cff)
  const gold = new THREE.Color(0xffcf5a)
  const tmpC = new THREE.Color()

  function build() {
    const tex = glowTexture(THREE)
    root = new THREE.Group()
    const geo = buildShipGeometry(THREE)
    root.add(
      new THREE.Mesh(geo, new THREE.MeshBasicMaterial({ color: 0x120a24, side: THREE.DoubleSide })),
      new THREE.LineSegments(new THREE.EdgesGeometry(geo, 1), new THREE.LineBasicMaterial({ color: 0x7fe8ff })),
    )
    engine = new THREE.Sprite(new THREE.SpriteMaterial({
      map: tex, color: 0xff7ad9, transparent: true, depthWrite: false, blending: THREE.AdditiveBlending,
    }))
    engine.position.set(-1.1, 0, 0)
    root.add(engine)

    // Contrail: points faded by age (colours written per frame; 72 of them)
    trailPos = new Float32Array(TRAIL * 3)
    trailCol = new Float32Array(TRAIL * 3)
    const tg = new THREE.BufferGeometry()
    tg.setAttribute('position', new THREE.BufferAttribute(trailPos, 3).setUsage(THREE.DynamicDrawUsage))
    tg.setAttribute('color', new THREE.BufferAttribute(trailCol, 3).setUsage(THREE.DynamicDrawUsage))
    trailPoints = new THREE.Points(tg, new THREE.PointsMaterial({
      map: tex, size: 1.1, sizeAttenuation: true, vertexColors: true,
      transparent: true, depthWrite: false, blending: THREE.AdditiveBlending,
    }))
    trailPoints.frustumCulled = false

    for (let i = 0; i < ORBS; i++) {
      const mk = (opacity: number) => new THREE.Sprite(new THREE.SpriteMaterial({
        map: tex, color: 0x7fe8ff, transparent: true, opacity, depthWrite: false, blending: THREE.AdditiveBlending,
      }))
      const core = mk(1)
      const halo = mk(0.35)
      core.visible = halo.visible = false
      orbSprites.push({ core, halo })
      scene.add(core, halo)
    }
    root.visible = false
    trailPoints.visible = false
    scene.add(root, trailPoints)
  }

  const visual: FlightVisual = {
    setVisible(v) {
      if (!root) return
      root.visible = v
      trailPoints.visible = v
      if (!v) for (const o of orbSprites) o.core.visible = o.halo.visible = false
    },
    draw({ ship, time, trail, orbs }) {
      root.position.copy(ship.p)
      root.rotation.set(ship.roll, ship.yaw, ship.pitch, 'YZX')
      engine.scale.setScalar(1.1 + ship.throttle * 1.0 + Math.sin(time * 30) * 0.05)
      engine.material.color.copy(tmpC.set(0xff7ad9).lerp(cyan, ship.throttle))

      // Colours fade with age, so the (72-point) trail is rewritten every frame
      {
        for (let i = 0; i < TRAIL; i++) {
          const age = time - trail[i * 4 + 3]!
          const life = Math.max(0, 1 - age / 1.6)
          trailPos[i * 3] = trail[i * 4]!
          trailPos[i * 3 + 1] = trail[i * 4 + 1]!
          trailPos[i * 3 + 2] = trail[i * 4 + 2]!
          tmpC.copy(cyan).lerp(violet, Math.min(1, age / 1.2)).multiplyScalar(life * 1.3)
          trailCol[i * 3] = tmpC.r
          trailCol[i * 3 + 1] = tmpC.g
          trailCol[i * 3 + 2] = tmpC.b
        }
        trailPoints.geometry.attributes.position.needsUpdate = true
        trailPoints.geometry.attributes.color.needsUpdate = true
      }

      for (let i = 0; i < ORBS; i++) {
        const s = orbSprites[i]!
        const o = orbs[i]
        if (!o) { s.core.visible = s.halo.visible = false; continue }
        const burst = o.taken >= 0 ? Math.min(1, (time - o.taken) / 0.6) : 0
        const pulse = 1 + Math.sin(time * 3 + o.seed) * 0.15
        const big = o.gold ? 1.35 : 1
        const fade = 1 - burst
        s.core.visible = s.halo.visible = fade > 0.01
        s.core.position.set(o.x, o.y, o.z)
        s.halo.position.set(o.x, o.y, o.z)
        s.core.scale.setScalar(1.6 * pulse * big * (1 + burst * 3))
        s.halo.scale.setScalar(5.5 * pulse * big * (1 + burst * 2))
        const col = o.gold ? gold : cyan
        s.core.material.color.copy(col)
        s.halo.material.color.copy(col)
        s.core.material.opacity = fade
        s.halo.material.opacity = 0.35 * fade
      }
    },
  }

  const core = createFlightCore({
    THREE,
    canvas: opts.canvas,
    reducedMotion: opts.reducedMotion,
    bounds: opts.bounds,
    speed: { min: 8, max: 46 },
    visual,
    onExitRequest: opts.onExitRequest,
    onPickup: (points, isGold) => opts.onPickup?.(points, isGold, core.ship.p.x, core.ship.p.z),
  })

  return {
    get active() { return core.active },
    start(pos: any, yaw: number) {
      if (!root) build()
      core.start(pos, yaw)
    },
    stop: () => core.stop(),
    update: (dt: number, time: number) => (root ? core.update(dt, time) : null),
    info: () => ({ speed: core.ship.speed, throttle: core.ship.throttle }),
    dispose: () => core.dispose(),
  }
}
