// Package serial is the shesha port of the serial_* tools (MC-12): the
// serial port lifecycle (serial_list, serial_start, serial_read,
// serial_write, serial_stop, serial_restart, serial_status). The underlying
// domain logic (internal/serial, internal/session) is unchanged; only the
// MCP registration/handler seam moves.
package serial

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"

	"github.com/dangernoodle-io/shesha"
	"github.com/dangernoodle-io/shesha/mcpx"

	"dangernoodle.io/pogopin/internal/esp"
	"dangernoodle.io/pogopin/internal/mcpprogress"
	"dangernoodle.io/pogopin/internal/serial"
	"dangernoodle.io/pogopin/internal/session"
)

// ListIn is serial_list's input: none.
type ListIn struct{}

// StartIn is serial_start's input. Baud/BufferSize/AutoReset are pointers so
// an absent argument is distinguishable from an explicit zero/false,
// mirroring the mark3labs-based handler's default-when-absent semantics
// (internal/mcpserver/serial_handlers.go).
type StartIn struct {
	// Port is the serial port name (e.g., /dev/ttyUSB0 or COM3). Required.
	Port string `json:"port" jsonschema:"serial port name (e.g., /dev/ttyUSB0 or COM3)"`
	// Baud is the baud rate. Defaults to 115200 when omitted.
	Baud *int `json:"baud,omitempty" jsonschema:"baud rate (default 115200)"`
	// BufferSize is the ring buffer size in lines. Defaults to 1000 when omitted.
	BufferSize *int `json:"buffer_size,omitempty" jsonschema:"ring buffer size in lines (default 1000)"`
	// AutoReset auto-resets USB CDC devices after start for immediate output.
	// Defaults to true when omitted.
	AutoReset *bool `json:"auto_reset,omitempty" jsonschema:"auto-reset USB CDC devices after start for immediate output (default true)"`
}

// ReadIn is serial_read's input.
type ReadIn struct {
	// Port is optional if only one port is open.
	Port string `json:"port,omitempty" jsonschema:"port name (optional if only one port open)"`
	// Pattern is an optional regex used to filter output lines.
	Pattern string `json:"pattern,omitempty" jsonschema:"regex pattern to filter output lines"`
	// Lines is the number of lines to read. Defaults to 50 when omitted.
	Lines *int `json:"lines,omitempty" jsonschema:"number of lines to read (default 50)"`
	// Clear drains the buffer after reading. Defaults to false.
	Clear bool `json:"clear,omitempty" jsonschema:"clear buffer after reading (default false)"`
	// Raw skips framing-noise filtering (ANSI escape stripping, repeated-byte
	// collapsing, garbled-line elision) and returns lines as captured.
	Raw bool `json:"raw,omitempty" jsonschema:"skip framing-noise filtering (ANSI escape stripping, repeated-byte collapsing, garbled-line elision) and return lines as captured (default false)"`
}

// WriteIn is serial_write's input.
type WriteIn struct {
	// Port is optional if only one port is open.
	Port string `json:"port,omitempty" jsonschema:"port name (optional if only one port open)"`
	// Data is the payload to write. Required.
	Data string `json:"data" jsonschema:"data to write"`
	// Raw skips appending a trailing newline. Defaults to false.
	Raw bool `json:"raw,omitempty" jsonschema:"skip appending newline (default false)"`
}

// StopIn is serial_stop's input.
type StopIn struct {
	// Port is optional if only one port is open.
	Port string `json:"port,omitempty" jsonschema:"port name (optional if only one port open)"`
}

// RestartIn is serial_restart's input. Shape mirrors StartIn (same
// optional-pointer semantics), plus Port is required here (unlike
// serial_start's fresh-open path, restart always needs a target port).
type RestartIn struct {
	// Port is the serial port name (e.g., /dev/ttyUSB0 or COM3). Required.
	Port string `json:"port" jsonschema:"serial port name (e.g., /dev/ttyUSB0 or COM3)"`
	// Baud is the baud rate. Defaults to the current baud if the port is
	// open, else 115200, when omitted.
	Baud *int `json:"baud,omitempty" jsonschema:"baud rate (default: current baud if open, else 115200)"`
	// BufferSize is the ring buffer size in lines. Defaults to 1000 when omitted.
	BufferSize *int `json:"buffer_size,omitempty" jsonschema:"ring buffer size in lines (default 1000)"`
	// AutoReset auto-resets USB CDC devices after restart for immediate
	// output. Defaults to true when omitted.
	AutoReset *bool `json:"auto_reset,omitempty" jsonschema:"auto-reset USB CDC devices after restart for immediate output (default true)"`
}

