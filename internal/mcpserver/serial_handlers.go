package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"dangernoodle.io/pogopin/internal/esp"
	"dangernoodle.io/pogopin/internal/flash"
	"dangernoodle.io/pogopin/internal/serial"
	"dangernoodle.io/pogopin/internal/session"
	"github.com/mark3labs/mcp-go/mcp"
)

// Bounds on handleSerialRead output to keep it well under the tool token cap.
const (
	maxLineBytes  = 512
	maxTotalBytes = 32768
)

// capLine sanitizes invalid UTF-8 and truncates a line that exceeds
// maxLineBytes on a valid rune boundary, appending a byte-count marker.
func capLine(line string) string {
	line = strings.ToValidUTF8(line, "�")
	if len(line) <= maxLineBytes {
		return line
	}
	cut := maxLineBytes
	for cut > 0 && !utf8.RuneStart(line[cut]) {
		cut--
	}
	dropped := len(line) - cut
	return fmt.Sprintf("%s …[+%d bytes]", line[:cut], dropped)
}

// boundOutput sanitizes and caps each line, then caps the total joined size
// by keeping the most recent lines that fit within maxTotalBytes.
func boundOutput(lines []string) string {
	capped := make([]string, len(lines))
	for i, l := range lines {
		capped[i] = capLine(l)
	}

	joined := strings.Join(capped, "\n")
	if len(joined) <= maxTotalBytes {
		return joined
	}

	var kept []string
	size := 0
	omitted := len(capped)
	for i := len(capped) - 1; i >= 0; i-- {
		lineSize := len(capped[i])
		if len(kept) > 0 {
			lineSize++ // account for the joining newline
		}
		if size+lineSize > maxTotalBytes {
			omitted = i + 1
			break
		}
		kept = append(kept, capped[i])
		size += lineSize
		omitted = i
	}
	for i, j := 0, len(kept)-1; i < j; i, j = i+1, j-1 {
		kept[i], kept[j] = kept[j], kept[i]
	}

	marker := fmt.Sprintf("[output truncated: %d earlier lines omitted]", omitted)
	if len(kept) == 0 {
		return marker
	}
	return marker + "\n" + strings.Join(kept, "\n")
}

func handleSerialList(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	unlockHardwareTier()
	usbOnly := false
	if v, ok := req.GetArguments()["usb_only"].(bool); ok {
		usbOnly = v
	}

	ports, err := serial.ListPorts(usbOnly)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	data, err := json.MarshalIndent(ports, "", "  ")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	return mcp.NewToolResultText(string(data)), nil
}

func handleSerialStart(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	unlockHardwareTier()
	port, err := req.RequireString("port")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	baud := 115200
	if baudFloat, ok := req.GetArguments()["baud"].(float64); ok {
		baud = int(baudFloat)
	}

	bufSize := 1000
	if v, ok := req.GetArguments()["buffer_size"].(float64); ok {
		bufSize = int(v)
	}

	if err := session.StartSession(port, baud, bufSize); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	autoReset := true
	if v, ok := req.GetArguments()["auto_reset"].(bool); ok {
		autoReset = v
	}

	msg := fmt.Sprintf("Started reading from %s at %d baud", port, baud)

	if autoReset && session.IsUSBPort(port) {
		sess, factory := session.AcquireForFlasher(port)
		resetErr := esp.ResetESP(factory, port, "")
		newPort := session.ReleaseFlasherImmediate(sess, port)

		if resetErr == nil {
			if newPort != "" {
				msg += fmt.Sprintf(" (auto-reset: USB CDC device rebooted, port changed to %s)", newPort)
			} else {
				msg += " (auto-reset: USB CDC device rebooted for output)"
			}
		}
	}

	return mcp.NewToolResultText(msg), nil
}

