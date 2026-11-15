package server

import "crypto/subtle"

// validateWebSocketToken reports whether token authorizes a terminal
// WebSocket upgrade. The terminal is an interactive shell, the most
// privileged thing Eggify offers, and its upgrade is a GET that the global auth
// middleware lets through when require_login is off, so it checks itself.
//
// Accepted credentials:
//   - user auth enabled: a valid, MFA-complete session token ONLY, of an admin
//     (or the only account; UserStore.MayAdminister). The API key is not enough:
//     every local process (agents included) can read it.
//   - API-key auth only (user auth disabled): the API key (constant-time)
//   - auth fully disabled: anything (the operator opted out of auth)
func (s *Server) validateWebSocketToken(token string) bool {
	if s.sessionAuthMiddleware != nil && s.userStore != nil {
		user := s.userStore.userForSessionToken(token)
		return user != nil && s.userStore.MayAdminister(user.Username)
	}

	if s.authMiddleware != nil && s.authMiddleware.enabled {
		if token == "" {
			return false
		}
		return subtle.ConstantTimeCompare([]byte(token), []byte(s.authMiddleware.apiKey)) == 1
	}

	return true
}
