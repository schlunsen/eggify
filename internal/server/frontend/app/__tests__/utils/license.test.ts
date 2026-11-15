import {
  formatSerialInput, normalizeSerial, serialCheckChar, serialProblem, licenseErrorMessage, licenseBlocks,
  checkResponseForLicenseRequired, probeLicenseAfterSocketClose, caretAfterFormat, noteLicenseRefusal, isLicenseRequiredBody, _resetLicenseThrottles,
} from '../../utils/license'

// docs/licensing-protocol.md vectors (body → serial)
const VECTORS: [string, string][] = [
  ['0000000000000000000', 'EGG-00000-00000-00000-00000'],
  ['ABCDEFGHJKMNPQRSTVW', 'EGG-ABCDE-FGHJK-MNPQR-STVWT'],
  ['ZZZZZZZZZZZZZZZZZZZ', 'EGG-ZZZZZ-ZZZZZ-ZZZZZ-ZZZZ0'],
  ['7H3K9Q2M4X8R6T1V5N0', 'EGG-7H3K9-Q2M4X-8R6T1-V5N0T'],
]

describe('serials', () => {
  it('matches the protocol vectors', () => {
    for (const [body, serial] of VECTORS) {
      expect(formatSerialInput(body + serialCheckChar(body))).toBe(serial)
      expect(normalizeSerial(serial)).toBe(body + serialCheckChar(body))
    }
  })

  it('normalises like the server', () => {
    expect(normalizeSerial('egg 7h3k9-q2m4x-8r6t1-v5n0t')).toBe('7H3K9Q2M4X8R6T1V5N0T')
    expect(normalizeSerial('EGG_7H3K9_Q2M4X_8R6TI_V5NOT')).toBe('7H3K9Q2M4X8R6T1V5N0T') // I→1, O→0
    expect(normalizeSerial('EGG-7H3K9-Q2M4X-8R6T1-V5N0A')).toBeNull() // wrong check character
    expect(normalizeSerial('EGG-7H3K9-Q2M4X-8R6T1-V5N0')).toBeNull()
    expect(normalizeSerial('EGG-7H3K9-Q2M4X-8R6T1-V5NUT')).toBeNull()
  })

  it('keeps a serial that starts with EGG', () => {
    const serial = 'EGG00000000000000001'
    expect(normalizeSerial('EGG-EGG00-00000-00000-00001')).toBe(serial)
    expect(normalizeSerial(serial)).toBe(serial)
    expect(normalizeSerial('EGGEGG00000000000000001')).toBe(serial)
  })

  it('formats as you type', () => {
    expect(formatSerialInput('')).toBe('')
    // Typing the prefix by hand
    expect(formatSerialInput('e')).toBe('E')
    expect(formatSerialInput('eg')).toBe('EG')
    expect(formatSerialInput('egg')).toBe('EGG')
    expect(formatSerialInput('EGG-')).toBe('EGG-')
    expect(formatSerialInput('egg7')).toBe('EGG-7')
    expect(formatSerialInput('ex')).toBe('EGG-EX') // a body that starts with E
    expect(formatSerialInput('egg7h3k9q2')).toBe('EGG-7H3K9-Q2')
    expect(formatSerialInput('EGG-7H3K9-')).toBe('EGG-7H3K9')
    expect(formatSerialInput('7h3k9 q2m4x 8r6t1 v5n0t')).toBe('EGG-7H3K9-Q2M4X-8R6T1-V5N0T')
  })

  it('keeps a pasted body that starts with EGG typed without the prefix', () => {
    // body EGG0000000000000000 + check char, no prefix, 20 characters
    const body = 'EGG0000000000000000'
    const serial = body + serialCheckChar(body)
    const formatted = formatSerialInput(serial)
    expect(formatted).toBe('EGG-EGG00-00000-00000-0000' + serialCheckChar(body))
    expect(normalizeSerial(formatted)).toBe(serial)
    // idempotent: reformatting the formatted value changes nothing
    expect(formatSerialInput(formatted)).toBe(formatted)
    // a body still being completed (19 characters) is not eaten either
    expect(formatSerialInput(body)).toBe('EGG-EGG00-00000-00000-0000')
    // with the prefix typed, one EGG is the prefix
    expect(formatSerialInput('EGG-' + serial)).toBe(formatted)
    // normal prefixed typing is unchanged
    expect(formatSerialInput('egg7h3k9q2')).toBe('EGG-7H3K9-Q2')
  })

  it('maps the caret after the same serial characters', () => {
    // editing in the middle of a formatted serial: insert a character after "EGG-7H3"
    const raw = 'EGG-7H3X-K9Q2M'
    const formatted = formatSerialInput(raw)
    expect(formatted).toBe('EGG-7H3XK-9Q2M')
    expect(caretAfterFormat(raw, 8, formatted)).toBe(8) // after X
    // a dash is dropped: caret stays after the typed characters, not at the end
    expect(caretAfterFormat('EGG-7H3K9Q2M', 7, 'EGG-7H3K9-Q2M')).toBe(7)
    // typed without prefix: the added "EGG-" doesn't count
    expect(caretAfterFormat('7H3K9Q2', 2, 'EGG-7H3K9-Q2')).toBe(6)
    // at the end stays at the end
    expect(caretAfterFormat('7H3K9Q2', 7, 'EGG-7H3K9-Q2')).toBe(12)
  })

  it('explains what is wrong in plain words', () => {
    expect(serialProblem('')).toBe('')
    expect(serialProblem('EGG-7H3K9')).toBe('15 more characters to go.')
    expect(serialProblem('EGG-7H3K9-Q2M4X-8R6T1-V5N0')).toBe('1 more character to go.')
    expect(serialProblem('EGG-7H3K9-Q2M4X-8R6T1-V5NUT')).toContain('"U"')
    expect(serialProblem('EGG-7H3K9-Q2M4X-8R6T1-V5N0A')).toContain('typo')
    expect(serialProblem('EGG-7H3K9-Q2M4X-8R6T1-V5N0T')).toBe('')
  })
})

