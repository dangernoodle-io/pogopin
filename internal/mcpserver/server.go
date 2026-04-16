package mcpserver

import (
	"github.com/mark3labs/mcp-go/server"
)

const instructions = `Serial monitoring: serial_start → serial_read/serial_write → serial_stop
ESP flashing: esp_flash (native Go), flash_external (PlatformIO/avrdude/any CLI)
ESP device info: esp_info (chip by default; pass include=security for secure boot/encryption)
ESP flash ops: esp_read_flash (raw bytes or md5=true for hash), esp_erase
ESP NVS: esp_read_nvs (read), esp_nvs_set (set keys, RMW), esp_nvs_delete (delete keys, RMW), esp_write_nvs (DESTRUCTIVE full partition replace)
ESP low-level: esp_register (read/write), esp_reset
Crash analysis: decode_backtrace (xtensa/riscv32 panic frames)
Always serial_stop before esp_* operations on the same port.`

// Serve starts the MCP server.
func Serve() error {
	s := server.NewMCPServer("breadboard", "0.1.0",
		server.WithToolCapabilities(true),
		server.WithInstructions(instructions),
	)

	registerTools(s)

	return server.ServeStdio(s)
}

// registerTools registers all MCP tools.
func registerTools(s *server.MCPServer) {
	registerSerialTools(s)
	registerESPTools(s)
	registerDecodeTools(s)
}
