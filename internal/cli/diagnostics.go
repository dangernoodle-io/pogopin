package cli

import (
	"github.com/spf13/cobra"
)

// diagnosticsCmd is a run-less parent grouping decode and gpio — the
// non-MCP, host-side diagnostic tools. decodeCmd and gpioCmd are unchanged;
// only re-parented here instead of added directly to root.
var diagnosticsCmd = &cobra.Command{
	Use:   "diagnostics",
	Short: "Host-side diagnostic tools: decode panic backtraces, probe GPIO",
}

func init() {
	diagnosticsCmd.AddCommand(decodeCmd)
	diagnosticsCmd.AddCommand(gpioCmd)
}
