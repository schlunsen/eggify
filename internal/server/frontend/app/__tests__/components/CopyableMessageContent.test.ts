// DOMPurify (used by CopyableMessageContent) needs a spec-compliant DOM; happy-dom
// mangles sanitized output, so this file runs under jsdom.
// @vitest-environment jsdom
import { flushPromises, mount } from '@vue/test-utils'
import CopyableMessageContent from '../../components/agents/CopyableMessageContent.vue'
import MessageContent from '../../components/agents/MessageBubble/MessageContent.vue'
import MessageDetailModal from '../../components/agents/MessageDetailModal.vue'
import { formatMessage } from '../../utils/agents/messageFormatters'

// jsdom has no Clipboard API; the tests spy on writeText.
if (!navigator.clipboard) {
  Object.defineProperty(navigator, 'clipboard', { value: { writeText: async () => {} }, configurable: true })
}

const title = 'What is an AI agent, really? <Draft> & "Final"'
const description = 'First line\n\n**Keep these markers** and `backticks`.\nhttps://example.com/watch?q=one&part=two\n'
const content = `Title:\n\`\`\`text\n${title}\n\`\`\`\nDescription:\n\`\`\`\n${description}\`\`\`\nFile: \`video/build/ch02.mp4\``

afterEach(() => vi.restoreAllMocks())

it.each(['chat', 'modal'])('copies individual title, description, and path values in %s', async (view) => {
  const write = vi.spyOn(navigator.clipboard, 'writeText').mockResolvedValue()
  const wrapper = view === 'chat'
    ? mount(MessageContent, { props: { content, messageRole: 'assistant', formatMessage } })
    : mount(MessageDetailModal, {
      props: { show: true, message: { id: 'message', role: 'assistant', content, timestamp: new Date() }, formatMessage, formatTime: () => '12:00' },
      global: { stubs: { Teleport: true } }
    })
  try {
    const buttons = wrapper.findAll('.snippet-copy-button')
    expect(buttons).toHaveLength(3)
    for (const [index, expected] of [title + '\n', description, 'video/build/ch02.mp4'].entries()) {
      await buttons[index].trigger('click')
      await flushPromises()
      expect(write).toHaveBeenLastCalledWith(expected)
      expect(buttons[index].text()).toBe('Copied')
    }
    expect(wrapper.find('pre strong').exists()).toBe(false)
    expect(wrapper.find('pre a').exists()).toBe(false)
    expect(wrapper.find('Draft').exists()).toBe(false)
  } finally {
    wrapper.unmount()
  }
})

it('keeps copy clicks and keyboard activation from opening the message modal', async () => {
  vi.spyOn(navigator.clipboard, 'writeText').mockResolvedValue()
  const host = document.createElement('div')
  document.body.append(host)
  const openModal = vi.fn()
  host.addEventListener('click', openModal)
  host.addEventListener('keydown', openModal)
  const wrapper = mount(CopyableMessageContent, { attachTo: host, props: { html: formatMessage('`a/path`') } })
  try {
    const button = wrapper.get('button')
    await button.trigger('keydown', { key: 'Enter' })
    await button.trigger('keydown', { key: ' ' })
    await button.trigger('click')
    await flushPromises()
    expect(openModal).not.toHaveBeenCalled()
  } finally {
    wrapper.unmount()
    host.remove()
  }
})

it('reports clipboard failures and supports retry', async () => {
  const write = vi.spyOn(navigator.clipboard, 'writeText').mockRejectedValueOnce(new Error('denied')).mockResolvedValueOnce()
  const wrapper = mount(CopyableMessageContent, { props: { html: '<pre><code>first<br>second</code></pre>' } })
  try {
    await wrapper.get('button').trigger('click')
    await flushPromises()
    expect(wrapper.get('button').text()).toBe('Copy failed')
    await wrapper.get('button').trigger('click')
    await flushPromises()
    expect(write).toHaveBeenLastCalledWith('first\nsecond')
    expect(wrapper.get('button').text()).toBe('Copied')
  } finally {
    wrapper.unmount()
  }
})

it('copies updated streaming content without duplicating buttons or interpreting HTML', async () => {
  const write = vi.spyOn(navigator.clipboard, 'writeText').mockResolvedValue()
  const wrapper = mount(CopyableMessageContent, { props: { html: formatMessage('`first`') } })
  try {
    await wrapper.setProps({ html: formatMessage('```\n<script>alert("x")</script>\nEGGIFY_CODE_0_END\n```') })
    expect(wrapper.findAll('button')).toHaveLength(1)
    expect(wrapper.find('script').exists()).toBe(false)
    await wrapper.get('button').trigger('click')
    await flushPromises()
    expect(write).toHaveBeenCalledWith('<script>alert("x")</script>\nEGGIFY_CODE_0_END\n')
  } finally {
    wrapper.unmount()
  }
})
