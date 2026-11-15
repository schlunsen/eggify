package server

import (
	"crypto/rand"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/schlunsen/eggify/internal/database"
	"golang.org/x/crypto/bcrypt"
)

// usernameRegex validates usernames: alphanumeric, underscores, hyphens, 1-64 chars
var usernameRegex = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`)

// User represents an authenticated user
type User struct {
	Username        string    `json:"username"`
	PasswordHash    string    `json:"-"`
	Email           string    `json:"email,omitempty"`
	AuthMethod      string    `json:"auth_method"` // 'password', 'oauth', 'both'
	OAuthProviderID string    `json:"oauth_provider_id,omitempty"`
	OAuthProvider   string    `json:"oauth_provider,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
	IsAdmin         bool      `json:"is_admin"`
	AvatarID        *int64    `json:"avatar_id,omitempty"` // Selected avatar
}

// Session represents a user session.
//
// SECURITY: the session token itself is never kept by the server, in memory
// or in the database (migration 49): sessions are keyed by
// database.HashToken(token), and every lookup hashes the presented token.
type Session struct {
	TokenHash    string    `json:"-"`
	Username     string    `json:"username"`
	ExpiresAt    time.Time `json:"expires_at"`
	CreatedAt    time.Time `json:"created_at"`
	MFARequired  bool      `json:"mfa_required"` // True if MFA is needed but not yet verified
	MFAVerified  bool      `json:"mfa_verified"` // True if MFA has been verified
	MFATempToken string    `json:"-"`            // Temporary token for MFA verification (not sent to client)

	// Device metadata (see user_sessions.go). SessionID is public: it names
	// the session for listing and revoking, it is not a credential.
	SessionID   string     `json:"session_id"`
	DeviceName  string     `json:"device_name,omitempty"`
	DeviceModel string     `json:"device_model,omitempty"`
	CreatedVia  string     `json:"created_via,omitempty"`
	LastUsedAt  *time.Time `json:"last_used_at,omitempty"`
	// MintedBy is the public id of the session whose pairing code created
	// this one; revoking that session revokes this one (revokeMinted).
	MintedBy      string `json:"-"`
	lastPersisted time.Time
}

// UserStore manages user accounts
// SECURITY: All map access is protected by mu (RWMutex) to prevent data races
// under concurrent Fiber request handling.
type UserStore struct {
	mu        sync.RWMutex
	users     map[string]*User
	sessions  map[string]*Session // keyed by database.HashToken(session token)
	db        *database.Database  // Database for all operations
	configDir string              // Directory where .secret file is stored
	apiKey    string              // Cached API key
	lifetime  time.Duration       // Session lifetime (auth.session_lifetime_hours); 0 = SessionDuration

	// onMintedRevoked closes the sockets of a session that was revoked
	// because the session that paired it was (optional).
	onMintedRevoked func(tokenHash string)
	// mintMu makes "the minting session is alive, create the paired session"
	// and "revoke the minting session and what it minted" mutually exclusive,
	// so a code made before a revocation cannot be redeemed after it.
	mintMu sync.Mutex
}

const (
	// SessionDuration is the session lifetime when auth.session_lifetime_hours
	// is not set (7 days, so users don't have to re-login daily).
	SessionDuration = 7 * 24 * time.Hour
	// Password hash cost
	PasswordHashCost = 12
)

// NewUserStore creates a new user store
func NewUserStore(configDir string) *UserStore {
	return &UserStore{
		users:     make(map[string]*User),
		sessions:  make(map[string]*Session),
		configDir: configDir,
	}
}

// SetSessionLifetime sets how long a new session stays valid
// (auth.session_lifetime_hours). Sessions created before are also refused
// once they are older than this. d <= 0 restores SessionDuration.
func (us *UserStore) SetSessionLifetime(d time.Duration) {
	us.mu.Lock()
	defer us.mu.Unlock()
	if d <= 0 {
		d = 0
	}
	us.lifetime = d
}

// SessionLifetime returns how long a session stays valid.
func (us *UserStore) SessionLifetime() time.Duration {
	if us == nil {
		return SessionDuration
	}
	us.mu.RLock()
	defer us.mu.RUnlock()
	if us.lifetime <= 0 {
		return SessionDuration
	}
	return us.lifetime
}

