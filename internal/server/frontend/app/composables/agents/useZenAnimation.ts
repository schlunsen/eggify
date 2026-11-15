/**
 * Zen Mode Three.js Animation Composable — Singleton Pattern
 *
 * "Waves": a synthwave landscape that reacts to agent activity.
 *   - An anti-aliased neon grid on a rolling wave surface runs to a glowing
 *     horizon. The wave displacement runs on the GPU (vertex shader).
 *   - Sky dome with a retro striped sun, horizon glow and twinkling stars.
 *   - Processing: the grid flies forward, waves swell, the sun pulses and a
 *     stream of light motes glides toward the horizon.
 *   - Tool calls: a light pillar shoots up from the surface, coloured by tool
 *     category (read / edit / bash / search / web / agent / error), with a
 *     travelling ripple and a glowing ring on the grid.
 *   - Send: a bright wave front sweeps from the camera to the horizon.
 *   - Voice: while the mic is live the grid warms up, and the user's voice
 *     rolls out from the camera as rings of light that lift the surface.
 *   - Bloom post-processing makes the bright parts glow.
 *   - Fly mode (zen/wavesFlight.ts): skim the grid in a neon glider and chase
 *     orbs for points.
 *
 * The scene (renderer, camera, meshes) is created once at module level and
 * persists across component mount/unmount cycles. Mounting attaches the
 * renderer's canvas to the container (with a short camera fly-in); unmounting
 * detaches it and pauses the render loop, so no GPU work happens while zen
 * mode is hidden.
 */
import { ref, onUnmounted, watch, type Ref } from 'vue'
import { getMicLevel, isMicActive } from '~/composables/useMicLevel'
import { createWavesFlight } from './zen/wavesFlight'
import { registerFlyTarget, zenFlyState } from './zen/flyState'
import { getZenAudio } from '~/composables/useZenAudio'

interface ZenAnimationOptions {
  /** Container element to attach the renderer canvas into */
  container: Ref<HTMLElement | null>
  /** Current session status - affects wave behavior */
  sessionStatus: Ref<string>
  /** Number of running subagents - affects wave intensity */
  runningAgentCount: Ref<number>
  /** Project color hex string (e.g. '#FF5733') — tints the horizon, sun and grid peaks */
  projectColor: Ref<string>
}

type RGB = [number, number, number]

// ── Helpers ──
function hexToRgb(hex: string): RGB | null {
  const result = /^#?([a-f\d]{2})([a-f\d]{2})([a-f\d]{2})$/i.exec(hex || '')
  if (!result) return null
  return [
    parseInt(result[1]!, 16) / 255,
    parseInt(result[2]!, 16) / 255,
    parseInt(result[3]!, 16) / 255
  ]
}

function mixRgb(a: RGB, b: RGB, t: number): RGB {
  return [a[0] + (b[0] - a[0]) * t, a[1] + (b[1] - a[1]) * t, a[2] + (b[2] - a[2]) * t]
}

const damp = (current: number, target: number, rate: number, dt: number) =>
  current + (target - current) * (1 - Math.exp(-rate * dt))

// ── Palette (sRGB 0-1; converted to linear in the shaders) ──
const PALETTE = {
  zenith: [0.025, 0.012, 0.06] as RGB,
  horizon: [0.46, 0.13, 0.52] as RGB,
  fill: [0.06, 0.03, 0.13] as RGB,
  lineLow: [0.33, 0.19, 0.63] as RGB,
  lineHigh: [0.55, 0.36, 0.97] as RGB,
  linePeak: [0.22, 0.91, 1.0] as RGB,
  sunTop: [1.0, 0.8, 0.36] as RGB,
  sunBottom: [0.98, 0.22, 0.62] as RGB,
  voice: [1.0, 0.42, 0.82] as RGB, // hot pink — the user's voice
}

/** Tool name → pillar colour */
export function toolColor(toolName: string, isError = false): RGB {
  if (isError) return [1.0, 0.3, 0.3]
  const t = (toolName || '').toLowerCase()
  if (t === 'read' || t === 'glob' || t === 'grep' || t === 'ls' || t === 'search') return [0.2, 0.85, 1.0]
  if (t === 'edit' || t === 'write' || t === 'multiedit' || t === 'notebookedit' || t === 'filechange') return [1.0, 0.72, 0.28]
  if (t === 'bash' || t === 'bashoutput' || t === 'killshell') return [0.3, 1.0, 0.55]
  if (t === 'agent' || t === 'task') return [1.0, 0.42, 0.78]
  if (t.startsWith('web')) return [0.45, 0.6, 1.0]
  if (t === 'todowrite') return [0.75, 0.55, 1.0]
  if (t.startsWith('mcp__')) return [0.25, 0.95, 0.85]
  return [0.62, 0.45, 1.0]
}

// ═══════════════════════════════════════════════════════════════════════════
// Scene constants
// ═══════════════════════════════════════════════════════════════════════════

const PLANE_WIDTH = 420
const PLANE_NEAR_Z = 45
const PLANE_FAR_Z = -185
const PLANE_DEPTH = PLANE_NEAR_Z - PLANE_FAR_Z
const SEG_X = 220
const SEG_Z = 180
const CELL = 3.0

const MAX_RIPPLES = 8
const MAX_PILLARS = 8
const PILLAR_LIFE = 1.8
const PARTICLE_COUNT = 1400

const BASE_AMPLITUDE = 1.2
const BASE_SPEED = 0.25

const SEND_DURATION = 3.5
const SEND_SPEED = 62
const SEND_WIDTH = 18
const SEND_AMPLITUDE = 2.5
const SEND_ORIGIN_Z = 38

// Voice — while the mic is live, the level history rolls out across the grid
// as rings of light from just in front of the camera (newest sample = nearest)
const VOICE_SAMPLES = 96
const VOICE_DT = 1 / 40
const VOICE_SPEED = 55
const VOICE_ORIGIN_Z = 36
const VOICE_LIFT = 2.4

const CAMERA_BASE = { x: 0, y: 14, z: 52 }
const CAMERA_TARGET = { x: 0, y: 4, z: -40 }
const INTRO_DURATION = 1.6

