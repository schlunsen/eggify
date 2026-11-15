/**
 * Inline markdown markers out of text that is drawn as plain text (speech
 * bubbles, the name plate): `**bold**` → bold, `[a](url)` → a, `# Heading` →
 * Heading. Nothing is rendered as HTML; lone `*`/`_` (2 * 3, snake_case) stay.
 */
export function stripInlineMarkdown(text: string): string {
  // Bubbles show a line or two; a cap keeps one huge marker-heavy line from stalling the page.
  return String(text ?? '').slice(0, 2000)
    .replace(/^[ \t]*```[\w-]*[ \t]*$/gm, '')
    .replace(/^[ \t]{0,3}(?:#{1,6}[ \t]+|>[ \t]?|[-*+][ \t]+|\d{1,3}[.)][ \t]+)/gm, '')
    .replace(/!?\[([^\]\n]*)\]\([^)\s]*(?:\s+"[^"]*")?\)/g, '$1')
    .replace(/`+([^`\n]+)`+/g, '$1')
    .replace(/\*\*(?=\S)([^\n]*?\S)\*\*/g, '$1')
    .replace(/(?<![\w_])__(?=[^\s_])([^\n]*?[^\s_])__(?![\w_])/g, '$1')
    .replace(/(?<![\w*])\*(?=[^\s*])([^*\n]*?[^\s*])\*(?![\w*])/g, '$1')
    .replace(/(?<![\w_])_(?=[^\s_])([^_\n]*?[^\s_])_(?![\w_])/g, '$1')
    .replace(/~~(?=\S)([^\n]*?\S)~~/g, '$1')
    // A marker whose partner has not streamed in yet.
    .replace(/\*\*/g, '')
}
