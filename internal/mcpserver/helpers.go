package mcpserver

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"tinygo.org/x/espflasher/pkg/espflasher"
	"tinygo.org/x/espflasher/pkg/nvs"

	"dangernoodle.io/breadboard/internal/session"
)

// parseNVSParams extracts NVS parameters from args with defaults.
func parseNVSParams(args map[string]interface{}) (offset uint32, size uint32, baudRate int) {
	offset = 0x9000
	size = uint32(nvs.DefaultPartSize)
	if v, ok := args["offset"].(float64); ok {
		offset = uint32(v)
	}
	if v, ok := args["size"].(float64); ok {
		size = uint32(v)
	}
	if v, ok := args["baud"].(float64); ok {
		baudRate = int(v)
	}
	return
}

// parseNVSValue converts a value to the specified NVS type.
func parseNVSValue(typ string, raw interface{}) (interface{}, error) {
	switch typ {
	case "u8":
		v, ok := raw.(float64)
		if !ok {
			return nil, fmt.Errorf("u8 value must be a number")
		}
		return uint8(v), nil
	case "u16":
		v, ok := raw.(float64)
		if !ok {
			return nil, fmt.Errorf("u16 value must be a number")
		}
		return uint16(v), nil
	case "u32":
		v, ok := raw.(float64)
		if !ok {
			return nil, fmt.Errorf("u32 value must be a number")
		}
		return uint32(v), nil
	case "i8":
		v, ok := raw.(float64)
		if !ok {
			return nil, fmt.Errorf("i8 value must be a number")
		}
		return int8(v), nil
	case "i16":
		v, ok := raw.(float64)
		if !ok {
			return nil, fmt.Errorf("i16 value must be a number")
		}
		return int16(v), nil
	case "i32":
		v, ok := raw.(float64)
		if !ok {
			return nil, fmt.Errorf("i32 value must be a number")
		}
		return int32(v), nil
	case "string":
		v, ok := raw.(string)
		if !ok {
			return nil, fmt.Errorf("string value must be a string")
		}
		return v, nil
	default:
		return nil, fmt.Errorf("unsupported NVS type: %s", typ)
	}
}

// parseNVSValueFromString converts a string value to the specified NVS type.
func parseNVSValueFromString(typ, valueStr string) (interface{}, error) {
	switch typ {
	case "u8":
		v, err := strconv.ParseUint(valueStr, 10, 8)
		if err != nil {
			return nil, fmt.Errorf("u8 parse error: %w", err)
		}
		return uint8(v), nil
	case "u16":
		v, err := strconv.ParseUint(valueStr, 10, 16)
		if err != nil {
			return nil, fmt.Errorf("u16 parse error: %w", err)
		}
		return uint16(v), nil
	case "u32":
		v, err := strconv.ParseUint(valueStr, 10, 32)
		if err != nil {
			return nil, fmt.Errorf("u32 parse error: %w", err)
		}
		return uint32(v), nil
	case "i8":
		v, err := strconv.ParseInt(valueStr, 10, 8)
		if err != nil {
			return nil, fmt.Errorf("i8 parse error: %w", err)
		}
		return int8(v), nil
	case "i16":
		v, err := strconv.ParseInt(valueStr, 10, 16)
		if err != nil {
			return nil, fmt.Errorf("i16 parse error: %w", err)
		}
		return int16(v), nil
	case "i32":
		v, err := strconv.ParseInt(valueStr, 10, 32)
		if err != nil {
			return nil, fmt.Errorf("i32 parse error: %w", err)
		}
		return int32(v), nil
	case "string":
		return valueStr, nil
	default:
		return nil, fmt.Errorf("unsupported NVS type: %s", typ)
	}
}

// withRecover wraps a tool handler to recover from panics and return an error result.
func withRecover(handler func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error)) func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, req mcp.CallToolRequest) (result *mcp.CallToolResult, err error) {
		defer func() {
			if r := recover(); r != nil {
				result = mcp.NewToolResultError(fmt.Sprintf("internal error: %v", r))
				err = nil
			}
		}()
		return handler(ctx, req)
	}
}

// handleSyncError checks if an error is a SyncError and returns a formatted error result if so.
// Returns nil if the error is not a SyncError.
func handleSyncError(err error) *mcp.CallToolResult {
	if syncErr, ok := err.(*espflasher.SyncError); ok {
		return mcp.NewToolResultError(fmt.Sprintf("device not in download mode (sync failed after %d attempts) — unplug, hold BOOT, plug in, release BOOT and retry", syncErr.Attempts))
	}
	return nil
}

// captureBootOutput waits for boot output to accumulate then reads it from the session's manager.
// Returns nil if sess is nil, mgr is nil, or bootWait is <= 0.
func captureBootOutput(sess *session.PortSession, bootWait float64) []string {
	if sess == nil || bootWait <= 0 {
		return nil
	}
	mgr := sess.GetManager()
	if mgr == nil {
		return nil
	}
	time.Sleep(time.Duration(bootWait * float64(time.Second)))
	return mgr.Read(100)
}
