package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/dangernoodle-io/shesha/host/claudecode/hooks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"dangernoodle.io/pogopin/internal/status"
)

const preToolUseTestToolName = "mcp__plugin_pogopin-mcp_pogopin__serial_start"

// writeStatusFileForAt writes a status file for pid directly into dir,
// bypassing status.Write so the test can control the owning pid and
// updated_at timestamp (staleness/dead-pid fixtures) — a sibling of
// writeStatusFileFor (statusline_test.go) which always stamps "now".
func writeStatusFileForAt(t *testing.T, dir string, pid int, ports []status.PortState, updatedAt time.Time) {
	t.Helper()
	sf := status.StatusFile{Ports: ports, UpdatedAt: updatedAt.Unix()}
	data, err := json.Marshal(sf)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, strconv.Itoa(pid)+".json"), data, 0644))
}

func toolInputJSON(t *testing.T, port string) json.RawMessage {
	t.Helper()
	if port == "" {
		return json.RawMessage(`{}`)
	}
	data, err := json.Marshal(map[string]string{"port": port})
	require.NoError(t, err)
	return data
}

func TestHookHandlePreToolUse_NoPort(t *testing.T) {
	prev := status.SetStatusDir(t.TempDir())
	defer status.SetStatusDir(prev)

	p := hooks.PreToolUsePayload{
		Common:    hooks.Common{SessionID: "sess-a"},
		ToolName:  preToolUseTestToolName,
		ToolInput: toolInputJSON(t, ""),
	}
	resp := hookHandlePreToolUse(context.Background(), nil, p)
	assert.Equal(t, hooks.Response{}, resp)
}

func TestHookHandlePreToolUse_MissingStatusDir(t *testing.T) {
	prev := status.SetStatusDir(filepath.Join(t.TempDir(), "does-not-exist"))
	defer status.SetStatusDir(prev)

	p := hooks.PreToolUsePayload{
		Common:    hooks.Common{SessionID: "sess-a"},
		ToolName:  preToolUseTestToolName,
		ToolInput: toolInputJSON(t, "/dev/ttyUSB0"),
	}
	resp := hookHandlePreToolUse(context.Background(), nil, p)
	assert.Equal(t, hooks.Response{}, resp)
}

func TestHookHandlePreToolUse_SameSession_NoOverride(t *testing.T) {
	dir := t.TempDir()
	prev := status.SetStatusDir(dir)
	defer status.SetStatusDir(prev)

	writeStatusFileForAt(t, dir, os.Getpid(), []status.PortState{
		{Port: "/dev/ttyUSB0", Running: true, PID: os.Getpid(), SessionID: "sess-a"},
	}, time.Now())

	p := hooks.PreToolUsePayload{
		Common:    hooks.Common{SessionID: "sess-a"}, // caller's own session, no POGOPIN_SESSION_ID
		ToolName:  preToolUseTestToolName,
		ToolInput: toolInputJSON(t, "/dev/ttyUSB0"),
	}
	resp := hookHandlePreToolUse(context.Background(), nil, p)
	assert.Equal(t, hooks.Response{}, resp, "own session's port must never be flagged")
}

// TestHookHandlePreToolUse_SameSession_WithOverride is the BR-87 regression
// test: a POGOPIN_SESSION_ID-launched server stamps its status entries with
// the override, but the raw stdin session_id it invokes its OWN PreToolUse
// hook with is a different (Claude Code-assigned) value. The retired JS hook
// compared entry.session_id against that raw stdin id and flagged this as a
// false cross-session conflict; the fix must not.
func TestHookHandlePreToolUse_SameSession_WithOverride(t *testing.T) {
	dir := t.TempDir()
	prev := status.SetStatusDir(dir)
	defer status.SetStatusDir(prev)
	t.Setenv("POGOPIN_SESSION_ID", "override-sess")

	writeStatusFileForAt(t, dir, os.Getpid(), []status.PortState{
		{Port: "/dev/ttyUSB0", Running: true, PID: os.Getpid(), SessionID: "override-sess"},
	}, time.Now())

	p := hooks.PreToolUsePayload{
		Common:    hooks.Common{SessionID: "raw-stdin-sess"}, // differs from override-sess
		ToolName:  preToolUseTestToolName,
		ToolInput: toolInputJSON(t, "/dev/ttyUSB0"),
	}
	resp := hookHandlePreToolUse(context.Background(), nil, p)
	assert.Equal(t, hooks.Response{}, resp, "BR-87: POGOPIN_SESSION_ID-launched server's own port must not be flagged")
}

func TestHookHandlePreToolUse_CrossSessionLive(t *testing.T) {
	dir := t.TempDir()
	prev := status.SetStatusDir(dir)
	defer status.SetStatusDir(prev)

	writeStatusFileForAt(t, dir, os.Getpid(), []status.PortState{
		{Port: "/dev/ttyUSB0", Mode: "reader", Running: true, PID: os.Getpid(), SessionID: "sess-b"},
	}, time.Now())

	p := hooks.PreToolUsePayload{
		Common:    hooks.Common{SessionID: "sess-a"},
		ToolName:  preToolUseTestToolName,
		ToolInput: toolInputJSON(t, "/dev/ttyUSB0"),
	}
	resp := hookHandlePreToolUse(context.Background(), nil, p)
	require.NotEqual(t, "", resp.AdditionalContext)
	assert.Equal(t, resp.AdditionalContext, resp.SystemMessage)
	assert.Contains(t, resp.AdditionalContext, "/dev/ttyUSB0")
	assert.Contains(t, resp.AdditionalContext, "serial_start")
}

