/**
 * Samples how the dashboard itself is doing (DOM size, running animations,
 * frame rate, JS memory) and posts it to /api/perf/client, where the server
 * keeps it next to its own readings. Read in Settings → Performance.
 */
export interface ClientPerfSample {
  dom_nodes: number
  animations: number
  fps: number
  messages: number
  sessions: number
  js_heap_mb: number
  /** Main-thread tasks over 50 ms since the previous sample */
  long_tasks: number
  /** The longest of them (ms) */
  max_stall_ms: number
  visible: boolean
  page: string
}

export const PERF_SAMPLE_INTERVAL_MS = 30_000
const FPS_WINDOW_MS = 1000

/** Frames per second over about one second, or 0 when the page is hidden. */
export function measureFps(
  raf: (cb: (t: number) => void) => number = window.requestAnimationFrame.bind(window),
  windowMs = FPS_WINDOW_MS,
): Promise<number> {
  return new Promise((resolve) => {
    let frames = 0
    let start = 0
    const tick = (t: number) => {
      if (!start) start = t
      frames++
      if (t - start >= windowMs) resolve((frames - 1) * 1000 / (t - start))
      else raf(tick)
    }
    raf(tick)
  })
}

/** Running animations (CSS, transitions, Web Animations), not finished or paused ones. */
export function countRunningAnimations(doc: Pick<Document, 'getAnimations'>): number {
  try {
    return doc.getAnimations().filter(a => a.playState === 'running').length
  } catch {
    return 0
  }
}

export interface RunningAnimation {
  name: string
  count: number
  /** A selector-like description of one element it runs on. */
  example: string
}

function describeTarget(el: Element | null | undefined): string {
  if (!el) return '(none)'
  const cls = typeof el.className === 'string' ? el.className.trim().split(/\s+/).filter(Boolean).slice(0, 2).join('.') : ''
  return `${el.tagName.toLowerCase()}${el.id ? '#' + el.id : ''}${cls ? '.' + cls : ''}`
}

/**
 * Which animations are running right now, grouped by name, most common first:
 * the way to find what keeps the GPU process busy.
 */
export function listRunningAnimations(doc: Pick<Document, 'getAnimations'>): RunningAnimation[] {
  const groups = new Map<string, RunningAnimation>()
  try {
    for (const a of doc.getAnimations()) {
      if (a.playState !== 'running') continue
      const name = (a as CSSAnimation).animationName
        || (a as CSSTransition).transitionProperty
        || 'script animation'
      const target = (a.effect as KeyframeEffect | null)?.target ?? null
      const g = groups.get(name)
      if (g) g.count++
      else groups.set(name, { name, count: 1, example: describeTarget(target) })
    }
  } catch { /* getAnimations unavailable */ }
  return [...groups.values()].sort((x, y) => y.count - x.count || x.name.localeCompare(y.name))
}

export const LONG_TASK_MS = 50
const PROBE_MS = 200

export interface StallMeter {
  /** Long tasks and the longest stall since the previous call. */
  take(): { longTasks: number, maxStallMs: number }
  stop(): void
}

/**
 * Counts main-thread stalls. Uses the Long Tasks API where it exists
 * (Chromium); WebKit (the desktop app) has none, so there a timer measures how
 * late it fires: a 200 ms timer that fires 2 s late means the thread was busy
 * for about 2 s. Hidden pages are skipped, since browsers throttle their timers.
 */
export function createStallMeter(env: {
  now?: () => number
  hidden?: () => boolean
  setInterval?: (fn: () => void, ms: number) => unknown
  clearInterval?: (h: any) => void
  observer?: typeof PerformanceObserver | undefined
} = {}): StallMeter {
  const now = env.now ?? (() => performance.now())
  const hidden = env.hidden ?? (() => typeof document !== 'undefined' && document.visibilityState !== 'visible')
  let count = 0
  let max = 0
  const note = (ms: number) => { count++; if (ms > max) max = ms }

  const PO = 'observer' in env ? env.observer : (typeof PerformanceObserver !== 'undefined' ? PerformanceObserver : undefined)
  let po: PerformanceObserver | null = null
  try {
    if (PO && (PO as any).supportedEntryTypes?.includes('longtask')) {
      po = new PO((list) => {
        if (hidden()) return
        for (const e of list.getEntries()) note(e.duration)
      })
      po.observe({ type: 'longtask', buffered: false } as PerformanceObserverInit)
    }
  } catch { po = null }

  let timer: unknown = null
  if (!po) {
    const si = env.setInterval ?? ((fn: () => void, ms: number) => setInterval(fn, ms))
    let last = now()
    timer = si(() => {
      const t = now()
      const late = t - last - PROBE_MS
      last = t
      if (!hidden() && late > LONG_TASK_MS) note(late)
    }, PROBE_MS)
  }

  return {
    take() {
      const r = { longTasks: count, maxStallMs: Math.round(max) }
      count = 0
      max = 0
      return r
    },
    stop() {
      try { po?.disconnect() } catch { /* ignore */ }
      if (timer !== null) (env.clearInterval ?? clearInterval)(timer as any)
      timer = null
    },
  }
}

export interface ClientPerfEnv {
  document: Document
  path: string
  messages: number
  sessions: number
  fps: number
  heapBytes?: number
  longTasks?: number
  maxStallMs?: number
}

export function buildClientSample(env: ClientPerfEnv): ClientPerfSample {
  return {
    dom_nodes: env.document.getElementsByTagName('*').length,
    animations: countRunningAnimations(env.document),
    fps: Math.round(env.fps * 10) / 10,
    messages: env.messages,
    sessions: env.sessions,
    js_heap_mb: env.heapBytes ? Math.round(env.heapBytes / 1048576 * 10) / 10 : 0,
    long_tasks: env.longTasks ?? 0,
    max_stall_ms: env.maxStallMs ?? 0,
    visible: env.document.visibilityState === 'visible',
    page: env.path,
  }
}

type Post = (sample: ClientPerfSample) => Promise<unknown> | unknown

/**
 * Starts the sampler. Samples once shortly after start and then on an interval;
 * a hidden page is reported as such (without measuring frames, which the
 * browser would not run anyway). Returns a stop function.
 */
export function startPerfReporter(opts: {
  post: Post
  getContext: () => { path: string, messages: number, sessions: number }
  intervalMs?: number
}): () => void {
  let stopped = false
  let timer: ReturnType<typeof setInterval> | null = null
  let first: ReturnType<typeof setTimeout> | null = null
  const stalls = createStallMeter()

  const sample = async () => {
    if (stopped) return
    const hidden = document.visibilityState !== 'visible'
    const ctx = opts.getContext()
    const fps = hidden ? 0 : await measureFps()
    if (stopped) return
    const heap = (performance as unknown as { memory?: { usedJSHeapSize: number } }).memory?.usedJSHeapSize
    try {
      const { longTasks, maxStallMs } = stalls.take()
      await opts.post(buildClientSample({ document, fps, heapBytes: heap, longTasks, maxStallMs, ...ctx }))
    } catch { /* monitoring must never get in the way */ }
  }

  first = setTimeout(sample, 5000)
  timer = setInterval(sample, opts.intervalMs ?? PERF_SAMPLE_INTERVAL_MS)
  return () => {
    stopped = true
    stalls.stop()
    if (first) clearTimeout(first)
    if (timer) clearInterval(timer)
  }
}
