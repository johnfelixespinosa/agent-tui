package main

import (
	"fmt"
	"image/color"
	"os"
	"path/filepath"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// ── Wizard Types ──────────────────────────────────────────────────

type WizardStep int

const (
	WizardChooseParty WizardStep = iota
	WizardNameParty
	WizardSelectProject
	WizardAddAgents
	WizardFinalize
)

type WizardState struct {
	Step           WizardStep
	Name           string
	Project        string
	SelectedAgents []string // agent names picked for slots (up to 4)
	Cursor         int
	TextBuf        string
	DirEntries     []string // subdirectory names for directory browser

	// Behaviour flags
	HasExistingParties bool // show chooser on back from name step
	CancelToNormal     bool // esc at first step cancels wizard (inline create)
}

// scanDirEntries returns sorted subdirectory names for a path.
func scanDirEntries(path string) []string {
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil
	}
	var dirs []string
	for _, e := range entries {
		if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
			dirs = append(dirs, e.Name())
		}
	}
	sort.Strings(dirs)
	return dirs
}

// ── Key Dispatch ──────────────────────────────────────────────────

func (m Model) handleWizardKeys(msg tea.KeyMsg) (Model, tea.Cmd) {
	switch m.wizard.Step {
	case WizardChooseParty:
		return m.wizardChooseParty(msg)
	case WizardNameParty:
		return m.wizardNameParty(msg)
	case WizardSelectProject:
		return m.wizardSelectProject(msg)
	case WizardAddAgents:
		return m.wizardAddAgents(msg)
	case WizardFinalize:
		return m.wizardFinalizeKeys(msg)
	}
	return m, nil
}

// ── Step Handlers ─────────────────────────────────────────────────

func (m Model) wizardChooseParty(msg tea.KeyMsg) (Model, tea.Cmd) {
	w := m.wizard
	maxIdx := len(m.parties) // last index = "Create New"

	switch msg.String() {
	case "up", "k":
		if w.Cursor > 0 {
			w.Cursor--
		}
	case "down", "j":
		if w.Cursor < maxIdx {
			w.Cursor++
		}
	case "enter":
		if w.Cursor < len(m.parties) {
			// Selected existing party
			m.activeParty = w.Cursor
			m.selectedAgent = 0
			m.wizard = nil
			return m.autoStartPartyAgents()
		}
		// "Create New" selected
		cwd, _ := os.Getwd()
		w.Step = WizardNameParty
		w.Cursor = 0
		w.TextBuf = ""
		if w.Project == "" {
			w.Project = cwd
		}
	case "q", "ctrl+c", "esc":
		return m, tea.Quit
	}
	return m, nil
}

func (m Model) wizardNameParty(msg tea.KeyMsg) (Model, tea.Cmd) {
	w := m.wizard

	switch msg.String() {
	case "enter":
		name := strings.TrimSpace(w.TextBuf)
		if name == "" {
			return m, nil
		}
		w.Name = name
		w.Step = WizardSelectProject
		w.Cursor = 0
		if w.Project == "" {
			cwd, _ := os.Getwd()
			w.Project = cwd
		}
		w.DirEntries = scanDirEntries(w.Project)
	case "esc":
		if w.CancelToNormal {
			m.wizard = nil
			return m, nil
		}
		if w.HasExistingParties {
			w.Step = WizardChooseParty
			w.Cursor = 0
		} else {
			return m, tea.Quit
		}
	case "backspace":
		if len(w.TextBuf) > 0 {
			w.TextBuf = w.TextBuf[:len(w.TextBuf)-1]
		}
	case "ctrl+c":
		return m, tea.Quit
	default:
		r := []rune(msg.String())
		if len(r) == 1 && r[0] >= ' ' {
			w.TextBuf += string(r)
		}
	}
	return m, nil
}

