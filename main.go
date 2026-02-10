package main

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/color"
	_ "image/jpeg"
	"image/png"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/vt"
	"github.com/creack/pty"
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

// Shared avatar image loaded once at startup.
var avatarImage image.Image

func init() {
	f, err := os.Open("assets/avatar.jpg")
	if err != nil {
		return
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	if err != nil {
		return
	}
	avatarImage = img
}

// encodeKittyAvatar creates a tinted version of the avatar and returns it
// as a base64-encoded PNG string for Kitty graphics protocol.
func encodeKittyAvatar(img image.Image, tint color.RGBA) string {
	if img == nil {
		return ""
	}
	tinted := tintImage(img, tint)
	var buf bytes.Buffer
	if err := png.Encode(&buf, tinted); err != nil {
		return ""
	}
	return base64.StdEncoding.EncodeToString(buf.Bytes())
}

// tintImage converts an image to grayscale then multiplies by the tint color.
func tintImage(img image.Image, tint color.RGBA) *image.RGBA {
	bounds := img.Bounds()
	out := image.NewRGBA(bounds)
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			r, g, b, a := img.At(x, y).RGBA()
			// Weighted luminance (16-bit range: 0–65535)
			gray := (r*299 + g*587 + b*114) / 1000
			// Multiply grayscale by tint color
			nr := (gray * uint32(tint.R)) / 255
			ng := (gray * uint32(tint.G)) / 255
			nb := (gray * uint32(tint.B)) / 255
			out.SetRGBA(x, y, color.RGBA{
				R: uint8(nr >> 8),
				G: uint8(ng >> 8),
				B: uint8(nb >> 8),
				A: uint8(a >> 8),
			})
		}
	}
	return out
}

// InputMode controls whether keys go to TUI navigation or the agent PTY.
type InputMode int

const (
	ModeNormal InputMode = iota
	ModeInsert
	ModeSwap
)

// Agent represents an AI agent slot with optional running process.
type Agent struct {
	ID          string
	Name        string
	Status      string // "idle", "running", "exited"
	CurrentTask string
	AvatarPath  string
	Tint        color.RGBA
	kittyB64    string // pre-encoded tinted avatar for Kitty graphics
	cmd         *exec.Cmd
	ptyFile     *os.File
	emulator    *vt.SafeEmulator
}

// Model is the main application state.
type Model struct {
	agents        [4]*Agent
	bench         []*Agent
	selectedAgent int
	swapIndex     int
	width         int
	height        int
	termWidth     int
	termHeight    int
	ready         bool
	mode          InputMode
}

// --- Messages ---

type AgentStartedMsg struct {
	ID       string
	Cmd      *exec.Cmd
	PtyFile  *os.File
	Emulator *vt.SafeEmulator
}

type AgentOutputMsg struct{ ID string }

type AgentExitedMsg struct {
	ID  string
	Err error
}

// forceResizeMsg triggers after a delay to send SIGWINCH so the child
// process re-queries the PTY size and re-renders at the correct dimensions.
type forceResizeMsg struct{ ID string }

// --- Initialization ---

