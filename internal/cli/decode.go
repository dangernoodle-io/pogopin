package cli

import (
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"dangernoodle.io/breadboard/internal/decode"
)

var decodeCmd = &cobra.Command{
	Use:   "decode",
	Short: "Symbolize panic backtraces against an ELF file",
	Long:  "Decode symbolizes ESP32 panic backtraces against an ELF binary, mapping PC addresses to function names and source locations.",
	RunE:  runDecode,
}

var (
	elfPath   string
	filePath  string
	inputPath string
)

func init() {
	decodeCmd.Flags().StringVar(&elfPath, "elf", "", "path to ELF binary (required)")
	decodeCmd.Flags().StringVar(&filePath, "file", "", "panic text file to decode")
	decodeCmd.Flags().StringVar(&inputPath, "input", "", "panic text from stdin or file (mutually exclusive with --file)")

	_ = decodeCmd.MarkFlagRequired("elf")
}

func runDecode(cmd *cobra.Command, args []string) error {
	panicText, err := readPanicInput(filePath, inputPath)
	if err != nil {
		return err
	}

	result, err := decode.Decode(elfPath, panicText)
	if err != nil {
		return err
	}

	printFrames(cmd.OutOrStdout(), result)
	return nil
}

// readPanicInput returns the panic text from the selected source.
// Both --file and --input set → error
// --input set → return it
// --file set → read file
// Neither set → if stdin is a pipe, read from stdin; else return usage error.
func readPanicInput(fileFlag, inputFlag string) (string, error) {
	if fileFlag != "" && inputFlag != "" {
		return "", fmt.Errorf("--file and --input are mutually exclusive")
	}

	if inputFlag != "" {
		return inputFlag, nil
	}

	if fileFlag != "" {
		data, err := os.ReadFile(fileFlag)
		if err != nil {
			return "", err
		}
		return string(data), nil
	}

	// Neither set; check if stdin is a pipe
	stat, err := os.Stdin.Stat()
	if err != nil {
		return "", fmt.Errorf("stat stdin: %w", err)
	}
	isTerminal := stat.Mode()&os.ModeCharDevice != 0
	if isTerminal {
		return "", fmt.Errorf("no panic text provided: use --file, --input, or pipe via stdin")
	}

	// Read from stdin
	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		return "", fmt.Errorf("read stdin: %w", err)
	}
	return string(data), nil
}

// printFrames writes the decoded frames to w in human-readable format.
func printFrames(w io.Writer, result *decode.Result) {
	archStr := ""
	switch result.Arch {
	case decode.ArchXtensa:
		archStr = "xtensa"
	case decode.ArchRiscv32:
		archStr = "riscv32"
	default:
		archStr = "unknown"
	}

	_, _ = fmt.Fprintf(w, "arch: %s (toolchain: %s)\n", archStr, result.ToolchainPath)
	_, _ = fmt.Fprintf(w, "\n")

	if len(result.Frames) == 0 {
		_, _ = fmt.Fprintf(w, "(no frames decoded)\n")
		return
	}

	for i, frame := range result.Frames {
		fileLine := ""
		if frame.File != "" {
			fileLine = fmt.Sprintf(" at %s:%d", frame.File, frame.Line)
		}
		_, _ = fmt.Fprintf(w, "#%d  %s  %s%s\n", i, frame.PC, frame.Function, fileLine)
	}
}
