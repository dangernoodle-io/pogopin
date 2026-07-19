// Package mcpapp is the shesha composition root for pogopin (MC-12).
// internal/cli builds the App via BuildApp and wires it into `pogo server`
// via shesha's cli.ServerCmd.
package mcpapp

import (
	"context"
	"time"

	"github.com/dangernoodle-io/shesha"
	"github.com/dangernoodle-io/shesha/host/generic"

	"dangernoodle.io/pogopin/internal/capability/decode"
	"dangernoodle.io/pogopin/internal/capability/esp"
	"dangernoodle.io/pogopin/internal/capability/flash"
	"dangernoodle.io/pogopin/internal/capability/serial"
	"dangernoodle.io/pogopin/internal/session"
	"dangernoodle.io/pogopin/internal/status"
)

// version is pogopin's MCP server version, matching the literal
// internal/mcpserver.Serve advertises via server.NewMCPServer.
const version = "0.1.0"

// instructions mirrors internal/mcpserver.instructions verbatim.
const instructions = `Serial monitoring: serial_start → serial_read/serial_write → serial_stop
ESP flashing: esp_flash (native Go), flash_external (PlatformIO/avrdude/any CLI)
ESP device info: esp_info (chip by default; pass include=security for secure boot/encryption)
ESP flash ops: esp_read_flash (raw bytes or md5=true for hash), esp_erase
ESP NVS: esp_read_nvs (read), esp_nvs_set (set keys, RMW), esp_nvs_delete (delete keys, RMW), esp_write_nvs (DESTRUCTIVE full partition replace)
ESP low-level: esp_register (read/write), esp_reset
ESP GPIO: esp_gpio_read (level), esp_gpio_set (drive), esp_gpio_sweep (probe pin range)
Crash analysis: decode_backtrace (xtensa/riscv32 panic frames)
Most esp_* tools auto-stop the monitor and restart after the op (exception: esp_gpio_* tools — see below).
esp_gpio_read/esp_gpio_set/esp_gpio_sweep hold the chip in download/bootloader mode with no reset after the call, so repeated probes reuse the same connection and don't perturb pin state; the port auto-returns to normal serial_start monitoring after ~5s of inactivity.`

// hardwareGroup is the shesha tool group name for the lazily-unlocked ESP
// and flash tool tier, mirroring internal/mcpserver's registerHardwareTools
// lazy-registration gate.
const hardwareGroup = "hardware"

// BuildApp composes the shesha App: serial, decode, esp, and flash
// capabilities over a stdio host. The hardware group starts locked;
// serialCap.UnlockHardware is late-bound to app.Unlock(hardwareGroup) so the
// serial handler lifts the lock on first hardware-workflow signal, mirroring
// the retired mark3labs-based server's lazy ESP/flash tool registration.
func BuildApp() (*shesha.App, error) {
	maybeEnableMock()

	serialCap := &serial.Capability{}

	app, err := shesha.New(shesha.Info{
		Name:         "pogopin",
		Version:      version,
		Instructions: instructions,
		KeepAlive:    15 * time.Second,
	}, generic.New(), serialCap, decode.Capability{}, esp.Capability{}, flash.Capability{})
	if err != nil {
		return nil, err
	}

	if err := app.Lock(hardwareGroup); err != nil {
		return nil, err
	}

	serialCap.UnlockHardware = func() error { return app.Unlock(hardwareGroup) }

	return app, nil
}

// heartbeatInterval is how often RunHeartbeat writes the status snapshot,
// matching the retired internal/mcpserver.Serve's heartbeat cadence.
const heartbeatInterval = 15 * time.Second

// RunHeartbeat writes the current port-state snapshot to internal/status
// every 15s, feeding `pogo statusline`, until ctx is cancelled. This is
// separate from shesha's own Info.KeepAlive (a transport-level liveness
// ping) — callers should run it in its own goroutine from the server
// command's OnStart, e.g. `go mcpapp.RunHeartbeat(ctx)`.
func RunHeartbeat(ctx context.Context) {
	runHeartbeat(ctx, heartbeatInterval)
}

// runHeartbeat is RunHeartbeat's interval-parameterized implementation,
// mirroring the retired internal/mcpserver.runHeartbeat's shape — the
// explicit interval is a test seam so TestRunHeartbeat can tick fast
// instead of waiting on the real 15s cadence.
func runHeartbeat(ctx context.Context, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			status.Write(session.AllPortStates())
			status.SweepStale()
		}
	}
}
