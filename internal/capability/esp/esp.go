// Package esp is the shesha port of the esp_* tools (MC-12): esp_flash,
// esp_erase, esp_info, esp_register, esp_reset, esp_read_flash,
// esp_read_nvs, esp_write_nvs, esp_nvs_set, esp_nvs_delete, esp_gpio_read,
// esp_gpio_set, and esp_gpio_sweep. The underlying domain logic
// (internal/esp, internal/session) is unchanged; only the MCP
// registration/handler seam moves. All 13 tools join the lazily-unlocked
// "hardware" tool group (shesha.Group), mirroring the mark3labs-based
// server's registerHardwareTools tier.
package esp

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/dangernoodle-io/shesha"
	"github.com/dangernoodle-io/shesha/mcpx"
	espflasher "tinygo.org/x/espflasher/pkg/espflasher"
	"tinygo.org/x/espflasher/pkg/nvs"

	"dangernoodle.io/pogopin/internal/esp"
	"dangernoodle.io/pogopin/internal/mcpprogress"
	"dangernoodle.io/pogopin/internal/session"
)

// hardwareGroup is the shesha tool group these tools join. Deliberately
// unexported: the lazy-unlock wiring lives in internal/mcpapp, which locks
// and unlocks this same group name by its own const.
const hardwareGroup = "hardware"

// gpioDefaultDwell mirrors the CLI's --dwell default (internal/cli/gpio.go)
// so the MCP tool and the CLI subcommand present identical defaults.
const gpioDefaultDwell = 5 * time.Second

// ImageIn is one entry of esp_flash's images array.
type ImageIn struct {
	// Path is the firmware image file path. Required.
	Path string `json:"path" jsonschema:"firmware image file path"`
	// Offset is the flash offset to write the image at. Required.
	Offset uint32 `json:"offset" jsonschema:"flash offset to write the image at"`
}

// FlashIn is esp_flash's input.
type FlashIn struct {
	// Port is the serial port name. Required.
	Port string `json:"port" jsonschema:"serial port"`
	// Images is an array of {path, offset} objects. Required.
	Images []ImageIn `json:"images" jsonschema:"array of {path, offset} objects"`
	// Baud is the connection baud rate. Defaults to 115200 (esp.FlashESP)
	// when zero/omitted.
	Baud int `json:"baud,omitempty" jsonschema:"connection baud rate (default 115200)"`
	// FlashBaud is the flash transfer baud rate. Defaults to 460800
	// (esp.FlashESP) when zero/omitted.
	FlashBaud int `json:"flash_baud,omitempty" jsonschema:"flash transfer baud rate (default 460800)"`
	// ResetMode selects the reset strategy. Empty means auto.
	ResetMode string `json:"reset_mode,omitempty" jsonschema:"reset mode: auto (default), default, usb_jtag, no_reset"`
	// FlashMode is the SPI flash mode.
	FlashMode string `json:"flash_mode,omitempty" jsonschema:"flash mode: dio, dout, qio, qout"`
	// FlashSize is the target flash size.
	FlashSize string `json:"flash_size,omitempty" jsonschema:"flash size: 1MB, 2MB, 4MB, 8MB, 16MB"`
	// ChipType overrides auto-detection. Empty means auto-detect.
	ChipType string `json:"chip_type,omitempty" jsonschema:"chip type: esp32, esp32s3, esp32c6, etc (default auto-detect)"`
	// ForceOffsets skips partition-table offset validation. DESTRUCTIVE if
	// misused.
	ForceOffsets bool `json:"force_offsets,omitempty" jsonschema:"skip partition-table validation. Use for factory-flash (combined firmware.factory.bin at 0x0), recovery from erased chip, or non-standard layouts. DESTRUCTIVE if misused."`
	// BootWait is how long to wait for boot output after reset. A nil
	// value defaults to 2 seconds; an explicit 0 skips boot capture
	// entirely — this distinction is why the field is a pointer.
	BootWait *float64 `json:"boot_wait,omitempty" jsonschema:"seconds to wait for boot output after reset (default 2; 0 = skip)"`
}

// EraseIn is esp_erase's input.
type EraseIn struct {
	// Port is the serial port name. Required.
	Port string `json:"port" jsonschema:"serial port"`
	// Baud is the connection baud rate. Defaults to 115200 when
	// zero/omitted.
	Baud int `json:"baud,omitempty" jsonschema:"connection baud rate (default 115200)"`
	// Offset is the erase offset. Omit to erase the entire chip
	// (DESTRUCTIVE); a nil value distinguishes "omitted" from an explicit
	// offset=0.
	Offset *uint32 `json:"offset,omitempty" jsonschema:"erase offset (optional; omit to erase entire chip)"`
	// Size is the erase size in bytes. Required when Offset is set.
	Size *uint32 `json:"size,omitempty" jsonschema:"erase size in bytes (required if offset is specified)"`
	// ResetMode selects the reset strategy. Empty means auto.
	ResetMode string `json:"reset_mode,omitempty" jsonschema:"reset mode: auto (default), default, usb_jtag, no_reset"`
	// BootWait is how long to wait for boot output after reset. A nil
	// value defaults to 2 seconds; an explicit 0 skips boot capture.
	BootWait *float64 `json:"boot_wait,omitempty" jsonschema:"seconds to wait for boot output after reset (default 2; 0 = skip)"`
}

// InfoIn is esp_info's input.
type InfoIn struct {
	// Port is the serial port name. Required.
	Port string `json:"port" jsonschema:"serial port"`
	// Baud is the connection baud rate. Defaults to 115200 when
	// zero/omitted.
	Baud int `json:"baud,omitempty" jsonschema:"connection baud rate (default 115200)"`
	// ResetMode selects the reset strategy. Empty means auto.
	ResetMode string `json:"reset_mode,omitempty" jsonschema:"reset mode: auto (default), default, usb_jtag, no_reset"`
	// Include selects which sections to return: chip (default), security,
	// or a comma-separated combination.
	Include string `json:"include,omitempty" jsonschema:"info to return: chip (default), security, or chip,security"`
}

