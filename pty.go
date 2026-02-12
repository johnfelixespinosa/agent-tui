package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/vt"
	"github.com/creack/pty"
)

var contextPattern = regexp.MustCompile(`(\d+(?:\.\d+)?)\s*[Kk]\s*/\s*(\d+(?:\.\d+)?)\s*[Kk]\s*tokens`)

var ptyBufPool = sync.Pool{New: func() any { return make([]byte, 32*1024) }}

// ── Agent Launcher Interface ──────────────────────────────────────

// LaunchConfig holds all parameters needed to launch an agent process.
type LaunchConfig struct {
	ID             string
	AgentName      string
	ClassName      string
	Equipped       []string
	Passives       []string
	Model          string
	Directives     string
	HandoffContext string
	ProjectDir     string
	PartyName      string
	Cols           int
	Rows           int
}

// AgentLauncher abstracts agent process creation for testability.
type AgentLauncher interface {
	Launch(cfg *ForgeConfig, lc LaunchConfig) tea.Cmd
}

// PtyLauncher is the production implementation that creates real PTY processes.
type PtyLauncher struct{}

func (PtyLauncher) Launch(cfg *ForgeConfig, lc LaunchConfig) tea.Cmd {
	return startAgentProcess(cfg, lc)
}

// DefaultLauncher is the production launcher.
var DefaultLauncher AgentLauncher = PtyLauncher{}

// ── Messages ───────────────────────────────────────────────────────

type AgentStartedMsg struct {
	ID       string
	Cmd      *exec.Cmd
	PtyFile  *os.File
	Emulator *vt.SafeEmulator
	Worktree string // path to git worktree (empty if not isolated)
	Branch   string // git branch for this worktree
}

type AgentOutputMsg struct {
	ID        string
	BytesRead int
}

type AgentExitedMsg struct {
	ID  string
	Err error
}

type forceResizeMsg struct{ ID string }

// ── Agent Launch ───────────────────────────────────────────────────

// startAgent builds a LaunchConfig from an AgentInstance and delegates to the launcher.
func startAgent(inst *AgentInstance, cols, rows int, cfg *ForgeConfig, projectDir, partyName string) tea.Cmd {
	handoff := inst.HandoffContext
	inst.HandoffContext = "" // consume handoff
	return DefaultLauncher.Launch(cfg, LaunchConfig{
		ID:             inst.ID,
		AgentName:      inst.AgentName,
		ClassName:      inst.ClassName,
		Equipped:       inst.Equipped,
		Passives:       inst.Passives,
		Model:          inst.Model,
		Directives:     inst.Directives,
		HandoffContext: handoff,
		ProjectDir:     projectDir,
		PartyName:      partyName,
		Cols:           cols,
		Rows:           rows,
	})
}

// startAgentProcess is the production implementation that launches a real PTY process.
func startAgentProcess(cfg *ForgeConfig, lc LaunchConfig) tea.Cmd {
	return func() tea.Msg {
		em := vt.NewSafeEmulator(lc.Cols, lc.Rows)

		// Compose the system prompt from equipped skills
		composed := ComposePrompt(cfg, lc.ClassName, lc.Equipped, lc.Passives, lc.Directives)

		// Build command args
		prompt := composed.Prompt
		if lc.HandoffContext != "" {
			prompt += lc.HandoffContext
		}
		args := []string{}
		if prompt != "" {
			args = append(args, "--append-system-prompt", prompt)
		}

		// Tool restrictions
		tools := BuildAllowedTools(cfg, lc.ClassName)
		if len(tools) > 0 {
			args = append(args, "--allowedTools", strings.Join(tools, ","))
		}

		// Model override (if set)
		if lc.Model != "" {
			args = append(args, "--model", lc.Model)
		}

		// Setup git worktree isolation (falls back to projectDir if not a git repo)
		workDir := lc.ProjectDir
		var worktree, branch string
		if wt, br, err := setupWorktree(lc.PartyName, lc.AgentName, lc.ProjectDir); err == nil {
			workDir = wt
			worktree = wt
			branch = br
		}

		cmd := exec.Command("claude", args...)
		cmd.Dir = workDir
		cmd.Env = append(os.Environ(), "TERM=xterm-256color")

		ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{
			Rows: uint16(lc.Rows),
			Cols: uint16(lc.Cols),
		})
		if err != nil {
			em.Close()
			return AgentExitedMsg{ID: lc.ID, Err: err}
		}

		// Save audit copy of effective prompt
		go saveAuditPrompt(lc.ID, composed.Prompt, args)

		return AgentStartedMsg{
			ID:       lc.ID,
			Cmd:      cmd,
			PtyFile:  ptmx,
			Emulator: em,
			Worktree: worktree,
			Branch:   branch,
		}
	}
}

