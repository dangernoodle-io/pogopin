// TestMockBench drives the pogopin MCP server over its real stdio wire
// protocol against internal/mockhw's virtual chip for the selected board's
// chip family (BR-66 PR3: ESP32, ESP32-S2, ESP32-C3, ESP32-S3 all have a
// mock profile) — the same scenarios TestHWBench (hwbench_test.go,
// hardware-gated) runs against real silicon, run here hardware-free. Gated
// on ACC_POGOPIN (acceptance-test convention, mirrors TF_ACC) so it skips
// in a plain `go test ./...` run; `make mock-bench` / `make acc` set the
// env var themselves. Untagged (no hwtest tag needed — the mock server
// binary is built explicitly below, with the `mock` build tag, independent
// of this test binary's own tags).
package hwbench

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMockBench(t *testing.T) {
	if os.Getenv("ACC_POGOPIN") == "" {
		t.Skip("ACC_POGOPIN not set — skipping hardware-free virtual-chip bench")
	}

	boardKey := os.Getenv("ACC_POGOPIN_BOARD")
	if boardKey == "" {
		boardKey = "s2"
	}
	profile, ok := lookupProfile(boardKey)
	require.True(t, ok, "unknown ACC_POGOPIN_BOARD %q", boardKey)
	require.NotEmpty(t, profile.MockPort, "board %q has no mockhw MockPort mapping", boardKey)

	bin := resolveBinary(t, "ACC_POGOPIN_BIN", []string{"mock"})
	h := newHarnessWithBinary(t, bin, profile.MockPort, profile)

	runGPIOScenarios(t, h)
	runSecurityInfoScenario(t, h)
	runChipIdentityScenario(t, h)
	runSerialMonitorScenarios(t, h)
}