// RegisterIn is esp_register's input.
type RegisterIn struct {
	// Port is the serial port name. Required.
	Port string `json:"port" jsonschema:"serial port"`
	// Address is the register address. Required.
	Address uint32 `json:"address" jsonschema:"register address"`
	// Value, when set, puts the tool in write mode; omit to read.
	Value *uint32 `json:"value,omitempty" jsonschema:"value to write (omit to read)"`
	// Baud is the connection baud rate. Defaults to 115200 when
	// zero/omitted.
	Baud int `json:"baud,omitempty" jsonschema:"connection baud rate (default 115200)"`
	// ResetMode selects the reset strategy. Empty means auto.
	ResetMode string `json:"reset_mode,omitempty" jsonschema:"reset mode: auto (default), default, usb_jtag, no_reset"`
}

// ResetIn is esp_reset's input.
type ResetIn struct {
	// Port is the serial port name. Required.
	Port string `json:"port" jsonschema:"serial port"`
	// ResetMode selects the reset strategy. Empty means auto.
	ResetMode string `json:"reset_mode,omitempty" jsonschema:"reset mode: auto (default), default, usb_jtag, no_reset"`
	// BootWait is how long to wait for boot output after reset. A nil
	// value defaults to 2 seconds; an explicit 0 skips boot capture.
	BootWait *float64 `json:"boot_wait,omitempty" jsonschema:"seconds to wait for boot output after reset (default 2; 0 = skip)"`
}

// ReadFlashIn is esp_read_flash's input.
type ReadFlashIn struct {
	// Port is the serial port name. Required.
	Port string `json:"port" jsonschema:"serial port"`
	// Offset is the flash offset to read from. Required.
	Offset uint32 `json:"offset" jsonschema:"flash offset"`
	// Size is the number of bytes to read. Required.
	Size uint32 `json:"size" jsonschema:"size in bytes"`
	// Baud is the connection baud rate. Defaults to 115200 when
	// zero/omitted.
	Baud int `json:"baud,omitempty" jsonschema:"connection baud rate (default 115200)"`
	// ResetMode selects the reset strategy. Empty means auto.
	ResetMode string `json:"reset_mode,omitempty" jsonschema:"reset mode: auto (default), default, usb_jtag, no_reset"`
	// MD5 returns an MD5 hash instead of raw bytes when true.
	MD5 bool `json:"md5,omitempty" jsonschema:"return MD5 hash instead of raw bytes (default false)"`
}

// ReadNVSIn is esp_read_nvs's input.
type ReadNVSIn struct {
	// Port is the serial port name. Required.
	Port string `json:"port" jsonschema:"serial port"`
	// Offset is the NVS partition offset. A nil value defaults to 0x9000.
	Offset *uint32 `json:"offset,omitempty" jsonschema:"NVS partition offset (default 0x9000)"`
	// Size is the NVS partition size. A nil value defaults to
	// nvs.DefaultPartSize (0x6000).
	Size *uint32 `json:"size,omitempty" jsonschema:"NVS partition size (default 0x6000)"`
	// Namespace filters returned entries by namespace.
	Namespace string `json:"namespace,omitempty" jsonschema:"filter by namespace"`
	// Baud is the connection baud rate. Defaults to 115200 when
	// zero/omitted.
	Baud int `json:"baud,omitempty" jsonschema:"connection baud rate (default 115200)"`
	// ResetMode selects the reset strategy. Empty means auto.
	ResetMode string `json:"reset_mode,omitempty" jsonschema:"reset mode: auto (default), default, usb_jtag, no_reset"`
}

// NVSEntryIn is one entry of esp_write_nvs's entries array.
type NVSEntryIn struct {
	// Namespace is the NVS namespace. Required.
	Namespace string `json:"namespace" jsonschema:"NVS namespace"`
	// Key is the NVS key. Required.
	Key string `json:"key" jsonschema:"NVS key"`
	// Type is the NVS value type: u8, u16, u32, i8, i16, i32, or string.
	// Required.
	Type string `json:"type" jsonschema:"NVS value type: u8, u16, u32, i8, i16, i32, or string"`
	// Value is the entry's value, typed per Type (a JSON number for
	// numeric types, a JSON string for "string").
	Value any `json:"value" jsonschema:"entry value, typed per type"`
}

// WriteNVSIn is esp_write_nvs's input.
type WriteNVSIn struct {
	// Port is the serial port name. Required.
	Port string `json:"port" jsonschema:"serial port"`
	// Entries is an array of {namespace, key, type, value} objects.
	// Required.
	Entries []NVSEntryIn `json:"entries" jsonschema:"array of {namespace, key, type, value} objects"`
	// Offset is the NVS partition offset. A nil value defaults to 0x9000.
	Offset *uint32 `json:"offset,omitempty" jsonschema:"NVS partition offset (default 0x9000)"`
	// Size is the NVS partition size. A nil value defaults to
	// nvs.DefaultPartSize (0x6000).
	Size *uint32 `json:"size,omitempty" jsonschema:"NVS partition size (default 0x6000)"`
	// Baud is the connection baud rate. Defaults to 115200 when
	// zero/omitted.
	Baud int `json:"baud,omitempty" jsonschema:"connection baud rate (default 115200)"`
	// ResetMode selects the reset strategy. Empty means auto.
	ResetMode string `json:"reset_mode,omitempty" jsonschema:"reset mode: auto (default), default, usb_jtag, no_reset"`
}

// NVSSetEntryIn is one entry of esp_nvs_set's entries array. Value is
// always a string here (unlike NVSEntryIn) — esp_nvs_set parses it from
// string per Type via parseNVSValueFromString, mirroring the
// mark3labs-based handler this replaces.
type NVSSetEntryIn struct {
	// Namespace is the NVS namespace. Required.
	Namespace string `json:"namespace" jsonschema:"NVS namespace"`
	// Key is the NVS key. Required.
	Key string `json:"key" jsonschema:"NVS key"`
	// Type is the NVS value type: u8, u16, u32, i8, i16, i32, or string.
	// Required.
	Type string `json:"type" jsonschema:"NVS value type: u8, u16, u32, i8, i16, i32, or string"`
	// Value is the entry's value, as a string to be parsed per Type.
	Value string `json:"value" jsonschema:"entry value as a string, parsed per type"`
}

