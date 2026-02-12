package main

import (
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"
)

// runRaid executes a party headlessly — no TUI, just parallel agent processes.
// Usage: orc raid --party <name> [--mission "description"]
func runRaid(args []string) error {
	var partyName, mission string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--party":
			if i+1 < len(args) {
				partyName = args[i+1]
				i++
			}
		case "--mission":
			if i+1 < len(args) {
				mission = args[i+1]
				i++
			}
		}
	}
	if partyName == "" {
		return fmt.Errorf("usage: orc raid --party <name> [--mission \"description\"]")
	}

	cfg, _, err := loadForgeConfig()
	if err != nil {
		return err
	}

	pf, err := LoadParty(partyName)
	if err != nil {
		return fmt.Errorf("party %q not found: %w", partyName, err)
	}

	projectDir := pf.Project
	if projectDir == "" {
		projectDir, _ = os.Getwd()
	}

	fmt.Printf("⚔️  RAID MODE: %s\n", partyName)
	fmt.Printf("   Project: %s\n", projectDir)
	if mission != "" {
		fmt.Printf("   Mission: %s\n", mission)
	}
	fmt.Printf("   Agents: %d\n\n", len(pf.Slots))

	agentMap := make(map[string]*AgentConfig)
	for i := range cfg.Agents {
		agentMap[cfg.Agents[i].Name] = &cfg.Agents[i]
	}

	// Handle graceful shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	var wg sync.WaitGroup
	var procs []*exec.Cmd
	var mu sync.Mutex

	for i, slot := range pf.Slots {
		def := agentMap[slot.Agent]
		if def == nil {
			fmt.Printf("   [%d] %s: agent not found, skipping\n", i+1, slot.Agent)
			continue
		}

		equipped := slot.Equipped
		if len(equipped) == 0 {
			equipped = def.DefaultEquipped
		}

		directives := def.Directives
		composed := ComposePrompt(cfg, def.Class, equipped, slot.Passives, directives)

		args := []string{}

		prompt := composed.Prompt
		if mission != "" {
			prompt += fmt.Sprintf("\n\n## Mission\n%s", mission)
		}
		if prompt != "" {
			args = append(args, "--append-system-prompt", prompt)
		}

		tools := BuildAllowedTools(cfg, def.Class)
		if len(tools) > 0 {
			args = append(args, "--allowedTools", strings.Join(tools, ","))
		}

		// Setup worktree for isolation
		workDir := projectDir
		if wt, _, wtErr := setupWorktree(partyName, def.Name, projectDir); wtErr == nil {
			workDir = wt
		}

		cmd := exec.Command("claude", args...)
		cmd.Dir = workDir
		cmd.Env = append(os.Environ(), "TERM=xterm-256color")
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr

		mu.Lock()
		procs = append(procs, cmd)
		mu.Unlock()

		wg.Add(1)
		agentName := def.Name
		idx := i + 1
		go func() {
			defer wg.Done()
			start := time.Now()
			fmt.Printf("   [%d] %s: starting...\n", idx, agentName)
			if err := cmd.Run(); err != nil {
				fmt.Printf("   [%d] %s: exited with error: %v (%.1fs)\n",
					idx, agentName, err, time.Since(start).Seconds())
			} else {
				fmt.Printf("   [%d] %s: completed (%.1fs)\n",
					idx, agentName, time.Since(start).Seconds())
			}
		}()
	}

	// Wait for all agents or signal
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		fmt.Println("\n⚔️  RAID COMPLETE")
	case sig := <-sigCh:
		fmt.Printf("\n⚔️  Received %s, stopping agents...\n", sig)
		mu.Lock()
		for _, cmd := range procs {
			if cmd.Process != nil {
				cmd.Process.Signal(syscall.SIGTERM)
			}
		}
		mu.Unlock()
		<-done
		fmt.Println("⚔️  RAID ABORTED")
	}

	return nil
}
