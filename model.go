package main

import (
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/vt"
	"github.com/creack/pty"
)

// ── Pull Request Types ────────────────────────────────────────────

type PullRequest struct {
	Number    int             `json:"number"`
	Title     string          `json:"title"`
	State     string          `json:"state"`
	Branch    string          `json:"headRefName"`
	Author    string          `json:"author"`
	IsDraft   bool            `json:"isDraft"`
	Checks    PRChecksStatus  `json:"statusCheckRollup"`
	ReviewDec string          `json:"reviewDecision"`
	UpdatedAt string          `json:"updatedAt"`
}

type PRChecksStatus struct {
	State string `json:"state"`
}

type prAuthor struct {
	Login string `json:"login"`
}

func (pr *PullRequest) UnmarshalJSON(data []byte) error {
	type Alias PullRequest
	aux := &struct {
		Author json.RawMessage `json:"author"`
		*Alias
	}{Alias: (*Alias)(pr)}

	if err := json.Unmarshal(data, aux); err != nil {
		return err
	}
	if len(aux.Author) > 0 {
		var a prAuthor
		if json.Unmarshal(aux.Author, &a) == nil {
			pr.Author = a.Login
		}
	}
	return nil
}

type PRListMsg struct {
	PRs []PullRequest
	Err error
}

func (pr PullRequest) StatusIcon() string {
	if pr.IsDraft {
		return "◌"
	}
	switch pr.Checks.State {
	case "SUCCESS":
		if pr.ReviewDec == "APPROVED" {
			return "✓"
		}
		return "●"
	case "FAILURE", "ERROR":
		return "✗"
	case "PENDING":
		return "○"
	}
	return "·"
}

func loadPRList(projectDir string) tea.Cmd {
	return func() tea.Msg {
		if projectDir == "" || projectDir == "." {
			cwd, _ := os.Getwd()
			projectDir = cwd
		}
		cmd := exec.Command("gh", "pr", "list", "--json",
			"number,title,state,headRefName,author,isDraft,statusCheckRollup,reviewDecision,updatedAt",
			"--limit", "25")
		cmd.Dir = projectDir
		out, err := cmd.Output()
		if err != nil {
			return PRListMsg{Err: err}
		}
		var prs []PullRequest
		if err := json.Unmarshal(out, &prs); err != nil {
			return PRListMsg{Err: err}
		}
		return PRListMsg{PRs: prs}
	}
}

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
	ModeCommandPalette
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
	avatarImg  image.Image // per-agent avatar for half-block rendering
	Bio        string
	Directives string // operational profile for system prompt

	// Skill loadout
	Equipped []string
	Passives []string
	Model    string // model override

	// Git worktree isolation
	Worktree string // path to git worktree (empty if not isolated)
	Branch   string // git branch for this worktree

	// Handoff
	LastOutput     string // final terminal output snapshot for handoff
	HandoffContext string // injected context from another agent's handoff

	// PTY state
	Status       string // "idle", "running", "exited"
	Task         string
	cmd          *exec.Cmd
	ptyFile      *os.File
	emulator     *vt.SafeEmulator
	ContextBytes  int64     // total PTY bytes for HP bar
	ContextTokens int       // parsed real token count (0 = use byte estimate)
	ContextMax    int       // parsed max context tokens (0 = use default)
	outputReads   int       // counter for periodic context scanning
	lastOutputAt  time.Time // last PTY output for activity detection

	// Pending changes (skills changed while running)
	PendingEquipped []string
	PendingPassives []string
	HasPending      bool

	// Cached half-block avatar render
	cachedHalfBlock     string
	cachedHalfBlockCols int
	cachedHalfBlockRows int
}

// Party is a workspace with agent slots and a bench.
type Party struct {
	Name    string
	Project string
	Slots   [MaxPartySlots]*AgentInstance
	Bench   []*AgentInstance
}

// ── Layout Cache ──────────────────────────────────────────────────

type LayoutCache struct {
	CardWidth     int
	AvatarCols    int
	AvatarRows    int
	CardHeight    int
	PartyHeight   int
	CardsPerRow   int
	TermWidth     int
	TermHeight    int
	MainPaneWidth int
	valid         bool
}

// ── Model ──────────────────────────────────────────────────────────