// NVSSetIn is esp_nvs_set's input.
type NVSSetIn struct {
	// Port is the serial port name. Required.
	Port string `json:"port" jsonschema:"serial port"`
	// Entries is an array of {namespace, key, type, value} objects.
	// Required.
	Entries []NVSSetEntryIn `json:"entries" jsonschema:"array of {namespace, key, type, value} objects"`
	// Offset is the NVS partition offset. A nil value defaults to 0x9000.
	Offset *uint32 `json:"offset,omitempty" jsonschema:"NVS partition offset (default 0x9000)"`
	// Size is the NVS partition size. A nil value defaults to
	// nvs.DefaultPartSize (0x6000).
	Size *uint32 `json:"size,omitempty" jsonschema:"NVS partition size (default 0x6000)"`
	// Baud is the connection baud rate. Defaults to 115200 when
	// zero/omitted.
	Baud int `json:"baud,omitempty" jsonschema:"connection baud rate (default 115200)"`
	// ResetMode selects the reset strategy. Empty means auto.
	ResetMode string `json:"reset_mode,omitempty" jsonschema:"reset mode: auto (default), default, usb_jtag, no_reset"`
}

// NVSDeleteIn is esp_nvs_delete's input.
type NVSDeleteIn struct {
	// Port is the serial port name. Required.
	Port string `json:"port" jsonschema:"serial port"`
	// Namespace is the NVS namespace to delete from. Required.
	Namespace string `json:"namespace" jsonschema:"NVS namespace"`
	// Key is the key to delete; omit to delete the entire namespace.
	Key string `json:"key,omitempty" jsonschema:"key to delete (omit to delete entire namespace)"`
	// Offset is the NVS partition offset. A nil value defaults to 0x9000.
	Offset *uint32 `json:"offset,omitempty" jsonschema:"NVS partition offset (default 0x9000)"`
	// Size is the NVS partition size. A nil value defaults to
	// nvs.DefaultPartSize (0x6000).
	Size *uint32 `json:"size,omitempty" jsonschema:"NVS partition size (default 0x6000)"`
	// Baud is the connection baud rate. Defaults to 115200 when
	// zero/omitted.
	Baud int `json:"baud,omitempty" jsonschema:"connection baud rate (default 115200)"`
	// ResetMode selects the reset strategy. Empty means auto.
	ResetMode string `json:"reset_mode,omitempty" jsonschema:"reset mode: auto (default), default, usb_jtag, no_reset"`
}

// GPIOReadIn is esp_gpio_read's input.
type GPIOReadIn struct {
	// Port is the serial port name. Required.
	Port string `json:"port" jsonschema:"serial port"`
	// Pin is the GPIO pin number. Required.
	Pin int `json:"pin" jsonschema:"GPIO pin number"`
	// Baud is the connection baud rate. Defaults to 115200 when
	// zero/omitted.
	Baud int `json:"baud,omitempty" jsonschema:"connection baud rate (default 115200)"`
	// ResetMode selects the reset strategy. Empty means auto.
	ResetMode string `json:"reset_mode,omitempty" jsonschema:"reset mode: auto (default), default, usb_jtag, no_reset"`
}

// GPIOSetIn is esp_gpio_set's input.
type GPIOSetIn struct {
	// Port is the serial port name. Required.
	Port string `json:"port" jsonschema:"serial port"`
	// Pin is the GPIO pin number. Required.
	Pin int `json:"pin" jsonschema:"GPIO pin number"`
	// Level is the level to drive. Accepts a JSON boolean (true for high,
	// false for low) or a numeric 0/1, matching the pre-migration mcp-go
	// handler's parseGPIOLevel (MC-12 review). `any`-typed (like
	// NVSEntryIn.Value below) rather than bool: a strict bool field's
	// inferred JSON schema type ("boolean") rejects a numeric 0/1 outright,
	// before the value ever reaches the handler. Required; parsed by
	// parseGPIOLevel in the handler.
	Level any `json:"level" jsonschema:"level to drive: true/1 for high, false/0 for low"`
	// IncludeReserved drives pins normally refused as reserved
	// (flash/PSRAM, strapping, UART0, USB-JTAG) when true.
	IncludeReserved bool `json:"include_reserved,omitempty" jsonschema:"drive pins normally refused as reserved (flash/PSRAM, strapping, UART0, USB-JTAG) (default false)"`
	// Baud is the connection baud rate. Defaults to 115200 when
	// zero/omitted.
	Baud int `json:"baud,omitempty" jsonschema:"connection baud rate (default 115200)"`
	// ResetMode selects the reset strategy. Empty means auto.
	ResetMode string `json:"reset_mode,omitempty" jsonschema:"reset mode: auto (default), default, usb_jtag, no_reset"`
}

// GPIOSweepIn is esp_gpio_sweep's input.
type GPIOSweepIn struct {
	// Port is the serial port name. Required.
	Port string `json:"port" jsonschema:"serial port"`
	// Pins is a comma-separated pin list and/or ranges (e.g. "4,16,17" or
	// "0-21"); omit or empty to sweep every drivable (non-reserved) pin.
	Pins string `json:"pins,omitempty" jsonschema:"comma-separated pin list and/or ranges, e.g. \"4,16,17\" or \"0-21\" (omit/empty for every drivable pin)"`
	// Dwell is how long to hold each polarity before advancing, in
	// seconds. A nil value defaults to 5s; an explicit 0 means no dwell —
	// this distinction is why the field is a pointer.
	Dwell *float64 `json:"dwell,omitempty" jsonschema:"seconds to hold each polarity before advancing (default 5)"`
	// Both drives each pin both high and low. A nil value defaults to
	// true; an explicit false means single-polarity only — this
	// distinction is why the field is a pointer.
	Both *bool `json:"both,omitempty" jsonschema:"drive each pin both high and low (default true)"`
	// IncludeReserved sweeps pins normally skipped as reserved
	// (flash/PSRAM, strapping, UART0, USB-JTAG, input-only) when true.
	IncludeReserved bool `json:"include_reserved,omitempty" jsonschema:"sweep pins normally skipped as reserved (flash/PSRAM, strapping, UART0, USB-JTAG, input-only) (default false)"`
	// Baud is the connection baud rate. Defaults to 115200 when
	// zero/omitted.
	Baud int `json:"baud,omitempty" jsonschema:"connection baud rate (default 115200)"`
	// ResetMode selects the reset strategy. Empty means auto.
	ResetMode string `json:"reset_mode,omitempty" jsonschema:"reset mode: auto (default), default, usb_jtag, no_reset"`
}

