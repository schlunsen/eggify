package database

import "testing"

func fileSkill(path, body string) *Skill {
	return &Skill{Name: "deploy", Description: "d", Scope: "project", Path: path, FrontmatterJSON: `{"name":"deploy"}`, Body: body, UserInvocable: true}
}

func mustSync(t *testing.T, repo *Repository, s *Skill, want SkillSyncResult) *Skill {
	t.Helper()
	got, err := repo.Skill.SyncDiscoveredSkill(s)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("sync = %s, want %s", got, want)
	}
	row, err := repo.Skill.GetSkillByName(s.Name)
	if err != nil || row == nil {
		t.Fatalf("skill row: %v %v", row, err)
	}
	return row
}

func approve(t *testing.T, repo *Repository, id string, body *string) {
	t.Helper()
	if _, err := repo.Skill.ReviewSkill(id, SkillReview{Status: ReviewStatusApproved, ReviewedBy: "user:alice", Body: body}); err != nil {
		t.Fatal(err)
	}
}

func TestSkillMigrationAddsSourceHash(t *testing.T) {
	db, _ := newReviewTestDB(t)
	if !columnExists(t, db, "skills", "source_hash") {
		t.Fatal("skills.source_hash missing")
	}
}

// A discovered skill file is pending until approved; an unchanged file keeps
// the decision; an edited file goes back to review with the new text.
func TestSyncDiscoveredSkillLifecycle(t *testing.T) {
	_, repo := newReviewTestDB(t)
	const path = "/repo/.claude/skills/deploy/SKILL.md"

	row := mustSync(t, repo, fileSkill(path, "v1"), SkillSyncCreatedPending)
	if row.Status != ReviewStatusPending || row.CreatedBy != CreatedByFile(path) {
		t.Fatalf("new file skill = %s/%s, want pending/file:", row.Status, row.CreatedBy)
	}
	mustSync(t, repo, fileSkill(path, "v1"), SkillSyncUnchanged)

	// A pending skill is never overwritten, even by its own file.
	row = mustSync(t, repo, fileSkill(path, "v2 before review"), SkillSyncSkipped)
	if row.Body != "v1" {
		t.Fatalf("pending body overwritten: %q", row.Body)
	}

	// Approved (with a reviewer edit): rediscovering the same file keeps it.
	edited := "v1, edited by the reviewer"
	approve(t, repo, row.ID, &edited)
	row = mustSync(t, repo, fileSkill(path, "v1"), SkillSyncUnchanged)
	if row.Status != ReviewStatusApproved || row.Body != edited {
		t.Fatalf("approved skill changed on rediscovery: %s %q", row.Status, row.Body)
	}

	// The file is edited: back to pending with the new content.
	row = mustSync(t, repo, fileSkill(path, "v3: push to main"), SkillSyncChangedPending)
	if row.Status != ReviewStatusPending || row.Body != "v3: push to main" || row.ReviewedBy != nil {
		t.Fatalf("edited file skill = %s %q reviewed_by=%v, want pending v3", row.Status, row.Body, row.ReviewedBy)
	}

	// Rejected stays rejected while the file is unchanged.
	if _, err := repo.Skill.ReviewSkill(row.ID, SkillReview{Status: ReviewStatusRejected, ReviewedBy: "user:alice"}); err != nil {
		t.Fatal(err)
	}
	row = mustSync(t, repo, fileSkill(path, "v3: push to main"), SkillSyncUnchanged)
	if row.Status != ReviewStatusRejected {
		t.Fatalf("rejected skill = %s", row.Status)
	}
}

// Rows synced before source_hash existed keep their approval when the file
// still matches; a name owned by another source is left alone.
func TestSyncDiscoveredSkillGrandfatheredAndForeignNames(t *testing.T) {
	_, repo := newReviewTestDB(t)
	const path = "/home/me/.claude/skills/deploy/SKILL.md"
	old := fileSkill(path, "v1")
	if err := repo.Skill.SaveSkill(old); err != nil { // the pre-upgrade upsert: approved user skill
		t.Fatal(err)
	}
	row := mustSync(t, repo, fileSkill(path, "v1"), SkillSyncRefingerprinted)
	if row.Status != ReviewStatusApproved {
		t.Fatalf("identical grandfathered skill = %s, want approved", row.Status)
	}
	mustSync(t, repo, fileSkill(path, "v1"), SkillSyncUnchanged)
	row = mustSync(t, repo, fileSkill(path, "v2"), SkillSyncChangedPending)
	if row.Status != ReviewStatusPending {
		t.Fatalf("edited grandfathered skill = %s, want pending", row.Status)
	}

	// A pending agent skill (no path) and a UI skill keep their name.
	agent := &Skill{Name: "helper", Scope: "project", Body: "agent text", UserInvocable: true}
	if err := repo.Skill.CreateAgentSkill(agent, "s1"); err != nil {
		t.Fatal(err)
	}
	s := fileSkill("/repo/.claude/skills/helper/SKILL.md", "file text")
	s.Name = "helper"
	row = mustSync(t, repo, s, SkillSyncSkipped)
	if row.Body != "agent text" || row.Status != ReviewStatusPending || row.CreatedBy != CreatedByAgent("s1") {
		t.Fatalf("agent skill overwritten: %+v", row)
	}
	ui := &Skill{Name: "ui", Scope: "project", Body: "user text", UserInvocable: true}
	if err := repo.Skill.SaveSkill(ui); err != nil {
		t.Fatal(err)
	}
	s = fileSkill("/repo/.claude/skills/ui/SKILL.md", "file text")
	s.Name = "ui"
	row = mustSync(t, repo, s, SkillSyncSkipped)
	if row.Body != "user text" || row.Status != ReviewStatusApproved {
		t.Fatalf("UI skill overwritten: %+v", row)
	}
}

