package database

import (
	"fmt"
	"math/rand"
	"os"

	"github.com/google/uuid"
)

// sandboxAppDir is the workspace folder of sandbox instances.
var sandboxAppDir = "/app"

// SeedDefaultProject seeds a default "app" project at /app if no projects exist
// and /app is a folder. This ensures sandbox instances have a ready-to-use
// project on first boot, without giving desktop installs a broken project.
func (db *Database) SeedDefaultProject() error {
	if info, err := os.Stat(sandboxAppDir); err != nil || !info.IsDir() {
		return nil
	}
	projectRepo := NewProjectRepository(db)

	// Check if any projects already exist — don't overwrite user setup
	projects, err := projectRepo.GetAllProjects()
	if err != nil {
		return fmt.Errorf("failed to check existing projects: %w", err)
	}
	if len(projects) > 0 {
		return nil // User already has projects, skip seeding
	}

	project := &Project{
		ID:       uuid.New().String(),
		Name:     "app",
		Path:     sandboxAppDir,
		IsActive: true,
		Color:    "#8b5cf6",
	}

	if err := projectRepo.CreateProject(project); err != nil {
		return fmt.Errorf("failed to seed default project: %w", err)
	}

	fmt.Println("Seeded default project: app (/app)")
	return nil
}

// avatarCharacter is one preset avatar of a builtin theme. The slug is part of
// the image file name.
type avatarCharacter struct {
	slug  string
	name  string
	color string
}

// builtinAvatarTheme describes a builtin avatar theme. Its images live at
// /avatars/<dir>/<prefix>-<slug>.jpg in the frontend's public folder.
type builtinAvatarTheme struct {
	name        string
	description string
	dir         string
	prefix      string
	characters  []avatarCharacter
}

// catCharacters are shared by the three cat themes. They also back the
// deterministic fallback avatar in composables/useCharacterAvatar.ts.
var catCharacters = []avatarCharacter{
	{"coder", "Bubbles de Catt", "#85C1E2"},
	{"coffee", "Julian Purresso", "#F8B88B"},
	{"detective", "Inspector Lahisker", "#F9E79F"},
	{"dj", "J-Roc Meowski", "#BB8FCE"},
	{"hacker", "Cyrus Rootpaw", "#4ECDC4"},
	{"music", "Conky Catsworth", "#F1948A"},
	{"ninja", "Shadow Rickitty", "#45B7D1"},
	{"pirate", "Captain Shittpaw", "#FF6B6B"},
	{"rocket", "Ricky Lawnchpurr", "#FFA07A"},
	{"sleeping", "Randy Catnaps", "#D7BDE2"},
	{"wizard", "The Liquor Whiskers", "#98D8C8"},
	{"zen", "Ray Pawston", "#ABEBC6"},
}

// eggCharacters are the Eggify egg characters (hand-cut paper collage, like
// the Zen Circus art). They are selectable only; the fallback stays a cat.
var eggCharacters = []avatarCharacter{
	{"coder", "Eggbert Bracket", "#85A9C4"},
	{"coffee", "Brew Benedict", "#F3B99A"},
	{"detective", "Inspector Yolkes", "#E3C16F"},
	{"dj", "DJ Scramble", "#B7A8D4"},
	{"hacker", "Cyrus Shelldon", "#5FA3A0"},
	{"music", "Maestro Omelette", "#E4A3B0"},
	{"ninja", "Shadow Shelly", "#6C84A6"},
	{"pirate", "Captain Eggbeard", "#E07A6E"},
	{"rocket", "Commander Cosmo-Yolk", "#E9A15B"},
	{"sleeping", "Sunny Napside", "#C3B7D6"},
	{"wizard", "Merlin the Poached", "#9DB38C"},
	{"zen", "Ovo the Calm", "#9CC9B8"},
	{"gent", "Sir Egbert Eggsworth", "#7FB3AE"},
}