// Capability is the shesha Capability for the ESP tool group. It registers
// all 13 esp_* tools onto the lazily-unlocked "hardware" group.
type Capability struct{}

// Attach registers c's tools against r.
func (c Capability) Attach(r *shesha.Registrar) error {
	shesha.AddTool(r, &mcpx.Tool{
		Name:        "esp_flash",
		Description: "Flash firmware to ESP chip using native Go flasher. images is an array of {path: string, offset: number} objects. Returns JSON with chip, flash_size, images_written, bytes_written, and new_port (if USB CDC re-enumerated). Captures boot_output (up to 100 lines) after flash.",
	}, shesha.Destructive, handleFlash, shesha.Group(hardwareGroup))

	shesha.AddTool(r, &mcpx.Tool{
		Name:        "esp_erase",
		Description: "Erase flash on ESP chip. Omit offset to erase the ENTIRE chip (DESTRUCTIVE); provide offset+size to erase only that region (safer). Returns new_port if USB CDC re-enumerated. Captures boot_output after erase.",
	}, shesha.Destructive, handleErase, shesha.Group(hardwareGroup))

	shesha.AddTool(r, &mcpx.Tool{
		Name:        "esp_info",
		Description: "Get ESP device info. Returns chip info by default (chip_name, manufacturer_id, device_id, flash_size). Pass include=security for secure boot/flash encryption status, or include=chip,security for both.",
	}, shesha.ReadOnly, handleESPInfo, shesha.Group(hardwareGroup))

	shesha.AddTool(r, &mcpx.Tool{
		Name:        "esp_register",
		Description: "Read or write a 32-bit ESP register. Omit value to read; provide value to write. Returns {value: hex string} in both cases.",
	}, shesha.Write, handleRegister, shesha.Group(hardwareGroup))

	shesha.AddTool(r, &mcpx.Tool{
		Name:        "esp_reset",
		Description: "Reset an ESP device via bootloader. Returns new_port if USB CDC re-enumerated. Captures boot_output after reset.",
	}, shesha.Write, handleReset, shesha.Group(hardwareGroup))

	shesha.AddTool(r, &mcpx.Tool{
		Name:        "esp_read_flash",
		Description: "Read from ESP flash. Returns base64-encoded raw bytes by default, or MD5 hash if md5=true.",
	}, shesha.ReadOnly, handleESPReadFlash, shesha.Group(hardwareGroup))

	shesha.AddTool(r, &mcpx.Tool{
		Name:        "esp_read_nvs",
		Description: "Read and parse NVS entries from ESP flash. Returns all key-value pairs; use namespace to filter.",
	}, shesha.ReadOnly, handleReadNVS, shesha.Group(hardwareGroup))

	shesha.AddTool(r, &mcpx.Tool{
		Name:        "esp_write_nvs",
		Description: "Write NVS entries to ESP flash. DESTRUCTIVE — REPLACES the entire NVS partition, dropping any keys not included in entries. entries is an array of {namespace, key, type, value} objects. For adding/updating/removing individual keys without touching the rest of the partition, use esp_nvs_set/esp_nvs_delete instead.",
	}, shesha.Destructive, handleWriteNVS, shesha.Group(hardwareGroup))

	shesha.AddTool(r, &mcpx.Tool{
		Name:        "esp_nvs_set",
		Description: "Set one or more NVS keys in a single read-modify-write cycle. entries is an array of {namespace, key, type, value} objects. type must be one of: u8, u16, u32, i8, i16, i32, string.",
	}, shesha.Write, handleNVSSet, shesha.Group(hardwareGroup))

	shesha.AddTool(r, &mcpx.Tool{
		Name:        "esp_nvs_delete",
		Description: "Delete an NVS key or namespace (read-modify-write). Omit key to delete entire namespace.",
	}, shesha.Write, handleNVSDelete, shesha.Group(hardwareGroup))

	shesha.AddTool(r, &mcpx.Tool{
		Name:        "esp_gpio_read",
		Description: "Read the current level of a single ESP GPIO pin directly against the ROM/stub bootloader's memory-mapped GPIO registers — no firmware needs to be running. Returns {pin, level}.",
	}, shesha.ReadOnly, handleGPIORead, shesha.Group(hardwareGroup))

	shesha.AddTool(r, &mcpx.Tool{
		Name:        "esp_gpio_set",
		Description: "Drive a single ESP GPIO pin high or low directly against the ROM/stub bootloader — no firmware needs to be running. level accepts true/false. Refuses reserved pins (flash/PSRAM, strapping, UART0, USB-JTAG) by default — pass include_reserved=true to override. Always refuses input-only/nonexistent pins regardless of include_reserved (the underlying error is surfaced verbatim).",
	}, shesha.Destructive, handleGPIOSet, shesha.Group(hardwareGroup))

	shesha.AddTool(r, &mcpx.Tool{
		Name:        "esp_gpio_sweep",
		Description: "Sweep a set of ESP GPIO pins over a single connection, driving each in turn and dwelling on each polarity — useful for finding which pin drives an unlabeled LED/relay without reflashing. pins is a comma-separated list and/or inclusive ranges (e.g. \"4,16,17\" or \"0-21\"); omit or empty to sweep every drivable (non-reserved) pin the detected chip exposes. Reserved pins (flash/PSRAM, strapping, UART0, USB-JTAG, input-only) are skipped by default — pass include_reserved=true to force them. Returns {pins: [{pin, skipped, reason}]}. Emits MCP progress notifications per pin.",
	}, shesha.Destructive, handleGPIOSweep, shesha.Group(hardwareGroup))

	return nil
}

// nvsOffsetSize resolves the NVS partition offset/size, applying the same
// defaults (0x9000 / nvs.DefaultPartSize) as
// internal/mcpserver/helpers.go's parseNVSParams did for an absent
// argument. A pointer nil means "omitted"; a non-nil pointer (including a
// pointer to 0) is honored verbatim.
func nvsOffsetSize(offset, size *uint32) (uint32, uint32) {
	o := uint32(0x9000)
	if offset != nil {
		o = *offset
	}
	s := uint32(nvs.DefaultPartSize)
	if size != nil {
		s = *size
	}
	return o, s
}

