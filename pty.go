package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/vt"
	"github.com/creack/pty"
)

// ── Messages ───────────────────────────────────────────────────────

type AgentStartedMsg struct {
	ID       string
	Cmd      *exec.Cmd
	PtyFile  *os.File
	Emulator *vt.SafeEmulator
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

// startAgent launches a claude process with skill-composed prompt and tool restrictions.
func startAgent(inst *AgentInstance, cols, rows int, cfg *ForgeConfig, projectDir string) tea.Cmd {
	id := inst.ID
	return func() tea.Msg {
		em := vt.NewSafeEmulator(cols, rows)

		// Compose the system prompt from equipped skills
		composed := ComposePrompt(cfg, inst.ClassName, inst.Equipped, inst.Passives, inst.Directives)

		// Build command args
		args := []string{}
		if composed.Prompt != "" {
			args = append(args, "--append-system-prompt", composed.Prompt)
		}

		// Tool restrictions
		tools := BuildAllowedTools(cfg, inst.ClassName)
		if len(tools) > 0 {
			args = append(args, "--allowedTools", strings.Join(tools, ","))
		}

		// Model override (if set)
		if inst.Model != "" {
			args = append(args, "--model", inst.Model)
		}

		cmd := exec.Command("claude", args...)
		cmd.Dir = projectDir
		cmd.Env = append(os.Environ(), "TERM=xterm-256color")

		ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{
			Rows: uint16(rows),
			Cols: uint16(cols),
		})
		if err != nil {
			em.Close()
			return AgentExitedMsg{ID: id, Err: err}
		}

		// Save audit copy of effective prompt
		go saveAuditPrompt(inst.ID, composed.Prompt, args)

		return AgentStartedMsg{
			ID:       id,
			Cmd:      cmd,
			PtyFile:  ptmx,
			Emulator: em,
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
		buf := make([]byte, 32*1024)
		n, err := ptf.Read(buf)
		if err != nil {
			return AgentExitedMsg{ID: id, Err: err}
		}
		em.Write(buf[:n])
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
