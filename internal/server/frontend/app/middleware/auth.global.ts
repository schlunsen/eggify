import { ACTIVATE_PATH, activationNeedsLogin } from '~/utils/license'
import { isNotchPath } from '~/utils/notch/notchRoute'

export default defineNuxtRouteMiddleware(async (to, from) => {
  // Skip middleware on server-side
  if (process.server) {
    return
  }

  // If on login page or setup page, always allow access
  if (to.path === '/login' || to.path === '/setup') {
    return
  }

  // The Notch Theatre window is too small for a login page: it shows "Sign
  // in to Eggify" itself and asks the server like any other page (its own
  // requests need the session).
  if (isNotchPath(to.path)) {
    return
  }

  // The activation page needs no session before the first account exists;
  // afterwards a signed-in user with MFA complete (the page checks again).
  if (to.path === ACTIVATE_PATH) {
    try {
      const { checkAuthStatus } = useAuth()
      const status = await checkAuthStatus()
      if (activationNeedsLogin(status)) {
        return navigateTo({ path: '/login', query: { redirect: ACTIVATE_PATH } })
      }
    } catch {
      // the page shows its own errors
    }
    return
  }

  // Always allow OAuth routes (they handle their own authentication)
  // This includes authorize, callback, and any other OAuth endpoints
  if (to.path.startsWith('/api/auth/oauth/')) {
    return
  }

  const { checkAuthStatus, authEnabled, isAuthenticated, needsSetup, licenseRequired, mfaRequired, mfaVerified } = useAuth()

  try {
    // Check authentication status
    await checkAuthStatus()

    // A licensed build that isn't activated: on a fresh install the
    // activation page comes before /setup (it works without an account).
    if (needsSetup.value) {
      return navigateTo(licenseRequired.value ? ACTIVATE_PATH : '/setup')
    }

    // If auth is not enabled, allow access (after activation)
    if (!authEnabled.value) {
      return licenseRequired.value ? navigateTo(ACTIVATE_PATH) : undefined
    }

    // SECURITY: If MFA is required but not yet verified, redirect to login for MFA verification
    // This handles the OAuth + MFA flow where user authenticated via OAuth but has MFA enabled
    if (mfaRequired.value && !mfaVerified.value) {
      return navigateTo({
        path: '/login',
        query: {
          redirect: to.fullPath
        }
      })
    }

    // If auth is enabled and user is not authenticated, redirect to login
    if (!isAuthenticated.value) {
      return navigateTo({
        path: '/login',
        query: {
          redirect: to.fullPath,
          required: 'true'
        }
      })
    }

    // Signed in, but this Eggify isn't activated yet
    if (licenseRequired.value) {
      return navigateTo(ACTIVATE_PATH)
    }

    // Allow access
  } catch (error) {
    // If we can't verify auth (backend down, network error, etc.), redirect to login
    return navigateTo({
      path: '/login',
      query: {
        redirect: to.fullPath,
        required: 'true',
        error: 'connection'
      }
    })
  }
})
