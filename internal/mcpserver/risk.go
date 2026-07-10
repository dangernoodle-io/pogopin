package mcpserver

import "github.com/mark3labs/mcp-go/mcp"

// RiskClass classifies a registered MCP tool by the worst-case impact of
// invoking it. This is the single source of truth for tool risk
// classification (BR-71). DESTRUCTIVE entries must stay aligned with
// board-operator.md's confirm-list — a later step adds the anti-drift test
// enforcing that.
type RiskClass int

const (
	// RiskRead marks a tool that only observes state — no mutation of the
	// board, flash, NVS, or session.
	RiskRead RiskClass = iota
	// RiskWrite marks a tool that mutates state but is scoped/reversible
	// (a single register, a session lifecycle transition, a targeted
	// key-level NVS change, etc).
	RiskWrite
	// RiskDestructive marks a tool that can cause broad or irreversible
	// damage (whole-chip erase, full-partition replace, driving a
	// reserved/strapping GPIO pin).
	RiskDestructive
)

// String returns the lowercase name of the risk class.
func (c RiskClass) String() string {
	switch c {
	case RiskRead:
		return "read"
	case RiskWrite:
		return "write"
	case RiskDestructive:
		return "destructive"
	default:
		return "unknown"
	}
}

// toolRiskClass is the authoritative map from registered tool name to its
// risk classification (BR-71). Every tool registered by registerTools/
// registerHardwareTools MUST have exactly one entry here — risk_test.go
// enforces two-way completeness against the live tool registry.
var toolRiskClass = map[string]RiskClass{
	// Read: observation only, no mutation.
	"serial_list":      RiskRead,
	"serial_read":      RiskRead,
	"serial_status":    RiskRead,
	"esp_info":         RiskRead,
	"esp_read_flash":   RiskRead,
	"esp_read_nvs":     RiskRead,
	"esp_gpio_read":    RiskRead,
	"decode_backtrace": RiskRead,

	// Write: scoped/reversible mutation.
	"serial_start":   RiskWrite,
	"serial_stop":    RiskWrite,
	"serial_restart": RiskWrite,
	"serial_write":   RiskWrite,
	"esp_register":   RiskWrite, // can write a register; classified at its highest-risk shape
	"esp_reset":      RiskWrite,
	"esp_nvs_set":    RiskWrite,
	"esp_nvs_delete": RiskWrite,

	// Destructive: broad/irreversible impact.
	"esp_erase":      RiskDestructive,
	"esp_flash":      RiskDestructive,
	"esp_write_nvs":  RiskDestructive,
	"flash_external": RiskDestructive,
	"esp_gpio_set":   RiskDestructive, // include_reserved=true can drive flash/strap pins
	"esp_gpio_sweep": RiskDestructive, // include_reserved=true can drive flash/strap pins
}

// riskClassOf returns the risk classification for a registered tool name.
// The bool reports whether the tool has a classification entry.
func riskClassOf(name string) (RiskClass, bool) {
	c, ok := toolRiskClass[name]
	return c, ok
}

// riskAnnotationOpts derives the MCP readOnly/destructive hint annotations
// for a tool from its toolRiskClass entry (BR-71 STEP 2). This is the only
// place that maps RiskClass -> mcp.ToolOption — every registration site MUST
// go through this (via newTool) rather than hand-typing annotation values,
// which would recreate the drift the registry exists to prevent.
//
// A tool missing a registry entry (a programming error the completeness test
// in risk_test.go already guards against) falls back to the safest signal:
// not read-only, destructive — the same as mcp-go's own zero-value default,
// so an unclassified tool never over-claims safety.
func riskAnnotationOpts(name string) []mcp.ToolOption {
	class, ok := toolRiskClass[name]
	if !ok {
		class = RiskDestructive
	}

	var readOnly, destructive bool
	switch class {
	case RiskRead:
		readOnly = true
		destructive = false
	case RiskWrite:
		readOnly = false
		destructive = false
	case RiskDestructive:
		readOnly = false
		destructive = true
	}

	return []mcp.ToolOption{
		mcp.WithReadOnlyHintAnnotation(readOnly),
		mcp.WithDestructiveHintAnnotation(destructive),
	}
}

// newTool wraps mcp.NewTool, appending the tool's risk-derived annotation
// options so every registration site automatically gets the correct
// readOnly/destructive hints without repeating the mapping.
func newTool(name string, opts ...mcp.ToolOption) mcp.Tool {
	return mcp.NewTool(name, append(opts, riskAnnotationOpts(name)...)...)
}
