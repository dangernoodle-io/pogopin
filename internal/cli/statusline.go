package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

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
	colorForeign      = "1" // red
	colorUnknown      = "3" // yellow
)

// foreignWarnGlyph prefixes a foreign-session port group so it stands out
// from this session's own ports at a glance (BR-92).
const foreignWarnGlyph = "⚠"

// unknownGlyph prefixes a port group whose ownership can't be determined
// because this invocation's own sessionID never resolved — rendered
// distinctly from a confirmed foreign-session port (BR-92 follow-up).
const unknownGlyph = "?"

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
// pogopin's serial-port status as styled Segments under a single leading
// "pogopin: " label. Byte-parity with the retired renderStatusline (minus
// the BR-92 relabel/foreign-indicator additions) is maintained at
// style.LevelNone: shesha's Render concatenates Segment.Text with no
// implicit separator, so literal separators (" | ") and inter-chunk spacing
// are encoded as their own plain-text Segments.
type statuslineProvider struct{}

// Statusline reads the mode from POGOPIN_STATUSLINE_MODE and renders a
// single "pogopin: " line covering both this session's own ports and, when
// the BR-92 foreign indicator is enabled, every other live session's ports
// — split into two disjoint collapsed categories depending on whether this
// invocation's own sessionID resolved:
//   - sessionID resolved (non-empty): a port belonging to a different,
//     known session renders "⚠ <base(port)>" in red ("foreign") — we know
//     it isn't ours. A port matching sessionID renders as the original
//     "<base(port)>@<baud> <mode> <lines>L" group ("own").
//   - sessionID unresolved (empty): we can't prove ANY port is or isn't
//     ours, so nothing is flagged red and nothing claims to be "own" —
//     every port carrying a real SessionID instead renders "? <base(port)>"
//     in yellow ("unknown"), the conservative middle ground between the two.
//   - collapsed groups (foreign or unknown — never both, since they're
//     mutually exclusive per invocation) render first, then own groups;
//     all groups are joined by a literal " | " separator.
//   - nothing to render, ModeAlways: a single "pogopin: idle" segment.
//   - nothing to render, ModePortsOnly/ModeFreshOnly: nothing.
//   - foreign or unknown present: always rendered regardless of mode.
//
// A port with an empty SessionID (no session identity available) or an
// empty Port path is skipped entirely — never classified, never
// rendered — preserving the pre-BR-92 filtered-view behavior and avoiding
// false flags or a bare "⚠ ."/"? ." from filepath.Base("").
//
// POGOPIN_STATUSLINE_FOREIGN (default on; falsey values "off"/"0"/"false",
// case-insensitive, disable it) is the interim toggle for both the foreign
// and unknown indicators; when disabled, only this session's own ports are
// read/rendered (via status.ReadLivePorts, same as pre-BR-92) — an
// unresolved sessionID then renders nothing, matching ReadLivePorts's own
// "" -> empty-slice contract — but the "pogopin: " relabel still applies.
//
// Fully fail-open: status read errors are intentionally discarded (mirrors
// the retired CLI's posture) and this method never panics, so Command's
// failOpen wrapper never has anything to swallow here.
func (statuslineProvider) Statusline(_ context.Context, _ statusline.Payload, sessionID string) ([]statusline.Segment, error) {
	mode := status.ParseMode(os.Getenv("POGOPIN_STATUSLINE_MODE"))

	var foreign, own, unknown []status.PortState
	if foreignIndicatorEnabled() {
		for _, p := range status.ReadAllLivePorts(mode) {
			if p.Port == "" || p.SessionID == "" {
				continue
			}
			switch {
			case sessionID == "":
				unknown = append(unknown, p)
			case p.SessionID == sessionID:
				own = append(own, p)
			default:
				foreign = append(foreign, p)
			}
		}
	} else {
		own, _ = status.ReadLivePorts(sessionID, mode)
	}

	if len(foreign) == 0 && len(own) == 0 && len(unknown) == 0 {
		if mode == status.ModeAlways {
			return []statusline.Segment{{Text: "pogopin: idle", Color: colorIdle}}, nil
		}
		return nil, nil
	}

	segments := make([]statusline.Segment, 0, 1+len(own)*5+(len(foreign)+len(unknown))*2)
	segments = append(segments, statusline.Segment{Text: "pogopin: "})

	first := true
	for _, p := range foreign {
		if !first {
			segments = append(segments, statusline.Segment{Text: " | "})
		}
		first = false
		segments = append(segments, collapsedSegments(foreignWarnGlyph, colorForeign, p)...)
	}
	for _, p := range unknown {
		if !first {
			segments = append(segments, statusline.Segment{Text: " | "})
		}
		first = false
		segments = append(segments, collapsedSegments(unknownGlyph, colorUnknown, p)...)
	}
	for _, p := range own {
		if !first {
			segments = append(segments, statusline.Segment{Text: " | "})
		}
		first = false
		segments = append(segments, portSegments(p)...)
	}

	return segments, nil
}

// foreignIndicatorEnabled parses POGOPIN_STATUSLINE_FOREIGN; default on,
// disabled only by a case-insensitive "off"/"0"/"false" (BR-92 interim
// toggle, superseded by the BR-95 config epic).
func foreignIndicatorEnabled() bool {
	switch strings.ToLower(os.Getenv("POGOPIN_STATUSLINE_FOREIGN")) {
	case "off", "0", "false":
		return false
	default:
		return true
	}
}

// collapsedSegments renders a port whose full detail isn't this
// invocation's to show as a single glyph-prefixed segment in the given
// color — "<glyph> <base(port)>", no baud/mode/line-count detail. Shared by
// both the "foreign" (confirmed different session, red ⚠) and "unknown"
// (ownership unprovable, yellow ?) collapsed render shapes, which differ
// only in glyph and color.
func collapsedSegments(glyph, color string, p status.PortState) []statusline.Segment {
	return []statusline.Segment{
		{Text: glyph + " " + filepath.Base(p.Port), Color: color},
	}
}

// portSegments renders one own-session port's status as a group of
// Segments whose concatenated Text is "<base(port)>@<baud> <mode> <lines>L".
// The port identity (basename@baud) carries a color; the mode word carries
// a mode-specific color; the trailing buffer-line count is dimmed. Literal
// spacing between chunks is its own plain (uncolored) Segment.
func portSegments(p status.PortState) []statusline.Segment {
	return []statusline.Segment{
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