// StatusIn is serial_status's input.
type StatusIn struct {
	// Port is optional; omit to get all ports.
	Port string `json:"port,omitempty" jsonschema:"port name (optional; returns all ports if not specified)"`
}

// Capability is the shesha Capability for the serial tool group. It
// registers serial_list, serial_start, serial_read, serial_write,
// serial_stop, serial_restart, and serial_status: the core tier, no Group
// (unlike the lazily-unlocked hardware tier esp/flash join later).
type Capability struct {
	// UnlockHardware is late-bound by the bootstrap (internal/mcpapp) to
	// app.Unlock("hardware"), so a serial handler can lift the hardware-tier
	// lock on first hardware-workflow signal (serial_list/serial_start/
	// serial_restart), mirroring the mark3labs-based server's lazy ESP/flash
	// registration. A nil UnlockHardware (unset) is a safe no-op.
	UnlockHardware func() error
}

// unlockHardware invokes c.UnlockHardware if set, ignoring its error the
// same way the mark3labs-based unlockHardwareTier did (best-effort; a
// registration failure here must never fail the underlying tool call).
func (c *Capability) unlockHardware() {
	if c.UnlockHardware == nil {
		return
	}
	_ = c.UnlockHardware()
}

// Attach registers c's tools against r.
func (c *Capability) Attach(r *shesha.Registrar) error {
	shesha.AddTool(r, &mcpx.Tool{
		Name:        "serial_list",
		Description: "List all available serial ports.",
	}, shesha.ReadOnly, c.handleSerialList)

	shesha.AddTool(r, &mcpx.Tool{
		Name:        "serial_start",
		Description: "Start reading from a serial port into a ring buffer. Must be called before serial_read, serial_write, or serial_flash. Use serial_status to check state.",
	}, shesha.Write, c.handleSerialStart)

	shesha.AddTool(r, &mcpx.Tool{
		Name:        "serial_read",
		Description: "Read buffered lines from a monitored serial port. Returns most recent lines (default 50). Use pattern to filter with regex. Use clear=true to drain the buffer after reading.",
	}, shesha.ReadOnly, c.handleSerialRead)

	shesha.AddTool(r, &mcpx.Tool{
		Name:        "serial_write",
		Description: `Write data to a monitored serial port. Appends \n by default; set raw=true to send exact bytes. Port must be started with serial_start.`,
	}, shesha.Write, c.handleSerialWrite)

	shesha.AddTool(r, &mcpx.Tool{
		Name:        "serial_stop",
		Description: "Stop serial monitoring and release the port. Required before manual port access outside MCP.",
	}, shesha.Write, c.handleSerialStop)

	shesha.AddTool(r, &mcpx.Tool{
		Name:        "serial_restart",
		Description: "Stop then restart buffered serial monitoring on a port (atomic stop+start); use to re-trigger a DTR/RTS reset without separate stop/start calls. If the port is open, its current baud is preserved as the default; request args override.",
	}, shesha.Write, c.handleSerialRestart)

	shesha.AddTool(r, &mcpx.Tool{
		Name:        "serial_status",
		Description: "Return serial port status. Returns JSON with running, port, baud, buffer_lines, reconnecting, last_error. Omit port to get all ports.",
	}, shesha.ReadOnly, c.handleSerialStatus)

	return nil
}