func initialModel() Model {
	allAgents := []*Agent{
		{ID: "agent-1", Name: "Jazzy", Status: "idle", CurrentTask: "Awaiting orders...", AvatarPath: "assets/avatar.jpg", Tint: color.RGBA{240, 208, 128, 255}},
		{ID: "agent-2", Name: "Codex", Status: "idle", CurrentTask: "Awaiting orders...", AvatarPath: "assets/avatar.jpg", Tint: color.RGBA{192, 144, 255, 255}},
		{ID: "agent-3", Name: "Claude", Status: "idle", CurrentTask: "Awaiting orders...", AvatarPath: "assets/avatar.jpg", Tint: color.RGBA{128, 176, 255, 255}},
		{ID: "agent-4", Name: "Pi", Status: "idle", CurrentTask: "Awaiting orders...", AvatarPath: "assets/avatar.jpg", Tint: color.RGBA{128, 224, 160, 255}},
		{ID: "agent-5", Name: "Sage", Status: "idle", CurrentTask: "Awaiting orders...", AvatarPath: "assets/avatar.jpg", Tint: color.RGBA{96, 210, 200, 255}},
		{ID: "agent-6", Name: "Nova", Status: "idle", CurrentTask: "Awaiting orders...", AvatarPath: "assets/avatar.jpg", Tint: color.RGBA{240, 128, 192, 255}},
		{ID: "agent-7", Name: "Byte", Status: "idle", CurrentTask: "Awaiting orders...", AvatarPath: "assets/avatar.jpg", Tint: color.RGBA{240, 176, 96, 255}},
		{ID: "agent-8", Name: "Echo", Status: "idle", CurrentTask: "Awaiting orders...", AvatarPath: "assets/avatar.jpg", Tint: color.RGBA{192, 192, 208, 255}},
		{ID: "agent-9", Name: "Flux", Status: "idle", CurrentTask: "Awaiting orders...", AvatarPath: "assets/avatar.jpg", Tint: color.RGBA{224, 96, 96, 255}},
		{ID: "agent-10", Name: "Zen", Status: "idle", CurrentTask: "Awaiting orders...", AvatarPath: "assets/avatar.jpg", Tint: color.RGBA{176, 160, 240, 255}},
	}

	for _, a := range allAgents {
		a.kittyB64 = encodeKittyAvatar(avatarImage, a.Tint)
	}

	return Model{
		agents:        [4]*Agent{allAgents[0], allAgents[1], allAgents[2], allAgents[3]},
		bench:         allAgents[4:],
		selectedAgent: 0,
		mode:          ModeNormal,
	}
}

func (m Model) Init() tea.Cmd {
	return nil
}

// --- Update ---

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.termWidth = m.width - 2
		_, _, _, _, partyH := m.cardLayout()
		m.termHeight = m.height - partyH - 4 // header(1) + border(2) + status(1)
		if m.termHeight < 3 {
			m.termHeight = 3
		}
		m.ready = true
		for _, agent := range m.agents {
			if agent.Status == "running" && agent.emulator != nil && agent.ptyFile != nil {
				pty.Setsize(agent.ptyFile, &pty.Winsize{
					Rows: uint16(m.termHeight),
					Cols: uint16(m.termWidth),
				})
				agent.emulator.Resize(m.termWidth, m.termHeight)
			}
		}
		return m, nil

	case AgentStartedMsg:
		agent := m.agentByID(msg.ID)
		if agent != nil {
			agent.cmd = msg.Cmd
			agent.ptyFile = msg.PtyFile
			agent.emulator = msg.Emulator
			agent.Status = "running"
			agent.CurrentTask = "Running claude..."
			// Forward emulator responses (DA queries etc.) back to PTY.
			go forwardResponses(agent)
			return m, tea.Batch(
				readAgentPTY(agent),
				delayedResize(agent, m.termWidth, m.termHeight),
			)
		}

	case AgentOutputMsg:
		agent := m.agentByID(msg.ID)
		if agent != nil && agent.Status == "running" {
			return m, readAgentPTY(agent)
		}

	case AgentExitedMsg:
		agent := m.agentByID(msg.ID)
		if agent != nil {
			agent.Status = "exited"
			agent.CurrentTask = "Process exited"
			if m.mode == ModeInsert && m.agents[m.selectedAgent] == agent {
				m.mode = ModeNormal
			}
			ptf := agent.ptyFile
			cmd := agent.cmd
			em := agent.emulator
			agent.ptyFile = nil
			agent.cmd = nil
			agent.emulator = nil
			go func() {
				if em != nil {
					em.Close() // Unblocks the forwardResponses goroutine
				}
				if ptf != nil {
					ptf.Close()
				}
				if cmd != nil {
					cmd.Wait()
				}
			}()
		}

	case forceResizeMsg:
		// SIGWINCH was sent; Parse will pick up the re-rendered output.
		return m, nil

	case tea.MouseMsg:
		return m.handleMouse(msg)

	case tea.KeyMsg:
		switch m.mode {
		case ModeInsert:
			return m.handleInsertMode(msg)
		case ModeSwap:
			return m.handleSwapMode(msg)
		default:
			return m.handleNormalMode(msg)
		}
	}

	return m, nil
}

// --- Mouse Handling ---