func (m Model) wizardSelectProject(msg tea.KeyMsg) (Model, tea.Cmd) {
	w := m.wizard
	totalEntries := 2 + len(w.DirEntries) // [Select] + ".." + subdirs

	switch msg.String() {
	case "up", "k":
		if w.Cursor > 0 {
			w.Cursor--
		}
	case "down", "j":
		if w.Cursor < totalEntries-1 {
			w.Cursor++
		}
	case "enter":
		if w.Cursor == 0 {
			// Select this directory
			w.Step = WizardAddAgents
			w.Cursor = 0
		} else if w.Cursor == 1 {
			// Go to parent
			parent := filepath.Dir(w.Project)
			if parent != w.Project {
				w.Project = parent
				w.DirEntries = scanDirEntries(parent)
				w.Cursor = 0
			}
		} else {
			// Enter subdirectory
			dirIdx := w.Cursor - 2
			if dirIdx >= 0 && dirIdx < len(w.DirEntries) {
				newPath := filepath.Join(w.Project, w.DirEntries[dirIdx])
				w.Project = newPath
				w.DirEntries = scanDirEntries(newPath)
				w.Cursor = 0
			}
		}
	case "backspace":
		parent := filepath.Dir(w.Project)
		if parent != w.Project {
			w.Project = parent
			w.DirEntries = scanDirEntries(parent)
			w.Cursor = 0
		}
	case "esc":
		w.Step = WizardNameParty
		w.TextBuf = w.Name
		w.Cursor = 0
	case "ctrl+c":
		return m, tea.Quit
	}
	return m, nil
}

func (m Model) wizardAddAgents(msg tea.KeyMsg) (Model, tea.Cmd) {
	w := m.wizard
	maxIdx := len(m.config.Agents) - 1

	switch msg.String() {
	case "up", "k":
		if w.Cursor > 0 {
			w.Cursor--
		}
	case "down", "j":
		if w.Cursor < maxIdx {
			w.Cursor++
		}
	case " ":
		if w.Cursor < len(m.config.Agents) {
			name := m.config.Agents[w.Cursor].Name
			found := -1
			for i, n := range w.SelectedAgents {
				if n == name {
					found = i
					break
				}
			}
			if found >= 0 {
				w.SelectedAgents = append(w.SelectedAgents[:found], w.SelectedAgents[found+1:]...)
			} else if len(w.SelectedAgents) < MaxPartySlots {
				w.SelectedAgents = append(w.SelectedAgents, name)
			}
		}
	case "enter":
		if len(w.SelectedAgents) > 0 {
			w.Step = WizardFinalize
			w.Cursor = 0
		}
	case "esc":
		w.Step = WizardSelectProject
		w.DirEntries = scanDirEntries(w.Project)
		w.Cursor = 0
	case "ctrl+c":
		return m, tea.Quit
	}
	return m, nil
}

func (m Model) wizardFinalizeKeys(msg tea.KeyMsg) (Model, tea.Cmd) {
	switch msg.String() {
	case "enter":
		return m.doFinalizeWizard()
	case "esc":
		m.wizard.Step = WizardAddAgents
		m.wizard.Cursor = 0
	case "ctrl+c":
		return m, tea.Quit
	}
	return m, nil
}

// ── Finalize & Auto-Start ─────────────────────────────────────────

func (m Model) doFinalizeWizard() (Model, tea.Cmd) {
	w := m.wizard

	pf := &PartyFile{
		Name:    w.Name,
		Project: w.Project,
	}
	for _, agentName := range w.SelectedAgents {
		pf.Slots = append(pf.Slots, PartySlotConfig{Agent: agentName})
	}

	// Remaining agents go to bench
	for _, a := range m.config.Agents {
		selected := false
		for _, n := range w.SelectedAgents {
			if n == a.Name {
				selected = true
				break
			}
		}
		if !selected {
			pf.Bench = append(pf.Bench, PartySlotConfig{Agent: a.Name})
		}
	}

	SaveParty(pf)

	party := m.buildParty(pf)
	m.parties = append(m.parties, party)
	m.activeParty = len(m.parties) - 1
	m.selectedAgent = 0

	m.wizard = nil
	m.mode = ModeNormal
	m.focus = FocusMainPane

	return m.autoStartPartyAgents()
}

func (m Model) autoStartPartyAgents() (Model, tea.Cmd) {
	p := m.party()
	if p == nil {
		return m, nil
	}

	tw := m.termWidth()
	th := m.termHeight()
	if tw <= 0 || th <= 0 {
		return m, nil
	}

	var cmds []tea.Cmd
	for _, inst := range p.Slots {
		if inst != nil && inst.AgentName != "Empty" && inst.Status == "idle" {
			inst.Task = "Starting..."
			cmds = append(cmds, startAgent(inst, tw, th, m.config, p.Project))
		}
	}

	if len(cmds) > 0 {
		return m, tea.Batch(cmds...)
	}
	return m, nil
}

