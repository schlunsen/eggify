import { describe, it, expect } from 'vitest'
import { isAuthPage, loginRedirectTarget } from '~/utils/loginRedirect'

describe('loginRedirectTarget', () => {
  it('keeps an in-app path', () => {
    expect(loginRedirectTarget('/agents?project=3')).toBe('/agents?project=3')
    expect(loginRedirectTarget(['/projects', '/x'])).toBe('/projects')
  })

  it('unwraps a login page nested by a 401 on the login page', () => {
    // fetchWithAuth used to push this while the login page was open; after
    // MFA the page then "redirected" to /login and stayed there.
    const nested = '/login?redirect=' + encodeURIComponent('/agents?session=abc') + '&required=true&error=unauthorized'
    expect(loginRedirectTarget(nested)).toBe('/agents?session=abc')
    const twice = '/login?redirect=' + encodeURIComponent(nested)
    expect(loginRedirectTarget(twice)).toBe('/agents?session=abc')
  })

  it('never returns an auth page', () => {
    expect(loginRedirectTarget('/login')).toBe('/')
    expect(loginRedirectTarget('/login?required=true')).toBe('/')
    expect(loginRedirectTarget('/setup')).toBe('/')
    expect(loginRedirectTarget('/access-denied')).toBe('/')
  })

  it('refuses targets that leave the origin', () => {
    for (const bad of ['https://evil.example', '//evil.example/x', '/\\evil.example', 'javascript:alert(1)', '/a\nb', '', undefined, null, 42]) {
      expect(loginRedirectTarget(bad)).toBe('/')
    }
  })
})

describe('isAuthPage', () => {
  it('matches only the auth pages', () => {
    expect(isAuthPage('/login')).toBe(true)
    expect(isAuthPage('/login?x=1')).toBe(true)
    expect(isAuthPage('/setup')).toBe(true)
    expect(isAuthPage('/loginx')).toBe(false)
    expect(isAuthPage('/agents')).toBe(false)
  })
})
