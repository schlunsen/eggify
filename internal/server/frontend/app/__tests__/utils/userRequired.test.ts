import { describe, it, expect, afterEach } from 'vitest'
import {
  USER_REQUIRED_EVENT,
  announceUserRequired,
  checkResponseForUserRequired,
  isUserRequiredBody,
  userRequiredText,
} from '~/utils/userRequired'

function listen() {
  const seen: string[] = []
  const handler = (e: Event) => seen.push((e as CustomEvent).detail.message)
  window.addEventListener(USER_REQUIRED_EVENT, handler)
  return { seen, stop: () => window.removeEventListener(USER_REQUIRED_EVENT, handler) }
}

describe('userRequired', () => {
  let stop: (() => void) | null = null
  afterEach(() => { stop?.(); stop = null })

  it('recognises the server refusal shape', () => {
    expect(isUserRequiredBody({ code: 'user_required', error: 'x' })).toBe(true)
    expect(isUserRequiredBody({ type: 'error', code: 'user_required', message: 'x' })).toBe(true)
    expect(isUserRequiredBody({ error: 'Forbidden' })).toBe(false)
    expect(isUserRequiredBody(null)).toBe(false)
  })

  it('builds a clear toast text', () => {
    expect(userRequiredText('Toggling YOLO mode requires a signed-in user'))
      .toBe('Toggling YOLO mode requires a signed-in user. Sign in to Eggify and try again.')
    expect(userRequiredText()).toContain('requires a signed-in user')
  })

  it('announces a 403 user_required response and leaves the body readable', async () => {
    const l = listen(); stop = l.stop
    const res = new Response(JSON.stringify({ error: 'Changing providers requires a signed-in user', code: 'user_required' }), {
      status: 403,
      headers: { 'Content-Type': 'application/json' },
    })
    expect(await checkResponseForUserRequired(res)).toBe(true)
    expect(l.seen).toEqual(['Changing providers requires a signed-in user. Sign in to Eggify and try again.'])
    // The caller can still read the original body.
    expect((await res.json()).code).toBe('user_required')
  })

  it('ignores other responses', async () => {
    const l = listen(); stop = l.stop
    expect(await checkResponseForUserRequired(new Response('{"error":"Forbidden"}', { status: 403 }))).toBe(false)
    expect(await checkResponseForUserRequired(new Response('not json', { status: 403 }))).toBe(false)
    expect(await checkResponseForUserRequired(new Response('{"code":"user_required"}', { status: 200 }))).toBe(false)
    expect(l.seen).toEqual([])
  })

  it('announces WebSocket refusals', () => {
    const l = listen(); stop = l.stop
    announceUserRequired('Answering a permission request requires a signed-in user')
    expect(l.seen).toHaveLength(1)
    expect(l.seen[0]).toMatch(/^Answering a permission request requires a signed-in user\. Sign in/)
  })

  it('announces admin_required refusals as admin-only', async () => {
    const seen: any[] = []
    const handler = (e: Event) => seen.push((e as CustomEvent).detail)
    window.addEventListener(USER_REQUIRED_EVENT, handler)
    stop = () => window.removeEventListener(USER_REQUIRED_EVENT, handler)
    expect(isUserRequiredBody({ code: 'admin_required', error: 'x' })).toBe(true)
    const res = new Response(JSON.stringify({ error: 'Opening a terminal requires an admin user', code: 'admin_required' }), {
      status: 403,
      headers: { 'Content-Type': 'application/json' },
    })
    expect(await checkResponseForUserRequired(res)).toBe(true)
    expect(seen).toEqual([{ message: 'Only an admin can do this (Opening a terminal requires an admin user).', admin: true }])
    expect(userRequiredText(undefined, 'admin_required')).toBe('Only an admin can do this.')
  })
})