// ── Wizard Rendering ──────────────────────────────────────────────

func (m Model) renderWizard() string {
	if !m.ready {
		return "Loading..."
	}

	w := m.wizard

	boxWidth := 60
	if w.Step == WizardAddAgents || w.Step == WizardSelectProject {
		boxWidth = 70
	}
	if m.width < boxWidth+4 {
		boxWidth = m.width - 4
	}

	boxStyle := lipgloss.NewStyle().
		Border(lipgloss.DoubleBorder()).
		BorderForeground(colorYellow).
		Padding(1, 3).
		Width(boxWidth).
		Background(colorBgMedium)

	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(colorTextBright)
	stepStyle := lipgloss.NewStyle().Foreground(colorYellow).Bold(true)
	dimStyle := lipgloss.NewStyle().Foreground(colorTextDim)
	hintStyle := lipgloss.NewStyle().Foreground(colorTextDim).MarginTop(1)

	var content string
	switch w.Step {
	case WizardChooseParty:
		content = m.renderWizardChoose(titleStyle, stepStyle, dimStyle, hintStyle)
	case WizardNameParty:
		content = m.renderWizardName(titleStyle, stepStyle, dimStyle, hintStyle)
	case WizardSelectProject:
		content = m.renderWizardProject(titleStyle, stepStyle, dimStyle, hintStyle)
	case WizardAddAgents:
		content = m.renderWizardAgentList(titleStyle, stepStyle, dimStyle, hintStyle)
	case WizardFinalize:
		content = m.renderWizardReview(titleStyle, stepStyle, dimStyle, hintStyle)
	}

	box := boxStyle.Render(content)

	return lipgloss.NewStyle().
		Width(m.width).
		Height(m.height).
		Align(lipgloss.Center, lipgloss.Center).
		Background(colorBgDark).
		Render(box)
}

func (m Model) renderWizardChoose(title, step, dim, hint lipgloss.Style) string {
	w := m.wizard
	var lines []string

	lines = append(lines, title.Render("⚔️  AGENT FORGE"))
	lines = append(lines, "")
	lines = append(lines, step.Render("Select a Party"))
	lines = append(lines, "")

	for i, p := range m.parties {
		prefix := "  "
		style := dim
		if i == w.Cursor {
			prefix = "> "
			style = lipgloss.NewStyle().Foreground(colorTextBright).Bold(true)
		}
		projName := filepath.Base(p.Project)
		lines = append(lines, style.Render(fmt.Sprintf("%s%-12s (%s)", prefix, p.Name, projName)))
	}

	lines = append(lines, "")

	createIdx := len(m.parties)
	style := lipgloss.NewStyle().Foreground(colorGreen)
	prefix := "  "
	if w.Cursor == createIdx {
		prefix = "> "
		style = style.Bold(true)
	}
	lines = append(lines, style.Render(prefix+"+ Create New Party"))

	lines = append(lines, "")
	lines = append(lines, hint.Render("↑↓:navigate  enter:select  esc:quit"))

	return strings.Join(lines, "\n")
}

func (m Model) renderWizardName(title, step, dim, hint lipgloss.Style) string {
	w := m.wizard
	var lines []string

	lines = append(lines, title.Render("⚔️  NEW PARTY"))
	lines = append(lines, "")
	lines = append(lines, step.Render("Step 1/4: Name your party"))
	lines = append(lines, "")

	inputStyle := lipgloss.NewStyle().
		Foreground(colorTextBright).
		Background(colorBgLight).
		Padding(0, 1)

	display := w.TextBuf + "█"
	lines = append(lines, dim.Render("Name: ")+inputStyle.Render(display))

	lines = append(lines, "")
	lines = append(lines, hint.Render("type:name  enter:confirm  esc:back"))

	return strings.Join(lines, "\n")
}

