package mcpserver

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"dangernoodle.io/pogopin/internal/esp"
	"dangernoodle.io/pogopin/internal/serial"
	"dangernoodle.io/pogopin/internal/session"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	goSerial "go.bug.st/serial"
	espflasher "tinygo.org/x/espflasher/pkg/espflasher"
)

func TestReadAfterDisconnect(t *testing.T) {
	setupTestPorts(t)

	testMgr := serial.NewManager()
	testMgr.AddToBuffer("line before disconnect")
	testMgr.AddToBuffer("second line")
	testMgr.SetTestState(false, "test-port", 115200, fmt.Errorf("device removed"))
	session.InsertPort("test-port", session.NewPortSession(testMgr, "test-port", 115200, session.ModeReader))

	req := mcp.CallToolRequest{}
	result, err := handleSerialRead(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, result)

	require.Len(t, result.Content, 1)
	tc, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	text := tc.Text
	assert.Contains(t, text, "line before disconnect")
	assert.Contains(t, text, "second line")
	assert.Contains(t, text, "[serial reader stopped: device removed]")
}

func TestSerialReadPatternFilter(t *testing.T) {
	setupTestPorts(t)

	testMgr := serial.NewManager()
	testMgr.AddToBuffer("INFO: starting up")
	testMgr.AddToBuffer("DEBUG: verbose stuff")
	testMgr.AddToBuffer("INFO: ready")
	testMgr.AddToBuffer("ERROR: something broke")
	testMgr.SetTestState(true, "test-port", 115200, nil)
	session.InsertPort("test-port", session.NewPortSession(testMgr, "test-port", testMgr.Baud(), session.ModeReader))

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{
		"pattern": "^INFO:",
	}
	result, err := handleSerialRead(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, result)

	tc, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, tc.Text, "INFO: starting up")
	assert.Contains(t, tc.Text, "INFO: ready")
	assert.NotContains(t, tc.Text, "DEBUG:")
	assert.NotContains(t, tc.Text, "ERROR:")
}

func TestSerialReadInvalidPattern(t *testing.T) {
	setupTestPorts(t)

	testMgr := serial.NewManager()
	testMgr.AddToBuffer("some line")
	testMgr.SetTestState(true, "test-port", 115200, nil)
	session.InsertPort("test-port", session.NewPortSession(testMgr, "test-port", testMgr.Baud(), session.ModeReader))

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{
		"pattern": "[invalid",
	}
	result, err := handleSerialRead(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, result)

	tc, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, tc.Text, "invalid pattern")
}

func TestHandleSerialStop(t *testing.T) {
	setupTestPorts(t)

	testMgr := serial.NewManager()
	testMgr.SetTestState(true, "test-port", 115200, nil)
	session.InsertPort("test-port", session.NewPortSession(testMgr, "test-port", testMgr.Baud(), session.ModeReader))

	req := mcp.CallToolRequest{}
	result, err := handleSerialStop(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, result)

	tc, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, tc.Text, "Stopped reading from test-port")
	assert.Equal(t, 0, session.PortCount())
}

func TestHandleSerialStatusSinglePort(t *testing.T) {
	setupTestPorts(t)

	testMgr := serial.NewManager()
	testMgr.SetTestState(true, "test-port", 115200, nil)
	testMgr.AddToBuffer("line 1")
	testMgr.AddToBuffer("line 2")
	testMgr.AddToBuffer("line 3")
	session.InsertPort("test-port", session.NewPortSession(testMgr, "test-port", testMgr.Baud(), session.ModeReader))

	req := mcp.CallToolRequest{}
	result, err := handleSerialStatus(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, result)

	tc, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, tc.Text, "\"running\": true")
	assert.Contains(t, tc.Text, "\"port\": \"test-port\"")
	assert.Contains(t, tc.Text, "\"baud\": 115200")
	assert.Contains(t, tc.Text, "\"buffer_lines\": 3")
	assert.Contains(t, tc.Text, "\"reconnecting\":")
}