// Status → targets
interface StatusTargets { amp: number; speed: number; activity: number; flow: number; scroll: number; sat: number }
const STATUS_TARGETS: Record<string, StatusTargets> = {
  processing: { amp: 1.6, speed: 1.7, activity: 1.0, flow: 1.0, scroll: 1.0, sat: 1.0 },
  idle: { amp: 1.0, speed: 1.0, activity: 0.12, flow: 0.12, scroll: 0.12, sat: 1.0 },
  ended: { amp: 0.5, speed: 0.5, activity: 0.0, flow: 0.0, scroll: 0.0, sat: 0.35 },
}

// ═══════════════════════════════════════════════════════════════════════════
// Shared GLSL
// ═══════════════════════════════════════════════════════════════════════════

const GLSL_COMMON = /* glsl */ `
  uniform float uTime;
  uniform float uPhase;
  uniform float uAmp;
  uniform vec4 uRipples[${MAX_RIPPLES}];      // x, z, startTime, intensity
  uniform vec3 uRippleColors[${MAX_RIPPLES}];
  uniform float uSendAge;                     // < 0 when inactive
  uniform float uSat;
  uniform float uLinearOut;
  uniform float uVoice[${VOICE_SAMPLES}];     // mic level history, [0] = newest
  uniform float uVoiceOn;                     // 0..1 while the mic is live

  const float FREQ = 0.08;

  // Voice level that reached point p: sample the history by travel time
  float voiceAt(vec2 p) {
    if (uVoiceOn <= 0.001) return 0.0;
    float d = distance(p, vec2(0.0, ${VOICE_ORIGIN_Z.toFixed(1)}));
    float idx = d / ${(VOICE_SPEED * VOICE_DT).toFixed(4)};
    if (idx >= ${(VOICE_SAMPLES - 1).toFixed(1)}) return 0.0;
    int i0 = int(floor(idx));
    float v = mix(uVoice[i0], uVoice[i0 + 1], fract(idx));
    // fade out with distance so the far field stays calm
    return v * (1.0 - idx / ${VOICE_SAMPLES.toFixed(1)}) * uVoiceOn;
  }

  float waveHeight(vec2 p) {
    float t = uPhase;
    float y = 0.0;
    y += sin(p.x * FREQ + t * 0.8) * uAmp * 0.6;
    y += cos(p.y * FREQ * 0.7 + t * 0.5) * uAmp * 0.4;
    y += sin((p.x + p.y) * FREQ * 1.3 + t * 0.6) * uAmp * 0.25;
    y += sin(p.x * FREQ * 2.5 + t * 1.2) * uAmp * 0.1;
    y += cos(p.y * FREQ * 2.0 - t * 0.9) * uAmp * 0.08;

    // Event ripples — a travelling wave packet from each origin
    for (int i = 0; i < ${MAX_RIPPLES}; i++) {
      vec4 r = uRipples[i];
      if (r.w <= 0.0) continue;
      float age = uTime - r.z;
      if (age < 0.0 || age > 6.0) continue;
      float d = distance(p, r.xy);
      float front = age * 16.0;
      float env = exp(-pow((d - front) / 12.0, 2.0));
      y += sin(d * 0.45 - age * 9.0) * r.w * 2.2 * env * exp(-age * 0.6);
    }

    // Send pulse — a wave front sweeping toward the horizon
    if (uSendAge >= 0.0) {
      float progress = uSendAge / ${SEND_DURATION.toFixed(2)};
      float frontZ = ${SEND_ORIGIN_Z.toFixed(1)} - uSendAge * ${SEND_SPEED.toFixed(1)};
      float dz = p.y - frontZ;
      float env = exp(-(dz * dz) / (${(SEND_WIDTH * SEND_WIDTH * 0.4).toFixed(1)}));
      float ramp = min(1.0, uSendAge * 4.0) * max(0.0, 1.0 - progress * progress);
      y += (sin(dz * 0.4 + uSendAge * 6.0) + 0.4 * sin(dz * 0.15 + uSendAge * 3.0)) * env * ramp * ${SEND_AMPLITUDE.toFixed(1)};
    }

    // Voice — the surface swells under each spoken syllable
    y += voiceAt(p) * ${VOICE_LIFT.toFixed(1)};
    return y;
  }

  vec3 finishColor(vec3 c) {
    float l = dot(c, vec3(0.299, 0.587, 0.114));
    c = mix(vec3(l), c, uSat);
    // Colours are authored in sRGB. With post-processing the render target is
    // linear and the output pass re-encodes, so convert first.
    return uLinearOut > 0.5 ? pow(max(c, 0.0), vec3(2.2)) : c;
  }
`

// ═══════════════════════════════════════════════════════════════════════════
// Module-level singleton state — shared across all mount/unmount cycles
// ═══════════════════════════════════════════════════════════════════════════

let THREE: any = null
let renderer: any = null
let composer: any = null
let bloomPass: any = null
let scene: any = null
let camera: any = null
let clock: any = null
let animationFrame: number | null = null
let singletonCreated = false
let creating: Promise<void> | null = null
let activeContainer: HTMLElement | null = null

let common: Record<string, { value: any }> = {}
let gridMesh: any = null
let skyMesh: any = null
let particles: any = null
const pillars: { mesh: any; start: number; active: boolean }[] = []

// Animated values (current → target, damped per frame)
const state = {
  amp: BASE_AMPLITUDE, targetAmp: BASE_AMPLITUDE,
  speed: BASE_SPEED, targetSpeed: BASE_SPEED,
  activity: 0.12, targetActivity: 0.12,
  flow: 0.12, targetFlow: 0.12,
  scrollSpeed: 0.12, targetScroll: 0.12,
  sat: 1, targetSat: 1,
  phase: 0, flowPos: 0, scroll: 0,
  sendStart: -1,
  pulse: 0,
  introStart: -1,
  agentBoost: 0,
  voiceOn: 0,
  voiceNow: 0,
  voiceAcc: 0,
}
let currentStatus = 'idle'
let rippleCursor = 0
let pillarCursor = 0
let reducedMotion = false

