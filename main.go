package main

import (
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
)

// loadForgeConfig initializes the forge directory and loads all configuration.
// Shared between TUI and headless (raid) modes.
func loadForgeConfig() (*ForgeConfig, *RosterFile, error) {
	if err := ensureForgeDir(); err != nil {
		return nil, nil, fmt.Errorf("creating forge dir: %w", err)
	}

	cfg, err := LoadConfig()
	if err != nil {
		if os.IsNotExist(err) {
			cfg = DefaultConfig()
			if err := SaveConfig(cfg); err != nil {
				return nil, nil, fmt.Errorf("saving default config: %w", err)
			}
		} else {
			return nil, nil, fmt.Errorf("loading config: %w", err)
		}
	}

	if err := EnsureDefaultAgents(); err != nil {
		return nil, nil, fmt.Errorf("seeding default agents: %w", err)
	}

	agents, err := LoadAgentsFromDir()
	if err != nil {
		return nil, nil, fmt.Errorf("loading agents: %w", err)
	}
	cfg.Agents = agents

	skills, err := LoadSkillsFromDir()
	if err != nil {
		return nil, nil, fmt.Errorf("loading skills: %w", err)
	}
	cfg.Skills = skills

	roster, err := LoadRoster()
	if err != nil {
		return nil, nil, fmt.Errorf("loading roster: %w", err)
	}
	for _, a := range cfg.Agents {
		if roster.Agents[a.Name] == nil {
			roster.Agents[a.Name] = &AgentRoster{XP: 0, Level: 1}
		}
	}
	SaveRoster(roster)

	return cfg, roster, nil
}

func initialModel() (Model, error) {
	cfg, roster, err := loadForgeConfig()
	if err != nil {
		return Model{}, err
	}

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

	for _, name := range partyNames {
		pf, err := LoadParty(name)
		if err != nil {
			continue
		}
		party := m.buildParty(pf)
		m.parties = append(m.parties, party)
	}

	m.rebuildAgentIndex()

	if len(m.parties) == 1 {
		m.activeParty = 0
		m.autoStartPending = true
	} else if len(m.parties) > 1 {
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
	// Check for subcommands
	if len(os.Args) > 1 && os.Args[1] == "raid" {
		if err := runRaid(os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		return
	}

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
