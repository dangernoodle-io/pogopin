package cli

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestServerCmdIsLazy confirms the package-level serverCmd var (built at
// init, before any subcommand is chosen) never touches mcpapp.BuildApp —
// only buildServerCmd (called from RunE, once the server subcommand is
// actually selected) does. This is what keeps a BuildApp composition bug
// from taking down diagnostics/statusline (MC-12 review).
func TestServerCmdIsLazy(t *testing.T) {
	require.Equal(t, "server", serverCmd.Use)
	assert.True(t, serverCmd.DisableFlagParsing,
		"flag parsing must be deferred to the real command built from BuildApp")
	assert.NotNil(t, serverCmd.RunE)
}

// TestBuildServerCmdWiresApp confirms buildServerCmd succeeds and produces
// a fully-flagged real server command (--read-only et al) once the App is
// built — exercised only when the server subcommand actually runs, never at
// package init.
func TestBuildServerCmdWiresApp(t *testing.T) {
	cmd, err := buildServerCmd()
	require.NoError(t, err)
	require.NotNil(t, cmd)
	assert.NotNil(t, cmd.Flags().Lookup("read-only"))
}