// Initialize loads users and sessions from database.
// Must be called before the server starts accepting requests.
func (us *UserStore) Initialize() error {
	if us.db == nil {
		return errors.New("database required for user store initialization")
	}

	us.mu.Lock()
	defer us.mu.Unlock()

	repo := database.NewRepository(us.db)

	// Load users from database
	dbUsers, err := repo.ListUsers()
	if err != nil {
		return fmt.Errorf("failed to load users from database: %w", err)
	}

	// Convert database users to UserStore format
	for _, dbUser := range dbUsers {
		email := ""
		if dbUser.Email.Valid {
			email = dbUser.Email.String
		}
		oauthProviderID := ""
		if dbUser.OAuthProviderID.Valid {
			oauthProviderID = dbUser.OAuthProviderID.String
		}
		oauthProvider := ""
		if dbUser.OAuthProvider.Valid {
			oauthProvider = dbUser.OAuthProvider.String
		}
		var avatarID *int64
		if dbUser.AvatarID.Valid {
			avatarID = &dbUser.AvatarID.Int64
		}

		us.users[dbUser.Username] = &User{
			Username:        dbUser.Username,
			PasswordHash:    dbUser.PasswordHash,
			Email:           email,
			AuthMethod:      dbUser.AuthMethod,
			OAuthProviderID: oauthProviderID,
			OAuthProvider:   oauthProvider,
			CreatedAt:       dbUser.CreatedAt,
			UpdatedAt:       dbUser.UpdatedAt,
			IsAdmin:         dbUser.IsAdmin,
			AvatarID:        avatarID,
		}
	}

	// Load valid (non-expired) sessions from database
	dbSessions, err := repo.ListValidSessions()
	if err != nil {
		// Log but don't fail on session load error
		fmt.Printf("Warning: failed to load sessions from database: %v\n", err)
	} else {
		for _, dbSession := range dbSessions {
			// Only load sessions that haven't expired
			if time.Now().Before(dbSession.ExpiresAt) {
				// dbSession.Token is already the hash (migration 49).
				us.sessions[dbSession.Token] = &Session{
					TokenHash:   dbSession.Token,
					Username:    dbSession.Username,
					ExpiresAt:   dbSession.ExpiresAt,
					CreatedAt:   dbSession.CreatedAt,
					SessionID:   dbSession.SessionID,
					DeviceName:  dbSession.DeviceName,
					DeviceModel: dbSession.DeviceModel,
					CreatedVia:  dbSession.CreatedVia,
					LastUsedAt:  dbSession.LastUsedAt,
					MintedBy:    dbSession.MintedBySession,
				}
			}
		}
	}

	// Cleanup expired sessions
	if err := repo.CleanupExpiredSessions(); err != nil {
		// Log but don't fail on cleanup error
		fmt.Printf("Warning: failed to cleanup expired sessions: %v\n", err)
	}

	return nil
}

// HasUsers returns true if any users exist
func (us *UserStore) HasUsers() bool {
	us.mu.RLock()
	defer us.mu.RUnlock()
	return len(us.users) > 0
}

// UserExists returns true if a user with the given username exists (thread-safe)
func (us *UserStore) UserExists(username string) bool {
	us.mu.RLock()
	defer us.mu.RUnlock()
	return us.users[username] != nil
}

// SetDatabase attaches a database to the UserStore for dual-write operations
func (us *UserStore) SetDatabase(db *database.Database) {
	us.db = db
}

// ValidateUsername checks if a username meets format requirements
func ValidateUsername(username string) error {
	if username == "" {
		return errors.New("username cannot be empty")
	}
	if !usernameRegex.MatchString(username) {
		return errors.New("username must be 1-64 characters and contain only letters, numbers, underscores, or hyphens")
	}
	return nil
}

