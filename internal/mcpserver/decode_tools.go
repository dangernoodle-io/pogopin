package mcpserver

import (
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// registerDecodeTools registers backtrace decoding tools.
func registerDecodeTools(s *server.MCPServer) {
	decodeTool := mcp.NewTool("decode_backtrace",
		mcp.WithDescription("Decode an ESP-IDF xtensa or riscv32 panic backtrace against an ELF file. Provide panic_text OR panic_file (one required) plus elf_path."),
		mcp.WithString("elf_path", mcp.Required(), mcp.Description("path to the ELF file")),
		mcp.WithString("panic_text", mcp.Description("inline panic text")),
		mcp.WithString("panic_file", mcp.Description("path to a file containing panic text")),
	)
	s.AddTool(decodeTool, handleDecodeBacktrace)
}
