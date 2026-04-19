package mcpserver

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"dangernoodle.io/pogopin/internal/esp"
	"dangernoodle.io/pogopin/internal/session"
	"github.com/mark3labs/mcp-go/mcp"
	"tinygo.org/x/espflasher/pkg/nvs"
)

func handleFlash(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	port, err := req.RequireString("port")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	sess, factory := session.AcquireForFlasher(port)

	bootWait := 2.0
	if bw, ok := req.GetArguments()["boot_wait"].(float64); ok {
		bootWait = bw
	}

	// Parse images array
	imagesRaw, ok := req.GetArguments()["images"].([]interface{})
	if !ok {
		return mcp.NewToolResultError("images must be an array"), nil
	}

	var images []esp.ImageSpec
	for _, item := range imagesRaw {
		imgMap, ok := item.(map[string]interface{})
		if !ok {
			return mcp.NewToolResultError("each image must be an object with path and offset"), nil
		}

		path, ok := imgMap["path"].(string)
		if !ok {
			return mcp.NewToolResultError("image path must be a string"), nil
		}

		offsetVal, ok := imgMap["offset"].(float64)
		if !ok {
			return mcp.NewToolResultError("image offset must be a number"), nil
		}

		images = append(images, esp.ImageSpec{
			Path:   path,
			Offset: uint32(offsetVal),
		})
	}

	// Parse options
	opts := esp.FlashOptions{}
	if baudFloat, ok := req.GetArguments()["baud"].(float64); ok {
		opts.BaudRate = int(baudFloat)
	}
	if flashBaudFloat, ok := req.GetArguments()["flash_baud"].(float64); ok {
		opts.FlashBaudRate = int(flashBaudFloat)
	}
	if resetMode, ok := req.GetArguments()["reset_mode"].(string); ok {
		opts.ResetMode = resetMode
	}
	if flashMode, ok := req.GetArguments()["flash_mode"].(string); ok {
		opts.FlashMode = flashMode
	}
	if flashSize, ok := req.GetArguments()["flash_size"].(string); ok {
		opts.FlashSize = flashSize
	}
	if chipType, ok := req.GetArguments()["chip_type"].(string); ok {
		opts.ChipType = chipType
	}

	// Flash
	result, err := esp.FlashESP(factory, port, images, opts)
	if err != nil {
		session.ReleaseFlasherImmediate(sess, port)
		if syncResult := handleSyncError(err); syncResult != nil {
			return syncResult, nil
		}
		return mcp.NewToolResultError(err.Error()), nil
	}

	// Detect port re-enumeration and restart managed port
	newPort := session.ReleaseFlasherImmediate(sess, port)

	bootLines := captureBootOutput(sess, bootWait)

	type flashResponse struct {
		*esp.FlashResult
		NewPort    string   `json:"new_port,omitempty"`
		BootOutput []string `json:"boot_output,omitempty"`
	}
	resp := flashResponse{FlashResult: &result, NewPort: newPort, BootOutput: bootLines}

	data, err := json.MarshalIndent(resp, "", "  ")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	return mcp.NewToolResultText(string(data)), nil
}

func handleErase(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	port, err := req.RequireString("port")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	sess, factory := session.AcquireForFlasher(port)

	bootWait := 2.0
	if bw, ok := req.GetArguments()["boot_wait"].(float64); ok {
		bootWait = bw
	}

	// Parse options
	opts := esp.EraseOptions{}
	if baudFloat, ok := req.GetArguments()["baud"].(float64); ok {
		opts.BaudRate = int(baudFloat)
	}
	if resetMode, ok := req.GetArguments()["reset_mode"].(string); ok {
		opts.ResetMode = resetMode
	}

	// Parse optional offset and size
	if offsetFloat, ok := req.GetArguments()["offset"].(float64); ok {
		offset := uint32(offsetFloat)
		opts.Offset = &offset

		// Size is required if offset is specified
		if sizeFloat, ok := req.GetArguments()["size"].(float64); ok {
			size := uint32(sizeFloat)
			opts.Size = &size
		} else {
			return mcp.NewToolResultError("size is required when offset is specified"), nil
		}
	}

	// Erase
	err = esp.EraseESP(factory, port, opts)
	if err != nil {
		session.ReleaseFlasherImmediate(sess, port)
		if syncResult := handleSyncError(err); syncResult != nil {
			return syncResult, nil
		}
		return mcp.NewToolResultError(err.Error()), nil
	}

	// Detect port re-enumeration and restart managed port
	newPort := session.ReleaseFlasherImmediate(sess, port)

	bootLines := captureBootOutput(sess, bootWait)

	type eraseResponse struct {
		Status     string   `json:"status"`
		NewPort    string   `json:"new_port,omitempty"`
		BootOutput []string `json:"boot_output,omitempty"`
	}
	resp := eraseResponse{Status: "success", NewPort: newPort, BootOutput: bootLines}

	data, err := json.MarshalIndent(resp, "", "  ")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	return mcp.NewToolResultText(string(data)), nil
}

