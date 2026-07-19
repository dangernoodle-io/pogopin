package mcpapp

import (
	"context"
	"testing"

	"github.com/dangernoodle-io/shesha"
	"github.com/dangernoodle-io/shesha/testkit"
	"github.com/stretchr/testify/require"

	"dangernoodle.io/pogopin/internal/serial"
	"dangernoodle.io/pogopin/internal/session"
)

// readOnlyToolNames is the exact set of ReadOnly-risk tools across both
// tiers (BR-72), mirroring internal/mcpserver/diagnostic_test.go's
// readToolNames.
var readOnlyToolNames = []string{
	"decode_backtrace",
	"serial_list", "serial_read", "serial_status",
	"esp_info", "esp_read_flash", "esp_read_nvs", "esp_gpio_read",
}

// buildReadOnlyApp composes the app the same way BuildApp does, then
// applies shesha.ReadOnlyMode() before anything finalizes registration
// (App.Gate is pre-start-only — see its doc comment) — the shesha
// equivalent of `pogo server --diagnostic`/POGOPIN_DIAGNOSTIC=1.
func buildReadOnlyApp(t *testing.T) *shesha.App {
	t.Helper()
	app, err := BuildApp()
	require.NoError(t, err)
	require.NoError(t, app.Gate(shesha.ReadOnlyMode()))
	return app
}

// TestReadOnlyModeRegistersOnlyReadToolsAtStartup proves a ReadOnlyMode-gated
// app's startup (core-tier) surface is exactly the ReadOnly-risk subset.
func TestReadOnlyModeRegistersOnlyReadToolsAtStartup(t *testing.T) {
	app := buildReadOnlyApp(t)
	h := testkit.New(t, app)

	testkit.AssertToolSet(t, h, "decode_backtrace", "serial_list", "serial_read", "serial_status")
}

// TestReadOnlyModeSerialListUnlocksOnlyReadHardwareTools proves the
// hardware-tier unlock in a ReadOnlyMode-gated app registers only the
// ReadOnly esp_* tools (esp_info, esp_read_flash, esp_read_nvs,
// esp_gpio_read) — every write/destructive esp_* tool and flash_external
// stay hard-blocked, mirroring BR-72's diagnostic-profile enforcement.
func TestReadOnlyModeSerialListUnlocksOnlyReadHardwareTools(t *testing.T) {
	setupTestPorts(t)
	origListFn := session.SetListPortsFn(func() ([]serial.PortInfo, error) { return nil, nil })
	t.Cleanup(func() { session.SetListPortsFn(origListFn) })

	app := buildReadOnlyApp(t)
	h := testkit.New(t, app)

	res, err := h.CallTool(context.Background(), "serial_list", map[string]any{})
	require.NoError(t, err)
	require.False(t, res.IsError)

	testkit.AssertToolSet(t, h, readOnlyToolNames...)
}