func handleSerialRead(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	m, _, err := session.ResolveSession(req.GetArguments())
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	running := m.IsRunning()
	if !running && m.BufferCount() == 0 {
		if lastErr := m.LastError(); lastErr != nil {
			return mcp.NewToolResultError(fmt.Sprintf("serial reader stopped: %v", lastErr)), nil
		}
		return mcp.NewToolResultError("serial port is not running"), nil
	}

	lines := 50
	if v, ok := req.GetArguments()["lines"].(float64); ok {
		lines = int(v)
	}

	clr := false
	if v, ok := req.GetArguments()["clear"].(bool); ok {
		clr = v
	}

	var output []string
	if clr {
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

	pattern, _ := req.GetArguments()["pattern"].(string)
	if pattern != "" {
		re, reErr := regexp.Compile(pattern)
		if reErr != nil {
			return mcp.NewToolResultError(fmt.Sprintf("invalid pattern: %v", reErr)), nil
		}
		filtered := make([]string, 0, len(output))
		for _, line := range output {
			if re.MatchString(line) {
				filtered = append(filtered, line)
			}
		}
		output = filtered
	}

	return mcp.NewToolResultText(boundOutput(output)), nil
}

func handleSerialStop(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	// First resolve to get the port name (handles single-port fallback)
	_, portName, err := session.ResolveSession(req.GetArguments())
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if err := session.StopSession(portName); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return mcp.NewToolResultText(fmt.Sprintf("Stopped reading from %s", portName)), nil
}

func handleSerialFlash(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	// Resolve port name first
	_, originalPort, err := session.ResolveSession(req.GetArguments())
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	command, err := req.RequireString("command")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	var args []string
	if v, ok := req.GetArguments()["args"].([]interface{}); ok {
		for _, a := range v {
			if s, ok := a.(string); ok {
				args = append(args, s)
			}
		}
	}

	var flashOpts *flash.Options
	if lines, ok := req.GetArguments()["output_lines"].(float64); ok && lines > 0 {
		if flashOpts == nil {
			flashOpts = &flash.Options{}
		}
		flashOpts.OutputLines = int(lines)
	}
	if filter, ok := req.GetArguments()["output_filter"].(string); ok && filter != "" {
		if flashOpts == nil {
			flashOpts = &flash.Options{}
		}
		flashOpts.OutputFilter = filter
	}

	if shell, ok := req.GetArguments()["shell"].(bool); ok && shell {
		if flashOpts == nil {
			flashOpts = &flash.Options{}
		}
		flashOpts.Shell = true
	}

	if cwd, ok := req.GetArguments()["cwd"].(string); ok && cwd != "" {
		if flashOpts == nil {
			flashOpts = &flash.Options{}
		}
		flashOpts.Cwd = cwd
	}

	// Acquire session for external command
	sess := session.AcquireForExternal(originalPort)

	result, err := flash.Flash(sess.GetManager(), command, args, flashOpts)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	// Handle port re-enumeration
	newPort := session.ReleaseExternal(sess, originalPort)

	type flashResponse struct {
		*flash.Result
		NewPort string `json:"new_port,omitempty"`
	}
	resp := flashResponse{Result: &result, NewPort: newPort}

	data, err := json.MarshalIndent(resp, "", "  ")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	return mcp.NewToolResultText(string(data)), nil
}

func handleSerialWrite(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	m, _, err := session.ResolveSession(req.GetArguments())
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	data, err := req.RequireString("data")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	raw := false
	if v, ok := req.GetArguments()["raw"].(bool); ok {
		raw = v
	}

	payload := data
	if !raw {
		payload += "\n"
	}

	n, err := m.Write([]byte(payload))
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	return mcp.NewToolResultText(fmt.Sprintf("Wrote %d bytes", n)), nil
}

func handleSerialStatus(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	port, _ := req.GetArguments()["port"].(string)

	count := session.PortCount()

	if port == "" && count > 1 {
		portStates := session.AllPortStates()

		data, err := json.MarshalIndent(map[string]interface{}{"ports": portStates}, "", "  ")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(string(data)), nil
	}

	m, _, err := session.ResolveSession(req.GetArguments())
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
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
		return mcp.NewToolResultError(err.Error()), nil
	}
	return mcp.NewToolResultText(string(data)), nil
}