func TestHandleSerialStatusMultiplePorts(t *testing.T) {
	setupTestPorts(t)

	testMgrA := serial.NewManager()
	testMgrA.SetTestState(true, "port-a", 9600, nil)
	session.InsertPort("port-a", session.NewPortSession(testMgrA, "port-a", testMgrA.Baud(), session.ModeReader))

	testMgrB := serial.NewManager()
	testMgrB.SetTestState(true, "port-b", 115200, nil)
	session.InsertPort("port-b", session.NewPortSession(testMgrB, "port-b", testMgrB.Baud(), session.ModeReader))

	req := mcp.CallToolRequest{}
	result, err := handleSerialStatus(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, result)

	tc, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, tc.Text, "\"ports\"")
	assert.Contains(t, tc.Text, "\"port-a\"")
	assert.Contains(t, tc.Text, "\"port-b\"")
	assert.Contains(t, tc.Text, "\"reconnecting\":")
}

func TestHandleSerialReadNotRunningNoBuffer(t *testing.T) {
	setupTestPorts(t)

	testMgr := serial.NewManager()
	testMgr.SetTestState(false, "test-port", 115200, nil)
	session.InsertPort("test-port", session.NewPortSession(testMgr, "test-port", testMgr.Baud(), session.ModeReader))

	req := mcp.CallToolRequest{}
	result, err := handleSerialRead(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, result)

	tc, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	// Dead sessions without buffered data are evicted with this error.
	assert.Contains(t, tc.Text, "has stopped")
}

func TestHandleSerialReadNotRunningWithError(t *testing.T) {
	setupTestPorts(t)

	testMgr := serial.NewManager()
	testMgr.SetTestState(false, "test-port", 115200, fmt.Errorf("connection lost"))
	session.InsertPort("test-port", session.NewPortSession(testMgr, "test-port", testMgr.Baud(), session.ModeReader))

	req := mcp.CallToolRequest{}
	result, err := handleSerialRead(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, result)

	tc, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	// Dead sessions without buffered data are evicted with this error.
	assert.Contains(t, tc.Text, "has stopped")
}

func TestHandleSerialWriteNotRunning(t *testing.T) {
	setupTestPorts(t)

	testMgr := serial.NewManager()
	testMgr.SetTestState(false, "test-port", 115200, nil)
	session.InsertPort("test-port", session.NewPortSession(testMgr, "test-port", testMgr.Baud(), session.ModeReader))

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{
		"data": "hello",
	}
	result, err := handleSerialWrite(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, result)

	tc, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	// Dead sessions without buffered data are evicted with this error.
	assert.Contains(t, tc.Text, "has stopped")
}

func TestHandleSerialList(t *testing.T) {
	req := mcp.CallToolRequest{}
	result, err := handleSerialList(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Len(t, result.Content, 1)
}

func TestWithRecover(t *testing.T) {
	panicHandler := func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		panic("test panic")
	}

	wrappedHandler := withRecover(panicHandler)
	result, err := wrappedHandler(context.Background(), mcp.CallToolRequest{})

	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, result.IsError)

	tc, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, tc.Text, "internal error: test panic")
}

func TestHandleSerialStartMissingPort(t *testing.T) {
	setupTestPorts(t)

	req := mcp.CallToolRequest{}
	result, err := handleSerialStart(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, result.IsError)

	tc, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, tc.Text, "port")
}

func TestHandleSerialFlashNoPort(t *testing.T) {
	setupTestPorts(t)

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{
		"command": "echo",
		"args":    []interface{}{"hi"},
	}
	result, err := handleSerialFlash(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, result.IsError)

	tc, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, tc.Text, "no serial port open")
}

func TestHandleSerialFlashMissingCommand(t *testing.T) {
	setupTestPorts(t)

	testMgr := serial.NewManager()
	testMgr.SetTestState(true, "test-port", 115200, nil)
	session.InsertPort("test-port", session.NewPortSession(testMgr, "test-port", testMgr.Baud(), session.ModeReader))

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{}
	result, err := handleSerialFlash(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, result.IsError)

	tc, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, tc.Text, "command")
}

