package mcpserver

import (
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
)

// TestRiskClassString covers the String() method for all three classes plus
// the default (unreachable via normal use, but keeps the switch exhaustive
// and covered).
func TestRiskClassString(t *testing.T) {
	assert.Equal(t, "read", RiskRead.String())
	assert.Equal(t, "write", RiskWrite.String())
	assert.Equal(t, "destructive", RiskDestructive.String())
	assert.Equal(t, "unknown", RiskClass(99).String())
}

// TestRiskClassOf covers the riskClassOf helper for both the hit and miss
// paths.
func TestRiskClassOf(t *testing.T) {
	c, ok := riskClassOf("esp_erase")
	assert.True(t, ok)
	assert.Equal(t, RiskDestructive, c)

	_, ok = riskClassOf("not_a_real_tool")
	assert.False(t, ok)
}

// TestToolRiskClassCompleteness is the drift guard for BR-71 STEP 1: the
// toolRiskClass registry must classify EXACTLY the set of tools actually
// registered by the server (core tier + lazily-registered hardware tier),
// two-way — no registered tool missing a class, no stale/typo'd registry
// entry pointing at a tool that no longer exists.
func TestToolRiskClassCompleteness(t *testing.T) {
	s := newTestServer(t)
	registerTools(s)
	registerHardwareTools(s)

	registered := toolNames(s)

	registeredSet := make(map[string]bool, len(registered))
	for name := range registered {
		registeredSet[name] = true
	}

	classifiedSet := make(map[string]bool, len(toolRiskClass))
	for name := range toolRiskClass {
		classifiedSet[name] = true
	}

	// 1. Every registered tool has exactly one classification entry.
	for name := range registeredSet {
		assert.True(t, classifiedSet[name], "registered tool %q has no toolRiskClass entry", name)
	}

	// 2. Every classification entry corresponds to a registered tool.
	for name := range classifiedSet {
		assert.True(t, registeredSet[name], "toolRiskClass entry %q does not correspond to a registered tool", name)
	}

	// Stronger than a count: full set equality.
	assert.Equal(t, registeredSet, classifiedSet)
	assert.Len(t, toolRiskClass, 22, "expected exactly 22 classified tools (8 read + 8 write + 6 destructive)")
}

// TestToolAnnotationsMatchRiskClass is the drift guard for BR-71 STEP 2: the
// readOnly/destructive hint annotations mcp-go emits for every registered
// tool must match its toolRiskClass entry. This exercises riskAnnotationOpts
// end-to-end (via newTool) rather than unit-testing it in isolation, so a
// tool that slips the wrapper and falls back to mcp.NewTool's zero-value
// default (ReadOnlyHint:false, DestructiveHint:true) is caught here too.
func TestToolAnnotationsMatchRiskClass(t *testing.T) {
	s := newTestServer(t)
	registerTools(s)
	registerHardwareTools(s)

	registered := s.ListTools()

	for name, class := range toolRiskClass {
		st, ok := registered[name]
		if !assert.True(t, ok, "tool %q in toolRiskClass but not registered", name) {
			continue
		}

		ann := st.Tool.Annotations
		if !assert.NotNil(t, ann.ReadOnlyHint, "tool %q: ReadOnlyHint not set", name) ||
			!assert.NotNil(t, ann.DestructiveHint, "tool %q: DestructiveHint not set", name) {
			continue
		}

		switch class {
		case RiskRead:
			assert.True(t, *ann.ReadOnlyHint, "tool %q (read): expected ReadOnlyHint=true", name)
			assert.False(t, *ann.DestructiveHint, "tool %q (read): expected DestructiveHint=false", name)
		case RiskWrite:
			assert.False(t, *ann.ReadOnlyHint, "tool %q (write): expected ReadOnlyHint=false", name)
			assert.False(t, *ann.DestructiveHint, "tool %q (write): expected DestructiveHint=false", name)
		case RiskDestructive:
			assert.False(t, *ann.ReadOnlyHint, "tool %q (destructive): expected ReadOnlyHint=false", name)
			assert.True(t, *ann.DestructiveHint, "tool %q (destructive): expected DestructiveHint=true", name)
		}
	}
}

// TestRiskAnnotationOptsUnknownFallback verifies the safety-critical unknown
// tool fallback in riskAnnotationOpts: an unclassified tool (missing from
// toolRiskClass) must encode readOnly=false AND destructive=true, never
// over-claiming safety.
func TestRiskAnnotationOptsUnknownFallback(t *testing.T) {
	opts := riskAnnotationOpts("definitely-not-a-registered-tool")

	// Create a throwaway tool with the returned options and inspect its annotations.
	tool := mcp.NewTool("test-unknown", opts...)
	ann := tool.Annotations

	// Both hints must be set (non-nil).
	if !assert.NotNil(t, ann.ReadOnlyHint, "ReadOnlyHint not set for unknown tool") {
		return
	}
	if !assert.NotNil(t, ann.DestructiveHint, "DestructiveHint not set for unknown tool") {
		return
	}

	// Unknown tool must be treated as destructive (the safe default).
	assert.False(t, *ann.ReadOnlyHint, "unknown tool: expected ReadOnlyHint=false")
	assert.True(t, *ann.DestructiveHint, "unknown tool: expected DestructiveHint=true")
}