func handleFlash(ctx context.Context, req *mcpx.CallToolRequest, in FlashIn) (*mcpx.CallToolResult, any, error) {
	connectEmit := mcpprogress.NewEmitter(mcpprogress.Emitter(ctx, req))
	sess, factory := session.AcquireForFlasher(in.Port, mcpprogress.ConnectStatusEmitter(connectEmit))

	bootWait := 2.0
	if in.BootWait != nil {
		bootWait = *in.BootWait
	}

	var images []esp.ImageSpec
	for _, img := range in.Images {
		images = append(images, esp.ImageSpec{Path: img.Path, Offset: img.Offset})
	}

	opts := esp.FlashOptions{
		BaudRate:      in.Baud,
		FlashBaudRate: in.FlashBaud,
		ResetMode:     in.ResetMode,
		FlashMode:     in.FlashMode,
		FlashSize:     in.FlashSize,
		ChipType:      in.ChipType,
		ForceOffsets:  in.ForceOffsets,
	}

	// Flash. Uses a separate emitter instance from connectEmit above — see
	// ConnectStatusEmitter's doc comment for why sharing one would drop
	// byte-progress ticks after connect's attempt-scale ticks.
	opEmit := mcpprogress.NewEmitter(mcpprogress.Emitter(ctx, req))
	result, err := esp.FlashESP(factory, in.Port, images, opts, func(current, total int) {
		opEmit(current, total, "flashing")
	})
	if err != nil {
		session.ReleaseFlasherImmediate(sess, in.Port)
		if syncResult := handleSyncError(err); syncResult != nil {
			return syncResult, nil, nil
		}
		return mcpx.ErrorResult(err.Error()), nil, nil
	}

	// Detect port re-enumeration and restart managed port.
	newPort := session.ReleaseFlasherImmediate(sess, in.Port)

	bootLines := captureBootOutput(sess, bootWait)

	type flashResponse struct {
		*esp.FlashResult
		NewPort    string   `json:"new_port,omitempty"`
		BootOutput []string `json:"boot_output,omitempty"`
	}
	resp := flashResponse{FlashResult: &result, NewPort: newPort, BootOutput: bootLines}

	data, err := json.MarshalIndent(resp, "", "  ")
	if err != nil {
		return mcpx.ErrorResult(err.Error()), nil, nil
	}

	return mcpx.TextResult(string(data)), nil, nil
}

func handleErase(ctx context.Context, req *mcpx.CallToolRequest, in EraseIn) (*mcpx.CallToolResult, any, error) {
	connectEmit := mcpprogress.NewEmitter(mcpprogress.Emitter(ctx, req))
	sess, factory := session.AcquireForFlasher(in.Port, mcpprogress.ConnectStatusEmitter(connectEmit))

	bootWait := 2.0
	if in.BootWait != nil {
		bootWait = *in.BootWait
	}

	opts := esp.EraseOptions{
		BaudRate:  in.Baud,
		ResetMode: in.ResetMode,
	}

	if in.Offset != nil {
		opts.Offset = in.Offset

		if in.Size == nil {
			session.ReleaseFlasherImmediate(sess, in.Port)
			return mcpx.ErrorResult("size is required when offset is specified"), nil, nil
		}
		opts.Size = in.Size
	}

	// Erase. Separate emitter instance from connectEmit above — see
	// ConnectStatusEmitter's doc comment.
	opEmit := mcpprogress.NewEmitter(mcpprogress.Emitter(ctx, req))
	err := esp.EraseESP(factory, in.Port, opts, espflasher.ProgressFunc(func(current, total int) {
		opEmit(current, total, "erasing")
	}))
	if err != nil {
		session.ReleaseFlasherImmediate(sess, in.Port)
		if syncResult := handleSyncError(err); syncResult != nil {
			return syncResult, nil, nil
		}
		return mcpx.ErrorResult(err.Error()), nil, nil
	}

	// Detect port re-enumeration and restart managed port.
	newPort := session.ReleaseFlasherImmediate(sess, in.Port)

	bootLines := captureBootOutput(sess, bootWait)

	type eraseResponse struct {
		Status     string   `json:"status"`
		NewPort    string   `json:"new_port,omitempty"`
		BootOutput []string `json:"boot_output,omitempty"`
	}
	resp := eraseResponse{Status: "success", NewPort: newPort, BootOutput: bootLines}

	data, err := json.MarshalIndent(resp, "", "  ")
	if err != nil {
		return mcpx.ErrorResult(err.Error()), nil, nil
	}

	return mcpx.TextResult(string(data)), nil, nil
}

func handleESPInfo(ctx context.Context, req *mcpx.CallToolRequest, in InfoIn) (*mcpx.CallToolResult, any, error) {
	done := mcpprogress.LifecycleStatus(ctx, req, "esp_info")
	defer done()

	connectEmit := mcpprogress.NewEmitter(mcpprogress.Emitter(ctx, req))
	sess, factory := session.AcquireForFlasher(in.Port, mcpprogress.ConnectStatusEmitter(connectEmit))
	defer session.ReleaseFlasherDeferred(sess, in.Port)

	// Parse include param (default "chip").
	include := "chip"
	if in.Include != "" {
		include = in.Include
	}

	// Split include on comma to get requested sections.
	sections := make(map[string]bool)
	for _, section := range strings.Split(include, ",") {
		section = strings.TrimSpace(section)
		if section != "" {
			sections[section] = true
		}
	}

	// If no valid sections requested, default to chip.
	if len(sections) == 0 {
		sections["chip"] = true
	}

	result := make(map[string]interface{})

	if sections["chip"] {
		chipInfo, err := esp.GetChipInfo(factory, in.Port, in.Baud, in.ResetMode)
		if err != nil {
			if syncResult := handleSyncError(err); syncResult != nil {
				return syncResult, nil, nil
			}
			return mcpx.ErrorResult(err.Error()), nil, nil
		}
		result["chip"] = chipInfo
	}

	if sections["security"] {
		secInfo, err := esp.GetSecurityInfo(factory, in.Port, in.Baud, in.ResetMode)
		if err != nil {
			if syncResult := handleSyncError(err); syncResult != nil {
				return syncResult, nil, nil
			}
			return mcpx.ErrorResult(err.Error()), nil, nil
		}
		result["security"] = secInfo
	}

	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return mcpx.ErrorResult(err.Error()), nil, nil
	}

	return mcpx.TextResult(string(data)), nil, nil
}

