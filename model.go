package main

import (
	"fmt"
	"image/color"
	"os"
	"os/exec"
	"syscall"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/vt"
	"github.com/creack/pty"
)

// ── Focus & Mode ───────────────────────────────────────────────────

type FocusZone int

const (
	FocusLeftPanel FocusZone = iota
	FocusMainPane
	FocusPartyBar
)

type InputMode int

const (
	ModeNormal InputMode = iota
	ModeInsert
	ModeSwap
	ModeCharSheet
	ModeCheckout
)

const MaxPartySlots = 8

// ── Runtime Types ──────────────────────────────────────────────────

// AgentInstance is a live agent in a party slot or bench.
type AgentInstance struct {
	ID        string
	AgentName string
	ClassName string
	Tint      color.RGBA
	kittyB64   string
	Bio        string
	Directives string // operational profile for system prompt

	// Skill loadout
	Equipped []string
	Passives []string
	Model    string // model override

	// PTY state
	Status       string // "idle", "running", "exited"
	Task         string
	cmd          *exec.Cmd
	ptyFile      *os.File
	emulator     *vt.SafeEmulator
	ContextBytes int64 // total PTY bytes for HP bar

	// Pending changes (skills changed while running)
	PendingEquipped []string
	PendingPassives []string
	HasPending      bool
}

// Party is a workspace with agent slots and a bench.
type Party struct {
	Name    string
	Project string
	Slots   [MaxPartySlots]*AgentInstance
	Bench   []*AgentInstance
}

// ── Model ──────────────────────────────────────────────────────────

type Model struct {
	config *ForgeConfig
	roster *RosterFile

	parties     []*Party
	activeParty int

	focus         FocusZone
	mode          InputMode
	selectedAgent int
	swapIndex     int

	// Character sheet state
	csSection int // 0=equipped, 1=available
	csCursor  int // cursor within current section
	bioScroll int // scroll offset for profile/bio section

	// Checkout modal
	checkoutAgent *AgentInstance

	// Wizard (nil when not active)
	wizard *WizardState

	// Delete confirmation
	deleteConfirm bool

	width  int
	height int
	ready  bool
}

func (m Model) Init() tea.Cmd {
	return nil
}

// ── Accessors ──────────────────────────────────────────────────────

func (m Model) party() *Party {
	if m.activeParty < len(m.parties) {
		return m.parties[m.activeParty]
	}
	return nil
}

func (m Model) agent() *AgentInstance {
	p := m.party()
	if p == nil {
		return nil
	}
	if m.selectedAgent >= 0 && m.selectedAgent < MaxPartySlots {
		return p.Slots[m.selectedAgent]
	}
	return nil
}

func (m Model) lastSlotIndex() int {
	p := m.party()
	if p == nil {
		return 0
	}
	last := 0
	for i, s := range p.Slots {
		if s != nil {
			last = i
		}
	}
	return last
}

func (m Model) agentByID(id string) *AgentInstance {
	for _, p := range m.parties {
		for _, a := range p.Slots {
			if a != nil && a.ID == id {
				return a
			}
		}
		for _, a := range p.Bench {
			if a != nil && a.ID == id {
				return a
			}
		}
	}
	return nil
}

// ── Layout ─────────────────────────────────────────────────────────

const leftPanelWidth = 20

func (m Model) mainPaneWidth() int {
	w := m.width - leftPanelWidth - 1 // panel content + border right
	if w < 20 {
		w = 20
	}
	return w
}

func (m Model) termWidth() int {
	return m.mainPaneWidth() - 2 // border
}

func (m Model) termHeight() int {
	_, _, _, _, ph, _ := m.cardLayout()
	h := m.height - ph - 4 // header(1) + border(2) + status(1)
	if h < 3 {
		h = 3
	}
	return h
}