func TestHandleSerialReadWithClear(t *testing.T) {
	setupTestPorts(t)

	testMgr := serial.NewManager()
	testMgr.AddToBuffer("line 1")
	testMgr.AddToBuffer("line 2")
	testMgr.AddToBuffer("line 3")
	testMgr.SetTestState(true, "test-port", 115200, nil)
	session.InsertPort("test-port", session.NewPortSession(testMgr, "test-port", testMgr.Baud(), session.ModeReader))

	// First read with clear=true.
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{
		"clear": true,
	}
	result, err := handleSerialRead(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, result)

	tc, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, tc.Text, "line 1")
	assert.Contains(t, tc.Text, "line 2")
	assert.Contains(t, tc.Text, "line 3")

	// Second read should be empty after clear.
	req2 := mcp.CallToolRequest{}
	result2, err2 := handleSerialRead(context.Background(), req2)
	require.NoError(t, err2)
	require.NotNil(t, result2)

	tc2, ok := result2.Content[0].(mcp.TextContent)
	require.True(t, ok)
	assert.Equal(t, "", tc2.Text)
}

func TestHandleSerialWriteRaw(t *testing.T) {
	setupTestPorts(t)

	testMgr := serial.NewManager()
	testMgr.SetTestState(true, "test-port", 115200, nil)
	session.InsertPort("test-port", session.NewPortSession(testMgr, "test-port", testMgr.Baud(), session.ModeReader))

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{
		"data": "hello",
		"raw":  true,
	}
	result, err := handleSerialWrite(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, result.IsError)

	tc, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, tc.Text, "serial port is not running")
}

func TestHandleSerialStatusWithError(t *testing.T) {
	setupTestPorts(t)

	testMgr := serial.NewManager()
	testMgr.SetTestState(true, "test-port", 115200, fmt.Errorf("read timeout"))
	session.InsertPort("test-port", session.NewPortSession(testMgr, "test-port", testMgr.Baud(), session.ModeReader))

	req := mcp.CallToolRequest{}
	result, err := handleSerialStatus(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, result)

	tc, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, tc.Text, "\"last_error\"")
	assert.Contains(t, tc.Text, "read timeout")
	assert.Contains(t, tc.Text, "\"reconnecting\":")
}

func TestHandleSerialStatusExplicitPort(t *testing.T) {
	setupTestPorts(t)

	testMgrA := serial.NewManager()
	testMgrA.SetTestState(true, "port-a", 9600, nil)
	session.InsertPort("port-a", session.NewPortSession(testMgrA, "port-a", testMgrA.Baud(), session.ModeReader))

	testMgrB := serial.NewManager()
	testMgrB.SetTestState(true, "port-b", 115200, nil)
	session.InsertPort("port-b", session.NewPortSession(testMgrB, "port-b", testMgrB.Baud(), session.ModeReader))

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{
		"port": "port-a",
	}
	result, err := handleSerialStatus(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, result)

	tc, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, tc.Text, "\"port\": \"port-a\"")
	assert.NotContains(t, tc.Text, "\"ports\"")
	assert.NotContains(t, tc.Text, "port-b")
	assert.Contains(t, tc.Text, "\"reconnecting\":")
}

func TestHandleSerialStartSuccess(t *testing.T) {
	setupTestPorts(t)
	setupTestManagersFunc(t)

	session.SetNewManagerFunc(func(bufSize int) *serial.Manager {
		m := serial.NewManagerWithBufferSize(bufSize)
		m.OpenFunc = func(name string, mode *goSerial.Mode) (goSerial.Port, error) {
			return &noopPort{}, nil
		}
		return m
	})

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{
		"port":        "test-port",
		"baud":        float64(9600),
		"buffer_size": float64(500),
	}
	result, err := handleSerialStart(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.False(t, result.IsError)

	tc, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, tc.Text, "Started reading from test-port at 9600 baud")
}

func TestHandleSerialStartOpenError(t *testing.T) {
	setupTestPorts(t)
	setupTestManagersFunc(t)

	session.SetNewManagerFunc(func(bufSize int) *serial.Manager {
		m := serial.NewManagerWithBufferSize(bufSize)
		m.OpenFunc = func(name string, mode *goSerial.Mode) (goSerial.Port, error) {
			return nil, fmt.Errorf("device busy")
		}
		return m
	})

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{
		"port": "test-port",
	}
	result, err := handleSerialStart(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, result.IsError)

	tc, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, tc.Text, "device busy")
}

