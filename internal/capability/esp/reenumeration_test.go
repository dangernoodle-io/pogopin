package esp

import (
	"context"
	"os"
	"testing"

	"github.com/dangernoodle-io/shesha/testkit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	goSerial "go.bug.st/serial"

	"dangernoodle.io/pogopin/internal/serial"
	"dangernoodle.io/pogopin/internal/session"
	"dangernoodle.io/pogopin/internal/testutil"
)

// setupReenumerationManagers wires a manager factory that accepts any port
// (OpenFunc always succeeds via a NoopPort) so esp_flash/esp_erase/esp_reset
// can complete their post-op managed-port restart against a synthetic port
// name.
func setupReenumerationManagers(t *testing.T) {
	t.Helper()
	orig := session.SetNewManagerFunc(func(bufSize int) *serial.Manager {
		mgr := serial.NewManagerWithBufferSize(bufSize)
		mgr.OpenFunc = func(portName string, mode *goSerial.Mode) (goSerial.Port, error) {
			return &testutil.NoopPort{}, nil
		}
		return mgr
	})
	t.Cleanup(func() { session.SetNewManagerFunc(orig) })
}

// TestHandleFlashNewPort pins re-enumeration detection: when the port list
// seen after the op differs from the pre-op snapshot, the response includes
// new_port with the new port name.
func TestHandleFlashNewPort(t *testing.T) {
	testutil.SetupTestPorts(t)
	testutil.SetupTestFlasherFactory(t)
	testutil.SetupFastWaitForPort(t)
	testutil.SetupTestListPorts(t)
	setupReenumerationManagers(t)

	mock := &testutil.MockFlasher{}
	setFlasher(t, mock)

	var listCalls int
	orig := session.SetListPortsFn(func() ([]serial.PortInfo, error) {
		listCalls++
		if listCalls == 1 {
			return []serial.PortInfo{{Name: "/dev/ttyUSB0"}}, nil
		}
		return []serial.PortInfo{{Name: "/dev/ttyUSB1"}}, nil
	})
	t.Cleanup(func() { session.SetListPortsFn(orig) })

	tmpDir := t.TempDir()
	fw := tmpDir + "/firmware.bin"
	require.NoError(t, os.WriteFile(fw, []byte("firmware data"), 0o644))

	h := newHarness(t)
	result, err := h.CallTool(context.Background(), "esp_flash", map[string]any{
		"port": "/dev/ttyUSB0",
		"images": []any{
			map[string]any{"path": fw, "offset": float64(0)},
		},
	})
	require.NoError(t, err)
	require.False(t, result.IsError)
	text := testkit.ResultText(result)
	assert.Contains(t, text, "new_port")
	assert.Contains(t, text, "/dev/ttyUSB1")
}

func TestHandleEraseNewPort(t *testing.T) {
	testutil.SetupTestPorts(t)
	testutil.SetupTestFlasherFactory(t)
	testutil.SetupFastWaitForPort(t)
	testutil.SetupTestListPorts(t)
	setupReenumerationManagers(t)

	mock := &testutil.MockFlasher{}
	setFlasher(t, mock)

	var listCalls int
	orig := session.SetListPortsFn(func() ([]serial.PortInfo, error) {
		listCalls++
		if listCalls == 1 {
			return []serial.PortInfo{{Name: "/dev/ttyUSB0"}}, nil
		}
		return []serial.PortInfo{{Name: "/dev/ttyUSB1"}}, nil
	})
	t.Cleanup(func() { session.SetListPortsFn(orig) })

	h := newHarness(t)
	result, err := h.CallTool(context.Background(), "esp_erase", map[string]any{"port": "/dev/ttyUSB0"})
	require.NoError(t, err)
	require.False(t, result.IsError)
	text := testkit.ResultText(result)
	assert.Contains(t, text, "new_port")
	assert.Contains(t, text, "/dev/ttyUSB1")
}

func TestHandleResetNewPort(t *testing.T) {
	testutil.SetupTestPorts(t)
	testutil.SetupTestFlasherFactory(t)
	testutil.SetupFastWaitForPort(t)
	testutil.SetupTestListPorts(t)
	setupReenumerationManagers(t)

	mock := &testutil.MockFlasher{}
	setFlasher(t, mock)

	var listCalls int
	orig := session.SetListPortsFn(func() ([]serial.PortInfo, error) {
		listCalls++
		if listCalls == 1 {
			return []serial.PortInfo{{Name: "/dev/ttyUSB0"}}, nil
		}
		return []serial.PortInfo{{Name: "/dev/ttyUSB1"}}, nil
	})
	t.Cleanup(func() { session.SetListPortsFn(orig) })

	h := newHarness(t)
	result, err := h.CallTool(context.Background(), "esp_reset", map[string]any{"port": "/dev/ttyUSB0"})
	require.NoError(t, err)
	require.False(t, result.IsError)
	text := testkit.ResultText(result)
	assert.Contains(t, text, "new_port")
	assert.Contains(t, text, "/dev/ttyUSB1")
}

// TestHandleFlashNoNewPort pins the negative case: an unchanged port list
// omits new_port entirely.
func TestHandleFlashNoNewPort(t *testing.T) {
	testutil.SetupTestPorts(t)
	testutil.SetupTestFlasherFactory(t)
	testutil.SetupFastWaitForPort(t)
	testutil.SetupTestListPorts(t)
	setupReenumerationManagers(t)

	mock := &testutil.MockFlasher{}
	setFlasher(t, mock)

	orig := session.SetListPortsFn(func() ([]serial.PortInfo, error) {
		return []serial.PortInfo{{Name: "/dev/ttyUSB0"}}, nil
	})
	t.Cleanup(func() { session.SetListPortsFn(orig) })

	tmpDir := t.TempDir()
	fw := tmpDir + "/firmware.bin"
	require.NoError(t, os.WriteFile(fw, []byte("firmware data"), 0o644))

	h := newHarness(t)
	result, err := h.CallTool(context.Background(), "esp_flash", map[string]any{
		"port": "/dev/ttyUSB0",
		"images": []any{
			map[string]any{"path": fw, "offset": float64(0)},
		},
	})
	require.NoError(t, err)
	require.False(t, result.IsError)
	assert.NotContains(t, testkit.ResultText(result), "new_port")
}
