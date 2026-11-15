package server

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/pquerna/otp/totp"
	"github.com/schlunsen/eggify/internal/database"
)

// TestMFASetup_ShownBackupCodesWork: setup showed one set of backup codes and
// verify stored a different one it never returned, so the codes users saved
// didn't work (a lost authenticator meant a locked account).
func TestMFASetup_ShownBackupCodesWork(t *testing.T) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	mgr, err := NewMFAManager(key)
	if err != nil {
		t.Fatal(err)
	}
	db := database.NewTestDB(t)
	if _, err := db.GetDB().Exec(`INSERT INTO users (username, password_hash, is_admin) VALUES ('alice', 'x', 1)`); err != nil {
		t.Logf("users insert (may not be needed): %v", err)
	}
	s := &Server{db: db, mfaManager: mgr}
	app := fiber.New()
	app.Use(func(c *fiber.Ctx) error { c.Locals("user", &User{Username: "alice"}); return c.Next() })
	app.Post("/setup", s.handleMFASetupStart)
	app.Post("/verify", s.handleMFASetupVerify)

	resp, err := app.Test(httptest.NewRequest("POST", "/setup", nil), -1)
	if err != nil {
		t.Fatal(err)
	}
	var setup database.TOTPSetupResponse
	if err := json.NewDecoder(resp.Body).Decode(&setup); err != nil || resp.StatusCode != 200 {
		t.Fatalf("setup: %d %v", resp.StatusCode, err)
	}
	if len(setup.BackupCodes) == 0 {
		t.Fatal("setup showed no backup codes")
	}

	code, err := totp.GenerateCode(setup.Secret, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]string{"code": code, "temporary_token": setup.TemporaryToken})
	req := httptest.NewRequest("POST", "/verify", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err = app.Test(req, -1)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("verify: %v %v", resp.StatusCode, err)
	}

	cfg, err := database.NewRepository(db).GetMFAConfig("alice")
	if err != nil || cfg == nil || !cfg.IsEnabled {
		t.Fatalf("MFA not enabled: %+v %v", cfg, err)
	}
	coll, err := mgr.DeserializeBackupCodes(cfg.BackupCodes.String)
	if err != nil {
		t.Fatal(err)
	}
	if ok, _, _ := mgr.VerifyAndUseBackupCode(coll, setup.BackupCodes[3]); !ok {
		t.Fatal("a backup code shown during setup doesn't work after setup")
	}
}
