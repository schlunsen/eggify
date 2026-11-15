// Package principal records who is behind a request or WebSocket connection
// and enforces that privileged actions come from a signed-in user.
//
// Threat model: agents run as the same OS user as the Eggify server, so they can
// read the API key (~/.claude/eggify/.secret) and call every endpoint with it.
// The API key therefore only proves "some local process", never "the human".
// Privileged actions (approving permissions, YOLO, always-allow rules,
// provider keys, MCP servers, the terminal, ...) need a USER principal: a
// valid session token from the HttpOnly session_token cookie, an
// Authorization: Bearer <session token> header (iOS), or a session token sent
// as the WebSocket auth token. Session tokens are stored only as hashes, so
// an agent reading eggify.db cannot obtain one.
//
// With more than one user account, privileged actions need an ADMIN user: a
// non-admin user is not the owner of the machine, but everything privileged
// (the terminal, YOLO, unsandboxed sessions, hooks, MCP servers, providers,
// remote access, ...) acts as the server's OS user. With a single account
// that user is the owner and counts as admin. Ordinary user-level actions
// (pairing one's own phone, one's own signed-in devices, answering a
// permission request in one's own session) use the *SignedIn* helpers.
//
// When user authentication is disabled there are no user principals; then
// everything keeps working as before (Enforced() is false) and the server
// logs a warning at startup.
package principal

import (
	"strings"
	"sync/atomic"

	"github.com/gofiber/fiber/v2"
	"github.com/schlunsen/eggify/internal/logging"
)

// Kind is the kind of credential a request was authenticated with.
type Kind string

const (
	// None: no credential (auth disabled, or a public route).
	None Kind = ""
	// User: a signed-in user's session token.
	User Kind = "user"
	// APIKey: the server API key only. Any local process can read it.
	APIKey Kind = "api_key"
)

// Principal identifies the caller of a request.
type Principal struct {
	Kind     Kind
	Username string // set for User
}

// IsUser reports whether the caller is a signed-in user.
func (p Principal) IsUser() bool { return p.Kind == User && p.Username != "" }

const localsKey = "eggify_principal"

// Set stores the principal of a request in its fiber locals.
func Set(c *fiber.Ctx, p Principal) { c.Locals(localsKey, p) }

// From returns the principal of a request. It falls back to the locals the
// session middleware has always set ("username", "authenticated_via_api_key").
func From(c *fiber.Ctx) Principal {
	if p, ok := c.Locals(localsKey).(Principal); ok {
		return p
	}
	if name, ok := c.Locals("username").(string); ok && name != "" {
		return Principal{Kind: User, Username: name}
	}
	if viaKey, _ := c.Locals("authenticated_via_api_key").(bool); viaKey {
		return Principal{Kind: APIKey}
	}
	return Principal{}
}

const sessionTokenHashKey = "eggify_session_token_hash"

// SetSessionTokenHash records the hash (database.HashToken) of the session
// token that made this request a User principal, wherever it arrived (cookie,
// Bearer header, ?token= on a WebSocket upgrade). The session middleware sets
// it together with the principal, so a socket opened by the request can be
// closed when that session is signed out.
func SetSessionTokenHash(c *fiber.Ctx, tokenHash string) { c.Locals(sessionTokenHashKey, tokenHash) }

// SessionTokenHash returns the hash recorded by SetSessionTokenHash, or "".
func SessionTokenHash(c *fiber.Ctx) string {
	hash, _ := c.Locals(sessionTokenHashKey).(string)
	return hash
}

var enforced atomic.Bool

// adminResolver decides whether a signed-in username may perform admin-level
// (privileged) actions. nil (tests, or before startup wiring) counts every
// signed-in user as admin, which is the single-user behaviour.
var adminResolver atomic.Pointer[func(username string) bool]

// SetAdminResolver installs the lookup used by IsAdmin. The server sets it at
// startup to "the user is an admin, or the only account". nil removes it.
func SetAdminResolver(fn func(username string) bool) {
	if fn == nil {
		adminResolver.Store(nil)
		return
	}
	adminResolver.Store(&fn)
}

// IsAdmin reports whether p is a signed-in user allowed to perform
// privileged actions.
func (p Principal) IsAdmin() bool {
	if !p.IsUser() {
		return false
	}
	fn := adminResolver.Load()
	if fn == nil {
		return true
	}
	return (*fn)(p.Username)
}

// SetEnforced turns the user-principal requirement on (user auth enabled) or
// off (user auth disabled: behave as before). Set once at server startup.
func SetEnforced(on bool) { enforced.Store(on) }

// Enforced reports whether privileged actions require a signed-in user.
func Enforced() bool { return enforced.Load() }

// Allowed reports whether p may perform a privileged action: a signed-in
// admin (or the only user) when user auth is on.
func Allowed(p Principal) bool { return !Enforced() || p.IsAdmin() }

