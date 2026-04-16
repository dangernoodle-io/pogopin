package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"dangernoodle.io/breadboard/internal/decode"
)

func TestReadPanicInput(t *testing.T) {
	t.Parallel()

	t.Run("both file and input set", func(t *testing.T) {
		_, err := readPanicInput("file.txt", "input text")
		require.Error(t, err)
		require.Equal(t, "--file and --input are mutually exclusive", err.Error())
	})

	t.Run("input flag set", func(t *testing.T) {
		got, err := readPanicInput("", "test panic text")
		require.NoError(t, err)
		require.Equal(t, "test panic text", got)
	})

	t.Run("file flag set", func(t *testing.T) {
		tmpFile := filepath.Join(t.TempDir(), "panic.txt")
		testContent := "Backtrace: 0x400d1234:0x3ffb0000"
		err := os.WriteFile(tmpFile, []byte(testContent), 0o644)
		require.NoError(t, err)

		got, err := readPanicInput(tmpFile, "")
		require.NoError(t, err)
		require.Equal(t, testContent, got)
	})

	t.Run("nonexistent file", func(t *testing.T) {
		_, err := readPanicInput("/nonexistent/path/file.txt", "")
		require.Error(t, err)
	})
}

func TestPrintFrames(t *testing.T) {
	t.Parallel()

	t.Run("with frames", func(t *testing.T) {
		result := &decode.Result{
			Arch:          decode.ArchXtensa,
			ToolchainPath: "/opt/homebrew/bin/xtensa-esp-elf-addr2line",
			Frames: []decode.Frame{
				{
					PC:       "0x400d1234",
					Function: "app_main",
					File:     "main/main.c",
					Line:     42,
				},
				{
					PC:       "0x400d5678",
					Function: "vTaskDelay",
					File:     "freertos/tasks.c",
					Line:     891,
				},
			},
		}

		var buf bytes.Buffer
		printFrames(&buf, result)
		output := buf.String()

		require.Contains(t, output, "arch: xtensa (toolchain: /opt/homebrew/bin/xtensa-esp-elf-addr2line)")
		require.Contains(t, output, "#0  0x400d1234  app_main at main/main.c:42")
		require.Contains(t, output, "#1  0x400d5678  vTaskDelay at freertos/tasks.c:891")
	})

	t.Run("no frames", func(t *testing.T) {
		result := &decode.Result{
			Arch:          decode.ArchRiscv32,
			ToolchainPath: "/path/to/riscv32-esp-elf-addr2line",
			Frames:        []decode.Frame{},
		}

		var buf bytes.Buffer
		printFrames(&buf, result)
		output := buf.String()

		require.Contains(t, output, "arch: riscv32 (toolchain: /path/to/riscv32-esp-elf-addr2line)")
		require.Contains(t, output, "(no frames decoded)")
	})

	t.Run("frame with no file", func(t *testing.T) {
		result := &decode.Result{
			Arch:          decode.ArchXtensa,
			ToolchainPath: "/opt/homebrew/bin/xtensa-esp-elf-addr2line",
			Frames: []decode.Frame{
				{
					PC:       "0x400d1234",
					Function: "unknown_func",
					File:     "",
					Line:     0,
				},
			},
		}

		var buf bytes.Buffer
		printFrames(&buf, result)
		output := buf.String()

		require.Contains(t, output, "#0  0x400d1234  unknown_func\n")
		require.NotContains(t, output, " at ")
	})

	t.Run("unknown arch", func(t *testing.T) {
		result := &decode.Result{
			Arch:          decode.ArchUnknown,
			ToolchainPath: "/path/to/addr2line",
			Frames:        []decode.Frame{},
		}

		var buf bytes.Buffer
		printFrames(&buf, result)
		output := buf.String()

		require.Contains(t, output, "arch: unknown (toolchain: /path/to/addr2line)")
	})

	t.Run("nil frames slice", func(t *testing.T) {
		result := &decode.Result{
			Arch:          decode.ArchXtensa,
			ToolchainPath: "/opt/homebrew/bin/xtensa-esp-elf-addr2line",
			Frames:        nil,
		}

		var buf bytes.Buffer
		printFrames(&buf, result)
		output := buf.String()

		require.Contains(t, output, "(no frames decoded)")
	})
}
