/**
 * Agent question policy (backend: AskUserQuestion in Claude sessions).
 *
 * What happens when an agent asks a question and nobody answers:
 *   wait        the agent waits without limit
 *   auto_after  after `timeout_seconds` the "(Recommended)" option is picked
 *   auto        it is picked at once
 * There is a global default and an optional per-session override (null =
 * follow the global policy). Confirmation cards (tool approvals, loop
 * proposals: `requires_user`) never auto-answer.
 */

export type QuestionPolicyMode = 'wait' | 'auto_after' | 'auto'

export interface QuestionPolicy {
  mode: QuestionPolicyMode
  /** Only for auto_after (10..86400). */
  timeout_seconds?: number
}

export const DEFAULT_QUESTION_POLICY: QuestionPolicy = { mode: 'auto_after', timeout_seconds: 120 }

/** The waits the UI offers for "Auto-pick after…" (seconds). */
export const QUESTION_TIMEOUT_CHOICES = [30, 60, 120, 300, 600, 1800] as const

export const MIN_QUESTION_TIMEOUT = 10
export const MAX_QUESTION_TIMEOUT = 86400

const MODES = new Set<QuestionPolicyMode>(['wait', 'auto_after', 'auto'])

/** A policy from a server payload, or null when it is missing or malformed. */
export function parseQuestionPolicy(raw: unknown): QuestionPolicy | null {
  if (!raw || typeof raw !== 'object') return null
  const mode = (raw as any).mode
  if (typeof mode !== 'string' || !MODES.has(mode as QuestionPolicyMode)) return null
  if (mode !== 'auto_after') return { mode: mode as QuestionPolicyMode }
  const t = Number((raw as any).timeout_seconds)
  if (!Number.isFinite(t) || t <= 0) return { ...DEFAULT_QUESTION_POLICY }
  const seconds = Math.min(MAX_QUESTION_TIMEOUT, Math.max(MIN_QUESTION_TIMEOUT, Math.round(t)))
  return { mode: 'auto_after', timeout_seconds: seconds }
}

/** "30 s", "1 min", "90 s", "2 min", "1 h", "1 h 30 min". */
export function formatWait(seconds: number): string {
  const s = Math.max(0, Math.round(seconds))
  if (s < 60 || (s < 600 && s % 60 !== 0)) return `${s} s`
  const minutes = Math.round(s / 60)
  if (minutes < 60) return `${minutes} min`
  const h = Math.floor(minutes / 60)
  const m = minutes % 60
  return m ? `${h} h ${m} min` : `${h} h`
}

/** "Wait for me", "Auto-pick after 2 min", "Auto-pick now". */
export function questionPolicyLabel(policy: QuestionPolicy | null | undefined): string {
  const p = policy || DEFAULT_QUESTION_POLICY
  if (p.mode === 'wait') return 'Wait for me'
  if (p.mode === 'auto') return 'Auto-pick now'
  return `Auto-pick after ${formatWait(p.timeout_seconds ?? DEFAULT_QUESTION_POLICY.timeout_seconds!)}`
}

/** Lower-case short form: "wait for me", "auto after 2 min", "auto now". */
export function questionPolicyShort(policy: QuestionPolicy | null | undefined): string {
  const p = policy || DEFAULT_QUESTION_POLICY
  if (p.mode === 'wait') return 'wait for me'
  if (p.mode === 'auto') return 'auto now'
  return `auto after ${formatWait(p.timeout_seconds ?? DEFAULT_QUESTION_POLICY.timeout_seconds!)}`
}

/** "Default: auto after 2 min" — what a session without an override follows. */
export function describeDefaultPolicy(globalPolicy: QuestionPolicy | null | undefined): string {
  return `Default: ${questionPolicyShort(globalPolicy)}`
}

export function samePolicy(a: QuestionPolicy | null | undefined, b: QuestionPolicy | null | undefined): boolean {
  if (!a || !b) return !a && !b
  if (a.mode !== b.mode) return false
  return a.mode !== 'auto_after' || a.timeout_seconds === b.timeout_seconds
}

// ── Dropdown keys ──────────────────────────────────────────────────────────
// One flat list of choices: "default" (null), "wait", "auto_after:<s>", "auto".

export const DEFAULT_POLICY_KEY = 'default'

export function policyKey(policy: QuestionPolicy | null | undefined): string {
  if (!policy) return DEFAULT_POLICY_KEY
  if (policy.mode === 'auto_after') return `auto_after:${policy.timeout_seconds ?? DEFAULT_QUESTION_POLICY.timeout_seconds}`
  return policy.mode
}

export function policyFromKey(key: string | number | null | undefined): QuestionPolicy | null {
  const k = String(key ?? '')
  if (k === 'wait' || k === 'auto') return { mode: k }
  const m = /^auto_after:(\d+)$/.exec(k)
  if (m) return parseQuestionPolicy({ mode: 'auto_after', timeout_seconds: Number(m[1]) })
  return null
}

export interface PolicyChoice {
  value: string
  label: string
  description?: string
}

