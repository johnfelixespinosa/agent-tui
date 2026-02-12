package main

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

// Stoneshard color palette
var (
	colorBgDark     = lipgloss.Color("#1a1614")
	colorBgMedium   = lipgloss.Color("#2d2520")
	colorBgLight    = lipgloss.Color("#3d342c")
	colorBorder     = lipgloss.Color("#5c4f43")
	colorBorderGold = lipgloss.Color("#c9a959")
	colorText       = lipgloss.Color("#e8d5a3")
	colorTextDim    = lipgloss.Color("#8a7a68")
	colorTextBright = lipgloss.Color("#fff8e7")
	colorGreen      = lipgloss.Color("#4a7c3f")
	colorRed        = lipgloss.Color("#a63d3d")
	colorBlue       = lipgloss.Color("#3d5a7c")
	colorYellow     = lipgloss.Color("#c9a959")
)

// Pre-allocated styles for hot render paths.
var (
	styleNameBright = lipgloss.NewStyle().Bold(true).Foreground(colorTextBright)
	styleTextDim    = lipgloss.NewStyle().Foreground(colorTextDim)
	styleText       = lipgloss.NewStyle().Foreground(colorText)
	styleYellowBold = lipgloss.NewStyle().Foreground(colorYellow).Bold(true)
	styleYellow     = lipgloss.NewStyle().Foreground(colorYellow)
	styleGreen      = lipgloss.NewStyle().Foreground(colorGreen)
)

func statusColor(status string) lipgloss.Color {
	switch status {
	case "running":
		return colorGreen
	case "exited":
		return colorRed
	case "idle":
		return colorTextDim
	}
	return colorTextDim
}

// displayStatus returns a human-readable status and color for an agent.
func displayStatus(inst *AgentInstance) (string, lipgloss.Color) {
	switch inst.Status {
	case "running":
		if time.Since(inst.lastOutputAt) > 3*time.Second {
			return "IDLE", colorYellow
		}
		return "WORKING", colorGreen
	case "exited":
		return "EXITED", colorRed
	default:
		return "STANDBY", colorTextDim
	}
}

// ── View ───────────────────────────────────────────────────────────

func (m Model) View() string {
	if !m.ready {
		return "Loading..."
	}

	if m.deleteConfirm {
		return m.renderDeleteConfirm()
	}

	if m.mode == ModeCommandPalette {
		return m.renderCommandPalette()
	}

	if m.wizard != nil {
		return m.renderWizard() + m.renderWizardKittyOverlay()
	}

	// Left panel + Main pane (left panel's BorderRight provides the divider)
	leftPanel := m.renderLeftPanel()
	mainPane := m.renderMainPane()

	body := lipgloss.JoinHorizontal(lipgloss.Top, leftPanel, mainPane)
	if m.showGitPanel {
		gitPanel := m.renderGitPanel()
		body = lipgloss.JoinHorizontal(lipgloss.Top, leftPanel, mainPane, gitPanel)
	}

	// Status bar
	statusBar := m.renderStatusBar()

	view := lipgloss.JoinVertical(lipgloss.Left, body, statusBar)

	// Kitty graphics overlay
	view += m.renderKittyOverlay()

	return view
}

// ── Header ─────────────────────────────────────────────────────────

func (m Model) renderCommandPalette() string {
	paletteWidth := 50
	if m.width < paletteWidth+4 {
		paletteWidth = m.width - 4
	}

	inputStyle := lipgloss.NewStyle().
		Foreground(colorTextBright).
		Background(colorBgLight).
		Width(paletteWidth - 6).
		Padding(0, 1)

	input := inputStyle.Render(": " + m.cmdPaletteInput + "█")

	actions := m.filteredPaletteActions()
	maxVisible := 12
	if len(actions) < maxVisible {
		maxVisible = len(actions)
	}

	var lines []string
	for i := 0; i < maxVisible; i++ {
		prefix := "  "
		style := lipgloss.NewStyle().Foreground(colorTextDim)
		if i == m.cmdPaletteCursor {
			prefix = "> "
			style = lipgloss.NewStyle().Foreground(colorTextBright).Bold(true)
		}
		lines = append(lines, style.Render(prefix+actions[i].Label))
	}
	if len(actions) == 0 {
		lines = append(lines, lipgloss.NewStyle().Foreground(colorTextDim).Render("  (no matches)"))
	}
	if len(actions) > maxVisible {
		lines = append(lines, lipgloss.NewStyle().Foreground(colorTextDim).
			Render(fmt.Sprintf("  ... %d more", len(actions)-maxVisible)))
	}

	list := strings.Join(lines, "\n")

	box := lipgloss.NewStyle().
		Width(paletteWidth).
		Padding(1, 2).
		Border(lipgloss.DoubleBorder()).
		BorderForeground(colorYellow).
		Background(colorBgMedium).
		Render(lipgloss.JoinVertical(lipgloss.Left, input, "", list))

	return lipgloss.NewStyle().
		Width(m.width).
		Height(m.height).
		Align(lipgloss.Center, lipgloss.Top).
		PaddingTop(2).
		Background(colorBgDark).
		Render(box)
}