func (m Model) handleMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	if msg.Action != tea.MouseActionPress || msg.Button != tea.MouseButtonLeft {
		return m, nil
	}

	// Terminal pane: Y from 1 to termHeight+2 (header=1 line, then border+content)
	termTop := 1
	termBottom := 1 + m.termHeight + 1 // inclusive (border top + content + border bottom)
	if msg.Y >= termTop && msg.Y <= termBottom {
		agent := m.agents[m.selectedAgent]
		if agent.Status == "running" && m.mode == ModeNormal {
			m.mode = ModeInsert
		}
		return m, nil
	}

	// Party bar: starts after terminal pane
	cw, _, _, _, ph := m.cardLayout()
	partyTop := termBottom + 1
	partyBottom := partyTop + ph - 1
	if msg.Y >= partyTop && msg.Y <= partyBottom {
		cardsStartX := 6
		cardTotalWidth := cw + 2 // content + border
		if msg.X >= cardsStartX {
			idx := (msg.X - cardsStartX) / cardTotalWidth
			if idx >= 0 && idx < 4 {
				m.selectedAgent = idx
			}
		}
		return m, nil
	}

	return m, nil
}

// --- Key Handling ---

func (m Model) handleNormalMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "ctrl+c":
		m.stopAllAgents()
		return m, tea.Quit
	case "left", "h":
		if m.selectedAgent > 0 {
			m.selectedAgent--
		}
	case "right", "l":
		if m.selectedAgent < 3 {
			m.selectedAgent++
		}
	case "1", "2", "3", "4":
		m.selectedAgent = int(msg.String()[0] - '1')
	case "s":
		agent := m.agents[m.selectedAgent]
		if (agent.Status == "idle" || agent.Status == "exited") && m.termWidth > 0 && m.termHeight > 0 {
			agent.Status = "idle"
			agent.CurrentTask = "Starting..."
			return m, startAgent(agent, m.termWidth, m.termHeight)
		}
	case "enter", "i":
		agent := m.agents[m.selectedAgent]
		if agent.Status == "running" {
			m.mode = ModeInsert
		}
	case "x":
		agent := m.agents[m.selectedAgent]
		if agent.Status == "running" {
			agent.Status = "exited"
			agent.CurrentTask = "Stopping..."
			if agent.cmd != nil && agent.cmd.Process != nil {
				agent.cmd.Process.Signal(syscall.SIGTERM)
			}
		}
	case " ":
		if len(m.bench) > 0 {
			m.mode = ModeSwap
			m.swapIndex = 0
		}
	}
	return m, nil
}

func (m Model) handleInsertMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.String() == "esc" {
		m.mode = ModeNormal
		return m, nil
	}
	agent := m.agents[m.selectedAgent]
	if agent.ptyFile == nil {
		m.mode = ModeNormal
		return m, nil
	}
	b := keyToBytes(msg)
	if b != nil {
		agent.ptyFile.Write(b)
	}
	return m, nil
}

func (m Model) handleSwapMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.mode = ModeNormal
	case "left", "h":
		if m.swapIndex > 0 {
			m.swapIndex--
		} else {
			m.swapIndex = len(m.bench) - 1
		}
	case "right", "l":
		if m.swapIndex < len(m.bench)-1 {
			m.swapIndex++
		} else {
			m.swapIndex = 0
		}
	case " ", "enter":
		if len(m.bench) > 0 {
			old := m.agents[m.selectedAgent]
			swapped := m.bench[m.swapIndex]
			m.agents[m.selectedAgent] = swapped
			m.bench[m.swapIndex] = old
			// Resize newly placed agent if it's running.
			if swapped.Status == "running" && swapped.emulator != nil && swapped.ptyFile != nil && m.termWidth > 0 && m.termHeight > 0 {
				pty.Setsize(swapped.ptyFile, &pty.Winsize{
					Rows: uint16(m.termHeight),
					Cols: uint16(m.termWidth),
				})
				swapped.emulator.Resize(m.termWidth, m.termHeight)
			}
		}
		m.mode = ModeNormal
	}
	return m, nil
}