func (m Model) cardLayout() (cardWidth, avatarCols, avatarRows, cardHeight, partyHeight, cardsPerRow int) {
	// Count active slots
	activeCount := 0
	p := m.party()
	if p != nil {
		for _, s := range p.Slots {
			if s != nil {
				activeCount++
			}
		}
	}
	if activeCount < 1 {
		activeCount = 1
	}

	availableWidth := m.mainPaneWidth() - 10 // party label + padding

	// Try fitting all cards in one row
	cardsPerRow = activeCount
	cardWidth = availableWidth/cardsPerRow - 2 // subtract borders

	// Enforce min/max card width
	if cardWidth > 28 {
		cardWidth = 28
	}
	if cardWidth < 18 {
		// Too narrow — reduce cards per row
		cardWidth = 18
		cardsPerRow = availableWidth / (cardWidth + 2)
		if cardsPerRow < 1 {
			cardsPerRow = 1
		}
	}

	avatarCols = cardWidth - 2
	if avatarCols < 8 {
		avatarCols = 8
	}

	// Square aspect ratio (half-block doubles vertical resolution)
	avatarRows = avatarCols / 2

	rows := (activeCount + cardsPerRow - 1) / cardsPerRow
	if rows < 1 {
		rows = 1
	}

	// Scale down avatars if party bar would exceed ~40% of terminal height
	if m.height > 0 {
		maxPartyH := m.height * 40 / 100
		if maxPartyH < 10 {
			maxPartyH = 10
		}
		maxAvatarRows := maxPartyH/rows - 6
		if maxAvatarRows < 3 {
			maxAvatarRows = 3
		}
		if avatarRows > maxAvatarRows {
			avatarRows = maxAvatarRows
		}
	}

	cardHeight = avatarRows + 4 // name + class + status + hp bar
	partyHeight = (cardHeight + 2) * rows
	return
}

// ── Update ─────────────────────────────────────────────────────────

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		return m.handleResize(msg)
	case AgentStartedMsg:
		return m.handleAgentStarted(msg)
	case AgentOutputMsg:
		return m.handleAgentOutput(msg)
	case AgentExitedMsg:
		return m.handleAgentExited(msg)
	case forceResizeMsg:
		return m, nil
	case tea.MouseMsg:
		if m.wizard != nil {
			return m, nil // no mouse in wizard
		}
		return m.handleMouse(msg)
	case tea.KeyMsg:
		if m.deleteConfirm {
			return m.handleDeleteConfirm(msg)
		}
		if m.wizard != nil {
			return m.handleWizardKeys(msg)
		}
		switch m.mode {
		case ModeInsert:
			return m.handleInsertMode(msg)
		case ModeSwap:
			return m.handleSwapMode(msg)
		case ModeCharSheet:
			return m.handleCharSheetMode(msg)
		case ModeCheckout:
			return m.handleCheckoutMode(msg)
		default:
			return m.handleNormalMode(msg)
		}
	}
	return m, nil
}

// ── Resize ─────────────────────────────────────────────────────────

func (m Model) handleResize(msg tea.WindowSizeMsg) (tea.Model, tea.Cmd) {
	m.width = msg.Width
	m.height = msg.Height
	m.ready = true

	tw := m.termWidth()
	th := m.termHeight()

	for _, p := range m.parties {
		for _, inst := range p.Slots {
			if inst != nil && inst.Status == "running" && inst.emulator != nil && inst.ptyFile != nil {
				pty.Setsize(inst.ptyFile, &pty.Winsize{Rows: uint16(th), Cols: uint16(tw)})
				inst.emulator.Resize(tw, th)
			}
		}
	}
	return m, nil
}

// ── Agent Lifecycle Messages ───────────────────────────────────────

func (m Model) handleAgentStarted(msg AgentStartedMsg) (tea.Model, tea.Cmd) {
	inst := m.agentByID(msg.ID)
	if inst == nil {
		return m, nil
	}
	inst.cmd = msg.Cmd
	inst.ptyFile = msg.PtyFile
	inst.emulator = msg.Emulator
	inst.Status = "running"
	inst.Task = "Running claude..."
	inst.ContextBytes = 0
	go forwardResponses(inst)
	return m, tea.Batch(
		readAgentPTY(inst),
		delayedResize(inst, m.termWidth(), m.termHeight()),
	)
}

