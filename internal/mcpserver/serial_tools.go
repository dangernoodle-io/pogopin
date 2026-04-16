package mcpserver

import (
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func registerSerialTools(s *server.MCPServer) {
	listTool := mcp.NewTool("serial_list",
		mcp.WithDescription("List available serial ports. Returns JSON array of {port, description, usb_vid, usb_pid, usb_serial} objects."),
		mcp.WithBoolean("usb_only", mcp.Description("Only list USB ports")),
	)
	s.AddTool(listTool, withRecover(handleSerialList))

	startTool := mcp.NewTool("serial_start",
		mcp.WithDescription("Start reading from a serial port into a ring buffer. Must be called before serial_read, serial_write, or serial_flash. Use serial_status to check state."),
		mcp.WithString("port", mcp.Required(), mcp.Description("Serial port name (e.g., /dev/ttyUSB0 or COM3)")),
		mcp.WithNumber("baud", mcp.Description("Baud rate (default 115200)")),
		mcp.WithNumber("buffer_size", mcp.Description("Ring buffer size in lines (default 1000)")),
		mcp.WithBoolean("auto_reset", mcp.Description("Auto-reset USB CDC devices after start for immediate output (default true)")),
	)
	s.AddTool(startTool, withRecover(handleSerialStart))

	readTool := mcp.NewTool("serial_read",
		mcp.WithDescription("Read buffered lines from a monitored serial port. Returns most recent lines (default 50). Use pattern to filter with regex. Use clear=true to drain the buffer after reading."),
		mcp.WithString("port", mcp.Description("Port name (optional if only one port open)")),
		mcp.WithNumber("lines", mcp.Description("Number of lines to read (default 50)")),
		mcp.WithBoolean("clear", mcp.Description("Clear buffer after reading (default false)")),
		mcp.WithString("pattern", mcp.Description("Regex pattern to filter output lines")),
	)
	s.AddTool(readTool, withRecover(handleSerialRead))

	stopTool := mcp.NewTool("serial_stop",
		mcp.WithDescription("Stop serial monitoring and release the port. Required before manual port access outside MCP."),
		mcp.WithString("port", mcp.Description("Port name (optional if only one port open)")),
	)
	s.AddTool(stopTool, withRecover(handleSerialStop))

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

	writeTool := mcp.NewTool("serial_write",
		mcp.WithDescription("Write data to a monitored serial port. Appends \\n by default; set raw=true to send exact bytes. Port must be started with serial_start."),
		mcp.WithString("port", mcp.Description("Port name (optional if only one port open)")),
		mcp.WithString("data", mcp.Required(), mcp.Description("Data to write")),
		mcp.WithBoolean("raw", mcp.Description("Skip appending newline (default false)")),
	)
	s.AddTool(writeTool, withRecover(handleSerialWrite))

	statusTool := mcp.NewTool("serial_status",
		mcp.WithDescription("Return serial port status. Returns JSON with running, port, baud, buffer_lines, reconnecting, last_error. Omit port to get all ports."),
		mcp.WithString("port", mcp.Description("Port name (optional; returns all ports if not specified)")),
	)
	s.AddTool(statusTool, withRecover(handleSerialStatus))
}
