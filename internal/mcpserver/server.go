package mcpserver

import (
	"context"
	"sync"
	"time"

	"dangernoodle.io/pogopin/internal/session"
	"dangernoodle.io/pogopin/internal/status"
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
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	s := server.NewMCPServer("pogopin", "0.1.0",
		server.WithToolCapabilities(true),
		server.WithInstructions(instructions),
	)

	setActiveServer(s)
	registerTools(s)
	go runHeartbeat(ctx, 15*time.Second)

	return server.ServeStdio(s)
}

func runHeartbeat(ctx context.Context, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			status.Write(session.AllPortStates())
		}
	}
}

// registerTools registers the core tier — serial monitoring and crash decode.
// ESP and flash tools are deferred via registerHardwareTools, triggered on
// first serial_list or serial_start call.
func registerTools(s *server.MCPServer) {
	registerSerialTools(s)
	registerDecodeTools(s)
}

// hardwareTierOnce gates lazy registration of the ESP and flash tools.
// Reset between tests via resetHardwareTier.
var hardwareTierOnce sync.Once

// registerHardwareTools lazily registers the ESP and flash_external tools.
// Safe to call from any handler; subsequent calls are no-ops. The mcp-go
// server emits notifications/tools/list_changed so the client re-fetches
// tools/list and sees the new tools.
func registerHardwareTools(s *server.MCPServer) {
	if s == nil {
		return
	}
	hardwareTierOnce.Do(func() {
		registerESPTools(s)
		registerFlashExternalTool(s)
	})
}

// activeServer holds the current MCP server for lazy registration. Set by
// Serve; read by serial handlers when they trigger the hardware tier.
var activeServer *server.MCPServer

func setActiveServer(s *server.MCPServer) { activeServer = s }

// unlockHardwareTier is called by serial_list and serial_start handlers to
// register the hardware tier on first hardware-workflow signal.
func unlockHardwareTier() { registerHardwareTools(activeServer) }

// resetHardwareTier resets the lazy-registration gate and active server for
// tests. Not safe for concurrent use with Serve.
func resetHardwareTier() {
	hardwareTierOnce = sync.Once{}
	activeServer = nil
}