func (m Model) handleAgentOutput(msg AgentOutputMsg) (tea.Model, tea.Cmd) {
	inst := m.agentByID(msg.ID)
	if inst != nil && inst.Status == "running" {
		inst.ContextBytes += int64(msg.BytesRead)
		return m, readAgentPTY(inst)
	}
	return m, nil
}

func (m Model) handleAgentExited(msg AgentExitedMsg) (tea.Model, tea.Cmd) {
	inst := m.agentByID(msg.ID)
	if inst == nil {
		return m, nil
	}
	inst.Status = "exited"
	inst.Task = "Process exited"

	// If we're in insert mode viewing this agent, switch back
	if m.mode == ModeInsert {
		a := m.agent()
		if a != nil && a.ID == inst.ID {
			m.mode = ModeNormal
		}
	}

	// Apply pending skill changes
	if inst.HasPending {
		inst.Equipped = inst.PendingEquipped
		inst.Passives = inst.PendingPassives
		inst.HasPending = false
		inst.PendingEquipped = nil
		inst.PendingPassives = nil
	}

	// Show checkout modal
	m.checkoutAgent = inst
	m.mode = ModeCheckout

	// Cleanup PTY resources
	ptf := inst.ptyFile
	cmd := inst.cmd
	em := inst.emulator
	inst.ptyFile = nil
	inst.cmd = nil
	inst.emulator = nil
	go func() {
		if em != nil {
			em.Close()
		}
		if ptf != nil {
			ptf.Close()
		}
		if cmd != nil {
			cmd.Wait()
		}
	}()

	return m, nil
}

// ── Mouse ──────────────────────────────────────────────────────────

func (m Model) handleMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	if msg.Action != tea.MouseActionPress || msg.Button != tea.MouseButtonLeft {
		return m, nil
	}

	panelRight := leftPanelWidth + 1 // panel content + border
	th := m.termHeight()

	// Row regions (0-indexed from bubbletea)
	headerBottom := 0                   // header is row 0
	termTop := 1                        // terminal starts at row 1
	termBottom := termTop + th + 1      // terminal border bottom
	// Click on left panel (party select)
	if msg.X < panelRight && msg.Y > headerBottom {
		m.focus = FocusLeftPanel
		// Each party entry takes 3 lines, starting at line 2 within panel (after title + blank)
		entryY := msg.Y - termTop - 2
		if entryY >= 0 {
			partyIdx := entryY / 3
			if partyIdx >= 0 && partyIdx < len(m.parties) {
				m.activeParty = partyIdx
				m.selectedAgent = 0
			} else if partyIdx == len(m.parties) {
				// Clicked "+ New Party"
				return m.createNewParty()
			}
		}
		return m, nil
	}

	// Click on terminal area (main pane)
	if msg.Y >= termTop && msg.Y <= termBottom && msg.X >= panelRight {
		m.focus = FocusMainPane
		inst := m.agent()
		if inst != nil && inst.Status == "running" && m.mode == ModeNormal {
			m.mode = ModeInsert
		}
		return m, nil
	}

	// Click on party bar
	if msg.Y > termBottom && msg.X >= panelRight {
		m.focus = FocusPartyBar
		cw, _, _, ch, _, cpr := m.cardLayout()
		cardStart := panelRight + 5
		cardTotalWidth := cw + 2
		cardTotalHeight := ch + 2
		if msg.X >= cardStart {
			colIdx := (msg.X - cardStart) / cardTotalWidth
			if colIdx >= 0 && colIdx < cpr {
				rowStart := termBottom + 1
				rowIdx := (msg.Y - rowStart) / cardTotalHeight
				if rowIdx < 0 {
					rowIdx = 0
				}
				idx := rowIdx*cpr + colIdx
				if idx >= 0 && idx <= m.lastSlotIndex() {
					m.selectedAgent = idx
				}
			}
		}
		return m, nil
	}

	return m, nil
}

