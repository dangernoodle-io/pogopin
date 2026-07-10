package mcpserver

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// claudeMdPath is the relative path from this package to the repo-root
// CLAUDE.md containing the human-readable tool table.
const claudeMdPath = "../../CLAUDE.md"

const (
	toolRiskTableStartMarker = "<!-- tool-risk-table:start -->"
	toolRiskTableEndMarker   = "<!-- tool-risk-table:end -->"
)

// toolRiskTableRow matches a `| tool | domain | risk | description |` data
// row (skips the header and separator rows, which don't start with a
// lowercase/underscore tool-name token).
var toolRiskTableRow = regexp.MustCompile(`(?m)^\|\s*([a-z_][a-z0-9_]*)\s*\|[^|]*\|\s*([a-z]+)\s*\|`)

// parseToolRiskTable extracts the tool-risk-table section from CLAUDE.md
// (between the tool-risk-table:start/end HTML comment markers, BR-71 STEP 4)
// and returns a map of tool name -> risk string as literally written in the
// doc. Fails loudly if the markers or the table are missing.
func parseToolRiskTable(t *testing.T) map[string]string {
	t.Helper()

	data, err := os.ReadFile(claudeMdPath)
	require.NoError(t, err, "reading CLAUDE.md")

	content := string(data)

	start := strings.Index(content, toolRiskTableStartMarker)
	require.GreaterOrEqual(t, start, 0, "tool-risk-table:start marker not found in CLAUDE.md")

	end := strings.Index(content, toolRiskTableEndMarker)
	require.GreaterOrEqual(t, end, 0, "tool-risk-table:end marker not found in CLAUDE.md")
	require.Greater(t, end, start, "tool-risk-table:end marker precedes tool-risk-table:start marker")

	section := content[start+len(toolRiskTableStartMarker) : end]

	matches := toolRiskTableRow.FindAllStringSubmatch(section, -1)
	require.NotEmpty(t, matches, "no tool-risk rows parsed from CLAUDE.md tool-risk-table section")

	table := make(map[string]string, len(matches))
	for _, m := range matches {
		table[m[1]] = m[2]
	}

	return table
}

// TestCLAUDEmdToolRiskTableMatchesRegistry is the anti-drift guard for
// BR-71 STEP 4: CLAUDE.md's tool table Risk column MUST exactly mirror the
// toolRiskClass registry (internal/mcpserver/risk.go) — every registered
// tool appears in the doc with the correct risk, and every doc row
// corresponds to a real registered tool with a matching risk (no stale or
// mistyped rows). Two-way so the doc can't silently drift from the SSOT in
// either direction.
func TestCLAUDEmdToolRiskTableMatchesRegistry(t *testing.T) {
	docTable := parseToolRiskTable(t)

	for name, class := range toolRiskClass {
		docRisk, ok := docTable[name]
		require.True(t, ok, "tool %q missing from CLAUDE.md tool-risk table", name)
		require.Equal(t, class.String(), docRisk,
			"tool %q: CLAUDE.md Risk column says %q, registry says %q", name, docRisk, class.String())
	}

	for name, docRisk := range docTable {
		class, ok := toolRiskClass[name]
		require.True(t, ok, "CLAUDE.md tool-risk table has row for %q, which is not in the toolRiskClass registry", name)
		require.Equal(t, class.String(), docRisk,
			"tool %q: CLAUDE.md Risk column says %q, registry says %q", name, docRisk, class.String())
	}
}
