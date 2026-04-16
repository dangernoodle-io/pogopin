package mcpserver

import (
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func registerESPTools(s *server.MCPServer) {
	espFlashTool := mcp.NewTool("esp_flash",
		mcp.WithDescription("Flash firmware to ESP chip using native Go flasher. images is an array of {path: string, offset: number} objects. Returns JSON with chip, flash_size, images_written, bytes_written, and new_port (if USB CDC re-enumerated). Captures boot_output (up to 100 lines) after flash."),
		mcp.WithString("port", mcp.Required(), mcp.Description("Serial port")),
		mcp.WithArray("images", mcp.Required(), mcp.Description("Array of {path, offset} objects")),
		mcp.WithNumber("baud", mcp.Description("Connection baud rate (default 115200)")),
		mcp.WithNumber("flash_baud", mcp.Description("Flash transfer baud rate (default 460800)")),
		mcp.WithString("reset_mode", mcp.Description("Reset mode: auto (default), default, usb_jtag, no_reset")),
		mcp.WithString("flash_mode", mcp.Description("Flash mode: dio, dout, qio, qout")),
		mcp.WithString("flash_size", mcp.Description("Flash size: 1MB, 2MB, 4MB, 8MB, 16MB")),
		mcp.WithString("chip_type", mcp.Description("Chip type: esp32, esp32s3, esp32c6, etc (default auto-detect)")),
		mcp.WithNumber("boot_wait", mcp.Description("Seconds to wait for boot output after reset (default 2; 0 = skip)")),
	)
	s.AddTool(espFlashTool, withRecover(handleFlash))

	espEraseTool := mcp.NewTool("esp_erase",
		mcp.WithDescription("Erase flash on ESP chip. Omit offset to erase entire chip; provide offset+size for a region. Returns new_port if USB CDC re-enumerated. Captures boot_output after erase."),
		mcp.WithString("port", mcp.Required(), mcp.Description("Serial port")),
		mcp.WithNumber("baud", mcp.Description("Connection baud rate (default 115200)")),
		mcp.WithNumber("offset", mcp.Description("Erase offset (optional; omit to erase entire chip)")),
		mcp.WithNumber("size", mcp.Description("Erase size in bytes (required if offset is specified)")),
		mcp.WithString("reset_mode", mcp.Description("Reset mode: auto (default), default, usb_jtag, no_reset")),
		mcp.WithNumber("boot_wait", mcp.Description("Seconds to wait for boot output after reset (default 2; 0 = skip)")),
	)
	s.AddTool(espEraseTool, withRecover(handleErase))

	espInfoTool := mcp.NewTool("esp_info",
		mcp.WithDescription("Get ESP device info. Returns chip info by default (chip_type, revision, flash_id, flash_size). Pass include=security for secure boot/flash encryption status, or include=chip,security for both."),
		mcp.WithString("port", mcp.Required(), mcp.Description("Serial port")),
		mcp.WithNumber("baud", mcp.Description("Connection baud rate (default 115200)")),
		mcp.WithString("reset_mode", mcp.Description("Reset mode: auto (default), default, usb_jtag, no_reset")),
		mcp.WithString("include", mcp.Description("Info to return: chip (default), security, or chip,security")),
	)
	s.AddTool(espInfoTool, withRecover(handleESPInfo))

	espRegisterTool := mcp.NewTool("esp_register",
		mcp.WithDescription("Read or write a 32-bit ESP register. Omit value to read; provide value to write. Returns {value: hex string} in both cases."),
		mcp.WithString("port", mcp.Required(), mcp.Description("Serial port")),
		mcp.WithNumber("address", mcp.Required(), mcp.Description("Register address")),
		mcp.WithNumber("value", mcp.Description("Value to write (omit to read)")),
		mcp.WithNumber("baud", mcp.Description("Connection baud rate (default 115200)")),
		mcp.WithString("reset_mode", mcp.Description("Reset mode: auto (default), default, usb_jtag, no_reset")),
	)
	s.AddTool(espRegisterTool, withRecover(handleRegister))

	espResetTool := mcp.NewTool("esp_reset",
		mcp.WithDescription("Reset an ESP device via bootloader. Returns new_port if USB CDC re-enumerated. Captures boot_output after reset."),
		mcp.WithString("port", mcp.Required(), mcp.Description("Serial port")),
		mcp.WithString("reset_mode", mcp.Description("Reset mode: auto (default), default, usb_jtag, no_reset")),
		mcp.WithNumber("boot_wait", mcp.Description("Seconds to wait for boot output after reset (default 2; 0 = skip)")),
	)
	s.AddTool(espResetTool, withRecover(handleReset))

	espReadFlashTool := mcp.NewTool("esp_read_flash",
		mcp.WithDescription("Read from ESP flash. Returns base64-encoded raw bytes by default, or MD5 hash if md5=true."),
		mcp.WithString("port", mcp.Required(), mcp.Description("Serial port")),
		mcp.WithNumber("offset", mcp.Required(), mcp.Description("Flash offset")),
		mcp.WithNumber("size", mcp.Required(), mcp.Description("Size in bytes")),
		mcp.WithNumber("baud", mcp.Description("Connection baud rate (default 115200)")),
		mcp.WithString("reset_mode", mcp.Description("Reset mode: auto (default), default, usb_jtag, no_reset")),
		mcp.WithBoolean("md5", mcp.Description("Return MD5 hash instead of raw bytes (default false)")),
	)
	s.AddTool(espReadFlashTool, withRecover(handleESPReadFlash))

	espReadNVSTool := mcp.NewTool("esp_read_nvs",
		mcp.WithDescription("Read and parse NVS entries from ESP flash. Returns all key-value pairs; use namespace to filter."),
		mcp.WithString("port", mcp.Required(), mcp.Description("Serial port")),
		mcp.WithNumber("offset", mcp.Description("NVS partition offset (default 0x9000)")),
		mcp.WithNumber("size", mcp.Description("NVS partition size (default 0x6000)")),
		mcp.WithString("namespace", mcp.Description("Filter by namespace")),
		mcp.WithNumber("baud", mcp.Description("Connection baud rate (default 115200)")),
		mcp.WithString("reset_mode", mcp.Description("Reset mode: auto (default), default, usb_jtag, no_reset")),
	)
	s.AddTool(espReadNVSTool, withRecover(handleReadNVS))

	espWriteNVSTool := mcp.NewTool("esp_write_nvs",
		mcp.WithDescription("Write NVS entries to ESP flash. REPLACES entire NVS partition. entries is an array of {namespace, key, type, value} objects."),
		mcp.WithString("port", mcp.Required(), mcp.Description("Serial port")),
		mcp.WithArray("entries", mcp.Required(), mcp.Description("Array of {namespace, key, type, value} objects")),
		mcp.WithNumber("offset", mcp.Description("NVS partition offset (default 0x9000)")),
		mcp.WithNumber("size", mcp.Description("NVS partition size (default 0x6000)")),
		mcp.WithNumber("baud", mcp.Description("Connection baud rate (default 115200)")),
		mcp.WithString("reset_mode", mcp.Description("Reset mode: auto (default), default, usb_jtag, no_reset")),
	)
	s.AddTool(espWriteNVSTool, withRecover(handleWriteNVS))

	espNVSSetTool := mcp.NewTool("esp_nvs_set",
		mcp.WithDescription("Set one or more NVS keys in a single read-modify-write cycle. entries is an array of {namespace, key, type, value} objects. type must be one of: u8, u16, u32, i8, i16, i32, string."),
		mcp.WithString("port", mcp.Required(), mcp.Description("Serial port")),
		mcp.WithArray("entries", mcp.Required(), mcp.Description("Array of {namespace, key, type, value} objects")),
		mcp.WithNumber("offset", mcp.Description("NVS partition offset (default 0x9000)")),
		mcp.WithNumber("size", mcp.Description("NVS partition size (default 0x6000)")),
		mcp.WithNumber("baud", mcp.Description("Connection baud rate (default 115200)")),
		mcp.WithString("reset_mode", mcp.Description("Reset mode: auto (default), default, usb_jtag, no_reset")),
	)
	s.AddTool(espNVSSetTool, withRecover(handleNVSSet))

	espNVSDeleteTool := mcp.NewTool("esp_nvs_delete",
		mcp.WithDescription("Delete an NVS key or namespace (read-modify-write). Omit key to delete entire namespace."),
		mcp.WithString("port", mcp.Required(), mcp.Description("Serial port")),
		mcp.WithString("namespace", mcp.Required(), mcp.Description("NVS namespace")),
		mcp.WithString("key", mcp.Description("Key to delete (omit to delete entire namespace)")),
		mcp.WithNumber("offset", mcp.Description("NVS partition offset (default 0x9000)")),
		mcp.WithNumber("size", mcp.Description("NVS partition size (default 0x6000)")),
		mcp.WithNumber("baud", mcp.Description("Connection baud rate (default 115200)")),
		mcp.WithString("reset_mode", mcp.Description("Reset mode: auto (default), default, usb_jtag, no_reset")),
	)
	s.AddTool(espNVSDeleteTool, withRecover(handleNVSDelete))
}