func (m Model) renderHeader() string {
	modeStr := "NORMAL"
	modeColor := colorTextDim
	switch m.mode {
	case ModeInsert:
		modeStr = "INSERT"
		modeColor = colorGreen
	case ModeSwap:
		modeStr = "SWAP"
		modeColor = colorYellow
	case ModeCharSheet:
		modeStr = "SHEET"
		modeColor = colorBlue
	case ModeCheckout:
		modeStr = "CHECKOUT"
		modeColor = colorYellow
	case ModeCommandPalette:
		modeStr = "COMMAND"
		modeColor = colorYellow
	}

	modeIndicator := lipgloss.NewStyle().
		Foreground(modeColor).
		Bold(true).
		Render("[" + modeStr + "]")

	return lipgloss.NewStyle().
		Bold(true).
		Foreground(colorTextBright).
		Background(colorBgMedium).
		Width(m.width).
		Padding(0, 2).
		Render(
			"⚔️  AGENT FORGE" +
				strings.Repeat(" ", max(0, m.width-45)) +
				modeIndicator,
		)
}

// ── Left Panel ─────────────────────────────────────────────────────

func (m Model) renderLeftPanel() string {
	panelBorder := colorBorder
	if m.focus == FocusLeftPanel {
		panelBorder = colorBorderGold
	}

	titleStyle := lipgloss.NewStyle().
		Foreground(colorYellow).
		Bold(true)

	// Build party list
	var lines []string
	lines = append(lines, titleStyle.Render(" PARTY SELECT"))
	lines = append(lines, "")

	for i, p := range m.parties {
		prefix := "  "
		nameStyle := lipgloss.NewStyle().Foreground(colorTextDim)
		if i == m.activeParty {
			prefix = "> "
			nameStyle = lipgloss.NewStyle().Foreground(colorTextBright).Bold(true)
		}

		// Party name (truncate to fit)
		name := p.Name
		if len(name) > leftPanelWidth-3 {
			name = name[:leftPanelWidth-3]
		}
		lines = append(lines, nameStyle.Render(prefix+name))

		// Project basename (dim)
		projName := filepath.Base(p.Project)
		if len(projName) > leftPanelWidth-3 {
			projName = projName[:leftPanelWidth-3]
		}
		lines = append(lines,
			lipgloss.NewStyle().Foreground(colorTextDim).Render("  "+projName))
		lines = append(lines, "")
	}

	// "+ New Party" button
	lines = append(lines, lipgloss.NewStyle().Foreground(colorGreen).Render("  + New Party"))

	// Calculate total body height: terminal(border+content) + party bar
	ph := m.layout.PartyHeight
	th := m.termHeight()
	bodyHeight := th + 2 + ph // terminal with border + party bar

	// Pad to fill height
	for len(lines) < bodyHeight {
		lines = append(lines, "")
	}
	if len(lines) > bodyHeight {
		lines = lines[:bodyHeight]
	}

	// Pad each line to panel width
	for i, l := range lines {
		vis := lipgloss.Width(l)
		if vis < leftPanelWidth {
			lines[i] = l + strings.Repeat(" ", leftPanelWidth-vis)
		}
	}

	content := strings.Join(lines, "\n")

	return lipgloss.NewStyle().
		Width(leftPanelWidth).
		Height(bodyHeight).
		BorderRight(true).
		BorderStyle(lipgloss.NormalBorder()).
		BorderForeground(panelBorder).
		Background(colorBgDark).
		Render(content)
}

// ── Main Pane ──────────────────────────────────────────────────────

