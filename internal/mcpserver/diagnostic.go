package mcpserver

import (
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// diagnosticMode gates tool registration to READ-class tools only (BR-72).
// Set via SetDiagnosticMode before registerTools/registerHardwareTools run.
// Inert (false) by default — no behavior change unless explicitly enabled.
var diagnosticMode bool

// SetDiagnosticMode enables or disables the diagnostic registration filter.
// When enabled, addTool (the sole AddTool entry point for every registration
// site) skips any tool whose toolRiskClass is not RiskRead — so a diagnostic
// server never exposes a WRITE or DESTRUCTIVE tool to the client at all
// (server-side enforcement, not just an advisory annotation).
func SetDiagnosticMode(enabled bool) {
	diagnosticMode = enabled
}

// addTool registers tool on s via handler, unless diagnosticMode is on and
// tool is not classified RiskRead in toolRiskClass, in which case
// registration is skipped entirely. Unknown tools are treated as non-READ
// and excluded from the diagnostic profile (safe-by-default).
func addTool(s *server.MCPServer, tool mcp.Tool, handler server.ToolHandlerFunc) {
	if diagnosticMode {
		class, ok := toolRiskClass[tool.Name]
		if !ok || class != RiskRead {
			return // unknown or non-READ tools are excluded from the read-only diagnostic profile
		}
	}
	s.AddTool(tool, handler)
}
