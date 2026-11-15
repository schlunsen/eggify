/**
 * Agent Space — a WebXR scene that shows every agent session as an orb
 * arranged in an arc around the viewer. Works as a normal 3D page on the
 * desktop (drag to look, click to select) and as an immersive VR / passthrough
 * AR session on Meta Quest (controller ray or hand pinch to select).
 */
import * as THREE from 'three'
import { XRButton } from 'three/examples/jsm/webxr/XRButton.js'

export type SpaceCategory = 'attention' | 'working' | 'recent'

export interface SpaceAgent {
  id: string
  name: string
  category: SpaceCategory
  label: string
  model?: string
  branch?: string
  project?: string
  subRunning: number
  subTotal: number
  logs: { kind: string; text: string }[]
}

export interface AgentSpace {
  update: (agents: SpaceAgent[]) => void
  select: (id: string | null) => void
  dispose: () => void
  renderer: THREE.WebGLRenderer
}

const COLORS: Record<SpaceCategory, number> = {
  working: 0x34d399,
  attention: 0xf59e0b,
  recent: 0x64748b
}
const CSS_COLORS: Record<SpaceCategory, string> = {
  working: '#34d399',
  attention: '#f59e0b',
  recent: '#94a3b8'
}

const EYE_HEIGHT = 1.5
const RADIUS = 2.6
const PER_ROW = 6
const COL_STEP = 0.4 // radians between orbs so labels don't overlap
const ROW_GAP = 0.95
const ORB_RADIUS = 0.16

interface OrbNode {
  data: SpaceAgent
  group: THREE.Group
  orb: THREE.Mesh<THREE.SphereGeometry, THREE.MeshStandardMaterial>
  halo: THREE.Mesh<THREE.SphereGeometry, THREE.MeshBasicMaterial>
  label: THREE.Mesh<THREE.PlaneGeometry, THREE.MeshBasicMaterial>
  labelCanvas: HTMLCanvasElement
  labelKey: string
  sats: THREE.Mesh[]
  target: THREE.Vector3
  phase: number
}

function roundRect(ctx: CanvasRenderingContext2D, x: number, y: number, w: number, h: number, r: number) {
  ctx.beginPath()
  ctx.moveTo(x + r, y)
  ctx.arcTo(x + w, y, x + w, y + h, r)
  ctx.arcTo(x + w, y + h, x, y + h, r)
  ctx.arcTo(x, y + h, x, y, r)
  ctx.arcTo(x, y, x + w, y, r)
  ctx.closePath()
}

function ellipsize(ctx: CanvasRenderingContext2D, text: string, max: number) {
  if (ctx.measureText(text).width <= max) return text
  let t = text
  while (t.length > 1 && ctx.measureText(t + '…').width > max) t = t.slice(0, -1)
  return t + '…'
}

function wrap(ctx: CanvasRenderingContext2D, text: string, max: number): string[] {
  const lines: string[] = []
  for (const para of text.replace(/\s+/g, ' ').trim().split('\n')) {
    let line = ''
    for (let word of para.split(' ')) {
      // Hard-break tokens (paths, hashes) that are wider than a whole line.
      while (ctx.measureText(word).width > max) {
        let cut = word.length - 1
        while (cut > 1 && ctx.measureText(word.slice(0, cut)).width > max) cut--
        if (line) { lines.push(line); line = '' }
        lines.push(word.slice(0, cut))
        word = word.slice(cut)
      }
      const next = line ? `${line} ${word}` : word
      if (ctx.measureText(next).width > max && line) {
        lines.push(line)
        line = word
      } else line = next
    }
    if (line) lines.push(line)
  }
  return lines
}

