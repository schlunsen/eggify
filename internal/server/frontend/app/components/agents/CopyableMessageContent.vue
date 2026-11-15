<template>
  <div
    class="copyable-message-content"
    @click="handleCopy"
    @keydown="handleCopyKeydown"
    v-html="rendered.html"
  ></div>
</template>

<script setup lang="ts">
import { copyText } from '~/utils/clipboard'
import { computed, onBeforeUnmount, watch } from 'vue'
import { sanitizeHtml } from '~/utils/safeMarkdown'

const props = defineProps<{ html: string }>()
const timers = new Map<HTMLButtonElement, ReturnType<typeof setTimeout>>()

const rendered = computed(() => {
  if (typeof document === 'undefined') return { html: props.html, snippets: [] as string[] }
  const template = document.createElement('template')
  // The html comes from formatMessage(), which escapes agent text before adding
  // markup; sanitize again so a formatter bug can't turn into script execution.
  template.innerHTML = sanitizeHtml(props.html)
  const snippets: string[] = []
  for (const element of template.content.querySelectorAll('pre, code')) {
    if (element.tagName === 'CODE' && element.closest('pre')) continue
    const block = element.tagName === 'PRE'
    const textNode = element.cloneNode(true) as HTMLElement
    for (const lineBreak of textNode.querySelectorAll('br')) lineBreak.replaceWith('\n')
    const text = textNode.textContent || ''
    if (!text) continue
    const wrapper = document.createElement(block ? 'div' : 'span')
    wrapper.className = block ? 'copyable-block' : 'copyable-inline'
    const button = document.createElement('button')
    button.type = 'button'
    button.className = 'snippet-copy-button'
    button.dataset.copySnippet = String(snippets.length)
    button.setAttribute('aria-label', block ? 'Copy code block' : 'Copy inline code')
    button.setAttribute('aria-live', 'polite')
    button.textContent = 'Copy'
    snippets.push(text)
    element.replaceWith(wrapper)
    if (block) {
      const toolbar = document.createElement('div')
      toolbar.className = 'snippet-toolbar'
      toolbar.append(button)
      wrapper.append(toolbar, element)
    } else {
      wrapper.append(element, button)
    }
  }
  return { html: template.innerHTML, snippets }
})

function copyButton(event: Event): HTMLButtonElement | null {
  const target = event.target
  if (!(target instanceof Element)) return null
  const button = target.closest<HTMLButtonElement>('button[data-copy-snippet]')
  return button && (event.currentTarget as HTMLElement).contains(button) ? button : null
}

function handleCopyKeydown(event: KeyboardEvent) {
  if (copyButton(event) && (event.key === 'Enter' || event.key === ' ')) event.stopPropagation()
}

async function handleCopy(event: MouseEvent) {
  const button = copyButton(event)
  if (!button) return
  event.preventDefault()
  event.stopPropagation()
  const text = rendered.value.snippets[Number(button.dataset.copySnippet)]
  if (text === undefined || button.disabled) return
  const previousTimer = timers.get(button)
  if (previousTimer) clearTimeout(previousTimer)
  const label = button.closest('.copyable-block') ? 'Copy code block' : 'Copy inline code'
  button.disabled = true
  try {
    await copyText(text)
    button.textContent = 'Copied'
    button.setAttribute('aria-label', 'Copied to clipboard')
  } catch {
    button.textContent = 'Copy failed'
    button.setAttribute('aria-label', 'Copy failed. Try again')
  } finally {
    button.disabled = false
  }
  if (!button.isConnected) return
  timers.set(button, setTimeout(() => {
    button.textContent = 'Copy'
    button.setAttribute('aria-label', label)
    timers.delete(button)
  }, 2000))
}

function clearTimers() {
  for (const timer of timers.values()) clearTimeout(timer)
  timers.clear()
}
watch(() => props.html, clearTimers)
onBeforeUnmount(clearTimers)
</script>

<style scoped>
.copyable-message-content :deep(.copyable-block) {
  min-width: 0;
  margin: 12px 0;
}
.copyable-message-content :deep(.snippet-toolbar) {
  display: flex;
  justify-content: flex-end;
  padding: 6px 10px;
  background: var(--bg-secondary);
  border: 1px solid var(--border-color);
  border-bottom: 0;
  border-radius: 8px 8px 0 0;
}
.copyable-message-content :deep(.copyable-block > pre) {
  margin: 0;
  border-radius: 0 0 8px 8px;
}
.copyable-message-content :deep(.snippet-copy-button) {
  display: inline-flex;
  align-items: center;
  padding: 4px 8px;
  border: 1px solid var(--border-color);
  border-radius: 5px;
  background: var(--bg-primary);
  color: var(--text-primary);
  font-family: 'Inter', -apple-system, BlinkMacSystemFont, 'Segoe UI', sans-serif;
  font-size: 0.75rem;
  line-height: 1.4;
  vertical-align: middle;
  cursor: pointer;
}
.copyable-message-content :deep(.copyable-inline .snippet-copy-button) {
  margin-left: 4px;
  padding: 2px 5px;
}
.copyable-message-content :deep(.snippet-copy-button:hover) {
  border-color: var(--accent-purple);
}
.copyable-message-content :deep(.snippet-copy-button:focus-visible) {
  outline: 2px solid var(--accent-purple);
  outline-offset: 2px;
}
.copyable-message-content :deep(.snippet-copy-button:disabled) {
  opacity: 0.6;
}
</style>
