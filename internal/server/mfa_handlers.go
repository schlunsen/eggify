package server

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/schlunsen/eggify/internal/database"
)

// handleMFASetupStart starts the MFA setup process
// POST /api/auth/mfa/setup
func (s *Server) handleMFASetupStart(c *fiber.Ctx) error {
	// Get current user from context
	user, ok := c.Locals("user").(*User)
	if !ok {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
			"error": "Not authenticated",
		})
	}

	// Check if MFA is already enabled
	repo := database.NewRepository(s.db)
	mfaConfig, err := repo.GetMFAConfig(user.Username)
	if err != nil {
		// Log the error for debugging
		fmt.Printf("Error getting MFA config: %v\n", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error":   "Failed to check MFA status",
			"details": err.Error(),
		})
	}

	if mfaConfig != nil && mfaConfig.IsEnabled {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "MFA is already enabled for this account",
		})
	}

	// Generate TOTP secret
	key, err := s.mfaManager.GenerateTOTPSecret(user.Username, "Eggify")
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Failed to generate TOTP secret",
		})
	}

	// Generate QR code
	qrCode, err := s.mfaManager.GenerateQRCode(key)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Failed to generate QR code",
		})
	}

	// Generate backup codes
	plainCodes, err := s.mfaManager.GenerateBackupCodes(10)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Failed to generate backup codes",
		})
	}

	// Keep the codes shown now (hashed) for when setup is verified; MFA stays
	// off until then. Verify used to make a new set it never showed, so the
	// codes the user saved didn't work.
	pending, err := s.mfaManager.NewBackupCodeCollection(plainCodes)
	if err == nil {
		var pendingJSON string
		if pendingJSON, err = s.mfaManager.SerializeBackupCodes(pending); err == nil {
			if mfaConfig == nil {
				mfaConfig = &database.MFAConfig{Username: user.Username, CreatedAt: time.Now()}
			}
			mfaConfig.BackupCodes = sql.NullString{String: pendingJSON, Valid: true}
			mfaConfig.UpdatedAt = time.Now()
			err = repo.SaveMFAConfig(mfaConfig)
		}
	}
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Failed to save backup codes",
		})
	}

	// Generate temporary token for setup
	tempToken := GenerateTemporaryToken()
	expiresAt := CalculateTokenExpiration(300) // 5 minutes

	temporaryToken := &database.MFATemporaryToken{
		Token:       tempToken,
		Username:    user.Username,
		TokenType:   "setup",
		TOTPSecret:  key.Secret(), // Store the TOTP secret for verification
		Verified:    false,
		Attempts:    0,
		MaxAttempts: 5,
		CreatedAt:   time.Now(),
		ExpiresAt:   expiresAt,
		IPAddress:   c.IP(),
		UserAgent:   c.Get("User-Agent"),
	}

	if err := repo.SaveMFATemporaryToken(temporaryToken); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Failed to save temporary token",
		})
	}

	// Log audit event
	auditLog := &database.MFAAuditLog{
		Username:  user.Username,
		Action:    "setup_started",
		Method:    "totp",
		Success:   true,
		IPAddress: c.IP(),
		UserAgent: c.Get("User-Agent"),
	}
	_ = repo.LogMFAAudit(auditLog)

	response := database.TOTPSetupResponse{
		QRCode:         qrCode,
		Secret:         key.Secret(), // Base32-encoded secret
		BackupCodes:    plainCodes,
		TemporaryToken: tempToken,
	}

	return c.JSON(response)
}

