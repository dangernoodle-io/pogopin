package cli

import (
	"os"
	"strconv"

	"github.com/spf13/cobra"

	"dangernoodle.io/pogopin/internal/mcpserver"
)

var mcpCmd = &cobra.Command{
	Use:   "server",
	Short: "Start the MCP server",
	Long:  "Start the MCP server providing pogopin capabilities over stdio.",
	RunE:  runMCPServer,
}

var diagnosticFlag bool

func init() {
	mcpCmd.Flags().BoolVar(&diagnosticFlag, "diagnostic", false,
		"register only read-class tools (observe-only: no writes, flashing, erase, or session start)")
}

func runMCPServer(cmd *cobra.Command, args []string) error {
	mcpserver.SetDiagnosticMode(diagnosticFlag || envTruthy(os.Getenv("POGOPIN_DIAGNOSTIC")))
	return mcpserver.Serve()
}

// envTruthy reports whether an env var value should be treated as enabled.
func envTruthy(v string) bool {
	b, err := strconv.ParseBool(v)
	return err == nil && b
}
