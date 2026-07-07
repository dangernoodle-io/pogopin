package mcpserver

import (
	"context"
	"os"
	"testing"
	"time"

	"dangernoodle.io/pogopin/internal/esp"
	"dangernoodle.io/pogopin/internal/serial"
	"dangernoodle.io/pogopin/internal/session"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	goSerial "go.bug.st/serial"
	espflasher "tinygo.org/x/espflasher/pkg/espflasher"
)

func TestCaptureBootOutputNilSession(t *testing.T) {
	setupFastBootCapture(t)
	result := captureBootOutput(nil, 2.0)
	assert.Nil(t, result)
}

func TestCaptureBootOutputZeroDwell(t *testing.T) {
	setupFastBootCapture(t)
	mgr := serial.NewManager()
	mgr.AddToBuffer("should not be read")
	sess := session.NewPortSession(mgr, "", 0, session.ModeReader)
	result := captureBootOutput(sess, 0)
	assert.Nil(t, result)
}

func TestCaptureBootOutputReadsLines(t *testing.T) {
	mgr := serial.NewManager()
	sess := session.NewPortSession(mgr, "", 0, session.ModeReader)
	orig := bootCaptureWait
	bootCaptureWait = func(d time.Duration) {
		mgr.AddToBuffer("boot: init")
		mgr.AddToBuffer("boot: ready")
	}
	t.Cleanup(func() { bootCaptureWait = orig })

	result := captureBootOutput(sess, 1.0)
	require.Len(t, result, 2)
	assert.Equal(t, "boot: init", result[0])
	assert.Equal(t, "boot: ready", result[1])
}

// TestCaptureBootOutputDropsStaleLines pins the BR-14 fix: lines that accumulated
// in the ring buffer before the ESP operation must not leak into boot_output.
func TestCaptureBootOutputDropsStaleLines(t *testing.T) {
	mgr := serial.NewManager()
	mgr.AddToBuffer("pre: stale line 1")
	mgr.AddToBuffer("pre: stale line 2")
	sess := session.NewPortSession(mgr, "", 0, session.ModeReader)
	orig := bootCaptureWait
	bootCaptureWait = func(d time.Duration) {
		mgr.AddToBuffer("boot: fresh")
	}
	t.Cleanup(func() { bootCaptureWait = orig })

	result := captureBootOutput(sess, 1.0)
	require.Len(t, result, 1)
	assert.Equal(t, "boot: fresh", result[0])
}

func TestHandleFlashBootCapture(t *testing.T) {
	setupTestPorts(t)
	setupTestFlasherFactory(t)
	setupFastWaitForPort(t)
	setupTestListPorts(t)

	// Set up managed port.
	testMgr := serial.NewManager()
	testMgr.SetTestState(true, "/dev/ttyUSB0", 115200, nil)
	session.InsertPort("/dev/ttyUSB0", session.NewPortSession(testMgr, "/dev/ttyUSB0", 115200, session.ModeReader))

	// Inject boot output during the capture wait window (post-reset).
	orig := bootCaptureWait
	bootCaptureWait = func(d time.Duration) {
		testMgr.AddToBuffer("ESP-IDF v5.1")
		testMgr.AddToBuffer("boot: ready")
	}
	t.Cleanup(func() { bootCaptureWait = orig })

	mockFlasher := &mockFlasher{}
	session.SetFlasherFactory(func(port string, opts *espflasher.FlasherOptions) (esp.Flasher, error) {
		return mockFlasher, nil
	})

	session.SetListPortsFn(func() ([]serial.PortInfo, error) {
		return []serial.PortInfo{{Name: "/dev/ttyUSB0"}}, nil
	})

	tmpDir := t.TempDir()
	fw := tmpDir + "/firmware.bin"
	err := os.WriteFile(fw, []byte("firmware"), 0644)
	require.NoError(t, err)

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{
		"port": "/dev/ttyUSB0",
		"images": []interface{}{
			map[string]interface{}{
				"path":   fw,
				"offset": float64(0),
			},
		},
		"boot_wait": float64(0.001),
	}

	result, err := handleFlash(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, result)

	tc, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, tc.Text, "boot_output")
	assert.Contains(t, tc.Text, "ESP-IDF v5.1")
}

func TestHandleEraseBootCapture(t *testing.T) {
	setupTestPorts(t)
	setupTestFlasherFactory(t)
	setupFastWaitForPort(t)
	setupTestListPorts(t)

	testMgr := serial.NewManager()
	testMgr.SetTestState(true, "/dev/ttyUSB0", 115200, nil)
	session.InsertPort("/dev/ttyUSB0", session.NewPortSession(testMgr, "/dev/ttyUSB0", 115200, session.ModeReader))

	orig := bootCaptureWait
	bootCaptureWait = func(d time.Duration) {
		testMgr.AddToBuffer("boot: erased")
	}
	t.Cleanup(func() { bootCaptureWait = orig })

	mockFlasher := &mockFlasher{}
	session.SetFlasherFactory(func(port string, opts *espflasher.FlasherOptions) (esp.Flasher, error) {
		return mockFlasher, nil
	})

	session.SetListPortsFn(func() ([]serial.PortInfo, error) {
		return []serial.PortInfo{{Name: "/dev/ttyUSB0"}}, nil
	})

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{
		"port":      "/dev/ttyUSB0",
		"boot_wait": float64(0.001),
	}

	result, err := handleErase(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, result)

	tc, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, tc.Text, "boot_output")
	assert.Contains(t, tc.Text, "status")
}