// Visibility change listener — registered once at module level
let visibilityListenerRegistered = false
let wasAnimatingBeforeHide = false

function handleVisibilityChange() {
  if (document.hidden) {
    if (animationFrame) {
      cancelAnimationFrame(animationFrame)
      animationFrame = null
      wasAnimatingBeforeHide = true
    }
  } else if (wasAnimatingBeforeHide && singletonCreated && activeContainer) {
    clock?.getDelta() // don't jump after being hidden
    animate()
    wasAnimatingBeforeHide = false
  }
}

// ═══════════════════════════════════════════════════════════════════════════
// Scene construction
// ═══════════════════════════════════════════════════════════════════════════

function v3(c: RGB) {
  return new THREE.Vector3(c[0], c[1], c[2])
}

function buildGrid() {
  const geometry = new THREE.PlaneGeometry(PLANE_WIDTH, PLANE_DEPTH, SEG_X, SEG_Z)
  geometry.rotateX(-Math.PI / 2)
  geometry.translate(0, 0, (PLANE_NEAR_Z + PLANE_FAR_Z) / 2)

  const material = new THREE.ShaderMaterial({
    uniforms: {
      ...common,
      uScroll: { value: 0 },
      uFill: { value: v3(PALETTE.fill) },
      uLineLow: { value: v3(PALETTE.lineLow) },
      uLineHigh: { value: v3(PALETTE.lineHigh) },
      uLinePeak: { value: v3(PALETTE.linePeak) },
      uHorizon: { value: v3(PALETTE.horizon) },
      uSendColor: { value: v3([0.92, 0.88, 1.0]) },
      uVoiceColor: { value: v3(PALETTE.voice) },
      uActivity: { value: 0.12 },
    },
    vertexShader: /* glsl */ `
      ${GLSL_COMMON}
      varying float vHeight;
      varying float vDepth;
      varying vec2 vXZ;

      void main() {
        vec3 p = position;
        p.y = waveHeight(p.xz);
        vHeight = p.y;
        vXZ = p.xz;
        vec4 mv = modelViewMatrix * vec4(p, 1.0);
        vDepth = -mv.z;
        gl_Position = projectionMatrix * mv;
      }
    `,
    fragmentShader: /* glsl */ `
      ${GLSL_COMMON}
      uniform float uScroll;
      uniform vec3 uFill;
      uniform vec3 uLineLow;
      uniform vec3 uLineHigh;
      uniform vec3 uLinePeak;
      uniform vec3 uHorizon;
      uniform vec3 uSendColor;
      uniform vec3 uVoiceColor;
      uniform float uActivity;

      varying float vHeight;
      varying float vDepth;
      varying vec2 vXZ;

      void main() {
        float h = clamp((vHeight + 2.2) / 4.4, 0.0, 1.0);

        // Anti-aliased grid lines (scrolling toward the camera when busy)
        vec2 coord = vec2(vXZ.x, vXZ.y + uScroll) / ${CELL.toFixed(1)};
        vec2 fw = max(fwidth(coord), vec2(1e-4));
        vec2 g = abs(fract(coord - 0.5) - 0.5) / fw;
        float line = 1.0 - min(min(g.x, g.y), 1.0);
        vec2 gGlow = abs(fract(coord - 0.5) - 0.5) / (fw * 4.0);
        float glow = (1.0 - min(min(gGlow.x, gGlow.y), 1.0)) * 0.35;
        // Fade lines out where they get denser than a pixel (no moiré)
        float density = smoothstep(0.25, 0.7, max(fw.x, fw.y));
        line = mix(line, 0.28, density);
        glow = mix(glow, 0.0, density);

        vec3 lineColor = h < 0.55
          ? mix(uLineLow, uLineHigh, h / 0.55)
          : mix(uLineHigh, uLinePeak, (h - 0.55) / 0.45);
        lineColor *= 1.0 + 0.12 * uActivity;

        // Listening: the whole grid brightens a touch and warms up
        lineColor = mix(lineColor, uVoiceColor, 0.18 * uVoiceOn) * (1.0 + 0.3 * uVoiceOn);

        vec3 fill = uFill * (0.7 + 0.6 * h);
        vec3 color = fill + lineColor * (line * 1.05 + glow);

        // Voice rings light the lines they pass over
        float v = voiceAt(vXZ);
        if (v > 0.001) {
          vec3 vc = mix(uVoiceColor, uLinePeak, clamp(v * 0.8, 0.0, 0.6));
          color += vc * v * (line * 2.4 + glow * 2.0 + 0.1);
        }

        // Event rings on the surface, in the event's colour
        for (int i = 0; i < ${MAX_RIPPLES}; i++) {
          vec4 r = uRipples[i];
          if (r.w <= 0.0) continue;
          float age = uTime - r.z;
          if (age < 0.0 || age > 4.0) continue;
          float d = distance(vXZ, r.xy);
          float front = age * 16.0;
          float ring = exp(-pow((d - front) / 1.6, 2.0)) * exp(-age * 0.9) * r.w;
          float core = exp(-d * d / 6.0) * exp(-age * 3.0) * r.w;
          color += uRippleColors[i] * (ring * (0.25 + line * 1.2) + core * 0.9);
        }

        // Send pulse — bright sweeping band
        if (uSendAge >= 0.0) {
          float progress = uSendAge / ${SEND_DURATION.toFixed(2)};
          float frontZ = ${SEND_ORIGIN_Z.toFixed(1)} - uSendAge * ${SEND_SPEED.toFixed(1)};
          float dist = abs(vXZ.y - frontZ);
          float band = exp(-dist * dist / ${(SEND_WIDTH * SEND_WIDTH * 0.5).toFixed(1)});
          float trail = exp(-dist * dist / ${(SEND_WIDTH * SEND_WIDTH * 4.0).toFixed(1)}) * 0.3;
          float ramp = min(1.0, uSendAge * 4.0) * max(0.0, 1.0 - progress * progress);
          float k = (band * 0.8 + trail) * ramp;
          color = mix(color, mix(uSendColor, uLinePeak, 0.3) * (0.6 + line), clamp(k, 0.0, 0.85));
        }

        // Fog into the horizon glow
        float fog = 1.0 - exp(-pow(vDepth * 0.0085, 2.0));
        color = mix(color, uHorizon * 1.05, clamp(fog, 0.0, 1.0));

        // Fade the side edges so the plane never shows a hard border
        float side = 1.0 - smoothstep(0.55, 0.98, abs(vXZ.x) / ${(PLANE_WIDTH / 2).toFixed(1)});
        float nearFade = smoothstep(${PLANE_NEAR_Z.toFixed(1)}, ${(PLANE_NEAR_Z - 12).toFixed(1)}, vXZ.y);

        gl_FragColor = vec4(finishColor(color), side * nearFade);
      }
    `,
    transparent: true,
    depthWrite: true,
    side: THREE.DoubleSide,
  })

  gridMesh = new THREE.Mesh(geometry, material)
  gridMesh.frustumCulled = false // vertices move in the shader
  scene.add(gridMesh)
}

