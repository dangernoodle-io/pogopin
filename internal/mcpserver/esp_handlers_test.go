package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"

	"dangernoodle.io/breadboard/internal/esp"
	"dangernoodle.io/breadboard/internal/serial"
	"dangernoodle.io/breadboard/internal/session"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	espflasher "tinygo.org/x/espflasher/pkg/espflasher"
)

func TestHandleFlashSuccess(t *testing.T) {
	setupTestPorts(t)
	setupTestFlasherFactory(t)

	tmpDir := t.TempDir()
	fw := tmpDir + "/firmware.bin"
	err := os.WriteFile(fw, []byte("firmware data"), 0644)
	require.NoError(t, err)

	mockFlasher := &mockFlasher{}
	session.SetFlasherFactory(func(port string, opts *espflasher.FlasherOptions) (esp.Flasher, error) {
		return mockFlasher, nil
	})
	t.Cleanup(func() { session.SetFlasherFactory(esp.DefaultFlasherFactory) })

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{
		"port": "/dev/ttyUSB0",
		"images": []interface{}{
			map[string]interface{}{
				"path":   fw,
				"offset": float64(0x1000),
			},
		},
		"baud": float64(115200),
	}

	result, err := handleFlash(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.False(t, result.IsError)

	tc, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, tc.Text, "bytes_written")
	assert.True(t, mockFlasher.flashImagesCalled)
}

func TestHandleFlashPortManaged(t *testing.T) {
	setupTestPorts(t)
	setupTestFlasherFactory(t)
	setupFastWaitForPort(t)

	testMgr := serial.NewManager()
	testMgr.SetTestState(true, "/dev/ttyUSB0", 115200, nil)
	session.InsertPort("/dev/ttyUSB0", session.NewPortSession(testMgr, "/dev/ttyUSB0", 115200, session.ModeReader))

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{
		"port": "/dev/ttyUSB0",
		"images": []interface{}{
			map[string]interface{}{
				"path":   "/tmp/fw.bin",
				"offset": float64(0),
			},
		},
	}

	result, err := handleFlash(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, result)

	// Should NOT get "managed by serial_start" error — port is auto-stopped.
	if result.IsError {
		tc, ok := result.Content[0].(mcp.TextContent)
		require.True(t, ok)
		assert.NotContains(t, tc.Text, "managed by serial_start")
	}
}

func TestHandleFlashMissingPort(t *testing.T) {
	setupTestPorts(t)
	setupTestFlasherFactory(t)

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{}

	result, err := handleFlash(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, result.IsError)
}

func TestHandleFlashInvalidImages(t *testing.T) {
	setupTestPorts(t)
	setupTestFlasherFactory(t)

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{
		"port":   "/dev/ttyUSB0",
		"images": "not-an-array",
	}

	result, err := handleFlash(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, result.IsError)
}

func TestHandleEraseSuccess(t *testing.T) {
	setupTestPorts(t)
	setupTestFlasherFactory(t)

	mockFlasher := &mockFlasher{}
	session.SetFlasherFactory(func(port string, opts *espflasher.FlasherOptions) (esp.Flasher, error) {
		return mockFlasher, nil
	})
	t.Cleanup(func() { session.SetFlasherFactory(esp.DefaultFlasherFactory) })

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{
		"port": "/dev/ttyUSB0",
		"baud": float64(115200),
	}

	result, err := handleErase(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.False(t, result.IsError)

	tc, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, tc.Text, "success")
	assert.True(t, mockFlasher.eraseFlashCalled)
}

func TestHandleErasePortManaged(t *testing.T) {
	setupTestPorts(t)
	setupTestFlasherFactory(t)
	setupFastWaitForPort(t)

	testMgr := serial.NewManager()
	testMgr.SetTestState(true, "/dev/ttyUSB0", 115200, nil)
	session.InsertPort("/dev/ttyUSB0", session.NewPortSession(testMgr, "/dev/ttyUSB0", 115200, session.ModeReader))

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{
		"port": "/dev/ttyUSB0",
	}

	result, err := handleErase(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, result)

	// Should NOT get "managed by serial_start" error — port is auto-stopped.
	if result.IsError {
		tc, ok := result.Content[0].(mcp.TextContent)
		require.True(t, ok)
		assert.NotContains(t, tc.Text, "managed by serial_start")
	}
}

