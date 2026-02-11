package main

import tea "github.com/charmbracelet/bubbletea"

// keyToBytes converts a bubbletea key message to raw bytes for PTY forwarding.
// This must cover ALL key types that bubbletea can parse, otherwise keypresses
// get silently dropped in insert mode.
func keyToBytes(msg tea.KeyMsg) []byte {
	switch msg.Type {

	// ── Basic ─────────────────────────────────────────────────────
	case tea.KeyEnter:
		return []byte{'\r'}
	case tea.KeyTab:
		return []byte{'\t'}
	case tea.KeyBackspace:
		return []byte{127}
	case tea.KeySpace:
		return []byte{' '}
	case tea.KeyEscape:
		return []byte{0x1b}

	// ── Arrow Keys ────────────────────────────────────────────────
	case tea.KeyUp:
		return []byte("\x1b[A")
	case tea.KeyDown:
		return []byte("\x1b[B")
	case tea.KeyRight:
		return []byte("\x1b[C")
	case tea.KeyLeft:
		return []byte("\x1b[D")

	// ── Shift+Arrow ───────────────────────────────────────────────
	case tea.KeyShiftTab:
		return []byte("\x1b[Z")
	case tea.KeyShiftUp:
		return []byte("\x1b[1;2A")
	case tea.KeyShiftDown:
		return []byte("\x1b[1;2B")
	case tea.KeyShiftRight:
		return []byte("\x1b[1;2C")
	case tea.KeyShiftLeft:
		return []byte("\x1b[1;2D")

	// ── Ctrl+Arrow ────────────────────────────────────────────────
	case tea.KeyCtrlUp:
		return []byte("\x1b[1;5A")
	case tea.KeyCtrlDown:
		return []byte("\x1b[1;5B")
	case tea.KeyCtrlRight:
		return []byte("\x1b[1;5C")
	case tea.KeyCtrlLeft:
		return []byte("\x1b[1;5D")

	// ── Ctrl+Shift+Arrow ─────────────────────────────────────────
	case tea.KeyCtrlShiftUp:
		return []byte("\x1b[1;6A")
	case tea.KeyCtrlShiftDown:
		return []byte("\x1b[1;6B")
	case tea.KeyCtrlShiftRight:
		return []byte("\x1b[1;6C")
	case tea.KeyCtrlShiftLeft:
		return []byte("\x1b[1;6D")

	// ── Navigation ────────────────────────────────────────────────
	case tea.KeyHome:
		return []byte("\x1b[H")
	case tea.KeyEnd:
		return []byte("\x1b[F")
	case tea.KeyInsert:
		return []byte("\x1b[2~")
	case tea.KeyDelete:
		return []byte("\x1b[3~")
	case tea.KeyPgUp:
		return []byte("\x1b[5~")
	case tea.KeyPgDown:
		return []byte("\x1b[6~")

	// ── Shift+Navigation ─────────────────────────────────────────
	case tea.KeyShiftHome:
		return []byte("\x1b[1;2H")
	case tea.KeyShiftEnd:
		return []byte("\x1b[1;2F")

	// ── Ctrl+Navigation ──────────────────────────────────────────
	case tea.KeyCtrlHome:
		return []byte("\x1b[1;5H")
	case tea.KeyCtrlEnd:
		return []byte("\x1b[1;5F")
	case tea.KeyCtrlPgUp:
		return []byte("\x1b[5;5~")
	case tea.KeyCtrlPgDown:
		return []byte("\x1b[6;5~")

	// ── Ctrl+Shift+Navigation ────────────────────────────────────
	case tea.KeyCtrlShiftHome:
		return []byte("\x1b[1;6H")
	case tea.KeyCtrlShiftEnd:
		return []byte("\x1b[1;6F")

	// ── Function Keys ─────────────────────────────────────────────
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
	case tea.KeyF13:
		return []byte("\x1b[25~")
	case tea.KeyF14:
		return []byte("\x1b[26~")
	case tea.KeyF15:
		return []byte("\x1b[28~")
	case tea.KeyF16:
		return []byte("\x1b[29~")
	case tea.KeyF17:
		return []byte("\x1b[31~")
	case tea.KeyF18:
		return []byte("\x1b[32~")
	case tea.KeyF19:
		return []byte("\x1b[33~")
	case tea.KeyF20:
		return []byte("\x1b[34~")

	// ── Ctrl+Letter ───────────────────────────────────────────────
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
	// KeyCtrlI = Tab (0x09), handled above
	// KeyCtrlJ = LF (0x0a)
	case tea.KeyCtrlK:
		return []byte{0x0b}
	case tea.KeyCtrlL:
		return []byte{0x0c}
	// KeyCtrlM = CR (0x0d), handled by KeyEnter
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

	// ── Ctrl+Symbol ───────────────────────────────────────────────
	case tea.KeyCtrlAt:
		return []byte{0x00}
	// KeyCtrlOpenBracket (27) == KeyEscape, handled above
	case tea.KeyCtrlBackslash:
		return []byte{0x1c}
	case tea.KeyCtrlCloseBracket:
		return []byte{0x1d}
	case tea.KeyCtrlCaret:
		return []byte{0x1e}
	case tea.KeyCtrlUnderscore:
		return []byte{0x1f}
	// KeyCtrlQuestionMark (127) == KeyBackspace, handled above

	// ── Regular Characters ────────────────────────────────────────
	case tea.KeyRunes:
		return []byte(string(msg.Runes))
	}

	return nil
}