type Model struct {
	config *ForgeConfig
	roster *RosterFile

	parties     []*Party
	activeParty int

	focus         FocusZone
	mode          InputMode
	modeStack     []InputMode // mode history for push/pop navigation
	selectedAgent int
	swapIndex     int

	// Character sheet state
	csSection int // 0=equipped, 1=available
	csCursor  int // cursor within current section
	bioScroll int // scroll offset for profile/bio section

	// Checkout modal
	checkoutAgent  *AgentInstance
	checkoutStep   int // 0=XP, 1=scroll naming, 2=handoff, 3=worktree
	handoffTarget  int // index into party slots for handoff target
	scrollNameBuf  string // text input for scroll name

	// Wizard (nil when not active)
	wizard *WizardState

	// Delete confirmation
	deleteConfirm bool

	// Git panel (files or PRs)
	showGitPanel   bool
	gitPanelMode   int // 0=files, 1=PRs
	gitTreeLines   []string
	gitPanelScroll int
	prList         []PullRequest
	prLoading      bool

	// Command palette
	cmdPaletteInput  string
	cmdPaletteCursor int

	// Layout cache (recomputed on resize/party change)
	layout LayoutCache

	// Auto-start flag for single-party skip-wizard flow
	autoStartPending bool

	// Agent index for O(1) lookup by ID
	agentIndex map[string]*AgentInstance

	width  int
	height int
	ready  bool
}

func (m Model) Init() tea.Cmd {
	return loadAvatarsAsync(m.config.Agents)
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
	if m.agentIndex != nil {
		return m.agentIndex[id]
	}
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

func (m *Model) rebuildAgentIndex() {
	idx := make(map[string]*AgentInstance)
	for _, p := range m.parties {
		for _, a := range p.Slots {
			if a != nil {
				idx[a.ID] = a
			}
		}
		for _, a := range p.Bench {
			if a != nil {
				idx[a.ID] = a
			}
		}
	}
	m.agentIndex = idx
}

func (m *Model) pushMode(mode InputMode) {
	m.modeStack = append(m.modeStack, m.mode)
	m.mode = mode
}

func (m *Model) popMode() {
	if len(m.modeStack) > 0 {
		m.mode = m.modeStack[len(m.modeStack)-1]
		m.modeStack = m.modeStack[:len(m.modeStack)-1]
	} else {
		m.mode = ModeNormal
	}
}

func (m Model) partyForAgent(inst *AgentInstance) *Party {
	for _, p := range m.parties {
		for _, a := range p.Slots {
			if a == inst {
				return p
			}
		}
		for _, a := range p.Bench {
			if a == inst {
				return p
			}
		}
	}
	return nil
}

// ── Layout ─────────────────────────────────────────────────────────

const leftPanelWidth = 20
const gitPanelWidth = 30

func (m Model) mainPaneWidth() int {
	w := m.width - leftPanelWidth - 1 // panel content + border right
	if m.showGitPanel {
		w -= gitPanelWidth + 1
	}
	if w < 20 {
		w = 20
	}
	return w
}

func (m Model) termWidth() int {
	if m.layout.valid {
		return m.layout.TermWidth
	}
	return m.mainPaneWidth() - 2 // border
}

func (m Model) termHeight() int {
	if m.layout.valid {
		return m.layout.TermHeight
	}
	_, _, _, _, ph, _ := m.cardLayout()
	h := m.height - ph - 3 // border(2) + status(1)
	if h < 3 {
		h = 3
	}
	return h
}

func (m *Model) recomputeLayout() {
	mpw := m.mainPaneWidth()
	cw, ac, ar, ch, ph, cpr := m.cardLayout()
	tw := mpw - 2
	th := m.height - ph - 3
	if th < 3 {
		th = 3
	}
	m.layout = LayoutCache{
		CardWidth:     cw,
		AvatarCols:    ac,
		AvatarRows:    ar,
		CardHeight:    ch,
		PartyHeight:   ph,
		CardsPerRow:   cpr,
		TermWidth:     tw,
		TermHeight:    th,
		MainPaneWidth: mpw,
		valid:         true,
	}
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
	partyHeight = (cardHeight+2)*rows + 1 // +1 for project dir footer
	return
}

// ── Update ─────────────────────────────────────────────────────────

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		return m.handleResize(msg)
	case AvatarReadyMsg:
		return m.handleAvatarReady(msg)
	case AgentStartedMsg:
		return m.handleAgentStarted(msg)
	case AgentOutputMsg:
		return m.handleAgentOutput(msg)
	case AgentExitedMsg:
		return m.handleAgentExited(msg)
	case PRListMsg:
		m.prLoading = false
		if msg.Err == nil {
			m.prList = msg.PRs
		}
		return m, nil
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
		case ModeCommandPalette:
			return m.handleCommandPalette(msg)
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
	m.recomputeLayout()

	tw := m.layout.TermWidth
	th := m.layout.TermHeight

	// Only resize active party agents
	p := m.party()
	if p != nil {
		for _, inst := range p.Slots {
			if inst != nil && inst.Status == "running" && inst.emulator != nil && inst.ptyFile != nil {
				pty.Setsize(inst.ptyFile, &pty.Winsize{Rows: uint16(th), Cols: uint16(tw)})
				inst.emulator.Resize(tw, th)
			}
		}
	}

	if m.autoStartPending {
		m.autoStartPending = false
		return m.autoStartPartyAgents()
	}

	return m, nil
}

