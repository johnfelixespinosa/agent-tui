package main

import (
	"fmt"
	"strings"
)

// ── Token Budget Constants ─────────────────────────────────────────

const (
	TokenBudgetTotal = 1000
)

// ── Prompt Composition ─────────────────────────────────────────────

// SkillSlot represents a skill in an agent's loadout.
type SkillSlot struct {
	SkillID  string
	IsInnate bool
	Tokens   int
}

// ComposedPrompt is the result of composing all skills into a system prompt.
type ComposedPrompt struct {
	Prompt      string
	Slots       []SkillSlot
	TotalTokens int
}

// ComposePrompt builds the system prompt for an agent instance.
// Concatenates class description + agent directives + innate skill content + equipped skill content.
func ComposePrompt(cfg *ForgeConfig, className string, equipped []string, passives []string, agentDirectives string) ComposedPrompt {
	classCfg := cfg.Classes[className]
	if classCfg == nil {
		return ComposedPrompt{Prompt: ""}
	}

	skillMap := make(map[string]*SkillEntry)
	for _, s := range cfg.Skills {
		skillMap[s.ID] = s
	}

	var slots []SkillSlot
	var parts []string

	// Class header
	classDisplay := className
	if len(classDisplay) > 0 {
		classDisplay = strings.ToUpper(classDisplay[:1]) + classDisplay[1:]
	}
	parts = append(parts, fmt.Sprintf("## Role: %s\n%s", classDisplay, classCfg.Description))

	// Agent profile directives (constraints, behavioral rules)
	if agentDirectives != "" {
		parts = append(parts, fmt.Sprintf("## Agent Profile\n%s", agentDirectives))
	}

	// Innate skills (always active)
	for _, sid := range classCfg.InnateSkills {
		skill := skillMap[sid]
		if skill == nil {
			continue
		}
		tokens := estimateTokens(skill.Content)
		slots = append(slots, SkillSlot{
			SkillID:  sid,
			IsInnate: true,
			Tokens:   tokens,
		})
		parts = append(parts, fmt.Sprintf("## Skill: %s (Innate)\n%s", skill.Name, skill.Content))
	}

	// Equipped skills
	for _, sid := range equipped {
		if isInnate(classCfg, sid) {
			continue
		}
		skill := skillMap[sid]
		if skill == nil {
			continue
		}
		tokens := estimateTokens(skill.Content)
		slots = append(slots, SkillSlot{
			SkillID:  sid,
			IsInnate: false,
			Tokens:   tokens,
		})
		parts = append(parts, fmt.Sprintf("## Skill: %s\n%s", skill.Name, skill.Content))
	}

	// Calculate total tokens
	total := 0
	for _, s := range slots {
		total += s.Tokens
	}

	return ComposedPrompt{
		Prompt:      strings.Join(parts, "\n\n"),
		Slots:       slots,
		TotalTokens: total,
	}
}

// BuildAllowedTools returns the --allowed-tools list for a class.
func BuildAllowedTools(cfg *ForgeConfig, className string) []string {
	classCfg := cfg.Classes[className]
	if classCfg == nil {
		return nil
	}
	return cfg.ToolProfiles[classCfg.ToolProfile]
}

// ── Equip Helpers ──────────────────────────────────────────────────

const MaxEquipSlots = 6

func isInnate(class *ClassConfig, skillID string) bool {
	for _, s := range class.InnateSkills {
		if s == skillID {
			return true
		}
	}
	return false
}

// CanEquip checks if a skill can be equipped in the given loadout.
func CanEquip(cfg *ForgeConfig, className string, equipped []string, skillID string) bool {
	classCfg := cfg.Classes[className]
	if classCfg == nil {
		return false
	}
	if isInnate(classCfg, skillID) {
		return false
	}
	for _, e := range equipped {
		if e == skillID {
			return false
		}
	}
	return len(equipped) < MaxEquipSlots
}

// ToggleEquip adds or removes a skill from the equipped list.
func ToggleEquip(cfg *ForgeConfig, className string, equipped []string, skillID string) []string {
	for i, e := range equipped {
		if e == skillID {
			return append(equipped[:i], equipped[i+1:]...)
		}
	}
	if CanEquip(cfg, className, equipped, skillID) {
		return append(equipped, skillID)
	}
	return equipped
}

// ── Helpers ────────────────────────────────────────────────────────

// estimateTokens gives a rough token estimate for text.
func estimateTokens(text string) int {
	words := len(strings.Fields(text))
	return int(float64(words) * 1.3)
}

// SkillByID finds a skill entry by ID.
func SkillByID(cfg *ForgeConfig, id string) *SkillEntry {
	for _, s := range cfg.Skills {
		if s.ID == id {
			return s
		}
	}
	return nil
}

// AllSkillIDs returns all skill IDs from config.
func AllSkillIDs(cfg *ForgeConfig) []string {
	ids := make([]string, 0, len(cfg.Skills))
	for _, s := range cfg.Skills {
		ids = append(ids, s.ID)
	}
	return ids
}
