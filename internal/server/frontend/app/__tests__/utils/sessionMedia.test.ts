import {
  anyConverting, formatMediaSize, isShowMediaTool, mediaByToolUse, mediaExtension,
  mentionsMediaPath, sortMedia, toolsMayTouchMedia, type MediaItem,
} from '../../utils/agents/sessionMedia'

const item = (over: Partial<MediaItem>): MediaItem => ({
  id: 'm', name: 'out.mp4', path: 'out.mp4', kind: 'video', mime_type: 'video/mp4', size: 10,
  modified: '2026-10-01T10:00:00Z', shown: false, ios_plays: true, url: '/x', ...over,
})

describe('mentionsMediaPath', () => {
  it('finds media file names in commands and text', () => {
    expect(mentionsMediaPath('ffmpeg -i in.png -c:v libx264 renders/out.mp4')).toBe(true)
    expect(mentionsMediaPath('"/tmp/song.FLAC"')).toBe(true)
    expect(mentionsMediaPath('wrote voice.opus.')).toBe(true)
    expect(mentionsMediaPath('take.aiff && ls')).toBe(true)
  })

  it('ignores other files and longer extensions', () => {
    expect(mentionsMediaPath('go test ./...')).toBe(false)
    expect(mentionsMediaPath('out.mp4.part')).toBe(false)
    expect(mentionsMediaPath('notes.mp3d')).toBe(false)
    expect(mentionsMediaPath('the mp4 format')).toBe(false)
    expect(mentionsMediaPath(undefined)).toBe(false)
  })
})

describe('toolsMayTouchMedia', () => {
  it('reacts to tool inputs that name media and to show_media', () => {
    expect(toolsMayTouchMedia([{ name: 'Bash', input: { command: 'ffmpeg -y -i a.wav b.m4a' } }])).toBe(true)
    expect(toolsMayTouchMedia([{ name: 'Write', input: { file_path: 'x.go', content: 'see clip.webm' } }])).toBe(true)
    expect(toolsMayTouchMedia([{ name: 'mcp__eggify-tools__show_media', input: { path: 'whatever' } }])).toBe(true)
    expect(toolsMayTouchMedia([{ name: 'Read', input: { file_path: 'main.go' } }, null])).toBe(false)
    expect(toolsMayTouchMedia(null)).toBe(false)
  })

  it('knows the show_media tool under any prefix', () => {
    expect(isShowMediaTool('show_media')).toBe(true)
    expect(isShowMediaTool('mcp__eggify-tools__show_media')).toBe(true)
    expect(isShowMediaTool('show_images')).toBe(false)
  })
})

describe('mediaByToolUse', () => {
  it('puts each file under the call that last named it, once', () => {
    const a = item({ id: 'a', tool_use_id: 't1', tool_use_ids: ['t0', 't1'] })
    const b = item({ id: 'b', tool_use_id: 't1' })
    const c = item({ id: 'c', tool_use_id: 't2' })
    const d = item({ id: 'd', tool_use_id: '' })
    const map = mediaByToolUse([a, b, c, d])
    expect([...map.keys()]).toEqual(['t1', 't2'])
    expect(map.get('t1')!.map(i => i.id)).toEqual(['a', 'b'])
    expect(map.get('t0')).toBeUndefined()
  })
})

describe('sortMedia', () => {
  it('lists newest first', () => {
    const old = item({ id: 'old', modified: '2026-01-01T00:00:00Z' })
    const recent = item({ id: 'new', modified: '2026-10-01T00:00:00Z' })
    const sameTimeLater = item({ id: 'seq', modified: '2026-01-01T00:00:00Z', sequence: 9 })
    expect(sortMedia([old, recent, sameTimeLater]).map(i => i.id)).toEqual(['new', 'seq', 'old'])
  })
})

describe('small helpers', () => {
  it('formats sizes and extensions', () => {
    expect(formatMediaSize(512)).toBe('512 B')
    expect(formatMediaSize(1536)).toBe('1.5 KB')
    expect(formatMediaSize(42 * 1024 * 1024)).toBe('42 MB')
    expect(mediaExtension('Clip.WEBM')).toBe('webm')
    expect(mediaExtension('noext')).toBe('')
    expect(anyConverting([item({ compatible: 'ready' }), item({ compatible: 'running' })])).toBe(true)
    expect(anyConverting([item({ compatible: 'failed' })])).toBe(false)
  })
})