// ── Normal Mode ────────────────────────────────────────────────────

func (m Model) handleNormalMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "ctrl+c":
		m.stopAllAgents()
		return m, tea.Quit

	case "tab":
		// Cycle focus zones
		switch m.focus {
		case FocusLeftPanel:
			m.focus = FocusMainPane
		case FocusMainPane:
			m.focus = FocusPartyBar
		case FocusPartyBar:
			m.focus = FocusLeftPanel
		}
		return m, nil

	case "shift+tab":
		switch m.focus {
		case FocusLeftPanel:
			m.focus = FocusPartyBar
		case FocusMainPane:
			m.focus = FocusLeftPanel
		case FocusPartyBar:
			m.focus = FocusMainPane
		}
		return m, nil
	}

	// Dispatch to focused zone
	switch m.focus {
	case FocusLeftPanel:
		return m.handleLeftPanelKeys(msg)
	case FocusMainPane:
		return m.handleMainPaneKeys(msg)
	case FocusPartyBar:
		return m.handlePartyBarKeys(msg)
	}

	return m, nil
}

func (m Model) handleLeftPanelKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "up", "k":
		if m.activeParty > 0 {
			m.activeParty--
			m.selectedAgent = 0
		}
	case "down", "j":
		if m.activeParty < len(m.parties)-1 {
			m.activeParty++
			m.selectedAgent = 0
		}
	case "enter":
		// Already showing active party, no-op for now
	case "n":
		return m.createNewParty()
	case "d":
		return m.deleteParty()
	case "1", "2", "3", "4", "5", "6", "7", "8", "9":
		idx := int(msg.String()[0] - '1')
		if idx < len(m.parties) {
			m.activeParty = idx
			m.selectedAgent = 0
		}
	}
	return m, nil
}

func (m Model) handleMainPaneKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "left", "h":
		if m.selectedAgent > 0 {
			m.selectedAgent--
		}
	case "right", "l":
		if m.selectedAgent < m.lastSlotIndex() {
			m.selectedAgent++
		}
	case "1", "2", "3", "4", "5", "6", "7", "8":
		idx := int(msg.String()[0] - '1')
		if idx <= m.lastSlotIndex() {
			m.selectedAgent = idx
		}
	case "enter":
		// Open character sheet
		inst := m.agent()
		if inst != nil {
			m.mode = ModeCharSheet
			m.csSection = 0
			m.csCursor = 0
			m.bioScroll = 0
		}
	case "i":
		inst := m.agent()
		if inst != nil && inst.Status == "running" {
			m.mode = ModeInsert
		}
	case "s":
		inst := m.agent()
		if inst != nil && (inst.Status == "idle" || inst.Status == "exited") {
			tw := m.termWidth()
			th := m.termHeight()
			if tw > 0 && th > 0 {
				inst.Status = "idle"
				inst.Task = "Starting..."
				p := m.party()
				projectDir := "."
				if p != nil && p.Project != "" {
					projectDir = p.Project
				}
				return m, startAgent(inst, tw, th, m.config, projectDir)
			}
		}
	case "x":
		inst := m.agent()
		if inst != nil && inst.Status == "running" {
			inst.Status = "exited"
			inst.Task = "Stopping..."
			if inst.cmd != nil && inst.cmd.Process != nil {
				inst.cmd.Process.Signal(syscall.SIGTERM)
			}
		}
	case " ":
		p := m.party()
		if p != nil && len(p.Bench) > 0 {
			m.mode = ModeSwap
			m.swapIndex = 0
		}
	}
	return m, nil
}

func (m Model) handlePartyBarKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "left", "h":
		if m.selectedAgent > 0 {
			m.selectedAgent--
		}
	case "right", "l":
		if m.selectedAgent < m.lastSlotIndex() {
			m.selectedAgent++
		}
	case "enter":
		inst := m.agent()
		if inst != nil {
			m.mode = ModeCharSheet
			m.csSection = 0
			m.csCursor = 0
			m.bioScroll = 0
		}
	case "s":
		inst := m.agent()
		if inst != nil && (inst.Status == "idle" || inst.Status == "exited") {
			tw := m.termWidth()
			th := m.termHeight()
			if tw > 0 && th > 0 {
				inst.Status = "idle"
				inst.Task = "Starting..."
				p := m.party()
				projectDir := "."
				if p != nil && p.Project != "" {
					projectDir = p.Project
				}
				return m, startAgent(inst, tw, th, m.config, projectDir)
			}
		}
	}
	return m, nil
}