func TestHandleEraseRegionSuccess(t *testing.T) {
	setupTestPorts(t)
	setupTestFlasherFactory(t)

	mock := &mockFlasher{}
	session.SetFlasherFactory(func(port string, opts *espflasher.FlasherOptions) (esp.Flasher, error) {
		return mock, nil
	})
	t.Cleanup(func() { session.SetFlasherFactory(esp.DefaultFlasherFactory) })

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{
		"port":   "/dev/ttyUSB0",
		"offset": float64(0x1000),
		"size":   float64(0x1000),
	}
	result, err := handleErase(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.False(t, result.IsError)

	assert.True(t, mock.eraseRegionCalled)
	assert.Equal(t, uint32(0x1000), mock.eraseRegionOffset)
	assert.Equal(t, uint32(0x1000), mock.eraseRegionSize)
}

func TestHandleEraseRegionMissingSize(t *testing.T) {
	setupTestPorts(t)

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{
		"port":   "/dev/ttyUSB0",
		"offset": float64(0x1000),
	}
	result, err := handleErase(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, result.IsError)

	tc, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, tc.Text, "size")
}

func TestHandleESPInfoChipSuccess(t *testing.T) {
	setupTestPorts(t)
	setupTestFlasherFactory(t)

	mockFlasher := &mockFlasher{
		chipNameVal: "ESP32-S3",
		flashIDMfg:  0x20,
		flashIDDev:  0x0060,
	}
	session.SetFlasherFactory(func(port string, opts *espflasher.FlasherOptions) (esp.Flasher, error) {
		return mockFlasher, nil
	})
	t.Cleanup(func() { session.SetFlasherFactory(esp.DefaultFlasherFactory) })

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{
		"port": "/dev/ttyUSB0",
		"baud": float64(115200),
	}

	result, err := handleESPInfo(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.False(t, result.IsError)

	tc, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)

	var info map[string]interface{}
	err = json.Unmarshal([]byte(tc.Text), &info)
	require.NoError(t, err)

	// Check for chip section in response.
	chipData, ok := info["chip"].(map[string]interface{})
	require.True(t, ok, "chip section not found in response")
	assert.Equal(t, "ESP32-S3", chipData["chip_name"])
	assert.Equal(t, float64(0x20), chipData["manufacturer_id"])
	assert.Equal(t, float64(0x0060), chipData["device_id"])
}

func TestHandleESPInfoPortManaged(t *testing.T) {
	setupTestPorts(t)
	setupTestFlasherFactory(t)
	setupFastWaitForPort(t)

	testMgr := serial.NewManager()
	testMgr.SetTestState(true, "/dev/ttyUSB0", 115200, nil)
	session.InsertPort("/dev/ttyUSB0", session.NewPortSession(testMgr, "/dev/ttyUSB0", 115200, session.ModeReader))

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{
		"port": "/dev/ttyUSB0",
	}

	result, err := handleESPInfo(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, result)

	// Should NOT get "managed by serial_start" error — port is auto-stopped.
	if result.IsError {
		tc, ok := result.Content[0].(mcp.TextContent)
		require.True(t, ok)
		assert.NotContains(t, tc.Text, "managed by serial_start")
	}
}

func TestHandleESPInfoMissingPort(t *testing.T) {
	setupTestPorts(t)

	req := mcp.CallToolRequest{}
	result, err := handleESPInfo(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, result.IsError)

	tc, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, tc.Text, "port")
}

