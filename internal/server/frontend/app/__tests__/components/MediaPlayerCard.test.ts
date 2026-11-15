import { mount, flushPromises } from '@vue/test-utils'
import MediaPlayerCard from '../../components/agents/MediaPlayerCard.vue'
import SessionMediaList from '../../components/agents/SessionMediaList.vue'
import ToolUseIndicators from '../../components/agents/MessageBubble/ToolUseIndicators.vue'
import { resetSessionMedia, useSessionMedia, MEDIA_CONVERT_POLL_MS, MEDIA_REFRESH_DEBOUNCE_MS } from '../../composables/agents/useSessionMedia'
import { extractDisplayToolUses } from '../../types/message'
import { mediaByToolUse, type MediaItem } from '../../utils/agents/sessionMedia'

const auth = vi.hoisted(() => ({ fetchWithAuth: vi.fn() }))
vi.mock('../../composables/useAuthenticatedFetch', () => ({ useAuthenticatedFetch: () => auth }))

const session = 'media-session'
const base = `/api/agent/sessions/${session}/media`

const video = (over: Partial<MediaItem> = {}): MediaItem => ({
  id: 'v1', name: 'render.webm', title: 'Intro render', path: 'renders/render.webm', kind: 'video',
  mime_type: 'video/webm', size: 2 * 1024 * 1024, modified: '2026-10-01T10:00:00Z', shown: true,
  tool_use_id: 'toolu_1', tool_use_ids: ['toolu_1'], ios_plays: false, url: `${base}/v1/file`,
  poster_url: `${base}/v1/poster`, ...over,
})
const audio = (over: Partial<MediaItem> = {}): MediaItem => ({
  id: 'a1', name: 'theme.mp3', path: 'theme.mp3', kind: 'audio', mime_type: 'audio/mpeg', size: 5000,
  modified: '2026-10-01T09:00:00Z', shown: false, tool_use_id: 'toolu_2', ios_plays: true, url: `${base}/a1/file`, ...over,
})

const json = (body: unknown, status = 200) => ({ ok: status < 400, status, json: async () => body, blob: async () => new Blob(['x']) }) as unknown as Response

let listBody: { items: MediaItem[]; ffmpeg: boolean }
let convertCalls = 0

beforeEach(() => {
  resetSessionMedia()
  convertCalls = 0
  listBody = { items: [video(), audio()], ffmpeg: true }
  auth.fetchWithAuth.mockReset()
  auth.fetchWithAuth.mockImplementation(async (url: string, opts?: RequestInit) => {
    if (url.endsWith('/convert') && opts?.method === 'POST') {
      convertCalls++
      return json({ state: 'running' })
    }
    if (url === base) return json(structuredClone(listBody))
    return json({}, 404)
  })
})

afterEach(() => {
  vi.useRealTimers()
})

describe('MediaPlayerCard', () => {
  it('plays a video with its poster, and says iPhone cannot play WebM', () => {
    const wrapper = mount(MediaPlayerCard, { props: { item: video(), sessionId: session, ffmpeg: true } })
    const el = wrapper.get('video')
    expect(el.attributes('src')).toBe(`${base}/v1/file`)
    expect(el.attributes('poster')).toBe(`${base}/v1/poster`)
    expect(el.attributes('preload')).toBe('metadata')
    expect(wrapper.find('audio').exists()).toBe(false)
    expect(wrapper.get('.media-card-title').text()).toBe('Intro render')
    expect(wrapper.get('.media-card-meta').text()).toContain('renders/render.webm')
    expect(wrapper.get('.media-card-meta').text()).toContain('2.0 MB')
    expect(wrapper.get('.media-card-ios').text()).toContain("iPhone can't play WEBM files.")
    expect(wrapper.get('.media-card-ios button').text()).toBe('Convert for iPhone')
  })

  it('plays audio, without an iPhone note when iPhone can play it', () => {
    const wrapper = mount(MediaPlayerCard, { props: { item: audio(), sessionId: session, ffmpeg: true } })
    expect(wrapper.get('audio').attributes('src')).toBe(`${base}/a1/file`)
    expect(wrapper.find('video').exists()).toBe(false)
    expect(wrapper.get('.media-card-title').text()).toBe('theme.mp3')
    expect(wrapper.find('.media-card-ios').exists()).toBe(false)
  })

  it('offers no conversion without ffmpeg, and shows each conversion state', async () => {
    const wrapper = mount(MediaPlayerCard, { props: { item: video(), sessionId: session, ffmpeg: false } })
    expect(wrapper.get('.media-card-ios').text()).toContain("iPhone can't play")
    expect(wrapper.find('.media-card-ios button').exists()).toBe(false)

    await wrapper.setProps({ ffmpeg: true, item: video({ compatible: 'running' }) })
    expect(wrapper.get('.media-card-ios').text()).toContain('Converting…')
    expect(wrapper.find('.media-card-ios button').exists()).toBe(false)

    await wrapper.setProps({ item: video({ compatible: 'ready', compatible_url: `${base}/v1/compatible` }) })
    expect(wrapper.get('.media-card-ios').text()).toContain('iPhone copy ready')

    await wrapper.setProps({ item: video({ compatible: 'failed', compatible_error: 'codec missing' }) })
    expect(wrapper.get('.media-card-ios').text()).toContain("Couldn't convert: codec missing")
    expect(wrapper.get('.media-card-ios button').text()).toBe('Try again')
  })

  it('falls back to the iPhone copy, then to an authenticated download, then to a message', async () => {
    const item = video({ compatible: 'ready', compatible_url: `${base}/v1/compatible` })
    const original = { create: URL.createObjectURL, revoke: URL.revokeObjectURL }
    URL.createObjectURL = vi.fn(() => 'blob:media')
    URL.revokeObjectURL = vi.fn()
    auth.fetchWithAuth.mockImplementation(async () => json({}))
    const wrapper = mount(MediaPlayerCard, { props: { item, sessionId: session, ffmpeg: true } })

    await wrapper.get('video').trigger('error')
    expect(wrapper.get('video').attributes('src')).toBe(`${base}/v1/compatible`)

    await wrapper.get('video').trigger('error')
    await flushPromises()
    expect(auth.fetchWithAuth).toHaveBeenCalledWith(`${base}/v1/file`)
    expect(wrapper.get('video').attributes('src')).toBe('blob:media')

    await wrapper.get('video').trigger('error')
    expect(wrapper.find('video').exists()).toBe(false)
    expect(wrapper.get('.media-card-error').text()).toContain("This browser can't play this video (WEBM)")
    expect(wrapper.get('.media-card-error button').text()).toBe('Download instead')
    wrapper.unmount()
    URL.createObjectURL = original.create
    URL.revokeObjectURL = original.revoke
  })

  it('reloads the list when the file changed since it was listed', async () => {
    const media = useSessionMedia()
    await media.load(session)
    auth.fetchWithAuth.mockClear()
    auth.fetchWithAuth.mockImplementation(async (url: string) => url === base ? json(structuredClone(listBody)) : json({}, 409))
    const wrapper = mount(MediaPlayerCard, { props: { item: audio(), sessionId: session } })
    await wrapper.get('audio').trigger('error')
    await flushPromises()
    expect(wrapper.get('.media-card-error').text()).toContain('The file changed')
    expect(auth.fetchWithAuth.mock.calls.map(c => c[0])).toContain(base)
  })
})

