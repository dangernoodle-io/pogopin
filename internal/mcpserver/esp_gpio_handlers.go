package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"dangernoodle.io/pogopin/internal/esp"
	"dangernoodle.io/pogopin/internal/session"
	"github.com/mark3labs/mcp-go/mcp"
)

// gpioDefaultDwell mirrors the CLI's --dwell default (internal/cli/gpio.go)
// so the MCP tool and the CLI subcommand present identical defaults.
const gpioDefaultDwell = 5 * time.Second

// All three esp_gpio_* handlers acquire the flasher exclusively via
// session.AcquireForFlasher (never esp.DefaultFlasherFactory directly) and
// release via session.ReleaseFlasherDeferredNoReset rather than
// ReleaseFlasherDeferred. GPIO read/drive/sweep is meant for repeated,
// continuous probing of a board (e.g. "which pin drives this LED") without
// resetting the chip out of the bootloader between calls — a normal deferred
// release's Reset() on expiry would boot the app and drop the pin state each
// time the 5s hold lapses. resetAfter is always passed false to the
// esp.*GPIO functions themselves; that argument is a no-op anyway here since
// AcquireForFlasher's factory returns a BorrowedFlasher whose Reset() is
// already a no-op — the real "leave in bootloader vs. reset" decision is
// made entirely by which session.ReleaseFlasher* function the handler calls.

func handleGPIORead(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	done := lifecycleStatus(ctx, req, "esp_gpio_read")
	defer done()

	port, err := req.RequireString("port")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	pinFloat, ok := req.GetArguments()["pin"].(float64)
	if !ok {
		return mcp.NewToolResultError("pin must be a number"), nil
	}
	pin := int(pinFloat)

	connectEmit := newProgressEmitter(sendProgress(ctx, progressToken(req)))
	sess, factory := session.AcquireForFlasher(port, connectStatusEmitter(connectEmit))
	defer session.ReleaseFlasherDeferredNoReset(sess, port)

	var baudRate int
	if baudFloat, ok := req.GetArguments()["baud"].(float64); ok {
		baudRate = int(baudFloat)
	}
	resetMode, _ := req.GetArguments()["reset_mode"].(string)

	result, err := esp.ReadGPIO(factory, port, pin, baudRate, resetMode, false)
	if err != nil {
		if syncResult := handleSyncError(err); syncResult != nil {
			return syncResult, nil
		}
		return mcp.NewToolResultError(err.Error()), nil
	}

	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	return mcp.NewToolResultText(string(data)), nil
}

func handleGPIOSet(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	done := lifecycleStatus(ctx, req, "esp_gpio_set")
	defer done()

	port, err := req.RequireString("port")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	pinFloat, ok := req.GetArguments()["pin"].(float64)
	if !ok {
		return mcp.NewToolResultError("pin must be a number"), nil
	}
	pin := int(pinFloat)

	level, err := parseGPIOLevel(req.GetArguments()["level"])
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	connectEmit := newProgressEmitter(sendProgress(ctx, progressToken(req)))
	sess, factory := session.AcquireForFlasher(port, connectStatusEmitter(connectEmit))
	defer session.ReleaseFlasherDeferredNoReset(sess, port)

	var baudRate int
	if baudFloat, ok := req.GetArguments()["baud"].(float64); ok {
		baudRate = int(baudFloat)
	}
	resetMode, _ := req.GetArguments()["reset_mode"].(string)
	includeReserved, _ := req.GetArguments()["include_reserved"].(bool)

	if err := esp.SetGPIO(factory, port, pin, level, baudRate, resetMode, false, includeReserved); err != nil {
		if syncResult := handleSyncError(err); syncResult != nil {
			return syncResult, nil
		}
		return mcp.NewToolResultError(err.Error()), nil
	}

	result := esp.GPIOReadResult{Pin: pin, Level: level}
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	return mcp.NewToolResultText(string(data)), nil
}

// parseGPIOLevel accepts either a JSON boolean or a 0/1 number for the
// "level" argument, matching how MCP clients commonly represent a binary
// GPIO level either way.
func parseGPIOLevel(raw interface{}) (bool, error) {
	switch v := raw.(type) {
	case bool:
		return v, nil
	case float64:
		return v != 0, nil
	default:
		return false, fmt.Errorf("level must be a boolean or 0/1")
	}
}

func handleGPIOSweep(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	port, err := req.RequireString("port")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	var pins []int
	if pinsStr, ok := req.GetArguments()["pins"].(string); ok && pinsStr != "" {
		parsed, err := esp.ParsePinList(pinsStr)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		pins = parsed
	}

	opts := esp.GPIOSweepOpts{
		BothPolarities: true,
		Dwell:          gpioDefaultDwell,
		Restore:        true,
	}
	if bothVal, ok := req.GetArguments()["both"].(bool); ok {
		opts.BothPolarities = bothVal
	}
	if dwellFloat, ok := req.GetArguments()["dwell"].(float64); ok {
		opts.Dwell = time.Duration(dwellFloat * float64(time.Second))
	}
	if includeRsv, ok := req.GetArguments()["include_reserved"].(bool); ok {
		opts.IncludeReserved = includeRsv
	}

	var baudRate int
	if baudFloat, ok := req.GetArguments()["baud"].(float64); ok {
		baudRate = int(baudFloat)
	}
	resetMode, _ := req.GetArguments()["reset_mode"].(string)

	connectEmit := newProgressEmitter(sendProgress(ctx, progressToken(req)))
	sess, factory := session.AcquireForFlasher(port, connectStatusEmitter(connectEmit))
	defer session.ReleaseFlasherDeferredNoReset(sess, port)

	// Separate emitter instance from connectEmit above — see
	// connectStatusEmitter's doc comment.
	opEmit := newProgressEmitter(sendProgress(ctx, progressToken(req)))
	status := gpioSweepStatusEmitter(opEmit)

	result, err := esp.SweepGPIO(factory, port, pins, opts, baudRate, resetMode, status, false)
	if err != nil {
		if syncResult := handleSyncError(err); syncResult != nil {
			return syncResult, nil
		}
		return mcp.NewToolResultError(err.Error()), nil
	}

	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	return mcp.NewToolResultText(string(data)), nil
}
