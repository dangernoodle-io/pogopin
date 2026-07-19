// Package flash is the shesha port of the flash_external tool (MC-12). The
// underlying domain logic (internal/flash, internal/session) is unchanged;
// only the MCP registration/handler seam moves. flash_external joins the
// lazily-unlocked "hardware" tool group (shesha.Group), mirroring the
// mark3labs-based server's registerHardwareTools tier.
package flash

import (
	"context"
	"encoding/json"
	"time"

	"github.com/dangernoodle-io/shesha"
	"github.com/dangernoodle-io/shesha/mcpx"

	"dangernoodle.io/pogopin/internal/flash"
	"dangernoodle.io/pogopin/internal/mcpprogress"
	"dangernoodle.io/pogopin/internal/session"
)

// hardwareGroup is the shesha tool group flash_external joins. Deliberately
// unexported: the lazy-unlock wiring lives in internal/mcpapp, which locks
// and unlocks this same group name by its own const.
const hardwareGroup = "hardware"

// bootCaptureWait is the sleep function captureBootOutput uses to honor
// boot_wait durations. Overridden in tests to avoid real sleeps.
var bootCaptureWait = time.Sleep

// captureBootOutput waits for boot output to accumulate then reads it from
// the session's manager. Returns nil if sess is nil, mgr is nil, or
// bootWait is <= 0. Mirrors internal/mcpserver/helpers.go's function of the
// same name.
func captureBootOutput(sess *session.PortSession, bootWait float64) []string {
	if sess == nil || bootWait <= 0 {
		return nil
	}
	mgr := sess.GetManager()
	if mgr == nil {
		return nil
	}
	mgr.ClearBuffer()
	bootCaptureWait(time.Duration(bootWait * float64(time.Second)))
	return mgr.Read(100)
}

// flashExternalPhases enumerates every flash.StatusPhase* constant, in the
// order flash_external's orchestration actually fires them, so
// flashExternalStepsTotal stays tied to this list instead of drifting
// independently. Mirrors internal/mcpserver/serial_handlers.go's var of the
// same name.
var flashExternalPhases = [...]string{
	flash.StatusPhaseStoppingPort,
	flash.StatusPhaseRunningCmd,
	flash.StatusPhaseRestarting,
	flash.StatusPhaseCapturingBoot,
	flash.StatusPhaseComplete,
}

var flashExternalStepsTotal = len(flashExternalPhases)

// ExternalIn is flash_external's input.
type ExternalIn struct {
	// Port is optional if only one port is open.
	Port string `json:"port,omitempty" jsonschema:"port name (optional if only one port open)"`
	// Command is the flash command to run. Required.
	Command string `json:"command" jsonschema:"flash command to run"`
	// Args is the command's arguments.
	Args []string `json:"args,omitempty" jsonschema:"command arguments"`
	// OutputLines limits command output to the last N lines; 0 means
	// unlimited.
	OutputLines int `json:"output_lines,omitempty" jsonschema:"limit command output to last N lines (0 = unlimited)"`
	// OutputFilter is a regex pattern to filter command output lines.
	OutputFilter string `json:"output_filter,omitempty" jsonschema:"regex pattern to filter command output lines"`
	// Shell runs the command via sh -c (enables &&, pipes, globs; Args
	// ignored).
	Shell bool `json:"shell,omitempty" jsonschema:"run command via sh -c (enables &&, pipes, globs; args ignored)"`
	// Cwd is the working directory for the command.
	Cwd string `json:"cwd,omitempty" jsonschema:"working directory for the command"`
	// BootWait is how long to wait after restart and capture boot output.
	// A nil value defaults to 2 seconds; an explicit 0 disables capture —
	// this distinction is why the field is a pointer.
	BootWait *float64 `json:"boot_wait,omitempty" jsonschema:"seconds to wait after restart and capture boot output (default 2.0, 0 disables)"`
}

// Capability is the shesha Capability for the flash tool group. It
// registers flash_external onto the lazily-unlocked "hardware" group.
type Capability struct{}

// Attach registers c's tools against r.
func (c Capability) Attach(r *shesha.Registrar) error {
	shesha.AddTool(r, &mcpx.Tool{
		Name:        "flash_external",
		Description: "Run a flash/build command while managing serial lifecycle (stop → exec → restart → capture boot output). Use for platformio, make, esptool.py, or any build+flash workflow. By default runs the command directly (no shell); set shell=true for &&, pipes, or globs. Set cwd for commands that need a working directory (e.g., make). For native ESP flashing without external tools, use esp_flash instead.",
	}, shesha.Destructive, handleFlashExternal, shesha.Group(hardwareGroup))

	return nil
}

func handleFlashExternal(ctx context.Context, req *mcpx.CallToolRequest, in ExternalIn) (*mcpx.CallToolResult, any, error) {
	// Resolve port name first.
	_, originalPort, err := session.ResolveSession(map[string]interface{}{"port": in.Port})
	if err != nil {
		return mcpx.ErrorResult(err.Error()), nil, nil
	}

	bootWait := 2.0
	if in.BootWait != nil {
		bootWait = *in.BootWait
	}

	var flashOpts *flash.Options
	if in.OutputLines > 0 {
		if flashOpts == nil {
			flashOpts = &flash.Options{}
		}
		flashOpts.OutputLines = in.OutputLines
	}
	if in.OutputFilter != "" {
		if flashOpts == nil {
			flashOpts = &flash.Options{}
		}
		flashOpts.OutputFilter = in.OutputFilter
	}
	if in.Shell {
		if flashOpts == nil {
			flashOpts = &flash.Options{}
		}
		flashOpts.Shell = true
	}
	if in.Cwd != "" {
		if flashOpts == nil {
			flashOpts = &flash.Options{}
		}
		flashOpts.Cwd = in.Cwd
	}

	// Acquire session for external command.
	sess := session.AcquireForExternal(originalPort)

	// stopping port -> running command -> restarting (inside flash.Flash)
	// -> capturing boot -> complete (here, after Flash returns, since boot
	// capture happens outside flash.Flash's own restart step). Sequential
	// emitter sized from flashExternalPhases rather than a bare literal --
	// see that var's doc comment.
	opEmit := mcpprogress.NewEmitter(mcpprogress.Emitter(ctx, req))
	status := mcpprogress.SequentialStatusEmitter(opEmit, flashExternalStepsTotal)

	result, err := flash.Flash(sess.GetManager(), in.Command, in.Args, flashOpts, status)
	if err != nil {
		// Flash() rejected the command (e.g. BR-51 preflight) before doing
		// anything to port state -- release the session we acquired above
		// so it doesn't stay stuck in ModeExternal.
		session.ReleaseExternal(sess, originalPort)
		return mcpx.ErrorResult(err.Error()), nil, nil
	}

	// Handle port re-enumeration.
	newPort := session.ReleaseExternal(sess, originalPort)

	status(flash.StatusPhaseCapturingBoot, 0, 0)
	bootLines := captureBootOutput(sess, bootWait)
	status(flash.StatusPhaseComplete, 0, 0)

	type flashResponse struct {
		*flash.Result
		NewPort    string   `json:"new_port,omitempty"`
		BootOutput []string `json:"boot_output,omitempty"`
	}
	resp := flashResponse{Result: &result, NewPort: newPort, BootOutput: bootLines}

	data, err := json.MarshalIndent(resp, "", "  ")
	if err != nil {
		return mcpx.ErrorResult(err.Error()), nil, nil
	}

	return mcpx.TextResult(string(data)), nil, nil
}
