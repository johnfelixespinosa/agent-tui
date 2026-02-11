package main

import (
	"fmt"
	"path/filepath"
	"strings"

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

// ── View ───────────────────────────────────────────────────────────

func (m Model) View() string {
	if !m.ready {
		return "Loading..."
	}

	if m.deleteConfirm {
		return m.renderDeleteConfirm()
	}

	if m.wizard != nil {
		return m.renderWizard() + m.renderWizardKittyOverlay()
	}

	// Header
	header := m.renderHeader()

	// Left panel + Main pane (left panel's BorderRight provides the divider)
	leftPanel := m.renderLeftPanel()
	mainPane := m.renderMainPane()

	body := lipgloss.JoinHorizontal(lipgloss.Top, leftPanel, mainPane)

	// Status bar
	statusBar := m.renderStatusBar()

	view := lipgloss.JoinVertical(lipgloss.Left, header, body, statusBar)

	// Kitty graphics overlay
	view += m.renderKittyOverlay()

	return view
}

// ── Header ─────────────────────────────────────────────────────────

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
	_, _, _, _, ph, _ := m.cardLayout()
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

// ── Party Bar ──────────────────────────────────────────────────────

func (m Model) renderPartyBar() string {
	p := m.party()
	if p == nil {
		return ""
	}

	cardWidth, avatarCols, avatarRows, cardHeight, _, cardsPerRow := m.cardLayout()

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

		sc := statusColor(displayInst.Status)

		// Avatar placeholder for Kitty overlay (centered with margin)
		var avatar string
		if displayInst.kittyB64 != "" {
			avatarLines := make([]string, avatarRows)
			for r := range avatarLines {
				avatarLines[r] = strings.Repeat(" ", avatarCols)
			}
			avatar = strings.Join(avatarLines, "\n")
		} else {
			avatar = renderHalfBlockAvatar(displayInst.avatarImg, avatarCols, avatarRows)
		}

		nameStyle := lipgloss.NewStyle().Bold(true).Foreground(colorTextBright)
		classStyle := lipgloss.NewStyle().Foreground(colorTextDim)
		statStyle := lipgloss.NewStyle().Foreground(sc)

		// Get class display name
		className := displayInst.ClassName
		if cls := m.config.Classes[className]; cls != nil {
			// Capitalize first letter
			if len(className) > 0 {
				className = strings.ToUpper(className[:1]) + className[1:]
			}
		}

		// HP bar (context window usage)
		hpBar := renderHPBar(displayInst, cardWidth-2)

		content := lipgloss.JoinVertical(
			lipgloss.Center,
			avatar,
			nameStyle.Render(displayInst.AgentName),
			classStyle.Render(className),
			statStyle.Render(strings.ToUpper(displayInst.Status)),
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

	return lipgloss.NewStyle().
		Background(colorBgDark).
		Padding(0, 1).
		Render(
			lipgloss.JoinHorizontal(lipgloss.Center, partyLabel, " ", cardsBlock),
		)
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
		hints = "1:great  2:normal  3:rough  esc:skip"
	default:
		switch m.focus {
		case FocusLeftPanel:
			hints = "↑↓:party  n:new  d:delete  enter:switch  tab:focus"
		case FocusMainPane:
			hints = "s:start  i:insert  x:stop  enter:sheet  space:swap  ←→:agent  tab:focus"
		case FocusPartyBar:
			hints = "←→:agent  enter:sheet  s:start  tab:focus"
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

func (m Model) renderKittyOverlay() string {
	if m.mode == ModeCharSheet || m.mode == ModeCheckout {
		return ""
	}

	p := m.party()
	if p == nil {
		return ""
	}

	var buf strings.Builder
	// Delete all previous images
	buf.WriteString("\x1b_Ga=d,d=a,q=2\x1b\\")

	cw, avatarCols, avatarRows, cardHeight, _, cardsPerRow := m.cardLayout()
	th := m.termHeight()

	// Row: header(1) + terminal top border(1) + termHeight + terminal bottom border(1)
	// + party bar padding top(0) + card top border(1) + 1 for content start
	avatarRowBase := 1 + 1 + th + 1 + 1 + 1 // = th + 5

	// Column: leftPanel(leftPanelWidth) + border(1) + party bar padding(1)
	// + party label(1) + margin(1) + space(1) + card border(1) + avatar margin(1)
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

func renderHPBar(inst *AgentInstance, width int) string {
	if width < 4 {
		return ""
	}

	barWidth := width
	if barWidth > 20 {
		barWidth = 20
	}

	var hpFraction float64
	switch inst.Status {
	case "running":
		hpFraction = 1.0 - float64(inst.ContextBytes)/float64(contextBytesMax)
		if hpFraction < 0 {
			hpFraction = 0
		}
	case "exited":
		if inst.ContextBytes > 0 {
			hpFraction = 1.0 - float64(inst.ContextBytes)/float64(contextBytesMax)
			if hpFraction < 0 {
				hpFraction = 0
			}
		} else {
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
	return lipgloss.NewStyle().Foreground(barColor).Render(bar)
}