function buildSky() {
  const geometry = new THREE.SphereGeometry(600, 48, 24)
  const material = new THREE.ShaderMaterial({
    uniforms: {
      ...common,
      uZenith: { value: v3(PALETTE.zenith) },
      uHorizon: { value: v3(PALETTE.horizon) },
      uSunTop: { value: v3(PALETTE.sunTop) },
      uSunBottom: { value: v3(PALETTE.sunBottom) },
      uSunDir: { value: new THREE.Vector3(0, 0.1, -1).normalize() },
      uSunRadius: { value: 0.115 },
      uActivity: { value: 0.12 },
      uPulse: { value: 0 },
      uStripeTime: { value: 0 },
    },
    vertexShader: /* glsl */ `
      varying vec3 vDir;
      void main() {
        vec4 wp = modelMatrix * vec4(position, 1.0);
        vDir = wp.xyz - cameraPosition;
        gl_Position = projectionMatrix * viewMatrix * wp;
        gl_Position.z = gl_Position.w; // always at the far plane
      }
    `,
    fragmentShader: /* glsl */ `
      ${GLSL_COMMON}
      uniform vec3 uZenith;
      uniform vec3 uHorizon;
      uniform vec3 uSunTop;
      uniform vec3 uSunBottom;
      uniform vec3 uSunDir;
      uniform float uSunRadius;
      uniform float uActivity;
      uniform float uPulse;
      uniform float uStripeTime;
      varying vec3 vDir;

      float hash(vec2 p) {
        p = fract(p * vec2(123.34, 456.21));
        p += dot(p, p + 45.32);
        return fract(p.x * p.y);
      }

      void main() {
        vec3 d = normalize(vDir);
        float e = d.y;

        // Gradient: horizon glow → deep zenith
        vec3 col = mix(uHorizon * 0.8, uZenith, smoothstep(0.0, 0.3, e));
        col = mix(col, uHorizon * 0.8, smoothstep(0.0, -0.03, e));
        col += uHorizon * exp(-abs(e) * 40.0) * (0.3 + 0.3 * uActivity);

        // Aurora shimmer while working
        if (uActivity > 0.02 && e > 0.0) {
          float az = atan(d.x, -d.z);
          float band = sin(az * 5.0 + uTime * 0.35 + sin(az * 11.0 - uTime * 0.2) * 0.8);
          float curtain = smoothstep(0.35, 1.0, band) * smoothstep(0.08, 0.2, e) * smoothstep(0.55, 0.25, e);
          col += mix(vec3(0.2, 0.9, 0.9), vec3(0.7, 0.3, 1.0), 0.5 + 0.5 * sin(az * 3.0 + uTime * 0.3))
                 * curtain * 0.16 * uActivity;
        }

        // Stars
        if (e > 0.0) {
          vec2 sp = vec2(atan(d.x, -d.z) * 1.6, e) * 170.0;
          vec2 cell = floor(sp);
          vec2 f = fract(sp) - 0.5;
          float r = hash(cell);
          if (r > 0.982) {
            vec2 off = vec2(hash(cell + 7.1), hash(cell + 3.7)) - 0.5;
            float tw = 0.55 + 0.45 * sin(uTime * (0.8 + r * 3.0) + r * 60.0);
            float s = exp(-dot(f - off * 0.6, f - off * 0.6) * 55.0) * tw;
            col += vec3(0.9, 0.85, 1.0) * s * smoothstep(0.03, 0.3, e) * (0.8 + 0.8 * (r - 0.982) / 0.018);
          }
        }

        // Retro sun with scrolling slits
        vec3 sd = normalize(uSunDir);
        float ang = acos(clamp(dot(d, sd), -1.0, 1.0));
        float R = uSunRadius * (1.0 + 0.03 * uPulse);
        float v = clamp(((e - sd.y) / R) * 0.5 + 0.5, 0.0, 1.0); // 0 bottom → 1 top
        if (ang < R) {
          vec3 sc = mix(uSunBottom, uSunTop, smoothstep(0.1, 0.95, v));
          float stripes = 1.0;
          if (v < 0.58) {
            float k = (0.58 - v) / 0.58;
            float s = fract(v * 13.0 + uStripeTime);
            stripes = smoothstep(mix(0.02, 0.6, k) - 0.03, mix(0.02, 0.6, k) + 0.03, s);
          }
          float edge = smoothstep(R, R * 0.985, ang);
          col = mix(col, sc * (0.8 + 0.08 * uPulse), edge * stripes);
        }
        // Halo
        float halo = exp(-max(ang - R, 0.0) * 14.0);
        col += mix(uSunBottom, uSunTop, 0.35) * halo * (0.07 + 0.05 * uActivity + 0.07 * uPulse);

        gl_FragColor = vec4(finishColor(col), 1.0);
      }
    `,
    side: THREE.BackSide,
    depthWrite: false,
    depthTest: false,
  })
  skyMesh = new THREE.Mesh(geometry, material)
  skyMesh.renderOrder = -10
  skyMesh.frustumCulled = false
  scene.add(skyMesh)
}