describe('Media tab', () => {
  it('lists every file newest first, and converts one until its copy is ready', async () => {
    vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] })
    const wrapper = mount(SessionMediaList, { props: { sessionId: session } })
    await flushPromises()
    expect(wrapper.get('.session-media-summary').text()).toContain('2 files')
    const cards = wrapper.findAll('.media-card')
    expect(cards.map(c => c.get('.media-card-title').text())).toEqual(['Intro render', 'theme.mp3'])

    await cards[0]!.get('.media-card-ios button').trigger('click')
    await flushPromises()
    expect(convertCalls).toBe(1)
    expect(wrapper.get('.media-card-ios').text()).toContain('Converting…')

    // The server finishes; the next poll sees it
    listBody.items[0] = video({ compatible: 'ready', compatible_url: `${base}/v1/compatible` })
    vi.advanceTimersByTime(MEDIA_CONVERT_POLL_MS)
    await flushPromises()
    expect(wrapper.get('.media-card-ios').text()).toContain('iPhone copy ready')
    wrapper.unmount()
  })

  it('explains what shows up here when there is nothing yet', async () => {
    listBody = { items: [], ffmpeg: false }
    const wrapper = mount(SessionMediaList, { props: { sessionId: session } })
    await flushPromises()
    expect(wrapper.text()).toContain('No video or audio yet')
    expect(wrapper.text()).toContain('show_media')
  })
})

describe('useSessionMedia', () => {
  it('reloads once after a burst of refreshes, and only for sessions it knows', async () => {
    vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] })
    const media = useSessionMedia()
    media.refresh('never-opened')
    await media.load(session)
    auth.fetchWithAuth.mockClear()
    media.refresh(session)
    media.refresh(session)
    vi.advanceTimersByTime(MEDIA_REFRESH_DEBOUNCE_MS - 1)
    expect(auth.fetchWithAuth).not.toHaveBeenCalled()
    vi.advanceTimersByTime(1)
    await flushPromises()
    expect(auth.fetchWithAuth.mock.calls.map(c => c[0])).toEqual([base])
  })

  it('keeps the same list when a reload finds nothing new', async () => {
    const media = useSessionMedia()
    await media.load(session)
    const first = media.stateFor(session)!.items
    await media.load(session)
    expect(media.stateFor(session)!.items).toBe(first)
  })
})

describe('inline in the chat', () => {
  it('shows a file only under the tool call that named it', () => {
    const toolUses = extractDisplayToolUses({
      id: 'm1', role: 'assistant', content: '', timestamp: new Date(),
      toolUses: [
        { id: 'toolu_0', name: 'Bash', input: { command: 'ls' } },
        { id: 'toolu_1', name: 'Bash', input: { command: 'ffmpeg -i a.png render.webm' } },
      ],
    } as any)
    const items = [video(), audio()]
    const wrapper = mount(ToolUseIndicators, {
      props: { toolUses, messageTimestamp: new Date(), media: { sessionId: session, ffmpeg: true, byToolUse: mediaByToolUse(items) } },
    })
    const wrappers = wrapper.findAll('.tool-use-wrapper')
    expect(wrappers[0]!.find('.media-card').exists()).toBe(false)
    expect(wrappers[1]!.findAll('.media-card')).toHaveLength(1)
    expect(wrappers[1]!.get('.media-card-title').text()).toBe('Intro render')
  })
})