func TestHandleSerialFlashSuccess(t *testing.T) {
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
		"port":    "test-port",
		"command": "echo",
		"args":    []interface{}{"hello"},
	}
	result, err := handleSerialFlash(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.False(t, result.IsError)

	tc, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, tc.Text, "\"success\": true")
	assert.Contains(t, tc.Text, "\"command_output\"")
	assert.Contains(t, tc.Text, "hello")

	portCount := session.PortCount()
	if portCount > 0 {
		m, _, _ := session.ResolveSession(map[string]interface{}{})
		if m != nil && m.IsRunning() {
			_ = m.Stop()
		}
	}
}

func TestHandleSerialStartReusesExistingManager(t *testing.T) {
	setupTestPorts(t)
	setupTestManagersFunc(t)

	m := serial.NewManagerWithBufferSize(1000)
	m.OpenFunc = func(name string, mode *goSerial.Mode) (goSerial.Port, error) {
		return &noopPort{}, nil
	}
	session.InsertPort("test-port", session.NewPortSession(m, "test-port", m.Baud(), session.ModeReader))

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{
		"port": "test-port",
		"baud": float64(9600),
	}
	result, err := handleSerialStart(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.False(t, result.IsError)

	assert.Equal(t, 1, session.PortCount())

	_ = m.Stop()
}

func TestHandleSerialFlashShellMode(t *testing.T) {
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
		"port":    "test-port",
		"command": "echo hello && echo world",
		"shell":   true,
	}
	result, err := handleSerialFlash(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.False(t, result.IsError)

	tc, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, tc.Text, "hello")
	assert.Contains(t, tc.Text, "world")

	portCount := session.PortCount()
	if portCount > 0 {
		m, _, _ := session.ResolveSession(map[string]interface{}{})
		if m != nil && m.IsRunning() {
			_ = m.Stop()
		}
	}
}

func TestHandleSerialFlashCwd(t *testing.T) {
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
		"port":    "test-port",
		"command": "pwd",
		"cwd":     "/tmp",
	}
	result, err := handleSerialFlash(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.False(t, result.IsError)

	tc, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	// macOS /tmp is symlinked to /private/tmp.
	assert.True(t, strings.Contains(tc.Text, "/tmp") || strings.Contains(tc.Text, "/private/tmp"))

	portCount := session.PortCount()
	if portCount > 0 {
		m, _, _ := session.ResolveSession(map[string]interface{}{})
		if m != nil && m.IsRunning() {
			_ = m.Stop()
		}
	}
}

func TestRegisterTools(t *testing.T) {
	s := server.NewMCPServer("pogopin", "test",
		server.WithToolCapabilities(true),
	)
	require.NotNil(t, s)
	registerTools(s)
	// If we get here without panicking, registration succeeded.
}

func TestHandleSerialStartAutoResetUSB(t *testing.T) {
	setupTestPorts(t)
	setupTestManagersFunc(t)
	setupTestFlasherFactory(t)
	setupTestIsUSBPort(t)
	setupFastWaitForPort(t)
	setupFastBootCapture(t)

	session.SetNewManagerFunc(func(bufSize int) *serial.Manager {
		m := serial.NewManagerWithBufferSize(bufSize)
		m.OpenFunc = func(name string, mode *goSerial.Mode) (goSerial.Port, error) {
			return &noopPort{}, nil
		}
		return m
	})

	mockFlasher := &mockESPFlasher{resetCalled: false}
	session.SetFlasherFactory(func(port string, opts *espflasher.FlasherOptions) (esp.Flasher, error) {
		return mockFlasher, nil
	})

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{
		"port": "/dev/cu.usbmodem1101",
	}
	result, err := handleSerialStart(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.False(t, result.IsError)

	tc, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, tc.Text, "Started reading from /dev/cu.usbmodem1101")
	assert.Contains(t, tc.Text, "auto-reset")
}

