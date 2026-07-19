package mcpapp

import (
	"context"
	"testing"
	"time"

	"github.com/dangernoodle-io/shesha/testkit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"dangernoodle.io/pogopin/internal/serial"
	"dangernoodle.io/pogopin/internal/session"
)

// TestFlashExternalRoundTrip proves flash_external is wired end to end
// through the shesha stack: an unresolvable command surfaces
// preflightFlashCommand's "not found on PATH" error (flash.Flash's
// BR-51 preflight, exercised without depending on any real binary).
func TestFlashExternalRoundTrip(t *testing.T) {
	setupTestPorts(t)
	// ReleaseExternal's WaitForPort polls for the port to reappear
	// regardless of Flash()'s outcome; listing it immediately (rather than
	// an empty list) lets WaitForPort resolve on its first poll instead of
	// blocking for its full 3s timeout.
	origListFn := session.SetListPortsFn(func() ([]serial.PortInfo, error) {
		return []serial.PortInfo{{Name: "/dev/cu.flash-smoke"}}, nil
	})
	t.Cleanup(func() { session.SetListPortsFn(origListFn) })
	origInterval := session.SetWaitForPortInterval(time.Millisecond)
	t.Cleanup(func() { session.SetWaitForPortInterval(origInterval) })

	testMgr := serial.NewManager()
	testMgr.SetTestState(true, "/dev/cu.flash-smoke", 115200, nil)
	session.InsertPort("/dev/cu.flash-smoke", session.NewPortSession(testMgr, "/dev/cu.flash-smoke", 115200, session.ModeReader))

	app, err := BuildApp()
	require.NoError(t, err)
	h := testkit.New(t, app)
	unlockHardwareTier(t, h)

	res, err := h.CallTool(context.Background(), "flash_external", map[string]any{
		"port":    "/dev/cu.flash-smoke",
		"command": "definitely-not-a-real-flasher-binary-xyz",
	})
	require.NoError(t, err)
	require.True(t, res.IsError)
	assert.Contains(t, testkit.ResultText(res), "not found on PATH")
}
