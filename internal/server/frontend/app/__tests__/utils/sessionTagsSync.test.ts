import {
  SESSION_TAGS_PENDING_KEY, SESSION_TAGS_STORAGE_KEY, SESSION_TAGS_UPLOADED_KEY,
  configureSessionTagSync, readSessionTags, syncSessionTags, writeSessionTagList,
} from '../../utils/sessionTags'

type Call = { url: string; method: string; body?: any }

/** A fake server: GET returns `server`, PUT stores (normalised to max 8) or 404s for unknown sessions. */
function fakeServer(server: Record<string, string[]>, opts: { known?: string[]; failPut?: boolean } = {}) {
  const calls: Call[] = []
  const fetcher = vi.fn(async (url: string, init?: RequestInit) => {
    const method = init?.method || 'GET'
    const body = init?.body ? JSON.parse(String(init.body)) : undefined
    calls.push({ url, method, body })
    if (method === 'GET') return new Response(JSON.stringify({ tags: server }), { status: 200 })
    if (opts.failPut) return new Response('{}', { status: 500 })
    const id = decodeURIComponent(url.split('/')[4])
    if (opts.known && !opts.known.includes(id)) return new Response('{"error":"session not found"}', { status: 404 })
    const tags = (body.tags as string[]).slice(0, 8)
    if (tags.length) server[id] = tags
    else delete server[id]
    return new Response(JSON.stringify({ tags }), { status: 200 })
  })
  configureSessionTagSync(fetcher)
  return { calls, fetcher, server }
}

const puts = (calls: Call[]) => calls.filter(c => c.method === 'PUT').map(c => [c.url, c.body.tags])
const pending = () => JSON.parse(localStorage.getItem(SESSION_TAGS_PENDING_KEY) || '[]')
const flush = () => new Promise(r => setTimeout(r, 0))

afterEach(() => {
  configureSessionTagSync(null)
  localStorage.clear()
})

describe('session tag sync', () => {
  it('sends every write to the server and clears the pending mark', async () => {
    const { calls, server } = fakeServer({})
    writeSessionTagList('s1', ['wip', ' Auth '])
    expect(readSessionTags().s1).toBe('wip, Auth') // written locally at once
    await flush()
    expect(puts(calls)).toEqual([['/api/agent/sessions/s1/tags', ['wip', 'Auth']]])
    expect(server.s1).toEqual(['wip', 'Auth'])
    expect(pending()).toEqual([])

    writeSessionTagList('s1', [])
    await flush()
    expect(puts(calls).at(-1)).toEqual(['/api/agent/sessions/s1/tags', []])
    expect(server.s1).toBeUndefined()
  })

  it('takes the server\'s normalised list', async () => {
    fakeServer({})
    writeSessionTagList('s1', ['1', '2', '3', '4', '5', '6', '7', '8', '9'])
    await flush()
    await flush()
    expect(readSessionTags().s1).toBe('1, 2, 3, 4, 5, 6, 7, 8')
  })

  it('keeps a failed write pending and sends it on the next sync', async () => {
    const failing = fakeServer({}, { failPut: true })
    writeSessionTagList('s1', ['offline'])
    await flush()
    expect(pending()).toEqual(['s1'])
    expect(failing.server.s1).toBeUndefined()

    localStorage.setItem(SESSION_TAGS_UPLOADED_KEY, '1')
    const { calls, server } = fakeServer({ s1: ['old'] })
    await syncSessionTags()
    expect(readSessionTags().s1).toBe('offline') // the unsent local write wins
    expect(puts(calls)).toEqual([['/api/agent/sessions/s1/tags', ['offline']]])
    expect(server.s1).toEqual(['offline'])
    expect(pending()).toEqual([])
  })

  it('first sync: server wins where it has tags, local-only tags are uploaded once', async () => {
    localStorage.setItem(SESSION_TAGS_STORAGE_KEY, JSON.stringify({ both: 'local', mine: 'desktop, bug', gone: 'x' }))
    const { calls, server } = fakeServer({ both: ['from', 'phone'], phoneOnly: ['ios'] }, { known: ['both', 'mine', 'phoneOnly'] })
    const storage = vi.fn()
    window.addEventListener('storage', storage)

    expect(await syncSessionTags()).toBe(true)
    window.removeEventListener('storage', storage)

    expect(puts(calls).sort()).toEqual([
      ['/api/agent/sessions/gone/tags', ['x']],
      ['/api/agent/sessions/mine/tags', ['desktop', 'bug']],
    ])
    expect(server.mine).toEqual(['desktop', 'bug'])
    expect(readSessionTags()).toEqual({ both: 'from, phone', phoneOnly: 'ios', mine: 'desktop, bug', gone: 'x' })
    expect(storage).toHaveBeenCalled() // views re-read
    expect(pending()).toEqual([]) // the 404 (deleted session) is not retried
    expect(localStorage.getItem(SESSION_TAGS_UPLOADED_KEY)).toBe('1')
  })

  it('later syncs drop local tags the server no longer has (removed elsewhere)', async () => {
    localStorage.setItem(SESSION_TAGS_UPLOADED_KEY, '1')
    localStorage.setItem(SESSION_TAGS_STORAGE_KEY, JSON.stringify({ s1: 'a', s2: 'b' }))
    const { calls } = fakeServer({ s2: ['b', 'c'] })
    await syncSessionTags()
    expect(readSessionTags()).toEqual({ s2: 'b, c' })
    expect(puts(calls)).toEqual([])
  })

  it('leaves localStorage alone when the server cannot be reached', async () => {
    localStorage.setItem(SESSION_TAGS_STORAGE_KEY, JSON.stringify({ s1: 'a' }))
    configureSessionTagSync(vi.fn(async () => new Response('', { status: 503 })))
    expect(await syncSessionTags()).toBe(false)
    expect(readSessionTags()).toEqual({ s1: 'a' })
    expect(localStorage.getItem(SESSION_TAGS_UPLOADED_KEY)).toBeNull()
  })

  it('without a configured fetcher writes stay local', async () => {
    writeSessionTagList('s1', ['a'])
    expect(readSessionTags().s1).toBe('a')
    expect(pending()).toEqual([])
    expect(await syncSessionTags()).toBe(false)
  })
})