func (m Model) renderMainPane() string {
	// Main pane has: terminal + party bar
	terminal := m.renderTerminal()
	partyBar := m.renderPartyBar()

	return lipgloss.JoinVertical(lipgloss.Left, terminal, partyBar)
}

func (m Model) renderTerminal() string {
	inst := m.agent()
	tw := m.termWidth()
	th := m.termHeight()

	termBorderColor := colorBorder
	if inst != nil {
		r, g, b := inst.Tint.R, inst.Tint.G, inst.Tint.B
		if m.focus == FocusMainPane || m.mode == ModeInsert {
			termBorderColor = lipgloss.Color(fmt.Sprintf("#%02x%02x%02x", r, g, b))
		} else {
			termBorderColor = lipgloss.Color(fmt.Sprintf("#%02x%02x%02x", r/2, g/2, b/2))
		}
	} else if m.focus == FocusMainPane {
		termBorderColor = colorBorderGold
	}

	// Character sheet overlay
	if m.mode == ModeCharSheet && inst != nil {
		return m.renderCharSheet(inst, tw, th)
	}

	// Checkout modal overlay
	if m.mode == ModeCheckout && m.checkoutAgent != nil {
		return m.renderCheckoutModal(tw, th)
	}

	switch {
	case inst == nil:
		return m.renderEmptyTerminal(tw, th, termBorderColor, "No agent selected")
	case inst.Status == "running" && inst.emulator != nil:
		screen := strings.ReplaceAll(inst.emulator.Render(), "\r\n", "\n")
		return lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(termBorderColor).
			Render(screen)
	case inst.Status == "exited":
		return m.renderEmptyTerminal(tw, th, termBorderColor, "Process exited. Press 's' to restart.")
	default:
		return m.renderEmptyTerminal(tw, th, termBorderColor, "Press 's' to start claude")
	}
}

func (m Model) renderEmptyTerminal(tw, th int, borderColor lipgloss.Color, msg string) string {
	placeholder := lipgloss.NewStyle().
		Foreground(colorTextDim).
		Width(tw).
		Height(th).
		Align(lipgloss.Center, lipgloss.Center).
		Render(msg)

	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(borderColor).
		Width(tw).
		Height(th).
		Render(placeholder)
}

func (m Model) renderCheckoutModal(tw, th int) string {
	switch m.checkoutStep {
	case 1:
		return m.renderScrollModal(tw, th)
	case 2:
		return m.renderHandoffModal(tw, th)
	case 3:
		return m.renderWorktreeDisposition(tw, th)
	}

	agent := m.checkoutAgent
	name := agent.AgentName
	class := agent.ClassName

	// Get current XP info
	entry := m.roster.Agents[name]
	xp := 0
	level := 1
	if entry != nil {
		xp = entry.XP
		level = entry.Level
	}

	modal := lipgloss.NewStyle().
		Width(40).
		Padding(1, 2).
		Border(lipgloss.DoubleBorder()).
		BorderForeground(colorYellow).
		Foreground(colorText).
		Background(colorBgMedium).
		Align(lipgloss.Center)

	title := lipgloss.NewStyle().Bold(true).Foreground(colorTextBright).
		Render(fmt.Sprintf("Session Ended: %s (%s)", name, class))
	lvl := lipgloss.NewStyle().Foreground(colorTextDim).
		Render(fmt.Sprintf("Lv.%d  XP: %d", level, xp))
	question := lipgloss.NewStyle().Foreground(colorText).
		Render("How did it go?")
	options := lipgloss.NewStyle().Foreground(colorYellow).
		Render("[1] Great  (+50 XP)\n[2] Normal (+20 XP)\n[3] Rough  (+5 XP)\n[Esc] Skip")

	content := lipgloss.JoinVertical(lipgloss.Center, title, lvl, "", question, "", options)
	box := modal.Render(content)

	// Center in terminal area
	return lipgloss.NewStyle().
		Width(tw + 2).
		Height(th + 2).
		Align(lipgloss.Center, lipgloss.Center).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colorBorder).
		Render(box)
}