// ── Insert Mode ────────────────────────────────────────────────────

func (m Model) handleInsertMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.String() == "esc" {
		m.mode = ModeNormal
		return m, nil
	}
	inst := m.agent()
	if inst == nil || inst.ptyFile == nil {
		m.mode = ModeNormal
		return m, nil
	}
	b := keyToBytes(msg)
	if b != nil {
		inst.ptyFile.Write(b)
		inst.ContextBytes += int64(len(b))
	}
	return m, nil
}

// ── Swap Mode ──────────────────────────────────────────────────────

func (m Model) handleSwapMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	p := m.party()
	if p == nil {
		m.mode = ModeNormal
		return m, nil
	}
	switch msg.String() {
	case "esc":
		m.mode = ModeNormal
	case "left", "h":
		if m.swapIndex > 0 {
			m.swapIndex--
		} else {
			m.swapIndex = len(p.Bench) - 1
		}
	case "right", "l":
		if m.swapIndex < len(p.Bench)-1 {
			m.swapIndex++
		} else {
			m.swapIndex = 0
		}
	case " ", "enter":
		if len(p.Bench) > 0 {
			old := p.Slots[m.selectedAgent]
			swapped := p.Bench[m.swapIndex]
			p.Slots[m.selectedAgent] = swapped
			p.Bench[m.swapIndex] = old
			if swapped.Status == "running" && swapped.emulator != nil && swapped.ptyFile != nil {
				tw := m.termWidth()
				th := m.termHeight()
				pty.Setsize(swapped.ptyFile, &pty.Winsize{Rows: uint16(th), Cols: uint16(tw)})
				swapped.emulator.Resize(tw, th)
			}
		}
		m.mode = ModeNormal
	}
	return m, nil
}

// ── Character Sheet Mode ───────────────────────────────────────────

func (m Model) handleCharSheetMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	inst := m.agent()
	if inst == nil {
		m.mode = ModeNormal
		return m, nil
	}

	switch msg.String() {
	case "esc":
		m.mode = ModeNormal
		// Save skill changes to party file
		m.saveCurrentParty()
	case "tab":
		m.csSection = (m.csSection + 1) % 2
		m.csCursor = 0
	case "shift+tab":
		m.csSection = (m.csSection + 1) % 2
		m.csCursor = 0
	case "up", "k":
		if m.csCursor > 0 {
			m.csCursor--
		}
	case "down", "j":
		m.csCursor++
		maxCursor := m.charSheetSectionLen(inst)
		if m.csCursor >= maxCursor {
			m.csCursor = maxCursor - 1
		}
		if m.csCursor < 0 {
			m.csCursor = 0
		}
	case " ":
		// Equip/unequip (only when idle)
		if inst.Status == "running" {
			return m, nil
		}
		m.charSheetToggle(inst)
	case "i":
		// Enter insert mode from char sheet (if running)
		if inst.Status == "running" {
			m.mode = ModeInsert
		}
	case "s":
		// Start agent from char sheet
		if inst.Status == "idle" || inst.Status == "exited" {
			tw := m.termWidth()
			th := m.termHeight()
			if tw > 0 && th > 0 {
				inst.Status = "idle"
				inst.Task = "Starting..."
				m.mode = ModeNormal
				p := m.party()
				projectDir := "."
				if p != nil && p.Project != "" {
					projectDir = p.Project
				}
				return m, startAgent(inst, tw, th, m.config, projectDir)
			}
		}
	case "x":
		if inst.Status == "running" && inst.cmd != nil && inst.cmd.Process != nil {
			inst.Status = "exited"
			inst.Task = "Stopping..."
			inst.cmd.Process.Signal(syscall.SIGTERM)
		}
	case "[":
		if m.bioScroll > 0 {
			m.bioScroll--
		}
	case "]":
		m.bioScroll++
	}
	return m, nil
}