// builtinAvatarThemes are seeded on startup, in this order. A builtin theme in
// the database that is not listed here is removed by SeedAvatarThemes; themes
// users made are kept.
var builtinAvatarThemes = []builtinAvatarTheme{
	{"Neon Cats", "Neon cyberpunk cat avatars with glowing portraits", "cats", "cat", catCharacters},
	{"Dali Cats", "Surrealist Dali-inspired cat avatars", "cats", "cat-dali", catCharacters},
	{"Line Cats", "Minimal line art cat avatars", "cats", "cat-line", catCharacters},
	{"Eggify Eggs", "Hand-cut paper collage egg characters", "eggs", "egg", eggCharacters},
}

// SeedAvatarThemes seeds the builtin avatar themes (cats and eggs). A theme
// that already exists is left untouched, so running it again adds nothing and
// existing installs only gain the themes they lack (e.g. Eggify Eggs).
func (db *Database) SeedAvatarThemes() error {
	repo := NewRepository(db)

	themes, err := repo.GetAvatarThemes()
	if err != nil {
		return fmt.Errorf("failed to check existing themes: %w", err)
	}

	knownThemes := make(map[string]bool, len(builtinAvatarThemes))
	for _, t := range builtinAvatarThemes {
		knownThemes[t.name] = true
	}

	// Track existing builtin themes and remove retired ones. Themes users
	// made (AI-generated, is_builtin = 0) are never touched: they used to be
	// deleted on every start.
	existingThemes := make(map[string]bool)
	for _, theme := range themes {
		if !theme.IsBuiltin {
			continue
		}
		if !knownThemes[theme.Name] {
			fmt.Printf("Removing legacy avatar theme: %s\n", theme.Name)
			if err := repo.DeleteAvatarTheme(theme.ID); err != nil {
				fmt.Printf("Warning: failed to remove legacy theme %s: %v\n", theme.Name, err)
			}
			continue
		}
		existingThemes[theme.Name] = true
	}

	for _, t := range builtinAvatarThemes {
		if !existingThemes[t.name] {
			if err := db.seedBuiltinTheme(repo, t); err != nil {
				return err
			}
		}
	}

	return nil
}

// seedBuiltinTheme creates one builtin theme with its local image paths.
func (db *Database) seedBuiltinTheme(repo *Repository, t builtinAvatarTheme) error {
	theme := &AvatarTheme{
		Name:        t.name,
		Description: t.description,
		IsBuiltin:   true,
	}
	if err := repo.CreateAvatarTheme(theme); err != nil {
		return fmt.Errorf("failed to create %s theme: %w", t.name, err)
	}

	var avatarIDs []int64
	for _, ch := range t.characters {
		avatar := &Avatar{
			ThemeID:   theme.ID,
			Name:      ch.name,
			Type:      AvatarTypePreset,
			ImagePath: builtinAvatarImagePath(t, ch.slug),
			Color:     ch.color,
		}
		if err := repo.CreateAvatar(avatar); err != nil {
			fmt.Printf("Warning: failed to seed avatar %s: %v\n", avatar.Name, err)
			continue
		}
		avatarIDs = append(avatarIDs, avatar.ID)
	}

	// Set a random avatar as the representative for the theme
	if len(avatarIDs) > 0 {
		randomIndex := rand.Intn(len(avatarIDs))
		theme.RepresentativeAvatarID = &avatarIDs[randomIndex]
		if err := repo.UpdateAvatarTheme(theme); err != nil {
			fmt.Printf("Warning: failed to set representative avatar for %s theme: %v\n", t.name, err)
		}
	}

	return nil
}

// builtinAvatarImagePath is the public path of a builtin avatar image.
func builtinAvatarImagePath(t builtinAvatarTheme, slug string) string {
	return fmt.Sprintf("/avatars/%s/%s-%s.jpg", t.dir, t.prefix, slug)
}
