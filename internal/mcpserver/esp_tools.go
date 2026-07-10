package mcpserver

import (
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func registerESPTools(s *server.MCPServer) {
	espFlashTool := newTool("esp_flash",
		mcp.WithDescription("Flash firmware to ESP chip using native Go flasher. images is an array of {path: string, offset: number} objects. Returns JSON with chip, flash_size, images_written, bytes_written, and new_port (if USB CDC re-enumerated). Captures boot_output (up to 100 lines) after flash."),
		mcp.WithString("port", mcp.Required(), mcp.Description("Serial port")),
		mcp.WithArray("images", mcp.Required(), mcp.Description("Array of {path, offset} objects")),
		mcp.WithNumber("baud", mcp.Description("Connection baud rate (default 115200)")),
		mcp.WithNumber("flash_baud", mcp.Description("Flash transfer baud rate (default 460800)")),
		mcp.WithString("reset_mode", mcp.Description("Reset mode: auto (default), default, usb_jtag, no_reset")),
		mcp.WithString("flash_mode", mcp.Description("Flash mode: dio, dout, qio, qout")),
		mcp.WithString("flash_size", mcp.Description("Flash size: 1MB, 2MB, 4MB, 8MB, 16MB")),
		mcp.WithString("chip_type", mcp.Description("Chip type: esp32, esp32s3, esp32c6, etc (default auto-detect)")),
		mcp.WithBoolean("force_offsets", mcp.Description("Skip partition-table validation. Use for factory-flash (combined firmware.factory.bin at 0x0), recovery from erased chip, or non-standard layouts. DESTRUCTIVE if misused.")),
		mcp.WithNumber("boot_wait", mcp.Description("Seconds to wait for boot output after reset (default 2; 0 = skip)")),
	)
	s.AddTool(espFlashTool, withRecover(handleFlash))

	espEraseTool := newTool("esp_erase",
		mcp.WithDescription("Erase flash on ESP chip. Omit offset to erase the ENTIRE chip (DESTRUCTIVE); provide offset+size to erase only that region (safer). Returns new_port if USB CDC re-enumerated. Captures boot_output after erase."),
		mcp.WithString("port", mcp.Required(), mcp.Description("Serial port")),
		mcp.WithNumber("baud", mcp.Description("Connection baud rate (default 115200)")),
		mcp.WithNumber("offset", mcp.Description("Erase offset (optional; omit to erase entire chip)")),
		mcp.WithNumber("size", mcp.Description("Erase size in bytes (required if offset is specified)")),
		mcp.WithString("reset_mode", mcp.Description("Reset mode: auto (default), default, usb_jtag, no_reset")),
		mcp.WithNumber("boot_wait", mcp.Description("Seconds to wait for boot output after reset (default 2; 0 = skip)")),
	)
	s.AddTool(espEraseTool, withRecover(handleErase))

	espInfoTool := newTool("esp_info",
		mcp.WithDescription("Get ESP device info. Returns chip info by default (chip_type, revision, flash_id, flash_size). Pass include=security for secure boot/flash encryption status, or include=chip,security for both."),
		mcp.WithString("port", mcp.Required(), mcp.Description("Serial port")),
		mcp.WithNumber("baud", mcp.Description("Connection baud rate (default 115200)")),
		mcp.WithString("reset_mode", mcp.Description("Reset mode: auto (default), default, usb_jtag, no_reset")),
		mcp.WithString("include", mcp.Description("Info to return: chip (default), security, or chip,security")),
	)
	s.AddTool(espInfoTool, withRecover(handleESPInfo))

	espRegisterTool := newTool("esp_register",
		mcp.WithDescription("Read or write a 32-bit ESP register. Omit value to read; provide value to write. Returns {value: hex string} in both cases."),
		mcp.WithString("port", mcp.Required(), mcp.Description("Serial port")),
		mcp.WithNumber("address", mcp.Required(), mcp.Description("Register address")),
		mcp.WithNumber("value", mcp.Description("Value to write (omit to read)")),
		mcp.WithNumber("baud", mcp.Description("Connection baud rate (default 115200)")),
		mcp.WithString("reset_mode", mcp.Description("Reset mode: auto (default), default, usb_jtag, no_reset")),
	)
	s.AddTool(espRegisterTool, withRecover(handleRegister))

	espResetTool := newTool("esp_reset",
		mcp.WithDescription("Reset an ESP device via bootloader. Returns new_port if USB CDC re-enumerated. Captures boot_output after reset."),
		mcp.WithString("port", mcp.Required(), mcp.Description("Serial port")),
		mcp.WithString("reset_mode", mcp.Description("Reset mode: auto (default), default, usb_jtag, no_reset")),
		mcp.WithNumber("boot_wait", mcp.Description("Seconds to wait for boot output after reset (default 2; 0 = skip)")),
	)
	s.AddTool(espResetTool, withRecover(handleReset))

	espReadFlashTool := newTool("esp_read_flash",
		mcp.WithDescription("Read from ESP flash. Returns base64-encoded raw bytes by default, or MD5 hash if md5=true."),
		mcp.WithString("port", mcp.Required(), mcp.Description("Serial port")),
		mcp.WithNumber("offset", mcp.Required(), mcp.Description("Flash offset")),
		mcp.WithNumber("size", mcp.Required(), mcp.Description("Size in bytes")),
		mcp.WithNumber("baud", mcp.Description("Connection baud rate (default 115200)")),
		mcp.WithString("reset_mode", mcp.Description("Reset mode: auto (default), default, usb_jtag, no_reset")),
		mcp.WithBoolean("md5", mcp.Description("Return MD5 hash instead of raw bytes (default false)")),
	)
	s.AddTool(espReadFlashTool, withRecover(handleESPReadFlash))

	espReadNVSTool := newTool("esp_read_nvs",
		mcp.WithDescription("Read and parse NVS entries from ESP flash. Returns all key-value pairs; use namespace to filter."),
		mcp.WithString("port", mcp.Required(), mcp.Description("Serial port")),
		mcp.WithNumber("offset", mcp.Description("NVS partition offset (default 0x9000)")),
		mcp.WithNumber("size", mcp.Description("NVS partition size (default 0x6000)")),
		mcp.WithString("namespace", mcp.Description("Filter by namespace")),
		mcp.WithNumber("baud", mcp.Description("Connection baud rate (default 115200)")),
		mcp.WithString("reset_mode", mcp.Description("Reset mode: auto (default), default, usb_jtag, no_reset")),
	)
	s.AddTool(espReadNVSTool, withRecover(handleReadNVS))

	espWriteNVSTool := newTool("esp_write_nvs",
		mcp.WithDescription("Write NVS entries to ESP flash. DESTRUCTIVE — REPLACES the entire NVS partition, dropping any keys not included in entries. entries is an array of {namespace, key, type, value} objects. For adding/updating/removing individual keys without touching the rest of the partition, use esp_nvs_set/esp_nvs_delete instead."),
		mcp.WithString("port", mcp.Required(), mcp.Description("Serial port")),
		mcp.WithArray("entries", mcp.Required(), mcp.Description("Array of {namespace, key, type, value} objects")),
		mcp.WithNumber("offset", mcp.Description("NVS partition offset (default 0x9000)")),
		mcp.WithNumber("size", mcp.Description("NVS partition size (default 0x6000)")),
		mcp.WithNumber("baud", mcp.Description("Connection baud rate (default 115200)")),
		mcp.WithString("reset_mode", mcp.Description("Reset mode: auto (default), default, usb_jtag, no_reset")),
	)
	s.AddTool(espWriteNVSTool, withRecover(handleWriteNVS))

	espNVSSetTool := newTool("esp_nvs_set",
		mcp.WithDescription("Set one or more NVS keys in a single read-modify-write cycle. entries is an array of {namespace, key, type, value} objects. type must be one of: u8, u16, u32, i8, i16, i32, string."),
		mcp.WithString("port", mcp.Required(), mcp.Description("Serial port")),
		mcp.WithArray("entries", mcp.Required(), mcp.Description("Array of {namespace, key, type, value} objects")),
		mcp.WithNumber("offset", mcp.Description("NVS partition offset (default 0x9000)")),
		mcp.WithNumber("size", mcp.Description("NVS partition size (default 0x6000)")),
		mcp.WithNumber("baud", mcp.Description("Connection baud rate (default 115200)")),
		mcp.WithString("reset_mode", mcp.Description("Reset mode: auto (default), default, usb_jtag, no_reset")),
	)
	s.AddTool(espNVSSetTool, withRecover(handleNVSSet))

	espNVSDeleteTool := newTool("esp_nvs_delete",
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

	espGPIOReadTool := newTool("esp_gpio_read",
		mcp.WithDescription("Read the current level of a single ESP GPIO pin directly against the ROM/stub bootloader's memory-mapped GPIO registers — no firmware needs to be running. Returns {pin, level}."),
		mcp.WithString("port", mcp.Required(), mcp.Description("Serial port")),
		mcp.WithNumber("pin", mcp.Required(), mcp.Description("GPIO pin number")),
		mcp.WithNumber("baud", mcp.Description("Connection baud rate (default 115200)")),
		mcp.WithString("reset_mode", mcp.Description("Reset mode: auto (default), default, usb_jtag, no_reset")),
	)
	s.AddTool(espGPIOReadTool, withRecover(handleGPIORead))

	espGPIOSetTool := newTool("esp_gpio_set",
		mcp.WithDescription("Drive a single ESP GPIO pin high or low directly against the ROM/stub bootloader — no firmware needs to be running. level accepts true/false or 1/0. Refuses reserved pins (flash/PSRAM, strapping, UART0, USB-JTAG) by default — pass include_reserved=true to override. Always refuses input-only/nonexistent pins regardless of include_reserved (the underlying error is surfaced verbatim)."),
		mcp.WithString("port", mcp.Required(), mcp.Description("Serial port")),
		mcp.WithNumber("pin", mcp.Required(), mcp.Description("GPIO pin number")),
		mcp.WithBoolean("level", mcp.Required(), mcp.Description("Level to drive: true/1 for high, false/0 for low")),
		mcp.WithBoolean("include_reserved", mcp.Description("Drive pins normally refused as reserved (flash/PSRAM, strapping, UART0, USB-JTAG) (default false)")),
		mcp.WithNumber("baud", mcp.Description("Connection baud rate (default 115200)")),
		mcp.WithString("reset_mode", mcp.Description("Reset mode: auto (default), default, usb_jtag, no_reset")),
	)
	s.AddTool(espGPIOSetTool, withRecover(handleGPIOSet))

	espGPIOSweepTool := newTool("esp_gpio_sweep",
		mcp.WithDescription("Sweep a set of ESP GPIO pins over a single connection, driving each in turn and dwelling on each polarity — useful for finding which pin drives an unlabeled LED/relay without reflashing. pins is a comma-separated list and/or inclusive ranges (e.g. \"4,16,17\" or \"0-21\"); omit or empty to sweep every drivable (non-reserved) pin the detected chip exposes. Reserved pins (flash/PSRAM, strapping, UART0, USB-JTAG, input-only) are skipped by default — pass include_reserved=true to force them. Returns {pins: [{pin, skipped, reason}]}. Emits MCP progress notifications per pin."),
		mcp.WithString("port", mcp.Required(), mcp.Description("Serial port")),
		mcp.WithString("pins", mcp.Description("Comma-separated pin list and/or ranges, e.g. \"4,16,17\" or \"0-21\" (omit/empty for every drivable pin)")),
		mcp.WithNumber("dwell", mcp.Description("Seconds to hold each polarity before advancing (default 5)")),
		mcp.WithBoolean("both", mcp.Description("Drive each pin both high and low (default true)")),
		mcp.WithBoolean("include_reserved", mcp.Description("Sweep pins normally skipped as reserved (flash/PSRAM, strapping, UART0, USB-JTAG, input-only) (default false)")),
		mcp.WithNumber("baud", mcp.Description("Connection baud rate (default 115200)")),
		mcp.WithString("reset_mode", mcp.Description("Reset mode: auto (default), default, usb_jtag, no_reset")),
	)
	s.AddTool(espGPIOSweepTool, withRecover(handleGPIOSweep))
}
