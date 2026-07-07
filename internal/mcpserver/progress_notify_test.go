package mcpserver

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"dangernoodle.io/pogopin/internal/esp"
	"dangernoodle.io/pogopin/internal/session"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	espflasher "tinygo.org/x/espflasher/pkg/espflasher"
)

// This file closes the gap left by progress_test.go: that file injects the
// `send` func directly into newProgressEmitter, so the real emission path —
// handleFlash/handleErase -> sendProgress(ctx, token) ->
// server.ServerFromContext(ctx) -> SendNotificationToClient(ctx,
// "notifications/progress", ...) -- is never exercised. These tests drive
// that real path end to end.
//
// mcp-go v0.48.0's own client.NewInProcessClient/transport.InProcessTransport
// stores an OnNotification handler (see
// client/transport/inprocess.go:onNotification) but never reads from the
// registered session's NotificationChannel to invoke it — there is no pump.
// So the in-process client cannot observe server->client notifications at
// all in this version; option (a) from the task brief is not viable.
//
// Instead we drive requests through (*server.MCPServer).HandleMessage —
// the same exported entry point every transport (stdio/sse/streamable-http)
// calls — with a context that carries a real ClientSession via
// srv.WithContext. HandleMessage itself attaches the *MCPServer to the
// context (via its internal serverKey), so server.ServerFromContext(ctx)
// resolves for real inside sendProgress, and
// srv.SendNotificationToClient(ctx, ...) resolves our session via
// server.ClientSessionFromContext(ctx) and pushes onto its
// NotificationChannel. This is a full round trip through real JSON-RPC
// dispatch, real tool registration (registerHardwareTools), and the real
// handleFlash/handleErase handlers -- no shortcuts.
//
// No production seam was needed: the flasher factory injection point
// (session.SetFlasherFactory) already existed for esp_handlers_test.go.

// notifyCapture is a minimal server.ClientSession whose NotificationChannel
// is a channel the test can read back from directly (server.ClientSession's
// own NotificationChannel() method is send-only by design, so a stand-in
// like server.InProcessSession cannot be drained from outside the server
// package).
type notifyCapture struct {
	id string
	ch chan mcp.JSONRPCNotification
}

func newNotifyCapture(id string) *notifyCapture {
	return &notifyCapture{id: id, ch: make(chan mcp.JSONRPCNotification, 256)}
}

func (c *notifyCapture) Initialize()                                         {}
func (c *notifyCapture) Initialized() bool                                   { return true }
func (c *notifyCapture) NotificationChannel() chan<- mcp.JSONRPCNotification { return c.ch }
func (c *notifyCapture) SessionID() string                                   { return c.id }

var _ server.ClientSession = (*notifyCapture)(nil)

// progressFrames drains every notifications/progress frame currently
// available on ch. Since handleFlash/handleErase run synchronously inside
// HandleMessage, every send has already happened by the time HandleMessage
// returns, so the non-blocking `default` case below returns on its first
// pass through an empty channel — this is the common (and only observed)
// path. The 50ms deadline is a defensive upper bound only, in case that
// synchronous-delivery assumption ever changes (e.g. an async sender);
// it is not expected to ever fire.
func progressFrames(t *testing.T, ch chan mcp.JSONRPCNotification) []map[string]any {
	t.Helper()
	var frames []map[string]any
	deadline := time.After(50 * time.Millisecond)
	for {
		select {
		case n := <-ch:
			if n.Method == "notifications/progress" {
				frames = append(frames, n.Params.AdditionalFields)
			}
		case <-deadline:
			return frames
		default:
			// Nothing buffered right now. handleFlash/handleErase run
			// synchronously inside HandleMessage, so every send that will
			// ever happen has already happened by the time we get here —
			// safe to stop. The deadline case above is a defensive bound
			// in case that ever changes (e.g. an async sender).
			if len(ch) == 0 {
				return frames
			}
		}
	}
}

// toolCallMessage builds a raw JSON-RPC tools/call request, optionally
// carrying _meta.progressToken, for HandleMessage.
func toolCallMessage(t *testing.T, name string, args map[string]any, progressToken any) json.RawMessage {
	t.Helper()
	params := map[string]any{
		"name":      name,
		"arguments": args,
	}
	if progressToken != nil {
		params["_meta"] = map[string]any{"progressToken": progressToken}
	}
	raw, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params":  params,
	})
	require.NoError(t, err)
	return raw
}

