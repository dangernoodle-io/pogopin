package mcpserver

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"dangernoodle.io/pogopin/internal/mockhw"
)

// TestMockGPIOInProcess drives the real MCP tool handlers in-process
// against internal/mockhw's virtual ESP32-S2 chip — through the real
// session -> espflasher path, no subprocess and no hardware. Higher
// fidelity than the mockFlasher-based handler unit tests elsewhere in this
// package (which stay unchanged): here the actual SLIP/ROM-loader protocol
// runs, just against a virtual port instead of a real one.
//
// Gated on ACC_POGOPIN (mirrors TF_ACC) so it skips in a plain
// `go test ./...` run; `make mcp-mock` / `make acc` set the env var
// themselves. mockhw.Install mutates session/serial package globals
// (serial-open, list-ports, is-USB-port seams) and this test also mutates
// the mcpserver package's activeServer/hardwareTierOnce via newTestServer —
// both save/restore via t.Cleanup, but this must still not run in parallel
// with other mcpserver tests that assume production openers or a fresh
// hardware-tier gate.
func TestMockGPIOInProcess(t *testing.T) {
	if os.Getenv("ACC_POGOPIN") == "" {
		t.Skip("ACC_POGOPIN not set — skipping hardware-free virtual-chip integration test")
	}

	t.Cleanup(mockhw.Install())
	setupTestPorts(t)
	setupTestFlasherFactory(t)
	setupTestManagersFunc(t)

	s := newTestServer(t)
	registerTools(s)

	t.Run("serial_list unlocks hardware tier and returns the mock port", func(t *testing.T) {
		result, err := handleSerialList(context.Background(), mcp.CallToolRequest{})
		require.NoError(t, err)
		require.NotNil(t, result)
		require.False(t, result.IsError, "serial_list returned error")

		tc, ok := result.Content[0].(mcp.TextContent)
		require.True(t, ok)

		var ports []struct {
			Name string `json:"name"`
		}
		require.NoError(t, json.Unmarshal([]byte(tc.Text), &ports))
		found := false
		for _, p := range ports {
			if p.Name == mockhw.MockPortName {
				found = true
			}
		}
		assert.True(t, found, "serial_list result missing mock port: %v", ports)

		names := toolNames(s)
		assert.True(t, names["esp_gpio_read"], "hardware tier not unlocked after serial_list")
	})

	t.Run("esp_gpio_set then esp_gpio_read on pin 15 round-trips", func(t *testing.T) {
		setReq := mcp.CallToolRequest{}
		setReq.Params.Arguments = map[string]interface{}{
			"port":       mockhw.MockPortName,
			"pin":        float64(15),
			"level":      true,
			"reset_mode": "no_reset",
		}
		setRes, err := handleGPIOSet(context.Background(), setReq)
		require.NoError(t, err)
		require.NotNil(t, setRes)
		require.False(t, setRes.IsError, "esp_gpio_set returned error")

		readReq := mcp.CallToolRequest{}
		readReq.Params.Arguments = map[string]interface{}{
			"port":       mockhw.MockPortName,
			"pin":        float64(15),
			"reset_mode": "no_reset",
		}
		readRes, err := handleGPIORead(context.Background(), readReq)
		require.NoError(t, err)
		require.NotNil(t, readRes)
		require.False(t, readRes.IsError, "esp_gpio_read returned error")

		tc, ok := readRes.Content[0].(mcp.TextContent)
		require.True(t, ok)
		var result struct {
			Pin   int  `json:"pin"`
			Level bool `json:"level"`
		}
		require.NoError(t, json.Unmarshal([]byte(tc.Text), &result))
		assert.Equal(t, 15, result.Pin)
		assert.True(t, result.Level)
	})

	t.Run("esp_gpio_set on reserved GPIO0 is refused", func(t *testing.T) {
		req := mcp.CallToolRequest{}
		req.Params.Arguments = map[string]interface{}{
			"port":       mockhw.MockPortName,
			"pin":        float64(0),
			"level":      true,
			"reset_mode": "no_reset",
		}
		res, err := handleGPIOSet(context.Background(), req)
		require.NoError(t, err)
		require.NotNil(t, res)
		require.True(t, res.IsError, "expected reserved-pin refusal, got success")

		tc, ok := res.Content[0].(mcp.TextContent)
		require.True(t, ok)
		assert.Contains(t, tc.Text, "reserved")
	})
}
