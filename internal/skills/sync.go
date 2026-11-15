package skills

import (
	"encoding/json"
	"path/filepath"

	"github.com/schlunsen/eggify/internal/database"
)

// SkippedSkill is a discovered file that was left alone, and why (its name
// belongs to another skill, or its skill is waiting for review).
type SkippedSkill struct {
	Path   string `json:"path"`
	Name   string `json:"name"`
	Reason string `json:"reason"`
}

// SyncReport counts what SyncDiscovered did.
type SyncReport struct {
	Discovered int `json:"discovered"`
	// Synced counts skills that were stored or were already up to date.
	Synced int `json:"synced"`
	// Pending counts skills that now wait for review (new or edited files).
	Pending int `json:"pending"`
	// Unchanged counts files that match what was synced (or approved) before.
	Unchanged int `json:"unchanged"`
	// Skipped counts pending skills and names owned by another source.
	Skipped      int            `json:"skipped"`
	SkippedItems []SkippedSkill `json:"skipped_items,omitempty"`
	// Errors lists files that could not be read, parsed or stored.
	Errors []DiscoveryIssue `json:"errors,omitempty"`
}

// ToDBSkill converts a parsed SKILL.md into a database row (without
// provenance; SyncDiscoveredSkill sets it).
func ToDBSkill(s *ParsedSkill) *database.Skill {
	fmJSON, _ := json.Marshal(s.Frontmatter)
	return &database.Skill{
		Name:                   s.EffectiveName(),
		Description:            s.Frontmatter.Description,
		Scope:                  s.Scope,
		Path:                   s.FilePath,
		FrontmatterJSON:        string(fmJSON),
		Body:                   s.Body,
		UserInvocable:          s.Frontmatter.IsUserInvocable(),
		DisableModelInvocation: s.Frontmatter.DisableModelInvocation,
		AllowedTools:           string(s.Frontmatter.AllowedTools),
		Model:                  s.Frontmatter.Model,
		Effort:                 s.Frontmatter.Effort,
		Context:                s.Frontmatter.Context,
		Agent:                  s.Frontmatter.Agent,
		ArgumentHint:           s.Frontmatter.ArgumentHint,
		Shell:                  s.Frontmatter.Shell,
	}
}

// SyncDiscovered stores skills found on disk. Skill files can be written by
// agents, so a new or edited folder is stored pending review and nothing is
// ever approved here (see database.SyncDiscoveredSkill), whoever triggers the
// discovery. The review covers the skill's whole folder (ScanFolder). With a
// store, the folder of each new or re-fingerprinted version is staged as a
// snapshot, so the reviewer approves that copy and later edits on disk have
// no effect until they are reviewed in turn.
func SyncDiscovered(repo *database.SkillRepository, store *Store, discovered []*ParsedSkill) SyncReport {
	report := SyncReport{Discovered: len(discovered)}
	for _, s := range discovered {
		row := ToDBSkill(s)
		folder := ScanFolder(folderOf(s.FilePath))
		row.FolderHash = folder.Hash()
		if folder.OverLimit {
			report.Errors = append(report.Errors, DiscoveryIssue{Path: s.FilePath, Name: row.Name, Message: folder.Problem})
		}
		res, reason, err := repo.SyncDiscoveredSkillWithReason(row)
		if err != nil {
			report.Errors = append(report.Errors, DiscoveryIssue{Path: s.FilePath, Name: row.Name, Message: err.Error()})
			continue
		}
		switch res {
		case database.SkillSyncCreatedPending, database.SkillSyncChangedPending, database.SkillSyncRefingerprinted:
			report.Synced++
			if res == database.SkillSyncRefingerprinted {
				report.Unchanged++
			} else {
				report.Pending++
			}
			if store != nil && !folder.OverLimit {
				if stored, err := repo.GetSkillByName(row.Name); err == nil && stored != nil {
					if _, err := store.EnsureSnapshot(stored); err != nil {
						report.Errors = append(report.Errors, DiscoveryIssue{Path: s.FilePath, Name: row.Name, Message: "cannot stage the skill folder: " + err.Error()})
					}
				}
			}
		case database.SkillSyncUnchanged:
			report.Synced++
			report.Unchanged++
		case database.SkillSyncSkipped:
			report.Skipped++
			report.SkippedItems = append(report.SkippedItems, SkippedSkill{Path: s.FilePath, Name: row.Name, Reason: reason})
		}
	}
	return report
}

// UpgradeLegacyFileSkills re-syncs approved or rejected file skills that were
// synced before folders were fingerprinted (database.IsLegacySkillHash), so
// their folder is fingerprinted (or the skill goes back to review) before a
// session uses them. It reports whether any file was re-synced.
func UpgradeLegacyFileSkills(repo *database.SkillRepository, store *Store, all []*database.Skill) bool {
	var parsed []*ParsedSkill
	for _, s := range all {
		if s.Path == "" || s.Status == database.ReviewStatusPending || !database.IsLegacySkillHash(s.SourceHash) {
			continue
		}
		p, err := ParseFile(s.Path)
		if err != nil {
			continue // the file is gone: the skill keeps only its approved SKILL.md
		}
		p.Scope = s.Scope
		if p.Frontmatter.Name == "" {
			p.Frontmatter.Name = filepath.Base(filepath.Dir(s.Path))
		}
		if p.EffectiveName() != s.Name {
			continue
		}
		parsed = append(parsed, p)
	}
	if len(parsed) == 0 {
		return false
	}
	SyncDiscovered(repo, store, parsed)
	return true
}
