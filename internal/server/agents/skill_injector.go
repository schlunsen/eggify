package agents

import (
	"github.com/schlunsen/eggify/internal/database"
	"github.com/schlunsen/eggify/internal/logging"
)

// SkillInjector resolves the skills a session enabled explicitly
// (SessionOptions.EnabledSkillIDs). Skills are no longer pasted into system
// prompts: Claude sessions load approved skills natively from a plugin
// directory and other runtimes get an index plus invoke_skill (see
// native_skills.go).
type SkillInjector struct {
	repo *database.Repository
}

// NewSkillInjector creates a new skill injector backed by the given repository.
func NewSkillInjector(repo *database.Repository) *SkillInjector {
	return &SkillInjector{repo: repo}
}

// ApprovedSkills returns the approved skills among ids (ids or names), in
// order. Pending and rejected skills are left out.
func (si *SkillInjector) ApprovedSkills(ids []string) []*database.Skill {
	var out []*database.Skill
	for _, id := range ids {
		skill, err := si.repo.Skill.GetSkillByID(id)
		if err != nil || skill == nil {
			skill, err = si.repo.Skill.GetSkillByName(id)
			if err != nil || skill == nil {
				logging.Warning("Skill '%s' enabled for a session was not found", id)
				continue
			}
		}
		if skill.Status != database.ReviewStatusApproved {
			continue
		}
		out = append(out, skill)
	}
	return out
}
