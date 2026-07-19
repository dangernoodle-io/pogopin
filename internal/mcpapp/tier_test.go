package mcpapp

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
)

// setupTestPorts resets session's ports map for the duration of the test,
// mirroring internal/mcpserver/helpers_test.go's fixture of the same name.
func setupTestPorts(t *testing.T) {
	t.Helper()
	orig := session.SetPorts(map[string]*session.PortSession{})
	t.Cleanup(func() {
		session.CleanupAllSessions()
		session.SetPorts(orig)
	})
}

// hardwareToolNames is the full 14-tool hardware tier (13 esp_* +
// flash_external) that appears once the group is unlocked.
var hardwareToolNames = []string{
	"esp_flash", "esp_erase", "esp_info", "esp_register", "esp_reset",
	"esp_read_flash", "esp_read_nvs", "esp_write_nvs", "esp_nvs_set",
	"esp_nvs_delete", "esp_gpio_read", "esp_gpio_set", "esp_gpio_sweep",
	"flash_external",
}

// fullToolSet is the core tier plus the unlocked hardware tier.
func fullToolSet() []string {
	names := []string{
		"decode_backtrace",
		"serial_list", "serial_start", "serial_read", "serial_write",
		"serial_stop", "serial_restart", "serial_status",
	}
	return append(names, hardwareToolNames...)
}

// TestSerialListUnlocksHardwareTierWithoutError proves calling serial_list
// lifts the hardware-tier lock without error: all 13 esp_* tools and
// flash_external appear in tools/list, and the unlock fires
// notifications/tools/list_changed (esp/flash are no longer empty stubs as
// of this commit, so the group has real pending tools to register).
func TestSerialListUnlocksHardwareTierWithoutError(t *testing.T) {
	setupTestPorts(t)
	origListFn := session.SetListPortsFn(func() ([]serial.PortInfo, error) { return nil, nil })
	t.Cleanup(func() { session.SetListPortsFn(origListFn) })

	app, err := BuildApp()
	require.NoError(t, err)
	h := testkit.New(t, app)

	// Before any hardware-workflow signal, tools/list is core-tier only.
	testkit.AssertToolSet(t, h,
		"decode_backtrace",
		"serial_list", "serial_start", "serial_read", "serial_write",
		"serial_stop", "serial_restart", "serial_status",
	)

	res, err := h.CallTool(context.Background(), "serial_list", map[string]any{})
	require.NoError(t, err)
	require.False(t, res.IsError)

	// The hardware tier is now populated: all 14 tools appear.
	testkit.AssertToolSet(t, h, fullToolSet()...)

	// list_changed fires now that real tools were added to the live
	// server (unlike the empty-stub commit this test previously asserted
	// against).
	assert.True(t, h.WaitForToolListChanged(2*time.Second))
}

// TestSerialStartUnlocksHardwareTierWithoutError mirrors
// TestSerialListUnlocksHardwareTierWithoutError for serial_start: the
// unlock must run even when the underlying serial_start call itself
// succeeds or fails, and the full hardware tier appears.
func TestSerialStartUnlocksHardwareTierWithoutError(t *testing.T) {
	setupTestPorts(t)

	origMgrFn := session.SetNewManagerFunc(func(bufSize int) *serial.Manager {
		m := serial.NewManagerWithBufferSize(bufSize)
		m.OpenFunc = func(name string, mode *goSerial.Mode) (goSerial.Port, error) {
			return nil, nil
		}
		return m
	})
	t.Cleanup(func() { session.SetNewManagerFunc(origMgrFn) })

	app, err := BuildApp()
	require.NoError(t, err)
	h := testkit.New(t, app)

	_, err = h.CallTool(context.Background(), "serial_start", map[string]any{"port": "/dev/cu.test"})
	require.NoError(t, err)

	// The hardware tier is now populated: all 14 tools appear.
	testkit.AssertToolSet(t, h, fullToolSet()...)

	assert.True(t, h.WaitForToolListChanged(2*time.Second))
}

// TestUnlockHardwareTierIdempotent proves repeated unlock calls (as
// serial_list/serial_start/serial_restart each trigger on every
// invocation) never error or panic.
func TestUnlockHardwareTierIdempotent(t *testing.T) {
	setupTestPorts(t)
	origListFn := session.SetListPortsFn(func() ([]serial.PortInfo, error) { return nil, nil })
	t.Cleanup(func() { session.SetListPortsFn(origListFn) })

	app, err := BuildApp()
	require.NoError(t, err)
	h := testkit.New(t, app)

	assert.NotPanics(t, func() {
		for i := 0; i < 3; i++ {
			res, err := h.CallTool(context.Background(), "serial_list", map[string]any{})
			require.NoError(t, err)
			require.False(t, res.IsError)
		}
	})
}
