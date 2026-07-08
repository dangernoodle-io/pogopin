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

// Noise-filtering thresholds for serial_read's default (non-raw) emit path.
// See BR-54: garbled/framing-noise bytes JSON-encode as \u00XX escapes
// (~6x bloat) with no signal, so we elide them before capLine truncation.
const (
	// maxRepeatRun collapses runs of the same byte longer than this into a
	// short "[0xXX×N]" marker before the noise ratio is computed, so a
	// stuck line (e.g. all 0xFF) doesn't dominate the ratio accounting.
	maxRepeatRun = 16
	// noiseRatioThreshold is the max fraction of a line's runes that may be
	// C0 control bytes (excluding \t \n \r) or U+FFFD substitutions before
	// the whole line is elided as framing noise rather than emitted.
	noiseRatioThreshold = 0.35
)

// ansiEscapeRE matches ANSI CSI ("ESC [ ... final-byte") and OSC
// ("ESC ] ... BEL-or-ST") escape sequences so they can be stripped before
// noise-ratio accounting; they carry no log signal for an LLM reader.
var ansiEscapeRE = regexp.MustCompile("\x1b(?:\\[[0-9;?]*[ -/]*[@-~]|\\][^\x07\x1b]*(?:\x07|\x1b\\\\))")

// stripANSI removes ANSI CSI/OSC escape sequences from line unconditionally;
// they carry no log signal for an LLM reader and JSON-encode expensively.
func stripANSI(line string) string {
	return ansiEscapeRE.ReplaceAllString(line, "")
}

// capLine sanitizes invalid UTF-8 and truncates a line that exceeds
// maxLineBytes on a valid rune boundary, appending a byte-count marker.
// This is also the entire raw:true fallback path — no noise filtering.
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

// collapseRepeats collapses runs of more than maxRepeatRun identical bytes
// into a short "[0xXX×N]" marker so long runs of stuck/framing bytes don't
// dominate the noise-ratio computation or the emitted line length.
func collapseRepeats(line string) string {
	data := []byte(line)
	if len(data) == 0 {
		return line
	}

	var b strings.Builder
	for i := 0; i < len(data); {
		j := i + 1
		for j < len(data) && data[j] == data[i] {
			j++
		}
		run := j - i
		if run > maxRepeatRun {
			fmt.Fprintf(&b, "[0x%02x×%d]", data[i], run)
		} else {
			b.Write(data[i:j])
		}
		i = j
	}
	return b.String()
}

// lineIsNoise reports whether more than noiseRatioThreshold of line's runes
// are C0 control bytes (excluding \t \n \r) or UTF-8 replacement runes,
// i.e. the line looks like garbled framing bytes rather than real log text.
func lineIsNoise(line string) bool {
	if line == "" {
		return false
	}
	var noisy, total int
	for _, r := range line {
		total++
		switch {
		case r == utf8.RuneError:
			noisy++
		case r < 0x20 && r != '\t' && r != '\n' && r != '\r':
			noisy++
		}
	}
	return total > 0 && float64(noisy)/float64(total) > noiseRatioThreshold
}

// sanitizeLine filters ANSI escapes and framing noise from a raw serial
// line before capLine's length truncation runs, per BR-54: strip ANSI
// unconditionally, collapse long repeated-byte runs, then elide the whole
// line if what remains is majority non-printable garbage. This is the
// default (non-raw) serial_read emit path.
func sanitizeLine(line string) string {
	origLen := len(line)

	filtered := collapseRepeats(stripANSI(line))
	valid := strings.ToValidUTF8(filtered, "�")

	if lineIsNoise(valid) {
		return fmt.Sprintf("[%d bytes of framing noise elided]", origLen)
	}

	return capLine(valid)
}