func (m Model) renderScrollModal(tw, th int) string {
	modal := lipgloss.NewStyle().
		Width(44).
		Padding(1, 2).
		Border(lipgloss.DoubleBorder()).
		BorderForeground(colorYellow).
		Foreground(colorText).
		Background(colorBgMedium).
		Align(lipgloss.Center)

	title := lipgloss.NewStyle().Bold(true).Foreground(colorTextBright).
		Render("Save as Scroll?")
	desc := lipgloss.NewStyle().Foreground(colorTextDim).
		Render("Save this session's prompt as\na reusable skill (Scroll)")

	inputStyle := lipgloss.NewStyle().
		Foreground(colorTextBright).
		Background(colorBgLight).
		Padding(0, 1)
	input := inputStyle.Render(m.scrollNameBuf + "█")

	hint := lipgloss.NewStyle().Foreground(colorTextDim).
		Render("type:name  enter:save  esc:skip")

	content := lipgloss.JoinVertical(lipgloss.Center, title, "", desc, "", input, "", hint)
	box := modal.Render(content)

	return lipgloss.NewStyle().
		Width(tw + 2).
		Height(th + 2).
		Align(lipgloss.Center, lipgloss.Center).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colorBorder).
		Render(box)
}

func (m Model) renderHandoffModal(tw, th int) string {
	agent := m.checkoutAgent
	p := m.party()

	modal := lipgloss.NewStyle().
		Width(44).
		Padding(1, 2).
		Border(lipgloss.DoubleBorder()).
		BorderForeground(colorYellow).
		Foreground(colorText).
		Background(colorBgMedium).
		Align(lipgloss.Center)

	title := lipgloss.NewStyle().Bold(true).Foreground(colorTextBright).
		Render(fmt.Sprintf("Handoff from %s", agent.AgentName))
	question := lipgloss.NewStyle().Foreground(colorText).
		Render("Pass context to another agent?")

	var targetLines []string
	if p != nil {
		idx := 0
		for _, inst := range p.Slots {
			if inst != nil && inst != agent {
				prefix := "  "
				style := lipgloss.NewStyle().Foreground(colorTextDim)
				if idx == m.handoffTarget {
					prefix = "> "
					style = lipgloss.NewStyle().Foreground(colorTextBright).Bold(true)
				}
				status := strings.ToUpper(inst.Status)
				targetLines = append(targetLines,
					style.Render(fmt.Sprintf("%s%s (%s)", prefix, inst.AgentName, status)))
				idx++
			}
		}
	}
	if len(targetLines) == 0 {
		targetLines = append(targetLines,
			lipgloss.NewStyle().Foreground(colorTextDim).Render("  (no other agents)"))
	}

	targets := strings.Join(targetLines, "\n")
	hint := lipgloss.NewStyle().Foreground(colorTextDim).
		Render("↑↓:select  enter:handoff  esc:skip")

	content := lipgloss.JoinVertical(lipgloss.Center, title, "", question, "", targets, "", hint)
	box := modal.Render(content)

	return lipgloss.NewStyle().
		Width(tw + 2).
		Height(th + 2).
		Align(lipgloss.Center, lipgloss.Center).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colorBorder).
		Render(box)
}

func (m Model) renderWorktreeDisposition(tw, th int) string {
	agent := m.checkoutAgent

	modal := lipgloss.NewStyle().
		Width(44).
		Padding(1, 2).
		Border(lipgloss.DoubleBorder()).
		BorderForeground(colorYellow).
		Foreground(colorText).
		Background(colorBgMedium).
		Align(lipgloss.Center)

	title := lipgloss.NewStyle().Bold(true).Foreground(colorTextBright).
		Render("Git Worktree")
	branchInfo := lipgloss.NewStyle().Foreground(colorTextDim).
		Render(fmt.Sprintf("Branch: %s", agent.Branch))
	question := lipgloss.NewStyle().Foreground(colorText).
		Render("What to do with this branch?")
	options := lipgloss.NewStyle().Foreground(colorYellow).
		Render("[1] Merge to main\n[2] Keep on branch\n[3] Discard changes\n[Esc] Keep (default)")

	content := lipgloss.JoinVertical(lipgloss.Center, title, branchInfo, "", question, "", options)
	box := modal.Render(content)

	return lipgloss.NewStyle().
		Width(tw + 2).
		Height(th + 2).
		Align(lipgloss.Center, lipgloss.Center).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colorBorder).
		Render(box)
}

// ── Party Bar ──────────────────────────────────────────────────────

