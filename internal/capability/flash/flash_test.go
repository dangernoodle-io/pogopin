package flash

import (
	"context"
	"testing"
	"time"

	"github.com/dangernoodle-io/shesha/testkit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	goSerial "go.bug.st/serial"

	"dangernoodle.io/pogopin/internal/serial"
	"dangernoodle.io/pogopin/internal/session"
	"dangernoodle.io/pogopin/internal/testutil"
)

func TestHandleFlashExternalSuccess(t *testing.T) {
	testutil.SetupTestPorts(t)
	testutil.SetupTestManagersFunc(t)

	m := serial.NewManagerWithBufferSize(1000)
	m.OpenFunc = func(name string, mode *goSerial.Mode) (goSerial.Port, error) {
		return &testutil.NoopPort{}, nil
	}
	session.InsertPort("test-port", session.NewPortSession(m, "test-port", m.Baud(), session.ModeReader))
	require.NoError(t, m.Start("test-port", 115200))

	h := newHarness(t)
	result, err := h.CallTool(context.Background(), "flash_external", map[string]any{
		"port":    "test-port",
		"command": "echo",
		"args":    []any{"hello"},
	})
	require.NoError(t, err)
	require.False(t, result.IsError)
	text := testkit.ResultText(result)
	assert.Contains(t, text, "\"success\": true")
	assert.Contains(t, text, "hello")

	if session.PortCount() > 0 {
		mgr, _, resolveErr := session.ResolveSession(map[string]interface{}{})
		if resolveErr == nil && mgr != nil && mgr.IsRunning() {
			_ = mgr.Stop()
		}
	}
}

func TestHandleFlashExternalMissingCommandIsSchemaRejected(t *testing.T) {
	testutil.SetupTestPorts(t)

	h := newHarness(t)
	result, err := h.CallTool(context.Background(), "flash_external", map[string]any{"port": "test-port"})
	require.NoError(t, err)
	require.True(t, result.IsError)
	assert.Contains(t, testkit.ResultText(result), "command")
}

func TestHandleFlashExternalNoPortOpen(t *testing.T) {
	testutil.SetupTestPorts(t)

	h := newHarness(t)
	result, err := h.CallTool(context.Background(), "flash_external", map[string]any{
		"command": "echo",
	})
	require.NoError(t, err)
	require.True(t, result.IsError)
}

// TestHandleFlashExternalBootCapture pins BR-30: flash_external must
// capture boot output after restarting the managed port, matching
// esp_flash's shape.
func TestHandleFlashExternalBootCapture(t *testing.T) {
	testutil.SetupTestPorts(t)
	testutil.SetupTestManagersFunc(t)

	m := serial.NewManagerWithBufferSize(1000)
	m.OpenFunc = func(name string, mode *goSerial.Mode) (goSerial.Port, error) {
		return &testutil.NoopPort{}, nil
	}
	session.InsertPort("test-port", session.NewPortSession(m, "test-port", m.Baud(), session.ModeReader))
	require.NoError(t, m.Start("test-port", 115200))

	orig := bootCaptureWait
	bootCaptureWait = func(d time.Duration) { m.AddToBuffer("boot: flashed") }
	t.Cleanup(func() { bootCaptureWait = orig })

	h := newHarness(t)
	result, err := h.CallTool(context.Background(), "flash_external", map[string]any{
		"port": "test-port", "command": "echo", "args": []any{"hello"}, "boot_wait": float64(1.0),
	})
	require.NoError(t, err)
	require.False(t, result.IsError)
	text := testkit.ResultText(result)
	assert.Contains(t, text, "boot_output")
	assert.Contains(t, text, "boot: flashed")

	if session.PortCount() > 0 {
		mgr, _, resolveErr := session.ResolveSession(map[string]interface{}{})
		if resolveErr == nil && mgr != nil && mgr.IsRunning() {
			_ = mgr.Stop()
		}
	}
}