// CreateUser creates a new user account
func (us *UserStore) CreateUser(username, password string, isAdmin bool) error {
	// Validate username format
	if err := ValidateUsername(username); err != nil {
		return err
	}

	// Validate password strength
	if len(password) < 8 {
		return errors.New("password must be at least 8 characters")
	}

	// Hash password (done outside lock since bcrypt is CPU-intensive)
	passwordHash, err := bcrypt.GenerateFromPassword([]byte(password), PasswordHashCost)
	if err != nil {
		return fmt.Errorf("failed to hash password: %w", err)
	}

	us.mu.Lock()
	defer us.mu.Unlock()

	// Check if user already exists (inside lock to prevent race condition)
	if _, exists := us.users[username]; exists {
		return errors.New("user already exists")
	}

	// Create user
	user := &User{
		Username:     username,
		PasswordHash: string(passwordHash),
		AuthMethod:   "password",
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
		IsAdmin:      isAdmin,
	}

	us.users[username] = user

	// Save to database
	repo := database.NewRepository(us.db)
	dbUser := &database.DBUser{
		Username:     user.Username,
		PasswordHash: user.PasswordHash,
		AuthMethod:   user.AuthMethod,
		IsAdmin:      user.IsAdmin,
		CreatedAt:    user.CreatedAt,
		UpdatedAt:    user.UpdatedAt,
	}
	if err := repo.CreateUser(dbUser); err != nil {
		// Roll back in-memory change
		delete(us.users, username)
		return fmt.Errorf("failed to save user to database: %w", err)
	}

	return nil
}

// bcryptCompare is bcrypt.CompareHashAndPassword; tests replace it to see
// that unknown users cost a comparison too.
var bcryptCompare = bcrypt.CompareHashAndPassword

// bcryptSlots bounds how many bcrypt comparisons (password logins, MFA
// backup codes) run at once, so a burst of guesses cannot take every CPU.
var bcryptSlots = make(chan struct{}, max(2, runtime.NumCPU()/2))

// compareHashLimited runs bcryptCompare in one of the bcryptSlots.
func compareHashLimited(hash, secret []byte) error {
	bcryptSlots <- struct{}{}
	defer func() { <-bcryptSlots }()
	return bcryptCompare(hash, secret)
}

var (
	dummyHashOnce sync.Once
	dummyHash     []byte
)

// dummyPasswordHash is a bcrypt hash of a random password with the real
// cost. Unknown users (and accounts without a password) are compared against
// it so their login takes as long as a wrong password for a real user, and
// timing does not reveal which usernames exist.
func dummyPasswordHash() []byte {
	dummyHashOnce.Do(func() {
		b := make([]byte, 24)
		_, _ = rand.Read(b)
		h, err := bcrypt.GenerateFromPassword([]byte(hex.EncodeToString(b)), PasswordHashCost)
		if err != nil {
			// Unreachable in practice; any well-formed hash of the same cost
			// keeps the timing.
			h = []byte("$2a$12$C6UzMDM.H6dfI/f/IKcEeO5n0q3bVGxAA6jGW6HfD4m9DMd7lZ9ZO")
		}
		dummyHash = h
	})
	return dummyHash
}

// VerifyPassword verifies username and password WITHOUT creating a session token.
// This is used during MFA flow to verify credentials before MFA verification.
// Returns the User object if credentials are valid, error otherwise.
//
// SECURITY: an unknown user, or one without a password (OAuth only), still
// costs one bcrypt comparison against a dummy hash, so the response time does
// not tell which usernames exist.
func (us *UserStore) VerifyPassword(username, password string) (*User, error) {
	us.mu.RLock()
	user, exists := us.users[username]
	us.mu.RUnlock()

	if !exists || user.PasswordHash == "" {
		_ = compareHashLimited(dummyPasswordHash(), []byte(password))
		return nil, errors.New("invalid username or password")
	}

	// Verify password (outside lock since bcrypt is CPU-intensive)
	if err := compareHashLimited([]byte(user.PasswordHash), []byte(password)); err != nil {
		return nil, errors.New("invalid username or password")
	}

	return user, nil
}

// Authenticate verifies username and password, returns session token on success
func (us *UserStore) Authenticate(username, password string) (string, error) {
	// Verify password first (acquires RLock internally)
	user, err := us.VerifyPassword(username, password)
	if err != nil {
		return "", err
	}

	// Generate session token
	token, err := us.generateSessionToken()
	if err != nil {
		return "", fmt.Errorf("failed to generate session token: %w", err)
	}

	// Create session
	session := &Session{
		TokenHash:  database.HashToken(token),
		Username:   user.Username,
		ExpiresAt:  time.Now().Add(us.SessionLifetime()),
		CreatedAt:  time.Now(),
		SessionID:  newSessionID(),
		CreatedVia: "password",
	}

	us.mu.Lock()
	us.sessions[session.TokenHash] = session
	us.mu.Unlock()

	// Save to database
	repo := database.NewRepository(us.db)
	dbSession := &database.DBSession{
		Token:      session.TokenHash,
		Username:   session.Username,
		ExpiresAt:  session.ExpiresAt,
		CreatedAt:  session.CreatedAt,
		SessionID:  session.SessionID,
		CreatedVia: session.CreatedVia,
	}
	if err := repo.CreateSession(dbSession); err != nil {
		return "", fmt.Errorf("failed to save session to database: %w", err)
	}

	return token, nil
}

