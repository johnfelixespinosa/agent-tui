package main

import (
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
)

func initialModel() (Model, error) {
	// Ensure ~/.agent-forge/ exists
	if err := ensureForgeDir(); err != nil {
		return Model{}, fmt.Errorf("creating forge dir: %w", err)
	}

	// Load or create config
	cfg, err := LoadConfig()
	if err != nil {
		if os.IsNotExist(err) {
			cfg = DefaultConfig()
			if err := SaveConfig(cfg); err != nil {
				return Model{}, fmt.Errorf("saving default config: %w", err)
			}
		} else {
			return Model{}, fmt.Errorf("loading config: %w", err)
		}
	}

	// Ensure default agents exist in ~/.claude/agents/
	if err := EnsureDefaultAgents(); err != nil {
		return Model{}, fmt.Errorf("seeding default agents: %w", err)
	}

	// Load agents from ~/.claude/agents/*.yaml
	agents, err := LoadAgentsFromDir()
	if err != nil {
		return Model{}, fmt.Errorf("loading agents: %w", err)
	}
	cfg.Agents = agents

	// Load skills from ~/.claude/skills/*/SKILL.md
	skills, err := LoadSkillsFromDir()
	if err != nil {
		return Model{}, fmt.Errorf("loading skills: %w", err)
	}
	cfg.Skills = skills

	// Load or create roster
	roster, err := LoadRoster()
	if err != nil {
		return Model{}, fmt.Errorf("loading roster: %w", err)
	}
	for _, a := range cfg.Agents {
		if roster.Agents[a.Name] == nil {
			roster.Agents[a.Name] = &AgentRoster{XP: 0, Level: 1}
		}
	}
	SaveRoster(roster)

	// Load existing parties
	partyNames, err := ListPartyFiles()
	if err != nil {
		return Model{}, fmt.Errorf("listing parties: %w", err)
	}

	m := Model{
		config:        cfg,
		roster:        roster,
		focus:         FocusMainPane,
		mode:          ModeNormal,
		selectedAgent: 0,
	}

	// Build existing parties from files
	for _, name := range partyNames {
		pf, err := LoadParty(name)
		if err != nil {
			continue
		}
		party := m.buildParty(pf)
		m.parties = append(m.parties, party)
	}

	// Start with wizard: choose existing party or create new
	if len(m.parties) > 0 {
		m.wizard = &WizardState{
			Step:               WizardChooseParty,
			HasExistingParties: true,
		}
	} else {
		cwd, _ := os.Getwd()
		m.wizard = &WizardState{
			Step:    WizardNameParty,
			Project: cwd,
		}
	}

	return m, nil
}

func main() {
	model, err := initialModel()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	p := tea.NewProgram(
		model,
		tea.WithAltScreen(),
		tea.WithMouseCellMotion(),
	)

	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