// handleMFASetupVerify verifies and completes MFA setup
// POST /api/auth/mfa/setup/verify
func (s *Server) handleMFASetupVerify(c *fiber.Ctx) error {
	// Get current user from context
	user, ok := c.Locals("user").(*User)
	if !ok {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
			"error": "Not authenticated",
		})
	}

	var req database.MFASetupVerifyRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Invalid request body",
		})
	}

	// Validate TOTP code format
	validator := &MFAConfigValidator{}
	if !validator.ValidateTOTPCode(req.Code) {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Invalid TOTP code format (must be 6 digits)",
		})
	}

	repo := database.NewRepository(s.db)

	// Get temporary token
	tempToken, err := repo.GetMFATemporaryToken(req.TemporaryToken)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Failed to validate temporary token",
		})
	}

	if tempToken == nil || tempToken.Username != user.Username || tempToken.TokenType != "setup" {
		auditLog := &database.MFAAuditLog{
			Username:  user.Username,
			Action:    "setup_failed",
			Method:    "totp",
			Success:   false,
			IPAddress: c.IP(),
			UserAgent: c.Get("User-Agent"),
			Reason:    "Invalid or expired temporary token",
		}
		_ = repo.LogMFAAudit(auditLog)

		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Invalid or expired setup token",
		})
	}

	// Check if token has expired
	if !VerifyTokenNotExpired(tempToken.ExpiresAt) {
		auditLog := &database.MFAAuditLog{
			Username:  user.Username,
			Action:    "setup_failed",
			Method:    "totp",
			Success:   false,
			IPAddress: c.IP(),
			UserAgent: c.Get("User-Agent"),
			Reason:    "Temporary token expired",
		}
		_ = repo.LogMFAAudit(auditLog)

		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Setup token has expired",
		})
	}

	// SECURITY: at most max_attempts guesses per setup token, counted
	// atomically like the login step.
	if claimed, err := repo.MFA.ClaimMFATemporaryTokenAttempt(req.TemporaryToken); err != nil || !claimed {
		return c.Status(fiber.StatusTooManyRequests).JSON(fiber.Map{
			"error": "Too many verification attempts. Start the setup again.",
		})
	}

	// Verify the TOTP code against the stored secret
	if tempToken.TOTPSecret == "" {
		auditLog := &database.MFAAuditLog{
			Username:  user.Username,
			Action:    "setup_failed",
			Method:    "totp",
			Success:   false,
			IPAddress: c.IP(),
			UserAgent: c.Get("User-Agent"),
			Reason:    "No TOTP secret found in temporary token",
		}
		_ = repo.LogMFAAudit(auditLog)

		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Invalid setup session",
		})
	}

	// Verify the TOTP code
	valid, err := s.mfaManager.VerifyTOTPCode(tempToken.TOTPSecret, req.Code)
	if err != nil || !valid {
		auditLog := &database.MFAAuditLog{
			Username:  user.Username,
			Action:    "setup_failed",
			Method:    "totp",
			Success:   false,
			IPAddress: c.IP(),
			UserAgent: c.Get("User-Agent"),
			Reason:    "Invalid TOTP code",
		}
		_ = repo.LogMFAAudit(auditLog)

		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Invalid verification code. Please check your authenticator app and try again.",
		})
	}

	// TOTP code verified: enable MFA with the backup codes setup showed
	// (stored hashed by handleMFASetupStart).
	mfaConfig, err := repo.GetMFAConfig(user.Username)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Failed to retrieve MFA config",
		})
	}
	var newCodes []string // only when setup's codes are missing (older start)
	backupCodesJSON := ""
	if mfaConfig != nil && mfaConfig.BackupCodes.Valid && mfaConfig.BackupCodes.String != "" {
		backupCodesJSON = mfaConfig.BackupCodes.String
	} else {
		newCodes, err = s.mfaManager.GenerateBackupCodes(10)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
				"error": "Failed to generate backup codes",
			})
		}
		collection, err := s.mfaManager.NewBackupCodeCollection(newCodes)
		if err == nil {
			backupCodesJSON, err = s.mfaManager.SerializeBackupCodes(collection)
		}
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
				"error": "Failed to process backup codes",
			})
		}
	}

	if mfaConfig == nil {
		mfaConfig = &database.MFAConfig{
			Username:  user.Username,
			CreatedAt: time.Now(),
		}
	}

	// Encrypt the TOTP secret for permanent storage
	encryptedSecret, iv, err := s.mfaManager.EncryptSecret(tempToken.TOTPSecret)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Failed to encrypt TOTP secret",
		})
	}

	// Update MFA config
	now := time.Now()
	mfaConfig.IsEnabled = true
	mfaConfig.TOTPSecret = sql.NullString{String: encryptedSecret, Valid: true}
	mfaConfig.TOTPSecretIV = sql.NullString{String: iv, Valid: true}
	mfaConfig.BackupCodes = sql.NullString{String: backupCodesJSON, Valid: true}
	mfaConfig.MFAEnabledAt = &now
	mfaConfig.UpdatedAt = now

	if err := repo.SaveMFAConfig(mfaConfig); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Failed to save MFA configuration",
		})
	}

	// Log audit event
	auditLog := &database.MFAAuditLog{
		Username:  user.Username,
		Action:    "setup_completed",
		Method:    "totp",
		Success:   true,
		IPAddress: c.IP(),
		UserAgent: c.Get("User-Agent"),
	}
	_ = repo.LogMFAAudit(auditLog)

	resp := fiber.Map{
		"message": "MFA setup completed successfully",
		"status":  "enabled",
	}
	if newCodes != nil {
		resp["backup_codes"] = newCodes // the ones shown at start were not kept
	}
	return c.JSON(resp)
}