func keyToBytes(msg tea.KeyMsg) []byte {
	switch msg.Type {
	case tea.KeyEnter:
		return []byte{'\r'}
	case tea.KeyTab:
		return []byte{'\t'}
	case tea.KeyBackspace:
		return []byte{127}
	case tea.KeySpace:
		return []byte{' '}
	case tea.KeyUp:
		return []byte("\x1b[A")
	case tea.KeyDown:
		return []byte("\x1b[B")
	case tea.KeyRight:
		return []byte("\x1b[C")
	case tea.KeyLeft:
		return []byte("\x1b[D")
	case tea.KeyHome:
		return []byte("\x1b[H")
	case tea.KeyEnd:
		return []byte("\x1b[F")
	case tea.KeyDelete:
		return []byte("\x1b[3~")
	case tea.KeyPgUp:
		return []byte("\x1b[5~")
	case tea.KeyPgDown:
		return []byte("\x1b[6~")
	case tea.KeyF1:
		return []byte("\x1bOP")
	case tea.KeyF2:
		return []byte("\x1bOQ")
	case tea.KeyF3:
		return []byte("\x1bOR")
	case tea.KeyF4:
		return []byte("\x1bOS")
	case tea.KeyF5:
		return []byte("\x1b[15~")
	case tea.KeyF6:
		return []byte("\x1b[17~")
	case tea.KeyF7:
		return []byte("\x1b[18~")
	case tea.KeyF8:
		return []byte("\x1b[19~")
	case tea.KeyF9:
		return []byte("\x1b[20~")
	case tea.KeyF10:
		return []byte("\x1b[21~")
	case tea.KeyF11:
		return []byte("\x1b[23~")
	case tea.KeyF12:
		return []byte("\x1b[24~")
	case tea.KeyCtrlA:
		return []byte{0x01}
	case tea.KeyCtrlB:
		return []byte{0x02}
	case tea.KeyCtrlC:
		return []byte{0x03}
	case tea.KeyCtrlD:
		return []byte{0x04}
	case tea.KeyCtrlE:
		return []byte{0x05}
	case tea.KeyCtrlF:
		return []byte{0x06}
	case tea.KeyCtrlG:
		return []byte{0x07}
	case tea.KeyCtrlH:
		return []byte{0x08}
	case tea.KeyCtrlK:
		return []byte{0x0b}
	case tea.KeyCtrlL:
		return []byte{0x0c}
	case tea.KeyCtrlN:
		return []byte{0x0e}
	case tea.KeyCtrlO:
		return []byte{0x0f}
	case tea.KeyCtrlP:
		return []byte{0x10}
	case tea.KeyCtrlQ:
		return []byte{0x11}
	case tea.KeyCtrlR:
		return []byte{0x12}
	case tea.KeyCtrlS:
		return []byte{0x13}
	case tea.KeyCtrlT:
		return []byte{0x14}
	case tea.KeyCtrlU:
		return []byte{0x15}
	case tea.KeyCtrlV:
		return []byte{0x16}
	case tea.KeyCtrlW:
		return []byte{0x17}
	case tea.KeyCtrlX:
		return []byte{0x18}
	case tea.KeyCtrlY:
		return []byte{0x19}
	case tea.KeyCtrlZ:
		return []byte{0x1a}
	case tea.KeyRunes:
		return []byte(string(msg.Runes))
	}
	return nil
}

// --- PTY Lifecycle ---

func startAgent(agent *Agent, cols, rows int) tea.Cmd {
	id := agent.ID
	return func() tea.Msg {
		em := vt.NewSafeEmulator(cols, rows)

		cmd := exec.Command("claude")
		cmd.Env = append(os.Environ(), "TERM=xterm-256color")

		ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{
			Rows: uint16(rows),
			Cols: uint16(cols),
		})
		if err != nil {
			em.Close()
			return AgentExitedMsg{ID: id, Err: err}
		}

		return AgentStartedMsg{
			ID:       id,
			Cmd:      cmd,
			PtyFile:  ptmx,
			Emulator: em,
		}
	}
}

func readAgentPTY(agent *Agent) tea.Cmd {
	id := agent.ID
	ptf := agent.ptyFile
	em := agent.emulator
	return func() tea.Msg {
		if ptf == nil || em == nil {
			return AgentExitedMsg{ID: id}
		}
		buf := make([]byte, 32*1024)
		n, err := ptf.Read(buf)
		if err != nil {
			return AgentExitedMsg{ID: id, Err: err}
		}
		em.Write(buf[:n])
		return AgentOutputMsg{ID: id}
	}
}

// forwardResponses reads terminal query responses from the emulator and
// writes them back to the PTY so the child process receives them.
func forwardResponses(agent *Agent) {
	buf := make([]byte, 1024)
	for {
		n, err := agent.emulator.Read(buf)
		if n > 0 && agent.ptyFile != nil {
			agent.ptyFile.Write(buf[:n])
		}
		if err != nil {
			return
		}
	}
}