func handleRegister(ctx context.Context, req *mcpx.CallToolRequest, in RegisterIn) (*mcpx.CallToolResult, any, error) {
	done := mcpprogress.LifecycleStatus(ctx, req, "esp_register")
	defer done()

	connectEmit := mcpprogress.NewEmitter(mcpprogress.Emitter(ctx, req))
	sess, factory := session.AcquireForFlasher(in.Port, mcpprogress.ConnectStatusEmitter(connectEmit))
	defer session.ReleaseFlasherDeferred(sess, in.Port)

	// hasValueKey distinguishes an explicit `"value": null` (write mode,
	// then errors "value must be a number" like a non-numeric value would)
	// from an omitted key (read mode) — a *uint32 field alone can't tell
	// these apart, since both decode in.Value to nil (MC-12 review parity
	// fix; matches the pre-migration handler's map-lookup presence check).
	hasValueKey := hasArgKey(req, "value")

	if in.Value == nil && hasValueKey {
		return mcpx.ErrorResult("value must be a number"), nil, nil
	}

	if in.Value != nil {
		// Write mode.
		if err := esp.WriteRegister(factory, in.Port, in.Address, *in.Value, in.Baud, in.ResetMode); err != nil {
			if syncResult := handleSyncError(err); syncResult != nil {
				return syncResult, nil, nil
			}
			return mcpx.ErrorResult(err.Error()), nil, nil
		}

		result := map[string]interface{}{
			"value": fmt.Sprintf("0x%08X", *in.Value),
		}
		data, err := json.MarshalIndent(result, "", "  ")
		if err != nil {
			return mcpx.ErrorResult(err.Error()), nil, nil
		}
		return mcpx.TextResult(string(data)), nil, nil
	}

	// Read mode.
	regVal, err := esp.ReadRegister(factory, in.Port, in.Address, in.Baud, in.ResetMode)
	if err != nil {
		if syncResult := handleSyncError(err); syncResult != nil {
			return syncResult, nil, nil
		}
		return mcpx.ErrorResult(err.Error()), nil, nil
	}

	result := map[string]interface{}{
		"value": regVal.Hex,
	}
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return mcpx.ErrorResult(err.Error()), nil, nil
	}
	return mcpx.TextResult(string(data)), nil, nil
}

func handleReset(ctx context.Context, req *mcpx.CallToolRequest, in ResetIn) (*mcpx.CallToolResult, any, error) {
	connectEmit := mcpprogress.NewEmitter(mcpprogress.Emitter(ctx, req))
	sess, factory := session.AcquireForFlasher(in.Port, mcpprogress.ConnectStatusEmitter(connectEmit))

	bootWait := 2.0
	if in.BootWait != nil {
		bootWait = *in.BootWait
	}

	// resetting -> capturing boot -> complete, on a shared 3-step
	// sequential emitter (separate instance from connectEmit above — see
	// ConnectStatusEmitter's doc comment). ResetESP itself only emits
	// "resetting"; the handler reuses the same status func directly for
	// the remaining two ticks since boot-output capture happens here, not
	// inside esp.ResetESP.
	opEmit := mcpprogress.NewEmitter(mcpprogress.Emitter(ctx, req))
	status := mcpprogress.SequentialStatusEmitter(opEmit, 3)

	err := esp.ResetESP(factory, in.Port, in.ResetMode, status)
	if err != nil {
		session.ReleaseFlasherImmediate(sess, in.Port)
		if syncResult := handleSyncError(err); syncResult != nil {
			return syncResult, nil, nil
		}
		return mcpx.ErrorResult(err.Error()), nil, nil
	}

	// Detect port re-enumeration and restart managed port.
	newPort := session.ReleaseFlasherImmediate(sess, in.Port)

	status(esp.StatusPhaseCapturingBoot, 0, 0)
	bootLines := captureBootOutput(sess, bootWait)
	status(esp.StatusPhaseComplete, 0, 0)

	type resetResponse struct {
		Status     string   `json:"status"`
		Message    string   `json:"message"`
		NewPort    string   `json:"new_port,omitempty"`
		BootOutput []string `json:"boot_output,omitempty"`
	}
	resp := resetResponse{Status: "success", Message: "device reset", NewPort: newPort, BootOutput: bootLines}

	data, err := json.MarshalIndent(resp, "", "  ")
	if err != nil {
		return mcpx.ErrorResult(err.Error()), nil, nil
	}

	return mcpx.TextResult(string(data)), nil, nil
}

func handleESPReadFlash(ctx context.Context, req *mcpx.CallToolRequest, in ReadFlashIn) (*mcpx.CallToolResult, any, error) {
	connectEmit := mcpprogress.NewEmitter(mcpprogress.Emitter(ctx, req))
	sess, factory := session.AcquireForFlasher(in.Port, mcpprogress.ConnectStatusEmitter(connectEmit))
	defer session.ReleaseFlasherDeferred(sess, in.Port)

	if in.MD5 {
		// MD5 mode. Real ETA-driven bar (separate emitter instance from
		// connectEmit above — see ConnectStatusEmitter's doc comment). The
		// fork's GetFlashMD5 takes an opt-in ProgressFunc that ticks a
		// synthetic elapsed/estimated-ms bar for the device-silent hash
		// computation, mirroring the erase path — forwarded straight to
		// the op emitter under a fixed "computing hash" label.
		opEmit := mcpprogress.NewEmitter(mcpprogress.Emitter(ctx, req))
		result, err := esp.GetFlashMD5(factory, in.Port, in.Offset, in.Size, in.Baud, in.ResetMode, espflasher.ProgressFunc(func(current, total int) {
			opEmit(current, total, "computing hash")
		}))
		if err != nil {
			if syncResult := handleSyncError(err); syncResult != nil {
				return syncResult, nil, nil
			}
			return mcpx.ErrorResult(err.Error()), nil, nil
		}

		data, err := json.MarshalIndent(result, "", "  ")
		if err != nil {
			return mcpx.ErrorResult(err.Error()), nil, nil
		}

		return mcpx.TextResult(string(data)), nil, nil
	}

	// Read mode. Separate emitter instance from connectEmit above — see
	// ConnectStatusEmitter's doc comment.
	opEmit := mcpprogress.NewEmitter(mcpprogress.Emitter(ctx, req))
	flashResult, err := esp.ReadFlashData(factory, in.Port, in.Offset, in.Size, in.Baud, in.ResetMode, espflasher.ProgressFunc(func(current, total int) {
		opEmit(current, total, "reading")
	}))
	if err != nil {
		if syncResult := handleSyncError(err); syncResult != nil {
			return syncResult, nil, nil
		}
		return mcpx.ErrorResult(err.Error()), nil, nil
	}

	result := map[string]interface{}{
		"offset": flashResult.Offset,
		"size":   flashResult.Size,
		"data":   base64.StdEncoding.EncodeToString(flashResult.Data),
	}

	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return mcpx.ErrorResult(err.Error()), nil, nil
	}

	return mcpx.TextResult(string(data)), nil, nil
}