// handleMFAVerify verifies MFA code during login
// POST /api/auth/mfa/verify
//
// SECURITY:
//   - Each attempt is claimed atomically in the database
//     (ClaimMFATemporaryTokenAttempt) before the code is checked, so at most
//     max_attempts (5) guesses per temporary token are ever evaluated, however
//     many arrive in parallel. Before, the count was read, checked and written
//     back later, and 60 parallel guesses were all evaluated.
//   - A backup code is consumed with a compare-and-swap on the stored codes,
//     so one code cannot be used twice in parallel.
//   - The temporary token is consumed atomically on success (one session per
//     login), and the MFA-pending session of an OAuth login is revoked.
//   - The route is rate limited per IP (routes/auth.go) and bcrypt work is
//     bounded (bcryptSlots).
func (s *Server) handleMFAVerify(c *fiber.Ctx) error {
	var req struct {
		TemporaryToken string `json:"temporary_token"`
		Code           string `json:"code"`
	}

	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Invalid request body",
		})
	}
	if req.TemporaryToken == "" || len(req.TemporaryToken) > 256 || len(req.Code) > 64 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Invalid temporary token",
		})
	}

	repo := database.NewRepository(s.db)

	// Get temporary token
	tempToken, err := repo.GetMFATemporaryToken(req.TemporaryToken)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Failed to validate temporary token",
		})
	}

	if tempToken == nil || tempToken.TokenType != "login" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Invalid temporary token",
		})
	}

	audit := func(action, method string, success bool, reason string) {
		_ = repo.LogMFAAudit(&database.MFAAuditLog{
			Username:  tempToken.Username,
			Action:    action,
			Method:    method,
			Success:   success,
			IPAddress: c.IP(),
			UserAgent: c.Get("User-Agent"),
			Reason:    reason,
		})
	}

	// SECURITY: Log IP address mismatch for audit but don't block (AUTH-VULN-04)
	// Strict IP binding can cause DoS when users are behind load balancers, CDNs,
	// or have changing IPs (mobile networks, IPv4/IPv6 dual-stack). We log the
	// mismatch for security monitoring instead of blocking the request.
	if tempToken.IPAddress != c.IP() {
		audit("verification_ip_mismatch", "totp", true,
			fmt.Sprintf("IP changed: login from %s, verify from %s (allowed, logged for audit)", tempToken.IPAddress, c.IP()))
	}

	// Check if token has expired
	if !VerifyTokenNotExpired(tempToken.ExpiresAt) {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Login token has expired",
		})
	}

	// SECURITY: count the attempt against the account (across temporary
	// tokens and addresses, see mfa_throttle.go). A locked account's code is
	// refused before it is checked, and the token keeps its attempts.
	limits := s.mfaLimits()
	if retryAfter, err := limits.begin(tempToken.Username); err != nil {
		audit("verification_failed", "totp", false, "Account locked - too many failed verifications")
		return mfaThrottled(c, retryAfter)
	}
	verified := false
	defer func() {
		if !verified {
			s.loginLimits().failed() // counts towards the global failure cap
		}
	}()

	// SECURITY: claim one attempt atomically before evaluating anything.
	claimed, err := repo.MFA.ClaimMFATemporaryTokenAttempt(req.TemporaryToken)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Failed to validate temporary token",
		})
	}
	if !claimed {
		audit("verification_failed", "totp", false, "Rate limited - too many attempts")
		return c.Status(fiber.StatusTooManyRequests).JSON(fiber.Map{
			"error": "Too many verification attempts. Please try again later.",
		})
	}

	// Get MFA config
	mfaConfig, err := repo.GetMFAConfig(tempToken.Username)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Failed to retrieve MFA config",
		})
	}

	if mfaConfig == nil || !mfaConfig.IsEnabled {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "MFA is not enabled for this account",
		})
	}

	validator := &MFAConfigValidator{}
	var method string
	var extra fiber.Map

	switch {
	case validator.ValidateTOTPCode(req.Code):
		method = "totp"
		// Verify the TOTP code against the stored encrypted secret
		if !mfaConfig.TOTPSecret.Valid || mfaConfig.TOTPSecret.String == "" {
			audit("verification_failed", method, false, "No TOTP secret stored in MFA config")
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
				"error": "MFA configuration error",
			})
		}

		// Decrypt the TOTP secret
		decryptedSecret, err := s.mfaManager.DecryptSecret(mfaConfig.TOTPSecret.String, mfaConfig.TOTPSecretIV.String)
		if err != nil {
			audit("verification_failed", method, false, "Failed to decrypt TOTP secret")
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
				"error": "MFA configuration error",
			})
		}

		// Verify the TOTP code
		step, valid, err := s.mfaManager.MatchTOTPStep(decryptedSecret, req.Code, time.Now())
		if err != nil || !valid {
			audit("verification_failed", method, false, "Invalid TOTP code")
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"error": "Invalid verification code. Please check your authenticator app and try again.",
			})
		}
		// SECURITY: a code is accepted once; a replay inside its window
		// (or a parallel second use) is refused.
		if !limits.acceptStep(tempToken.Username, step) {
			audit("verification_failed", method, false, "TOTP code already used")
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"error": "This code was already used. Wait for your authenticator app to show a new code and try again.",
			})
		}

	case validator.ValidateBackupCodeFormat(req.Code):
		method = "backup_code"
		if !mfaConfig.BackupCodes.Valid || mfaConfig.BackupCodes.String == "" {
			audit("verification_failed", method, false, "No backup codes available")
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"error": "No backup codes available",
			})
		}

		stored := mfaConfig.BackupCodes.String
		collection, err := s.mfaManager.DeserializeBackupCodes(stored)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
				"error": "Failed to process backup codes",
			})
		}

		// Codes are generated upper case; accept what the user typed in any case.
		valid, updatedCodes, err := s.mfaManager.VerifyAndUseBackupCode(collection, strings.ToUpper(req.Code))
		if valid && err == nil {
			// SECURITY: consume the code only if nobody changed the codes
			// since we read them (the same code used in parallel).
			swapped, swapErr := repo.MFA.SwapMFABackupCodes(tempToken.Username, stored, updatedCodes)
			valid = swapErr == nil && swapped
		}
		if !valid || err != nil {
			audit("verification_failed", method, false, "Invalid or already used backup code")
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"error": "Invalid or already used backup code",
			})
		}
		extra = fiber.Map{"backup_codes_remaining": s.mfaManager.CountUnusedBackupCodes(collection)}

	default:
		audit("verification_failed", "totp", false, "Invalid code format")
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Invalid verification code format",
		})
	}

	// SECURITY: consume the temporary token; only the request that deletes
	// it gets a session.
	consumed, err := repo.MFA.ConsumeMFATemporaryToken(req.TemporaryToken)
	if err != nil || !consumed {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Invalid temporary token",
		})
	}
	// The OAuth MFA-pending session (if any) has done its job.
	s.userStore.revokeMFAPendingSessions(req.TemporaryToken)
	verified = true
	limits.succeeded(tempToken.Username)

	audit("verification_success", method, true, "")
	_ = repo.MFA.RecordMFAVerification(tempToken.Username, method)

	// Create a session token for the user
	sessionToken, err := s.userStore.createUserSession(tempToken.Username, sessionMetaFromRequest(c, "password"))
	if err != nil {
		fmt.Printf("Error generating session token: %v\n", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Failed to create session",
		})
	}

	// Set session cookie
	c.Cookie(&fiber.Cookie{
		Name:     "session_token",
		Value:    sessionToken,
		Path:     "/",
		HTTPOnly: true,
		Secure:   s.config.TLS.Enabled || requestIsHTTPS(c),
		SameSite: "Lax",
		Expires:  time.Now().Add(s.userStore.SessionLifetime()),
	})

	// SECURITY: Session token not included in response body - cookie only
	resp := fiber.Map{
		"message": "MFA verification successful",
		"status":  "verified",
	}
	if method == "backup_code" {
		resp["message"] = "MFA verification successful with backup code"
	}
	for k, v := range extra {
		resp[k] = v
	}
	return c.JSON(resp)
}

