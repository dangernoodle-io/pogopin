package cli

import (
	"github.com/spf13/cobra"
)

// Version is set via ldflags at build time.
var Version string

var rootCmd = &cobra.Command{
	Use:          "breadboard",
	Short:        "Embedded development MCP server",
	Long:         "Embedded development MCP server — serial monitoring, ESP-IDF flashing, crash decode, and more.",
	Version:      Version,
	SilenceUsage: true,
}

func init() {
	rootCmd.AddCommand(decodeCmd)
	rootCmd.AddCommand(mcpCmd)
}

// Execute runs the root command.
func Execute() error {
	return rootCmd.Execute()
}