func (m Model) renderPartyBar() string {
	p := m.party()
	if p == nil {
		return ""
	}

	cardWidth := m.layout.CardWidth
	avatarCols := m.layout.AvatarCols
	avatarRows := m.layout.AvatarRows
	cardHeight := m.layout.CardHeight
	cardsPerRow := m.layout.CardsPerRow

	barBorderColor := colorBorder
	if m.focus == FocusPartyBar {
		barBorderColor = colorBorderGold
	}

	cardStyle := lipgloss.NewStyle().
		Width(cardWidth).
		Height(cardHeight).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(barBorderColor).
		Align(lipgloss.Center, lipgloss.Center)

	swapCardStyle := cardStyle.
		BorderForeground(colorYellow).
		Background(colorBgMedium)

	var cards []string
	for i, inst := range p.Slots {
		if inst == nil {
			continue
		}
		displayInst := inst
		style := cardStyle
		if i == m.selectedAgent {
			if m.mode == ModeSwap && len(p.Bench) > 0 {
				displayInst = p.Bench[m.swapIndex]
				style = swapCardStyle
			} else {
				// Tint selected card border with agent's color
				r, g, b := inst.Tint.R, inst.Tint.G, inst.Tint.B
				tintColor := lipgloss.Color(fmt.Sprintf("#%02x%02x%02x", r, g, b))
				style = cardStyle.
					BorderForeground(tintColor).
					Background(colorBgLight)
			}
		}

		// Avatar placeholder for Kitty overlay (centered with margin)
		var avatar string
		if displayInst.kittyB64 != "" {
			avatarLines := make([]string, avatarRows)
			for r := range avatarLines {
				avatarLines[r] = strings.Repeat(" ", avatarCols)
			}
			avatar = strings.Join(avatarLines, "\n")
		} else {
			avatar = displayInst.halfBlockAvatar(avatarCols, avatarRows)
		}

		nameStyle := styleNameBright
		classStyle := styleTextDim

		// Class display name (title case)
		className := strings.Title(displayInst.ClassName)

		// Level from roster
		lvlStr := ""
		if entry := m.roster.Agents[displayInst.AgentName]; entry != nil {
			lvlStr = fmt.Sprintf(" Lv.%d", entry.Level)
		}
		lvlStyle := styleYellow

		// Activity-based status display
		statusText, sc := displayStatus(displayInst)
		statStyle := lipgloss.NewStyle().Foreground(sc)

		// HP bar (context window usage)
		hpBar := renderHPBar(displayInst, cardWidth-2)

		content := lipgloss.JoinVertical(
			lipgloss.Center,
			avatar,
			nameStyle.Render(displayInst.AgentName)+lvlStyle.Render(lvlStr),
			classStyle.Render(className),
			statStyle.Render(statusText),
			hpBar,
		)

		cards = append(cards, style.Render(content))
	}

	partyLabel := lipgloss.NewStyle().
		Foreground(colorYellow).
		Bold(true).
		MarginRight(1).
		Render("⚔\nP\nA\nR\nT\nY")

	var rows []string
	for i := 0; i < len(cards); i += cardsPerRow {
		end := i + cardsPerRow
		if end > len(cards) {
			end = len(cards)
		}
		rows = append(rows, lipgloss.JoinHorizontal(lipgloss.Top, cards[i:end]...))
	}
	cardsBlock := lipgloss.JoinVertical(lipgloss.Left, rows...)

	// Project directory footer
	projPath := p.Project
	maxProjW := m.mainPaneWidth() - 6
	if maxProjW < 10 {
		maxProjW = 10
	}
	if len(projPath) > maxProjW {
		projPath = "..." + projPath[len(projPath)-maxProjW+3:]
	}
	projLine := lipgloss.NewStyle().
		Foreground(colorTextDim).
		Render("  " + projPath)

	partyContent := lipgloss.JoinHorizontal(lipgloss.Center, partyLabel, " ", cardsBlock)

	return lipgloss.NewStyle().
		Background(colorBgDark).
		Padding(0, 1).
		Render(
			lipgloss.JoinVertical(lipgloss.Left, partyContent, projLine),
		)
}

// ── Git Panel ──────────────────────────────────────────────────────