// handleMFAStatus returns MFA status
// GET /api/auth/mfa/status
func (s *Server) handleMFAStatus(c *fiber.Ctx) error {
	// Get current user from context
	user, ok := c.Locals("user").(*User)
	if !ok {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
			"error": "Not authenticated",
		})
	}

	repo := database.NewRepository(s.db)
	mfaConfig, err := repo.GetMFAConfig(user.Username)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Failed to retrieve MFA status",
		})
	}

	status := &database.MFAStatus{
		Enabled: false,
	}

	if mfaConfig != nil && mfaConfig.IsEnabled {
		status.Enabled = true
		status.Method = "totp"
		status.LastVerifiedAt = mfaConfig.LastVerifiedAt
		if mfaConfig.LastVerificationMethod.Valid {
			status.LastVerificationMethod = mfaConfig.LastVerificationMethod.String
		}

		if mfaConfig.BackupCodes.Valid && mfaConfig.BackupCodes.String != "" {
			collection, _ := s.mfaManager.DeserializeBackupCodes(mfaConfig.BackupCodes.String)
			if collection != nil {
				status.BackupCodesCount = s.mfaManager.CountUnusedBackupCodes(collection)
			}
		}
	}

	return c.JSON(status)
}

// handleMFADisable disables MFA for the user
// POST /api/auth/mfa/disable
func (s *Server) handleMFADisable(c *fiber.Ctx) error {
	// Get current user from context
	user, ok := c.Locals("user").(*User)
	if !ok {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
			"error": "Not authenticated",
		})
	}

	var req struct {
		Password string `json:"password"` // Require password to disable MFA
	}

	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Invalid request body",
		})
	}

	// SECURITY: Use VerifyPassword instead of Authenticate to avoid creating orphaned sessions
	// (behind the login throttle: wrong passwords here count and lock like
	// wrong login passwords, so a stolen session cannot guess the password).
	_, err := s.userStore.verifyLogin(s.loginLimits(), clientIP(c), user.Username, req.Password)
	if errors.Is(err, ErrLoginThrottled) {
		return passwordConfirmThrottled(c)
	}
	if err != nil {
		auditLog := &database.MFAAuditLog{
			Username:  user.Username,
			Action:    "disable_failed",
			Method:    "totp",
			Success:   false,
			IPAddress: c.IP(),
			UserAgent: c.Get("User-Agent"),
			Reason:    "Invalid password",
		}
		repo := database.NewRepository(s.db)
		_ = repo.LogMFAAudit(auditLog)

		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
			"error": "Invalid password",
		})
	}

	repo := database.NewRepository(s.db)

	// Disable MFA
	if err := repo.DisableMFAForUser(user.Username); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Failed to disable MFA",
		})
	}

	// Log audit event
	auditLog := &database.MFAAuditLog{
		Username:  user.Username,
		Action:    "mfa_disabled",
		Method:    "totp",
		Success:   true,
		IPAddress: c.IP(),
		UserAgent: c.Get("User-Agent"),
	}
	_ = repo.LogMFAAudit(auditLog)

	return c.JSON(fiber.Map{
		"message": "MFA disabled successfully",
		"status":  "disabled",
	})
}