func folderSkill(path, body, folder string) *Skill {
	s := fileSkill(path, body)
	s.FolderHash = folder
	return s
}

// The review covers the whole folder: a script changed after approval sends
// the skill back to pending, and the new folder hash belongs to the new row.
func TestSyncDiscoveredSkillFolderChange(t *testing.T) {
	_, repo := newReviewTestDB(t)
	const path = "/repo/.claude/skills/deploy/SKILL.md"
	row := mustSync(t, repo, folderSkill(path, "v1", "folder-a"), SkillSyncCreatedPending)
	if row.FolderHash != "folder-a" || IsLegacySkillHash(row.SourceHash) {
		t.Fatalf("new row folder=%q source=%q", row.FolderHash, row.SourceHash)
	}
	approve(t, repo, row.ID, nil)
	mustSync(t, repo, folderSkill(path, "v1", "folder-a"), SkillSyncUnchanged)

	row = mustSync(t, repo, folderSkill(path, "v1", "folder-b"), SkillSyncChangedPending)
	if row.Status != ReviewStatusPending || row.FolderHash != "folder-b" {
		t.Fatalf("script change = %s folder=%q, want pending folder-b", row.Status, row.FolderHash)
	}
}

// A folder over the limits never matches: approved goes back to pending, and
// it never counts as unchanged.
func TestSyncDiscoveredSkillOverLimitNeverMatches(t *testing.T) {
	_, repo := newReviewTestDB(t)
	const path = "/repo/.claude/skills/deploy/SKILL.md"
	row := mustSync(t, repo, folderSkill(path, "v1", "folder-a"), SkillSyncCreatedPending)
	approve(t, repo, row.ID, nil)
	row = mustSync(t, repo, folderSkill(path, "v1", FolderHashOverLimitPrefix+"r1"), SkillSyncChangedPending)
	if row.Status != ReviewStatusPending {
		t.Fatalf("over-limit folder = %s", row.Status)
	}
}

// Upgrade path: a row holding a v1 hash (SKILL.md only) is re-fingerprinted
// once when SKILL.md still matches, keeping approved or rejected; a v1 row
// whose SKILL.md changed goes back to pending.
func TestSyncDiscoveredSkillLegacyHashUpgrade(t *testing.T) {
	db, repo := newReviewTestDB(t)
	const path = "/repo/.claude/skills/deploy/SKILL.md"
	row := mustSync(t, repo, folderSkill(path, "v1", "folder-a"), SkillSyncCreatedPending)
	approve(t, repo, row.ID, nil)
	legacy := SkillContentHash("deploy", "d", `{"name":"deploy"}`, "v1")
	if _, err := db.db.Exec(`UPDATE skills SET source_hash = ?, folder_hash = '' WHERE id = ?`, legacy, row.ID); err != nil {
		t.Fatal(err)
	}
	row = mustSync(t, repo, folderSkill(path, "v1", "folder-now"), SkillSyncRefingerprinted)
	if row.Status != ReviewStatusApproved || row.FolderHash != "folder-now" || IsLegacySkillHash(row.SourceHash) {
		t.Fatalf("upgraded row = %s folder=%q source=%q", row.Status, row.FolderHash, row.SourceHash)
	}
	mustSync(t, repo, folderSkill(path, "v1", "folder-now"), SkillSyncUnchanged)

	// A legacy row whose SKILL.md changed is not upgraded.
	if _, err := db.db.Exec(`UPDATE skills SET source_hash = ? WHERE id = ?`, legacy, row.ID); err != nil {
		t.Fatal(err)
	}
	row = mustSync(t, repo, folderSkill(path, "v2", "folder-now"), SkillSyncChangedPending)
	if row.Status != ReviewStatusPending {
		t.Fatalf("changed legacy row = %s", row.Status)
	}
}