// TestHandleFlashExternalCommandRejected covers handleFlashExternal's
// flash.Flash-error branch: a command that fails preflight (not found on
// PATH) must release the acquired session (not leave it stuck in
// ModeExternal) and surface the error, without ever touching port state.
func TestHandleFlashExternalCommandRejected(t *testing.T) {
	testutil.SetupTestPorts(t)
	testutil.SetupTestManagersFunc(t)

	m := serial.NewManagerWithBufferSize(1000)
	m.OpenFunc = func(name string, mode *goSerial.Mode) (goSerial.Port, error) {
		return &testutil.NoopPort{}, nil
	}
	session.InsertPort("test-port", session.NewPortSession(m, "test-port", m.Baud(), session.ModeReader))
	require.NoError(t, m.Start("test-port", 115200))

	h := newHarness(t)
	result, err := h.CallTool(context.Background(), "flash_external", map[string]any{
		"port": "test-port", "command": "pogopin-nonexistent-command-xyz",
	})
	require.NoError(t, err)
	require.True(t, result.IsError)

	if session.PortCount() > 0 {
		mgr, _, resolveErr := session.ResolveSession(map[string]interface{}{})
		if resolveErr == nil && mgr != nil && mgr.IsRunning() {
			_ = mgr.Stop()
		}
	}
}

// TestHandleFlashExternalOptionsWireThrough covers the OutputLines/
// OutputFilter/Shell/Cwd ExternalIn->flash.Options branches together: each
// is a separate if-block in handleFlashExternal that a zero-value request
// (the other tests in this file) never touches.
func TestHandleFlashExternalOptionsWireThrough(t *testing.T) {
	testutil.SetupTestPorts(t)
	testutil.SetupTestManagersFunc(t)

	m := serial.NewManagerWithBufferSize(1000)
	m.OpenFunc = func(name string, mode *goSerial.Mode) (goSerial.Port, error) {
		return &testutil.NoopPort{}, nil
	}
	session.InsertPort("test-port", session.NewPortSession(m, "test-port", m.Baud(), session.ModeReader))
	require.NoError(t, m.Start("test-port", 115200))

	h := newHarness(t)
	result, err := h.CallTool(context.Background(), "flash_external", map[string]any{
		"port":          "test-port",
		"command":       "printf 'one\\ntwo\\nthree\\n'",
		"shell":         true,
		"cwd":           t.TempDir(),
		"output_lines":  2,
		"output_filter": "^(one|two|three)$",
	})
	require.NoError(t, err)
	require.False(t, result.IsError)
	text := testkit.ResultText(result)
	assert.Contains(t, text, "two")
	assert.Contains(t, text, "three")
	assert.NotContains(t, text, "one")

	if session.PortCount() > 0 {
		mgr, _, resolveErr := session.ResolveSession(map[string]interface{}{})
		if resolveErr == nil && mgr != nil && mgr.IsRunning() {
			_ = mgr.Stop()
		}
	}
}

// TestHandleFlashExternalBootWaitZeroOmitsField pins that boot_wait=0 omits
// boot_output entirely, like esp_flash.
func TestHandleFlashExternalBootWaitZeroOmitsField(t *testing.T) {
	testutil.SetupTestPorts(t)
	testutil.SetupTestManagersFunc(t)

	m := serial.NewManagerWithBufferSize(1000)
	m.OpenFunc = func(name string, mode *goSerial.Mode) (goSerial.Port, error) {
		return &testutil.NoopPort{}, nil
	}
	session.InsertPort("test-port", session.NewPortSession(m, "test-port", m.Baud(), session.ModeReader))
	require.NoError(t, m.Start("test-port", 115200))

	h := newHarness(t)
	result, err := h.CallTool(context.Background(), "flash_external", map[string]any{
		"port": "test-port", "command": "echo", "args": []any{"hello"}, "boot_wait": float64(0),
	})
	require.NoError(t, err)
	require.False(t, result.IsError)
	assert.NotContains(t, testkit.ResultText(result), "boot_output")

	if session.PortCount() > 0 {
		mgr, _, resolveErr := session.ResolveSession(map[string]interface{}{})
		if resolveErr == nil && mgr != nil && mgr.IsRunning() {
			_ = mgr.Stop()
		}
	}
}