func TestHandleESPInfoError(t *testing.T) {
	setupTestPorts(t)
	setupTestFlasherFactory(t)

	session.SetFlasherFactory(func(port string, opts *espflasher.FlasherOptions) (esp.Flasher, error) {
		return nil, fmt.Errorf("connection failed")
	})
	t.Cleanup(func() { session.SetFlasherFactory(esp.DefaultFlasherFactory) })

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{
		"port": "/dev/ttyUSB0",
	}
	result, err := handleESPInfo(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, result.IsError)

	tc, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, tc.Text, "connection failed")
}

func TestHandleRegisterReadSuccess(t *testing.T) {
	setupTestPorts(t)
	setupTestFlasherFactory(t)

	mock := &mockFlasher{
		readRegisterVal: 0xDEADBEEF,
	}
	session.SetFlasherFactory(func(port string, opts *espflasher.FlasherOptions) (esp.Flasher, error) {
		return mock, nil
	})
	t.Cleanup(func() { session.SetFlasherFactory(esp.DefaultFlasherFactory) })

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{
		"port":    "/dev/ttyUSB0",
		"address": float64(0x3FF00000),
	}
	result, err := handleRegister(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.False(t, result.IsError)

	tc, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, tc.Text, "0xDEADBEEF")
}

func TestHandleRegisterReadPortManaged(t *testing.T) {
	setupTestPorts(t)
	setupTestFlasherFactory(t)
	setupFastWaitForPort(t)

	testMgr := serial.NewManager()
	testMgr.SetTestState(true, "/dev/ttyUSB0", 115200, nil)
	session.InsertPort("/dev/ttyUSB0", session.NewPortSession(testMgr, "/dev/ttyUSB0", 115200, session.ModeReader))

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{
		"port":    "/dev/ttyUSB0",
		"address": float64(0x3FF00000),
	}
	result, err := handleRegister(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, result)

	// Should NOT get "managed by serial_start" error — port is auto-stopped.
	if result.IsError {
		tc, ok := result.Content[0].(mcp.TextContent)
		require.True(t, ok)
		assert.NotContains(t, tc.Text, "managed by serial_start")
	}
}

func TestHandleRegisterWriteSuccess(t *testing.T) {
	setupTestPorts(t)
	setupTestFlasherFactory(t)

	mock := &mockFlasher{}
	session.SetFlasherFactory(func(port string, opts *espflasher.FlasherOptions) (esp.Flasher, error) {
		return mock, nil
	})
	t.Cleanup(func() { session.SetFlasherFactory(esp.DefaultFlasherFactory) })

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{
		"port":    "/dev/ttyUSB0",
		"address": float64(0x3FF00000),
		"value":   float64(0xABCD1234),
	}
	result, err := handleRegister(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.False(t, result.IsError)

	assert.Equal(t, uint32(0x3FF00000), mock.writeRegisterAddr)
	assert.Equal(t, uint32(0xABCD1234), mock.writeRegisterVal)

	tc, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, tc.Text, "0xABCD1234")
}

func TestHandleRegisterWritePortManaged(t *testing.T) {
	setupTestPorts(t)
	setupTestFlasherFactory(t)
	setupFastWaitForPort(t)

	testMgr := serial.NewManager()
	testMgr.SetTestState(true, "/dev/ttyUSB0", 115200, nil)
	session.InsertPort("/dev/ttyUSB0", session.NewPortSession(testMgr, "/dev/ttyUSB0", 115200, session.ModeReader))

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{
		"port":    "/dev/ttyUSB0",
		"address": float64(0x3FF00000),
		"value":   float64(0x12345678),
	}
	result, err := handleRegister(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, result)

	// Should NOT get "managed by serial_start" error — port is auto-stopped.
	if result.IsError {
		tc, ok := result.Content[0].(mcp.TextContent)
		require.True(t, ok)
		assert.NotContains(t, tc.Text, "managed by serial_start")
	}
}

func TestHandleRegisterReadMissingAddress(t *testing.T) {
	setupTestPorts(t)

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{
		"port": "/dev/ttyUSB0",
	}
	result, err := handleRegister(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, result.IsError)

	tc, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, tc.Text, "address")
}

