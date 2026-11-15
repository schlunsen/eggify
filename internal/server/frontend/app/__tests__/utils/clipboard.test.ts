import { describe, it, expect, vi, afterEach } from 'vitest'

const runtime = { Clipboard: { SetText: vi.fn(async () => {}) } }
vi.mock('~/utils/desktop', () => ({
  desktopShell: vi.fn(() => 'web'),
  loadWailsRuntime: vi.fn(async () => runtime),
}))

import { copyText } from '~/utils/clipboard'
import { desktopShell } from '~/utils/desktop'

function setClipboard(writeText: ((t: string) => Promise<void>) | undefined) {
  Object.defineProperty(navigator, 'clipboard', {
    value: writeText ? { writeText } : undefined,
    configurable: true,
  })
}

afterEach(() => {
  vi.mocked(desktopShell).mockReturnValue('web')
  runtime.Clipboard.SetText.mockClear()
})

describe('copyText', () => {
  it('uses the native Wails clipboard in the desktop app', async () => {
    vi.mocked(desktopShell).mockReturnValue('wails')
    const writeText = vi.fn(async () => { throw new Error('NotAllowedError') })
    setClipboard(writeText)
    await copyText('hello')
    expect(runtime.Clipboard.SetText).toHaveBeenCalledWith('hello')
    expect(writeText).not.toHaveBeenCalled()
  })

  it('uses the Clipboard API in a browser', async () => {
    const writeText = vi.fn(async () => {})
    setClipboard(writeText)
    await copyText('hi')
    expect(writeText).toHaveBeenCalledWith('hi')
    expect(runtime.Clipboard.SetText).not.toHaveBeenCalled()
  })

  it('falls back to execCommand when the Clipboard API is rejected', async () => {
    setClipboard(vi.fn(async () => { throw new Error('denied') }))
    const exec = vi.fn(() => true)
    ;(document as any).execCommand = exec
    await copyText('fallback')
    expect(exec).toHaveBeenCalledWith('copy')
    expect(document.querySelector('textarea')).toBeNull()
  })

  it('rejects when every route fails', async () => {
    setClipboard(undefined)
    ;(document as any).execCommand = vi.fn(() => false)
    await expect(copyText('x')).rejects.toThrow('Clipboard unavailable')
  })
})
