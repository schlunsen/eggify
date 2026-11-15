import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { isChromium, parakeetDevice, parakeetFiles } from '../../utils/parakeetModel'

const UA = {
  // WKWebView in the desktop app: no "Version/" or "Safari/" token, no Chrome
  desktop: 'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko)',
  safari: 'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/26.0 Safari/605.1.15',
  chrome: 'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/141.0.0.0 Safari/537.36',
  edge: 'Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/141.0.0.0 Safari/537.36 Edg/141.0.0.0',
  firefox: 'Mozilla/5.0 (Macintosh; Intel Mac OS X 10.15; rv:144.0) Gecko/20100101 Firefox/144.0',
  iosChrome: 'Mozilla/5.0 (iPhone; CPU iPhone OS 26_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) CriOS/141.0 Mobile/15E148 Safari/604.1',
}

describe('Parakeet model choice', () => {
  it('uses fp32 on WebGPU only in Chromium with a WebGPU adapter', () => {
    expect(parakeetDevice(UA.chrome, true)).toBe('webgpu')
    expect(parakeetDevice(UA.edge, true)).toBe('webgpu')
    expect(parakeetDevice(UA.chrome, false)).toBe('wasm')
  })

  it('never gives WebKit (desktop app, Safari, iOS) or Firefox the 2.4 GB encoder', () => {
    for (const ua of [UA.desktop, UA.safari, UA.firefox, UA.iosChrome]) {
      expect(isChromium(ua)).toBe(false)
      expect(parakeetDevice(ua, true)).toBe('wasm')
      expect(parakeetFiles(parakeetDevice(ua, true)).encoderData).toBeUndefined()
    }
  })

  it('pairs each device with its files', () => {
    expect(parakeetFiles('wasm')).toEqual({
      encoder: 'encoder-model.int8.onnx',
      decoder: 'decoder_joint-model.int8.onnx',
      vocab: 'vocab.txt',
    })
    expect(parakeetFiles('webgpu')).toMatchObject({
      encoder: 'encoder-model.onnx',
      encoderData: 'encoder-model.onnx.data',
    })
  })
})

// The worker must hand the cache URLs to parakeet.js and never copy a model
// file into a Blob: WebKit crashes the page process on a large Blob in a worker.
const fromUrls = vi.fn()
vi.mock('parakeet.js', () => ({
  fromUrls: (cfg: unknown) => fromUrls(cfg),
  getModelConfig: () => ({ featuresSize: 128, subsampling: 8 }),
}))

describe('parakeet worker', () => {
  const posted: any[] = []
  const fetched: string[] = []
  let cancels = 0
  let blobs = 0

  beforeEach(() => {
    vi.resetModules()
    posted.length = 0
    fetched.length = 0
    cancels = 0
    blobs = 0
    fromUrls.mockReset()
    fromUrls.mockResolvedValue({ transcribe: async () => ({ utterance_text: '', words: [] }) })
    vi.stubGlobal('postMessage', (m: unknown) => posted.push(m))
    vi.stubGlobal('fetch', async (url: string) => {
      fetched.push(url)
      return {
        ok: true,
        headers: new Headers({ 'X-Cache': 'HIT', 'content-length': '1000' }),
        body: { cancel: async () => { cancels++ } },
      }
    })
    const RealBlob = Blob
    vi.stubGlobal('Blob', class extends RealBlob {
      constructor(...args: ConstructorParameters<typeof Blob>) { super(...args); blobs++ }
    })
  })
  afterEach(() => vi.unstubAllGlobals())

  async function load(device: 'webgpu' | 'wasm') {
    await import('../../workers/parakeet-worker')
    await (self.onmessage as any)({ data: { type: 'load', data: { modelVersion: 'parakeet-tdt-0.6b-v3', options: { device } } } })
  }

  it('loads the int8 encoder from the cache URLs on WASM, without Blobs', async () => {
    await load('wasm')
    const base = `${self.location.origin}/api/model-cache/parakeet-tdt-0.6b-v3/`
    expect(fetched.sort()).toEqual([
      `${base}decoder_joint-model.int8.onnx`,
      `${base}encoder-model.int8.onnx`,
      `${base}vocab.txt`,
    ])
    const cfg = fromUrls.mock.calls[0]![0]
    expect(cfg).toMatchObject({
      encoderUrl: `${base}encoder-model.int8.onnx`,
      decoderUrl: `${base}decoder_joint-model.int8.onnx`,
      tokenizerUrl: `${base}vocab.txt`,
      encoderDataUrl: undefined,
      backend: 'wasm',
    })
    expect(cancels).toBe(3)
    expect(blobs).toBe(0)
    expect(posted.some(m => m.status === 'ready')).toBe(true)
  })

  it('fetches the fp32 weights only when asked for WebGPU', async () => {
    await load('webgpu')
    const cfg = fromUrls.mock.calls[0]![0]
    expect(cfg.encoderDataUrl).toMatch(/\/encoder-model\.onnx\.data$/)
    expect(cfg.backend).toBe('webgpu-hybrid')
    expect(blobs).toBe(0)
  })
})