func TestHandleRegisterReadError(t *testing.T) {
	setupTestPorts(t)
	setupTestFlasherFactory(t)

	mock := &mockFlasher{
		readRegisterErr: fmt.Errorf("read timeout"),
	}
	session.SetFlasherFactory(func(port string, opts *espflasher.FlasherOptions) (esp.Flasher, error) {
		return mock, nil
	})
	t.Cleanup(func() { session.SetFlasherFactory(esp.DefaultFlasherFactory) })

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{
		"port":    "/dev/ttyUSB0",
		"address": float64(0x3FF00000),
	}
	result, err := handleRegister(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, result.IsError)

	tc, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, tc.Text, "read timeout")
}

func TestHandleRegisterWriteMissingAddress(t *testing.T) {
	setupTestPorts(t)

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{
		"port":  "/dev/ttyUSB0",
		"value": float64(0x12345678),
	}
	result, err := handleRegister(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, result.IsError)

	tc, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, tc.Text, "address")
}

func TestHandleRegisterValueMustBeNumber(t *testing.T) {
	setupTestPorts(t)
	setupTestFlasherFactory(t)

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{
		"port":    "/dev/ttyUSB0",
		"address": float64(0x3FF00000),
		"value":   "not-a-number",
	}
	result, err := handleRegister(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, result.IsError)

	tc, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, tc.Text, "value")
}

func TestHandleRegisterWriteError(t *testing.T) {
	setupTestPorts(t)
	setupTestFlasherFactory(t)

	mock := &mockFlasher{
		writeRegisterErr: fmt.Errorf("write timeout"),
	}
	session.SetFlasherFactory(func(port string, opts *espflasher.FlasherOptions) (esp.Flasher, error) {
		return mock, nil
	})
	t.Cleanup(func() { session.SetFlasherFactory(esp.DefaultFlasherFactory) })

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{
		"port":    "/dev/ttyUSB0",
		"address": float64(0x3FF00000),
		"value":   float64(0x12345678),
	}
	result, err := handleRegister(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, result.IsError)

	tc, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, tc.Text, "write timeout")
}

func TestHandleResetSuccess(t *testing.T) {
	setupTestPorts(t)
	setupTestFlasherFactory(t)

	mockFlasher := &mockFlasher{}
	session.SetFlasherFactory(func(port string, opts *espflasher.FlasherOptions) (esp.Flasher, error) {
		return mockFlasher, nil
	})
	t.Cleanup(func() { session.SetFlasherFactory(esp.DefaultFlasherFactory) })

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
	assert.Contains(t, tc.Text, "success")
	assert.Contains(t, tc.Text, "device reset")
}

func TestHandleResetPortManaged(t *testing.T) {
	setupTestPorts(t)
	setupTestFlasherFactory(t)
	setupFastWaitForPort(t)

	testMgr := serial.NewManager()
	testMgr.SetTestState(true, "/dev/ttyUSB0", 115200, nil)
	session.InsertPort("/dev/ttyUSB0", session.NewPortSession(testMgr, "/dev/ttyUSB0", 115200, session.ModeReader))

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{
		"port": "/dev/ttyUSB0",
	}
	result, err := handleReset(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, result)

	// Should NOT get "managed by serial_start" error — port is auto-stopped.
	if result.IsError {
		tc, ok := result.Content[0].(mcp.TextContent)
		require.True(t, ok)
		assert.NotContains(t, tc.Text, "managed by serial_start")
	}
}

func TestHandleResetMissingPort(t *testing.T) {
	req := mcp.CallToolRequest{}
	result, err := handleReset(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, result.IsError)
}

func TestHandleResetError(t *testing.T) {
	setupTestPorts(t)
	setupTestFlasherFactory(t)

	session.SetFlasherFactory(func(port string, opts *espflasher.FlasherOptions) (esp.Flasher, error) {
		return nil, fmt.Errorf("connection failed")
	})
	t.Cleanup(func() { session.SetFlasherFactory(esp.DefaultFlasherFactory) })

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{
		"port": "/dev/ttyUSB0",
	}
	result, err := handleReset(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, result.IsError)

	tc, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, tc.Text, "connection failed")
}

