package database

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Skills discovered from SKILL.md files (a project's .claude/skills and
// ~/.claude/skills) come from files an agent can write, so filesystem
// discovery never approves anything by itself. A skill is its whole folder:
// SKILL.md plus the scripts and templates next to it (FolderHash, computed by
// internal/skills), and the review covers all of it:
//
//   - a new folder is stored pending, created_by "file:<path>";
//   - a folder whose hash matches what was last synced keeps its review
//     decision (approved stays approved, rejected stays rejected);
//   - a row synced before folders were fingerprinted (a v1 hash of SKILL.md
//     alone, or none) whose SKILL.md still matches is re-fingerprinted once
//     with its current folder and keeps its decision, so upgrading does not
//     send every approved skill back to review;
//   - an edited SKILL.md, or any added, removed or edited file in the folder,
//     sends an approved or rejected skill back to pending with the new
//     content. The old approved version is NOT kept in use meanwhile: the
//     skill is unavailable until the user approves the new content (one row
//     per name, and a reviewer always approves exactly what will be used);
//   - a folder over the size limits never matches (always "changed");
//   - a pending skill is never touched, nor is a skill of the same name that
//     came from somewhere else (an agent proposal, the UI, another file).

// CreatedByFilePrefix starts CreatedBy for skills discovered from a file.
const CreatedByFilePrefix = "file:"

// CreatedByFile returns the CreatedBy value for a skill read from path.
func CreatedByFile(path string) string { return CreatedByFilePrefix + path }

// IsFileOrigin reports whether createdBy names a discovered skill file.
func IsFileOrigin(createdBy string) bool { return strings.HasPrefix(createdBy, CreatedByFilePrefix) }

// SkillSyncResult says what SyncDiscoveredSkill did.
type SkillSyncResult string

const (
	// SkillSyncCreatedPending: a new skill, waiting for review.
	SkillSyncCreatedPending SkillSyncResult = "created_pending"
	// SkillSyncChangedPending: the file changed; the skill waits for review again.
	SkillSyncChangedPending SkillSyncResult = "changed_pending"
	// SkillSyncUnchanged: the file matches what was synced (or approved) before.
	SkillSyncUnchanged SkillSyncResult = "unchanged"
	// SkillSyncSkipped: a pending skill, or one of the same name from elsewhere.
	SkillSyncSkipped SkillSyncResult = "skipped"
	// SkillSyncRefingerprinted: a row from before folder fingerprints whose
	// SKILL.md still matches; it now carries the v2 hash of its current folder
	// and keeps its review decision.
	SkillSyncRefingerprinted SkillSyncResult = "refingerprinted"
)

// skillHashV2Prefix marks source hashes that cover the skill's whole folder.
const skillHashV2Prefix = "v2:"

// FolderHashOverLimitPrefix starts the FolderHash of a folder over the size
// limits. Such a hash is random, so it never matches a stored one.
const FolderHashOverLimitPrefix = "overlimit:"

// IsLegacySkillHash reports whether a stored source hash predates folder
// fingerprints (a v1 hash of SKILL.md alone, or empty).
func IsLegacySkillHash(h string) bool { return !strings.HasPrefix(h, skillHashV2Prefix) }

// SkillSourceHash is the v2 hash of a discovered skill: its SKILL.md content
// (as SkillContentHash) and the fingerprint of the rest of its folder.
func SkillSourceHash(name, description, frontmatterJSON, body, folderHash string) string {
	return skillHashV2Prefix + SkillSnapshotKey(name, description, frontmatterJSON, body, folderHash)
}

// SkillSnapshotKey content-addresses a skill version (its fields and folder):
// the name of its snapshot directory.
func SkillSnapshotKey(name, description, frontmatterJSON, body, folderHash string) string {
	h := sha256.New()
	for _, part := range []string{"skill-v2", name, description, frontmatterJSON, body, folderHash} {
		h.Write([]byte(part))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// SnapshotKey is SkillSnapshotKey for the row's current content.
func (s *Skill) SnapshotKey() string {
	return SkillSnapshotKey(s.Name, s.Description, s.FrontmatterJSON, s.Body, s.FolderHash)
}

// SkillContentHash is the v1 hash of a skill's SKILL.md: its name,
// description, frontmatter (tools, model, ...) and body. Rows synced before
// migration 56 store it; SkillSourceHash replaced it.
func SkillContentHash(name, description, frontmatterJSON, body string) string {
	h := sha256.New()
	for _, part := range []string{name, description, frontmatterJSON, body} {
		h.Write([]byte(part))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// SyncDiscoveredSkill stores a skill read from skill.Path following the rules
// above. skill.FolderHash must hold the fingerprint of the files next to
// SKILL.md. skill.CreatedBy, Status and review fields are ignored.
func (r *SkillRepository) SyncDiscoveredSkill(skill *Skill) (SkillSyncResult, error) {
	res, _, err := r.SyncDiscoveredSkillWithReason(skill)
	return res, err
}

// SyncDiscoveredSkillWithReason is SyncDiscoveredSkill that also says, for
// SkillSyncSkipped, why the file was left alone (for the discovery report).
func (r *SkillRepository) SyncDiscoveredSkillWithReason(skill *Skill) (SkillSyncResult, string, error) {
	if skill.Name == "" || skill.Path == "" {
		return SkillSyncSkipped, "", fmt.Errorf("discovered skill needs a name and a path")
	}
	hash := SkillSourceHash(skill.Name, skill.Description, skill.FrontmatterJSON, skill.Body, skill.FolderHash)
	overLimit := strings.HasPrefix(skill.FolderHash, FolderHashOverLimitPrefix)
	nilIfEmpty := func(s string) interface{} {
		if s == "" {
			return nil
		}
		return s
	}

	r.db.mu.Lock()
	defer r.db.mu.Unlock()

	var (
		id, path, status, sourceHash, createdBy string
		description, fmJSON, body               sql.NullString
	)
	err := r.db.db.QueryRow(`SELECT id, path, status, source_hash, created_by, description, frontmatter_json, body
		FROM skills WHERE name = ?`, skill.Name).
		Scan(&id, &path, &status, &sourceHash, &createdBy, &description, &fmJSON, &body)
	now := time.Now()

	if err == sql.ErrNoRows {
		if skill.ID == "" {
			skill.ID = uuid.New().String()
		}
		skill.CreatedBy = CreatedByFile(skill.Path)
		skill.Status = ReviewStatusPending
		skill.ReviewedBy, skill.ReviewedAt = nil, nil
		_, err := r.db.db.Exec(`INSERT INTO skills (
				id, name, description, scope, path, frontmatter_json, body,
				user_invocable, disable_model_invocation, allowed_tools,
				model, effort, context, agent, argument_hint, shell,
				created_at, updated_at, created_by, status, source_hash, folder_hash
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			skill.ID, skill.Name, skill.Description, skill.Scope, skill.Path,
			skill.FrontmatterJSON, skill.Body,
			skill.UserInvocable, skill.DisableModelInvocation, skill.AllowedTools,
			skill.Model, nilIfEmpty(skill.Effort), nilIfEmpty(skill.Context),
			skill.Agent, skill.ArgumentHint, nilIfEmpty(skill.Shell),
			now, now, skill.CreatedBy, skill.Status, hash, skill.FolderHash)
		if err != nil {
			return SkillSyncSkipped, "", fmt.Errorf("failed to save discovered skill: %w", err)
		}
		return SkillSyncCreatedPending, "", nil
	}
	if err != nil {
		return SkillSyncSkipped, "", fmt.Errorf("failed to look up skill: %w", err)
	}

	// Another source owns this name (an agent proposal, a UI skill, another
	// file): leave it alone.
	if path != skill.Path {
		return SkillSyncSkipped, foreignOwner(skill.Name, path, createdBy), nil
	}
	if sourceHash == hash {
		return SkillSyncUnchanged, "", nil
	}
	// Never overwrite (or approve) a skill that is waiting for review: the
	// reviewer must approve exactly the text they read.
	if status == ReviewStatusPending {
		return SkillSyncSkipped, fmt.Sprintf("the file changed while %q waits for review; approve or reject the queued version first", skill.Name), nil
	}
	// Synced before folders were fingerprinted, and SKILL.md is unchanged
	// (or, for rows older than source_hash, equals the approved content):
	// adopt the current folder once and keep the decision.
	if IsLegacySkillHash(sourceHash) && !overLimit {
		fileV1 := SkillContentHash(skill.Name, skill.Description, skill.FrontmatterJSON, skill.Body)
		rowV1 := SkillContentHash(skill.Name, description.String, fmJSON.String, body.String)
		if (sourceHash != "" && sourceHash == fileV1) || (status == ReviewStatusApproved && rowV1 == fileV1) {
			if _, err := r.db.db.Exec(`UPDATE skills SET source_hash = ?, folder_hash = ? WHERE id = ?`, hash, skill.FolderHash, id); err != nil {
				return SkillSyncSkipped, "", fmt.Errorf("failed to record skill hash: %w", err)
			}
			skill.ID = id
			return SkillSyncRefingerprinted, "", nil
		}
	}

	// Edited file: new content, back to review.
	skill.ID = id
	skill.CreatedBy = CreatedByFile(skill.Path)
	skill.Status = ReviewStatusPending
	skill.ReviewedBy, skill.ReviewedAt = nil, nil
	_, err = r.db.db.Exec(`UPDATE skills SET
			description = ?, scope = ?, frontmatter_json = ?, body = ?,
			user_invocable = ?, disable_model_invocation = ?, allowed_tools = ?,
			model = ?, effort = ?, context = ?, agent = ?, argument_hint = ?, shell = ?,
			updated_at = ?, created_by = ?, status = ?, reviewed_by = NULL, reviewed_at = NULL,
			source_hash = ?, folder_hash = ?
		WHERE id = ? AND status != 'pending'`,
		skill.Description, skill.Scope, skill.FrontmatterJSON, skill.Body,
		skill.UserInvocable, skill.DisableModelInvocation, skill.AllowedTools,
		skill.Model, nilIfEmpty(skill.Effort), nilIfEmpty(skill.Context),
		skill.Agent, skill.ArgumentHint, nilIfEmpty(skill.Shell),
		now, skill.CreatedBy, skill.Status, hash, skill.FolderHash, id)
	if err != nil {
		return SkillSyncSkipped, "", fmt.Errorf("failed to update discovered skill: %w", err)
	}
	return SkillSyncChangedPending, "", nil
}

// foreignOwner describes who already holds a skill name.
func foreignOwner(name, path, createdBy string) string {
	switch {
	case IsFileOrigin(createdBy) && path != "":
		return fmt.Sprintf("name %q is already used by %s", name, path)
	case strings.HasPrefix(createdBy, "agent:"):
		return fmt.Sprintf("name %q is already used by a skill an agent proposed", name)
	case path != "":
		return fmt.Sprintf("name %q is already used by %s", name, path)
	default:
		return fmt.Sprintf("name %q is already used by a skill created in Eggify", name)
	}
}