// requireToolCallOK fails the test if the JSON-RPC response is a
// protocol-level error or the tool itself reported isError.
func requireToolCallOK(t *testing.T, msg mcp.JSONRPCMessage) {
	t.Helper()
	if rpcErr, ok := msg.(mcp.JSONRPCError); ok {
		t.Fatalf("unexpected JSON-RPC error: %+v", rpcErr.Error)
	}
	raw, err := json.Marshal(msg)
	require.NoError(t, err)
	var resp struct {
		Result mcp.CallToolResult `json:"result"`
	}
	require.NoError(t, json.Unmarshal(raw, &resp))
	require.False(t, resp.Result.IsError, "tool call reported isError")
}

// newProgressTestServer sets up an MCPServer with the full hardware tier
// registered, matching how Serve() wires things in production.
func newProgressTestServer(t *testing.T) *server.MCPServer {
	t.Helper()
	s := newTestServer(t)
	registerTools(s)
	registerHardwareTools(s)
	return s
}

func TestHandleFlashEmitsRealProgressNotifications(t *testing.T) {
	setupTestPorts(t)
	setupTestFlasherFactory(t)

	tmpDir := t.TempDir()
	fw := tmpDir + "/firmware.bin"
	require.NoError(t, os.WriteFile(fw, []byte("firmware data"), 0644))

	flasher := &mockFlasher{
		flashImagesProgress: func(progress espflasher.ProgressFunc) {
			// Simulate a chunked byte-progress sequence. Several calls
			// land on the same integer percent and must be throttled away
			// by newProgressEmitter; only the transitions should reach
			// the client.
			progress(0, 1000)    // 0%
			progress(10, 1000)   // 1%
			progress(15, 1000)   // still 1% -> dropped
			progress(500, 1000)  // 50%
			progress(500, 1000)  // 50% again -> dropped
			progress(999, 1000)  // 99%
			progress(1000, 1000) // 100% completion, always emitted
		},
	}
	session.SetFlasherFactory(func(port string, opts *espflasher.FlasherOptions) (esp.Flasher, error) {
		return flasher, nil
	})
	t.Cleanup(func() { session.SetFlasherFactory(esp.DefaultFlasherFactory) })

	s := newProgressTestServer(t)
	sess := newNotifyCapture("flash-progress")
	ctx := s.WithContext(context.Background(), sess)

	args := map[string]any{
		// Use tmpDir (a real path) rather than a literal /dev/mock-* string:
		// ReleaseFlasherImmediate polls WaitForPort(port, 3*time.Second, ...)
		// after every successful flash/erase to detect USB re-enumeration.
		// os.Stat succeeds on a real path immediately, so the poll returns
		// on its first iteration instead of paying the full 3s deadline.
		"port": tmpDir,
		"images": []any{
			map[string]any{"path": fw, "offset": float64(0x1000)},
		},
		"force_offsets": true,
		"boot_wait":     float64(0),
	}
	msg := s.HandleMessage(ctx, toolCallMessage(t, "esp_flash", args, "tok-flash"))
	requireToolCallOK(t, msg)
	assert.True(t, flasher.flashImagesCalled)

	frames := progressFrames(t, sess.ch)
	require.Len(t, frames, 5, "expected throttled percent-gated frames, not one per byte-progress call")

	lastProgress := -1
	for i, f := range frames {
		assert.Equal(t, "tok-flash", f["progressToken"], "frame %d token", i)
		assert.Equal(t, "flashing", f["message"], "frame %d message", i)
		assert.EqualValues(t, 1000, f["total"], "frame %d total", i)
		progress, ok := f["progress"].(int)
		require.True(t, ok, "frame %d progress must be an int", i)
		assert.Greater(t, progress, lastProgress, "frame %d progress must strictly increase", i)
		lastProgress = progress
	}
	assert.EqualValues(t, 1000, frames[len(frames)-1]["progress"], "final frame must reach completion")
}