func TestHandleESPReadFlashMD5Success(t *testing.T) {
	setupTestPorts(t)
	setupTestFlasherFactory(t)

	mockFlasher := &mockFlasher{
		flashMD5Val: "5d41402abc4b2a76b9719d911017c592",
	}
	session.SetFlasherFactory(func(port string, opts *espflasher.FlasherOptions) (esp.Flasher, error) {
		return mockFlasher, nil
	})
	t.Cleanup(func() { session.SetFlasherFactory(esp.DefaultFlasherFactory) })

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{
		"port":   "/dev/ttyUSB0",
		"offset": float64(0x1000),
		"size":   float64(0x1000),
		"md5":    true,
	}
	result, err := handleESPReadFlash(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.False(t, result.IsError)

	tc, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, tc.Text, "md5")
	assert.Contains(t, tc.Text, "offset")
	assert.Contains(t, tc.Text, "size")
}

func TestHandleESPReadFlashMD5PortManaged(t *testing.T) {
	setupTestPorts(t)
	setupTestFlasherFactory(t)
	setupFastWaitForPort(t)

	testMgr := serial.NewManager()
	testMgr.SetTestState(true, "/dev/ttyUSB0", 115200, nil)
	session.InsertPort("/dev/ttyUSB0", session.NewPortSession(testMgr, "/dev/ttyUSB0", 115200, session.ModeReader))

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{
		"port":   "/dev/ttyUSB0",
		"offset": float64(0x1000),
		"size":   float64(0x1000),
		"md5":    true,
	}
	result, err := handleESPReadFlash(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, result)

	// Should NOT get "managed by serial_start" error — port is auto-stopped.
	if result.IsError {
		tc, ok := result.Content[0].(mcp.TextContent)
		require.True(t, ok)
		assert.NotContains(t, tc.Text, "managed by serial_start")
	}
}

func TestHandleESPReadFlashMD5MissingOffset(t *testing.T) {
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{
		"port": "/dev/ttyUSB0",
		"size": float64(0x1000),
		"md5":  true,
	}
	result, err := handleESPReadFlash(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, result.IsError)
}

func TestHandleESPReadFlashSuccess(t *testing.T) {
	setupTestPorts(t)
	setupTestFlasherFactory(t)

	testData := []byte{0xAA, 0xBB, 0xCC, 0xDD}
	mockFlasher := &mockFlasher{
		readFlashVal: testData,
	}
	session.SetFlasherFactory(func(port string, opts *espflasher.FlasherOptions) (esp.Flasher, error) {
		return mockFlasher, nil
	})
	t.Cleanup(func() { session.SetFlasherFactory(esp.DefaultFlasherFactory) })

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{
		"port":   "/dev/ttyUSB0",
		"offset": float64(0x2000),
		"size":   float64(4),
	}
	result, err := handleESPReadFlash(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.False(t, result.IsError)

	tc, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, tc.Text, "data")
	assert.Contains(t, tc.Text, "offset")
	assert.Contains(t, tc.Text, "size")

	var respData map[string]interface{}
	err = json.Unmarshal([]byte(tc.Text), &respData)
	require.NoError(t, err)
	assert.Equal(t, "qrvM3Q==", respData["data"]) // base64 of [0xAA, 0xBB, 0xCC, 0xDD].
}

func TestHandleESPReadFlashPortManaged(t *testing.T) {
	setupTestPorts(t)
	setupTestFlasherFactory(t)
	setupFastWaitForPort(t)

	testMgr := serial.NewManager()
	testMgr.SetTestState(true, "/dev/ttyUSB0", 115200, nil)
	session.InsertPort("/dev/ttyUSB0", session.NewPortSession(testMgr, "/dev/ttyUSB0", 115200, session.ModeReader))

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{
		"port":   "/dev/ttyUSB0",
		"offset": float64(0x2000),
		"size":   float64(4),
	}
	result, err := handleESPReadFlash(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, result)

	// Should NOT get "managed by serial_start" error — port is auto-stopped.
	if result.IsError {
		tc, ok := result.Content[0].(mcp.TextContent)
		require.True(t, ok)
		assert.NotContains(t, tc.Text, "managed by serial_start")
	}
}