// ── Avatar Ready ──────────────────────────────────────────────────

func (m Model) handleAvatarReady(msg AvatarReadyMsg) (tea.Model, tea.Cmd) {
	if msg.Image == nil {
		return m, nil
	}
	// Update the AgentConfig template
	for i := range m.config.Agents {
		if m.config.Agents[i].Name == msg.AgentName {
			m.config.Agents[i].AvatarImage = msg.Image
			m.config.Agents[i].KittyB64 = msg.KittyB64
			break
		}
	}
	// Propagate to all AgentInstances
	for _, p := range m.parties {
		for _, inst := range p.Slots {
			if inst != nil && inst.AgentName == msg.AgentName {
				inst.avatarImg = msg.Image
				inst.kittyB64 = msg.KittyB64
				inst.cachedHalfBlock = "" // invalidate cache
			}
		}
		for _, inst := range p.Bench {
			if inst != nil && inst.AgentName == msg.AgentName {
				inst.avatarImg = msg.Image
				inst.kittyB64 = msg.KittyB64
				inst.cachedHalfBlock = ""
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
	inst.Worktree = msg.Worktree
	inst.Branch = msg.Branch
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
		inst.lastOutputAt = time.Now()
		inst.outputReads++

		// Periodically scan terminal for context window info
		if inst.outputReads%50 == 0 && inst.emulator != nil {
			parseContextFromTerminal(inst)
		}

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

	// Capture final output for handoff before closing emulator
	if inst.emulator != nil {
		inst.LastOutput = strings.ReplaceAll(inst.emulator.Render(), "\r\n", "\n")
		// Trim to last ~2000 chars to keep handoff context reasonable
		if len(inst.LastOutput) > 2000 {
			inst.LastOutput = inst.LastOutput[len(inst.LastOutput)-2000:]
		}
	}

	// Show checkout modal
	m.checkoutAgent = inst
	m.checkoutStep = 0
	m.handoffTarget = -1
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
		cw, ch, cpr := m.layout.CardWidth, m.layout.CardHeight, m.layout.CardsPerRow
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

	case ":":
		m.pushMode(ModeCommandPalette)
		m.cmdPaletteInput = ""
		m.cmdPaletteCursor = 0
		return m, nil

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
	prevParty := m.activeParty
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
	if m.activeParty != prevParty {
		m.recomputeLayout()
		m.resizeActivePartyAgents()
	}
	return m, nil
}

func (m *Model) resizeActivePartyAgents() {
	p := m.party()
	if p == nil {
		return
	}
	tw := m.layout.TermWidth
	th := m.layout.TermHeight
	for _, inst := range p.Slots {
		if inst != nil && inst.Status == "running" && inst.emulator != nil && inst.ptyFile != nil {
			pty.Setsize(inst.ptyFile, &pty.Winsize{Rows: uint16(th), Cols: uint16(tw)})
			inst.emulator.Resize(tw, th)
		}
	}
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
			m.pushMode(ModeCharSheet)
			m.csSection = 0
			m.csCursor = 0
			m.bioScroll = 0
		}
	case "i":
		inst := m.agent()
		if inst != nil && inst.Status == "running" {
			m.pushMode(ModeInsert)
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
				partyName := ""
				if p != nil {
					if p.Project != "" {
						projectDir = p.Project
					}
					partyName = p.Name
				}
				return m, startAgent(inst, tw, th, m.config, projectDir, partyName)
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
			m.pushMode(ModeSwap)
			m.swapIndex = 0
		}
	case "g":
		return m.toggleGitPanel()
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
			m.pushMode(ModeCharSheet)
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
				partyName := ""
				if p != nil {
					if p.Project != "" {
						projectDir = p.Project
					}
					partyName = p.Name
				}
				return m, startAgent(inst, tw, th, m.config, projectDir, partyName)
			}
		}
	case "g":
		return m.toggleGitPanel()
	}
	return m, nil
}

// ── Insert Mode ────────────────────────────────────────────────────

