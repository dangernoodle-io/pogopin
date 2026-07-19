package mcpapp

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/dangernoodle-io/shesha/testkit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"dangernoodle.io/pogopin/internal/mockhw"
)

// TestMockGPIOInProcess drives the real MCP tool handlers in-process
// against internal/mockhw's virtual ESP32-S2 chip -- through the real
// session -> espflasher path, no subprocess and no hardware -- via
// testkit.Harness against a real BuildApp() App. MC-12 port of
// internal/mcpserver/mock_integration_test.go's test of the same name: the
// mark3labs-based server's package-global activeServer/hardwareTierOnce
// reset is gone (shesha.App has no such singleton), so this needs no
// newTestServer/registerTools equivalent -- BuildApp() alone suffices.
//
// Gated on ACC_POGOPIN (mirrors TF_ACC) so it skips in a plain
// `go test ./...` run; `make mock-mcp` / `make acc` set the env var
// themselves. mockhw.Install mutates session/serial package globals
// (serial-open, list-ports, is-USB-port seams); restored via t.Cleanup.
func TestMockGPIOInProcess(t *testing.T) {
	if os.Getenv("ACC_POGOPIN") == "" {
		t.Skip("ACC_POGOPIN not set — skipping hardware-free virtual-chip integration test")
	}

	t.Cleanup(mockhw.Install())
	setupTestPorts(t)

	app, err := BuildApp()
	require.NoError(t, err)
	h := testkit.New(t, app)

	t.Run("serial_list unlocks hardware tier and returns the mock port", func(t *testing.T) {
		result, err := h.CallTool(context.Background(), "serial_list", map[string]any{})
		require.NoError(t, err)
		require.False(t, result.IsError, "serial_list returned error")

		var ports []struct {
			Name string `json:"name"`
		}
		require.NoError(t, json.Unmarshal([]byte(testkit.ResultText(result)), &ports))
		found := false
		for _, p := range ports {
			if p.Name == mockhw.MockPortName {
				found = true
			}
		}
		assert.True(t, found, "serial_list result missing mock port: %v", ports)

		testkit.AssertToolSet(t, h,
			"decode_backtrace",
			"serial_list", "serial_start", "serial_read", "serial_write",
			"serial_stop", "serial_restart", "serial_status",
			"esp_flash", "esp_erase", "esp_info", "esp_register", "esp_reset",
			"esp_read_flash", "esp_read_nvs", "esp_write_nvs", "esp_nvs_set", "esp_nvs_delete",
			"esp_gpio_read", "esp_gpio_set", "esp_gpio_sweep", "flash_external",
		)
	})

	t.Run("esp_gpio_set then esp_gpio_read on pin 15 round-trips", func(t *testing.T) {
		setRes, err := h.CallTool(context.Background(), "esp_gpio_set", map[string]any{
			"port":       mockhw.MockPortName,
			"pin":        float64(15),
			"level":      true,
			"reset_mode": "no_reset",
		})
		require.NoError(t, err)
		require.False(t, setRes.IsError, "esp_gpio_set returned error")

		readRes, err := h.CallTool(context.Background(), "esp_gpio_read", map[string]any{
			"port":       mockhw.MockPortName,
			"pin":        float64(15),
			"reset_mode": "no_reset",
		})
		require.NoError(t, err)
		require.False(t, readRes.IsError, "esp_gpio_read returned error")

		var result struct {
			Pin   int  `json:"pin"`
			Level bool `json:"level"`
		}
		require.NoError(t, json.Unmarshal([]byte(testkit.ResultText(readRes)), &result))
		assert.Equal(t, 15, result.Pin)
		assert.True(t, result.Level)
	})

	t.Run("esp_gpio_set on reserved GPIO0 is refused", func(t *testing.T) {
		res, err := h.CallTool(context.Background(), "esp_gpio_set", map[string]any{
			"port":       mockhw.MockPortName,
			"pin":        float64(0),
			"level":      true,
			"reset_mode": "no_reset",
		})
		require.NoError(t, err)
		require.True(t, res.IsError, "expected reserved-pin refusal, got success")
		assert.Contains(t, testkit.ResultText(res), "reserved")
	})
}

// TestMockSerialMonitorInProcess drives the real serial_start/read/write/
// stop handlers -> session -> serial.Manager -> internal/mockhw's
// virtualMonitorPort, in-process (no subprocess, no hardware), via
// testkit.Harness against a real BuildApp() App. Companion to
// TestMockGPIOInProcess for the monitor path, as opposed to the
// ROM-loader/flasher path that one exercises.
//
// Gated on ACC_POGOPIN; see TestMockGPIOInProcess's doc comment.
func TestMockSerialMonitorInProcess(t *testing.T) {
	if os.Getenv("ACC_POGOPIN") == "" {
		t.Skip("ACC_POGOPIN not set — skipping hardware-free virtual-chip integration test")
	}

	t.Cleanup(mockhw.Install())
	setupTestPorts(t)

	app, err := BuildApp()
	require.NoError(t, err)
	h := testkit.New(t, app)

	port := mockhw.MockPortName

	// serialReadTextContaining polls serial_read (handleSerialRead runs
	// against the live ring buffer, which the manager's readLoop goroutine
	// fills asynchronously) until want appears or the timeout elapses,
	// guarding against the inherent goroutine-scheduling race between
	// starting/writing the port and the readLoop draining
	// virtualMonitorPort's outbound queue.
	serialReadTextContaining := func(t *testing.T, want string) string {
		t.Helper()
		var text string
		require.Eventually(t, func() bool {
			readRes, err := h.CallTool(context.Background(), "serial_read", map[string]any{"port": port})
			if err != nil || readRes == nil || readRes.IsError {
				return false
			}
			text = testkit.ResultText(readRes)
			return strings.Contains(text, want)
		}, 2*time.Second, 10*time.Millisecond, "serial_read never observed %q", want)
		return text
	}

	t.Run("serial_start_returns_boot_banner_on_read", func(t *testing.T) {
		startRes, err := h.CallTool(context.Background(), "serial_start", map[string]any{"port": port})
		require.NoError(t, err)
		require.False(t, startRes.IsError, "serial_start returned error")

		serialReadTextContaining(t, "mock-esp32: virtual chip ready")
	})

	t.Run("serial_write_loopback_captured_exact", func(t *testing.T) {
		writeRes, err := h.CallTool(context.Background(), "serial_write", map[string]any{
			"port": port, "data": "PING-mock-integration",
		})
		require.NoError(t, err)
		require.False(t, writeRes.IsError, "serial_write returned error")

		serialReadTextContaining(t, "PING-mock-integration")
	})

	t.Run("serial_stop_then_status_reports_not_open", func(t *testing.T) {
		stopRes, err := h.CallTool(context.Background(), "serial_stop", map[string]any{"port": port})
		require.NoError(t, err)
		require.False(t, stopRes.IsError, "serial_stop returned error")

		// serial_stop tears the port session down entirely
		// (session.StopSession removes it from the ports map), so a
		// subsequent serial_status by name errors rather than reporting
		// running=false -- there's no session left to report status for.
		statusRes, err := h.CallTool(context.Background(), "serial_status", map[string]any{"port": port})
		require.NoError(t, err)
		assert.True(t, statusRes.IsError, "serial_status expected an error after serial_stop, got success")
		assert.Contains(t, testkit.ResultText(statusRes), "no serial port open")
	})
}