func TestHandleESPReadFlashMissingSize(t *testing.T) {
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{
		"port":   "/dev/ttyUSB0",
		"offset": float64(0x2000),
	}
	result, err := handleESPReadFlash(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, result.IsError)
}

func TestHandleESPReadFlashMissingPort(t *testing.T) {
	setupTestPorts(t)

	req := mcp.CallToolRequest{}
	result, err := handleESPReadFlash(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, result.IsError)

	tc, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, tc.Text, "port")
}

func TestHandleESPReadFlashError(t *testing.T) {
	setupTestPorts(t)
	setupTestFlasherFactory(t)

	mock := &mockFlasher{
		readFlashErr: fmt.Errorf("flash read failed"),
	}
	session.SetFlasherFactory(func(port string, opts *espflasher.FlasherOptions) (esp.Flasher, error) {
		return mock, nil
	})
	t.Cleanup(func() { session.SetFlasherFactory(esp.DefaultFlasherFactory) })

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{
		"port":   "/dev/ttyUSB0",
		"offset": float64(0x1000),
		"size":   float64(0x1000),
	}
	result, err := handleESPReadFlash(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, result.IsError)

	tc, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, tc.Text, "flash read failed")
}

// SyncError tests for specific ESP handlers.

func TestHandleFlashSyncError(t *testing.T) {
	setupTestPorts(t)
	setupTestFlasherFactory(t)
	session.SetFlasherFactory(func(port string, opts *espflasher.FlasherOptions) (esp.Flasher, error) {
		return nil, &espflasher.SyncError{Attempts: 10}
	})
	t.Cleanup(func() { session.SetFlasherFactory(esp.DefaultFlasherFactory) })

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{
		"port":   "/dev/ttyUSB0",
		"images": []interface{}{map[string]interface{}{"path": "/tmp/fw.bin", "offset": float64(0)}},
	}
	result, err := handleFlash(context.Background(), req)
	require.NoError(t, err)
	tc, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, tc.Text, "not in download mode")
}

func TestHandleEraseSyncError(t *testing.T) {
	setupTestPorts(t)
	setupTestFlasherFactory(t)
	session.SetFlasherFactory(func(port string, opts *espflasher.FlasherOptions) (esp.Flasher, error) {
		return nil, &espflasher.SyncError{Attempts: 10}
	})
	t.Cleanup(func() { session.SetFlasherFactory(esp.DefaultFlasherFactory) })

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{
		"port": "/dev/ttyUSB0",
	}
	result, err := handleErase(context.Background(), req)
	require.NoError(t, err)
	tc, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, tc.Text, "not in download mode")
}

func TestHandleESPInfoSyncError(t *testing.T) {
	setupTestPorts(t)
	setupTestFlasherFactory(t)
	session.SetFlasherFactory(func(port string, opts *espflasher.FlasherOptions) (esp.Flasher, error) {
		return nil, &espflasher.SyncError{Attempts: 10}
	})
	t.Cleanup(func() { session.SetFlasherFactory(esp.DefaultFlasherFactory) })

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{
		"port": "/dev/ttyUSB0",
	}
	result, err := handleESPInfo(context.Background(), req)
	require.NoError(t, err)
	tc, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, tc.Text, "not in download mode")
}

func TestHandleRegisterReadSyncError(t *testing.T) {
	setupTestPorts(t)
	setupTestFlasherFactory(t)
	session.SetFlasherFactory(func(port string, opts *espflasher.FlasherOptions) (esp.Flasher, error) {
		return nil, &espflasher.SyncError{Attempts: 10}
	})
	t.Cleanup(func() { session.SetFlasherFactory(esp.DefaultFlasherFactory) })

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{
		"port":    "/dev/ttyUSB0",
		"address": float64(0x3FF00000),
	}
	result, err := handleRegister(context.Background(), req)
	require.NoError(t, err)
	tc, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, tc.Text, "not in download mode")
}