func handleESPInfo(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	port, err := req.RequireString("port")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	sess, factory := session.AcquireForFlasher(port)
	defer session.ReleaseFlasherDeferred(sess, port)

	// Parse baud rate
	var baudRate int
	if baudFloat, ok := req.GetArguments()["baud"].(float64); ok {
		baudRate = int(baudFloat)
	}

	resetMode, _ := req.GetArguments()["reset_mode"].(string)

	// Parse include param (default "chip")
	include := "chip"
	if inc, ok := req.GetArguments()["include"].(string); ok && inc != "" {
		include = inc
	}

	// Split include on comma to get requested sections
	sections := make(map[string]bool)
	for _, section := range strings.Split(include, ",") {
		section = strings.TrimSpace(section)
		if section != "" {
			sections[section] = true
		}
	}

	// If no valid sections requested, default to chip
	if len(sections) == 0 {
		sections["chip"] = true
	}

	result := make(map[string]interface{})

	// Get chip info if requested
	if sections["chip"] {
		chipInfo, err := esp.GetChipInfo(factory, port, baudRate, resetMode)
		if err != nil {
			if syncResult := handleSyncError(err); syncResult != nil {
				return syncResult, nil
			}
			return mcp.NewToolResultError(err.Error()), nil
		}
		result["chip"] = chipInfo
	}

	// Get security info if requested
	if sections["security"] {
		secInfo, err := esp.GetSecurityInfo(factory, port, baudRate, resetMode)
		if err != nil {
			if syncResult := handleSyncError(err); syncResult != nil {
				return syncResult, nil
			}
			return mcp.NewToolResultError(err.Error()), nil
		}
		result["security"] = secInfo
	}

	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	return mcp.NewToolResultText(string(data)), nil
}

func handleRegister(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	port, err := req.RequireString("port")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	sess, factory := session.AcquireForFlasher(port)
	defer session.ReleaseFlasherDeferred(sess, port)

	// Parse address
	addressFloat, ok := req.GetArguments()["address"].(float64)
	if !ok {
		return mcp.NewToolResultError("address must be a number"), nil
	}
	address := uint32(addressFloat)

	// Parse baud rate
	var baudRate int
	if baudFloat, ok := req.GetArguments()["baud"].(float64); ok {
		baudRate = int(baudFloat)
	}

	resetMode, _ := req.GetArguments()["reset_mode"].(string)

	// Check if value is provided
	_, hasValue := req.GetArguments()["value"]
	if hasValue {
		// Write mode
		valueFloat, ok := req.GetArguments()["value"].(float64)
		if !ok {
			return mcp.NewToolResultError("value must be a number"), nil
		}
		value := uint32(valueFloat)

		err = esp.WriteRegister(factory, port, address, value, baudRate, resetMode)
		if err != nil {
			if syncResult := handleSyncError(err); syncResult != nil {
				return syncResult, nil
			}
			return mcp.NewToolResultError(err.Error()), nil
		}

		result := map[string]interface{}{
			"value": fmt.Sprintf("0x%08X", value),
		}
		data, err := json.MarshalIndent(result, "", "  ")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(string(data)), nil
	} else {
		// Read mode
		regVal, err := esp.ReadRegister(factory, port, address, baudRate, resetMode)
		if err != nil {
			if syncResult := handleSyncError(err); syncResult != nil {
				return syncResult, nil
			}
			return mcp.NewToolResultError(err.Error()), nil
		}

		result := map[string]interface{}{
			"value": regVal.Hex,
		}
		data, err := json.MarshalIndent(result, "", "  ")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(string(data)), nil
	}
}

