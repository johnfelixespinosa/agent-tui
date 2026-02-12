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
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// AvatarReadyMsg is sent when a single agent's avatar finishes loading async.
type AvatarReadyMsg struct {
	AgentName string
	Image     image.Image
	KittyB64  string
}

// loadAvatarsAsync returns a tea.Cmd that loads all agent avatars in parallel.
func loadAvatarsAsync(agents []AgentConfig) tea.Cmd {
	var cmds []tea.Cmd
	for _, a := range agents {
		agentName := a.Name
		tint := color.RGBA{a.Tint[0], a.Tint[1], a.Tint[2], 255}
		cmds = append(cmds, func() tea.Msg {
			img := loadAgentAvatar(strings.ToLower(agentName))
			if img == nil {
				return AvatarReadyMsg{AgentName: agentName}
			}
			tinted := tintImage(img, tint)
			b64 := encodeKittyAvatarDirect(tinted)
			return AvatarReadyMsg{
				AgentName: agentName,
				Image:     tinted,
				KittyB64:  b64,
			}
		})
	}
	if len(cmds) == 0 {
		return nil
	}
	return tea.Batch(cmds...)
}

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
			gray := (r*299 + g*587 + b*114) / 1000
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

// loadAgentAvatar tries to load a per-agent avatar from assets/<name>.png or .jpg.
func loadAgentAvatar(name string) image.Image {
	lower := strings.ToLower(name)
	for _, ext := range []string{".png", ".jpg"} {
		path := filepath.Join("assets", lower+ext)
		f, err := os.Open(path)
		if err != nil {
			continue
		}
		img, _, err := image.Decode(f)
		f.Close()
		if err != nil {
			continue
		}
		return img
	}
	return nil
}

// encodeKittyAvatarDirect encodes an image as base64 PNG for Kitty protocol
// without any tinting — used for custom per-agent avatars.
func encodeKittyAvatarDirect(img image.Image) string {
	if img == nil {
		return ""
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return ""
	}
	return base64.StdEncoding.EncodeToString(buf.Bytes())
}

// kittyImageSeq returns a Kitty graphics escape sequence that transmits and
// displays a base64-encoded PNG image spanning cols×rows character cells.
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

// halfBlockAvatar returns a cached half-block render, recomputing only when dimensions change.
func (inst *AgentInstance) halfBlockAvatar(cols, rows int) string {
	if cols == inst.cachedHalfBlockCols && rows == inst.cachedHalfBlockRows && inst.cachedHalfBlock != "" {
		return inst.cachedHalfBlock
	}
	result := renderHalfBlockAvatar(inst.avatarImg, cols, rows)
	inst.cachedHalfBlock = result
	inst.cachedHalfBlockCols = cols
	inst.cachedHalfBlockRows = rows
	return result
}

// renderHalfBlockAvatar renders an image as colored half-block characters.
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
