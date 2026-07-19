package decode

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/dangernoodle-io/shesha/testkit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	pogodecode "dangernoodle.io/pogopin/internal/decode"
	"dangernoodle.io/pogopin/internal/testutil"
)

// newHarness composes a minimal shesha App around Capability{} alone and
// returns a ready testkit.Harness, so decode capability tests exercise real
// *mcpx.CallToolRequest values (valid Params, real progress-token plumbing)
// instead of a bare &mcpx.CallToolRequest{} (whose nil Params panics once a
// handler touches mcpx.ProgressToken — see the progress-lifecycle fix this
// mirrors serial/esp/flash's test convention for).
func newHarness(t *testing.T) *testkit.Harness {
	t.Helper()
	return testutil.NewHarness(t, Capability{})
}

func TestHandleDecodeBacktrace(t *testing.T) {
	t.Parallel()

	t.Run("both panic_text and panic_file", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		result, err := h.CallTool(context.Background(), "decode_backtrace", map[string]any{
			"elf_path":   "/path/to/elf",
			"panic_text": "Backtrace: 0x400d1234:0x3ffb0000",
			"panic_file": "/path/to/file",
		})
		require.NoError(t, err)
		require.True(t, result.IsError)
		require.Equal(t, "panic_text and panic_file are mutually exclusive", testkit.ResultText(result))
	})

	t.Run("neither panic_text nor panic_file", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		result, err := h.CallTool(context.Background(), "decode_backtrace", map[string]any{
			"elf_path": "/path/to/elf",
		})
		require.NoError(t, err)
		require.True(t, result.IsError)
		require.Equal(t, "one of panic_text or panic_file is required", testkit.ResultText(result))
	})

	t.Run("panic_file not found", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		result, err := h.CallTool(context.Background(), "decode_backtrace", map[string]any{
			"elf_path":   "/path/to/elf",
			"panic_file": "/nonexistent/panic.txt",
		})
		require.NoError(t, err)
		require.True(t, result.IsError)
		require.Contains(t, testkit.ResultText(result), "failed to read panic_file")
	})

	t.Run("bad ELF file returns tool error", func(t *testing.T) {
		t.Parallel()
		badElfPath := filepath.Join(t.TempDir(), "bad.elf")
		require.NoError(t, os.WriteFile(badElfPath, []byte("not an elf"), 0o644))

		h := newHarness(t)
		result, err := h.CallTool(context.Background(), "decode_backtrace", map[string]any{
			"elf_path":   badElfPath,
			"panic_text": "Backtrace: 0x400d1234:0x3ffb0000",
		})
		require.NoError(t, err)
		require.True(t, result.IsError)
		require.Contains(t, testkit.ResultText(result), "decode failed")
	})

	// TestHandleDecodeBacktrace/emits_progress_lifecycle_ticks proves
	// decode_backtrace wires mcpprogress.LifecycleStatus (MC-12 review
	// parity fix): a client-supplied progress token yields at least a
	// start and completion notification, mirroring
	// internal/capability/serial's TestSerialCapabilityProgressLifecycle.
	t.Run("emits progress lifecycle ticks", func(t *testing.T) {
		t.Parallel()

		h := newHarness(t)
		token := "tok-decode-backtrace"
		_, err := h.CallToolWithProgressToken(context.Background(), "decode_backtrace", map[string]any{
			"elf_path":   "/path/to/elf",
			"panic_text": "Backtrace: 0x400d1234:0x3ffb0000",
		}, token)
		require.NoError(t, err)

		// Progress notifications are delivered to the harness on the
		// client's async receive goroutine (see
		// testutil.WaitForProgressComplete), so poll for the terminal
		// completion tick instead of reading ProgressEvents immediately --
		// otherwise this races that delivery and flakes under CI's
		// slower/contended runners.
		events := testutil.WaitForProgressComplete(t, h, token, "decode_backtrace")
		require.GreaterOrEqual(t, len(events), 2, "expected at least a start and completion tick")
		assert.Contains(t, events[0].Message, "start: decode_backtrace")
		assert.Contains(t, events[len(events)-1].Message, "complete: decode_backtrace")
	})

	t.Run("happy path with panic_text (skipped if toolchain unavailable)", func(t *testing.T) {
		t.Parallel()
		if _, err := pogodecode.FindToolchain(pogodecode.ArchXtensa); err != nil {
			t.Skip("xtensa toolchain not available; skipping happy path test")
		}

		realElf := filepath.Join("..", "..", "..", "testdata", "xtensa.elf")
		h := newHarness(t)
		result, err := h.CallTool(context.Background(), "decode_backtrace", map[string]any{
			"elf_path":   realElf,
			"panic_text": "Backtrace: 0x400d1234:0x3ffb0000",
		})
		require.NoError(t, err)
		require.Greater(t, len(testkit.ResultText(result)), 0)
	})
}