func TestHandleRegisterWriteSyncError(t *testing.T) {
	setupTestPorts(t)
	setupTestFlasherFactory(t)
	session.SetFlasherFactory(func(port string, opts *espflasher.FlasherOptions) (esp.Flasher, error) {
		return nil, &espflasher.SyncError{Attempts: 10}
	})
	t.Cleanup(func() { session.SetFlasherFactory(esp.DefaultFlasherFactory) })

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{
		"port":    "/dev/ttyUSB0",
		"address": float64(0x3FF00000),
		"value":   float64(0x1234),
	}
	result, err := handleRegister(context.Background(), req)
	require.NoError(t, err)
	tc, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, tc.Text, "not in download mode")
}

func TestHandleResetSyncError(t *testing.T) {
	setupTestPorts(t)
	setupTestFlasherFactory(t)
	session.SetFlasherFactory(func(port string, opts *espflasher.FlasherOptions) (esp.Flasher, error) {
		return nil, &espflasher.SyncError{Attempts: 10}
	})
	t.Cleanup(func() { session.SetFlasherFactory(esp.DefaultFlasherFactory) })

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{
		"port": "/dev/ttyUSB0",
	}
	result, err := handleReset(context.Background(), req)
	require.NoError(t, err)
	tc, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, tc.Text, "not in download mode")
}

func TestHandleESPReadFlashMD5SyncError(t *testing.T) {
	setupTestPorts(t)
	setupTestFlasherFactory(t)
	session.SetFlasherFactory(func(port string, opts *espflasher.FlasherOptions) (esp.Flasher, error) {
		return nil, &espflasher.SyncError{Attempts: 10}
	})
	t.Cleanup(func() { session.SetFlasherFactory(esp.DefaultFlasherFactory) })

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{
		"port":   "/dev/ttyUSB0",
		"offset": float64(0),
		"size":   float64(0x1000),
		"md5":    true,
	}
	result, err := handleESPReadFlash(context.Background(), req)
	require.NoError(t, err)
	tc, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, tc.Text, "not in download mode")
}

// Security info tests.

func TestHandleESPInfoSecuritySuccess(t *testing.T) {
	setupTestPorts(t)
	setupTestFlasherFactory(t)

	chipID := uint32(0x12345678)
	apiVer := uint32(0x00000001)
	mockFlasher := &mockFlasher{
		getSecurityInfoVal: &espflasher.SecurityInfo{
			Flags:      0x12345678,
			ChipID:     &chipID,
			APIVersion: &apiVer,
		},
	}
	session.SetFlasherFactory(func(port string, opts *espflasher.FlasherOptions) (esp.Flasher, error) {
		return mockFlasher, nil
	})
	t.Cleanup(func() { session.SetFlasherFactory(esp.DefaultFlasherFactory) })

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{
		"port":    "/dev/ttyUSB0",
		"include": "security",
	}
	result, err := handleESPInfo(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.False(t, result.IsError)

	tc, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, tc.Text, "security")
	assert.Contains(t, tc.Text, "flags")
	assert.Contains(t, tc.Text, "chip_id")
	assert.Contains(t, tc.Text, "api_version")
}

func TestHandleESPInfoSecurityPortManaged(t *testing.T) {
	setupTestPorts(t)
	setupTestFlasherFactory(t)
	setupFastWaitForPort(t)

	testMgr := serial.NewManager()
	testMgr.SetTestState(true, "/dev/ttyUSB0", 115200, nil)
	session.InsertPort("/dev/ttyUSB0", session.NewPortSession(testMgr, "/dev/ttyUSB0", 115200, session.ModeReader))

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{
		"port":    "/dev/ttyUSB0",
		"include": "security",
	}
	result, err := handleESPInfo(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, result)

	// Should NOT get "managed by serial_start" error — port is auto-stopped.
	if result.IsError {
		tc, ok := result.Content[0].(mcp.TextContent)
		require.True(t, ok)
		assert.NotContains(t, tc.Text, "managed by serial_start")
	}
}

