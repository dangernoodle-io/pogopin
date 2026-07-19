package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/dangernoodle-io/shesha/host/claudecode/statusline"
	"github.com/dangernoodle-io/shesha/style"

	"dangernoodle.io/pogopin/internal/status"
)

// ANSI color codes (termenv-style "0"-"255" strings, per statusline.Segment's
// Color contract) used to distinguish the port-identity chunk from the mode
// word in colored output. Purely cosmetic — LevelNone (--plain, NO_COLOR, a
// non-TTY pipe) drops these entirely and renders bare text, so they carry no
// behavioral weight.
const (
	colorPortIdentity = "6" // cyan
	colorModeReader   = "2" // green
	colorModeFlasher  = "3" // yellow
	colorModeExternal = "5" // magenta
	colorIdle         = "3" // yellow
)

// statuslineOpts is the Option set the production `pogo statusline` command
// is built with — extracted to a shared var so anything that needs to
// exercise the exact production wiring (e.g. the color/--plain tests in
// statusline_test.go) sources it from here rather than re-deriving its own
// copy, which would silently drift if this set ever changes.
//
// WithAppPrefix("POGOPIN") gives Resolve the same precedence pogopin's
// retired bespoke resolver used: POGOPIN_SESSION_ID > stdin session_id >
// CLAUDE_CODE_SESSION_ID.
//
// WithForceLevel(style.LevelBasic) is required for color to ever reach the
// real Claude Code status bar: Claude Code always pipes statusline stdout to
// a non-TTY, so shesha's default style.Detect(w) would resolve LevelNone and
// every colored Segment would silently degrade to plain text in production
// (mirrors ouroboros's MC-55/56 fix for the identical problem). Per shesha's
// cmd.go, the resolution order is: --plain wins outright and forces
// LevelNone regardless of this option; otherwise the forced level here
// (LevelBasic, 16-color ANSI) applies; only when neither is set does
// style.Detect(w) run. So `pogo statusline` now renders color, while
// `pogo statusline --plain` still renders byte-identical plain output.
var statuslineOpts = []statusline.Option{
	statusline.WithAppPrefix("POGOPIN"),
	statusline.WithForceLevel(style.LevelBasic),
}

// statuslineCmd is the `pogo statusline` leaf, built on shesha's
// host/claudecode/statusline seam (Command/StatuslineProvider). It preserves
// the exact top-level invocation name (`pogo statusline`) the plugin's
// settings.json statusLine.command depends on.
var statuslineCmd = statusline.Command(statuslineProvider{}, statuslineOpts...)

// statuslineProvider implements statusline.StatuslineProvider, rendering
// pogopin's serial-port status as styled Segments. Byte-parity with the
// retired renderStatusline is maintained at style.LevelNone: shesha's Render
// concatenates Segment.Text with no implicit separator, so literal
// separators (" | ") and the "serial: " / " " spacing are encoded as their
// own plain-text Segments — the same bytes fmt.Sprintf produced before.
type statuslineProvider struct{}

// Statusline reads the mode from POGOPIN_STATUSLINE_MODE and the live,
// session-filtered port view via status.ReadLivePorts, then renders:
//   - ports present: one "serial: <base(port)>@<baud> <mode> <lines>L"
//     segment group per port, joined by a literal " | " separator segment.
//   - no ports + ModeAlways: a single "serial: idle" segment.
//   - no ports + ModePortsOnly/ModeFreshOnly: no segments (renders nothing).
//
// Fully fail-open: status.ReadLivePorts's error is intentionally discarded
// (mirrors the retired CLI's posture) and this method never panics, so
// Command's failOpen wrapper never has anything to swallow here.
func (statuslineProvider) Statusline(_ context.Context, _ statusline.Payload, sessionID string) ([]statusline.Segment, error) {
	mode := status.ParseMode(os.Getenv("POGOPIN_STATUSLINE_MODE"))
	ports, _ := status.ReadLivePorts(sessionID, mode)

	if len(ports) == 0 {
		if mode == status.ModeAlways {
			return []statusline.Segment{{Text: "serial: idle", Color: colorIdle}}, nil
		}
		return nil, nil
	}

	segments := make([]statusline.Segment, 0, len(ports)*6)
	for i, p := range ports {
		if i > 0 {
			segments = append(segments, statusline.Segment{Text: " | "})
		}
		segments = append(segments, portSegments(p)...)
	}

	return segments, nil
}

// portSegments renders one port's status as a group of Segments whose
// concatenated Text is byte-identical to the retired
// fmt.Sprintf("serial: %s@%d %s %dL", ...) format. The port identity
// (basename@baud) carries a color; the mode word carries a mode-specific
// color; the trailing buffer-line count is dimmed. Literal spacing between
// chunks is its own plain (uncolored) Segment.
func portSegments(p status.PortState) []statusline.Segment {
	return []statusline.Segment{
		{Text: "serial: "},
		{Text: fmt.Sprintf("%s@%d", filepath.Base(p.Port), p.Baud), Color: colorPortIdentity, Bold: true},
		{Text: " "},
		{Text: p.Mode, Color: modeColor(p.Mode)},
		{Text: " "},
		{Text: fmt.Sprintf("%dL", p.BufferLines), Dim: true},
	}
}

// modeColor maps a PortState.Mode string to a color; unknown modes (e.g.
// "pending") render uncolored.
func modeColor(mode string) string {
	switch mode {
	case "reader":
		return colorModeReader
	case "flasher":
		return colorModeFlasher
	case "external":
		return colorModeExternal
	default:
		return ""
	}
}