function buildParticles() {
  const geometry = new THREE.BufferGeometry()
  const seeds = new Float32Array(PARTICLE_COUNT * 3)
  for (let i = 0; i < PARTICLE_COUNT; i++) {
    seeds[i * 3] = Math.random() * 2 - 1        // x spread
    seeds[i * 3 + 1] = Math.random()            // z offset
    seeds[i * 3 + 2] = Math.random()            // speed / size / colour
  }
  // Positions are computed in the shader; three still needs a position attribute
  geometry.setAttribute('position', new THREE.BufferAttribute(new Float32Array(PARTICLE_COUNT * 3), 3))
  geometry.setAttribute('aSeed', new THREE.BufferAttribute(seeds, 3))

  const material = new THREE.ShaderMaterial({
    uniforms: {
      ...common,
      uFlow: { value: 0 },
      uAlpha: { value: 0.2 },
      uColorA: { value: v3(PALETTE.linePeak) },
      uColorB: { value: v3([1.0, 0.55, 0.9]) },
      uPixelRatio: { value: 1 },
    },
    vertexShader: /* glsl */ `
      ${GLSL_COMMON}
      attribute vec3 aSeed;
      uniform float uFlow;
      uniform float uPixelRatio;
      varying float vSeed;
      varying float vFade;
      void main() {
        float x = aSeed.x * ${(PLANE_WIDTH * 0.32).toFixed(1)};
        float travel = fract(aSeed.y + uFlow * (0.35 + aSeed.z * 0.65));
        float z = ${PLANE_NEAR_Z.toFixed(1)} - travel * ${PLANE_DEPTH.toFixed(1)};
        vec3 p = vec3(x, 0.0, z);
        p.y = waveHeight(p.xz) + 0.5 + aSeed.z * 1.4;
        vec4 mv = modelViewMatrix * vec4(p, 1.0);
        gl_Position = projectionMatrix * mv;
        gl_PointSize = (1.2 + aSeed.z * 2.2) * uPixelRatio * (70.0 / max(-mv.z, 1.0));
        vSeed = aSeed.z;
        vFade = smoothstep(0.0, 0.08, travel) * (1.0 - smoothstep(0.7, 1.0, travel));
      }
    `,
    fragmentShader: /* glsl */ `
      ${GLSL_COMMON}
      uniform float uAlpha;
      uniform vec3 uColorA;
      uniform vec3 uColorB;
      varying float vSeed;
      varying float vFade;
      void main() {
        vec2 c = gl_PointCoord - 0.5;
        float a = exp(-dot(c, c) * 18.0);
        vec3 col = mix(uColorA, uColorB, step(0.82, vSeed)) * 1.6;
        gl_FragColor = vec4(finishColor(col), a * vFade * uAlpha);
      }
    `,
    transparent: true,
    depthWrite: false,
    blending: THREE.AdditiveBlending,
  })
  particles = new THREE.Points(geometry, material)
  particles.frustumCulled = false
  scene.add(particles)
}

function buildPillars() {
  const geometry = new THREE.CylinderGeometry(0.85, 0.85, 1, 20, 1, true)
  geometry.translate(0, 0.5, 0) // base at y = 0
  for (let i = 0; i < MAX_PILLARS; i++) {
    const material = new THREE.ShaderMaterial({
      uniforms: {
        uLinearOut: common.uLinearOut,
        uSat: common.uSat,
        uColor: { value: new THREE.Vector3(1, 1, 1) },
        uAlpha: { value: 0 },
      },
      vertexShader: /* glsl */ `
        varying float vY;
        varying float vFacing;
        void main() {
          vY = uv.y;
          vec3 n = normalize(normalMatrix * normal);
          vFacing = abs(n.z);
          gl_Position = projectionMatrix * modelViewMatrix * vec4(position, 1.0);
        }
      `,
      fragmentShader: /* glsl */ `
        uniform vec3 uColor;
        uniform float uAlpha;
        uniform float uSat;
        uniform float uLinearOut;
        varying float vY;
        varying float vFacing;
        void main() {
          float core = pow(vFacing, 3.0);
          float along = pow(1.0 - vY, 1.6);
          vec3 col = mix(uColor, vec3(1.0), core * 0.18) * (0.75 + 0.55 * core);
          float l = dot(col, vec3(0.299, 0.587, 0.114));
          col = mix(vec3(l), col, uSat);
          if (uLinearOut > 0.5) col = pow(col, vec3(2.2));
          gl_FragColor = vec4(col, (0.25 + 0.75 * core) * along * uAlpha);
        }
      `,
      transparent: true,
      depthWrite: false,
      blending: THREE.AdditiveBlending,
    })
    const mesh = new THREE.Mesh(geometry, material)
    mesh.visible = false
    mesh.frustumCulled = false
    scene.add(mesh)
    pillars.push({ mesh, start: 0, active: false })
  }
}

async function setupPostProcessing() {
  try {
    const [{ EffectComposer }, { RenderPass }, { UnrealBloomPass }, { OutputPass }] = await Promise.all([
      import('three/examples/jsm/postprocessing/EffectComposer.js'),
      import('three/examples/jsm/postprocessing/RenderPass.js'),
      import('three/examples/jsm/postprocessing/UnrealBloomPass.js'),
      import('three/examples/jsm/postprocessing/OutputPass.js'),
    ])
    composer = new EffectComposer(renderer)
    composer.addPass(new RenderPass(scene, camera))
    bloomPass = new UnrealBloomPass(new THREE.Vector2(512, 512), 0.65, 0.45, 0.58)
    composer.addPass(bloomPass)
    composer.addPass(new OutputPass())
    common.uLinearOut!.value = 1
  } catch (err) {
    console.warn('[zen] bloom unavailable, rendering without post-processing', err)
    composer = null
    bloomPass = null
    common.uLinearOut!.value = 0
  }
}

