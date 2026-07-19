package mcpapp

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/dangernoodle-io/shesha/testkit"
	"github.com/stretchr/testify/require"

	"dangernoodle.io/pogopin/internal/serial"
	"dangernoodle.io/pogopin/internal/session"
)

// MC-12 port of internal/mcpserver/confirm_list_test.go and
// internal/mcpserver/tool_risk_doc_test.go's anti-drift guards (BR-71).
// Risk is no longer a central toolRiskClass map in the shesha stack: it is
// scattered across each capability's shesha.AddTool call (a per-tool
// shesha.Risk argument). Rather than hand-maintaining a second, parallel
// package-level map as the doc-alignment source of truth (which itself
// could drift from the real registrations), these tests introspect the
// BUILT app's live tools/list via testkit.Harness.ListTools: each tool's
// mcpx.ToolAnnotations (ReadOnlyHint / DestructiveHint), which shesha.AddTool
// derives directly from its Risk argument (mcpx.RiskAnnotations), is the
// single, always-accurate source of truth. This is the "introspect the
// built App's tool annotations" option from the two offered — chosen over a
// hand-authored map because it can never drift from what's actually
// registered.

// toolRisk classifies a tool by its shesha Risk, decoded from
// mcpx.ToolAnnotations the same way shesha.AddTool encodes it
// (readOnly=true => ReadOnly; destructive=true => Destructive; otherwise
// Write).
type toolRisk int

const (
	riskRead toolRisk = iota
	riskWrite
	riskDestructive
)

func (r toolRisk) String() string {
	switch r {
	case riskRead:
		return "read"
	case riskWrite:
		return "write"
	case riskDestructive:
		return "destructive"
	default:
		return "unknown"
	}
}

// liveToolRisks unlocks the hardware tier (so every tool, not just the core
// startup set, is registered) and returns the live tools/list's risk
// classification for every tool, keyed by name.
func liveToolRisks(t *testing.T) map[string]toolRisk {
	t.Helper()
	setupTestPorts(t)
	origListFn := session.SetListPortsFn(func() ([]serial.PortInfo, error) { return nil, nil })
	t.Cleanup(func() { session.SetListPortsFn(origListFn) })

	app, err := BuildApp()
	require.NoError(t, err)
	h := testkit.New(t, app)

	res, err := h.CallTool(context.Background(), "serial_list", map[string]any{})
	require.NoError(t, err)
	require.False(t, res.IsError)

	list, err := h.ListTools(context.Background())
	require.NoError(t, err)
	require.NotEmpty(t, list.Tools)

	out := make(map[string]toolRisk, len(list.Tools))
	for _, tool := range list.Tools {
		require.NotNil(t, tool.Annotations, "tool %q missing annotations", tool.Name)
		switch {
		case tool.Annotations.ReadOnlyHint:
			out[tool.Name] = riskRead
		case tool.Annotations.DestructiveHint != nil && *tool.Annotations.DestructiveHint:
			out[tool.Name] = riskDestructive
		default:
			out[tool.Name] = riskWrite
		}
	}
	return out
}

// boardOperatorAgentPath is the relative path from this package to
// board-operator.md's confirm-before-destructive list.
const boardOperatorAgentPath = "../../plugin/agents/board-operator.md"

const (
	confirmListStartMarker = "<!-- confirm-list:start -->"
	confirmListEndMarker   = "<!-- confirm-list:end -->"
)

// confirmListSection extracts the text between the confirm-list:start/end
// HTML comment markers in board-operator.md, mirroring
// internal/mcpserver/confirm_list_test.go's helper of the same name.
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
// for BR-71 STEP 3: every tool the live app registers as Destructive MUST be
// named (backticked) in board-operator.md's confirm-before-destructive
// list. A tool with no matching confirm-list bullet is a real safety-gap
// drift -- the tool would run without a confirm-gate.
func TestBoardOperatorConfirmListCoversDestructiveTools(t *testing.T) {
	risks := liveToolRisks(t)
	section := confirmListSection(t)

	for name, risk := range risks {
		if risk != riskDestructive {
			continue
		}

		needle := fmt.Sprintf("`%s`", name)
		require.Contains(t, section, needle,
			"DESTRUCTIVE tool %q must be named in board-operator.md's confirm-before-destructive list (missing %s)", name, needle)
	}
}

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
// doc.
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
// live app's registered tool risks -- every registered tool appears in the
// doc with the correct risk, and every doc row corresponds to a real
// registered tool with a matching risk (no stale or mistyped rows).
func TestCLAUDEmdToolRiskTableMatchesRegistry(t *testing.T) {
	risks := liveToolRisks(t)
	docTable := parseToolRiskTable(t)

	for name, risk := range risks {
		docRisk, ok := docTable[name]
		require.True(t, ok, "tool %q missing from CLAUDE.md tool-risk table", name)
		require.Equal(t, risk.String(), docRisk,
			"tool %q: CLAUDE.md Risk column says %q, live registration says %q", name, docRisk, risk.String())
	}

	for name, docRisk := range docTable {
		risk, ok := risks[name]
		require.True(t, ok, "CLAUDE.md tool-risk table has row for %q, which is not a registered tool", name)
		require.Equal(t, risk.String(), docRisk,
			"tool %q: CLAUDE.md Risk column says %q, live registration says %q", name, docRisk, risk.String())
	}
}
