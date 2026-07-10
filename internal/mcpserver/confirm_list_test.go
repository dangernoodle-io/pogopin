package mcpserver

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// boardOperatorAgentPath is the relative path from this package to
// board-operator.md's confirm-before-destructive list.
const boardOperatorAgentPath = "../../plugin/agents/board-operator.md"

const (
	confirmListStartMarker = "<!-- confirm-list:start -->"
	confirmListEndMarker   = "<!-- confirm-list:end -->"
)

// confirmListSection extracts the text between the confirm-list:start/end
// HTML comment markers in board-operator.md, so the drift check below only
// looks at the confirm-before-destructive section rather than the whole
// file (a tool name mentioned elsewhere, e.g. in the autonomy line, must
// not false-pass the check).
func confirmListSection(t *testing.T) string {
	t.Helper()

	data, err := os.ReadFile(boardOperatorAgentPath)
	require.NoError(t, err, "reading board-operator.md")

	content := string(data)

	start := strings.Index(content, confirmListStartMarker)
	require.GreaterOrEqual(t, start, 0, "confirm-list:start marker not found in board-operator.md")

	end := strings.Index(content, confirmListEndMarker)
	require.GreaterOrEqual(t, end, 0, "confirm-list:end marker not found in board-operator.md")
	require.Greater(t, end, start, "confirm-list:end marker precedes confirm-list:start marker")

	return content[start+len(confirmListStartMarker) : end]
}

// TestBoardOperatorConfirmListCoversDestructiveTools is the anti-drift guard
// for BR-71 STEP 3: every tool classified RiskDestructive in the
// toolRiskClass registry (internal/mcpserver/risk.go) MUST be named
// (backticked) in board-operator.md's confirm-before-destructive list. A
// registry entry with no matching confirm-list bullet is a real safety-gap
// drift — the tool would run without a confirm-gate — so this test fails
// loudly rather than letting the two drift apart silently (the exact defect
// this ticket closes: flash_external was DESTRUCTIVE in the registry but
// absent from the confirm-list).
func TestBoardOperatorConfirmListCoversDestructiveTools(t *testing.T) {
	section := confirmListSection(t)

	for name, class := range toolRiskClass {
		if class != RiskDestructive {
			continue
		}

		needle := fmt.Sprintf("`%s`", name)
		require.Contains(t, section, needle,
			"DESTRUCTIVE tool %q must be named in board-operator.md's confirm-before-destructive list (missing %s)", name, needle)
	}
}