func handleReset(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	port, err := req.RequireString("port")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	sess, factory := session.AcquireForFlasher(port)

	bootWait := 2.0
	if bw, ok := req.GetArguments()["boot_wait"].(float64); ok {
		bootWait = bw
	}

	resetMode, _ := req.GetArguments()["reset_mode"].(string)

	err = esp.ResetESP(factory, port, resetMode)
	if err != nil {
		session.ReleaseFlasherImmediate(sess, port)
		if syncResult := handleSyncError(err); syncResult != nil {
			return syncResult, nil
		}
		return mcp.NewToolResultError(err.Error()), nil
	}

	// Detect port re-enumeration and restart managed port
	newPort := session.ReleaseFlasherImmediate(sess, port)

	bootLines := captureBootOutput(sess, bootWait)

	type resetResponse struct {
		Status     string   `json:"status"`
		Message    string   `json:"message"`
		NewPort    string   `json:"new_port,omitempty"`
		BootOutput []string `json:"boot_output,omitempty"`
	}
	resp := resetResponse{Status: "success", Message: "device reset", NewPort: newPort, BootOutput: bootLines}

	data, err := json.MarshalIndent(resp, "", "  ")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	return mcp.NewToolResultText(string(data)), nil
}

func handleESPReadFlash(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	port, err := req.RequireString("port")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	sess, factory := session.AcquireForFlasher(port)
	defer session.ReleaseFlasherDeferred(sess, port)

	// Parse offset
	offsetFloat, ok := req.GetArguments()["offset"].(float64)
	if !ok {
		return mcp.NewToolResultError("offset must be a number"), nil
	}
	offset := uint32(offsetFloat)

	// Parse size
	sizeFloat, ok := req.GetArguments()["size"].(float64)
	if !ok {
		return mcp.NewToolResultError("size must be a number"), nil
	}
	size := uint32(sizeFloat)

	// Parse baud rate
	var baudRate int
	if baudFloat, ok := req.GetArguments()["baud"].(float64); ok {
		baudRate = int(baudFloat)
	}

	resetMode, _ := req.GetArguments()["reset_mode"].(string)

	// Check if md5 param is provided (default false)
	md5 := false
	if mdVal, ok := req.GetArguments()["md5"].(bool); ok {
		md5 = mdVal
	}

	if md5 {
		// MD5 mode
		result, err := esp.GetFlashMD5(factory, port, offset, size, baudRate, resetMode)
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
	} else {
		// Read mode
		flashResult, err := esp.ReadFlashData(factory, port, offset, size, baudRate, resetMode)
		if err != nil {
			if syncResult := handleSyncError(err); syncResult != nil {
				return syncResult, nil
			}
			return mcp.NewToolResultError(err.Error()), nil
		}

		// Encode data as base64
		result := map[string]interface{}{
			"offset": flashResult.Offset,
			"size":   flashResult.Size,
			"data":   base64.StdEncoding.EncodeToString(flashResult.Data),
		}

		data, err := json.MarshalIndent(result, "", "  ")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		return mcp.NewToolResultText(string(data)), nil
	}
}

func handleReadNVS(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	port, err := req.RequireString("port")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	sess, factory := session.AcquireForFlasher(port)
	defer session.ReleaseFlasherDeferred(sess, port)

	offset, size, baudRate := parseNVSParams(req.GetArguments())
	namespace, _ := req.GetArguments()["namespace"].(string)
	resetMode, _ := req.GetArguments()["reset_mode"].(string)

	entries, err := esp.ReadNVS(factory, port, offset, size, baudRate, namespace, resetMode)
	if err != nil {
		if syncResult := handleSyncError(err); syncResult != nil {
			return syncResult, nil
		}
		return mcp.NewToolResultError(err.Error()), nil
	}

	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	return mcp.NewToolResultText(string(data)), nil
}

