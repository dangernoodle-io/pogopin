package mcpserver

import (
	"context"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
)

// readToolNames is the exact set of RiskRead tools across both tiers (BR-72).
var readToolNames = []string{
	"serial_list", "serial_read", "serial_status",
	"esp_info", "esp_read_flash", "esp_read_nvs", "esp_gpio_read",
	"decode_backtrace",
}

// writeOrDestructiveToolNames is every non-READ tool across both tiers.
var writeOrDestructiveToolNames = []string{
	"serial_start", "serial_stop", "serial_restart", "serial_write",
	"esp_register", "esp_reset", "esp_nvs_set", "esp_nvs_delete",
	"esp_erase", "esp_flash", "esp_write_nvs", "flash_external",
	"esp_gpio_set", "esp_gpio_sweep",
}

func TestDiagnosticModeRegistersOnlyReadTools(t *testing.T) {
	s := newTestServer(t)
	SetDiagnosticMode(true)
	t.Cleanup(func() { SetDiagnosticMode(false) })

	registerTools(s)
	registerHardwareTools(s)

	names := toolNames(s)
	assert.Len(t, names, len(readToolNames), "expected exactly the READ tool set")
	for _, n := range readToolNames {
		assert.True(t, names[n], "expected %s to be registered in diagnostic mode", n)
	}
	for _, n := range writeOrDestructiveToolNames {
		assert.False(t, names[n], "expected %s NOT to be registered in diagnostic mode", n)
	}
}

func TestNormalModeStillRegistersAllTools(t *testing.T) {
	s := newTestServer(t)
	SetDiagnosticMode(false)

	registerTools(s)
	registerHardwareTools(s)

	names := toolNames(s)
	assert.Len(t, names, len(readToolNames)+len(writeOrDestructiveToolNames),
		"expected all 22 tools registered in normal mode")
	for _, n := range readToolNames {
		assert.True(t, names[n], "expected %s to be registered", n)
	}
	for _, n := range writeOrDestructiveToolNames {
		assert.True(t, names[n], "expected %s to be registered", n)
	}
}

func TestDiagnosticModeSerialListStillUnlocksHardwareTier(t *testing.T) {
	setupTestPorts(t)
	setupTestListPorts(t)
	s := newTestServer(t)
	SetDiagnosticMode(true)
	t.Cleanup(func() { SetDiagnosticMode(false) })
	registerTools(s)

	names := toolNames(s)
	assert.True(t, names["serial_list"], "serial_list (READ) must register even in diagnostic mode")
	assert.False(t, names["esp_info"], "hardware tier must not be unlocked until serial_list runs")

	unlockHardwareTier()

	names = toolNames(s)
	assert.True(t, names["esp_info"], "expected esp_info (READ) registered after unlock")
	assert.False(t, names["esp_flash"], "expected esp_flash (DESTRUCTIVE) NOT registered after unlock in diagnostic mode")
}

func TestDiagnosticModeExcludesUnclassifiedTools(t *testing.T) {
	s := newTestServer(t)
	SetDiagnosticMode(true)
	t.Cleanup(func() { SetDiagnosticMode(false) })

	// Attempt to register an unclassified tool in diagnostic mode.
	unknownTool := mcp.NewTool("definitely-not-registered")
	addTool(s, unknownTool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultText(""), nil
	})

	// The unknown tool should NOT be in the registered tools.
	names := toolNames(s)
	assert.False(t, names["definitely-not-registered"], "expected unclassified tool to be excluded in diagnostic mode")
}