func handleReadNVS(ctx context.Context, req *mcpx.CallToolRequest, in ReadNVSIn) (*mcpx.CallToolResult, any, error) {
	connectEmit := mcpprogress.NewEmitter(mcpprogress.Emitter(ctx, req))
	sess, factory := session.AcquireForFlasher(in.Port, mcpprogress.ConnectStatusEmitter(connectEmit))
	defer session.ReleaseFlasherDeferred(sess, in.Port)

	offset, size := nvsOffsetSize(in.Offset, in.Size)

	// ReadNVS only ever emits StatusPhaseReadingPartition (bytes) then
	// StatusPhaseParsing — both already classified by
	// mcpprogress.NVSBytePhases/NVSPhaseOrdinal, so this reuses the same
	// adapter as the NVS read-modify-write handlers rather than forking a
	// new one.
	byteEmit := mcpprogress.NewEmitter(mcpprogress.Emitter(ctx, req))
	phaseEmit := mcpprogress.NewEmitter(mcpprogress.Emitter(ctx, req))
	status := mcpprogress.NVSStatusEmitter(byteEmit, phaseEmit)

	entries, err := esp.ReadNVS(factory, in.Port, offset, size, in.Baud, in.Namespace, in.ResetMode, status)
	if err != nil {
		if syncResult := handleSyncError(err); syncResult != nil {
			return syncResult, nil, nil
		}
		return mcpx.ErrorResult(err.Error()), nil, nil
	}

	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return mcpx.ErrorResult(err.Error()), nil, nil
	}

	return mcpx.TextResult(string(data)), nil, nil
}

func handleWriteNVS(ctx context.Context, req *mcpx.CallToolRequest, in WriteNVSIn) (*mcpx.CallToolResult, any, error) {
	connectEmit := mcpprogress.NewEmitter(mcpprogress.Emitter(ctx, req))
	sess, factory := session.AcquireForFlasher(in.Port, mcpprogress.ConnectStatusEmitter(connectEmit))
	defer session.ReleaseFlasherDeferred(sess, in.Port)

	var entries []nvs.Entry
	for _, e := range in.Entries {
		value, err := parseNVSValue(e.Type, e.Value)
		if err != nil {
			return mcpx.ErrorResult(err.Error()), nil, nil
		}

		entries = append(entries, nvs.Entry{
			Namespace: e.Namespace,
			Key:       e.Key,
			Type:      e.Type,
			Value:     value,
		})
	}

	offset, size := nvsOffsetSize(in.Offset, in.Size)

	byteEmit := mcpprogress.NewEmitter(mcpprogress.Emitter(ctx, req))
	phaseEmit := mcpprogress.NewEmitter(mcpprogress.Emitter(ctx, req))
	status := mcpprogress.NVSStatusEmitter(byteEmit, phaseEmit)

	err := esp.WriteNVS(factory, in.Port, entries, offset, size, in.Baud, in.ResetMode, status)
	if err != nil {
		if syncResult := handleSyncError(err); syncResult != nil {
			return syncResult, nil, nil
		}
		return mcpx.ErrorResult(err.Error()), nil, nil
	}

	result := map[string]string{
		"status": "success",
	}
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return mcpx.ErrorResult(err.Error()), nil, nil
	}

	return mcpx.TextResult(string(data)), nil, nil
}

func handleNVSSet(ctx context.Context, req *mcpx.CallToolRequest, in NVSSetIn) (*mcpx.CallToolResult, any, error) {
	connectEmit := mcpprogress.NewEmitter(mcpprogress.Emitter(ctx, req))
	sess, factory := session.AcquireForFlasher(in.Port, mcpprogress.ConnectStatusEmitter(connectEmit))
	defer session.ReleaseFlasherDeferred(sess, in.Port)

	var updates []esp.NVSUpdate
	for i, e := range in.Entries {
		if e.Namespace == "" || e.Key == "" || e.Type == "" {
			return mcpx.ErrorResult(fmt.Sprintf("entries[%d] requires namespace, key, and type", i)), nil, nil
		}

		value, err := parseNVSValueFromString(e.Type, e.Value)
		if err != nil {
			return mcpx.ErrorResult(fmt.Sprintf("entries[%d]: %s", i, err.Error())), nil, nil
		}

		updates = append(updates, esp.NVSUpdate{
			Namespace: e.Namespace,
			Key:       e.Key,
			Type:      e.Type,
			Value:     value,
		})
	}

	offset, size := nvsOffsetSize(in.Offset, in.Size)

	byteEmit := mcpprogress.NewEmitter(mcpprogress.Emitter(ctx, req))
	phaseEmit := mcpprogress.NewEmitter(mcpprogress.Emitter(ctx, req))
	status := mcpprogress.NVSStatusEmitter(byteEmit, phaseEmit)

	writeResult, err := esp.NVSSetBatch(factory, in.Port, updates, offset, size, in.Baud, in.ResetMode, status)
	if err != nil {
		if syncResult := handleSyncError(err); syncResult != nil {
			return syncResult, nil, nil
		}
		return mcpx.ErrorResult(err.Error()), nil, nil
	}

	result := map[string]interface{}{
		"status":  "success",
		"updated": writeResult.Applied,
	}
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return mcpx.ErrorResult(err.Error()), nil, nil
	}

	return mcpx.TextResult(string(data)), nil, nil
}

