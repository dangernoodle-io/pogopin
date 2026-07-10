package mcpserver

import (
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func registerSerialTools(s *server.MCPServer) {
	listTool := newTool("serial_list",
		mcp.WithDescription("List all available serial ports."),
	)
	addTool(s, listTool, withRecover(handleSerialList))

	startTool := newTool("serial_start",
		mcp.WithDescription("Start reading from a serial port into a ring buffer. Must be called before serial_read, serial_write, or serial_flash. Use serial_status to check state."),
		mcp.WithString("port", mcp.Required(), mcp.Description("Serial port name (e.g., /dev/ttyUSB0 or COM3)")),
		mcp.WithNumber("baud", mcp.Description("Baud rate (default 115200)")),
		mcp.WithNumber("buffer_size", mcp.Description("Ring buffer size in lines (default 1000)")),
		mcp.WithBoolean("auto_reset", mcp.Description("Auto-reset USB CDC devices after start for immediate output (default true)")),
	)
	addTool(s, startTool, withRecover(handleSerialStart))

	readTool := newTool("serial_read",
		mcp.WithDescription("Read buffered lines from a monitored serial port. Returns most recent lines (default 50). Use pattern to filter with regex. Use clear=true to drain the buffer after reading."),
		mcp.WithString("port", mcp.Description("Port name (optional if only one port open)")),
		mcp.WithNumber("lines", mcp.Description("Number of lines to read (default 50)")),
		mcp.WithBoolean("clear", mcp.Description("Clear buffer after reading (default false)")),
		mcp.WithString("pattern", mcp.Description("Regex pattern to filter output lines")),
		mcp.WithBoolean("raw", mcp.Description("Skip framing-noise filtering (ANSI escape stripping, repeated-byte collapsing, garbled-line elision) and return lines as captured (default false)")),
	)
	addTool(s, readTool, withRecover(handleSerialRead))

	stopTool := newTool("serial_stop",
		mcp.WithDescription("Stop serial monitoring and release the port. Required before manual port access outside MCP."),
		mcp.WithString("port", mcp.Description("Port name (optional if only one port open)")),
	)
	addTool(s, stopTool, withRecover(handleSerialStop))

	restartTool := newTool("serial_restart",
		mcp.WithDescription("Stop then restart buffered serial monitoring on a port (atomic stop+start); use to re-trigger a DTR/RTS reset without separate stop/start calls. If the port is open, its current baud is preserved as the default; request args override."),
		mcp.WithString("port", mcp.Required(), mcp.Description("Serial port name (e.g., /dev/ttyUSB0 or COM3)")),
		mcp.WithNumber("baud", mcp.Description("Baud rate (default: current baud if open, else 115200)")),
		mcp.WithNumber("buffer_size", mcp.Description("Ring buffer size in lines (default 1000)")),
		mcp.WithBoolean("auto_reset", mcp.Description("Auto-reset USB CDC devices after restart for immediate output (default true)")),
	)
	addTool(s, restartTool, withRecover(handleSerialRestart))

	writeTool := newTool("serial_write",
		mcp.WithDescription("Write data to a monitored serial port. Appends \\n by default; set raw=true to send exact bytes. Port must be started with serial_start."),
		mcp.WithString("port", mcp.Description("Port name (optional if only one port open)")),
		mcp.WithString("data", mcp.Required(), mcp.Description("Data to write")),
		mcp.WithBoolean("raw", mcp.Description("Skip appending newline (default false)")),
	)
	addTool(s, writeTool, withRecover(handleSerialWrite))

	statusTool := newTool("serial_status",
		mcp.WithDescription("Return serial port status. Returns JSON with running, port, baud, buffer_lines, reconnecting, last_error. Omit port to get all ports."),
		mcp.WithString("port", mcp.Description("Port name (optional; returns all ports if not specified)")),
	)
	addTool(s, statusTool, withRecover(handleSerialStatus))
}
