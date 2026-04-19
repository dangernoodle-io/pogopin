package mcpserver

import (
	"context"
	"encoding/json"
	"os"

	"github.com/mark3labs/mcp-go/mcp"

	"dangernoodle.io/pogopin/internal/decode"
)

// handleDecodeBacktrace handles the decode_backtrace MCP tool request.
func handleDecodeBacktrace(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	// Extract required parameter
	elfPath, err := req.RequireString("elf_path")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	// Extract optional parameters
	args := req.GetArguments()
	panicText, hasPanicText := args["panic_text"].(string)
	panicFile, hasPanicFile := args["panic_file"].(string)

	// Validate mutual exclusion
	if hasPanicText && hasPanicFile {
		return mcp.NewToolResultError("panic_text and panic_file are mutually exclusive"), nil
	}
	if !hasPanicText && !hasPanicFile {
		return mcp.NewToolResultError("one of panic_text or panic_file is required"), nil
	}

	// Determine panic text
	var panicContent string
	if hasPanicText {
		panicContent = panicText
	} else {
		data, err := os.ReadFile(panicFile)
		if err != nil {
			return mcp.NewToolResultError("failed to read panic_file: " + err.Error()), nil //nolint:nilerr // MCP tool error, not Go error
		}
		panicContent = string(data)
	}

	// Call decoder
	result, err := decode.Decode(elfPath, panicContent)
	if err != nil {
		return mcp.NewToolResultError("decode failed: " + err.Error()), nil //nolint:nilerr // MCP tool error, not Go error
	}

	// Marshal result to JSON
	jsonBytes, err := json.Marshal(result)
	if err != nil {
		return mcp.NewToolResultError("failed to marshal result: " + err.Error()), nil //nolint:nilerr // MCP tool error, not Go error
	}

	return mcp.NewToolResultText(string(jsonBytes)), nil
}
