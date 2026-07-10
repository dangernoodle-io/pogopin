//go:build hwtest

// TestHWBench drives the pogopin MCP server over its real stdio wire
// protocol against a physical ESP board. It is the committed form of the
// scratchpad JS driver that HW-validated the esp_gpio_* tools (10/10 on an
// ESP32-S2). Skipped entirely unless POGOPIN_HW_PORT is set, so `go test
// -tags hwtest ./...` without hardware attached is a clean skip and
// `go test ./...` (no tag) never compiles this file at all. Shared
// scaffolding (harness, resolveBinary, runGPIOScenarios, ...) lives in the
// untagged bench_common_test.go, shared with the hardware-free mock lane
// (mock_test.go).
package hwbench

import (
	"os"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
)

// newHarness builds the plain (no extra tags) pogo server from
// POGOPIN_HW_BIN or a fresh `go build`, and connects the MCP client to it —
// the hardware-lane constructor. See newHarnessWithBinary (bench_common_test.go)
// for the shared spawn/init logic used by both this lane and the mock lane
// (mock_test.go).
func newHarness(t *testing.T, port string, profile boardProfile) *harness {
	bin := resolveBinary(t, "POGOPIN_HW_BIN", nil)
	return newHarnessWithBinary(t, bin, port, profile)
}

func TestHWBench(t *testing.T) {
	port := os.Getenv("POGOPIN_HW_PORT")
	if port == "" {
		t.Skip("POGOPIN_HW_PORT not set — skipping hardware-integration bench")
	}

	boardKey := os.Getenv("POGOPIN_HW_BOARD")
	if boardKey == "" {
		boardKey = "s2"
	}
	profile, ok := lookupProfile(boardKey)
	require.True(t, ok, "unknown POGOPIN_HW_BOARD %q", boardKey)
	if pinOverride := os.Getenv("POGOPIN_HW_LED_PIN"); pinOverride != "" {
		v, err := strconv.Atoi(pinOverride)
		require.NoError(t, err, "invalid POGOPIN_HW_LED_PIN %q", pinOverride)
		profile.LEDPin = v
	}

	h := newHarness(t, port, profile)

	runGPIOScenarios(t, h)
	runSecurityInfoScenario(t, h)
	runChipIdentityScenario(t, h)
}