func (c *Capability) handleSerialList(ctx context.Context, req *mcpx.CallToolRequest, _ ListIn) (*mcpx.CallToolResult, any, error) {
	done := mcpprogress.LifecycleStatus(ctx, req, "serial_list")
	defer done()
	c.unlockHardware()

	ports, err := serial.ListPorts()
	if err != nil {
		return mcpx.ErrorResult(err.Error()), nil, nil
	}

	data, err := json.MarshalIndent(ports, "", "  ")
	if err != nil {
		return mcpx.ErrorResult(err.Error()), nil, nil
	}

	return mcpx.TextResult(string(data)), nil, nil
}

func (c *Capability) handleSerialStart(ctx context.Context, req *mcpx.CallToolRequest, in StartIn) (*mcpx.CallToolResult, any, error) {
	done := mcpprogress.LifecycleStatus(ctx, req, "serial_start")
	defer done()
	c.unlockHardware()

	baud := 115200
	if in.Baud != nil {
		baud = *in.Baud
	}

	bufSize := 1000
	if in.BufferSize != nil {
		bufSize = *in.BufferSize
	}

	autoReset := true
	if in.AutoReset != nil {
		autoReset = *in.AutoReset
	}

	return startSessionWithAutoReset(in.Port, baud, bufSize, autoReset, "Started"), nil, nil
}

// startSessionWithAutoReset starts (or restarts) buffered monitoring on port
// via session.StartSession, then runs the same auto-reset dance for USB CDC
// devices that serial_start has always done, returning a status message.
// verb ("Started"/"Restarted") lets callers share this body with differing
// wording. Shared by handleSerialStart and handleSerialRestart.
//
// No progress/emitter context is threaded into the auto-reset's
// esp.ResetESP call here (this is serial_start's/serial_restart's internal
// auto-reset, not an esp_* tool call) — no connect-status callback, exactly
// mirroring the mark3labs-based handler this replaces.
func startSessionWithAutoReset(port string, baud, bufSize int, autoReset bool, verb string) *mcpx.CallToolResult {
	if err := session.StartSession(port, baud, bufSize); err != nil {
		return mcpx.ErrorResult(err.Error())
	}

	msg := fmt.Sprintf("%s reading from %s at %d baud", verb, port, baud)

	if autoReset && session.IsUSBPort(port) {
		sess, factory := session.AcquireForFlasher(port, nil)
		resetErr := esp.ResetESP(factory, port, "", nil)
		newPort := session.ReleaseFlasherImmediate(sess, port)

		if resetErr == nil {
			if newPort != "" {
				msg += fmt.Sprintf(" (auto-reset: USB CDC device rebooted, port changed to %s)", newPort)
			} else {
				msg += " (auto-reset: USB CDC device rebooted for output)"
			}
		}
	}

	return mcpx.TextResult(msg)
}

// handleSerialRestart performs an atomic stop+start on a port to re-trigger
// a DTR/RTS reset without separate serial_stop/serial_start calls. See
// session.RestartSession for the single-portsMu-acquisition guarantee
// (BR-21 HIGH).
func (c *Capability) handleSerialRestart(ctx context.Context, req *mcpx.CallToolRequest, in RestartIn) (*mcpx.CallToolResult, any, error) {
	done := mcpprogress.LifecycleStatus(ctx, req, "serial_restart")
	defer done()
	c.unlockHardware()

	bufSize := 1000
	if in.BufferSize != nil {
		bufSize = *in.BufferSize
	}

	baud, err := session.RestartSession(in.Port, in.Baud, bufSize)
	if err != nil {
		return mcpx.ErrorResult(err.Error()), nil, nil
	}

	return mcpx.TextResult(fmt.Sprintf("Restarted reading from %s at %d baud", in.Port, baud)), nil, nil
}

