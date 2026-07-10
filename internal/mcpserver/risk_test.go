package mcpserver

import (
	"testing"

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
