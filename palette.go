package main

import (
	"fmt"
	"strings"
	"syscall"

	tea "github.com/charmbracelet/bubbletea"
)

// ── Command Palette (Sub-Model Pattern) ───────────────────────────
//
// This file demonstrates the sub-model extraction pattern. The palette
// manages its own state (input, cursor) and action list, but delegates
// back to the parent Model for state mutations via Action closures.
// Future candidates for this pattern: WizardModel, CharSheetModel.

// PaletteAction represents a single command palette entry.
type PaletteAction struct {
	Label  string
	Action func(m *Model) tea.Cmd
}

// paletteActions builds the full list of available commands.
func (m Model) paletteActions() []PaletteAction {
	var actions []PaletteAction

	p := m.party()
	if p != nil {
		for i, inst := range p.Slots {
			if inst == nil {
				continue
			}
			idx := i
			name := inst.AgentName
			if inst.Status == "idle" || inst.Status == "exited" {
				actions = append(actions, PaletteAction{
					Label: fmt.Sprintf("Start %s", name),
					Action: func(m *Model) tea.Cmd {
						m.selectedAgent = idx
						inst := m.agent()
						if inst == nil {
							return nil
						}
						tw := m.termWidth()
						th := m.termHeight()
						if tw <= 0 || th <= 0 {
							return nil
						}
						inst.Status = "idle"
						inst.Task = "Starting..."
						projectDir := "."
						partyName := ""
						if p := m.party(); p != nil {
							if p.Project != "" {
								projectDir = p.Project
							}
							partyName = p.Name
						}
						return startAgent(inst, tw, th, m.config, projectDir, partyName)
					},
				})
			}
			if inst.Status == "running" {
				actions = append(actions, PaletteAction{
					Label: fmt.Sprintf("Focus %s", name),
					Action: func(m *Model) tea.Cmd {
						m.selectedAgent = idx
						m.focus = FocusMainPane
						m.mode = ModeInsert
						return nil
					},
				})
				actions = append(actions, PaletteAction{
					Label: fmt.Sprintf("Stop %s", name),
					Action: func(m *Model) tea.Cmd {
						m.selectedAgent = idx
						inst := m.agent()
						if inst != nil && inst.cmd != nil && inst.cmd.Process != nil {
							inst.Status = "exited"
							inst.Task = "Stopping..."
							inst.cmd.Process.Signal(syscall.SIGTERM)
						}
						return nil
					},
				})
			}
			actions = append(actions, PaletteAction{
				Label: fmt.Sprintf("Sheet %s", name),
				Action: func(m *Model) tea.Cmd {
					m.selectedAgent = idx
					m.mode = ModeCharSheet
					m.csSection = 0
					m.csCursor = 0
					m.bioScroll = 0
					return nil
				},
			})
		}
	}

	// Party actions
	for i, party := range m.parties {
		idx := i
		pName := party.Name
		actions = append(actions, PaletteAction{
			Label: fmt.Sprintf("Switch to %s", pName),
			Action: func(m *Model) tea.Cmd {
				m.activeParty = idx
				m.selectedAgent = 0
				m.recomputeLayout()
				m.resizeActivePartyAgents()
				return nil
			},
		})
	}

	// Panel actions
	actions = append(actions, PaletteAction{
		Label: "Toggle files/PRs panel",
		Action: func(m *Model) tea.Cmd {
			newM, cmd := m.toggleGitPanel()
			*m = newM
			return cmd
		},
	})

	actions = append(actions, PaletteAction{
		Label: "New party",
		Action: func(m *Model) tea.Cmd {
			newM, cmd := m.createNewParty()
			*m = newM
			return cmd
		},
	})

	return actions
}

// filteredPaletteActions returns actions matching the current input filter.
func (m Model) filteredPaletteActions() []PaletteAction {
	all := m.paletteActions()
	if m.cmdPaletteInput == "" {
		return all
	}
	query := strings.ToLower(m.cmdPaletteInput)
	var filtered []PaletteAction
	for _, a := range all {
		if strings.Contains(strings.ToLower(a.Label), query) {
			filtered = append(filtered, a)
		}
	}
	return filtered
}

// handleCommandPalette processes key input for the command palette mode.
func (m Model) handleCommandPalette(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.popMode()
		return m, nil
	case "enter":
		actions := m.filteredPaletteActions()
		if m.cmdPaletteCursor >= 0 && m.cmdPaletteCursor < len(actions) {
			cmd := actions[m.cmdPaletteCursor].Action(&m)
			if m.mode == ModeCommandPalette {
				m.popMode()
			}
			return m, cmd
		}
		m.popMode()
		return m, nil
	case "up", "ctrl+p":
		if m.cmdPaletteCursor > 0 {
			m.cmdPaletteCursor--
		}
	case "down", "ctrl+n":
		actions := m.filteredPaletteActions()
		if m.cmdPaletteCursor < len(actions)-1 {
			m.cmdPaletteCursor++
		}
	case "backspace":
		if len(m.cmdPaletteInput) > 0 {
			m.cmdPaletteInput = m.cmdPaletteInput[:len(m.cmdPaletteInput)-1]
			m.cmdPaletteCursor = 0
		}
	default:
		r := []rune(msg.String())
		if len(r) == 1 && r[0] >= ' ' {
			m.cmdPaletteInput += string(r)
			m.cmdPaletteCursor = 0
		}
	}
	return m, nil
}