func TestHandleESPInfoSecurityMissingPort(t *testing.T) {
	setupTestPorts(t)

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{
		"include": "security",
	}
	result, err := handleESPInfo(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, result.IsError)

	tc, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, tc.Text, "port")
}

func TestHandleESPInfoSecurityError(t *testing.T) {
	setupTestPorts(t)
	setupTestFlasherFactory(t)

	mock := &mockFlasher{
		getSecurityInfoErr: fmt.Errorf("security read failed"),
	}
	session.SetFlasherFactory(func(port string, opts *espflasher.FlasherOptions) (esp.Flasher, error) {
		return mock, nil
	})
	t.Cleanup(func() { session.SetFlasherFactory(esp.DefaultFlasherFactory) })

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{
		"port":    "/dev/ttyUSB0",
		"include": "security",
	}
	result, err := handleESPInfo(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, result.IsError)

	tc, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, tc.Text, "security read failed")
}

func TestHandleESPInfoSecuritySyncError(t *testing.T) {
	setupTestPorts(t)
	setupTestFlasherFactory(t)
	session.SetFlasherFactory(func(port string, opts *espflasher.FlasherOptions) (esp.Flasher, error) {
		return nil, &espflasher.SyncError{Attempts: 10}
	})
	t.Cleanup(func() { session.SetFlasherFactory(esp.DefaultFlasherFactory) })

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{
		"port":    "/dev/ttyUSB0",
		"include": "security",
	}
	result, err := handleESPInfo(context.Background(), req)
	require.NoError(t, err)
	tc, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, tc.Text, "not in download mode")
}

func TestHandleESPReadFlashMD5MissingPort(t *testing.T) {
	setupTestPorts(t)

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{
		"offset": float64(0x1000),
		"size":   float64(0x1000),
		"md5":    true,
	}
	result, err := handleESPReadFlash(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, result.IsError)

	tc, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, tc.Text, "port")
}

func TestHandleESPReadFlashMD5Error(t *testing.T) {
	setupTestPorts(t)
	setupTestFlasherFactory(t)

	mock := &mockFlasher{
		flashMD5Err: fmt.Errorf("md5 computation failed"),
	}
	session.SetFlasherFactory(func(port string, opts *espflasher.FlasherOptions) (esp.Flasher, error) {
		return mock, nil
	})
	t.Cleanup(func() { session.SetFlasherFactory(esp.DefaultFlasherFactory) })

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{
		"port":   "/dev/ttyUSB0",
		"offset": float64(0x1000),
		"size":   float64(0x1000),
		"md5":    true,
	}
	result, err := handleESPReadFlash(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, result.IsError)

	tc, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, tc.Text, "md5 computation failed")
}

func TestHandleEraseWithResetMode(t *testing.T) {
	setupTestPorts(t)
	setupTestFlasherFactory(t)
	mockF := &mockFlasher{}
	session.SetFlasherFactory(func(port string, opts *espflasher.FlasherOptions) (esp.Flasher, error) {
		return mockF, nil
	})
	t.Cleanup(func() { session.SetFlasherFactory(esp.DefaultFlasherFactory) })

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{
		"port":       "/dev/ttyUSB0",
		"reset_mode": "auto",
	}
	result, err := handleErase(context.Background(), req)
	require.NoError(t, err)
	require.False(t, result.IsError)
}

// Unit tests for parse functions.

func TestHandleSyncError(t *testing.T) {
	// Test with SyncError.
	syncErr := &espflasher.SyncError{Attempts: 10}
	result := handleSyncError(syncErr)
	require.NotNil(t, result)
	require.True(t, result.IsError)
	tc, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, tc.Text, "10 attempts")

	// Test with regular error.
	result = handleSyncError(fmt.Errorf("some other error"))
	assert.Nil(t, result)

	// Test with nil.
	result = handleSyncError(nil)
	assert.Nil(t, result)
}