func (m Model) charSheetSectionLen(inst *AgentInstance) int {
	switch m.csSection {
	case 0: // equipped: innate + equipped + empty slots
		classCfg := m.config.Classes[inst.ClassName]
		innateCount := 0
		if classCfg != nil {
			innateCount = len(classCfg.InnateSkills)
		}
		return innateCount + MaxEquipSlots
	case 1: // available
		return len(m.availableSkills(inst))
	}
	return 0
}

func (m Model) charSheetToggle(inst *AgentInstance) {
	switch m.csSection {
	case 0: // equipped section — unequip at cursor
		classCfg := m.config.Classes[inst.ClassName]
		innateCount := 0
		if classCfg != nil {
			innateCount = len(classCfg.InnateSkills)
		}
		equipIdx := m.csCursor - innateCount
		if equipIdx >= 0 && equipIdx < len(inst.Equipped) {
			inst.Equipped = append(inst.Equipped[:equipIdx], inst.Equipped[equipIdx+1:]...)
		}
	case 1: // available section — equip at cursor
		avail := m.availableSkills(inst)
		if m.csCursor < len(avail) {
			inst.Equipped = ToggleEquip(m.config, inst.ClassName, inst.Equipped, avail[m.csCursor])
		}
	}
}

// availableSkills returns skills NOT innate and NOT equipped.
func (m Model) availableSkills(inst *AgentInstance) []string {
	classCfg := m.config.Classes[inst.ClassName]
	all := AllSkillIDs(m.config)
	var avail []string
	for _, sid := range all {
		if classCfg != nil && isInnate(classCfg, sid) {
			continue
		}
		equipped := false
		for _, e := range inst.Equipped {
			if e == sid {
				equipped = true
				break
			}
		}
		if !equipped {
			avail = append(avail, sid)
		}
	}
	return avail
}

// ── Checkout Mode ──────────────────────────────────────────────────

func (m Model) handleCheckoutMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.checkoutAgent == nil {
		m.mode = ModeNormal
		return m, nil
	}

	var xpGain int
	switch msg.String() {
	case "1":
		xpGain = 50
	case "2":
		xpGain = 20
	case "3":
		xpGain = 5
	case "esc":
		xpGain = 0
	default:
		return m, nil
	}

	if xpGain > 0 {
		name := m.checkoutAgent.AgentName
		entry := m.roster.Agents[name]
		if entry == nil {
			entry = &AgentRoster{XP: 0, Level: 1}
			m.roster.Agents[name] = entry
		}
		entry.XP += xpGain
		entry.Level = LevelForXP(entry.XP)
		SaveRoster(m.roster)
	}

	m.checkoutAgent = nil
	m.mode = ModeNormal
	return m, nil
}

// ── Party Management ───────────────────────────────────────────────

func (m Model) createNewParty() (Model, tea.Cmd) {
	cwd, _ := os.Getwd()
	m.wizard = &WizardState{
		Step:               WizardNameParty,
		Project:            cwd,
		HasExistingParties: len(m.parties) > 0,
		CancelToNormal:     true,
	}
	return m, nil
}

func (m Model) deleteParty() (Model, tea.Cmd) {
	if len(m.parties) <= 1 {
		return m, nil // keep at least one
	}
	p := m.party()
	if p == nil {
		return m, nil
	}
	// Check for running agents — prompt confirmation
	for _, inst := range p.Slots {
		if inst != nil && inst.Status == "running" {
			m.deleteConfirm = true
			return m, nil
		}
	}
	return m.doDeleteParty()
}

