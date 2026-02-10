# Agent TUI - Handoff Document

## Project Overview

**Goal:** Build a terminal-based UI (TUI) for AI agent orchestration with an RPG/Stoneshard aesthetic. Think "lazygit but for managing AI agents" with pixel art avatars and a party system like classic RPGs.

**Location:** `~/Desktop/Projects/agent-tui/`

## Current State

### What's Built
- Go + Bubbletea/Lipgloss TUI application
- 4 agent "party members" displayed in a horizontal bar at bottom
- Terminal output pane showing selected agent's output
- Kitty graphics protocol implementation for inline images (partially working)
- Stoneshard-inspired color palette (warm browns, golds, cream text)

### What's Broken (see screenshot)
- **Layout is glitching/repeating** - the party bar renders multiple times
- **Kitty graphics positioning** - images appear but in wrong positions
- The terminal pane content isn't showing properly

### Tech Stack
- **Go 1.25** 
- **Bubbletea** - TUI framework (same as lazygit)
- **Lipgloss** - styling/layout
- **Kitty Graphics Protocol** - for inline images

## File Structure

```
~/Desktop/Projects/agent-tui/
├── main.go           # Main TUI application
├── go.mod            # Go module
├── go.sum            # Dependencies
├── agent-tui         # Compiled binary
└── assets/
    ├── agent1.jpg    # Jazzy avatar (green gameboy style, glasses)
    ├── agent2.jpg    # Codex avatar (dark monochrome skull)
    ├── agent3.jpg    # Claude avatar (calico cat pixel art)
    └── agent4.jpg    # Pi avatar (green gameboy style, curly hair)
```

## Design Requirements

### Visual Style
- **Stoneshard/16-bit RPG aesthetic**
- **Transparency** - windows should be semi-transparent
- **Pixel art avatars** - the 4 JPGs in assets/
- **Ranger.fm style** - minimal, clean terminal aesthetic

### Layout (intended)
```
┌─────────────────────────────────────────────────────────────┐
│ ⚔️  AGENT FORGE                          q:quit  ←→:select │
├─────────────────────────────────────────────────────────────┤
│ ┤ Selected Agent Terminal ├                                 │
│                                                             │
│ $ codex --yolo 'Fix auth bug'                              │
│ > Reading src/auth.ts...                                    │
│ > Found issue on line 42                                    │
│                                                             │
├─────────────────────────────────────────────────────────────┤
│ ⚔  ┌──────────┐ ┌──────────┐ ┌──────────┐ ┌──────────┐    │
│ P  │ [AVATAR] │ │ [AVATAR] │ │ [AVATAR] │ │ [AVATAR] │    │
│ A  │  Jazzy   │ │  Codex   │ │  Claude  │ │    Pi    │    │
│ R  │ ████░░░░ │ │ ███░░░░░ │ │ ██████░░ │ │ ██░░░░░░ │    │
│ T  │   IDLE   │ │ WORKING  │ │ WORKING  │ │   IDLE   │    │
│ Y  └──────────┘ └──────────┘ └──────────┘ └──────────┘    │
├─────────────────────────────────────────────────────────────┤
│ Agent: Codex │ Status: WORKING │ Task: Fix auth │ Tokens: 45% │
└─────────────────────────────────────────────────────────────┘
```

### Agent Data Structure
```go
type Agent struct {
    ID          string
    Name        string
    Status      string   // idle, working, blocked, done
    TokenUsage  int      // 0-100 (shown as HP bar)
    CurrentTask string
    Output      []string // Terminal output lines
    AvatarPath  string
    AvatarData  string   // base64 encoded for kitty protocol
}
```

## Key Issues to Fix

1. **Layout rendering bug** - Party bar repeats/glitches multiple times
2. **Kitty graphics positioning** - Images show but not in the right place within cards
3. **Terminal pane** - Should show selected agent's output clearly at top

## Kitty Graphics Protocol

Current implementation sends escape sequences:
```go
// Format: \x1b_Ga=T,f=100,i=ID,c=COLS,r=ROWS,q=2,m=MORE;BASE64DATA\x1b\\
```

This is chunked for large images. The protocol works (images do appear) but positioning within the Bubbletea layout is broken.

**Reference:** https://sw.kovidgoyal.net/kitty/graphics-protocol/

## Color Palette (Stoneshard-inspired)

```go
colorBgDark     = "#1a1614"  // Darkest background
colorBgMedium   = "#2d2520"  // Medium background
colorBgLight    = "#3d342c"  // Light background
colorBorder     = "#5c4f43"  // Default border
colorBorderGold = "#c9a959"  // Selected/highlight border
colorText       = "#e8d5a3"  // Primary text (cream)
colorTextDim    = "#8a7a68"  // Dim text
colorTextBright = "#fff8e7"  // Bright text
colorGreen      = "#4a7c3f"  // Working status
colorRed        = "#a63d3d"  // Blocked/error status
colorBlue       = "#3d5a7c"  // Done status
colorYellow     = "#c9a959"  // Gold accent
```

## Terminal Setup

Using **Ghostty** terminal with custom config at `~/.config/ghostty/config`:
- Same color palette
- 92% opacity with blur
- JetBrains Mono font

## Next Steps

1. Fix the layout/rendering bug (party bar repeating)
2. Fix kitty image positioning within agent cards
3. Add real PTY connections for actual terminal output from agents
4. Add keybinds to spawn/kill agents
5. Add split panes for multiple agent terminals

## Running

```bash
cd ~/Desktop/Projects/agent-tui
go build -o agent-tui .
./agent-tui
```

Must run in a terminal that supports kitty graphics (Ghostty, Kitty, WezTerm).

## Related Files

- Ghostty config: `~/.config/ghostty/config`
- Lazygit config: `~/.config/lazygit/config.yml`
- Original web prototype (abandoned): `~/Desktop/Projects/agent-forge/`