func TestHookHandlePreToolUse_StaleEntry(t *testing.T) {
	dir := t.TempDir()
	prev := status.SetStatusDir(dir)
	defer status.SetStatusDir(prev)

	writeStatusFileForAt(t, dir, os.Getpid(), []status.PortState{
		{Port: "/dev/ttyUSB0", Running: true, PID: os.Getpid(), SessionID: "sess-b"},
	}, time.Now().Add(-time.Hour))

	p := hooks.PreToolUsePayload{
		Common:    hooks.Common{SessionID: "sess-a"},
		ToolName:  preToolUseTestToolName,
		ToolInput: toolInputJSON(t, "/dev/ttyUSB0"),
	}
	resp := hookHandlePreToolUse(context.Background(), nil, p)
	assert.Equal(t, hooks.Response{}, resp)
}

func TestHookHandlePreToolUse_DeadPID(t *testing.T) {
	dir := t.TempDir()
	prev := status.SetStatusDir(dir)
	defer status.SetStatusDir(prev)

	writeStatusFileForAt(t, dir, 999999, []status.PortState{
		{Port: "/dev/ttyUSB0", Running: true, PID: 999999, SessionID: "sess-b"},
	}, time.Now())

	p := hooks.PreToolUsePayload{
		Common:    hooks.Common{SessionID: "sess-a"},
		ToolName:  preToolUseTestToolName,
		ToolInput: toolInputJSON(t, "/dev/ttyUSB0"),
	}
	resp := hookHandlePreToolUse(context.Background(), nil, p)
	assert.Equal(t, hooks.Response{}, resp)
}

func TestHookHandlePreToolUse_NoSessionIDOnEntry(t *testing.T) {
	dir := t.TempDir()
	prev := status.SetStatusDir(dir)
	defer status.SetStatusDir(prev)

	writeStatusFileForAt(t, dir, os.Getpid(), []status.PortState{
		{Port: "/dev/ttyUSB0", Running: true, PID: os.Getpid()}, // no SessionID
	}, time.Now())

	p := hooks.PreToolUsePayload{
		Common:    hooks.Common{SessionID: "sess-a"},
		ToolName:  preToolUseTestToolName,
		ToolInput: toolInputJSON(t, "/dev/ttyUSB0"),
	}
	resp := hookHandlePreToolUse(context.Background(), nil, p)
	assert.Equal(t, hooks.Response{}, resp)
}

func TestHookHandlePreToolUse_NonPortTool(t *testing.T) {
	dir := t.TempDir()
	prev := status.SetStatusDir(dir)
	defer status.SetStatusDir(prev)

	writeStatusFileForAt(t, dir, os.Getpid(), []status.PortState{
		{Port: "/dev/ttyUSB0", Running: true, PID: os.Getpid(), SessionID: "sess-b"},
	}, time.Now())

	p := hooks.PreToolUsePayload{
		Common:    hooks.Common{SessionID: "sess-a"},
		ToolName:  "Bash",
		ToolInput: toolInputJSON(t, "/dev/ttyUSB0"),
	}
	resp := hookHandlePreToolUse(context.Background(), nil, p)
	assert.Equal(t, hooks.Response{}, resp)
}

// TestHookHandlePreToolUse_EmptyToolName locks in the fix for the empty-
// ToolName guard bypass: an empty ToolName must return the zero Response
// via the early-return guard, even when a foreign-session live port entry
// exists that would otherwise match on port.
func TestHookHandlePreToolUse_EmptyToolName(t *testing.T) {
	dir := t.TempDir()
	prev := status.SetStatusDir(dir)
	defer status.SetStatusDir(prev)

	writeStatusFileForAt(t, dir, os.Getpid(), []status.PortState{
		{Port: "/dev/ttyUSB0", Running: true, PID: os.Getpid(), SessionID: "sess-b"},
	}, time.Now())

	p := hooks.PreToolUsePayload{
		Common:    hooks.Common{SessionID: "sess-a"},
		ToolName:  "",
		ToolInput: toolInputJSON(t, "/dev/ttyUSB0"),
	}
	resp := hookHandlePreToolUse(context.Background(), nil, p)
	assert.Equal(t, hooks.Response{}, resp)
}

// TestFindPortConflict_SkipsNonMatchingPortBeforeMatch exercises the loop's
// non-matching-port continue: the first entry's port doesn't match the
// requested port and must be skipped, while a later entry that does match a
// foreign session still surfaces the conflict.
func TestFindPortConflict_SkipsNonMatchingPortBeforeMatch(t *testing.T) {
	ports := []status.PortState{
		{Port: "/dev/ttyUSB1", Running: true, PID: os.Getpid(), SessionID: "sess-b"},
		{Port: "/dev/ttyUSB0", Running: true, PID: os.Getpid(), SessionID: "sess-b"},
	}
	entry, ok := findPortConflict(ports, "/dev/ttyUSB0", "sess-a")
	require.True(t, ok)
	assert.Equal(t, "/dev/ttyUSB0", entry.Port)
	assert.Equal(t, "sess-b", entry.SessionID)
}

func TestResolveConsumerSessionID(t *testing.T) {
	t.Run("override wins", func(t *testing.T) {
		t.Setenv("POGOPIN_SESSION_ID", "override-sess")
		assert.Equal(t, "override-sess", ResolveConsumerSessionID("payload-sess"))
	})
	t.Run("falls back to payload", func(t *testing.T) {
		assert.Equal(t, "payload-sess", ResolveConsumerSessionID("payload-sess"))
	})
}