async function createScene() {
  // Lazy load Three.js to avoid SSR issues
  THREE = await import('three')

  reducedMotion = typeof window !== 'undefined' &&
    !!window.matchMedia?.('(prefers-reduced-motion: reduce)').matches

  renderer = new THREE.WebGLRenderer({ alpha: false, antialias: true, powerPreference: 'high-performance' })
  renderer.setPixelRatio(Math.min(window.devicePixelRatio, 2))
  renderer.setClearColor(0x07040f, 1)

  const canvas = renderer.domElement as HTMLCanvasElement
  canvas.style.position = 'absolute'
  canvas.style.top = '0'
  canvas.style.left = '0'
  canvas.style.width = '100%'
  canvas.style.height = '100%'
  canvas.style.zIndex = '0'
  canvas.style.opacity = '0'
  canvas.style.transition = 'opacity 0.9s ease'

  scene = new THREE.Scene()
  camera = new THREE.PerspectiveCamera(50, 1, 0.1, 1500)
  camera.position.set(CAMERA_BASE.x, CAMERA_BASE.y, CAMERA_BASE.z)
  camera.lookAt(CAMERA_TARGET.x, CAMERA_TARGET.y, CAMERA_TARGET.z)

  // Uniforms shared by reference between all materials
  common = {
    uTime: { value: 0 },
    uPhase: { value: 0 },
    uAmp: { value: BASE_AMPLITUDE },
    uRipples: { value: Array.from({ length: MAX_RIPPLES }, () => new THREE.Vector4(0, 0, -100, 0)) },
    uRippleColors: { value: Array.from({ length: MAX_RIPPLES }, () => new THREE.Vector3(1, 1, 1)) },
    uSendAge: { value: -1 },
    uSat: { value: 1 },
    uLinearOut: { value: 0 },
    uVoice: { value: new Float32Array(VOICE_SAMPLES) },
    uVoiceOn: { value: 0 },
  }

  buildSky()
  buildGrid()
  buildParticles()
  buildPillars()
  await setupPostProcessing()

  clock = new THREE.Clock()
  singletonCreated = true
}

// ═══════════════════════════════════════════════════════════════════════════
// Colour
// ═══════════════════════════════════════════════════════════════════════════

function applyProjectColor(projectHex: string) {
  if (!gridMesh || !skyMesh) return
  const tint = hexToRgb(projectHex)
  const t = (base: RGB, amount: number) => (tint ? mixRgb(base, tint, amount) : base)

  const g = gridMesh.material.uniforms
  const s = skyMesh.material.uniforms
  const horizon = t(PALETTE.horizon, 0.35)
  g.uLinePeak.value.set(...t(PALETTE.linePeak, 0.4))
  g.uLineHigh.value.set(...t(PALETTE.lineHigh, 0.2))
  g.uHorizon.value.set(...horizon)
  s.uHorizon.value.set(...horizon)
  s.uSunBottom.value.set(...t(PALETTE.sunBottom, 0.35))
  if (particles) particles.material.uniforms.uColorA.value.set(...t(PALETTE.linePeak, 0.4))
}

// ═══════════════════════════════════════════════════════════════════════════
// Frame loop
// ═══════════════════════════════════════════════════════════════════════════

// ═══════════════════════════════════════════════════════════════════════════
// Fly mode
// ═══════════════════════════════════════════════════════════════════════════

const WAVES_FLY = Symbol('zen-waves')
let flight: ReturnType<typeof createWavesFlight> | null = null
let flyReportAt = 0
let flyFovActive = false

/** The wave surface height at (x, z) — the same sum of sines as the shader
 *  (without event ripples), so the ship can skim just above it. */
function waveHeightAt(x: number, z: number) {
  const t = state.phase
  const A = state.amp
  const F = 0.08
  return Math.sin(x * F + t * 0.8) * A * 0.6
    + Math.cos(z * F * 0.7 + t * 0.5) * A * 0.4
    + Math.sin((x + z) * F * 1.3 + t * 0.6) * A * 0.25
    + Math.sin(x * F * 2.5 + t * 1.2) * A * 0.1
    + Math.cos(z * F * 2.0 - t * 0.9) * A * 0.08
}

function setWavesFlying(on: boolean) {
  if (!scene || !renderer) return
  if (on) {
    if (!flight) {
      flight = createWavesFlight({
        THREE, scene, canvas: renderer.domElement, reducedMotion,
        bounds: {
          cx: 0, cz: -60, softR: 95, hardR: 135,
          floor: (x, z) => waveHeightAt(x, z) + 2.2,
          ceil: 45,
          orbY: (x, z) => { const f = waveHeightAt(x, z); return [f + 3, f + 14] },
        },
        onExitRequest: () => setWavesFlying(false),
        onPickup: (_points, gold, x, z) => {
          addRipple(x, z, gold ? 1 : 0.6, gold ? [1.0, 0.8, 0.35] : [0.5, 0.9, 1.0])
          state.pulse = Math.min(1, state.pulse + (gold ? 0.7 : 0.35))
          try { getZenAudio().playToolBip(gold ? 'Edit' : 'Read') } catch { /* sound off */ }
        },
      })
    }
    // Take off just ahead of the camera, heading into the grid (-Z)
    flight.start(new THREE.Vector3(camera.position.x, 10, camera.position.z - 22), Math.PI / 2)
  } else {
    flight?.stop()
  }
  zenFlyState.active = on
  zenFlyState.near = ''
}

function render() {
  if (composer) composer.render()
  else renderer.render(scene, camera)
}

