package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"dangernoodle.io/pogopin/internal/status"
)

var statuslineCmd = &cobra.Command{
	Use:   "statusline",
	Short: "Output the serial-monitor status line for the current session",
	Long: "Output the serial-monitor status line for the current Claude Code session, " +
		"reading the Claude statusline stdin contract and the pogo status cache. " +
		"Replaces plugin/scripts/statusline.js.",
	RunE: runStatusline,
}

// statuslineInput is the subset of the Claude Code statusline stdin contract
// this command consumes. cwd is parsed for forward-compatibility but
// currently unused.
type statuslineInput struct {
	SessionID string `json:"session_id"`
	Cwd       string `json:"cwd"`
}

// runStatusline renders the serial-monitor status line, byte-parity with the
// retired plugin/scripts/statusline.js. It is fully fail-open: parse errors,
// missing status data, and read errors all result in rendering nothing (or
// "serial: idle" in ModeAlways) rather than a non-nil error — a non-nil
// return here would print a cobra usage line, which is never appropriate for
// a statusline widget. Always returns nil.
func runStatusline(cmd *cobra.Command, args []string) error {
	in := parseStatuslineStdin(cmd.InOrStdin())

	sessionID := resolveConsumerSessionID(in)
	mode := status.ParseMode(os.Getenv("POGOPIN_STATUSLINE_MODE"))

	ports, _ := status.ReadLivePorts(sessionID, mode)

	renderStatusline(cmd.OutOrStdout(), ports, mode)
	return nil
}

// resolveConsumerSessionID resolves the session identity a statusline
// invocation should filter its port view by: POGOPIN_SESSION_ID (host-
// agnostic override) > the statusline stdin contract's session_id > the
// CLAUDE_CODE_SESSION_ID env var > "" (renders nothing). Mirrors the
// producer-side resolver in internal/session/session.go but adds the stdin
// source, since this is a one-shot invocation per statusline render rather
// than a long-running server process.
func resolveConsumerSessionID(in statuslineInput) string {
	if v := os.Getenv("POGOPIN_SESSION_ID"); v != "" {
		return v
	}
	if in.SessionID != "" {
		return in.SessionID
	}
	return os.Getenv("CLAUDE_CODE_SESSION_ID")
}

// parseStatuslineStdin reads and parses the Claude statusline stdin
// contract from r. Fail-open: empty stdin or unparseable JSON both yield a
// zero-value statuslineInput rather than an error.
func parseStatuslineStdin(r io.Reader) statuslineInput {
	var in statuslineInput

	data, err := io.ReadAll(r)
	if err != nil || len(data) == 0 {
		return in
	}

	if err := json.Unmarshal(data, &in); err != nil {
		return statuslineInput{}
	}

	return in
}

// renderStatusline writes the rendered status line to w, byte-parity with
// statusline.js:38-50:
//   - ports present: one "serial: <base(port)>@<baud> <mode> <lines>L"
//     segment per port, joined by " | ", newline-terminated.
//   - no ports + ModeAlways: "serial: idle".
//   - no ports + ModePortsOnly/ModeFreshOnly: nothing (no output at all).
func renderStatusline(w io.Writer, ports []status.PortState, mode status.Mode) {
	if len(ports) == 0 {
		if mode == status.ModeAlways {
			_, _ = fmt.Fprintln(w, "serial: idle")
		}
		return
	}

	segments := make([]string, len(ports))
	for i, p := range ports {
		segments[i] = fmt.Sprintf("serial: %s@%d %s %dL", filepath.Base(p.Port), p.Baud, p.Mode, p.BufferLines)
	}

	_, _ = fmt.Fprintln(w, strings.Join(segments, " | "))
}
