/**
 * Message and time formatting utilities for agent conversations
 */
import { sanitizeHtml, sessionImageUrl } from '~/utils/safeMarkdown'

/**
 * Escape HTML entities to prevent XSS
 * @param text - Raw text to escape
 * @returns HTML-safe string
 */
export const escapeHtml = (text: string): string => {
  return text
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
    .replace(/'/g, '&#39;')
}

/**
 * Format a timestamp to a readable time string
 * @param timestamp - Date timestamp
 * @returns Formatted time string (e.g., "2:30 PM")
 */
export const formatTime = (timestamp: string | number | Date): string => {
  const date = new Date(timestamp)
  return date.toLocaleTimeString('en-US', {
    hour: 'numeric',
    minute: '2-digit',
    hour12: true
  })
}

/**
 * Format a timestamp as relative time
 * @param timestamp - Date timestamp
 * @returns Relative time string (e.g., "5m ago", "2h ago")
 */
export const formatRelativeTime = (timestamp: string | number | Date): string => {
  const date = new Date(timestamp)
  const now = new Date()
  const diffMs = now.getTime() - date.getTime()
  const diffMins = Math.floor(diffMs / 60000)
  const diffHours = Math.floor(diffMs / 3600000)
  const diffDays = Math.floor(diffMs / 86400000)

  if (diffMins < 1) return 'just now'
  if (diffMins < 60) return `${diffMins}m ago`
  if (diffHours < 24) return `${diffHours}h ago`
  if (diffDays < 7) return `${diffDays}d ago`
  return date.toLocaleDateString()
}

/**
 * Format message content for display with markdown support
 * @param content - Raw message content
 * @returns HTML-formatted message string
 */
export interface FormatMessageOptions {
  /** The session the message belongs to: ![alt](path) shows that file. */
  sessionId?: string | null
}

const unescapeHtml = (text: string): string => text
  .replace(/&#39;/g, "'")
  .replace(/&quot;/g, '"')
  .replace(/&gt;/g, '>')
  .replace(/&lt;/g, '<')
  .replace(/&amp;/g, '&')

/**
 * ![alt](src) in an (already escaped) reply. A path the agent wrote becomes an
 * image from the session's image endpoint, which serves only image files in the
 * session's folders; a remote image is never loaded on its own (it becomes a
 * link, as in rendered markdown). Without a session the alt text stays.
 */
function imageHtml(alt: string, src: string, options: FormatMessageOptions): string {
  const rawSrc = unescapeHtml(src).replace(/^<|>$/g, '')
  const label = alt.trim() || 'image'
  if (/^https?:\/\//i.test(rawSrc)) {
    return `<a href="${escapeHtml(rawSrc)}" target="_blank" rel="noopener noreferrer" class="md-remote-image">🖼 ${label}</a>`
  }
  let path = rawSrc
  if (/^file:\/\//i.test(path)) {
    try {
      path = decodeURIComponent(path.slice('file://'.length))
    } catch {
      path = path.slice('file://'.length)
    }
  }
  if (!options.sessionId || !path || /^[a-z][a-z0-9+.-]*:/i.test(path)) {
    return `<span class="md-image-unavailable" title="${escapeHtml(rawSrc)}">🖼 ${label}</span>`
  }
  const url = sessionImageUrl(options.sessionId, path)
  return `<a href="${escapeHtml(url)}" target="_blank" rel="noopener noreferrer" class="chat-image-link"><img class="chat-image" src="${escapeHtml(url)}" alt="${label}" title="${escapeHtml(path)}"></a>`
}

export const formatMessage = (content: any, options: FormatMessageOptions = {}): string => {
  // Handle array of content blocks (structured format)
  if (Array.isArray(content)) {
    // Check if this is a tool_result array - these should not be displayed
    const hasToolResult = content.some((block: any) => block && block.type === 'tool_result')
    if (hasToolResult) {
      return '<em class="system-message">Tool execution complete</em>'
    }

    // Extract text blocks
    const textBlocks = content
      .filter((block: any) => block && block.type === 'text' && block.text)
      .map((block: any) => block.text)

    if (textBlocks.length > 0) {
      content = textBlocks.join('\n\n')
    } else {
      return '<em class="system-message">Processing...</em>'
    }
  }

  // If content is an object, extract text from it first
  if (typeof content === 'object' && content !== null) {
    // Check if this is a single tool_result block - these should not be displayed
    if (content.type === 'tool_result') {
      return '<em class="system-message">Tool execution complete</em>'
    }

    // Try to extract meaningful text from the object
    if (content.text) {
      content = Array.isArray(content.text) ? content.text.join('\n') : String(content.text)
    } else if (content.content) {
      content = String(content.content)
    } else {
      // For other objects, create a concise representation
      const objType = escapeHtml(String(content.type || 'unknown'))
      const keys = Object.keys(content).filter(k => k !== 'type')
      if (keys.length === 0) {
        return `<em class="system-message">${objType}</em>`
      }
      // Show key properties in a readable format
      const props = keys.slice(0, 3).map(k => `${escapeHtml(k)}: ${escapeHtml(String(content[k]).substring(0, 30))}`).join(', ')
      return `<em class="system-message">${objType} - ${props}</em>`
    }
  }

  // Ensure content is a string at this point
  content = String(content)

  // Check if content is stringified JSON with tool_result
  if (content.trim().startsWith('[') || content.trim().startsWith('{')) {
    try {
      const parsed = JSON.parse(content)
      if (Array.isArray(parsed) && parsed.some((block: any) => block.type === 'tool_result')) {
        return '<em class="system-message">Tool execution complete</em>'
      }
      if (parsed.type === 'tool_result') {
        return '<em class="system-message">Tool execution complete</em>'
      }
    } catch (e) {
      // Not valid JSON, continue with normal processing
    }
  }

  // Skip system messages and JSON-like content
  if (content.includes('SystemMessage(') || (content.startsWith('{') && content.includes('"type"'))) {
    return '<em class="system-message">Processing...</em>'
  }

  // Clean up the content
  let cleanContent = content

  // If it's a string representation of an object, try to extract meaningful text
  if (cleanContent.includes('assistant:')) {
    const match = cleanContent.match(/assistant:\s*(.+?)(?:\n|$)/i)
    if (match) {
      cleanContent = match[1]
    }
  }

  // Escape HTML entities before applying markdown formatting to prevent XSS
  cleanContent = escapeHtml(cleanContent)

  // Convert markdown to HTML (basic)
  let codePrefix = 'EGGIFY_CODE_'
  while (cleanContent.includes(codePrefix)) codePrefix += '_'
  const codeFragments: string[] = []
  const preserveCode = (html: string) => {
    const index = codeFragments.push(html) - 1
    return `${codePrefix}${index}_END`
  }
  let formatted = cleanContent
    .replace(/```(.*?)\n([\s\S]*?)```/g, (_match: string, language: string, code: string) => preserveCode(`<pre><code class="language-${language}">${code}</code></pre>`))
    .replace(/`([^`]+)`/g, (_match: string, code: string) => preserveCode(`<code>${code}</code>`))
    .replace(/!\[([^\]\n]*)\]\(\s*([^)\s]+)(?:\s+&quot;[^\n]*?&quot;)?\s*\)/g, (_match: string, alt: string, src: string) => preserveCode(imageHtml(alt, src, options)))
    .replace(/\*\*(.*?)\*\*/g, '<strong>$1</strong>')
    .replace(/\*(.*?)\*/g, '<em>$1</em>')

  // Convert URLs to clickable links (must be done before newline conversion).
  // Only plain text between code placeholders is linked: a URL that ran into a
  // placeholder would put the restored <code class="..."> markup inside href
  // and break out of the attribute.
  const placeholderRe = new RegExp(`(${codePrefix}\\d+_END)`, 'g')
  // The text is already escaped, so a URL also ends at an escaped quote or
  // angle bracket (&quot; &#39; &lt; &gt;).
  const urlRegex = /(?<!<[^>]*)(https?:\/\/(?:(?!&quot;|&#39;|&lt;|&gt;)[^\s<"'])*(?:(?!&quot;|&#39;|&lt;|&gt;)[^\s<"'.,;:!?)]))/gi
  formatted = formatted
    .split(placeholderRe)
    .map((segment: string, i: number) => i % 2 === 1
      ? segment
      : segment.replace(urlRegex, '<a href="$1" target="_blank" rel="noopener noreferrer" class="message-link">$1</a>'))
    .join('')

  // Convert newlines to <br> last
  const html = formatted.replace(/\n/g, '<br>').replace(new RegExp(`${codePrefix}(\\d+)_END`, 'g'), (_match: string, index: string) => codeFragments[Number(index)])
  // Defense in depth: the result is rendered with v-html, so run it through the
  // same DOMPurify policy as rendered markdown.
  return sanitizeHtml(html)
}

/**
 * Truncate a path or text to a maximum length
 * @param text - Text to truncate
 * @param maxLength - Maximum length
 * @returns Truncated text with ellipsis if needed
 */
export const truncatePath = (text: string | undefined | null, maxLength: number): string => {
  if (!text) return ''
  if (text.length <= maxLength) return text

  // For paths, try to keep the filename
  if (text.includes('/')) {
    const parts = text.split('/')
    const filename = parts[parts.length - 1]
    if (filename.length < maxLength - 3) {
      return `.../${filename}`
    }
  }

  return text.substring(0, maxLength - 3) + '...'
}
