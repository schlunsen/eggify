import { checkResponseForUserRequired } from '~/utils/userRequired'

/**
 * Shows the "sign in" toast whenever the server refuses a privileged request
 * because it did not come from a signed-in user (403, code "user_required").
 * Wraps window.fetch so every caller is covered without changes; the response
 * is passed through untouched.
 */
export default defineNuxtPlugin(() => {
  const originalFetch = window.fetch.bind(window)
  window.fetch = async (input: RequestInfo | URL, init?: RequestInit) => {
    const response = await originalFetch(input, init)
    if (response.status === 403) {
      void checkResponseForUserRequired(response)
    }
    return response
  }
})