function animate() {
  if (!renderer || !scene || !camera || !clock) return
  animationFrame = requestAnimationFrame(animate)

  const dt = Math.min(clock.getDelta(), 0.1)
  const elapsed: number = clock.getElapsedTime()
  const motion = reducedMotion ? 0.35 : 1

  // Smooth towards status targets
  state.amp = damp(state.amp, state.targetAmp * (1 + state.agentBoost), 1.2, dt)
  state.speed = damp(state.speed, state.targetSpeed, 1.2, dt)
  state.activity = damp(state.activity, state.targetActivity, 1.5, dt)
  state.flow = damp(state.flow, state.targetFlow, 1.0, dt)
  state.scrollSpeed = damp(state.scrollSpeed, state.targetScroll, 0.8, dt)
  state.sat = damp(state.sat, state.targetSat, 1.0, dt)
  state.pulse = Math.max(0, state.pulse - dt * 1.4)

  // Integrate phases so speed changes never make the waves jump
  state.phase += dt * state.speed * motion
  state.flowPos += dt * (0.012 + state.flow * 0.07) * motion
  state.scroll += dt * state.scrollSpeed * 9 * motion

  common.uTime!.value = elapsed
  common.uPhase!.value = state.phase
  common.uAmp!.value = state.amp
  common.uSat!.value = state.sat
  common.uSendAge!.value = state.sendStart >= 0 ? elapsed - state.sendStart : -1
  if (state.sendStart >= 0 && elapsed - state.sendStart > SEND_DURATION) {
    state.sendStart = -1
    common.uSendAge!.value = -1
  }

  // Voice: shift the mic level into the history at a fixed rate so rings
  // travel at a constant speed; keep shifting after the mic stops so the
  // last rings roll out instead of vanishing
  const micLive = isMicActive()
  state.voiceOn = damp(state.voiceOn, micLive ? 1 : 0, micLive ? 3 : 1.2, dt)
  state.voiceNow = micLive ? getMicLevel() : 0
  const hist = common.uVoice!.value as Float32Array
  if (state.voiceOn > 0.001 || hist[0]! > 0) {
    state.voiceAcc += dt
    while (state.voiceAcc >= VOICE_DT) {
      state.voiceAcc -= VOICE_DT
      hist.copyWithin(1, 0, VOICE_SAMPLES - 1)
      hist[0] = state.voiceNow * (reducedMotion ? 0.5 : 1)
    }
  } else {
    state.voiceAcc = 0
  }
  common.uVoiceOn!.value = state.voiceOn

  // Heartbeat while working
  const beat = Math.pow(Math.max(0, Math.sin(elapsed * 2.4)), 12) * state.activity * 0.6

  const g = gridMesh.material.uniforms
  g.uScroll.value = state.scroll
  g.uActivity.value = state.activity

  const s = skyMesh.material.uniforms
  s.uActivity.value = state.activity
  s.uPulse.value = Math.min(1, state.pulse + beat + state.voiceNow * 0.7)
  s.uStripeTime.value = (s.uStripeTime.value + dt * (0.04 + state.activity * 0.22) * motion) % 1

  const p = particles.material.uniforms
  p.uFlow.value = state.flowPos
  p.uAlpha.value = 0.18 + state.activity * 0.75

  // Pillars
  for (const pillar of pillars) {
    if (!pillar.active) continue
    const age = elapsed - pillar.start
    if (age > PILLAR_LIFE) {
      pillar.active = false
      pillar.mesh.visible = false
      continue
    }
    const grow = 1 - Math.pow(1 - Math.min(age / 0.28, 1), 3)
    const fade = Math.pow(1 - age / PILLAR_LIFE, 1.4)
    pillar.mesh.scale.set(1 + age * 0.6, 34 * grow, 1 + age * 0.6)
    pillar.mesh.material.uniforms.uAlpha.value = fade
  }

  if (bloomPass) {
    bloomPass.strength = 0.55 + state.activity * 0.25 + (s.uPulse.value as number) * 0.2 + state.voiceNow * 0.35
  }

  // Camera: intro fly-in, gentle sway, lean in while working
  let intro = 1
  if (state.introStart >= 0) {
    intro = Math.min(1, (elapsed - state.introStart) / INTRO_DURATION)
    if (intro >= 1) state.introStart = -1
  }
  const ease = 1 - Math.pow(1 - intro, 3)
  const sway = reducedMotion ? 0 : 1
  camera.position.x = CAMERA_BASE.x + Math.sin(elapsed * 0.05) * 3 * sway
  camera.position.y = CAMERA_BASE.y + Math.sin(elapsed * 0.08) * 1.2 * sway + (1 - ease) * 26
  camera.position.z = CAMERA_BASE.z - state.activity * 4 + (1 - ease) * 30
  const lookY = CAMERA_TARGET.y - (1 - ease) * 10
  const fv = flight?.update(dt, elapsed) || null
  if (fv) {
    // Glide between the orbit view and the chase camera
    const b = fv.blend
    camera.position.lerp(fv.pos, b)
    camera.lookAt(
      CAMERA_TARGET.x + (fv.look.x - CAMERA_TARGET.x) * b,
      lookY + (fv.look.y - lookY) * b,
      CAMERA_TARGET.z + (fv.look.z - CAMERA_TARGET.z) * b,
    )
    if (fv.bank) camera.rotateZ(fv.bank)
    camera.fov = 50 + (fv.fov - 50) * b
    camera.updateProjectionMatrix()
    flyFovActive = true
    if (flight?.active && elapsed - flyReportAt > 0.12) {
      flyReportAt = elapsed
      const info = flight.info()
      zenFlyState.speed = info.speed
      zenFlyState.throttle = info.throttle
    }
  } else {
    camera.lookAt(CAMERA_TARGET.x, lookY, CAMERA_TARGET.z)
    if (flyFovActive) {
      camera.fov = 50
      camera.updateProjectionMatrix()
      flyFovActive = false
    }
  }
  skyMesh.position.copy(camera.position)

  render()
}

function handleResize() {
  if (!renderer || !camera || !activeContainer) return
  const width = activeContainer.clientWidth || 800
  const height = activeContainer.clientHeight || 600
  camera.aspect = width / height
  camera.updateProjectionMatrix()
  renderer.setSize(width, height)
  if (composer) {
    composer.setPixelRatio(Math.min(window.devicePixelRatio, 1.5))
    composer.setSize(width, height)
  }
  if (particles) particles.material.uniforms.uPixelRatio.value = renderer.getPixelRatio()
}

// ═══════════════════════════════════════════════════════════════════════════
// Events
// ═══════════════════════════════════════════════════════════════════════════

function randomSurfacePoint(): [number, number] {
  return [(Math.random() - 0.5) * 70, -Math.random() * 55 + 8]
}

function addRipple(x: number, z: number, intensity: number, color: RGB) {
  if (!clock) return
  const i = rippleCursor
  rippleCursor = (rippleCursor + 1) % MAX_RIPPLES
  common.uRipples!.value[i].set(x, z, clock.getElapsedTime(), intensity)
  common.uRippleColors!.value[i].set(color[0], color[1], color[2])
}