// handleMFABackupCodesRegenerate generates new backup codes
// POST /api/auth/mfa/backup-codes/regenerate
func (s *Server) handleMFABackupCodesRegenerate(c *fiber.Ctx) error {
	// Get current user from context
	user, ok := c.Locals("user").(*User)
	if !ok {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
			"error": "Not authenticated",
		})
	}

	var req struct {
		Password string `json:"password"` // Require password to regenerate codes
	}

	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Invalid request body",
		})
	}

	// SECURITY: Use VerifyPassword instead of Authenticate to avoid creating orphaned sessions
	// (behind the login throttle, like MFA disable).
	_, err := s.userStore.verifyLogin(s.loginLimits(), clientIP(c), user.Username, req.Password)
	if errors.Is(err, ErrLoginThrottled) {
		return passwordConfirmThrottled(c)
	}
	if err != nil {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
			"error": "Invalid password",
		})
	}

	repo := database.NewRepository(s.db)

	// Get current MFA config
	mfaConfig, err := repo.GetMFAConfig(user.Username)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Failed to retrieve MFA config",
		})
	}

	if mfaConfig == nil || !mfaConfig.IsEnabled {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "MFA is not enabled for this account",
		})
	}

	// Generate new backup codes
	plainCodes, err := s.mfaManager.GenerateBackupCodes(10)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Failed to generate backup codes",
		})
	}

	collection, err := s.mfaManager.NewBackupCodeCollection(plainCodes)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Failed to process backup codes",
		})
	}

	backupCodesJSON, err := s.mfaManager.SerializeBackupCodes(collection)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Failed to serialize backup codes",
		})
	}

	// Update config
	mfaConfig.BackupCodes = sql.NullString{String: backupCodesJSON, Valid: true}
	mfaConfig.UpdatedAt = time.Now()

	if err := repo.SaveMFAConfig(mfaConfig); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Failed to save new backup codes",
		})
	}

	// Log audit event
	auditLog := &database.MFAAuditLog{
		Username:  user.Username,
		Action:    "backup_codes_regenerated",
		Method:    "totp",
		Success:   true,
		IPAddress: c.IP(),
		UserAgent: c.Get("User-Agent"),
	}
	_ = repo.LogMFAAudit(auditLog)

	response := fiber.Map{
		"message":      "Backup codes regenerated successfully",
		"backup_codes": plainCodes,
	}

	return c.JSON(response)
}

// handleMFAAuditLog retrieves MFA audit log
// GET /api/auth/mfa/audit-log
func (s *Server) handleMFAAuditLog(c *fiber.Ctx) error {
	// Get current user from context
	user, ok := c.Locals("user").(*User)
	if !ok {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
			"error": "Not authenticated",
		})
	}

	limit := c.QueryInt("limit", 50)
	offset := c.QueryInt("offset", 0)

	if limit > 500 {
		limit = 500 // Cap limit to prevent abuse
	}

	repo := database.NewRepository(s.db)
	logs, err := repo.GetMFAAuditLog(user.Username, limit, offset)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Failed to retrieve audit log",
		})
	}

	return c.JSON(fiber.Map{
		"logs":  logs,
		"count": len(logs),
	})
}