// AllowedSignedIn reports whether p may perform a user-level action that only
// needs a human (any signed-in user), not an admin.
func AllowedSignedIn(p Principal) bool { return !Enforced() || p.IsUser() }

// ErrorCode is the machine-readable code of a "signed-in user required"
// refusal, on REST (JSON "code") and WebSocket ("code" of an error message).
const ErrorCode = "user_required"

// AdminErrorCode is the code of a refusal to a signed-in user who is not an
// admin while several accounts exist.
const AdminErrorCode = "admin_required"

// Message is the human-readable refusal for action, e.g.
// "Approving permissions requires a signed-in user".
func Message(action string) string {
	if action == "" {
		action = "This action"
	}
	return action + " requires a signed-in user"
}

// AdminMessage is the refusal for a non-admin user, e.g.
// "Changing providers requires an admin user".
func AdminMessage(action string) string {
	if action == "" {
		action = "This action"
	}
	return strings.TrimSuffix(action, ".") + " requires an admin user"
}

// Refusal returns the error code and message for p attempting a privileged
// action: admin_required for a signed-in non-admin, user_required otherwise.
func Refusal(p Principal, action string) (code, message string) {
	if p.IsUser() {
		return AdminErrorCode, AdminMessage(action)
	}
	return ErrorCode, Message(action)
}

// Forbidden answers 403 for a privileged action attempted without a user.
func Forbidden(c *fiber.Ctx, action string) error {
	return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
		"error": Message(action),
		"code":  ErrorCode,
	})
}

// ForbiddenFor answers 403 with the refusal that fits p (see Refusal).
func ForbiddenFor(c *fiber.Ctx, p Principal, action string) error {
	code, msg := Refusal(p, action)
	return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": msg, "code": code})
}

// Check returns (true, nil) when the request may perform the privileged
// action (a signed-in admin, or the only user); otherwise it has already
// written the 403 (user_required or admin_required) and returns (false, err)
// for the handler to return.
//
// With user auth disabled a non-user caller is allowed and a warning is
// logged, since nothing then tells the human from an agent.
func Check(c *fiber.Ctx, action string) (bool, error) {
	return check(c, action, Principal.IsAdmin)
}

// CheckSignedIn is Check for user-level actions: any signed-in user passes.
func CheckSignedIn(c *fiber.Ctx, action string) (bool, error) {
	return check(c, action, Principal.IsUser)
}

func check(c *fiber.Ctx, action string, ok func(Principal) bool) (bool, error) {
	p := From(c)
	if ok(p) {
		return true, nil
	}
	if !Enforced() {
		logging.Warning("SECURITY: %s (%s %s) allowed without a signed-in user because user authentication is disabled",
			Message(action), c.Method(), c.Path())
		return true, nil
	}
	code, msg := Refusal(p, action)
	logging.Warning("SECURITY: refused %s %s for %q: %s (%s)", c.Method(), c.Path(), p.Username, msg, code)
	return false, ForbiddenFor(c, p, action)
}

// Username returns the signed-in user of a request, or ("", false) when the
// caller is not a signed-in user (API key only, or no credential).
func Username(c *fiber.Ctx) (string, bool) {
	if p := From(c); p.IsUser() {
		return p.Username, true
	}
	return "", false
}

// FromConnUser builds the principal of a WebSocket connection from the user
// recorded on it (AgentHandler.GetConnectionUser), which is only ever set
// from a validated, MFA-complete session token. nil or "" means the
// connection authenticated with the API key only.
func FromConnUser(username *string) Principal {
	if username != nil && *username != "" {
		return Principal{Kind: User, Username: *username}
	}
	return Principal{Kind: APIKey}
}

// AllowedConnUser reports whether a WebSocket connection whose recorded user
// is username may perform a privileged action (admin, or the only user).
func AllowedConnUser(username *string) bool { return Allowed(FromConnUser(username)) }

// AllowedConnSignedIn reports whether the connection may perform a
// user-level action (any signed-in user).
func AllowedConnSignedIn(username *string) bool { return AllowedSignedIn(FromConnUser(username)) }

// RequireUser is route middleware for privileged routes: a signed-in admin
// (or the only user) when user auth is on. The name predates the admin role;
// a signed-in non-admin gets 403 admin_required.
func RequireUser(action string) fiber.Handler {
	return func(c *fiber.Ctx) error {
		if ok, err := Check(c, action); !ok {
			return err
		}
		return c.Next()
	}
}

// RequireAdmin is RequireUser under the name that says what it checks.
func RequireAdmin(action string) fiber.Handler { return RequireUser(action) }

// RequireSignedIn is route middleware for user-level routes that need a human
// but not an admin (one's own devices, pairing, continuing one's sessions).
func RequireSignedIn(action string) fiber.Handler {
	return func(c *fiber.Ctx) error {
		if ok, err := CheckSignedIn(c, action); !ok {
			return err
		}
		return c.Next()
	}
}