func (m Model) handleInsertMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.String() == "esc" {
		m.popMode()
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
		m.popMode()
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
		m.recomputeLayout()
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
		m.popMode()
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
				partyName := ""
				if p != nil {
					if p.Project != "" {
						projectDir = p.Project
					}
					partyName = p.Name
				}
				return m, startAgent(inst, tw, th, m.config, projectDir, partyName)
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

	switch m.checkoutStep {
	case 0:
		return m.handleCheckoutXP(msg)
	case 1:
		return m.handleCheckoutScroll(msg)
	case 2:
		return m.handleCheckoutHandoff(msg)
	case 3:
		return m.handleCheckoutWorktree(msg)
	}
	return m, nil
}

func (m Model) handleCheckoutXP(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
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

	// If rated Great, offer to save as Scroll
	if xpGain == 50 {
		m.checkoutStep = 1
		m.scrollNameBuf = ""
		return m, nil
	}

	return m.advanceCheckout(2)
}

// advanceCheckout skips to the next applicable checkout step from the given step.
func (m Model) advanceCheckout(fromStep int) (tea.Model, tea.Cmd) {
	if fromStep <= 2 && m.checkoutAgent.LastOutput != "" {
		m.checkoutStep = 2
		m.handoffTarget = 0
		return m, nil
	}
	if fromStep <= 3 && m.checkoutAgent.Worktree != "" {
		m.checkoutStep = 3
		return m, nil
	}
	m.checkoutAgent = nil
	m.checkoutStep = 0
	m.mode = ModeNormal
	return m, nil
}

func (m Model) handleCheckoutScroll(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter":
		name := strings.TrimSpace(m.scrollNameBuf)
		if name != "" {
			go saveScroll(name, m.checkoutAgent, m.config)
		}
		return m.advanceCheckout(2)
	case "esc":
		return m.advanceCheckout(2)
	case "backspace":
		if len(m.scrollNameBuf) > 0 {
			m.scrollNameBuf = m.scrollNameBuf[:len(m.scrollNameBuf)-1]
		}
	default:
		r := []rune(msg.String())
		if len(r) == 1 && r[0] >= ' ' {
			m.scrollNameBuf += string(r)
		}
	}
	return m, nil
}

// saveScroll persists the agent's effective prompt as a reusable skill (Scroll).
func saveScroll(name string, inst *AgentInstance, cfg *ForgeConfig) {
	composed := ComposePrompt(cfg, inst.ClassName, inst.Equipped, inst.Passives, inst.Directives)
	if composed.Prompt == "" {
		return
	}

	// Sanitize name for directory
	dirName := strings.ToLower(strings.ReplaceAll(name, " ", "-"))
	skillDir := filepath.Join(skillsDir(), dirName)
	os.MkdirAll(skillDir, 0755)

	content := fmt.Sprintf("---\nname: %s\ndescription: Scroll from %s (%s) session\n---\n\n%s\n",
		name, inst.AgentName, inst.ClassName, composed.Prompt)

	os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0644)
}

func (m Model) handleCheckoutHandoff(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	p := m.party()
	if p == nil {
		m.checkoutStep = 2
		return m, nil
	}

	// Build list of other agents in the party (excluding the checkout agent)
	var targets []*AgentInstance
	for _, inst := range p.Slots {
		if inst != nil && inst != m.checkoutAgent {
			targets = append(targets, inst)
		}
	}

	switch msg.String() {
	case "up", "k":
		if m.handoffTarget > 0 {
			m.handoffTarget--
		}
	case "down", "j":
		if m.handoffTarget < len(targets)-1 {
			m.handoffTarget++
		}
	case "enter":
		// Perform handoff to selected target
		if m.handoffTarget >= 0 && m.handoffTarget < len(targets) {
			target := targets[m.handoffTarget]
			handoffCtx := fmt.Sprintf(
				"\n\n## Handoff from %s (%s)\nThe following is the final output from %s's session. Use it as context:\n\n```\n%s\n```",
				m.checkoutAgent.AgentName, m.checkoutAgent.ClassName,
				m.checkoutAgent.AgentName,
				m.checkoutAgent.LastOutput,
			)
			target.HandoffContext = handoffCtx
		}
		return m.advanceCheckout(3)
	case "esc":
		return m.advanceCheckout(3)
	}
	return m, nil
}

