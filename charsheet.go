package main

import (
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// renderCharSheet renders the full character sheet modal in the terminal pane area.
func (m Model) renderCharSheet(inst *AgentInstance, tw, th int) string {
	// Get XP/Level
	entry := m.roster.Agents[inst.AgentName]
	xp := 0
	level := 1
	if entry != nil {
		xp = entry.XP
		level = entry.Level
	}
	nextXP := XPForNextLevel(level)

	className := inst.ClassName
	if len(className) > 0 {
		className = strings.ToUpper(className[:1]) + className[1:]
	}

	isRunning := inst.Status == "running"

	// Compute token estimate
	composed := ComposePrompt(m.config, inst.ClassName, inst.Equipped, inst.Passives, inst.Directives)

	// ── Build Sections ─────────────────────────────────────────

	equippedSection := m.renderEquippedSection(inst)
	availableSection := m.renderAvailableSection(inst)
	statsSection := m.renderStatsSection(inst, className, level, xp, nextXP)

	// ── Layout ─────────────────────────────────────────────────

	leftColWidth := tw/2 - 2
	rightColWidth := tw - leftColWidth - 4

	leftCol := lipgloss.NewStyle().Width(leftColWidth).Render(
		lipgloss.JoinVertical(lipgloss.Left, equippedSection, "", availableSection),
	)
	// Agent bio section (from <name>.md)
	var bioSection string
	if inst.Bio != "" {
		bioSection = m.renderBioSection(inst, rightColWidth)
	}

	rightCol := lipgloss.NewStyle().Width(rightColWidth).Render(
		lipgloss.JoinVertical(lipgloss.Left, statsSection, "", bioSection),
	)

	body := lipgloss.JoinHorizontal(lipgloss.Top, leftCol, "  ", rightCol)

	// Title bar
	title := lipgloss.NewStyle().Bold(true).Foreground(colorTextBright).
		Render(fmt.Sprintf(" %s ", inst.AgentName))
	classLabel := lipgloss.NewStyle().Foreground(colorTextDim).
		Render(fmt.Sprintf(" %s ", className))
	levelLabel := lipgloss.NewStyle().Foreground(colorYellow).
		Render(fmt.Sprintf(" Lv.%d ", level))

	titleBar := fmt.Sprintf("─── %s ──── %s ─── %s ───", title, classLabel, levelLabel)

	// Token bar
	tokenColor := colorGreen
	if composed.TotalTokens > 800 {
		tokenColor = colorYellow
	}
	if composed.TotalTokens > 950 {
		tokenColor = colorRed
	}
	tokenStr := lipgloss.NewStyle().Foreground(tokenColor).
		Render(fmt.Sprintf("~%d tokens", composed.TotalTokens))

	// Pending banner
	var pendingBanner string
	if inst.HasPending && isRunning {
		pendingBanner = lipgloss.NewStyle().
			Foreground(colorYellow).Bold(true).
			Render("  ⚠ Pending — restart to apply")
	}

	// Read-only indicator
	var readonlyBanner string
	if isRunning {
		readonlyBanner = lipgloss.NewStyle().
			Foreground(colorTextDim).Italic(true).
			Render("  (read-only while running)")
	}

	// Footer hints
	hints := lipgloss.NewStyle().Foreground(colorTextDim).
		Render("  ↑↓:navigate  tab:section  space:equip  []:scroll  s:start  esc:close")

	// Compose full sheet
	content := lipgloss.JoinVertical(lipgloss.Left,
		titleBar,
		readonlyBanner,
		pendingBanner,
		"",
		body,
		"",
		tokenStr,
		hints,
	)

	return lipgloss.NewStyle().
		Border(lipgloss.DoubleBorder()).
		BorderForeground(colorBlue).
		Width(tw).
		Height(th).
		Padding(0, 1).
		Render(content)
}

// ── Equipped Section ───────────────────────────────────────────────

func (m Model) renderEquippedSection(inst *AgentInstance) string {
	sectionStyle := lipgloss.NewStyle().Foreground(colorText)
	headerStyle := lipgloss.NewStyle().Foreground(colorYellow).Bold(true)

	isActive := m.csSection == 0

	header := headerStyle.Render("┌─ EQUIPPED ──────────────┐")

	classCfg := m.config.Classes[inst.ClassName]

	var lines []string
	cursor := 0

	// Innate skills (★)
	if classCfg != nil {
		for _, sid := range classCfg.InnateSkills {
			skill := SkillByID(m.config, sid)
			name := sid
			if skill != nil {
				name = skill.Name
			}
			prefix := "  "
			if isActive && cursor == m.csCursor {
				prefix = "> "
			}
			line := fmt.Sprintf("%s★ %s", prefix, name)
			style := lipgloss.NewStyle().Foreground(colorYellow)
			lines = append(lines, style.Render(line))
			cursor++
		}
	}

	// Equipped skills (●)
	for _, sid := range inst.Equipped {
		skill := SkillByID(m.config, sid)
		name := sid
		if skill != nil {
			name = skill.Name
		}
		prefix := "  "
		if isActive && cursor == m.csCursor {
			prefix = "> "
		}
		line := fmt.Sprintf("%s● %s", prefix, name)
		style := lipgloss.NewStyle().Foreground(colorText)
		lines = append(lines, style.Render(line))
		cursor++
	}

	// Empty slots (○)
	emptyCount := MaxEquipSlots - len(inst.Equipped)
	for i := 0; i < emptyCount; i++ {
		prefix := "  "
		if isActive && cursor == m.csCursor {
			prefix = "> "
		}
		line := fmt.Sprintf("%s○ ── empty ──", prefix)
		style := lipgloss.NewStyle().Foreground(colorTextDim)
		lines = append(lines, style.Render(line))
		cursor++
	}

	lines = append(lines, headerStyle.Render("└─────────────────────────┘"))

	return sectionStyle.Render(lipgloss.JoinVertical(lipgloss.Left,
		append([]string{header}, lines...)...,
	))
}

// ── Available Section ──────────────────────────────────────────────

func (m Model) renderAvailableSection(inst *AgentInstance) string {
	headerStyle := lipgloss.NewStyle().Foreground(colorYellow).Bold(true)

	isActive := m.csSection == 1

	header := headerStyle.Render("┌─ AVAILABLE ─────────────┐")

	avail := m.availableSkills(inst)
	sort.Strings(avail)

	var lines []string
	for i, sid := range avail {
		skill := SkillByID(m.config, sid)
		name := sid
		if skill != nil {
			name = skill.Name
		}
		prefix := "  "
		if isActive && i == m.csCursor {
			prefix = "> "
		}
		line := fmt.Sprintf("%s%s", prefix, name)
		style := lipgloss.NewStyle().Foreground(colorTextDim)
		lines = append(lines, style.Render(line))
	}

	if len(lines) == 0 {
		lines = append(lines, lipgloss.NewStyle().Foreground(colorTextDim).Render("  (all equipped)"))
	}

	lines = append(lines, headerStyle.Render("└─────────────────────────┘"))

	return lipgloss.JoinVertical(lipgloss.Left,
		append([]string{header}, lines...)...,
	)
}

// ── Stats Section ──────────────────────────────────────────────────

func (m Model) renderStatsSection(inst *AgentInstance, className string, level, xp, nextXP int) string {
	headerStyle := lipgloss.NewStyle().Foreground(colorYellow).Bold(true)

	header := headerStyle.Render("┌─ STATS ─────────────────┐")

	statLine := func(label, value string) string {
		return fmt.Sprintf("  %-9s %s",
			lipgloss.NewStyle().Foreground(colorTextDim).Render(label+":"),
			lipgloss.NewStyle().Foreground(colorText).Render(value))
	}

	var lines []string
	lines = append(lines, statLine("Class", className))
	lines = append(lines, statLine("Status", strings.ToUpper(inst.Status)))
	lines = append(lines, statLine("Level", fmt.Sprintf("%d", level)))
	lines = append(lines, statLine("XP", fmt.Sprintf("%d / %d", xp, nextXP)))

	// Class description
	classCfg := m.config.Classes[inst.ClassName]
	if classCfg != nil {
		lines = append(lines, "")
		lines = append(lines, lipgloss.NewStyle().Foreground(colorTextDim).Render("  "+classCfg.Description))
	}

	// Tool profile
	if classCfg != nil && classCfg.ToolProfile != "" {
		lines = append(lines, "")
		lines = append(lines, lipgloss.NewStyle().Foreground(colorTextDim).Render("  TOOLS"))
		tools := m.config.ToolProfiles[classCfg.ToolProfile]
		if len(tools) > 0 {
			lines = append(lines, lipgloss.NewStyle().Foreground(colorText).
				Render("  "+strings.Join(tools, ", ")))
		}
	}

	lines = append(lines, headerStyle.Render("└─────────────────────────┘"))

	return lipgloss.JoinVertical(lipgloss.Left,
		append([]string{header}, lines...)...,
	)
}

// ── Profile Section ────────────────────────────────────────────────

func (m Model) renderBioSection(inst *AgentInstance, maxWidth int) string {
	headerStyle := lipgloss.NewStyle().Foreground(colorYellow).Bold(true)

	header := headerStyle.Render("┌─ PROFILE ───────────────┐")

	// Style each line based on markdown content
	bioLines := strings.Split(inst.Bio, "\n")
	var styled []string
	for _, line := range bioLines {
		if len(line) > maxWidth-4 {
			line = line[:maxWidth-4]
		}
		trimmed := strings.TrimSpace(line)
		var rendered string
		switch {
		case strings.HasPrefix(trimmed, "## "):
			rendered = lipgloss.NewStyle().Foreground(colorYellow).Bold(true).Render("  " + line)
		case strings.HasPrefix(trimmed, "> "):
			rendered = lipgloss.NewStyle().Foreground(colorTextDim).Italic(true).Render("  " + line)
		case strings.HasPrefix(trimmed, "- NEVER") || strings.HasPrefix(trimmed, "- REFUSE"):
			rendered = lipgloss.NewStyle().Foreground(colorRed).Render("  " + line)
		case strings.HasPrefix(trimmed, "- ALWAYS"):
			rendered = lipgloss.NewStyle().Foreground(colorGreen).Render("  " + line)
		default:
			rendered = lipgloss.NewStyle().Foreground(colorText).Render("  " + line)
		}
		styled = append(styled, rendered)
	}

	// Calculate available display height (use terminal height minus overhead)
	availHeight := m.termHeight() - 16
	if availHeight < 5 {
		availHeight = 5
	}

	// Apply scroll offset
	offset := m.bioScroll
	if offset > len(styled)-availHeight {
		offset = len(styled) - availHeight
	}
	if offset < 0 {
		offset = 0
	}

	// Scroll indicators
	var topIndicator, bottomIndicator string
	if offset > 0 {
		topIndicator = lipgloss.NewStyle().Foreground(colorTextDim).Render("  ^ more ^")
	}
	if offset+availHeight < len(styled) {
		bottomIndicator = lipgloss.NewStyle().Foreground(colorTextDim).Render("  v more v")
	}

	// Slice visible lines
	end := offset + availHeight
	if end > len(styled) {
		end = len(styled)
	}
	visible := styled[offset:end]

	var lines []string
	if topIndicator != "" {
		lines = append(lines, topIndicator)
	}
	lines = append(lines, visible...)
	if bottomIndicator != "" {
		lines = append(lines, bottomIndicator)
	}

	lines = append(lines, headerStyle.Render("└─────────────────────────┘"))

	return lipgloss.JoinVertical(lipgloss.Left,
		append([]string{header}, lines...)...,
	)
}
