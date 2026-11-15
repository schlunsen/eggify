/**
 * Characters for the Flying Circus zen stage: cut-out Victorian archetypes in
 * the spirit of 1970s British collage animation. Each subagent gets one from
 * its id, so it looks the same everywhere it is shown (zen stage, overview).
 */
import { hashString, type SpeechBubble } from './subagentCharacters'

export type CircusKind = 'gent' | 'knight' | 'hanky' | 'parrot' | 'egg' | 'lady'

export interface CircusCharacter {
  kind: CircusKind
  /** Coat, tunic, feathers or dress. */
  color: string
  /** Tie, plume, tank top, hat band. */
  accent: string
  /** Walking pace, as a share of the stage per second. */
  pace: number
}

export const CIRCUS_KINDS: CircusKind[] = ['gent', 'knight', 'hanky', 'parrot', 'egg', 'lady']

/**
 * The engraved egg each kind is drawn as (public/zen/circus/eggs, made by
 * scripts/generate_circus_eggs.sh + process_circus_eggs.py): the City gent,
 * the knight, the knotted hanky, the explorer with a parrot, the professor and
 * the lady. Width / height of each file, so a figure can be sized before it loads.
 */
export const EGG_ASPECT: Record<CircusKind, number> = {
  gent: 381 / 600,
  knight: 263 / 600,
  hanky: 274 / 600,
  parrot: 388 / 600,
  egg: 358 / 600,
  lady: 319 / 600,
}
export const circusEggArt = (kind: CircusKind) => `/zen/circus/eggs/egg-${kind}.webp`
// Hand-tinted engraving colours (see the Flying Circus theme).
const COATS = ['#3b3b45', '#2a9d8f', '#c2185b', '#8a6510', '#4a6fa5', '#6b4f3a', '#7a3b69', '#2f5d50']
const ACCENTS = ['#e8458b', '#d4a017', '#2a9d8f', '#f08a4b', '#a8d8cc', '#f2c14e']

export function circusCharacterFor(agentId: string): CircusCharacter {
  const h = hashString(agentId)
  const kind = CIRCUS_KINDS[h % CIRCUS_KINDS.length]!
  // A parrot is always a Norwegian shade of blue; the rest pick a coat.
  const color = kind === 'parrot' ? '#3d7cc9' : COATS[(h >>> 3) % COATS.length]!
  let accent = ACCENTS[(h >>> 9) % ACCENTS.length]!
  if (accent.toLowerCase() === color.toLowerCase()) accent = ACCENTS[((h >>> 9) + 1) % ACCENTS.length]!
  return { kind, color, accent, pace: 3 + ((h >>> 15) % 5) }
}

/** Flying Circus wording for a subagent's bubble once it is done or has failed. */
export function circusSpeech(bubble: SpeechBubble, status: string, error?: string): SpeechBubble {
  if (status === 'completed') return { glyph: '✓', text: 'Splendid!' }
  if (status === 'failed') {
    const why = (error || '').replace(/\s+/g, ' ').trim()
    const text = why ? `'Tis but a scratch: ${why}` : "'Tis but a scratch."
    return { glyph: '✕', text: text.length > 90 ? `${text.slice(0, 89)}…` : text }
  }
  if (bubble.text === 'Getting started…') return { glyph: '…', text: 'Right. Where was I…' }
  return bubble
}

/** What the main agent says on the Flying Circus stage. */
export function circusLeadSpeech(working: boolean, running: number, said?: SpeechBubble): SpeechBubble {
  if (working) return !said || said.text === 'Getting started…' ? { glyph: '✦', text: 'Pondering…' } : said
  if (running) return { glyph: '◔', text: `Awaiting ${running} subagent${running === 1 ? '' : 's'}. Tea, anyone?` }
  return { glyph: '☾', text: 'Your move, I believe.' }
}