// errMFAPending is returned by ValidateSession for a session whose MFA step
// is not done yet.
var errMFAPending = errors.New("MFA verification required")

// ValidateSession checks that a session token is valid AND fully signed in.
//
// SECURITY: an MFA-pending session (OAuth login of a user with MFA, before
// the code was entered) is refused with errMFAPending. Only the REST auth
// middleware and /api/auth/status need to see such a session; they use
// validateSessionAllowPending.
func (us *UserStore) ValidateSession(token string) (*User, error) {
	user, sess, err := us.validateSessionAllowPending(token)
	if err != nil {
		return nil, err
	}
	if sess.MFARequired && !sess.MFAVerified {
		return nil, errMFAPending
	}
	return user, nil
}

// validateSessionAllowPending returns the user and session of a valid,
// unexpired token, including MFA-pending sessions. Callers must check
// sess.MFARequired && !sess.MFAVerified themselves; use ValidateSession
// unless the MFA-pending state is exactly what you need to look at.
func (us *UserStore) validateSessionAllowPending(token string) (*User, *Session, error) {
	if token == "" {
		return nil, nil, errors.New("invalid session")
	}
	key := database.HashToken(token)

	us.mu.RLock()
	session, exists := us.sessions[key]
	us.mu.RUnlock()

	if !exists {
		return nil, nil, errors.New("invalid session")
	}

	// Check expiration. A session issued under a longer lifetime than the
	// configured one ends when the configured lifetime runs out.
	now := time.Now()
	if now.After(session.ExpiresAt) || (!session.CreatedAt.IsZero() && now.After(session.CreatedAt.Add(us.SessionLifetime()))) {
		us.mu.Lock()
		delete(us.sessions, key)
		us.mu.Unlock()
		return nil, nil, errors.New("session expired")
	}

	us.mu.RLock()
	user, exists := us.users[session.Username]
	us.mu.RUnlock()

	if !exists {
		return nil, nil, errors.New("user not found")
	}

	if !session.MFARequired || session.MFAVerified {
		us.touchSession(key)
	}
	return user, session, nil
}

// revokeMFAPendingSessions removes the MFA-pending sessions created for an
// MFA temporary token, once that token was used (or given up).
func (us *UserStore) revokeMFAPendingSessions(mfaTempToken string) int {
	if mfaTempToken == "" {
		return 0
	}
	n := 0
	us.mu.Lock()
	for key, sess := range us.sessions {
		if sess.MFARequired && !sess.MFAVerified &&
			subtle.ConstantTimeCompare([]byte(sess.MFATempToken), []byte(mfaTempToken)) == 1 {
			delete(us.sessions, key)
			n++
		}
	}
	us.mu.Unlock()
	return n
}

// RevokeSession removes a session
func (us *UserStore) RevokeSession(token string) error {
	us.mintMu.Lock()
	defer us.mintMu.Unlock()
	key := database.HashToken(token)
	us.mu.Lock()
	minter := ""
	if sess := us.sessions[key]; sess != nil {
		minter = sess.SessionID
	}
	delete(us.sessions, key)
	us.mu.Unlock()
	repo := database.NewRepository(us.db)
	err := repo.DeleteSession(key)
	us.revokeMinted(minter)
	return err
}