func (m Model) renderGitPanel() string {
	if m.gitPanelMode == 1 {
		return m.renderPRPanel()
	}

	ph := m.layout.PartyHeight
	th := m.termHeight()
	bodyHeight := th + 2 + ph // terminal with border + party bar

	contentHeight := bodyHeight - 2 // border top/bottom

	// Clamp scroll
	maxScroll := len(m.gitTreeLines) - contentHeight
	if maxScroll < 0 {
		maxScroll = 0
	}
	if m.gitPanelScroll > maxScroll {
		m.gitPanelScroll = maxScroll
	}

	// Slice visible lines
	start := m.gitPanelScroll
	end := start + contentHeight
	if end > len(m.gitTreeLines) {
		end = len(m.gitTreeLines)
	}

	dirStyle := lipgloss.NewStyle().Foreground(colorYellow)
	fileStyle := lipgloss.NewStyle().Foreground(colorText)

	var lines []string
	for i := start; i < end; i++ {
		line := m.gitTreeLines[i]
		if strings.HasSuffix(line, "/") {
			lines = append(lines, dirStyle.Render(truncLine(line, gitPanelWidth-2)))
		} else {
			lines = append(lines, fileStyle.Render(truncLine(line, gitPanelWidth-2)))
		}
	}

	// Pad remaining height
	for len(lines) < contentHeight {
		lines = append(lines, "")
	}

	content := strings.Join(lines, "\n")

	return lipgloss.NewStyle().
		Width(gitPanelWidth).
		Height(bodyHeight).
		BorderLeft(true).
		BorderStyle(lipgloss.NormalBorder()).
		BorderForeground(colorBorder).
		Background(colorBgDark).
		Render(
			lipgloss.NewStyle().
				Foreground(colorYellow).
				Bold(true).
				Render(" FILES  (g:PRs)") + "\n" + content,
		)
}

func (m Model) renderPRPanel() string {
	ph := m.layout.PartyHeight
	th := m.termHeight()
	bodyHeight := th + 2 + ph

	contentHeight := bodyHeight - 2

	var lines []string
	if m.prLoading {
		lines = append(lines, lipgloss.NewStyle().Foreground(colorTextDim).Render(" Loading..."))
	} else if len(m.prList) == 0 {
		lines = append(lines, lipgloss.NewStyle().Foreground(colorTextDim).Render(" No open PRs"))
	} else {
		// Clamp scroll
		maxScroll := len(m.prList)*3 - contentHeight
		if maxScroll < 0 {
			maxScroll = 0
		}
		if m.gitPanelScroll > maxScroll {
			m.gitPanelScroll = maxScroll
		}

		for _, pr := range m.prList {
			icon := pr.StatusIcon()
			iconColor := colorTextDim
			switch icon {
			case "✓":
				iconColor = colorGreen
			case "✗":
				iconColor = colorRed
			case "●":
				iconColor = colorBlue
			case "○":
				iconColor = colorYellow
			}
			iconStr := lipgloss.NewStyle().Foreground(iconColor).Render(icon)

			numStr := lipgloss.NewStyle().Foreground(colorTextDim).
				Render(fmt.Sprintf("#%d", pr.Number))
			title := truncLine(pr.Title, gitPanelWidth-8)
			titleStr := lipgloss.NewStyle().Foreground(colorText).Render(title)

			branchStr := lipgloss.NewStyle().Foreground(colorTextDim).
				Render("  " + truncLine(pr.Branch, gitPanelWidth-4))

			lines = append(lines, fmt.Sprintf(" %s %s %s", iconStr, numStr, titleStr))
			lines = append(lines, branchStr)
			lines = append(lines, "")
		}
	}

	// Apply scroll
	start := m.gitPanelScroll
	if start > len(lines) {
		start = len(lines)
	}
	lines = lines[start:]

	// Pad
	for len(lines) < contentHeight {
		lines = append(lines, "")
	}
	if len(lines) > contentHeight {
		lines = lines[:contentHeight]
	}

	content := strings.Join(lines, "\n")

	return lipgloss.NewStyle().
		Width(gitPanelWidth).
		Height(bodyHeight).
		BorderLeft(true).
		BorderStyle(lipgloss.NormalBorder()).
		BorderForeground(colorBorder).
		Background(colorBgDark).
		Render(
			lipgloss.NewStyle().
				Foreground(colorYellow).
				Bold(true).
				Render(" PULL REQUESTS  (g:close)") + "\n" + content,
		)
}

func truncLine(s string, maxW int) string {
	if lipgloss.Width(s) <= maxW {
		return s
	}
	// Trim rune by rune
	r := []rune(s)
	for len(r) > 0 && lipgloss.Width(string(r)) > maxW {
		r = r[:len(r)-1]
	}
	return string(r)
}

// ── Status Bar ─────────────────────────────────────────────────────

