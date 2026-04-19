package mcpserver

import (
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func registerFlashExternalTool(s *server.MCPServer) {
	flashTool := mcp.NewTool("flash_external",
		mcp.WithDescription("Run a flash/build command while managing serial lifecycle (stop → exec → restart → capture boot output). Use for platformio, make, esptool.py, or any build+flash workflow. By default runs the command directly (no shell); set shell=true for &&, pipes, or globs. Set cwd for commands that need a working directory (e.g., make). For native ESP flashing without external tools, use serial_flash_esp instead."),
		mcp.WithString("port", mcp.Description("Port name (optional if only one port open)")),
		mcp.WithString("command", mcp.Required(), mcp.Description("Flash command to run")),
		mcp.WithArray("args", mcp.Description("Command arguments")),
		mcp.WithNumber("output_lines", mcp.Description("Limit command output to last N lines (0 = unlimited)")),
		mcp.WithString("output_filter", mcp.Description("Regex pattern to filter command output lines")),
		mcp.WithBoolean("shell", mcp.Description("Run command via sh -c (enables &&, pipes, globs; args ignored)")),
		mcp.WithString("cwd", mcp.Description("Working directory for the command")),
	)
	s.AddTool(flashTool, withRecover(handleSerialFlash))
}
