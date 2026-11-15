/**
 * Short, readable model labels for subagents: "claude-haiku-4-5-20251001" →
 * "Haiku 4.5", "claude-sonnet-5-5" → "Sonnet 5.5", "opus" → "Opus".
 * Anything else (GPT, Codex, provider paths) is shortened, not renamed.
 */

const FAMILIES: Record<string, string> = { opus: 'Opus', sonnet: 'Sonnet', haiku: 'Haiku', fable: 'Fable', mythos: 'Mythos' }

const MAX_OTHER = 24

export function modelLabel(model: string | null | undefined): string {
  const raw = (model ?? '').trim()
  if (!raw) return ''
  // "anthropic/claude-sonnet-4-5", "us.anthropic.claude-...-v1:0", "claude-opus-4-1[1m]"
  const base = (raw.split('/').pop() ?? raw).replace(/\[[^\]]*\]$/, '')
  const id = base.toLowerCase()

  // claude-<family>-<major>[-<minor>][-<date>], and the older claude-<major>-<minor>-<family>
  const named = id.match(/(?:^|[.\-_])(opus|sonnet|haiku|fable|mythos)(?:-(\d+)(?:[-.](\d{1,2}))?)?(?=$|[-@:_])/)
  if (named) {
    const family = FAMILIES[named[1]!]!
    // A date suffix (claude-haiku-4-5-20251001 is caught above; claude-3-5-sonnet-20241022 here) is no version.
    let version = named[2] && named[2].length < 6 ? named[2] + (named[3] ? '.' + named[3] : '') : ''
    if (!version) {
      const old = id.match(/claude-(\d+)(?:[-.](\d))?-(?:opus|sonnet|haiku)/)
      if (old) version = old[1] + (old[2] ? '.' + old[2] : '')
    }
    return version ? `${family} ${version}` : family
  }
  return base.length > MAX_OTHER ? base.slice(0, MAX_OTHER - 1) + '…' : base
}

/** Tooltip for a subagent's model chip. */
export function modelTitle(model: string | null | undefined, source?: string | null): string {
  const raw = (model ?? '').trim()
  if (!raw) return ''
  return source === 'requested'
    ? `Model requested at launch: ${raw} (not yet confirmed by the agent)`
    : `Runs on ${raw}`
}

/**
 * The full name of a model whose id hides its version: DeepSeek's API serves
 * V4.1 Flash as plain `deepseek-flash`. Shown next to the provider in the model
 * picker and matched by its search ("4.1", "flash"). Unknown ids give ''.
 */
const MODEL_FULL_NAMES: Record<string, string> = {
  'deepseek-flash': 'V4.1 Flash',
  'deepseek-v4-pro': 'V4 Pro',
}

export function modelFullName(model: string | null | undefined): string {
  return MODEL_FULL_NAMES[(model ?? '').trim().toLowerCase()] ?? ''
}