func (m Model) renderStatusBar() string {
	p := m.party()
	inst := m.agent()

	partyName := ""
	agentName := ""
	agentStatus := ""
	if p != nil {
		partyName = p.Name
	}
	if inst != nil {
		agentName = inst.AgentName
		agentStatus = strings.ToUpper(inst.Status)
	}

	var hints string
	switch m.mode {
	case ModeInsert:
		hints = "esc:normal"
	case ModeSwap:
		benchAgent := ""
		benchLen := 0
		if p != nil {
			benchLen = len(p.Bench)
			if benchLen > 0 {
				benchAgent = p.Bench[m.swapIndex].AgentName
			}
		}
		hints = fmt.Sprintf("←→:cycle (%s %d/%d)  space/enter:confirm  esc:cancel",
			benchAgent, m.swapIndex+1, benchLen)
	case ModeCharSheet:
		hints = "↑↓:navigate  tab:section  space:equip  []:scroll  s:start  esc:close"
	case ModeCheckout:
		switch m.checkoutStep {
		case 0:
			hints = "1:great  2:normal  3:rough  esc:skip"
		case 1:
			hints = "type:name  enter:save  esc:skip"
		case 2:
			hints = "↑↓:select  enter:handoff  esc:skip"
		case 3:
			hints = "1:merge  2:keep  3:discard  esc:keep"
		}
	default:
		switch m.focus {
		case FocusLeftPanel:
			hints = "↑↓:party  n:new  d:delete  enter:switch  tab:focus"
		case FocusMainPane:
			hints = "s:start  i:insert  x:stop  enter:sheet  space:swap  g:files  ←→:agent  tab:focus"
		case FocusPartyBar:
			hints = "←→:agent  enter:sheet  s:start  g:files  tab:focus"
		}
	}

	return lipgloss.NewStyle().
		Background(colorBgMedium).
		Foreground(colorText).
		Width(m.width).
		Padding(0, 2).
		Render(fmt.Sprintf("Party: %s │ Agent: %s │ %s │ %s",
			lipgloss.NewStyle().Bold(true).Render(partyName),
			lipgloss.NewStyle().Bold(true).Render(agentName),
			lipgloss.NewStyle().Foreground(statusColor(strings.ToLower(agentStatus))).Render(agentStatus),
			lipgloss.NewStyle().Foreground(colorTextDim).Render(hints),
		))
}

// ── Kitty Overlay ──────────────────────────────────────────────────

// Package-level key tracking — only clear images when layout actually changes.
var lastOverlayKey string

func (m Model) renderKittyOverlay() string {
	if m.mode == ModeCheckout {
		if lastOverlayKey != "" {
			lastOverlayKey = ""
			return "\x1b_Ga=d,d=a,q=2\x1b\\"
		}
		return ""
	}

	p := m.party()
	if p == nil {
		return ""
	}

	cw := m.layout.CardWidth
	avatarCols := m.layout.AvatarCols
	avatarRows := m.layout.AvatarRows
	cardHeight := m.layout.CardHeight
	cardsPerRow := m.layout.CardsPerRow
	th := m.termHeight()

	// Build a key from factors that affect avatar positions/content
	var keyBuf strings.Builder
	fmt.Fprintf(&keyBuf, "%d:%d:%d:%d:%d:%v:", m.activeParty, th, cw, avatarCols, avatarRows, m.showGitPanel)
	for i := 0; i < MaxPartySlots; i++ {
		if p.Slots[i] != nil {
			keyBuf.WriteString(p.Slots[i].ID)
		}
		keyBuf.WriteByte(',')
	}
	if m.mode == ModeSwap {
		fmt.Fprintf(&keyBuf, "swap:%d:%d", m.selectedAgent, m.swapIndex)
	}
	key := keyBuf.String()

	var buf strings.Builder

	// Always clear before placing — Kitty placements persist across redraws
	// and stale images at old positions cause ghosting on layout changes.
	buf.WriteString("\x1b_Ga=d,d=a,q=2\x1b\\")
	lastOverlayKey = key

	// Always resend images (Kitty placements don't survive screen redraws)
	// Row: terminal top border(1) + termHeight + terminal bottom border(1)
	// + card top border(1) + 1 for content start
	avatarRowBase := 1 + th + 1 + 1 + 1

	panelTotalWidth := leftPanelWidth + 1
	cardAreaStart := panelTotalWidth + 4 + 2

	cardIdx := 0
	for i := 0; i < MaxPartySlots; i++ {
		inst := p.Slots[i]
		if inst == nil {
			continue
		}
		b64 := inst.kittyB64
		if m.mode == ModeSwap && i == m.selectedAgent && len(p.Bench) > 0 {
			b64 = p.Bench[m.swapIndex].kittyB64
		}
		if b64 == "" {
			cardIdx++
			continue
		}

		row := cardIdx / cardsPerRow
		colInRow := cardIdx % cardsPerRow
		avatarRow := avatarRowBase + row*(cardHeight+2)
		col := cardAreaStart + colInRow*(cw+2)

		buf.WriteString(fmt.Sprintf("\x1b7\x1b[%d;%dH", avatarRow, col))
		buf.WriteString(kittyImageSeq(b64, avatarCols, avatarRows))
		buf.WriteString("\x1b8")
		cardIdx++
	}

	return buf.String()
}