func (c *Capability) handleSerialRead(ctx context.Context, req *mcpx.CallToolRequest, in ReadIn) (*mcpx.CallToolResult, any, error) {
	done := mcpprogress.LifecycleStatus(ctx, req, "serial_read")
	defer done()

	m, _, err := session.ResolveSession(map[string]interface{}{"port": in.Port})
	if err != nil {
		return mcpx.ErrorResult(err.Error()), nil, nil
	}

	running := m.IsRunning()
	if !running && m.BufferCount() == 0 {
		if lastErr := m.LastError(); lastErr != nil {
			return mcpx.ErrorResult(fmt.Sprintf("serial reader stopped: %v", lastErr)), nil, nil
		}
		return mcpx.ErrorResult("serial port is not running"), nil, nil
	}

	lines := 50
	if in.Lines != nil {
		lines = *in.Lines
	}

	var output []string
	if in.Clear {
		output = m.ReadAndClear(lines)
	} else {
		output = m.Read(lines)
	}

	if !running {
		if lastErr := m.LastError(); lastErr != nil {
			output = append(output, fmt.Sprintf("[serial reader stopped: %v]", lastErr))
		} else {
			output = append(output, "[serial port stopped]")
		}
	}

	if in.Pattern != "" {
		re, reErr := regexp.Compile(in.Pattern)
		if reErr != nil {
			return mcpx.ErrorResult(fmt.Sprintf("invalid pattern: %v", reErr)), nil, nil
		}
		filtered := make([]string, 0, len(output))
		for _, line := range output {
			if re.MatchString(line) {
				filtered = append(filtered, line)
			}
		}
		output = filtered
	}

	return mcpx.TextResult(boundOutput(output, in.Raw)), nil, nil
}

func (c *Capability) handleSerialStop(ctx context.Context, req *mcpx.CallToolRequest, in StopIn) (*mcpx.CallToolResult, any, error) {
	done := mcpprogress.LifecycleStatus(ctx, req, "serial_stop")
	defer done()

	// First resolve to get the port name (handles single-port fallback).
	_, portName, err := session.ResolveSession(map[string]interface{}{"port": in.Port})
	if err != nil {
		return mcpx.ErrorResult(err.Error()), nil, nil
	}
	if err := session.StopSession(portName); err != nil {
		return mcpx.ErrorResult(err.Error()), nil, nil
	}
	return mcpx.TextResult(fmt.Sprintf("Stopped reading from %s", portName)), nil, nil
}

func (c *Capability) handleSerialWrite(ctx context.Context, req *mcpx.CallToolRequest, in WriteIn) (*mcpx.CallToolResult, any, error) {
	done := mcpprogress.LifecycleStatus(ctx, req, "serial_write")
	defer done()

	m, _, err := session.ResolveSession(map[string]interface{}{"port": in.Port})
	if err != nil {
		return mcpx.ErrorResult(err.Error()), nil, nil
	}

	payload := in.Data
	if !in.Raw {
		payload += "\n"
	}

	n, err := m.Write([]byte(payload))
	if err != nil {
		return mcpx.ErrorResult(err.Error()), nil, nil
	}

	return mcpx.TextResult(fmt.Sprintf("Wrote %d bytes", n)), nil, nil
}

func (c *Capability) handleSerialStatus(ctx context.Context, req *mcpx.CallToolRequest, in StatusIn) (*mcpx.CallToolResult, any, error) {
	done := mcpprogress.LifecycleStatus(ctx, req, "serial_status")
	defer done()

	count := session.PortCount()

	if in.Port == "" && count > 1 {
		portStates := session.AllPortStates()

		data, err := json.MarshalIndent(map[string]interface{}{"ports": portStates}, "", "  ")
		if err != nil {
			return mcpx.ErrorResult(err.Error()), nil, nil
		}
		return mcpx.TextResult(string(data)), nil, nil
	}

	m, _, err := session.ResolveSession(map[string]interface{}{"port": in.Port})
	if err != nil {
		return mcpx.ErrorResult(err.Error()), nil, nil
	}

	status := map[string]interface{}{
		"running":      m.IsRunning(),
		"port":         m.PortName(),
		"baud":         m.Baud(),
		"buffer_lines": m.BufferCount(),
		"reconnecting": m.IsReconnecting(),
		"last_error":   nil,
	}
	if lastErr := m.LastError(); lastErr != nil {
		status["last_error"] = lastErr.Error()
	}

	data, err := json.MarshalIndent(status, "", "  ")
	if err != nil {
		return mcpx.ErrorResult(err.Error()), nil, nil
	}
	return mcpx.TextResult(string(data)), nil, nil
}