func (m Model) handleCheckoutWorktree(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	p := m.partyForAgent(m.checkoutAgent)
	projectDir := "."
	if p != nil && p.Project != "" {
		projectDir = p.Project
	}

	switch msg.String() {
	case "1": // Merge to main
		go cleanupWorktree(projectDir, m.checkoutAgent.Worktree, m.checkoutAgent.Branch, "merge")
		m.checkoutAgent.Worktree = ""
		m.checkoutAgent.Branch = ""
	case "2", "esc": // Keep on branch
		// Worktree stays for next session
	case "3": // Discard
		go cleanupWorktree(projectDir, m.checkoutAgent.Worktree, m.checkoutAgent.Branch, "discard")
		m.checkoutAgent.Worktree = ""
		m.checkoutAgent.Branch = ""
	default:
		return m, nil
	}

	m.checkoutAgent = nil
	m.checkoutStep = 0
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
	// Clean up git worktrees for this party
	go cleanupPartyWorktrees(p.Name, p.Project)
	os.Remove(partyPath(p.Name))
	m.parties = append(m.parties[:m.activeParty], m.parties[m.activeParty+1:]...)
	if m.activeParty >= len(m.parties) {
		m.activeParty = len(m.parties) - 1
	}
	m.selectedAgent = 0
	m.deleteConfirm = false
	m.recomputeLayout()
	m.rebuildAgentIndex()
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

	// Use per-agent avatar if available, otherwise fall back to shared + tinting
	var agentAvatar image.Image
	kittyB64 := def.KittyB64
	if def.AvatarImage != nil {
		agentAvatar = def.AvatarImage
	} else {
		agentAvatar = avatarImage
		if kittyB64 == "" {
			kittyB64 = encodeKittyAvatar(avatarImage, tint)
		}
	}

	equipped := slot.Equipped
	if len(equipped) == 0 {
		equipped = def.DefaultEquipped
	}

	return &AgentInstance{
		ID:         fmt.Sprintf("%s-%d-%s", partyName, idx, def.Name),
		AgentName:  def.Name,
		ClassName:  def.Class,
		Tint:       tint,
		kittyB64:   kittyB64,
		avatarImg:  agentAvatar,
		Bio:        def.Bio,
		Directives: def.Directives,
		Equipped:   equipped,
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

// ── Git Panel ──────────────────────────────────────────────────────

func (m Model) toggleGitPanel() (Model, tea.Cmd) {
	var cmd tea.Cmd
	if !m.showGitPanel {
		// Open panel in files mode
		m.showGitPanel = true
		m.gitPanelMode = 0
		p := m.party()
		if p != nil {
			m.gitTreeLines = loadGitTree(p.Project)
		}
		m.gitPanelScroll = 0
	} else if m.gitPanelMode == 0 {
		// Switch to PR mode
		m.gitPanelMode = 1
		m.gitPanelScroll = 0
		m.prLoading = true
		p := m.party()
		projectDir := "."
		if p != nil && p.Project != "" {
			projectDir = p.Project
		}
		cmd = loadPRList(projectDir)
	} else {
		// Close panel
		m.showGitPanel = false
		m.gitPanelMode = 0
	}

	m.recomputeLayout()
	m.resizeActivePartyAgents()
	return m, cmd
}

func loadGitTree(projectDir string) []string {
	if projectDir == "" {
		projectDir = "."
	}
	out, err := exec.Command("git", "-C", projectDir, "ls-files").Output()
	if err != nil {
		return []string{"(not a git repo)"}
	}

	files := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(files) == 0 || (len(files) == 1 && files[0] == "") {
		return []string{"(no files)"}
	}

	// Build tree structure: map of dir -> children
	type entry struct {
		name  string
		isDir bool
	}
	dirs := make(map[string][]entry)
	seen := make(map[string]bool)

	for _, f := range files {
		parts := strings.Split(f, "/")
		// Register all intermediate directories
		for i := 0; i < len(parts)-1; i++ {
			parent := strings.Join(parts[:i], "/")
			dirName := parts[i]
			key := parent + "/" + dirName
			if !seen[key] {
				seen[key] = true
				dirs[parent] = append(dirs[parent], entry{dirName, true})
			}
		}
		// Register the file
		parent := strings.Join(parts[:len(parts)-1], "/")
		fileName := parts[len(parts)-1]
		dirs[parent] = append(dirs[parent], entry{fileName, false})
	}

	// Render tree recursively
	var lines []string
	var render func(prefix, dir string)
	render = func(prefix, dir string) {
		children := dirs[dir]
		// Directories first, then files, alphabetical within each
		var dirEntries, fileEntries []entry
		for _, c := range children {
			if c.isDir {
				dirEntries = append(dirEntries, c)
			} else {
				fileEntries = append(fileEntries, c)
			}
		}
		for _, d := range dirEntries {
			lines = append(lines, prefix+d.name+"/")
			childDir := d.name
			if dir != "" {
				childDir = dir + "/" + d.name
			}
			render(prefix+"  ", childDir)
		}
		for _, f := range fileEntries {
			lines = append(lines, prefix+f.name)
		}
	}
	render("", "")
	return lines
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