function drawLabel(canvas: HTMLCanvasElement, a: SpaceAgent) {
  const ctx = canvas.getContext('2d')!
  const { width: w, height: h } = canvas
  ctx.clearRect(0, 0, w, h)
  roundRect(ctx, 4, 4, w - 8, h - 8, 28)
  ctx.fillStyle = 'rgba(8, 12, 22, 0.85)'
  ctx.fill()
  ctx.lineWidth = 4
  ctx.strokeStyle = CSS_COLORS[a.category]
  ctx.stroke()
  ctx.fillStyle = '#f8fafc'
  ctx.font = '600 46px Inter, system-ui, sans-serif'
  ctx.fillText(ellipsize(ctx, a.name, w - 56), 28, 66)
  ctx.fillStyle = CSS_COLORS[a.category]
  ctx.font = '500 34px Inter, system-ui, sans-serif'
  const sub = a.subTotal ? `  ·  ${a.subRunning}/${a.subTotal} subagents` : ''
  ctx.fillText(ellipsize(ctx, `● ${a.label}${sub}`, w - 56), 28, 116)
  ctx.fillStyle = '#94a3b8'
  ctx.font = '400 28px Inter, system-ui, sans-serif'
  const meta = [a.project, a.model, a.branch].filter(Boolean).join('  ·  ')
  ctx.fillText(ellipsize(ctx, meta, w - 56), 28, 160)
}

function drawDetail(canvas: HTMLCanvasElement, a: SpaceAgent) {
  const ctx = canvas.getContext('2d')!
  const { width: w, height: h } = canvas
  ctx.clearRect(0, 0, w, h)
  roundRect(ctx, 4, 4, w - 8, h - 8, 36)
  ctx.fillStyle = 'rgba(8, 12, 22, 0.92)'
  ctx.fill()
  ctx.lineWidth = 5
  ctx.strokeStyle = CSS_COLORS[a.category]
  ctx.stroke()

  ctx.fillStyle = '#f8fafc'
  ctx.font = '600 52px Inter, system-ui, sans-serif'
  ctx.fillText(ellipsize(ctx, a.name, w - 80), 40, 80)
  ctx.fillStyle = CSS_COLORS[a.category]
  ctx.font = '500 32px Inter, system-ui, sans-serif'
  ctx.fillText(ellipsize(ctx, [a.label, a.model, a.branch].filter(Boolean).join('  ·  '), w - 80), 40, 128)

  ctx.font = '400 28px "JetBrains Mono", ui-monospace, monospace'
  const lineH = 38
  const maxLines = Math.floor((h - 230) / lineH)
  const rendered: { kind: string; line: string; first: boolean }[] = []
  for (const entry of a.logs.slice(-12)) {
    const lines = wrap(ctx, entry.text, w - 250).slice(0, 4)
    lines.forEach((line, i) => rendered.push({ kind: entry.kind, line, first: i === 0 }))
  }
  const visible = rendered.slice(-maxLines)
  let y = 190
  if (!visible.length) {
    ctx.fillStyle = '#64748b'
    ctx.fillText('No recent activity', 40, y)
  }
  for (const r of visible) {
    if (r.first) {
      ctx.fillStyle = r.kind === 'User' ? '#a78bfa' : r.kind === 'Agent' ? '#38bdf8' : '#f59e0b'
      ctx.fillText(ellipsize(ctx, r.kind, 180), 40, y)
    }
    ctx.fillStyle = '#e2e8f0'
    ctx.fillText(r.line, 230, y)
    y += lineH
  }
  ctx.fillStyle = '#64748b'
  ctx.font = '400 26px Inter, system-ui, sans-serif'
  ctx.fillText('Select the panel to close', 40, h - 34)
}