// ── Delete Confirmation ───────────────────────────────────────────

func (m Model) renderDeleteConfirm() string {
	p := m.party()
	name := ""
	if p != nil {
		name = p.Name
	}

	boxWidth := 42
	boxStyle := lipgloss.NewStyle().
		Border(lipgloss.DoubleBorder()).
		BorderForeground(colorRed).
		Padding(1, 3).
		Width(boxWidth).
		Background(colorBgMedium)

	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(colorTextBright)
	warnStyle := lipgloss.NewStyle().Foreground(colorRed)
	hintStyle := lipgloss.NewStyle().Foreground(colorYellow)

	content := lipgloss.JoinVertical(lipgloss.Center,
		titleStyle.Render(fmt.Sprintf("Delete \"%s\"?", name)),
		"",
		warnStyle.Render("Running agents will be stopped."),
		"",
		hintStyle.Render("[y] Delete  [n] Cancel"),
	)

	box := boxStyle.Render(content)

	return lipgloss.NewStyle().
		Width(m.width).
		Height(m.height).
		Align(lipgloss.Center, lipgloss.Center).
		Background(colorBgDark).
		Render(box)
}

// ── HP Bar ────────────────────────────────────────────────────────

const contextBytesMax = 1_600_000 // ~200K tokens worth of PTY traffic
const defaultContextMax = 200_000 // default max context tokens

func renderHPBar(inst *AgentInstance, width int) string {
	if width < 4 {
		return ""
	}

	barWidth := width
	if barWidth > 20 {
		barWidth = 20
	}

	var hpFraction float64
	var label string

	switch inst.Status {
	case "running":
		if inst.ContextTokens > 0 {
			// Real token data available
			max := inst.ContextMax
			if max == 0 {
				max = defaultContextMax
			}
			hpFraction = 1.0 - float64(inst.ContextTokens)/float64(max)
			label = fmt.Sprintf(" %dK/%dK", inst.ContextTokens/1000, max/1000)
		} else {
			// Fall back to byte estimate (~4 bytes per token)
			estimatedTokens := inst.ContextBytes / 4
			hpFraction = 1.0 - float64(estimatedTokens)/float64(defaultContextMax)
		}
		if hpFraction < 0 {
			hpFraction = 0
		}
	case "exited":
		if inst.ContextTokens > 0 {
			max := inst.ContextMax
			if max == 0 {
				max = defaultContextMax
			}
			hpFraction = 1.0 - float64(inst.ContextTokens)/float64(max)
			label = fmt.Sprintf(" %dK/%dK", inst.ContextTokens/1000, max/1000)
		} else if inst.ContextBytes > 0 {
			estimatedTokens := inst.ContextBytes / 4
			hpFraction = 1.0 - float64(estimatedTokens)/float64(defaultContextMax)
		} else {
			hpFraction = 0
		}
		if hpFraction < 0 {
			hpFraction = 0
		}
	default: // idle
		hpFraction = 1.0
	}

	filled := int(hpFraction * float64(barWidth))
	if filled > barWidth {
		filled = barWidth
	}

	barColor := colorGreen
	if hpFraction < 0.5 {
		barColor = colorYellow
	}
	if hpFraction < 0.25 {
		barColor = colorRed
	}

	bar := strings.Repeat("█", filled) + strings.Repeat("░", barWidth-filled)
	result := lipgloss.NewStyle().Foreground(barColor).Render(bar)
	if label != "" {
		labelStyle := lipgloss.NewStyle().Foreground(colorTextDim)
		result += labelStyle.Render(label)
	}
	return result
}
