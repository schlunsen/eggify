/**
 * Galaxy fly mode: the flight core (flightCore.ts) drawn with TSL on the
 * WebGPURenderer — neon glider, engine glow, contrail and chase orbs.
 */
import { createFlightCore, buildShipGeometry, TRAIL, ORBS, type FlightVisual } from './flightCore'

export function createGalaxyFlight(opts: {
  THREE: any
  T: any
  scene: any
  canvas: HTMLCanvasElement
  reducedMotion: boolean
  onExitRequest: () => void
  onPickup?: (points: number, gold: boolean) => void
}) {
  const { THREE, T, scene } = opts
  let root: any = null
  let engine: any = null
  let trailAttr: any = null
  let orbA: any = null
  let orbB: any = null
  let uTime: any = null
  let uThrottle: any = null
  let orbSprites: any[] = []
  let trailSprite: any = null

  function build() {
    root = new THREE.Group()
    const geo = buildShipGeometry(THREE)
    root.add(
      new THREE.Mesh(geo, new THREE.MeshBasicNodeMaterial({ color: 0x120a24, side: THREE.DoubleSide })),
      new THREE.LineSegments(new THREE.EdgesGeometry(geo, 1), new THREE.LineBasicNodeMaterial({ color: 0x7fe8ff })),
    )
    uTime = T.uniform(0)
    uThrottle = T.uniform(0.45)
    const disc = (power: number) => {
      const d = T.uv().sub(0.5).length().mul(2)
      return T.exp(d.mul(d).mul(-power)).mul(T.float(1).sub(T.smoothstep(0.8, 1, d)))
    }

    // Engine glow
    const glowMat = new THREE.SpriteNodeMaterial({ transparent: true, depthWrite: false, blending: THREE.AdditiveBlending })
    glowMat.colorNode = T.mix(T.color(0xff7ad9), T.color(0xbff4ff), uThrottle).mul(T.float(1.2).add(uThrottle))
    glowMat.opacityNode = disc(5)
    engine = new THREE.Sprite(glowMat)
    engine.position.set(-1.1, 0, 0)
    root.add(engine)

    // Contrail from the core's ring buffer, faded by age on the GPU
    trailAttr = new THREE.InstancedBufferAttribute(new Float32Array(TRAIL * 4), 4)
    trailAttr.setUsage(THREE.DynamicDrawUsage)
    const a = T.instancedBufferAttribute(trailAttr)
    const age = uTime.sub(a.w)
    const life = T.clamp(T.float(1).sub(age.div(1.6)), 0, 1)
    const trailMat = new THREE.SpriteNodeMaterial({ transparent: true, depthWrite: false, blending: THREE.AdditiveBlending })
    trailMat.positionNode = a.xyz
    trailMat.scaleNode = T.float(0.25).add(age.mul(0.5)).mul(life)
    trailMat.colorNode = T.mix(T.color(0x7fe8ff), T.color(0xb07cff), T.clamp(age.div(1.2), 0, 1)).mul(life.mul(1.4))
    trailMat.opacityNode = disc(4).mul(life)
    trailSprite = new THREE.Sprite(trailMat)
    trailSprite.count = TRAIL
    trailSprite.frustumCulled = false

    // Orbs: a bright core and a soft halo; gold ones are worth more
    orbA = new THREE.InstancedBufferAttribute(new Float32Array(ORBS * 4), 4) // x, y, z, taken
    orbB = new THREE.InstancedBufferAttribute(new Float32Array(ORBS * 4), 4) // gold, alive, seed, -
    orbA.setUsage(THREE.DynamicDrawUsage)
    orbB.setUsage(THREE.DynamicDrawUsage)
    const oa = T.instancedBufferAttribute(orbA)
    const ob = T.instancedBufferAttribute(orbB)
    const takenAge = uTime.sub(oa.w)
    const taken = T.step(0, oa.w)
    const burst = T.clamp(takenAge.div(0.6), 0, 1).mul(taken)
    const pulse = T.float(1).add(T.sin(uTime.mul(3).add(ob.z)).mul(0.15))
    const alpha = ob.y.mul(T.float(1).sub(burst))
    const orbColor = T.mix(T.color(0x7fe8ff), T.color(0xffcf5a), ob.x)
    orbSprites = [
      { scale: 1.1, power: 5, gain: 2.6 },
      { scale: 4.2, power: 2.5, gain: 0.55 },
    ].map(({ scale, power, gain }) => {
      const m = new THREE.SpriteNodeMaterial({ transparent: true, depthWrite: false, blending: THREE.AdditiveBlending })
      m.positionNode = oa.xyz
      m.scaleNode = T.float(scale).mul(pulse).mul(T.float(1).add(ob.x.mul(0.35))).mul(T.float(1).add(burst.mul(3)))
      m.colorNode = orbColor.mul(gain).mul(T.float(1).add(burst.mul(2)))
      m.opacityNode = disc(power).mul(alpha)
      const s = new THREE.Sprite(m)
      s.count = ORBS
      s.frustumCulled = false
      return s
    })

    root.visible = false
    trailSprite.visible = false
    for (const s of orbSprites) s.visible = false
    scene.add(root, trailSprite, ...orbSprites)
  }

  const visual: FlightVisual = {
    setVisible(v) {
      if (!root) return
      root.visible = v
      trailSprite.visible = v
      for (const s of orbSprites) s.visible = v
    },
    draw({ ship, time, trail, trailDirty, orbs }) {
      root.position.copy(ship.p)
      root.rotation.set(ship.roll, ship.yaw, ship.pitch, 'YZX')
      uTime.value = time
      uThrottle.value = ship.throttle
      engine.scale.setScalar(1.0 + ship.throttle * 0.8 + Math.sin(time * 30) * 0.05)
      if (trailDirty) {
        trailAttr.array.set(trail)
        trailAttr.needsUpdate = true
      }
      for (let i = 0; i < ORBS; i++) {
        const o = orbs[i]
        if (o) {
          orbA.array.set([o.x, o.y, o.z, o.taken], i * 4)
          orbB.array.set([o.gold ? 1 : 0, 1, o.seed, 0], i * 4)
        } else {
          orbB.array[i * 4 + 1] = 0
        }
      }
      orbA.needsUpdate = true
      orbB.needsUpdate = true
    },
  }

  const core = createFlightCore({
    THREE,
    canvas: opts.canvas,
    reducedMotion: opts.reducedMotion,
    visual,
    onExitRequest: opts.onExitRequest,
    onPickup: opts.onPickup,
    bounds: {
      cx: 0, cz: 0, softR: 105, hardR: 165,
      floor: () => -70, ceil: 70, softY: 40, coreR: 6,
      orbY: () => [-12, 14],
    },
  })

  return {
    get active() { return core.active },
    /** Take off from near the orbit camera, heading for the core. */
    start(fromCam: any) {
      if (!root) build()
      const p = fromCam.clone().multiplyScalar(0.55)
      p.y = Math.max(-20, Math.min(22, p.y))
      core.start(p, Math.atan2(p.z, -p.x))
    },
    stop: () => core.stop(),
    update: (dt: number, time: number) => (root ? core.update(dt, time) : null),
    info: () => ({ speed: core.ship.speed, throttle: core.ship.throttle }),
    position: () => core.ship.p,
    dispose: () => core.dispose(),
  }
}