// delayedResize waits for the child process to initialize, then performs a
// "size jiggle": first resize to (rows-1, cols) then back to (rows, cols).
// This guarantees the child sees a genuine SIGWINCH with an actual dimension
// change, forcing a full re-render at the correct size.
func delayedResize(agent *Agent, cols, rows int) tea.Cmd {
	id := agent.ID
	return func() tea.Msg {
		time.Sleep(300 * time.Millisecond)
		if agent.ptyFile == nil || agent.emulator == nil {
			return forceResizeMsg{ID: id}
		}
		// Step 1: shrink by one row — genuine size change triggers SIGWINCH.
		pty.Setsize(agent.ptyFile, &pty.Winsize{
			Rows: uint16(rows - 1),
			Cols: uint16(cols),
		})
		agent.emulator.Resize(cols, rows-1)
		time.Sleep(100 * time.Millisecond)

		// Step 2: restore to real size — another genuine SIGWINCH.
		pty.Setsize(agent.ptyFile, &pty.Winsize{
			Rows: uint16(rows),
			Cols: uint16(cols),
		})
		agent.emulator.Resize(cols, rows)
		return forceResizeMsg{ID: id}
	}
}

func (m Model) stopAllAgents() {
	all := make([]*Agent, 0, 4+len(m.bench))
	for _, a := range m.agents {
		all = append(all, a)
	}
	all = append(all, m.bench...)
	for _, agent := range all {
		if agent.Status == "running" && agent.cmd != nil && agent.cmd.Process != nil {
			agent.cmd.Process.Signal(syscall.SIGTERM)
			agent.Status = "exited"
			agent.CurrentTask = "Stopping..."
		}
	}
}

// cardLayout computes dynamic card dimensions based on terminal width.
// Avatar fills the card width; height is derived to maintain square aspect ratio.
func (m Model) cardLayout() (cardWidth, avatarCols, avatarRows, cardHeight, partyHeight int) {
	cardWidth = (m.width - 20) / 4
	if cardWidth < 20 {
		cardWidth = 20
	}
	if cardWidth > 40 {
		cardWidth = 40
	}
	avatarCols = cardWidth
	avatarRows = avatarCols / 2 // square aspect with half-blocks
	cardHeight = avatarRows + 2 // name + status
	partyHeight = cardHeight + 2 // + border
	return
}

func (m Model) agentByID(id string) *Agent {
	for _, a := range m.agents {
		if a.ID == id {
			return a
		}
	}
	for _, a := range m.bench {
		if a.ID == id {
			return a
		}
	}
	return nil
}

// --- Kitty Graphics Protocol ---

// kittyImageSeq returns a Kitty graphics escape sequence that transmits and
// displays a base64-encoded PNG image spanning cols×rows character cells.
// Uses chunked transmission (4096 bytes per chunk) and q=2 to suppress responses.
func kittyImageSeq(b64Data string, cols, rows int) string {
	var buf strings.Builder
	const chunkSize = 4096
	total := len(b64Data)
	for i := 0; i < total; i += chunkSize {
		end := i + chunkSize
		if end > total {
			end = total
		}
		chunk := b64Data[i:end]
		more := 1
		if end >= total {
			more = 0
		}
		if i == 0 {
			fmt.Fprintf(&buf, "\x1b_Gf=100,t=d,a=T,c=%d,r=%d,q=2,m=%d;%s\x1b\\", cols, rows, more, chunk)
		} else {
			fmt.Fprintf(&buf, "\x1b_Gm=%d;%s\x1b\\", more, chunk)
		}
	}
	return buf.String()
}

// --- Avatar Rendering ---