// saveAuditPrompt writes the effective prompt to a session audit file.
func saveAuditPrompt(agentID, prompt string, args []string) {
	sessionDir := filepath.Join(sessionsDir(), agentID+"-"+time.Now().Format("20060102-150405"))
	os.MkdirAll(sessionDir, 0755)

	content := fmt.Sprintf("# Effective Prompt for %s\n\nLaunch time: %s\n\n## CLI Args\n```\nclaude %s\n```\n\n## System Prompt\n\n%s\n",
		agentID, time.Now().Format(time.RFC3339), strings.Join(args, " "), prompt)

	os.WriteFile(filepath.Join(sessionDir, "effective_prompt.md"), []byte(content), 0644)
}

// ── PTY I/O ────────────────────────────────────────────────────────

func readAgentPTY(inst *AgentInstance) tea.Cmd {
	id := inst.ID
	ptf := inst.ptyFile
	em := inst.emulator
	return func() tea.Msg {
		if ptf == nil || em == nil {
			return AgentExitedMsg{ID: id}
		}
		buf := ptyBufPool.Get().([]byte)
		n, err := ptf.Read(buf)
		if err != nil {
			ptyBufPool.Put(buf)
			return AgentExitedMsg{ID: id, Err: err}
		}
		em.Write(buf[:n])
		ptyBufPool.Put(buf)
		return AgentOutputMsg{ID: id, BytesRead: n}
	}
}

// forwardResponses reads terminal query responses from the emulator and
// writes them back to the PTY so the child process receives them.
func forwardResponses(inst *AgentInstance) {
	buf := make([]byte, 1024)
	for {
		n, err := inst.emulator.Read(buf)
		if n > 0 && inst.ptyFile != nil {
			inst.ptyFile.Write(buf[:n])
		}
		if err != nil {
			return
		}
	}
}

// ── Git Worktree Isolation ─────────────────────────────────────────

// setupWorktree creates (or reuses) a git worktree for an agent.
// Returns the worktree path and branch name, or an error if the project
// is not a git repo or worktree creation fails.
func setupWorktree(partyName, agentName, projectDir string) (string, string, error) {
	if projectDir == "" || projectDir == "." {
		cwd, _ := os.Getwd()
		projectDir = cwd
	}

	// Check if projectDir is a git repo
	out, err := exec.Command("git", "-C", projectDir, "rev-parse", "--is-inside-work-tree").Output()
	if err != nil || strings.TrimSpace(string(out)) != "true" {
		return "", "", fmt.Errorf("not a git repo")
	}

	branch := fmt.Sprintf("forge/%s/%s", partyName, strings.ToLower(agentName))
	wtPath := filepath.Join(worktreesDir(), partyName, strings.ToLower(agentName))

	// Reuse existing valid worktree
	if _, statErr := os.Stat(wtPath); statErr == nil {
		if exec.Command("git", "-C", wtPath, "rev-parse", "--is-inside-work-tree").Run() == nil {
			return wtPath, branch, nil
		}
		// Stale worktree — clean up
		exec.Command("git", "-C", projectDir, "worktree", "remove", "--force", wtPath).Run()
		os.RemoveAll(wtPath)
	}

	// Prune stale worktree entries
	exec.Command("git", "-C", projectDir, "worktree", "prune").Run()

	os.MkdirAll(filepath.Dir(wtPath), 0755)

	// Try existing branch first, then create new
	if err := exec.Command("git", "-C", projectDir, "worktree", "add", wtPath, branch).Run(); err != nil {
		if err := exec.Command("git", "-C", projectDir, "worktree", "add", "-b", branch, wtPath).Run(); err != nil {
			return "", "", fmt.Errorf("git worktree add: %w", err)
		}
	}

	return wtPath, branch, nil
}