describe('license errors', () => {
  it('lists the machines when every seat is taken', () => {
    const msg = licenseErrorMessage({
      code: 'seats_full',
      activations: [{ machine: 'Studio' }, { machine: 'Laptop' }, { machine: 'Work Mac' }],
    }, 409)
    expect(msg).toContain('Studio, Laptop, Work Mac')
    expect(msg).toContain('Settings → License')
  })

  it("uses the server's words otherwise", () => {
    expect(licenseErrorMessage({ code: 'unknown_serial', error: "That serial number doesn't exist." }, 404))
      .toBe("That serial number doesn't exist.")
    expect(licenseErrorMessage(null, 403)).toContain('admin')
  })

  it('knows when the app is blocked', () => {
    expect(licenseBlocks({ required: true, state: 'unlicensed' })).toBe(true)
    expect(licenseBlocks({ required: true, state: 'expired' })).toBe(true)
    expect(licenseBlocks({ required: true, state: 'active' })).toBe(false)
    expect(licenseBlocks({ required: false, state: 'unlicensed' })).toBe(false)
  })
})

describe('license_required redirects', () => {
  const navigate = vi.fn()
  beforeEach(() => {
    _resetLicenseThrottles()
    navigate.mockReset()
    vi.stubGlobal('navigateTo', navigate)
    window.history.replaceState({}, '', '/agents')
  })
  afterEach(() => vi.unstubAllGlobals())

  const json = (status: number, body: unknown) =>
    new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })

  it('opens /activate on a 402 license_required and leaves the body unread', async () => {
    const res = json(402, { code: 'license_required', error: 'Activate Eggify with your serial number' })
    expect(await checkResponseForLicenseRequired(res)).toBe(true)
    expect(navigate).toHaveBeenCalledWith('/activate')
    expect(res.bodyUsed).toBe(false)
  })

  it('ignores other refusals and pages already on /activate', async () => {
    expect(await checkResponseForLicenseRequired(json(403, { code: 'user_required' }))).toBe(false)
    expect(await checkResponseForLicenseRequired(json(402, { code: 'something_else' }))).toBe(false)
    window.history.replaceState({}, '', '/activate')
    await checkResponseForLicenseRequired(json(402, { code: 'license_required' }))
    expect(navigate).not.toHaveBeenCalled()
  })

  it('asks /api/license after a socket closes (throttled)', async () => {
    const fetcher = vi.fn().mockResolvedValue(json(200, { required: true, state: 'unlicensed' }))
    expect(await probeLicenseAfterSocketClose(fetcher as any)).toBe(true)
    expect(navigate).toHaveBeenCalledWith('/activate')
    expect(await probeLicenseAfterSocketClose(fetcher as any)).toBe(false) // within 30 s
    expect(fetcher).toHaveBeenCalledTimes(1)

    _resetLicenseThrottles()
    navigate.mockReset()
    fetcher.mockResolvedValue(json(200, { required: true, state: 'active' }))
    expect(await probeLicenseAfterSocketClose(fetcher as any)).toBe(false)
    expect(navigate).not.toHaveBeenCalled()
  })

  it('probes at once after a license_required refusal, throttling only repeats', async () => {
    const fetcher = vi.fn().mockImplementation(async () => json(200, { required: true, state: 'unlicensed' }))
    expect(await probeLicenseAfterSocketClose(fetcher as any)).toBe(true)
    noteLicenseRefusal()
    expect(await probeLicenseAfterSocketClose(fetcher as any)).toBe(true)
    expect(fetcher).toHaveBeenCalledTimes(2)
    expect(await probeLicenseAfterSocketClose(fetcher as any)).toBe(false)
  })

  it('recognises the WebSocket error shape', () => {
    expect(isLicenseRequiredBody({ type: 'error', code: 'license_required', message: 'x' })).toBe(true)
    expect(isLicenseRequiredBody({ type: 'error', code: 'user_required' })).toBe(false)
  })
})
