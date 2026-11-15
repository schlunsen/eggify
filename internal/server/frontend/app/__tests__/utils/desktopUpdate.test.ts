import { describe, it, expect, afterEach, vi } from 'vitest'
import {
  desktopCheckForUpdates,
  desktopInstallUpdate,
  desktopShell,
  desktopUpdateState,
  onDesktopUpdate,
  updateToastMode,
  type DesktopUpdateState,
} from '~/utils/desktop'

const base: DesktopUpdateState = {
  current_version: '2.0.0',
  latest_version: '2.1.0',
  update_available: true,
  can_install: true,
  phase: 'idle',
  percent: 0,
}

describe('desktop self-update adapter', () => {
  afterEach(() => {
    delete (window as any).__TAURI_INTERNALS__
    vi.restoreAllMocks()
  })

  it('is a no-op in a plain browser', async () => {
    expect(desktopShell()).toBe('web')
    expect(await desktopUpdateState()).toBeNull()
    expect(await desktopCheckForUpdates()).toBeNull()
    expect(await desktopInstallUpdate()).toBe(false)
    const off = await onDesktopUpdate(() => {})
    expect(typeof off).toBe('function')
  })

  it('is a no-op in the Tauri app', async () => {
    const invoke = vi.fn()
    ;(window as any).__TAURI_INTERNALS__ = { invoke }
    expect(desktopShell()).toBe('tauri')
    expect(await desktopUpdateState()).toBeNull()
    expect(await desktopInstallUpdate()).toBe(false)
    expect(invoke).not.toHaveBeenCalled()
  })
})

describe('updateToastMode', () => {
  it('hides without state or update', () => {
    expect(updateToastMode(null, null)).toBe('hidden')
    expect(updateToastMode({ ...base, update_available: false }, null)).toBe('hidden')
  })

  it('offers restart when installable, download otherwise', () => {
    expect(updateToastMode(base, null)).toBe('available')
    expect(updateToastMode({ ...base, can_install: false }, null)).toBe('manual')
  })

  it('respects a dismissal for that version only', () => {
    expect(updateToastMode(base, '2.1.0')).toBe('hidden')
    expect(updateToastMode(base, '2.0.5')).toBe('available')
  })

  it('shows progress and errors even when dismissed', () => {
    expect(updateToastMode({ ...base, phase: 'downloading', percent: 40 }, '2.1.0')).toBe('progress')
    expect(updateToastMode({ ...base, phase: 'restarting' }, null)).toBe('progress')
    expect(updateToastMode({ ...base, phase: 'error', error: 'checksum mismatch' }, '2.1.0')).toBe('error')
  })
})