function spawnPillar(x: number, z: number, color: RGB) {
  if (!clock || !pillars.length) return
  const pillar = pillars[pillarCursor]!
  pillarCursor = (pillarCursor + 1) % pillars.length
  pillar.start = clock.getElapsedTime()
  pillar.active = true
  pillar.mesh.position.set(x, -1, z)
  pillar.mesh.scale.set(1, 0.001, 1)
  pillar.mesh.material.uniforms.uColor.value.set(color[0], color[1], color[2])
  pillar.mesh.material.uniforms.uAlpha.value = 1
  pillar.mesh.visible = true
}

function triggerPulse(intensity: number = 0.8) {
  if (!singletonCreated) return
  const [x, z] = randomSurfacePoint()
  addRipple(x, z, Math.min(1, intensity) * 0.8, PALETTE.lineHigh)
}

function triggerBeamDrop(type: 'message' | 'tool' | 'error' = 'message', customColor?: [number, number, number]) {
  if (!singletonCreated) return
  let color: RGB
  if (customColor) color = [...customColor] as RGB
  else if (type === 'tool') color = [0.2, 0.85, 1.0]
  else if (type === 'error') color = [1.0, 0.3, 0.3]
  else color = [0.62, 0.45, 1.0]
  const [x, z] = randomSurfacePoint()
  addRipple(x, z, 0.55 + Math.random() * 0.25, color)
  state.pulse = Math.max(state.pulse, 0.35)
}

/** A tool call: light pillar + ripple coloured by tool category */
function triggerToolBeam(toolName: string, isError = false) {
  if (!singletonCreated) return
  const color = toolColor(toolName, isError)
  const [x, z] = randomSurfacePoint()
  spawnPillar(x, z, color)
  addRipple(x, z, isError ? 1.0 : 0.75, color)
  state.pulse = Math.max(state.pulse, isError ? 0.9 : 0.5)
}

function applyStatus(status: string) {
  currentStatus = status
  const t = STATUS_TARGETS[status] || STATUS_TARGETS.idle!
  state.targetAmp = BASE_AMPLITUDE * t.amp
  state.targetSpeed = BASE_SPEED * t.speed
  state.targetActivity = t.activity
  state.targetFlow = t.flow
  state.targetScroll = t.scroll
  state.targetSat = t.sat
}

let sendCalmTimer: ReturnType<typeof setTimeout> | null = null

function triggerSendPulse() {
  if (!clock || !singletonCreated) return
  state.sendStart = clock.getElapsedTime()
  state.pulse = 1
  state.targetAmp = BASE_AMPLITUDE * 2.0
  state.targetSpeed = BASE_SPEED * 1.9
  state.targetActivity = 1
  state.targetScroll = 1.4

  if (sendCalmTimer) clearTimeout(sendCalmTimer)
  sendCalmTimer = setTimeout(() => {
    sendCalmTimer = null
    applyStatus(currentStatus) // settle to whatever the session is doing now
  }, SEND_DURATION * 800)

  addRipple(0, 20, 0.6, [0.85, 0.75, 1.0])
}

// ═══════════════════════════════════════════════════════════════════════════
// Attach / detach
// ═══════════════════════════════════════════════════════════════════════════

/** Attach the singleton canvas to a container and start/resume animation */
function attach(container: HTMLElement | null) {
  if (!container || !renderer) return
  const wasAttached = activeContainer === container
  activeContainer = container

  const canvas = renderer.domElement as HTMLCanvasElement
  if (canvas.parentElement !== container) {
    canvas.style.opacity = '0'
    container.appendChild(canvas)
  }

  handleResize()

  if (!wasAttached && clock) {
    clock.getDelta()
    state.introStart = reducedMotion ? -1 : clock.getElapsedTime()
  }

  if (!animationFrame) {
    animate()
  }
  registerFlyTarget(WAVES_FLY, setWavesFlying)
  // Fade the canvas in once a frame has been drawn
  requestAnimationFrame(() => {
    canvas.style.opacity = '1'
  })
}

/** Detach the canvas from the DOM and pause animation */
function detach() {
  registerFlyTarget(WAVES_FLY, null)
  if (flight?.active) setWavesFlying(false)
  if (animationFrame) {
    cancelAnimationFrame(animationFrame)
    animationFrame = null
  }
  if (renderer?.domElement?.parentElement) {
    renderer.domElement.parentElement.removeChild(renderer.domElement)
  }
  activeContainer = null
}

// ═══════════════════════════════════════════════════════════════════════════
// Composable — per-component watchers and lifecycle hooks
// ═══════════════════════════════════════════════════════════════════════════

/** Start downloading three.js ahead of time (e.g. when the user hovers "Zen"). */
export function preloadZenScene() {
  if (typeof window === 'undefined') return
  import('three').catch(() => {})
}

export function useZenAnimation(options: ZenAnimationOptions) {
  const isInitialized = ref(singletonCreated)

  if (!visibilityListenerRegistered && typeof document !== 'undefined') {
    document.addEventListener('visibilitychange', handleVisibilityChange)
    visibilityListenerRegistered = true
  }

  let unmounted = false

  async function init() {
    if (!singletonCreated) {
      if (!creating) creating = createScene()
      await creating
    }
    if (unmounted) return
    applyStatus(options.sessionStatus.value)
    // Jump straight to the status look on first attach (no slow ramp)
    state.activity = state.targetActivity
    state.sat = state.targetSat
    applyProjectColor(options.projectColor.value)
    attach(options.container.value)
    isInitialized.value = true
  }

  watch(options.sessionStatus, (status) => {
    applyStatus(status)
  })

  watch(options.runningAgentCount, (count, oldCount) => {
    state.agentBoost = Math.min(0.6, count * 0.12)
    if (count > (oldCount || 0)) triggerPulse(0.5)
    else if (count < (oldCount || 0)) triggerPulse(0.3)
  })

  watch(options.projectColor, (color) => {
    applyProjectColor(color)
  })

  onUnmounted(() => {
    unmounted = true
    detach()
  })

  return {
    isInitialized,
    init,
    detach,
    handleResize,
    triggerPulse,
    triggerBeamDrop,
    triggerToolBeam,
    triggerSendPulse
  }
}
