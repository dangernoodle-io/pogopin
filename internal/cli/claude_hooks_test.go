package cli

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestClaudeProvider_MountsHookLeaves proves `pogo claude hooks
// user-prompt-submit` and `pogo claude hooks pre-tool-use` are reachable
// leaves on rootCmd, and guards against nesting `statusline` under `claude`
// — the plugin's settings.json statusLine.command depends on the exact
// top-level `pogo statusline` invocation.
func TestClaudeProvider_MountsHookLeaves(t *testing.T) {
	cmd, _, err := rootCmd.Find([]string{"claude", "hooks", "user-prompt-submit"})
	require.NoError(t, err)
	assert.Equal(t, "user-prompt-submit", cmd.Name())

	cmd, _, err = rootCmd.Find([]string{"claude", "hooks", "pre-tool-use"})
	require.NoError(t, err)
	assert.Equal(t, "pre-tool-use", cmd.Name())

	cmd, _, err = rootCmd.Find([]string{"statusline"})
	require.NoError(t, err)
	assert.Equal(t, statuslineCmd, cmd, "pogo statusline must stay the top-level command")

	// "claude statusline" must not resolve past the "claude" namespace
	// command itself — proves statusline was never mounted under it.
	cmd, _, err = rootCmd.Find([]string{"claude", "statusline"})
	require.NoError(t, err)
	assert.Equal(t, "claude", cmd.Name())
}