func handleWriteNVS(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	port, err := req.RequireString("port")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	sess, factory := session.AcquireForFlasher(port)
	defer session.ReleaseFlasherDeferred(sess, port)

	// Parse entries array
	entriesRaw, ok := req.GetArguments()["entries"].([]interface{})
	if !ok {
		return mcp.NewToolResultError("entries must be an array"), nil
	}

	var entries []nvs.Entry
	for _, item := range entriesRaw {
		entryMap, ok := item.(map[string]interface{})
		if !ok {
			return mcp.NewToolResultError("each entry must be an object"), nil
		}

		namespace, ok := entryMap["namespace"].(string)
		if !ok {
			return mcp.NewToolResultError("entry namespace must be a string"), nil
		}

		key, ok := entryMap["key"].(string)
		if !ok {
			return mcp.NewToolResultError("entry key must be a string"), nil
		}

		typ, ok := entryMap["type"].(string)
		if !ok {
			return mcp.NewToolResultError("entry type must be a string"), nil
		}

		value, err := parseNVSValue(typ, entryMap["value"])
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		entries = append(entries, nvs.Entry{
			Namespace: namespace,
			Key:       key,
			Type:      typ,
			Value:     value,
		})
	}

	offset, size, baudRate := parseNVSParams(req.GetArguments())
	resetMode, _ := req.GetArguments()["reset_mode"].(string)

	err = esp.WriteNVS(factory, port, entries, offset, size, baudRate, resetMode)
	if err != nil {
		if syncResult := handleSyncError(err); syncResult != nil {
			return syncResult, nil
		}
		return mcp.NewToolResultError(err.Error()), nil
	}

	result := map[string]string{
		"status": "success",
	}
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	return mcp.NewToolResultText(string(data)), nil
}

func handleNVSSet(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	port, err := req.RequireString("port")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	sess, factory := session.AcquireForFlasher(port)
	defer session.ReleaseFlasherDeferred(sess, port)

	entriesRaw, ok := req.GetArguments()["entries"].([]interface{})
	if !ok {
		return mcp.NewToolResultError("entries must be an array"), nil
	}

	var updates []esp.NVSUpdate
	for i, raw := range entriesRaw {
		entry, ok := raw.(map[string]interface{})
		if !ok {
			return mcp.NewToolResultError(fmt.Sprintf("entries[%d] must be an object", i)), nil
		}

		namespace, _ := entry["namespace"].(string)
		key, _ := entry["key"].(string)
		typ, _ := entry["type"].(string)
		valueStr, _ := entry["value"].(string)

		if namespace == "" || key == "" || typ == "" {
			return mcp.NewToolResultError(fmt.Sprintf("entries[%d] requires namespace, key, and type", i)), nil
		}

		value, err := parseNVSValueFromString(typ, valueStr)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("entries[%d]: %s", i, err.Error())), nil
		}

		updates = append(updates, esp.NVSUpdate{
			Namespace: namespace,
			Key:       key,
			Type:      typ,
			Value:     value,
		})
	}

	offset, size, baudRate := parseNVSParams(req.GetArguments())
	resetMode, _ := req.GetArguments()["reset_mode"].(string)

	err = esp.NVSSetBatch(factory, port, updates, offset, size, baudRate, resetMode)
	if err != nil {
		if syncResult := handleSyncError(err); syncResult != nil {
			return syncResult, nil
		}
		return mcp.NewToolResultError(err.Error()), nil
	}

	result := map[string]interface{}{
		"status":  "success",
		"updated": len(updates),
	}
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	return mcp.NewToolResultText(string(data)), nil
}

func handleNVSDelete(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	port, err := req.RequireString("port")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	sess, factory := session.AcquireForFlasher(port)
	defer session.ReleaseFlasherDeferred(sess, port)

	namespace, err := req.RequireString("namespace")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	key, _ := req.GetArguments()["key"].(string)
	offset, size, baudRate := parseNVSParams(req.GetArguments())
	resetMode, _ := req.GetArguments()["reset_mode"].(string)

	err = esp.NVSDelete(factory, port, namespace, key, offset, size, baudRate, resetMode)
	if err != nil {
		if syncResult := handleSyncError(err); syncResult != nil {
			return syncResult, nil
		}
		return mcp.NewToolResultError(err.Error()), nil
	}

	result := map[string]string{
		"status": "success",
	}
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	return mcp.NewToolResultText(string(data)), nil
}