// boundOutput sanitizes and caps each line, then caps the total joined size
// by keeping the most recent lines that fit within maxTotalBytes. When raw
// is true, framing-noise filtering (sanitizeLine) is skipped and each line
// only gets capLine's UTF-8 validation + length cap.
func boundOutput(lines []string, raw bool) string {
	capped := make([]string, len(lines))
	for i, l := range lines {
		if raw {
			capped[i] = capLine(l)
		} else {
			capped[i] = sanitizeLine(l)
		}
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
	done := lifecycleStatus(ctx, req, "serial_list")
	defer done()
	unlockHardwareTier()

	ports, err := serial.ListPorts()
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
	done := lifecycleStatus(ctx, req, "serial_start")
	defer done()
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

	return startSessionWithAutoReset(req, port, baud, bufSize, "Started"), nil
}

// startSessionWithAutoReset starts (or restarts) buffered monitoring on port
// via session.StartSession, then runs the same auto-reset dance for USB CDC
// devices that handleSerialStart has always done, returning a status message.
// verb ("Started"/"Restarted") lets callers share this body with differing
// wording. Shared by handleSerialStart and handleSerialRestart.
func startSessionWithAutoReset(req mcp.CallToolRequest, port string, baud, bufSize int, verb string) *mcp.CallToolResult {
	if err := session.StartSession(port, baud, bufSize); err != nil {
		return mcp.NewToolResultError(err.Error())
	}

	autoReset := true
	if v, ok := req.GetArguments()["auto_reset"].(bool); ok {
		autoReset = v
	}

	msg := fmt.Sprintf("%s reading from %s at %d baud", verb, port, baud)

	if autoReset && session.IsUSBPort(port) {
		// No progress/emitter context here (this is serial_start's internal
		// auto-reset, not an esp_* tool call) — no connect-status callback.
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

	return mcp.NewToolResultText(msg)
}

// handleSerialRestart performs an atomic stop+start on a port to re-trigger
// a DTR/RTS reset without separate serial_stop/serial_start calls. The
// entire sequence runs under a single portsMu acquisition inside
// session.RestartSession so a concurrent serial_start/serial_stop/
// serial_restart on the same port can't interleave in an unlocked gap
// (BR-21 HIGH). If the port is currently open, its baud is preserved as the
// default (request args override). If the port isn't open, this behaves
// like a plain serial_start (no stop needed).
func handleSerialRestart(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	done := lifecycleStatus(ctx, req, "serial_restart")
	defer done()
	unlockHardwareTier()
	port, err := req.RequireString("port")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	var baudOverride *int
	if baudFloat, ok := req.GetArguments()["baud"].(float64); ok {
		b := int(baudFloat)
		baudOverride = &b
	}

	bufSize := 1000
	if v, ok := req.GetArguments()["buffer_size"].(float64); ok {
		bufSize = int(v)
	}

	baud, err := session.RestartSession(port, baudOverride, bufSize)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	return mcp.NewToolResultText(fmt.Sprintf("Restarted reading from %s at %d baud", port, baud)), nil
}

func handleSerialRead(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	done := lifecycleStatus(ctx, req, "serial_read")
	defer done()
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

	raw := false
	if v, ok := req.GetArguments()["raw"].(bool); ok {
		raw = v
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

	return mcp.NewToolResultText(boundOutput(output, raw)), nil
}

func handleSerialStop(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	done := lifecycleStatus(ctx, req, "serial_stop")
	defer done()
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

// flashExternalPhases enumerates every flash.StatusPhase* constant, in the
// order flash_external's combined sequence emits them: three inside
// flash.Flash() (stopping port, running command, restarting) plus two the
// handler emits itself after Flash returns (capturing boot, complete).
// flashExternalStepsTotal is derived from its length rather than a bare
// literal, so newSequentialStatusEmitter's step count in handleSerialFlash
// stays tied to this list instead of drifting independently. Go constants
// aren't reflectable, so this list is hardcoded (mirroring
// TestAllStatusPhasesClassified's esp.StatusPhase* enumeration) --
// TestFlashExternalStepsTotalMatchesPhases asserts it covers every
// flash.StatusPhase* constant, so adding one without updating this list
// fails loudly instead of newSequentialStatusEmitter silently under/over-
// counting.
var flashExternalPhases = [...]string{
	flash.StatusPhaseStoppingPort,
	flash.StatusPhaseRunningCmd,
	flash.StatusPhaseRestarting,
	flash.StatusPhaseCapturingBoot,
	flash.StatusPhaseComplete,
}

var flashExternalStepsTotal = len(flashExternalPhases)

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

	bootWait := 2.0
	if bw, ok := req.GetArguments()["boot_wait"].(float64); ok {
		bootWait = bw
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

	// stopping port -> running command -> restarting (inside flash.Flash) ->
	// capturing boot -> complete (here, after Flash returns, since boot
	// capture happens outside flash.Flash's own restart step). Sequential
	// emitter sized from flashExternalPhases rather than a bare literal --
	// see that var's doc comment and TestFlashExternalStepsTotalMatchesPhases
	// for the guard tying it to the real flash.StatusPhase* set.
	opEmit := newProgressEmitter(sendProgress(ctx, progressToken(req)))
	status := newSequentialStatusEmitter(opEmit, flashExternalStepsTotal)

	result, err := flash.Flash(sess.GetManager(), command, args, flashOpts, status)
	if err != nil {
		// Flash() rejected the command (e.g. BR-51 preflight) before doing
		// anything to port state -- release the session we acquired above so
		// it doesn't stay stuck in ModeExternal.
		session.ReleaseExternal(sess, originalPort)
		return mcp.NewToolResultError(err.Error()), nil
	}

	// Handle port re-enumeration
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
		return mcp.NewToolResultError(err.Error()), nil
	}

	return mcp.NewToolResultText(string(data)), nil
}

func handleSerialWrite(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	done := lifecycleStatus(ctx, req, "serial_write")
	defer done()
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
	done := lifecycleStatus(ctx, req, "serial_status")
	defer done()
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
