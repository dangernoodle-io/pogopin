// Package decode is the shesha port of decode_backtrace (MC-12): the
// pattern-proving first tool ported off internal/mcpserver onto shesha.
// The underlying decode logic (internal/decode) is unchanged; only the MCP
// registration/handler seam moves.
package decode

import (
	"context"
	"encoding/json"
	"os"

	"github.com/dangernoodle-io/shesha"
	"github.com/dangernoodle-io/shesha/mcpx"

	"dangernoodle.io/pogopin/internal/decode"
	"dangernoodle.io/pogopin/internal/mcpprogress"
)

// In is decode_backtrace's input: an ELF path plus exactly one of inline
// panic text or a panic-text file. Mirrors the mark3labs-based tool's
// elf_path/panic_text/panic_file parameters (internal/mcpserver/decode_tools.go).
type In struct {
	// ElfPath is the path to the ELF file. Required.
	ElfPath string `json:"elf_path" jsonschema:"path to the ELF file"`
	// PanicText is inline panic text. Mutually exclusive with PanicFile;
	// exactly one of the two is required.
	PanicText string `json:"panic_text,omitempty" jsonschema:"inline panic text"`
	// PanicFile is the path to a file containing panic text. Mutually
	// exclusive with PanicText; exactly one of the two is required.
	PanicFile string `json:"panic_file,omitempty" jsonschema:"path to a file containing panic text"`
}

// Capability is the shesha Capability for the decode tool group.
type Capability struct{}

// Attach registers decode_backtrace against r.
func (c Capability) Attach(r *shesha.Registrar) error {
	shesha.AddTool(r, &mcpx.Tool{
		Name:        "decode_backtrace",
		Description: "Decode an ESP-IDF xtensa or riscv32 panic backtrace against an ELF file. Provide panic_text OR panic_file (one required) plus elf_path.",
	}, shesha.ReadOnly, handleDecodeBacktrace)
	return nil
}

// handleDecodeBacktrace handles the decode_backtrace MCP tool request. The
// decode logic itself (decode.Decode) is unchanged from the mark3labs-based
// handler this replaces.
func handleDecodeBacktrace(ctx context.Context, req *mcpx.CallToolRequest, in In) (*mcpx.CallToolResult, any, error) {
	done := mcpprogress.LifecycleStatus(ctx, req, "decode_backtrace")
	defer done()

	hasPanicText := in.PanicText != ""
	hasPanicFile := in.PanicFile != ""

	if hasPanicText && hasPanicFile {
		return mcpx.ErrorResult("panic_text and panic_file are mutually exclusive"), nil, nil
	}
	if !hasPanicText && !hasPanicFile {
		return mcpx.ErrorResult("one of panic_text or panic_file is required"), nil, nil
	}

	var panicContent string
	if hasPanicText {
		panicContent = in.PanicText
	} else {
		data, err := os.ReadFile(in.PanicFile)
		if err != nil {
			return mcpx.ErrorResult("failed to read panic_file: " + err.Error()), nil, nil //nolint:nilerr // MCP tool error, not Go error
		}
		panicContent = string(data)
	}

	result, err := decode.Decode(in.ElfPath, panicContent)
	if err != nil {
		return mcpx.ErrorResult("decode failed: " + err.Error()), nil, nil //nolint:nilerr // MCP tool error, not Go error
	}

	jsonBytes, err := json.Marshal(result)
	if err != nil {
		return mcpx.ErrorResult("failed to marshal result: " + err.Error()), nil, nil //nolint:nilerr // MCP tool error, not Go error
	}

	return mcpx.TextResult(string(jsonBytes)), nil, nil
}