// UpdatePassword changes a user's password
func (us *UserStore) UpdatePassword(username, oldPassword, newPassword string) error {
	us.mu.RLock()
	user, exists := us.users[username]
	us.mu.RUnlock()

	if !exists {
		return errors.New("user not found")
	}

	// Verify old password (outside lock, bcrypt is CPU-intensive)
	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(oldPassword)); err != nil {
		return errors.New("invalid current password")
	}

	// Validate new password
	if len(newPassword) < 8 {
		return errors.New("new password must be at least 8 characters")
	}

	// Hash new password (outside lock, bcrypt is CPU-intensive)
	passwordHash, err := bcrypt.GenerateFromPassword([]byte(newPassword), PasswordHashCost)
	if err != nil {
		return fmt.Errorf("failed to hash password: %w", err)
	}

	// Update user and revoke all other sessions
	us.mu.Lock()
	user.PasswordHash = string(passwordHash)
	user.UpdatedAt = time.Now()

	// SECURITY: Invalidate all sessions for this user after password change
	// This ensures a stolen session token cannot be used after the password is changed
	var tokensToRevoke []string
	for token, session := range us.sessions {
		if session.Username == username {
			tokensToRevoke = append(tokensToRevoke, token)
		}
	}
	for _, token := range tokensToRevoke {
		delete(us.sessions, token)
	}
	us.mu.Unlock()

	// Save to database
	repo := database.NewRepository(us.db)
	if err := repo.UpdateUserPassword(user.Username, user.PasswordHash); err != nil {
		return err
	}
	// Also revoke sessions in database
	for _, token := range tokensToRevoke {
		_ = repo.DeleteSession(token)
	}
	return nil
}

// DeleteUser removes a user account
func (us *UserStore) DeleteUser(username string) error {
	us.mu.Lock()
	delete(us.users, username)
	// SECURITY: Also revoke all sessions belonging to this user
	for token, session := range us.sessions {
		if session.Username == username {
			delete(us.sessions, token)
		}
	}
	us.mu.Unlock()

	repo := database.NewRepository(us.db)
	return repo.DeleteUser(username)
}

// ListUsers returns all usernames
func (us *UserStore) ListUsers() []string {
	us.mu.RLock()
	defer us.mu.RUnlock()
	usernames := make([]string, 0, len(us.users))
	for username := range us.users {
		usernames = append(usernames, username)
	}
	return usernames
}

// UserInfo represents basic user info for the admin user list
type UserInfo struct {
	Username string `json:"username"`
	IsAdmin  bool   `json:"is_admin"`
}

// ListUsersDetailed returns all users with their admin status
func (us *UserStore) ListUsersDetailed() []UserInfo {
	us.mu.RLock()
	defer us.mu.RUnlock()
	users := make([]UserInfo, 0, len(us.users))
	for _, u := range us.users {
		users = append(users, UserInfo{
			Username: u.Username,
			IsAdmin:  u.IsAdmin,
		})
	}
	return users
}

// SetUserAdmin updates the admin status of a user
func (us *UserStore) SetUserAdmin(username string, isAdmin bool) error {
	us.mu.Lock()
	defer us.mu.Unlock()

	user, exists := us.users[username]
	if !exists {
		return errors.New("user not found")
	}

	oldIsAdmin := user.IsAdmin
	user.IsAdmin = isAdmin

	// Persist to database
	if us.db != nil {
		repo := database.NewRepository(us.db)
		if err := repo.SetUserAdmin(username, isAdmin); err != nil {
			// Revert in-memory change on DB failure
			user.IsAdmin = oldIsAdmin
			log.Printf("AUDIT: Failed to update admin status for user %q: %v", username, err)
			return errors.New("failed to update user")
		}
	}

	log.Printf("AUDIT: User %q admin status changed from %v to %v", username, oldIsAdmin, isAdmin)

	// SECURITY: Invalidate all sessions for this user after privilege change
	// to force re-authentication with updated permissions
	var tokensToRevoke []string
	for token, session := range us.sessions {
		if session.Username == username {
			tokensToRevoke = append(tokensToRevoke, token)
		}
	}
	for _, token := range tokensToRevoke {
		delete(us.sessions, token)
	}

	// Also revoke sessions in database
	if us.db != nil && len(tokensToRevoke) > 0 {
		repo := database.NewRepository(us.db)
		for _, token := range tokensToRevoke {
			_ = repo.DeleteSession(token)
		}
	}

	return nil
}

// generateSessionToken generates a random session token
func (us *UserStore) generateSessionToken() (string, error) {
	bytes := make([]byte, 32) // 256 bits
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes), nil
}

// maxSessionsPerUser is the maximum number of concurrent sessions allowed per user.
// Each browser, desktop window, iPhone and paired Mac holds one, and a login
// over the cap signs out another device, so the cap only stops runaway session
// creation. It used to be 5: a few logins (or a sign-in loop) signed out the
// iPhone and the other Macs' pairings.
const maxSessionsPerUser = 50