// renderHalfBlockAvatar renders an image as colored half-block characters.
// Each character row represents 2 pixel rows using ▄ with fg=bottom, bg=top.
func renderHalfBlockAvatar(img image.Image, cols, rows int) string {
	if img == nil {
		return lipgloss.NewStyle().
			Width(cols).Height(rows).
			Foreground(colorTextDim).
			Align(lipgloss.Center, lipgloss.Center).
			Render("?")
	}

	pixelH := rows * 2
	bounds := img.Bounds()
	srcW := bounds.Dx()
	srcH := bounds.Dy()

	var buf strings.Builder
	for py := 0; py < pixelH; py += 2 {
		for px := 0; px < cols; px++ {
			srcX := bounds.Min.X + (px * srcW / cols)
			srcY1 := bounds.Min.Y + (py * srcH / pixelH)
			srcY2 := bounds.Min.Y + ((py + 1) * srcH / pixelH)

			r1, g1, b1, _ := img.At(srcX, srcY1).RGBA()
			r2, g2, b2, _ := img.At(srcX, srcY2).RGBA()

			// fg = bottom pixel (▄ foreground), bg = top pixel (▄ background)
			buf.WriteString(fmt.Sprintf("\x1b[38;2;%d;%d;%dm\x1b[48;2;%d;%d;%dm▄",
				r2>>8, g2>>8, b2>>8,
				r1>>8, g1>>8, b1>>8,
			))
		}
		buf.WriteString("\x1b[m")
		if py+2 < pixelH {
			buf.WriteByte('\n')
		}
	}
	return buf.String()
}

// --- View ---

func (m Model) View() string {
	if !m.ready {
		return "Loading..."
	}

	agent := m.agents[m.selectedAgent]

	// === HEADER ===
	modeStr := "NORMAL"
	modeColor := colorTextDim
	switch m.mode {
	case ModeInsert:
		modeStr = "INSERT"
		modeColor = colorGreen
	case ModeSwap:
		modeStr = "SWAP"
		modeColor = colorYellow
	}

	headerStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(colorTextBright).
		Background(colorBgMedium).
		Width(m.width).
		Padding(0, 2)

	modeIndicator := lipgloss.NewStyle().
		Foreground(modeColor).
		Bold(true).
		Render("[" + modeStr + "]")

	header := headerStyle.Render(
		"⚔️  AGENT FORGE" +
			strings.Repeat(" ", max(0, m.width-45)) +
			modeIndicator,
	)

	// === TERMINAL PANE ===
	termBorderColor := colorBorder
	if m.mode == ModeInsert {
		termBorderColor = colorBorderGold
	}

	var terminal string
	switch agent.Status {
	case "running":
		style := lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(termBorderColor)
		// Render() returns a full ANSI-encoded screen snapshot with 24-bit color.
		// Use \n line separators (Render uses \r\n).
		screen := strings.ReplaceAll(agent.emulator.Render(), "\r\n", "\n")
		terminal = style.Render(screen)
	case "exited":
		style := lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(termBorderColor).
			Width(m.termWidth).
			Height(m.termHeight)
		placeholder := lipgloss.NewStyle().
			Foreground(colorTextDim).
			Width(m.termWidth).
			Height(m.termHeight).
			Align(lipgloss.Center, lipgloss.Center).
			Render("Process exited. Press 's' to restart.")
		terminal = style.Render(placeholder)
	default:
		style := lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(termBorderColor).
			Width(m.termWidth).
			Height(m.termHeight)
		placeholder := lipgloss.NewStyle().
			Foreground(colorTextDim).
			Width(m.termWidth).
			Height(m.termHeight).
			Align(lipgloss.Center, lipgloss.Center).
			Render("Press 's' to start claude")
		terminal = style.Render(placeholder)
	}

	// === PARTY BAR ===
	partyBar := m.renderPartyBar()

	// === STATUS BAR ===
	statusStyle := lipgloss.NewStyle().
		Background(colorBgMedium).
		Foreground(colorText).
		Width(m.width).
		Padding(0, 2)

	var hints string
	switch m.mode {
	case ModeInsert:
		hints = lipgloss.NewStyle().Foreground(colorTextDim).Render("esc:normal mode")
	case ModeSwap:
		benchAgent := ""
		if len(m.bench) > 0 {
			benchAgent = m.bench[m.swapIndex].Name
		}
		hints = lipgloss.NewStyle().Foreground(colorTextDim).Render(
			fmt.Sprintf("←→:cycle (%s %d/%d)  space/enter:confirm  esc:cancel", benchAgent, m.swapIndex+1, len(m.bench)),
		)
	default:
		hints = lipgloss.NewStyle().Foreground(colorTextDim).Render("s:start  i:insert  x:stop  space:swap  ←→:select  1-4:jump  q:quit")
	}

	statusBar := statusStyle.Render(
		fmt.Sprintf("Agent: %s │ %s │ %s",
			lipgloss.NewStyle().Bold(true).Render(agent.Name),
			lipgloss.NewStyle().Foreground(statusColor(agent.Status)).Render(strings.ToUpper(agent.Status)),
			hints,
		),
	)

	// === COMPOSE ===
	view := lipgloss.JoinVertical(
		lipgloss.Left,
		header,
		terminal,
		partyBar,
		statusBar,
	)

	// === KITTY GRAPHICS OVERLAY ===
	// Overlay pixel-perfect avatar images using cursor positioning.
	// Ghostty and other Kitty-compatible terminals render these natively.
	// Delete all previous Kitty images first to prevent tiling on resize.
	view += "\x1b_Ga=d,d=a,q=2\x1b\\"
	cw, avatarCols, avatarRows, _, _ := m.cardLayout()
	// Avatar row: header(1) + terminal(termHeight+2) + card top border(1) + 1
	avatarRow := m.termHeight + 5 // 1-indexed ANSI row
	// Column prefix: outer padding(1) + label(2) + space(1) = 4
	colPrefix := 5 // 1-indexed
	for i := 0; i < 4; i++ {
		b64 := m.agents[i].kittyB64
		// In swap mode, show the bench agent preview for the selected slot.
		if m.mode == ModeSwap && i == m.selectedAgent && len(m.bench) > 0 {
			b64 = m.bench[m.swapIndex].kittyB64
		}
		if b64 == "" {
			continue
		}
		col := colPrefix + i*(cw+2)
		view += fmt.Sprintf("\x1b7\x1b[%d;%dH", avatarRow, col)
		view += kittyImageSeq(b64, avatarCols, avatarRows)
		view += "\x1b8"
	}

	return view
}

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

