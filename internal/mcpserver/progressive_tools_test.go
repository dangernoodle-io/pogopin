package mcpserver

import (
	"context"
	"testing"

	"dangernoodle.io/pogopin/internal/serial"
	"dangernoodle.io/pogopin/internal/session"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestServer returns a fresh MCPServer with the active-server hook wired
// and the hardware-tier gate reset. Cleanup restores package state.
func newTestServer(t *testing.T) *server.MCPServer {
	t.Helper()
	resetHardwareTier()
	s := server.NewMCPServer("pogopin-test", "0.0.0",
		server.WithToolCapabilities(true),
	)
	setActiveServer(s)
	t.Cleanup(resetHardwareTier)
	return s
}

func toolNames(s *server.MCPServer) map[string]bool {
	names := map[string]bool{}
	for name := range s.ListTools() {
		names[name] = true
	}
	return names
}

func TestRegisterToolsOnlyRegistersCoreTier(t *testing.T) {
	s := newTestServer(t)
	registerTools(s)

	names := toolNames(s)
	// Core tier: 6 serial_* + decode_backtrace
	assert.True(t, names["serial_list"])
	assert.True(t, names["serial_start"])
	assert.True(t, names["serial_read"])
	assert.True(t, names["serial_write"])
	assert.True(t, names["serial_stop"])
	assert.True(t, names["serial_status"])
	assert.True(t, names["decode_backtrace"])
	// Hardware tier: not registered until unlocked.
	assert.False(t, names["esp_flash"])
	assert.False(t, names["esp_info"])
	assert.False(t, names["flash_external"])
}

func TestRegisterHardwareToolsRegistersFullTier(t *testing.T) {
	s := newTestServer(t)
	registerTools(s)
	registerHardwareTools(s)

	names := toolNames(s)
	for _, n := range []string{
		"esp_flash", "esp_erase", "esp_info", "esp_register", "esp_reset",
		"esp_read_flash", "esp_read_nvs", "esp_write_nvs", "esp_nvs_set",
		"esp_nvs_delete", "flash_external",
	} {
		assert.True(t, names[n], "expected %s to be registered", n)
	}
}

func TestRegisterHardwareToolsIsIdempotent(t *testing.T) {
	s := newTestServer(t)
	registerTools(s)
	registerHardwareTools(s)
	// Second call must not panic (AddTool would panic on duplicate task-tool name
	// collision, but duplicate regular-tool registration is a silent overwrite —
	// the sync.Once guard prevents even that).
	assert.NotPanics(t, func() { registerHardwareTools(s) })
}

func TestSerialListUnlocksHardwareTier(t *testing.T) {
	setupTestPorts(t)
	setupTestListPorts(t)
	s := newTestServer(t)
	registerTools(s)
	session.SetListPortsFn(func(usbOnly bool) ([]serial.PortInfo, error) {
		return nil, nil
	})

	require.False(t, toolNames(s)["esp_flash"], "precondition: esp_flash not registered")

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{}
	_, err := handleSerialList(context.Background(), req)
	require.NoError(t, err)

	assert.True(t, toolNames(s)["esp_flash"])
	assert.True(t, toolNames(s)["flash_external"])
}

func TestSerialStartUnlocksHardwareTier(t *testing.T) {
	setupTestPorts(t)
	setupTestFlasherFactory(t)
	setupTestManagersFunc(t)
	setupFastWaitForPort(t)
	setupTestIsUSBPort(t)
	s := newTestServer(t)
	registerTools(s)

	require.False(t, toolNames(s)["esp_flash"], "precondition: esp_flash not registered")

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{
		"port": "/dev/cu.test",
	}
	_, _ = handleSerialStart(context.Background(), req)

	// Even if the call itself errors (no real port), the unlock must run.
	assert.True(t, toolNames(s)["esp_flash"])
}

func TestUnlockHardwareTierNoopWhenNoActiveServer(t *testing.T) {
	resetHardwareTier()
	assert.NotPanics(t, unlockHardwareTier)
}
