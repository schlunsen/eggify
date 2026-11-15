package database

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func newAvatarSeedDB(t *testing.T) *Database {
	t.Helper()
	ResetInstance()
	db, err := Initialize(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close(); ResetInstance() })
	return db
}

// avatarSnapshot maps theme name -> its avatars, for comparing runs.
func avatarSnapshot(t *testing.T, repo *Repository) map[string][]*Avatar {
	t.Helper()
	themes, err := repo.GetAvatarThemes()
	if err != nil {
		t.Fatal(err)
	}
	out := make(map[string][]*Avatar, len(themes))
	for _, th := range themes {
		if _, dup := out[th.Name]; dup {
			t.Fatalf("theme %q exists twice", th.Name)
		}
		avatars, err := repo.GetThemeAvatars(th.ID)
		if err != nil {
			t.Fatal(err)
		}
		out[th.Name] = avatars
	}
	return out
}

func checkEggTheme(t *testing.T, avatars []*Avatar) {
	t.Helper()
	if len(avatars) != len(eggCharacters) || len(avatars) != 13 {
		t.Fatalf("want 13 egg avatars, got %d", len(avatars))
	}
	paths := map[string]bool{}
	for _, a := range avatars {
		paths[a.ImagePath] = true
	}
	for _, ch := range eggCharacters {
		want := "/avatars/eggs/egg-" + ch.slug + ".jpg"
		if !paths[want] {
			t.Errorf("missing egg avatar %s", want)
		}
	}
}

func TestSeedAvatarThemesFreshDB(t *testing.T) {
	db := newAvatarSeedDB(t)
	if err := db.SeedAvatarThemes(); err != nil {
		t.Fatal(err)
	}
	snap := avatarSnapshot(t, NewRepository(db))
	if len(snap) != 4 {
		t.Fatalf("want 4 themes, got %d", len(snap))
	}
	for _, name := range []string{"Neon Cats", "Dali Cats", "Line Cats"} {
		if len(snap[name]) != 12 {
			t.Errorf("%s: want 12 avatars, got %d", name, len(snap[name]))
		}
		for _, a := range snap[name] {
			if !strings.HasPrefix(a.ImagePath, "/avatars/cats/cat-") {
				t.Errorf("%s: unexpected image path %s", name, a.ImagePath)
			}
		}
	}
	checkEggTheme(t, snap["Eggify Eggs"])
}

func TestSeedAvatarThemesAddsEggsToExistingCats(t *testing.T) {
	db := newAvatarSeedDB(t)
	repo := NewRepository(db)
	// An install from before the egg theme: only the cat themes.
	for _, th := range builtinAvatarThemes[:3] {
		if err := db.seedBuiltinTheme(repo, th); err != nil {
			t.Fatal(err)
		}
	}
	before := avatarSnapshot(t, repo)
	if _, ok := before["Eggify Eggs"]; ok || len(before) != 3 {
		t.Fatalf("setup: want only the 3 cat themes, got %d", len(before))
	}

	if err := db.SeedAvatarThemes(); err != nil {
		t.Fatal(err)
	}
	after := avatarSnapshot(t, repo)
	if len(after) != 4 {
		t.Fatalf("want 4 themes, got %d", len(after))
	}
	checkEggTheme(t, after["Eggify Eggs"])
	for name, cats := range before {
		got := after[name]
		if len(got) != len(cats) {
			t.Fatalf("%s: avatar count changed %d -> %d", name, len(cats), len(got))
		}
		for i := range cats {
			if got[i].ID != cats[i].ID || got[i].Name != cats[i].Name || got[i].ImagePath != cats[i].ImagePath {
				t.Errorf("%s: avatar %d changed: %+v -> %+v", name, i, cats[i], got[i])
			}
		}
	}

	// A second run adds nothing.
	if err := db.SeedAvatarThemes(); err != nil {
		t.Fatal(err)
	}
	again := avatarSnapshot(t, repo)
	if len(again) != 4 {
		t.Fatalf("second run: want 4 themes, got %d", len(again))
	}
	for name, avatars := range after {
		if len(again[name]) != len(avatars) {
			t.Errorf("second run: %s went from %d to %d avatars", name, len(avatars), len(again[name]))
		}
		for i := range avatars {
			if again[name][i].ID != avatars[i].ID {
				t.Errorf("second run: %s avatar %d re-created", name, i)
			}
		}
	}
}

// Every seeded image path must exist in the frontend's public folder, since
// the binary embeds it.
func TestBuiltinAvatarImagesExist(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	public := filepath.Join(filepath.Dir(file), "..", "server", "frontend", "public")
	for _, th := range builtinAvatarThemes {
		for _, ch := range th.characters {
			p := builtinAvatarImagePath(th, ch.slug)
			if _, err := os.Stat(filepath.Join(public, filepath.FromSlash(p))); err != nil {
				t.Errorf("%s: %v", th.name, err)
			}
		}
	}
}

// Themes users made (AI-generated, not builtin) survive seeding; retired
// builtin themes are still removed.
func TestSeedAvatarThemesKeepsUserThemes(t *testing.T) {
	db := newAvatarSeedDB(t)
	repo := NewRepository(db)
	user := &AvatarTheme{Name: "AI Generated", Description: "from a prompt", IsBuiltin: false}
	retired := &AvatarTheme{Name: "Old Builtin Cats", Description: "retired", IsBuiltin: true}
	for _, th := range []*AvatarTheme{user, retired} {
		if err := repo.CreateAvatarTheme(th); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 2; i++ {
		if err := db.SeedAvatarThemes(); err != nil {
			t.Fatal(err)
		}
	}
	themes, err := repo.GetAvatarThemes()
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, th := range themes {
		names[th.Name] = true
	}
	if !names["AI Generated"] {
		t.Error("user-made theme was deleted by seeding")
	}
	if names["Old Builtin Cats"] {
		t.Error("retired builtin theme was kept")
	}
	if len(themes) != len(builtinAvatarThemes)+1 {
		t.Errorf("got %d themes, want %d builtin + 1 user", len(themes), len(builtinAvatarThemes))
	}
}

// Deleting a theme clears the sessions and users that picked one of its
// avatars (users.avatar_id; the query used a column that doesn't exist, so
// every delete failed).
func TestDeleteAvatarThemeClearsReferences(t *testing.T) {
	db := newAvatarSeedDB(t)
	repo := NewRepository(db)
	theme := &AvatarTheme{Name: "AI Generated", IsBuiltin: false}
	if err := repo.CreateAvatarTheme(theme); err != nil {
		t.Fatal(err)
	}
	res, err := db.db.Exec(`INSERT INTO avatars (theme_id, name, image_path) VALUES (?, 'Bot', '/x.png')`, theme.ID)
	if err != nil {
		t.Fatal(err)
	}
	avatarID, _ := res.LastInsertId()
	if _, err := db.db.Exec(`INSERT INTO users (id, username, password_hash, avatar_id) VALUES ('u1', 'alice', 'x', ?)`, avatarID); err != nil {
		t.Fatal(err)
	}
	if err := repo.DeleteAvatarTheme(theme.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	var ref *int64
	if err := db.db.QueryRow(`SELECT avatar_id FROM users WHERE id = 'u1'`).Scan(&ref); err != nil {
		t.Fatal(err)
	}
	if ref != nil {
		t.Fatalf("user still points at deleted avatar %d", *ref)
	}
}
