package esp

import (
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/dangernoodle-io/shesha/mcpx"
	espflasher "tinygo.org/x/espflasher/pkg/espflasher"

	"dangernoodle.io/pogopin/internal/session"
)

// handleSyncError checks if err is a SyncError and returns a formatted
// error result if so. Returns nil if err is not a SyncError, mirroring
// internal/mcpserver/helpers.go's handleSyncError (mcpx-flavored here).
func handleSyncError(err error) *mcpx.CallToolResult {
	if syncErr, ok := err.(*espflasher.SyncError); ok {
		return mcpx.ErrorResult(fmt.Sprintf("device not in download mode (sync failed after %d attempts) — unplug, hold BOOT, plug in, release BOOT and retry", syncErr.Attempts))
	}
	return nil
}

// hasArgKey reports whether key was present in req's raw wire arguments,
// even if its value was explicit JSON null — distinguishing "explicit null"
// from "omitted" the way the pre-migration mcp-go handlers did via
// req.GetArguments()[key] map-lookup presence (esp_register's "value" key,
// MC-12 review parity fix). Cheap: req.Params.Arguments is already the raw
// json.RawMessage the wire client sent, no extra round trip. Fails closed
// (false) on any decode error — matching the typed handler's own tolerance
// for malformed arguments, which schema validation already rejects earlier
// in the pipeline.
func hasArgKey(req *mcpx.CallToolRequest, key string) bool { //nolint:unparam // general-purpose key-presence check; esp_register's "value" is its only caller today
	if req == nil || req.Params == nil || len(req.Params.Arguments) == 0 {
		return false
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(req.Params.Arguments, &raw); err != nil {
		return false
	}
	_, ok := raw[key]
	return ok
}

// parseGPIOLevel accepts either a JSON boolean or a 0/1 number for the
// "level" argument (decoded as `any` by GPIOSetIn.Level), matching how MCP
// clients commonly represent a binary GPIO level either way. Mirrors
// internal/mcpserver/esp_gpio_handlers.go's function of the same name
// (MC-12 review parity fix).
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

// parseNVSValue converts a raw JSON value (as decoded from an `any`-typed
// struct field) to the specified NVS type, mirroring
// internal/mcpserver/helpers.go's function of the same name.
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

// parseNVSValueFromString converts a string value to the specified NVS
// type, mirroring internal/mcpserver/helpers.go's function of the same
// name.
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