// evictionOrder sorts a user's sessions for eviction: MFA-pending ones first
// (a half-finished login), then the least recently used.
func evictionOrder(sessions []*Session) {
	lastActive := func(s *Session) time.Time {
		if s.LastUsedAt != nil && s.LastUsedAt.After(s.CreatedAt) {
			return *s.LastUsedAt
		}
		return s.CreatedAt
	}
	sort.SliceStable(sessions, func(i, j int) bool {
		pi := sessions[i].MFARequired && !sessions[i].MFAVerified
		pj := sessions[j].MFARequired && !sessions[j].MFAVerified
		if pi != pj {
			return pi
		}
		return lastActive(sessions[i]).Before(lastActive(sessions[j]))
	})
}

// generateSessionTokenForUser creates a session token for a specific username (used after MFA verification).
// Allows multiple concurrent sessions (up to maxSessionsPerUser) so users can be logged in
// on multiple devices simultaneously. When the limit is exceeded, the oldest sessions are revoked.
func (us *UserStore) generateSessionTokenForUser(username string) (string, error) {
	return us.createUserSession(username, SessionMeta{CreatedVia: "password"})
}

// createUserSession is generateSessionTokenForUser with device metadata.
func (us *UserStore) createUserSession(username string, meta SessionMeta) (string, error) {
	us.mu.RLock()
	_, exists := us.users[username]
	us.mu.RUnlock()

	if !exists {
		return "", fmt.Errorf("user not found: %s", username)
	}

	// Enforce max concurrent sessions per user: at the limit, sign out the
	// least recently used session(s) (half-finished MFA logins first).
	us.mu.Lock()
	var userSessions []*Session
	for _, session := range us.sessions {
		if session.Username == username {
			userSessions = append(userSessions, session)
		}
	}

	var oldTokens []string
	if len(userSessions) >= maxSessionsPerUser {
		evictionOrder(userSessions)
		toRemove := len(userSessions) - maxSessionsPerUser + 1
		for i := 0; i < toRemove; i++ {
			key := userSessions[i].TokenHash
			oldTokens = append(oldTokens, key)
			delete(us.sessions, key)
		}
		log.Printf("AUDIT: user %q is at %d sessions; signed out %d least recently used", username, maxSessionsPerUser, toRemove)
	}
	us.mu.Unlock()

	repo := database.NewRepository(us.db)

	// Clean up evicted sessions from database
	for _, token := range oldTokens {
		_ = repo.DeleteSession(token)
	}

	// Generate session token
	token, err := us.generateSessionToken()
	if err != nil {
		return "", fmt.Errorf("failed to generate session token: %w", err)
	}

	// Create session
	session := &Session{
		TokenHash:   database.HashToken(token),
		Username:    username,
		ExpiresAt:   time.Now().Add(us.SessionLifetime()),
		CreatedAt:   time.Now(),
		SessionID:   newSessionID(),
		DeviceName:  meta.DeviceName,
		DeviceModel: meta.DeviceModel,
		CreatedVia:  meta.CreatedVia,
		MintedBy:    meta.MintedBy,
	}

	us.mu.Lock()
	us.sessions[session.TokenHash] = session
	us.mu.Unlock()

	// Save to database
	dbSession := &database.DBSession{
		Token:       session.TokenHash,
		Username:    session.Username,
		ExpiresAt:   session.ExpiresAt,
		CreatedAt:   session.CreatedAt,
		SessionID:   session.SessionID,
		DeviceName:  session.DeviceName,
		DeviceModel: session.DeviceModel,
		CreatedVia:  session.CreatedVia,

		MintedBySession: session.MintedBy,
	}
	if err := repo.CreateSession(dbSession); err != nil {
		return "", fmt.Errorf("failed to save session to database: %w", err)
	}

	return token, nil
}

// GetSession retrieves a session by token
func (us *UserStore) GetSession(token string) *Session {
	if token == "" {
		return nil
	}
	us.mu.RLock()
	defer us.mu.RUnlock()
	return us.sessions[database.HashToken(token)]
}