func TestHandleSerialStartAutoResetFalse(t *testing.T) {
	setupTestPorts(t)
	setupTestManagersFunc(t)
	setupTestFlasherFactory(t)
	setupFastWaitForPort(t)
	setupFastBootCapture(t)

	session.SetNewManagerFunc(func(bufSize int) *serial.Manager {
		m := serial.NewManagerWithBufferSize(bufSize)
		m.OpenFunc = func(name string, mode *goSerial.Mode) (goSerial.Port, error) {
			return &noopPort{}, nil
		}
		return m
	})

	mockFlasher := &mockESPFlasher{resetCalled: false}
	session.SetFlasherFactory(func(port string, opts *espflasher.FlasherOptions) (esp.Flasher, error) {
		return mockFlasher, nil
	})

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{
		"port":       "/dev/cu.usbmodem1101",
		"auto_reset": false,
	}
	result, err := handleSerialStart(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.False(t, result.IsError)

	tc, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, tc.Text, "Started reading from /dev/cu.usbmodem1101")
	assert.NotContains(t, tc.Text, "auto-reset")
	assert.False(t, mockFlasher.resetCalled)
}

func TestHandleSerialStartAutoResetNonUSB(t *testing.T) {
	setupTestPorts(t)
	setupTestManagersFunc(t)
	setupTestFlasherFactory(t)
	setupFastWaitForPort(t)
	setupFastBootCapture(t)

	session.SetNewManagerFunc(func(bufSize int) *serial.Manager {
		m := serial.NewManagerWithBufferSize(bufSize)
		m.OpenFunc = func(name string, mode *goSerial.Mode) (goSerial.Port, error) {
			return &noopPort{}, nil
		}
		return m
	})

	mockFlasher := &mockESPFlasher{resetCalled: false}
	session.SetFlasherFactory(func(port string, opts *espflasher.FlasherOptions) (esp.Flasher, error) {
		return mockFlasher, nil
	})

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{
		"port": "/dev/ttyS0",
	}
	result, err := handleSerialStart(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.False(t, result.IsError)

	tc, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, tc.Text, "Started reading from /dev/ttyS0")
	assert.NotContains(t, tc.Text, "auto-reset")
	assert.False(t, mockFlasher.resetCalled)
}

func TestHandleSerialStartAutoResetFailure(t *testing.T) {
	setupTestPorts(t)
	setupTestManagersFunc(t)
	setupTestFlasherFactory(t)
	setupFastWaitForPort(t)
	setupFastBootCapture(t)

	session.SetNewManagerFunc(func(bufSize int) *serial.Manager {
		m := serial.NewManagerWithBufferSize(bufSize)
		m.OpenFunc = func(name string, mode *goSerial.Mode) (goSerial.Port, error) {
			return &noopPort{}, nil
		}
		return m
	})

	session.SetFlasherFactory(func(port string, opts *espflasher.FlasherOptions) (esp.Flasher, error) {
		return nil, fmt.Errorf("device not found")
	})

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{
		"port": "/dev/cu.usbmodem1101",
	}
	result, err := handleSerialStart(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.False(t, result.IsError)

	tc, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, tc.Text, "Started reading from /dev/cu.usbmodem1101")
	assert.NotContains(t, tc.Text, "auto-reset")
}

func TestHandleSerialStopExplicitPort(t *testing.T) {
	setupTestPorts(t)
	testMgr := serial.NewManager()
	testMgr.SetTestState(true, "port-a", 115200, nil)
	session.InsertPort("port-a", session.NewPortSession(testMgr, "port-a", testMgr.Baud(), session.ModeReader))
	session.InsertPort("port-b", session.NewPortSession(serial.NewManager(), "port-b", 115200, session.ModeReader))

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{"port": "port-a"}
	result, err := handleSerialStop(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.False(t, result.IsError)
}

func TestHandleSerialStopNoPort(t *testing.T) {
	setupTestPorts(t)
	req := mcp.CallToolRequest{}
	result, err := handleSerialStop(context.Background(), req)
	require.NoError(t, err)
	require.True(t, result.IsError)
}
