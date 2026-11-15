/**
 * Which Parakeet encoder a browser loads.
 *
 * The fp32 encoder keeps its weights in a 2.4 GB external `.data` file. WebKit
 * (Safari and the desktop app's WKWebView) crashes the whole page process when
 * a worker holds that much, and WebKit then reloads the page, which preloads
 * the model again: a reload every few seconds. So fp32 on WebGPU is used only
 * in Chromium with a working WebGPU adapter; everything else gets the
 * self-contained int8 encoder on WASM.
 */

export type ParakeetDevice = 'webgpu' | 'wasm'

export interface ParakeetFiles {
  encoder: string
  /** The fp32 encoder's external weights, fetched alongside it. */
  encoderData?: string
  decoder: string
  vocab: string
}

/** Chromium-based browsers (Chrome, Edge, Brave, Arc, Opera); not Safari/WKWebView or Firefox. */
export function isChromium(userAgent: string): boolean {
  return /\b(Chrome|Chromium|Edg|OPR)\//.test(userAgent)
}

export function parakeetDevice(userAgent: string, hasWebGPU: boolean): ParakeetDevice {
  return hasWebGPU && isChromium(userAgent) ? 'webgpu' : 'wasm'
}

export function parakeetFiles(device: ParakeetDevice): ParakeetFiles {
  const shared = { decoder: 'decoder_joint-model.int8.onnx', vocab: 'vocab.txt' }
  return device === 'webgpu'
    ? { encoder: 'encoder-model.onnx', encoderData: 'encoder-model.onnx.data', ...shared }
    : { encoder: 'encoder-model.int8.onnx', ...shared }
}

/** Rough download size, for the loading message. */
export function parakeetDownloadSize(device: ParakeetDevice): string {
  return device === 'webgpu' ? '~2.5GB' : '~700MB'
}