// createMFAPendingSession creates a session that requires MFA verification
// This is used when a user logs in via OAuth but has MFA enabled
func (us *UserStore) createMFAPendingSession(username, mfaTempToken string) (string, error) {
	us.mu.RLock()
	_, exists := us.users[username]
	us.mu.RUnlock()

	if !exists {
		return "", fmt.Errorf("user not found: %s", username)
	}

	// Generate session token
	token, err := us.generateSessionToken()
	if err != nil {
		return "", fmt.Errorf("failed to generate session token: %w", err)
	}

	// Create session with MFA pending
	session := &Session{
		TokenHash:    database.HashToken(token),
		Username:     username,
		ExpiresAt:    time.Now().Add(5 * time.Minute), // Shorter expiry for MFA pending
		CreatedAt:    time.Now(),
		MFARequired:  true,         // User must complete MFA
		MFAVerified:  false,        // Not yet verified
		MFATempToken: mfaTempToken, // Store temp token for MFA verification
	}

	us.mu.Lock()
	us.sessions[session.TokenHash] = session
	us.mu.Unlock()

	// Note: We don't save MFA-pending sessions to database
	// They expire quickly and are only in-memory
	// After MFA verification, a full session will be created and saved

	return token, nil
}

// CreateOAuthUser creates a new OAuth user account
// username can be generated from email or provided explicitly
// isAdmin determines if user should be created as admin
func (us *UserStore) CreateOAuthUser(username, email, oauthProvider, oauthProviderID string, isAdmin bool) error {
	// Validate username
	if username == "" {
		return errors.New("username cannot be empty")
	}

	// Validate email
	if email == "" {
		return errors.New("email cannot be empty")
	}

	us.mu.Lock()
	defer us.mu.Unlock()

	// Check if user already exists (inside lock)
	if _, exists := us.users[username]; exists {
		return errors.New("user already exists")
	}

	// Create OAuth user (no password needed for OAuth)
	user := &User{
		Username:        username,
		Email:           email,
		AuthMethod:      "oauth",
		OAuthProvider:   oauthProvider,
		OAuthProviderID: oauthProviderID,
		CreatedAt:       time.Now(),
		UpdatedAt:       time.Now(),
		IsAdmin:         isAdmin,
	}

	us.users[username] = user

	// Save to database
	repo := database.NewRepository(us.db)
	dbUser := &database.DBUser{
		Username:        user.Username,
		Email:           sql.NullString{String: user.Email, Valid: user.Email != ""},
		AuthMethod:      user.AuthMethod,
		OAuthProvider:   sql.NullString{String: user.OAuthProvider, Valid: user.OAuthProvider != ""},
		OAuthProviderID: sql.NullString{String: user.OAuthProviderID, Valid: user.OAuthProviderID != ""},
		IsAdmin:         user.IsAdmin,
		CreatedAt:       user.CreatedAt,
		UpdatedAt:       user.UpdatedAt,
	}
	if err := repo.CreateUser(dbUser); err != nil {
		delete(us.users, username) // Roll back
		return fmt.Errorf("failed to save OAuth user to database: %w", err)
	}

	return nil
}

// FindUserByOAuthID finds a user by OAuth provider and provider ID
func (us *UserStore) FindUserByOAuthID(oauthProvider, oauthProviderID string) *User {
	us.mu.RLock()
	defer us.mu.RUnlock()
	for _, user := range us.users {
		if user.OAuthProvider == oauthProvider && user.OAuthProviderID == oauthProviderID {
			return user
		}
	}
	return nil
}

// FindUserByEmail finds a user by email address
func (us *UserStore) FindUserByEmail(email string) *User {
	us.mu.RLock()
	defer us.mu.RUnlock()
	for _, user := range us.users {
		if user.Email == email {
			return user
		}
	}
	return nil
}

// ValidateAPIKey validates an API key against the stored secret
func (us *UserStore) ValidateAPIKey(token string) bool {
	// Return false if token is empty
	if token == "" {
		return false
	}

	// Use cached API key if available
	if us.apiKey != "" {
		return subtle.ConstantTimeCompare([]byte(token), []byte(us.apiKey)) == 1
	}

	// Try to load API key from file
	secretFile := filepath.Join(us.configDir, ".secret")
	data, err := os.ReadFile(secretFile)
	if err != nil {
		return false
	}

	us.apiKey = strings.TrimSpace(string(data))
	return subtle.ConstantTimeCompare([]byte(token), []byte(us.apiKey)) == 1
}
