package mcpserver

import (
	"context"
	"os"
	"testing"

	"dangernoodle.io/pogopin/internal/esp"
	"dangernoodle.io/pogopin/internal/serial"
	"dangernoodle.io/pogopin/internal/session"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
	setupFastBootCapture(t)
	mgr := serial.NewManager()
	mgr.AddToBuffer("boot: init")
	mgr.AddToBuffer("boot: ready")
	sess := session.NewPortSession(mgr, "", 0, session.ModeReader)
	result := captureBootOutput(sess, 1.0)
	require.Len(t, result, 2)
	assert.Equal(t, "boot: init", result[0])
	assert.Equal(t, "boot: ready", result[1])
}

func TestHandleFlashBootCapture(t *testing.T) {
	setupTestPorts(t)
	setupTestFlasherFactory(t)
	setupFastWaitForPort(t)
	setupTestListPorts(t)
	setupFastBootCapture(t)

	// Set up managed port.
	testMgr := serial.NewManager()
	testMgr.SetTestState(true, "/dev/ttyUSB0", 115200, nil)
	session.InsertPort("/dev/ttyUSB0", session.NewPortSession(testMgr, "/dev/ttyUSB0", 115200, session.ModeReader))

	// Add boot output that will be captured.
	testMgr.AddToBuffer("ESP-IDF v5.1")
	testMgr.AddToBuffer("boot: ready")

	mockFlasher := &mockFlasher{}
	session.SetFlasherFactory(func(port string, opts *espflasher.FlasherOptions) (esp.Flasher, error) {
		return mockFlasher, nil
	})

	session.SetListPortsFn(func(usbOnly bool) ([]serial.PortInfo, error) {
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
	setupFastBootCapture(t)

	testMgr := serial.NewManager()
	testMgr.SetTestState(true, "/dev/ttyUSB0", 115200, nil)
	session.InsertPort("/dev/ttyUSB0", session.NewPortSession(testMgr, "/dev/ttyUSB0", 115200, session.ModeReader))
	testMgr.AddToBuffer("boot: erased")

	mockFlasher := &mockFlasher{}
	session.SetFlasherFactory(func(port string, opts *espflasher.FlasherOptions) (esp.Flasher, error) {
		return mockFlasher, nil
	})

	session.SetListPortsFn(func(usbOnly bool) ([]serial.PortInfo, error) {
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
	setupFastBootCapture(t)

	testMgr := serial.NewManager()
	testMgr.SetTestState(true, "/dev/ttyUSB0", 115200, nil)
	session.InsertPort("/dev/ttyUSB0", session.NewPortSession(testMgr, "/dev/ttyUSB0", 115200, session.ModeReader))
	testMgr.AddToBuffer("boot: reset")

	mockFlasher := &mockFlasher{}
	session.SetFlasherFactory(func(port string, opts *espflasher.FlasherOptions) (esp.Flasher, error) {
		return mockFlasher, nil
	})

	session.SetListPortsFn(func(usbOnly bool) ([]serial.PortInfo, error) {
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

	session.SetListPortsFn(func(usbOnly bool) ([]serial.PortInfo, error) {
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
