import { checkResponseForLicenseRequired, goToActivation, isLicenseRequiredBody } from '~/utils/license'

/**
 * Opens the activation page whenever the server refuses a request because
 * Eggify isn't activated (402, code "license_required").
 *
 * Runs first (`enforce: 'pre'`) so code that caches `fetch` at plugin time
 * gets the wrapped one, and also hooks the global `$fetch` (ofetch), which
 * `useFetch`/`useAsyncData`/`$fetch` use. Responses pass through untouched.
 */
export function installLicenseInterceptors(win: Window & typeof globalThis = window) {
  const originalFetch = win.fetch.bind(win)
  win.fetch = async (input: RequestInfo | URL, init?: RequestInit) => {
    const response = await originalFetch(input, init)
    if (response.status === 402) {
      void checkResponseForLicenseRequired(response)
    }
    return response
  }

  const g = globalThis as any
  if (typeof g.$fetch?.create === 'function') {
    g.$fetch = g.$fetch.create({
      onResponseError({ response }: { response: { status: number, _data?: unknown } }) {
        if (response?.status === 402 && isLicenseRequiredBody(response._data)) {
          goToActivation()
        }
      },
    })
  }
}

export default defineNuxtPlugin({
  name: 'license-required',
  enforce: 'pre',
  setup() {
    installLicenseInterceptors()
  },
})