export function createAgentSpace(container: HTMLElement, opts: { onSelect?: (id: string | null) => void } = {}): AgentSpace {
  const renderer = new THREE.WebGLRenderer({ antialias: true, alpha: true })
  renderer.setPixelRatio(Math.min(window.devicePixelRatio, 2))
  renderer.setSize(container.clientWidth, container.clientHeight)
  renderer.xr.enabled = true
  renderer.xr.setReferenceSpaceType('local-floor')
  container.appendChild(renderer.domElement)

  const scene = new THREE.Scene()
  const background = new THREE.Color(0x05070d)
  scene.background = background
  scene.fog = new THREE.Fog(0x05070d, 8, 30)

  const camera = new THREE.PerspectiveCamera(65, container.clientWidth / container.clientHeight, 0.05, 100)
  camera.position.set(0, 1.6, 0.4)
  camera.rotation.order = 'YXZ'

  scene.add(new THREE.AmbientLight(0xffffff, 0.5))
  const key = new THREE.PointLight(0xffffff, 30, 20)
  key.position.set(0, 3.5, 0)
  scene.add(key)

  // Environment only shown in VR / on the desktop — hidden in passthrough AR.
  const environment = new THREE.Group()
  const starGeo = new THREE.BufferGeometry()
  const starPos = new Float32Array(900 * 3)
  for (let i = 0; i < 900; i++) {
    const v = new THREE.Vector3().randomDirection().multiplyScalar(25 + Math.random() * 10)
    v.y = Math.abs(v.y) * 0.8 + 1
    starPos.set([v.x, v.y, v.z], i * 3)
  }
  starGeo.setAttribute('position', new THREE.BufferAttribute(starPos, 3))
  environment.add(new THREE.Points(starGeo, new THREE.PointsMaterial({ color: 0x9fb4ff, size: 0.06, transparent: true, opacity: 0.7 })))
  const grid = new THREE.PolarGridHelper(6, 24, 8, 64, 0x1e293b, 0x1e293b)
  environment.add(grid)
  scene.add(environment)

  const root = new THREE.Group()
  scene.add(root)
  const nodes = new Map<string, OrbNode>()
  const orbGeo = new THREE.SphereGeometry(ORB_RADIUS, 40, 24)
  const haloGeo = new THREE.SphereGeometry(ORB_RADIUS * 1.6, 32, 16)
  const satGeo = new THREE.SphereGeometry(0.03, 12, 8)
  const labelGeo = new THREE.PlaneGeometry(0.9, 0.9 * (192 / 512))

  // Detail panel
  const detailCanvas = document.createElement('canvas')
  detailCanvas.width = 1024
  detailCanvas.height = 700
  const detailTex = new THREE.CanvasTexture(detailCanvas)
  detailTex.colorSpace = THREE.SRGBColorSpace
  const detail = new THREE.Mesh(
    new THREE.PlaneGeometry(1.4, 1.4 * (700 / 1024)),
    new THREE.MeshBasicMaterial({ map: detailTex, transparent: true, depthTest: false })
  )
  detail.renderOrder = 10
  detail.visible = false
  detail.name = 'detail'
  scene.add(detail)
  let selectedId: string | null = null
  let detailKey = ''

  // ── Nodes ────────────────────────────────────────────────────────────────
  function createNode(a: SpaceAgent): OrbNode {
    const group = new THREE.Group()
    const orb = new THREE.Mesh(orbGeo, new THREE.MeshStandardMaterial({ color: COLORS[a.category], emissive: COLORS[a.category], emissiveIntensity: 1, roughness: 0.3, metalness: 0.1 }))
    orb.userData.agentId = a.id
    const halo = new THREE.Mesh(haloGeo, new THREE.MeshBasicMaterial({ color: COLORS[a.category], transparent: true, opacity: 0.15, blending: THREE.AdditiveBlending, depthWrite: false }))
    const labelCanvas = document.createElement('canvas')
    labelCanvas.width = 512
    labelCanvas.height = 192
    const tex = new THREE.CanvasTexture(labelCanvas)
    tex.colorSpace = THREE.SRGBColorSpace
    const label = new THREE.Mesh(labelGeo, new THREE.MeshBasicMaterial({ map: tex, transparent: true }))
    label.position.y = ORB_RADIUS + 0.26
    label.userData.agentId = a.id
    group.add(orb, halo, label)
    root.add(group)
    return { data: a, group, orb, halo, label, labelCanvas, labelKey: '', sats: [], target: new THREE.Vector3(), phase: Math.random() * Math.PI * 2 }
  }

  function disposeNode(n: OrbNode) {
    root.remove(n.group)
    n.orb.material.dispose()
    n.halo.material.dispose()
    n.label.material.map?.dispose()
    n.label.material.dispose()
    n.sats.forEach(s => (s.material as THREE.Material).dispose())
  }

  function syncSatellites(n: OrbNode) {
    const want = Math.min(n.data.subTotal, 8)
    while (n.sats.length > want) {
      const s = n.sats.pop()!
      n.group.remove(s)
      ;(s.material as THREE.Material).dispose()
    }
    while (n.sats.length < want) {
      const s = new THREE.Mesh(satGeo, new THREE.MeshBasicMaterial({ color: 0xffffff }))
      n.group.add(s)
      n.sats.push(s)
    }
    n.sats.forEach((s, i) => {
      const running = i < n.data.subRunning
      ;(s.material as THREE.MeshBasicMaterial).color.set(running ? 0x7dd3fc : 0x475569)
    })
  }

  function layout() {
    const list = [...nodes.values()]
    const rows = Math.ceil(list.length / PER_ROW) || 1
    list.forEach((n, i) => {
      const row = Math.floor(i / PER_ROW)
      const inRow = Math.min(PER_ROW, list.length - row * PER_ROW)
      const col = i % PER_ROW
      const spread = (inRow - 1) * COL_STEP
      const angle = inRow > 1 ? -spread / 2 + (spread * col) / (inRow - 1) : 0
      const y = EYE_HEIGHT + Math.min(rows - 1, 1) * (ROW_GAP / 2) - row * ROW_GAP
      n.target.set(Math.sin(angle) * RADIUS, y, -Math.cos(angle) * RADIUS)
      if (n.group.position.lengthSq() === 0) n.group.position.copy(n.target)
    })
  }

  function update(agents: SpaceAgent[]) {
    const seen = new Set<string>()
    for (const a of agents) {
      seen.add(a.id)
      let n = nodes.get(a.id)
      if (!n) {
        n = createNode(a)
        nodes.set(a.id, n)
      }
      n.data = a
      const color = COLORS[a.category]
      n.orb.material.color.set(color)
      n.orb.material.emissive.set(color)
      n.halo.material.color.set(color)
      const key = JSON.stringify([a.name, a.label, a.category, a.model, a.branch, a.project, a.subRunning, a.subTotal])
      if (key !== n.labelKey) {
        n.labelKey = key
        drawLabel(n.labelCanvas, a)
        n.label.material.map!.needsUpdate = true
      }
      syncSatellites(n)
    }
    for (const [id, n] of nodes) {
      if (!seen.has(id)) {
        disposeNode(n)
        nodes.delete(id)
      }
    }
    // Keep ordering stable: attention first, then working, then recent.
    const order: Record<SpaceCategory, number> = { attention: 0, working: 1, recent: 2 }
    const sorted = [...nodes.entries()].sort((x, y) => order[x[1].data.category] - order[y[1].data.category])
    nodes.clear()
    sorted.forEach(([id, n]) => nodes.set(id, n))
    layout()
    if (selectedId && !nodes.has(selectedId)) select(null)
    else refreshDetail()
  }

  function refreshDetail() {
    if (!selectedId) return
    const n = nodes.get(selectedId)
    if (!n) return
    const key = JSON.stringify([n.labelKey, n.data.logs.slice(-12)])
    if (key === detailKey) return
    detailKey = key
    drawDetail(detailCanvas, n.data)
    detailTex.needsUpdate = true
  }

  function select(id: string | null) {
    selectedId = id && nodes.has(id) ? id : null
    detailKey = ''
    detail.visible = !!selectedId
    if (selectedId) {
      const n = nodes.get(selectedId)!
      // Place the panel between the viewer and the orb, slightly below eye level.
      const dir = n.target.clone().setY(0).normalize()
      detail.position.set(dir.x * 1.3, EYE_HEIGHT - 0.15, dir.z * 1.3)
      detail.lookAt(0, EYE_HEIGHT - 0.15, 0)
      refreshDetail()
    }
    opts.onSelect?.(selectedId)
  }

  // ── Picking ──────────────────────────────────────────────────────────────
  const raycaster = new THREE.Raycaster()
  const tmpMatrix = new THREE.Matrix4()

  function pick(): { id: string | null; detail: boolean; distance: number } | null {
    const targets: THREE.Object3D[] = [...[...nodes.values()].flatMap(n => [n.orb, n.label])]
    if (detail.visible) targets.unshift(detail)
    const hit = raycaster.intersectObjects(targets, false)[0]
    if (!hit) return null
    if (hit.object === detail) return { id: null, detail: true, distance: hit.distance }
    return { id: hit.object.userData.agentId ?? null, detail: false, distance: hit.distance }
  }

  function activate(result: ReturnType<typeof pick>) {
    if (!result) return
    if (result.detail) select(null)
    else if (result.id) select(result.id === selectedId ? null : result.id)
  }

  // XR controllers / hands (pinch fires select events on Quest hand tracking).
  const rayGeo = new THREE.BufferGeometry().setFromPoints([new THREE.Vector3(0, 0, 0), new THREE.Vector3(0, 0, -1)])
  const controllers = [0, 1].map(i => {
    const c = renderer.xr.getController(i)
    const line = new THREE.Line(rayGeo, new THREE.LineBasicMaterial({ color: 0x7dd3fc, transparent: true, opacity: 0.8 }))
    line.scale.z = 5
    c.add(line)
    const tip = new THREE.Mesh(new THREE.SphereGeometry(0.012, 12, 8), new THREE.MeshBasicMaterial({ color: 0xffffff }))
    c.add(tip)
    c.userData.line = line
    c.userData.tip = tip
    c.addEventListener('selectstart', () => {
      setRayFrom(c)
      activate(pick())
    })
    scene.add(c)
    return c
  })

  function setRayFrom(c: THREE.Object3D) {
    tmpMatrix.identity().extractRotation(c.matrixWorld)
    raycaster.ray.origin.setFromMatrixPosition(c.matrixWorld)
    raycaster.ray.direction.set(0, 0, -1).applyMatrix4(tmpMatrix)
  }

  // Desktop / 2D-browser pointer: drag to look around, tap to select.
  const pointer = new THREE.Vector2()
  let dragging = false
  let dragStart = { x: 0, y: 0 }
  let moved = 0
  let yaw = 0
  let pitch = -0.05
  let hoverFromMouse: string | null = null

  function toNdc(e: PointerEvent) {
    const rect = renderer.domElement.getBoundingClientRect()
    pointer.set(((e.clientX - rect.left) / rect.width) * 2 - 1, -((e.clientY - rect.top) / rect.height) * 2 + 1)
  }
  function onPointerDown(e: PointerEvent) {
    dragging = true
    moved = 0
    dragStart = { x: e.clientX, y: e.clientY }
    renderer.domElement.setPointerCapture(e.pointerId)
  }
  function onPointerMove(e: PointerEvent) {
    toNdc(e)
    if (dragging) {
      const dx = e.clientX - dragStart.x
      const dy = e.clientY - dragStart.y
      moved += Math.abs(dx) + Math.abs(dy)
      yaw += dx * 0.004
      pitch = THREE.MathUtils.clamp(pitch + dy * 0.004, -1.2, 1.2)
      dragStart = { x: e.clientX, y: e.clientY }
    }
    raycaster.setFromCamera(pointer, camera)
    hoverFromMouse = pick()?.id ?? null
    renderer.domElement.style.cursor = hoverFromMouse || (detail.visible && pick()?.detail) ? 'pointer' : dragging ? 'grabbing' : 'grab'
  }
  function onPointerUp(e: PointerEvent) {
    dragging = false
    if (moved < 6) {
      toNdc(e)
      raycaster.setFromCamera(pointer, camera)
      activate(pick())
    }
  }
  const canvas = renderer.domElement
  canvas.style.touchAction = 'none'
  canvas.addEventListener('pointerdown', onPointerDown)
  canvas.addEventListener('pointermove', onPointerMove)
  canvas.addEventListener('pointerup', onPointerUp)

  // ── XR session: passthrough when the headset blends with the room ───────
  renderer.xr.addEventListener('sessionstart', () => {
    const session = renderer.xr.getSession()
    const passthrough = session && session.environmentBlendMode && session.environmentBlendMode !== 'opaque'
    scene.background = passthrough ? null : background
    environment.visible = !passthrough
    scene.fog = passthrough ? null : scene.fog
  })
  renderer.xr.addEventListener('sessionend', () => {
    scene.background = background
    environment.visible = true
    scene.fog = new THREE.Fog(0x05070d, 8, 30)
  })

  const button = XRButton.createButton(renderer, {
    requiredFeatures: ['local-floor'],
    optionalFeatures: ['hand-tracking']
  })
  button.classList.add('vr-enter-button')
  container.appendChild(button)

  // ── Loop ────────────────────────────────────────────────────────────────
  const clock = new THREE.Clock()
  renderer.setAnimationLoop(() => {
    const t = clock.getElapsedTime()
    const inXR = renderer.xr.isPresenting

    if (!inXR) {
      camera.rotation.set(pitch, -yaw, 0)
    }

    // Hover from controllers (XR) or mouse (desktop).
    const hovered = new Set<string>()
    if (inXR) {
      for (const c of controllers) {
        setRayFrom(c)
        const hit = pick()
        const line = c.userData.line as THREE.Line
        line.scale.z = hit ? hit.distance : 5
        ;(c.userData.tip as THREE.Mesh).position.set(0, 0, -(hit ? hit.distance : 5))
        if (hit?.id) hovered.add(hit.id)
      }
    } else if (hoverFromMouse) hovered.add(hoverFromMouse)

    for (const n of nodes.values()) {
      n.group.position.lerp(n.target, 0.08)
      n.group.lookAt(0, n.group.position.y, 0)
      const p = t + n.phase
      let scale = 1
      let glow = 0.6
      if (n.data.category === 'working') {
        scale = 1 + Math.sin(p * 4) * 0.07
        glow = 1.1 + Math.sin(p * 4) * 0.5
      } else if (n.data.category === 'attention') {
        n.orb.position.y = Math.sin(p * 3) * 0.03
        glow = 1.3 + Math.sin(p * 6) * 0.7
      } else {
        glow = 0.35
      }
      if (hovered.has(n.data.id) || selectedId === n.data.id) scale *= 1.18
      n.orb.scale.setScalar(scale)
      n.orb.material.emissiveIntensity = glow
      n.halo.scale.setScalar(scale * (1 + Math.sin(p * 2) * 0.05))
      n.halo.material.opacity = selectedId === n.data.id ? 0.35 : 0.12 + glow * 0.06
      const count = n.sats.length
      n.sats.forEach((s, i) => {
        const speed = i < n.data.subRunning ? 1.8 : 0.4
        const a = t * speed + (i * Math.PI * 2) / count
        s.position.set(Math.cos(a) * 0.28, Math.sin(a * 0.7 + i) * 0.06, Math.sin(a) * 0.28)
      })
    }
    renderer.render(scene, camera)
  })

  const resize = new ResizeObserver(() => {
    if (renderer.xr.isPresenting) return
    const w = container.clientWidth
    const h = container.clientHeight
    if (!w || !h) return
    camera.aspect = w / h
    camera.updateProjectionMatrix()
    renderer.setSize(w, h)
  })
  resize.observe(container)

  function dispose() {
    renderer.setAnimationLoop(null)
    resize.disconnect()
    canvas.removeEventListener('pointerdown', onPointerDown)
    canvas.removeEventListener('pointermove', onPointerMove)
    canvas.removeEventListener('pointerup', onPointerUp)
    renderer.xr.getSession()?.end().catch(() => {})
    for (const n of nodes.values()) disposeNode(n)
    nodes.clear()
    ;[orbGeo, haloGeo, satGeo, labelGeo, rayGeo, starGeo].forEach(g => g.dispose())
    detailTex.dispose()
    renderer.dispose()
    canvas.remove()
    button.remove()
  }

  return { update, select, dispose, renderer }
}