func TestHandleResetBootCapture(t *testing.T) {
	setupTestPorts(t)
	setupTestFlasherFactory(t)
	setupFastWaitForPort(t)
	setupTestListPorts(t)

	testMgr := serial.NewManager()
	testMgr.SetTestState(true, "/dev/ttyUSB0", 115200, nil)
	session.InsertPort("/dev/ttyUSB0", session.NewPortSession(testMgr, "/dev/ttyUSB0", 115200, session.ModeReader))

	orig := bootCaptureWait
	bootCaptureWait = func(d time.Duration) {
		testMgr.AddToBuffer("boot: reset")
	}
	t.Cleanup(func() { bootCaptureWait = orig })

	mockFlasher := &mockFlasher{}
	session.SetFlasherFactory(func(port string, opts *espflasher.FlasherOptions) (esp.Flasher, error) {
		return mockFlasher, nil
	})

	session.SetListPortsFn(func() ([]serial.PortInfo, error) {
		return []serial.PortInfo{{Name: "/dev/ttyUSB0"}}, nil
	})

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{
		"port":      "/dev/ttyUSB0",
		"boot_wait": float64(0.001),
	}

	result, err := handleReset(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, result)

	tc, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, tc.Text, "boot_output")
	assert.Contains(t, tc.Text, "device reset")
}

func TestHandleFlashBootWaitZero(t *testing.T) {
	setupTestPorts(t)
	setupTestFlasherFactory(t)
	setupFastWaitForPort(t)
	setupTestListPorts(t)
	setupFastBootCapture(t)

	mockFlasher := &mockFlasher{}
	session.SetFlasherFactory(func(port string, opts *espflasher.FlasherOptions) (esp.Flasher, error) {
		return mockFlasher, nil
	})

	session.SetListPortsFn(func() ([]serial.PortInfo, error) {
		return []serial.PortInfo{{Name: "/dev/ttyUSB0"}}, nil
	})

	tmpDir := t.TempDir()
	fw := tmpDir + "/firmware.bin"
	err := os.WriteFile(fw, []byte("firmware"), 0644)
	require.NoError(t, err)

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{
		"port": "/dev/ttyUSB0",
		"images": []interface{}{
			map[string]interface{}{
				"path":   fw,
				"offset": float64(0),
			},
		},
		"boot_wait": float64(0),
	}

	result, err := handleFlash(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.False(t, result.IsError)

	tc, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	assert.NotContains(t, tc.Text, "boot_output")
}

// TestHandleSerialFlashBootCapture pins BR-30: flash_external must capture boot
// output after restarting the managed port, matching handleFlash's shape.
func TestHandleSerialFlashBootCapture(t *testing.T) {
	setupTestPorts(t)
	setupTestManagersFunc(t)

	m := serial.NewManagerWithBufferSize(1000)
	m.OpenFunc = func(name string, mode *goSerial.Mode) (goSerial.Port, error) {
		return &noopPort{}, nil
	}
	session.InsertPort("test-port", session.NewPortSession(m, "test-port", m.Baud(), session.ModeReader))

	err := m.Start("test-port", 115200)
	require.NoError(t, err)
	assert.True(t, m.IsRunning())

	orig := bootCaptureWait
	bootCaptureWait = func(d time.Duration) {
		m.AddToBuffer("boot: flashed")
	}
	t.Cleanup(func() { bootCaptureWait = orig })

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{
		"port":      "test-port",
		"command":   "echo",
		"args":      []interface{}{"hello"},
		"boot_wait": float64(1.0),
	}
	result, err := handleSerialFlash(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.False(t, result.IsError)

	tc, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, tc.Text, "boot_output")
	assert.Contains(t, tc.Text, "boot: flashed")

	portCount := session.PortCount()
	if portCount > 0 {
		mgr, _, _ := session.ResolveSession(map[string]interface{}{})
		if mgr != nil && mgr.IsRunning() {
			_ = mgr.Stop()
		}
	}
}

// TestHandleSerialFlashBootWaitZeroOmitsField pins that boot_wait=0 (or the
// default with nothing captured) omits boot_output entirely, like handleFlash.
func TestHandleSerialFlashBootWaitZeroOmitsField(t *testing.T) {
	setupTestPorts(t)
	setupTestManagersFunc(t)

	m := serial.NewManagerWithBufferSize(1000)
	m.OpenFunc = func(name string, mode *goSerial.Mode) (goSerial.Port, error) {
		return &noopPort{}, nil
	}
	session.InsertPort("test-port", session.NewPortSession(m, "test-port", m.Baud(), session.ModeReader))

	err := m.Start("test-port", 115200)
	require.NoError(t, err)
	assert.True(t, m.IsRunning())

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{
		"port":      "test-port",
		"command":   "echo",
		"args":      []interface{}{"hello"},
		"boot_wait": float64(0),
	}
	result, err := handleSerialFlash(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.False(t, result.IsError)

	tc, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	assert.NotContains(t, tc.Text, "boot_output")

	portCount := session.PortCount()
	if portCount > 0 {
		mgr, _, _ := session.ResolveSession(map[string]interface{}{})
		if mgr != nil && mgr.IsRunning() {
			_ = mgr.Stop()
		}
	}
}