func (m Model) renderWizardProject(title, step, dim, hint lipgloss.Style) string {
	w := m.wizard
	var lines []string

	lines = append(lines, title.Render("⚔️  NEW PARTY"))
	lines = append(lines, "")
	lines = append(lines, step.Render("Step 2/4: Project directory"))
	lines = append(lines, "")

	pathStyle := lipgloss.NewStyle().
		Foreground(colorTextBright).
		Bold(true).
		Background(colorBgLight).
		Padding(0, 1)
	lines = append(lines, pathStyle.Render(w.Project))
	lines = append(lines, "")

	totalEntries := 2 + len(w.DirEntries)
	maxVisible := 12
	scrollOffset := 0
	if w.Cursor >= maxVisible {
		scrollOffset = w.Cursor - maxVisible + 1
	}

	for i := scrollOffset; i < totalEntries && i < scrollOffset+maxVisible; i++ {
		prefix := "  "
		style := dim
		if i == w.Cursor {
			prefix = "> "
			style = lipgloss.NewStyle().Foreground(colorTextBright).Bold(true)
		}

		var label string
		switch {
		case i == 0:
			label = "[Select this directory]"
			if i == w.Cursor {
				style = lipgloss.NewStyle().Foreground(colorGreen).Bold(true)
			} else {
				style = lipgloss.NewStyle().Foreground(colorGreen)
			}
		case i == 1:
			label = "../"
		default:
			label = w.DirEntries[i-2] + "/"
		}

		lines = append(lines, style.Render(prefix+label))
	}

	if totalEntries > maxVisible {
		scrollInfo := fmt.Sprintf("  (%d/%d)", w.Cursor+1, totalEntries)
		lines = append(lines, dim.Render(scrollInfo))
	}

	lines = append(lines, "")
	lines = append(lines, hint.Render("↑↓:navigate  enter:open/select  backspace:parent  esc:back"))

	return strings.Join(lines, "\n")
}

func (m Model) renderWizardAgentList(title, step, dim, hint lipgloss.Style) string {
	w := m.wizard
	var lines []string

	lines = append(lines, title.Render("⚔️  NEW PARTY"))
	lines = append(lines, "")
	lines = append(lines, step.Render(fmt.Sprintf("Step 3/4: Select agents (%d/%d)", len(w.SelectedAgents), MaxPartySlots)))
	lines = append(lines, "")

	// Build agent list lines
	var listLines []string
	for i, a := range m.config.Agents {
		selected := false
		for _, n := range w.SelectedAgents {
			if n == a.Name {
				selected = true
				break
			}
		}

		prefix := "  "
		if i == w.Cursor {
			prefix = "> "
		}

		check := "[ ]"
		nameStyle := dim
		if selected {
			check = "[x]"
			nameStyle = lipgloss.NewStyle().Foreground(colorTextBright)
		}
		if i == w.Cursor {
			nameStyle = nameStyle.Bold(true)
		}

		className := a.Class
		if len(className) > 0 {
			className = strings.ToUpper(className[:1]) + className[1:]
		}

		line := fmt.Sprintf("%s%s %-10s %s", prefix, check, a.Name, className)
		rendered := nameStyle.Render(line)
		// Pad to fixed width for consistent Kitty overlay positioning
		vis := lipgloss.Width(rendered)
		if vis < wizardListWidth {
			rendered += strings.Repeat(" ", wizardListWidth-vis)
		}
		listLines = append(listLines, rendered)
	}

	// Build avatar preview for highlighted agent
	listBlock := strings.Join(listLines, "\n")
	avatarBlock := m.wizardAgentPreview(w.Cursor)

	if avatarBlock != "" {
		combined := lipgloss.JoinHorizontal(lipgloss.Center, listBlock, "  ", avatarBlock)
		lines = append(lines, combined)
	} else {
		lines = append(lines, listBlock)
	}

	lines = append(lines, "")

	enterHint := "enter:continue"
	if len(w.SelectedAgents) == 0 {
		enterHint = "(select at least 1)"
	}
	lines = append(lines, hint.Render(fmt.Sprintf("↑↓:navigate  space:toggle  %s  esc:back", enterHint)))

	return strings.Join(lines, "\n")
}

// Wizard avatar layout constants.
const (
	wizardAvatarCols = 16
	wizardAvatarRows = 8
	wizardListWidth  = 28
)

