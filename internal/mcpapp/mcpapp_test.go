package mcpapp

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/dangernoodle-io/shesha/testkit"
	"github.com/stretchr/testify/require"

	"dangernoodle.io/pogopin/internal/decode"
)

// TestBuildApp_ToolSet proves BuildApp composes exactly the expected
// startup tool surface: decode_backtrace plus the 7 serial_* tools. esp and
// flash now register their full hardware-tier tool set (MC-12 commits 4-5),
// but the hardware group starts locked, so none of it is visible until
// serial_list/serial_start unlocks it (see internal/mcpapp/tier_test.go).
func TestBuildApp_ToolSet(t *testing.T) {
	app, err := BuildApp()
	require.NoError(t, err)

	h := testkit.New(t, app)

	testkit.AssertToolSet(t, h,
		"decode_backtrace",
		"serial_list", "serial_start", "serial_read", "serial_write",
		"serial_stop", "serial_restart", "serial_status",
	)
}

// TestBuildApp_DecodeBacktraceRoundTrip proves the ported decode_backtrace
// tool is wired end to end through the shesha stack: a call reaches the
// handler, which validates its input the same way the mark3labs-based
// handler did.
func TestBuildApp_DecodeBacktraceRoundTrip(t *testing.T) {
	app, err := BuildApp()
	require.NoError(t, err)

	h := testkit.New(t, app)
	ctx := context.Background()

	t.Run("missing panic_text and panic_file", func(t *testing.T) {
		res, err := h.CallTool(ctx, "decode_backtrace", map[string]any{
			"elf_path": "/path/to/elf",
		})
		require.NoError(t, err)
		require.True(t, res.IsError)
		require.Equal(t, "one of panic_text or panic_file is required", testkit.ResultText(res))
	})

	t.Run("happy path (skipped if toolchain unavailable)", func(t *testing.T) {
		if _, err := decode.FindToolchain(decode.ArchXtensa); err != nil {
			t.Skip("xtensa toolchain not available; skipping happy path test")
		}

		realElf := filepath.Join("..", "..", "testdata", "xtensa.elf")
		res, err := h.CallTool(ctx, "decode_backtrace", map[string]any{
			"elf_path":   realElf,
			"panic_text": "Backtrace: 0x400d1234:0x3ffb0000",
		})
		require.NoError(t, err)
		require.Greater(t, len(testkit.ResultText(res)), 0)
	})
}