func (m Model) renderPartyBar() string {
	cardWidth, avatarCols, avatarRows, cardHeight, _ := m.cardLayout()

	cardStyle := lipgloss.NewStyle().
		Width(cardWidth).
		Height(cardHeight).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colorBorder).
		Align(lipgloss.Center, lipgloss.Center)

	selectedCardStyle := cardStyle.
		BorderForeground(colorBorderGold).
		Background(colorBgLight)

	swapCardStyle := cardStyle.
		BorderForeground(colorYellow).
		Background(colorBgMedium)

	var cards []string
	for i, agent := range m.agents {
		displayAgent := agent
		style := cardStyle
		if i == m.selectedAgent {
			if m.mode == ModeSwap && len(m.bench) > 0 {
				displayAgent = m.bench[m.swapIndex]
				style = swapCardStyle
			} else {
				style = selectedCardStyle
			}
		}

		sc := statusColor(displayAgent.Status)

		// Use space placeholder for avatar area — Kitty graphics overlay
		// renders the tinted image on top via cursor positioning in View().
		var avatar string
		if displayAgent.kittyB64 != "" {
			lines := make([]string, avatarRows)
			for r := range lines {
				lines[r] = strings.Repeat(" ", avatarCols)
			}
			avatar = strings.Join(lines, "\n")
		} else {
			avatar = renderHalfBlockAvatar(avatarImage, avatarCols, avatarRows)
		}

		nameStyle := lipgloss.NewStyle().Bold(true).Foreground(colorTextBright)
		statStyle := lipgloss.NewStyle().Foreground(sc)

		content := lipgloss.JoinVertical(
			lipgloss.Center,
			avatar,
			nameStyle.Render(displayAgent.Name),
			statStyle.Render(strings.ToUpper(displayAgent.Status)),
		)

		cards = append(cards, style.Render(content))
	}

	partyLabel := lipgloss.NewStyle().
		Foreground(colorYellow).
		Bold(true).
		MarginRight(1).
		Render("⚔\nP\nA\nR\nT\nY")

	cardsRow := lipgloss.JoinHorizontal(lipgloss.Top, cards...)

	return lipgloss.NewStyle().
		Background(colorBgDark).
		Padding(0, 1).
		Render(
			lipgloss.JoinHorizontal(lipgloss.Center, partyLabel, " ", cardsRow),
		)
}

// --- Main ---

func main() {
	p := tea.NewProgram(
		initialModel(),
		tea.WithAltScreen(),
		tea.WithMouseCellMotion(),
	)

	if _, err := p.Run(); err != nil {
		fmt.Printf("Error: %v", err)
		os.Exit(1)
	}
}
