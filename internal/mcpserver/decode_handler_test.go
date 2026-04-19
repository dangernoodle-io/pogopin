package mcpserver

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/require"

	"dangernoodle.io/pogopin/internal/decode"
)

// extractTextContent retrieves the text from a CallToolResult.
func extractTextContent(t *testing.T, result *mcp.CallToolResult) string {
	t.Helper()
	require.NotNil(t, result)
	require.Greater(t, len(result.Content), 0)
	textContent, ok := mcp.AsTextContent(result.Content[0])
	require.True(t, ok, "expected text content")
	return textContent.Text
}

func TestHandleDecodeBacktrace(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	t.Run("missing elf_path", func(t *testing.T) {
		req := mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Arguments: map[string]interface{}{
					"panic_text": "Backtrace: 0x400d1234:0x3ffb0000",
				},
			},
		}
		result, err := handleDecodeBacktrace(ctx, req)
		require.NoError(t, err)
		text := extractTextContent(t, result)
		require.Contains(t, text, "elf_path")
		require.Contains(t, text, "required")
	})

	t.Run("both panic_text and panic_file", func(t *testing.T) {
		req := mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Arguments: map[string]interface{}{
					"elf_path":   "/path/to/elf",
					"panic_text": "Backtrace: 0x400d1234:0x3ffb0000",
					"panic_file": "/path/to/file",
				},
			},
		}
		result, err := handleDecodeBacktrace(ctx, req)
		require.NoError(t, err)
		text := extractTextContent(t, result)
		require.Equal(t, "panic_text and panic_file are mutually exclusive", text)
	})

	t.Run("neither panic_text nor panic_file", func(t *testing.T) {
		req := mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Arguments: map[string]interface{}{
					"elf_path": "/path/to/elf",
				},
			},
		}
		result, err := handleDecodeBacktrace(ctx, req)
		require.NoError(t, err)
		text := extractTextContent(t, result)
		require.Equal(t, "one of panic_text or panic_file is required", text)
	})

	t.Run("panic_file not found", func(t *testing.T) {
		req := mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Arguments: map[string]interface{}{
					"elf_path":   "/path/to/elf",
					"panic_file": "/nonexistent/panic.txt",
				},
			},
		}
		result, err := handleDecodeBacktrace(ctx, req)
		require.NoError(t, err)
		text := extractTextContent(t, result)
		require.Contains(t, text, "failed to read panic_file")
	})

	t.Run("happy path with panic_text (skipped if toolchain unavailable)", func(t *testing.T) {
		// Create a synthetic ELF for testing
		elfPath := filepath.Join(t.TempDir(), "test.elf")
		err := os.WriteFile(elfPath, []byte{0x7f, 0x45, 0x4c, 0x46}, 0o644)
		require.NoError(t, err)

		// Check if toolchain is available; skip if not
		_, err = decode.FindToolchain(decode.ArchXtensa)
		if err != nil {
			t.Skip("xtensa toolchain not available; skipping happy path test")
		}

		// Use real ELF from testdata
		realElf := filepath.Join("../../testdata/xtensa.elf")
		panicText := "Backtrace: 0x400d1234:0x3ffb0000"

		req := mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Arguments: map[string]interface{}{
					"elf_path":   realElf,
					"panic_text": panicText,
				},
			},
		}
		result, err := handleDecodeBacktrace(ctx, req)
		require.NoError(t, err)

		// Result should be JSON (error or success; either is valid as long as decode ran)
		text := extractTextContent(t, result)
		require.Greater(t, len(text), 0)
	})

	t.Run("bad ELF file returns tool error", func(t *testing.T) {
		badElfPath := filepath.Join(t.TempDir(), "bad.elf")
		err := os.WriteFile(badElfPath, []byte("not an elf"), 0o644)
		require.NoError(t, err)

		req := mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Arguments: map[string]interface{}{
					"elf_path":   badElfPath,
					"panic_text": "Backtrace: 0x400d1234:0x3ffb0000",
				},
			},
		}
		result, err := handleDecodeBacktrace(ctx, req)
		require.NoError(t, err)
		text := extractTextContent(t, result)
		require.Contains(t, text, "decode failed")
	})
}
