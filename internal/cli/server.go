package cli

import (
	"context"
	"fmt"

	sheshacli "github.com/dangernoodle-io/shesha/cli"
	"github.com/spf13/cobra"

	"dangernoodle.io/pogopin/internal/mcpapp"
	"dangernoodle.io/pogopin/internal/status"
)

// serverCmd is a lazy shell around the real shesha-built server command:
// mcpapp.BuildApp only runs when the `server` subcommand is actually
// invoked (from RunE), not at package-var init — a BuildApp failure (a
// capability composition bug: duplicate tool names, bad schema, etc.)
// must not take down every other command (diagnostics/statusline/--help)
// just because the binary happened to load (MC-12 review). Flag parsing
// is disabled on this shell and re-delegated to the real command (built
// only after BuildApp succeeds), so --read-only/--http/--stateless still
// work identically once the App exists.
var serverCmd = &cobra.Command{
	Use:                "server",
	Short:              "Start the MCP server (stdio)",
	DisableFlagParsing: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		realCmd, err := buildServerCmd()
		if err != nil {
			return err
		}

		realCmd.SetArgs(args)
		realCmd.SetContext(cmd.Context())

		return realCmd.Execute()
	},
}

// buildServerCmd constructs the real shesha `server` command via shesha's
// App: --diagnostic (BR-72) is shesha's --read-only flag, OR'd with
// POGOPIN_DIAGNOSTIC via ReadOnlyEnv. The status heartbeat (feeds `pogo
// statusline`) is started in OnStart on the server's lifecycle context,
// and stopped implicitly when that context is cancelled; OnShutdown
// removes the status file.
func buildServerCmd() (*cobra.Command, error) {
	app, err := mcpapp.BuildApp()
	if err != nil {
		return nil, fmt.Errorf("cli: mcpapp.BuildApp: %w", err)
	}

	return sheshacli.ServerCmd(sheshacli.Server{
		App:         app,
		Use:         "server",
		Short:       "Start the MCP server (stdio)",
		ReadOnlyEnv: "POGOPIN_DIAGNOSTIC",
		OnStart: func(ctx context.Context) error {
			go mcpapp.RunHeartbeat(ctx)
			return nil
		},
		OnShutdown: func(ctx context.Context) error {
			status.Remove()
			return nil
		},
	}), nil
}