// cleanupWorktree handles worktree disposition after agent session ends.
// Actions: "merge" (squash into main), "keep" (leave as-is), "discard" (remove).
func cleanupWorktree(projectDir, wtPath, branch, action string) {
	switch action {
	case "merge":
		exec.Command("git", "-C", projectDir, "merge", "--squash", branch).Run()
		exec.Command("git", "-C", projectDir, "commit", "--no-edit", "-m",
			fmt.Sprintf("Merge work from %s", branch)).Run()
		exec.Command("git", "-C", projectDir, "worktree", "remove", "--force", wtPath).Run()
		exec.Command("git", "-C", projectDir, "branch", "-D", branch).Run()
	case "discard":
		exec.Command("git", "-C", projectDir, "worktree", "remove", "--force", wtPath).Run()
		exec.Command("git", "-C", projectDir, "branch", "-D", branch).Run()
	}
	// "keep" is a no-op — worktree and branch stay for next session
}

// cleanupPartyWorktrees removes all worktrees for a deleted party.
func cleanupPartyWorktrees(partyName, projectDir string) {
	wtDir := filepath.Join(worktreesDir(), partyName)
	entries, err := os.ReadDir(wtDir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() {
			wtPath := filepath.Join(wtDir, e.Name())
			branch := fmt.Sprintf("forge/%s/%s", partyName, e.Name())
			exec.Command("git", "-C", projectDir, "worktree", "remove", "--force", wtPath).Run()
			exec.Command("git", "-C", projectDir, "branch", "-D", branch).Run()
		}
	}
	os.RemoveAll(wtDir)
}

// parseContextFromTerminal scans rendered terminal output for context usage info.
func parseContextFromTerminal(inst *AgentInstance) {
	if inst.emulator == nil {
		return
	}
	screen := inst.emulator.Render()
	// Scan last ~500 chars for context patterns
	if len(screen) > 500 {
		screen = screen[len(screen)-500:]
	}
	matches := contextPattern.FindStringSubmatch(screen)
	if len(matches) >= 3 {
		if used, err := strconv.ParseFloat(matches[1], 64); err == nil {
			if max, err := strconv.ParseFloat(matches[2], 64); err == nil {
				inst.ContextTokens = int(used * 1000)
				inst.ContextMax = int(max * 1000)
			}
		}
	}
}

// delayedResize performs a "size jiggle" to force SIGWINCH.
func delayedResize(inst *AgentInstance, cols, rows int) tea.Cmd {
	id := inst.ID
	return func() tea.Msg {
		time.Sleep(300 * time.Millisecond)
		if inst.ptyFile == nil || inst.emulator == nil {
			return forceResizeMsg{ID: id}
		}
		pty.Setsize(inst.ptyFile, &pty.Winsize{
			Rows: uint16(rows - 1),
			Cols: uint16(cols),
		})
		inst.emulator.Resize(cols, rows-1)
		time.Sleep(100 * time.Millisecond)

		pty.Setsize(inst.ptyFile, &pty.Winsize{
			Rows: uint16(rows),
			Cols: uint16(cols),
		})
		inst.emulator.Resize(cols, rows)
		return forceResizeMsg{ID: id}
	}
}
