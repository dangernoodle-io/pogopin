package cli

import (
	"github.com/spf13/cobra"

	"dangernoodle.io/breadboard/internal/mcpserver"
)

var mcpCmd = &cobra.Command{
	Use:   "server",
	Short: "Start the MCP server",
	Long:  "Start the MCP server providing espidf-tools capabilities over stdio.",
	RunE:  runMCPServer,
}

func runMCPServer(cmd *cobra.Command, args []string) error {
	return mcpserver.Serve()
}