func (m Model) doDeleteParty() (Model, tea.Cmd) {
	p := m.party()
	if p == nil {
		return m, nil
	}
	for _, inst := range p.Slots {
		if inst != nil && inst.Status == "running" && inst.cmd != nil && inst.cmd.Process != nil {
			inst.cmd.Process.Signal(syscall.SIGTERM)
		}
	}
	os.Remove(partyPath(p.Name))
	m.parties = append(m.parties[:m.activeParty], m.parties[m.activeParty+1:]...)
	if m.activeParty >= len(m.parties) {
		m.activeParty = len(m.parties) - 1
	}
	m.selectedAgent = 0
	m.deleteConfirm = false
	return m, nil
}

func (m Model) handleDeleteConfirm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "y", "Y", "enter":
		return m.doDeleteParty()
	case "n", "N", "esc":
		m.deleteConfirm = false
	}
	return m, nil
}

func (m Model) buildParty(pf *PartyFile) *Party {
	party := &Party{
		Name:    pf.Name,
		Project: pf.Project,
	}

	agentMap := make(map[string]*AgentConfig)
	for i := range m.config.Agents {
		agentMap[m.config.Agents[i].Name] = &m.config.Agents[i]
	}

	// Build slots
	for i := 0; i < MaxPartySlots && i < len(pf.Slots); i++ {
		slot := pf.Slots[i]
		party.Slots[i] = m.buildInstance(agentMap, slot, pf.Name, i)
	}

	// Build bench
	for i, slot := range pf.Bench {
		inst := m.buildInstance(agentMap, slot, pf.Name, 4+i)
		party.Bench = append(party.Bench, inst)
	}

	return party
}

func (m Model) buildInstance(agentMap map[string]*AgentConfig, slot PartySlotConfig, partyName string, idx int) *AgentInstance {
	def := agentMap[slot.Agent]
	if def == nil {
		return &AgentInstance{
			ID:        fmt.Sprintf("%s-%d", partyName, idx),
			AgentName: slot.Agent,
			ClassName: "coder",
			Status:    "idle",
			Task:      "Awaiting orders...",
			Tint:      color.RGBA{128, 128, 128, 255},
		}
	}

	tint := color.RGBA{def.Tint[0], def.Tint[1], def.Tint[2], 255}
	return &AgentInstance{
		ID:         fmt.Sprintf("%s-%d-%s", partyName, idx, def.Name),
		AgentName:  def.Name,
		ClassName:  def.Class,
		Tint:       tint,
		kittyB64:   encodeKittyAvatar(avatarImage, tint),
		Bio:        def.Bio,
		Directives: def.Directives,
		Equipped:   slot.Equipped,
		Passives:   slot.Passives,
		Status:     "idle",
		Task:       "Awaiting orders...",
	}
}

func (m Model) saveCurrentParty() {
	p := m.party()
	if p == nil {
		return
	}
	pf := &PartyFile{
		Name:    p.Name,
		Project: p.Project,
	}
	for _, inst := range p.Slots {
		if inst != nil {
			pf.Slots = append(pf.Slots, PartySlotConfig{
				Agent:    inst.AgentName,
				Equipped: inst.Equipped,
				Passives: inst.Passives,
			})
		}
	}
	for _, inst := range p.Bench {
		if inst != nil {
			pf.Bench = append(pf.Bench, PartySlotConfig{
				Agent:    inst.AgentName,
				Equipped: inst.Equipped,
				Passives: inst.Passives,
			})
		}
	}
	SaveParty(pf)
}

// ── Cleanup ────────────────────────────────────────────────────────

func (m Model) stopAllAgents() {
	for _, p := range m.parties {
		for _, inst := range p.Slots {
			if inst != nil && inst.Status == "running" && inst.cmd != nil && inst.cmd.Process != nil {
				inst.cmd.Process.Signal(syscall.SIGTERM)
				inst.Status = "exited"
			}
		}
		for _, inst := range p.Bench {
			if inst != nil && inst.Status == "running" && inst.cmd != nil && inst.cmd.Process != nil {
				inst.cmd.Process.Signal(syscall.SIGTERM)
				inst.Status = "exited"
			}
		}
	}
}