// wizardAgentPreview returns a placeholder block for the Kitty avatar overlay
// with agent name and class below.
func (m Model) wizardAgentPreview(idx int) string {
	if idx < 0 || idx >= len(m.config.Agents) || avatarImage == nil {
		return ""
	}
	a := m.config.Agents[idx]

	// Reserve blank space for Kitty graphics overlay
	avatarLines := make([]string, wizardAvatarRows)
	for r := range avatarLines {
		avatarLines[r] = strings.Repeat(" ", wizardAvatarCols)
	}
	preview := strings.Join(avatarLines, "\n")

	nameStyle := lipgloss.NewStyle().
		Foreground(colorTextBright).
		Bold(true).
		Width(wizardAvatarCols).
		Align(lipgloss.Center)
	classStyle := lipgloss.NewStyle().
		Foreground(colorTextDim).
		Width(wizardAvatarCols).
		Align(lipgloss.Center)

	className := a.Class
	if len(className) > 0 {
		className = strings.ToUpper(className[:1]) + className[1:]
	}

	return lipgloss.JoinVertical(lipgloss.Center,
		preview,
		nameStyle.Render(a.Name),
		classStyle.Render(className),
	)
}

// renderWizardKittyOverlay renders the Kitty graphics avatar for the wizard agent selection.
func (m Model) renderWizardKittyOverlay() string {
	if m.wizard == nil || m.wizard.Step != WizardAddAgents {
		return ""
	}
	w := m.wizard
	if w.Cursor < 0 || w.Cursor >= len(m.config.Agents) || avatarImage == nil {
		return ""
	}

	a := m.config.Agents[w.Cursor]
	tint := color.RGBA{a.Tint[0], a.Tint[1], a.Tint[2], 255}
	b64 := encodeKittyAvatar(avatarImage, tint)
	if b64 == "" {
		return ""
	}

	// Calculate box dimensions for positioning
	boxWidth := 70
	if m.width < boxWidth+4 {
		boxWidth = m.width - 4
	}

	// Content height: header(4) + combined block + blank(1) + hint(1)
	agentCount := len(m.config.Agents)
	avatarBlockH := wizardAvatarRows + 2 // avatar + name + class
	combinedH := agentCount
	if avatarBlockH > combinedH {
		combinedH = avatarBlockH
	}
	contentH := 4 + combinedH + 2
	boxH := contentH + 4 // border(2) + padding(2)

	// Box position on screen (1-indexed for ANSI escape sequences)
	boxTopRow := (m.height-boxH)/2 + 1
	boxLeftCol := (m.width-boxWidth)/2 + 1

	// Avatar centering within combined block
	centerOffset := 0
	if combinedH > avatarBlockH {
		centerOffset = (combinedH - avatarBlockH) / 2
	}

	// Avatar screen position: border(1) + padding(1) + header(4) + centering
	avatarRow := boxTopRow + 2 + 4 + centerOffset
	// Column: border(1) + padding(3) + list width + spacer(2)
	avatarCol := boxLeftCol + 4 + wizardListWidth + 2

	var buf strings.Builder
	buf.WriteString("\x1b_Ga=d,d=a,q=2\x1b\\") // clear previous images
	buf.WriteString(fmt.Sprintf("\x1b7\x1b[%d;%dH", avatarRow, avatarCol))
	buf.WriteString(kittyImageSeq(b64, wizardAvatarCols, wizardAvatarRows))
	buf.WriteString("\x1b8")

	return buf.String()
}

func (m Model) renderWizardReview(title, step, dim, hint lipgloss.Style) string {
	w := m.wizard
	var lines []string

	lines = append(lines, title.Render("⚔️  NEW PARTY"))
	lines = append(lines, "")
	lines = append(lines, step.Render("Step 4/4: Review & Launch"))
	lines = append(lines, "")

	textStyle := lipgloss.NewStyle().Foreground(colorText)
	lines = append(lines, textStyle.Render(fmt.Sprintf("  Party:   %s", w.Name)))
	lines = append(lines, textStyle.Render(fmt.Sprintf("  Project: %s", w.Project)))
	lines = append(lines, "")

	lines = append(lines, step.Render("  Agents:"))
	for i, name := range w.SelectedAgents {
		class := ""
		for _, a := range m.config.Agents {
			if a.Name == name {
				class = a.Class
				if len(class) > 0 {
					class = strings.ToUpper(class[:1]) + class[1:]
				}
				break
			}
		}
		lines = append(lines, textStyle.Render(
			fmt.Sprintf("    %d. %s (%s)", i+1, name, class)))
	}

	lines = append(lines, "")
	lines = append(lines, lipgloss.NewStyle().Foreground(colorGreen).Render(
		"  All agents will start automatically."))

	lines = append(lines, "")
	lines = append(lines, hint.Render("enter:Finalize Party  esc:back"))

	return strings.Join(lines, "\n")
}