/**
 * Choices for a per-session (or new session) picker. A current custom wait
 * that is not one of the offered ones is kept in the list.
 */
export function questionPolicyChoices(globalPolicy: QuestionPolicy | null | undefined, current?: QuestionPolicy | null): PolicyChoice[] {
  const waits: number[] = [...QUESTION_TIMEOUT_CHOICES]
  const t = current?.mode === 'auto_after' ? current.timeout_seconds : undefined
  if (t && !waits.includes(t)) waits.push(t)
  waits.sort((a, b) => a - b)
  return [
    { value: DEFAULT_POLICY_KEY, label: `Default (${questionPolicyShort(globalPolicy)})`, description: 'Follows Settings → Agents' },
    { value: 'wait', label: 'Wait for me', description: 'The agent waits until you answer' },
    ...waits.map(s => ({ value: `auto_after:${s}`, label: `Auto-pick after ${formatWait(s)}` })),
    { value: 'auto', label: 'Auto-pick now', description: 'The recommended option is picked at once' },
  ]
}

// ── Countdown ─────────────────────────────────────────────────────────────

/** Milliseconds until an RFC3339 time (negative when past), or null. */
export function msUntil(iso: string | null | undefined, now: number = Date.now()): number | null {
  if (!iso) return null
  const at = Date.parse(iso)
  return Number.isFinite(at) ? at - now : null
}

/** "1:42", "0:05", "12:00", "1:05:00". Rounds up, never negative. */
export function formatCountdown(ms: number): string {
  const total = Math.max(0, Math.ceil(ms / 1000))
  const h = Math.floor(total / 3600)
  const m = Math.floor((total % 3600) / 60)
  const s = total % 60
  const ss = String(s).padStart(2, '0')
  return h ? `${h}:${String(m).padStart(2, '0')}:${ss}` : `${m}:${ss}`
}

// ── Auto-pick ─────────────────────────────────────────────────────────────

const RECOMMENDED_RE = /\(recommended\)\s*$/i

export function isRecommendedLabel(label: string | null | undefined): boolean {
  return RECOMMENDED_RE.test(label || '')
}

interface QuestionLike {
  options?: Array<{ label: string }>
  multiSelect?: boolean
  multi_select?: boolean
  recommended?: unknown
  requires_user?: boolean
}

/** Option indexes from the server's `recommended` field (defensive). */
export function parseRecommended(raw: unknown, optionCount?: number): number[] {
  if (!Array.isArray(raw)) return []
  const out: number[] = []
  for (const v of raw) {
    const n = Number(v)
    if (!Number.isInteger(n) || n < 0) continue
    if (optionCount !== undefined && n >= optionCount) continue
    if (!out.includes(n)) out.push(n)
  }
  return out
}

/**
 * The options the auto-pick would choose: the server's `recommended` indexes,
 * else the same rule the server uses (labels ending in "(Recommended)";
 * single-select the first, multi-select all; none → option 0).
 */
export function autoPickIndexes(question: QuestionLike | null | undefined): number[] {
  const options = question?.options || []
  if (!options.length || question?.requires_user) return []
  const fromServer = parseRecommended(question?.recommended, options.length)
  if (fromServer.length) return fromServer
  const multi = !!(question?.multiSelect ?? question?.multi_select)
  const rec = options.map((o, i) => (isRecommendedLabel(o.label) ? i : -1)).filter(i => i >= 0)
  if (!rec.length) return [0]
  return multi ? rec : [rec[0]!]
}

export function autoPickLabels(question: QuestionLike | null | undefined): string[] {
  const options = question?.options || []
  return autoPickIndexes(question).map(i => options[i]!.label)
}

// ── Where questions exist ─────────────────────────────────────────────────

/** Providers that run on the Claude CLI (the only runtime with AskUserQuestion). */
const CLAUDE_CLI_PROVIDERS = new Set(['claude', 'anthropic', 'deepseek', 'glm', 'kimi'])
/** Providers that always use the OpenAI-compatible loop. */
const DIRECT_PROVIDERS = new Set(['openai', 'ollama', 'openrouter', 'lmstudio', 'litellm'])

/**
 * Whether a session on this provider/model can ask questions: true on the
 * Claude CLI, false for Codex and the OpenAI-compatible loop, and true for
 * unknown/custom providers (they may use an Anthropic-compatible URL).
 */
export function questionPolicyApplies(provider?: string | null, model?: string | null): boolean {
  const p = (provider || 'claude').trim().toLowerCase()
  if (p === 'codex') return false
  if (DIRECT_PROVIDERS.has(p)) return false
  // Claude CLI providers and custom ones (maybe Anthropic-compatible); a
  // model name containing "codex" routes to Codex.
  return !/codex/i.test(model || '')
}

/** Known Claude CLI providers (for notes that say "Claude sessions only"). */
export function isClaudeCliProvider(provider?: string | null): boolean {
  return CLAUDE_CLI_PROVIDERS.has((provider || 'claude').trim().toLowerCase())
}
