package mcpserver

import (
	"context"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	espflasher "tinygo.org/x/espflasher/pkg/espflasher"
)

// progressToken extracts the MCP progress token from a tool call request, if
// the caller supplied one via _meta.progressToken. Returns nil when absent.
func progressToken(req mcp.CallToolRequest) any {
	if req.Params.Meta == nil {
		return nil
	}
	return req.Params.Meta.ProgressToken
}

// newProgressEmitter returns a throttled progress emitter. It forwards to
// send only when the integer percent complete changes (or on completion),
// and drops any non-monotonic (regressing) current values.
func newProgressEmitter(send func(progress, total int, msg string)) func(current, total int, msg string) {
	lastCurrent := -1
	lastPercent := -1

	return func(current, total int, msg string) {
		if total <= 0 {
			return
		}
		if current < lastCurrent {
			return
		}

		percent := current * 100 / total
		if percent == lastPercent && current != total {
			return
		}

		lastCurrent = current
		lastPercent = percent
		// Completion always emits, bypassing the percent throttle. Callers whose
		// progress source signals 100% exactly once (espflasher's FlashImages loop
		// and tickErase both do) get one completion tick; a source that reports
		// current==total repeatedly would emit a duplicate notification here
		// (MCP-tolerant, just wasteful).
		send(current, total, msg)
	}
}

// sendProgress returns the production progress sender for a given request
// context and progress token. If token is nil (the caller didn't ask for
// progress notifications), it returns a no-op. Send errors are swallowed:
// transient conditions (blocked/unintialized notification channel) must
// never fail the underlying tool call.
func sendProgress(ctx context.Context, token any) func(progress, total int, msg string) {
	if token == nil {
		return func(progress, total int, msg string) {}
	}

	return func(progress, total int, msg string) {
		srv := server.ServerFromContext(ctx)
		if srv == nil {
			return
		}
		_ = srv.SendNotificationToClient(ctx, "notifications/progress", map[string]any{
			"progressToken": token,
			"progress":      progress,
			"total":         total,
			"message":       msg,
		})
	}
}

// connectPhaseOrdinal fixes each ConnectPhase to a step on a 4-step bar, in
// the order the connect sequence actually fires: reset, sync, detect_chip,
// load_stub. Total is always 4, so a clean connect renders four distinct
// integer percents (25/50/75/100) — none collide, so newProgressEmitter's
// same-percent throttle can't collapse them into one visible jump.
var connectPhaseOrdinal = map[espflasher.ConnectPhase]int{
	espflasher.ConnectPhaseReset:      1,
	espflasher.ConnectPhaseSync:       2,
	espflasher.ConnectPhaseDetectChip: 3,
	espflasher.ConnectPhaseLoadStub:   4,
}

const connectPhaseTotal = 4

// Note: the connect-phase bar may legitimately cap at 50% or 75% and never
// reach 100%. Upstream only fires detect_chip when ChipType==ChipAuto and
// only fires load_stub when a stub exists for the detected chip, so a
// skipped tail phase on a given connect is expected, not a bug.

// connectStatusEmitter adapts a progress emitter (as returned by
// newProgressEmitter) into an espflasher.ConnectStatusFunc, for wiring onto
// session.AcquireForFlasher so connect-sequence status (reset/sync/
// detect_chip/load_stub) surfaces as MCP progress notifications.
//
// Callers MUST pass a dedicated emitter instance here, separate from any
// op-progress emitter (flash/erase/read byte progress) built for the same
// progressToken. Connect-phase progress and op-progress bytes are two
// different, non-monotonic scales on the same token; newProgressEmitter
// drops any current < its own last-seen current, so sharing one emitter
// between connect and op progress would drop the op's early ticks (which
// legitimately start back at 0 after connect completes). Two independent
// emitter instances on the same token is protocol-legal — the client just
// renders two notification streams.
//
// The bar scale is the fixed phase ordinal (reset=1, sync=2,
// detect_chip=3, load_stub=4) out of a fixed total of 4 — not the
// attempt/maxAttempts retry counters espflasher reports. Retry detail
// (e.g. "attempt 3/7") is folded into the message text instead: it's
// meaningful context, but attempt/maxAttempts varies per phase (reset and
// sync retry against a real maxAttempts; detect_chip and load_stub are
// single-shot with maxAttempts=0) and previously drove the bar itself,
// which produced duplicate or dropped percents (14%, 14% dropped, 100%,
// 100% dropped) instead of a legible 4-step progression. Phases fire in
// order on a clean connect, so the ordinal is non-decreasing; on a retried
// reset, the ordinal repeats (1 again) and newProgressEmitter's monotonic
// guard is a no-op there (current isn't decreasing), while the same-percent
// throttle may drop the repeated message — acceptable, since the bar
// already showed the reset step and this is optimizing clean-connect
// legibility, not retry visualization.
func connectStatusEmitter(emit func(current, total int, msg string)) espflasher.ConnectStatusFunc {
	return func(phase espflasher.ConnectPhase, attempt, maxAttempts int, message string) {
		ordinal, ok := connectPhaseOrdinal[phase]
		if !ok {
			// Unrecognized phase: skip rather than emit a spurious 0/4 (0%) tick.
			return
		}
		msg := fmt.Sprintf("%s: %s", phase, message)
		if maxAttempts > 0 {
			msg = fmt.Sprintf("%s: %s (attempt %d/%d)", phase, message, attempt, maxAttempts)
		}
		emit(ordinal, connectPhaseTotal, msg)
	}
}

// newSequentialStatusEmitter adapts an emit func onto a purely-discrete phase
// orchestration — one with no byte-denominated phase, unlike the NVS
// read-modify-write paths (nvsStatusEmitter). Every distinct tick received is
// assigned the next sequential ordinal (1-based) out of a fixed stepsTotal,
// in call order. This removes the need for a hand-maintained phase->ordinal
// map (nvsPhaseOrdinal's style) per tool: callers just supply the known step
// count for their orchestration, and the phase label always rides along as
// the notification message. Shared by esp_reset, esp_read_flash's md5
// branch, and flash_external's phase markers.
//
// Both esp.StatusFunc and flash.StatusFunc share the same
// func(phase string, current, total int) shape, so the returned func is
// directly assignable to either.
func newSequentialStatusEmitter(emit func(current, total int, msg string), stepsTotal int) func(phase string, current, total int) {
	step := 0
	return func(phase string, current, total int) {
		if step < stepsTotal {
			step++
		}
		emit(step, stepsTotal, phase)
	}
}

// lifecycleStatus emits a start tick (1/2) then returns a done func that
// emits a completion tick (2/2). This is the minimal uniform start+completion
// status signal every tool emits per the plan's design — including fast
// (<1s) tools whose MCP progress bar the client won't visibly paint. Callers
// typically `defer done()` right after acquiring it. A nil progress token
// (the caller didn't request notifications) makes both ticks a no-op via
// sendProgress.
func lifecycleStatus(ctx context.Context, req mcp.CallToolRequest, label string) func() {
	emit := newProgressEmitter(sendProgress(ctx, progressToken(req)))
	emit(1, 2, "start: "+label)
	return func() {
		emit(2, 2, "complete: "+label)
	}
}
