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
	goSerial "go.bug.st/serial"
	espflasher "tinygo.org/x/espflasher/pkg/espflasher"
)

func TestHandleFlashNewPort(t *testing.T) {
	setupTestPorts(t)
	setupTestFlasherFactory(t)
	setupFastWaitForPort(t)
	setupTestListPorts(t)
	setupTestManagersFunc(t)

	mockFlasher := &mockFlasher{}
	session.SetFlasherFactory(func(port string, opts *espflasher.FlasherOptions) (esp.Flasher, error) {
		return mockFlasher, nil
	})

	// Mock manager that accepts any port.
	session.SetNewManagerFunc(func(bufSize int) *serial.Manager {
		mgr := serial.NewManagerWithBufferSize(bufSize)
		// Override OpenFunc to accept any port (for testing).
		mgr.OpenFunc = func(portName string, mode *goSerial.Mode) (goSerial.Port, error) {
			return &noopPort{}, nil
		}
		return mgr
	})

	// Return a different port to simulate re-enumeration.
	session.SetListPortsFn(func(usbOnly bool) ([]serial.PortInfo, error) {
		return []serial.PortInfo{
			{Name: "/dev/ttyUSB1"},
		}, nil
	})

	tmpDir := t.TempDir()
	fw := tmpDir + "/firmware.bin"
	err := os.WriteFile(fw, []byte("firmware data"), 0644)
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
	}

	result, err := handleFlash(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.False(t, result.IsError)

	tc, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, tc.Text, "new_port")
	assert.Contains(t, tc.Text, "/dev/ttyUSB1")
}

func TestHandleEraseNewPort(t *testing.T) {
	setupTestPorts(t)
	setupTestFlasherFactory(t)
	setupFastWaitForPort(t)
	setupTestListPorts(t)
	setupTestManagersFunc(t)

	mockFlasher := &mockFlasher{}
	session.SetFlasherFactory(func(port string, opts *espflasher.FlasherOptions) (esp.Flasher, error) {
		return mockFlasher, nil
	})

	// Mock manager that accepts any port.
	session.SetNewManagerFunc(func(bufSize int) *serial.Manager {
		mgr := serial.NewManagerWithBufferSize(bufSize)
		mgr.OpenFunc = func(portName string, mode *goSerial.Mode) (goSerial.Port, error) {
			return &noopPort{}, nil
		}
		return mgr
	})

	session.SetListPortsFn(func(usbOnly bool) ([]serial.PortInfo, error) {
		return []serial.PortInfo{
			{Name: "/dev/ttyUSB1"},
		}, nil
	})

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{
		"port": "/dev/ttyUSB0",
	}

	result, err := handleErase(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.False(t, result.IsError)

	tc, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, tc.Text, "new_port")
	assert.Contains(t, tc.Text, "/dev/ttyUSB1")
}

func TestHandleResetNewPort(t *testing.T) {
	setupTestPorts(t)
	setupTestFlasherFactory(t)
	setupFastWaitForPort(t)
	setupTestListPorts(t)
	setupTestManagersFunc(t)

	mockFlasher := &mockFlasher{}
	session.SetFlasherFactory(func(port string, opts *espflasher.FlasherOptions) (esp.Flasher, error) {
		return mockFlasher, nil
	})

	// Mock manager that accepts any port.
	session.SetNewManagerFunc(func(bufSize int) *serial.Manager {
		mgr := serial.NewManagerWithBufferSize(bufSize)
		mgr.OpenFunc = func(portName string, mode *goSerial.Mode) (goSerial.Port, error) {
			return &noopPort{}, nil
		}
		return mgr
	})

	session.SetListPortsFn(func(usbOnly bool) ([]serial.PortInfo, error) {
		return []serial.PortInfo{
			{Name: "/dev/ttyUSB1"},
		}, nil
	})

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{
		"port": "/dev/ttyUSB0",
	}

	result, err := handleReset(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.False(t, result.IsError)

	tc, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, tc.Text, "new_port")
	assert.Contains(t, tc.Text, "/dev/ttyUSB1")
}

func TestHandleFlashNoNewPort(t *testing.T) {
	setupTestPorts(t)
	setupTestFlasherFactory(t)
	setupFastWaitForPort(t)
	setupTestListPorts(t)
	setupTestManagersFunc(t)

	mockFlasher := &mockFlasher{}
	session.SetFlasherFactory(func(port string, opts *espflasher.FlasherOptions) (esp.Flasher, error) {
		return mockFlasher, nil
	})

	// Mock manager that accepts any port.
	session.SetNewManagerFunc(func(bufSize int) *serial.Manager {
		mgr := serial.NewManagerWithBufferSize(bufSize)
		mgr.OpenFunc = func(portName string, mode *goSerial.Mode) (goSerial.Port, error) {
			return &noopPort{}, nil
		}
		return mgr
	})

	// Return same port — no re-enumeration.
	session.SetListPortsFn(func(usbOnly bool) ([]serial.PortInfo, error) {
		return []serial.PortInfo{
			{Name: "/dev/ttyUSB0"},
		}, nil
	})

	tmpDir := t.TempDir()
	fw := tmpDir + "/firmware.bin"
	err := os.WriteFile(fw, []byte("firmware data"), 0644)
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
	}

	result, err := handleFlash(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.False(t, result.IsError)

	tc, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	assert.NotContains(t, tc.Text, "new_port")
}