func handleNVSDelete(ctx context.Context, req *mcpx.CallToolRequest, in NVSDeleteIn) (*mcpx.CallToolResult, any, error) {
	connectEmit := mcpprogress.NewEmitter(mcpprogress.Emitter(ctx, req))
	sess, factory := session.AcquireForFlasher(in.Port, mcpprogress.ConnectStatusEmitter(connectEmit))
	defer session.ReleaseFlasherDeferred(sess, in.Port)

	offset, size := nvsOffsetSize(in.Offset, in.Size)

	byteEmit := mcpprogress.NewEmitter(mcpprogress.Emitter(ctx, req))
	phaseEmit := mcpprogress.NewEmitter(mcpprogress.Emitter(ctx, req))
	status := mcpprogress.NVSStatusEmitter(byteEmit, phaseEmit)

	writeResult, err := esp.NVSDelete(factory, in.Port, in.Namespace, in.Key, offset, size, in.Baud, in.ResetMode, status)
	if err != nil {
		if syncResult := handleSyncError(err); syncResult != nil {
			return syncResult, nil, nil
		}
		return mcpx.ErrorResult(err.Error()), nil, nil
	}

	result := map[string]interface{}{
		"status":  "success",
		"deleted": writeResult.Applied,
	}
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return mcpx.ErrorResult(err.Error()), nil, nil
	}

	return mcpx.TextResult(string(data)), nil, nil
}

// All three esp_gpio_* handlers acquire the flasher exclusively via
// session.AcquireForFlasher (never esp.DefaultFlasherFactory directly) and
// release via session.ReleaseFlasherDeferredNoReset rather than
// ReleaseFlasherDeferred. GPIO read/drive/sweep is meant for repeated,
// continuous probing of a board (e.g. "which pin drives this LED") without
// resetting the chip out of the bootloader between calls — a normal
// deferred release's Reset() on expiry would boot the app and drop the pin
// state each time the 5s hold lapses. resetAfter is always passed false to
// the esp.*GPIO functions themselves; that argument is a no-op anyway here
// since AcquireForFlasher's factory returns a BorrowedFlasher whose Reset()
// is already a no-op — the real "leave in bootloader vs. reset" decision is
// made entirely by which session.ReleaseFlasher* function the handler
// calls.

func handleGPIORead(ctx context.Context, req *mcpx.CallToolRequest, in GPIOReadIn) (*mcpx.CallToolResult, any, error) {
	done := mcpprogress.LifecycleStatus(ctx, req, "esp_gpio_read")
	defer done()

	connectEmit := mcpprogress.NewEmitter(mcpprogress.Emitter(ctx, req))
	sess, factory := session.AcquireForFlasher(in.Port, mcpprogress.ConnectStatusEmitter(connectEmit))
	defer session.ReleaseFlasherDeferredNoReset(sess, in.Port)

	result, err := esp.ReadGPIO(factory, in.Port, in.Pin, in.Baud, in.ResetMode, false)
	if err != nil {
		if syncResult := handleSyncError(err); syncResult != nil {
			return syncResult, nil, nil
		}
		return mcpx.ErrorResult(err.Error()), nil, nil
	}

	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return mcpx.ErrorResult(err.Error()), nil, nil
	}

	return mcpx.TextResult(string(data)), nil, nil
}

func handleGPIOSet(ctx context.Context, req *mcpx.CallToolRequest, in GPIOSetIn) (*mcpx.CallToolResult, any, error) {
	level, err := parseGPIOLevel(in.Level)
	if err != nil {
		return mcpx.ErrorResult(err.Error()), nil, nil
	}

	done := mcpprogress.LifecycleStatus(ctx, req, "esp_gpio_set")
	defer done()

	connectEmit := mcpprogress.NewEmitter(mcpprogress.Emitter(ctx, req))
	sess, factory := session.AcquireForFlasher(in.Port, mcpprogress.ConnectStatusEmitter(connectEmit))
	defer session.ReleaseFlasherDeferredNoReset(sess, in.Port)

	if err := esp.SetGPIO(factory, in.Port, in.Pin, level, in.Baud, in.ResetMode, false, in.IncludeReserved); err != nil {
		if syncResult := handleSyncError(err); syncResult != nil {
			return syncResult, nil, nil
		}
		return mcpx.ErrorResult(err.Error()), nil, nil
	}

	result := esp.GPIOReadResult{Pin: in.Pin, Level: level}
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return mcpx.ErrorResult(err.Error()), nil, nil
	}

	return mcpx.TextResult(string(data)), nil, nil
}

func handleGPIOSweep(ctx context.Context, req *mcpx.CallToolRequest, in GPIOSweepIn) (*mcpx.CallToolResult, any, error) {
	var pins []int
	if in.Pins != "" {
		parsed, err := esp.ParsePinList(in.Pins)
		if err != nil {
			return mcpx.ErrorResult(err.Error()), nil, nil
		}
		pins = parsed
	}

	opts := esp.GPIOSweepOpts{
		BothPolarities: true,
		Dwell:          gpioDefaultDwell,
		Restore:        true,
	}
	if in.Both != nil {
		opts.BothPolarities = *in.Both
	}
	if in.Dwell != nil {
		opts.Dwell = time.Duration(*in.Dwell * float64(time.Second))
	}
	opts.IncludeReserved = in.IncludeReserved

	connectEmit := mcpprogress.NewEmitter(mcpprogress.Emitter(ctx, req))
	sess, factory := session.AcquireForFlasher(in.Port, mcpprogress.ConnectStatusEmitter(connectEmit))
	defer session.ReleaseFlasherDeferredNoReset(sess, in.Port)

	// Separate emitter instance from connectEmit above — see
	// ConnectStatusEmitter's doc comment.
	opEmit := mcpprogress.NewEmitter(mcpprogress.Emitter(ctx, req))
	status := mcpprogress.GPIOSweepStatusEmitter(opEmit)

	result, err := esp.SweepGPIO(factory, in.Port, pins, opts, in.Baud, in.ResetMode, status, false)
	if err != nil {
		if syncResult := handleSyncError(err); syncResult != nil {
			return syncResult, nil, nil
		}
		return mcpx.ErrorResult(err.Error()), nil, nil
	}

	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return mcpx.ErrorResult(err.Error()), nil, nil
	}

	return mcpx.TextResult(string(data)), nil, nil
}
