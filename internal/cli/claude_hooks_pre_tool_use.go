package cli

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"strings"

	"github.com/dangernoodle-io/shesha/host/claudecode/hooks"

	"dangernoodle.io/pogopin/internal/status"
)

// portToolPrefix is the MCP tool-name prefix pogopin's port-using tools all
// share — mirrors hooks.json's PreToolUse matcher
// (mcp__plugin_pogopin-mcp_pogopin__.*).
const portToolPrefix = "mcp__plugin_pogopin-mcp_pogopin__"

// preToolUseToolInput is the subset of PreToolUsePayload.ToolInput this hook
// reads: every port-using pogopin tool accepts a top-level string "port"
// argument.
type preToolUseToolInput struct {
	Port string `json:"port"`
}

// ResolveConsumerSessionID resolves the session identity a hook handler
// should compare a status.PortState.SessionID against, given the raw
// session_id off a hook's stdin payload. POGOPIN_SESSION_ID (a
// host-agnostic override) takes precedence over the payload's own
// session_id — the SAME precedence resolveProducerSessionID uses on the
// server-writing side (internal/session/session.go). This symmetry is the
// BR-87 fix: the retired JS hook compared entry.session_id against the raw
// stdin session_id with no POGOPIN_SESSION_ID override, so a
// POGOPIN_SESSION_ID-launched server's own ports looked foreign to its own
// PreToolUse hook invocation and were flagged as a false cross-session
// conflict.
func ResolveConsumerSessionID(payloadSessionID string) string {
	if v := os.Getenv("POGOPIN_SESSION_ID"); v != "" {
		return v
	}
	return payloadSessionID
}

// hookHandlePreToolUse is the PreToolUse-hook cross-session port-conflict
// warning, a native port of plugin/scripts/pre-tool-port-check.js (BR-31),
// fixed for BR-87 (see ResolveConsumerSessionID). Warn-only: never blocks,
// only ever returns AdditionalContext/SystemMessage or the zero Response.
// Fully fail-open — any decode error or missing data yields the zero
// Response, never a panic.
func hookHandlePreToolUse(_ context.Context, _ io.Reader, p hooks.PreToolUsePayload) hooks.Response {
	// Defensive re-check: hooks.json's matcher already restricts invocation
	// to pogopin MCP tools, but a ToolName that isn't one (e.g. a future
	// looser matcher) should never be treated as a port-conflict candidate.
	if !strings.HasPrefix(p.ToolName, portToolPrefix) {
		return hooks.Response{}
	}

	var input preToolUseToolInput
	if err := json.Unmarshal(p.ToolInput, &input); err != nil || input.Port == "" {
		return hooks.Response{}
	}

	entry, ok := findPortConflict(status.ReadAllLivePorts(status.ModeAlways), input.Port, ResolveConsumerSessionID(p.SessionID))
	if !ok {
		return hooks.Response{}
	}

	text := buildPortConflictWarning(p.ToolName, entry)
	return hooks.Response{AdditionalContext: text, SystemMessage: text}
}

// findPortConflict returns the busy PortState entry for port iff it
// represents a genuine cross-session conflict, or (zero, false) otherwise —
// free/absent/same-session/no-session-identity all fail open, no warning.
// Faithful port of pre-tool-port-check.js's findConflict, with
// callerSessionID resolved via ResolveConsumerSessionID rather than the raw
// payload session_id (the BR-87 fix). The JS original's separate
// "owning process dead" check is not repeated here: ports comes from
// status.ReadAllLivePorts, which already drops every port belonging to a
// dead-PID (or stale) status file before this function ever sees it — a
// stale/dead-owner entry simply isn't present, so it never matches below.
func findPortConflict(ports []status.PortState, port, callerSessionID string) (status.PortState, bool) {
	for _, entry := range ports {
		if entry.Port != port || !entry.Running {
			continue
		}

		// No session identity recorded (older server / session id unset):
		// can't tell same- from cross-session, so don't warn.
		if entry.SessionID == "" {
			return status.PortState{}, false
		}

		// Same session owns this port (BR-87: resolved via
		// ResolveConsumerSessionID, not the raw payload session_id) — the
		// normal same-session flow. Silent.
		if entry.SessionID == callerSessionID {
			return status.PortState{}, false
		}

		return entry, true
	}
	return status.PortState{}, false
}

// buildPortConflictWarning renders the same warning text into both
// AdditionalContext and SystemMessage, mirroring
// pre-tool-port-check.js's buildWarning (systemMessage +
// hookSpecificOutput.additionalContext both carrying identical text).
func buildPortConflictWarning(toolName string, entry status.PortState) string {
	name := strings.TrimPrefix(toolName, portToolPrefix)
	return "pogopin: port " + entry.Port + " is in use by another Claude Code session " +
		"(mode=" + entry.Mode + ", running=true) — " +
		name + " may conflict with that session"
}