func TestHandleFlashNoProgressTokenEmitsNoNotifications(t *testing.T) {
	setupTestPorts(t)
	setupTestFlasherFactory(t)

	tmpDir := t.TempDir()
	fw := tmpDir + "/firmware.bin"
	require.NoError(t, os.WriteFile(fw, []byte("firmware data"), 0644))

	flasher := &mockFlasher{
		flashImagesProgress: func(progress espflasher.ProgressFunc) {
			progress(0, 1000)
			progress(500, 1000)
			progress(1000, 1000)
		},
	}
	session.SetFlasherFactory(func(port string, opts *espflasher.FlasherOptions) (esp.Flasher, error) {
		return flasher, nil
	})
	t.Cleanup(func() { session.SetFlasherFactory(esp.DefaultFlasherFactory) })

	s := newProgressTestServer(t)
	sess := newNotifyCapture("flash-no-token")
	ctx := s.WithContext(context.Background(), sess)

	args := map[string]any{
		"port": tmpDir, // real path so WaitForPort's os.Stat check short-circuits; see comment above
		"images": []any{
			map[string]any{"path": fw, "offset": float64(0x1000)},
		},
		"force_offsets": true,
		"boot_wait":     float64(0),
	}
	msg := s.HandleMessage(ctx, toolCallMessage(t, "esp_flash", args, nil))
	requireToolCallOK(t, msg)
	assert.True(t, flasher.flashImagesCalled)

	frames := progressFrames(t, sess.ch)
	assert.Empty(t, frames, "no progressToken supplied -> zero progress notifications")
}

func TestHandleEraseEmitsRealProgressNotifications(t *testing.T) {
	setupTestPorts(t)
	setupTestFlasherFactory(t)

	flasher := &mockFlasher{
		eraseFlashProgress: func(progress espflasher.ProgressFunc) {
			progress(0, 100)   // 0%
			progress(1, 100)   // 1%
			progress(1, 100)   // dropped (same percent)
			progress(50, 100)  // 50%
			progress(100, 100) // 100% completion
		},
	}
	session.SetFlasherFactory(func(port string, opts *espflasher.FlasherOptions) (esp.Flasher, error) {
		return flasher, nil
	})
	t.Cleanup(func() { session.SetFlasherFactory(esp.DefaultFlasherFactory) })

	s := newProgressTestServer(t)
	sess := newNotifyCapture("erase-progress")
	ctx := s.WithContext(context.Background(), sess)

	args := map[string]any{
		// Real path (see comment in TestHandleFlashEmitsRealProgressNotifications)
		// so ReleaseFlasherImmediate's post-erase WaitForPort returns instantly.
		"port":      t.TempDir(),
		"boot_wait": float64(0),
	}
	msg := s.HandleMessage(ctx, toolCallMessage(t, "esp_erase", args, "tok-erase"))
	requireToolCallOK(t, msg)
	assert.True(t, flasher.eraseFlashCalled)

	frames := progressFrames(t, sess.ch)
	require.Len(t, frames, 4, "expected throttled percent-gated frames, not one per byte-progress call")

	lastProgress := -1
	for i, f := range frames {
		assert.Equal(t, "tok-erase", f["progressToken"], "frame %d token", i)
		assert.Equal(t, "erasing", f["message"], "frame %d message", i)
		assert.EqualValues(t, 100, f["total"], "frame %d total", i)
		progress, ok := f["progress"].(int)
		require.True(t, ok, "frame %d progress must be an int", i)
		assert.Greater(t, progress, lastProgress, "frame %d progress must strictly increase", i)
		lastProgress = progress
	}
	assert.EqualValues(t, 100, frames[len(frames)-1]["progress"], "final frame must reach completion")
}

func TestHandleEraseNoProgressTokenEmitsNoNotifications(t *testing.T) {
	setupTestPorts(t)
	setupTestFlasherFactory(t)

	flasher := &mockFlasher{
		eraseFlashProgress: func(progress espflasher.ProgressFunc) {
			progress(0, 100)
			progress(50, 100)
			progress(100, 100)
		},
	}
	session.SetFlasherFactory(func(port string, opts *espflasher.FlasherOptions) (esp.Flasher, error) {
		return flasher, nil
	})
	t.Cleanup(func() { session.SetFlasherFactory(esp.DefaultFlasherFactory) })

	s := newProgressTestServer(t)
	sess := newNotifyCapture("erase-no-token")
	ctx := s.WithContext(context.Background(), sess)

	args := map[string]any{
		"port":      t.TempDir(), // real path so WaitForPort short-circuits; see comment above
		"boot_wait": float64(0),
	}
	msg := s.HandleMessage(ctx, toolCallMessage(t, "esp_erase", args, nil))
	requireToolCallOK(t, msg)
	assert.True(t, flasher.eraseFlashCalled)

	frames := progressFrames(t, sess.ch)
	assert.Empty(t, frames, "no progressToken supplied -> zero progress notifications")
}
