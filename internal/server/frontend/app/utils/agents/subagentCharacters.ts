/**
 * Characters for the zen subagents scene. Each subagent gets a small creature
 * derived from its id, so it looks the same every time it is shown.
 */
import { parseToolLine } from './subagentOutput'

export type CharacterBody = 'bean' | 'boombox' | 'cassette' | 'acid' | 'alien' | 'cat'
export type CharacterEyes = 'shades' | 'aviators' | 'visor' | 'wide'
export type CharacterAccessory = 'cap' | 'beanie' | 'headphones' | 'mohawk' | 'chain' | 'none'

export interface SubagentCharacter {
  body: CharacterBody
  eyes: CharacterEyes
  accessory: CharacterAccessory
  color: string
  /** A second colour for its gear (cap, beanie, hair, headphones), never the body colour. */
  accent: string
  /** Walking pace, as a share of the scene per second. */
  pace: number
}

// The zen palette (see ZenMode.vue).
export const ZEN_COLORS = ['#8B5CF6', '#22d3ee', '#42D096', '#F59E43', '#EC609A', '#6494F7', '#C87DEC', '#4EC3C4']
// Gear in neon, so it pops against any body.
const ACCENTS = ['#ff4fd8', '#22d3ee', '#ffd36e', '#42D096', '#ff7a45', '#a78bfa']
const BODIES: CharacterBody[] = ['bean', 'boombox', 'cassette', 'acid', 'alien', 'cat']
const EYES: CharacterEyes[] = ['shades', 'aviators', 'visor', 'wide']
const ACCESSORIES: CharacterAccessory[] = ['cap', 'beanie', 'headphones', 'mohawk', 'chain', 'none']

/** FNV-1a: a small, stable string hash. */
export function hashString(value: string): number {
  let hash = 0x811c9dc5
  for (let i = 0; i < value.length; i++) {
    hash ^= value.charCodeAt(i)
    hash = Math.imul(hash, 0x01000193)
  }
  return hash >>> 0
}

export function characterFor(agentId: string): SubagentCharacter {
  const h = hashString(agentId)
  const body = BODIES[h % BODIES.length]!
  const color = ZEN_COLORS[(h >>> 3) % ZEN_COLORS.length]!
  let accent = ACCENTS[(h >>> 19) % ACCENTS.length]!
  if (accent.toLowerCase() === color.toLowerCase()) accent = ACCENTS[((h >>> 19) + 1) % ACCENTS.length]!
  let accessory = ACCESSORIES[(h >>> 11) % ACCESSORIES.length]!
  // An alien's antennae leave no room for headwear.
  if (body === 'alien' && (accessory === 'cap' || accessory === 'beanie' || accessory === 'mohawk')) accessory = 'chain'
  return {
    body,
    color,
    accent,
    // A cassette's eyes are its reels; the rest pick eyewear.
    eyes: EYES[(h >>> 7) % EYES.length]!,
    accessory,
    pace: 3 + ((h >>> 15) % 5) // 3-7% of the scene per second
  }
}

/** A glyph for what a tool does, shown in front of the speech bubble. */
export function toolGlyph(tool: string): string {
  // MCP tools are named mcp__<server>__<tool>; the tool part says what it does.
  const name = tool.split('__').pop() || tool
  if (/^(Bash|BashOutput|KillShell|command)/i.test(name)) return '>_'
  if (/^(Edit|Write|MultiEdit|NotebookEdit|FileChange)/i.test(name)) return '✎'
  if (/^(Read|NotebookRead)/i.test(name)) return '▤'
  if (/^(Grep|Glob|LS|Search)/i.test(name)) return '⌕'
  if (/^Web/i.test(name)) return '◍'
  if (/^(Agent|Task)/i.test(name)) return '✦'
  if (/^TodoWrite/i.test(name)) return '☑'
  return '⚙'
}

export interface SpeechBubble {
  glyph: string
  text: string
}

/** What a subagent says: its latest tool call or line of text. */
export function speechFor(lines: string[], status: string, error?: string, max = 90): SpeechBubble {
  const clip = (text: string) => {
    const flat = text.replace(/\s+/g, ' ').trim()
    return flat.length > max ? `${flat.slice(0, max - 1)}…` : flat
  }
  if (status === 'failed') return { glyph: '✕', text: clip(error ? `Ouch: ${error}` : 'Ouch, that failed') }
  if (status === 'completed') return { glyph: '✓', text: 'Done!' }
  for (let i = lines.length - 1; i >= 0; i--) {
    const line = lines[i] ?? ''
    const tool = parseToolLine(line)
    if (tool) return { glyph: toolGlyph(tool.name), text: clip(tool.detail ? `${tool.name} ${tool.detail}` : tool.name) }
    if (!line.trim() || line === 'RESULT_START' || line === 'RESULT_END') continue
    // Tool output is what the tool said, not the subagent: skip it.
    if (lines.lastIndexOf('RESULT_START', i) > lines.lastIndexOf('RESULT_END', i)) continue
    return { glyph: '💬', text: clip(line) }
  }
  return { glyph: '…', text: 'Getting started…' }
}

interface LeadMessageLike {
  role?: string
  content?: unknown
  toolUses?: Array<{ name?: string; toolName?: string; input?: Record<string, unknown> } | null> | null
  isToolResult?: boolean
}

/** What a tool call acts on, in the shape subagent output lines use. */
function toolDetail(input: Record<string, unknown> | undefined): string {
  if (!input) return ''
  for (const key of ['command', 'file_path', 'path', 'pattern', 'url', 'query', 'description', 'prompt']) {
    const value = input[key]
    if (typeof value === 'string' && value.trim()) return value.replace(/\s+/g, ' ').trim()
  }
  return ''
}

/**
 * The main agent's recent activity as subagent-style output lines
 * ("TOOL:<name>: <detail>" or its own text), oldest first, so speechFor can
 * read it the same way it reads a subagent's.
 */
export function leadLinesFrom(messages: LeadMessageLike[] | undefined, limit = 12): string[] {
  const lines: string[] = []
  if (!messages?.length) return lines
  for (let i = messages.length - 1; i >= 0 && lines.length < limit; i--) {
    const message = messages[i]
    if (message?.role !== 'assistant') continue
    const found: string[] = []
    if (!message.isToolResult) {
      if (typeof message.content === 'string' && message.content.trim()) found.push(message.content)
      else if (Array.isArray(message.content)) {
        for (const block of message.content as any[]) if (block?.type === 'text' && block.text?.trim()) found.push(block.text)
      }
    }
    for (const tool of message.toolUses || []) {
      const name = tool?.name || tool?.toolName
      if (!name) continue
      const detail = toolDetail(tool?.input)
      found.push(detail ? `TOOL:${name}: ${detail}` : `TOOL:${name}`)
    }
    lines.unshift(...found)
  }
  return lines.slice(-limit)
}
