// Package mcpprogress is the framework-neutral progress-emitter foundation
// shared by shesha capabilities (MC-12): the throttle/monotonic/phase-ordinal
// logic ported unchanged from internal/mcpserver/progress.go, with only the
// leaf notification-send swapped from mark3labs/mcp-go onto mcpx.
package mcpprogress

import (
	"context"
	"fmt"

	"github.com/dangernoodle-io/shesha/mcpx"
	espflasher "tinygo.org/x/espflasher/pkg/espflasher"

	"dangernoodle.io/pogopin/internal/esp"
)

// Emitter returns the production progress sender for req. If the caller
// didn't attach a progress token to req (mcpx.ProgressToken returns nil),
// Emitter returns a no-op — preserving the mark3labs-based server's
// unconditional-no-op-when-tokenless behavior. Send errors are swallowed:
// transient conditions (blocked/uninitialized notification channel) must
// never fail the underlying tool call.
func Emitter(ctx context.Context, req *mcpx.CallToolRequest) func(progress, total int, msg string) {
	if mcpx.ProgressToken(req) == nil {
		return func(progress, total int, msg string) {}
	}

	return func(progress, total int, msg string) {
		_ = mcpx.NotifyProgress(ctx, req, msg, float64(progress), float64(total))
	}
}

// NewEmitter returns a throttled progress emitter. It forwards to send only
// when the integer percent complete changes (or on completion), and drops
// any non-monotonic (regressing) current values.
func NewEmitter(send func(progress, total int, msg string)) func(current, total int, msg string) {
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

// ConnectPhaseOrdinal fixes each ConnectPhase to a step on a 4-step bar, in
// the order the connect sequence actually fires: reset, sync, detect_chip,
// load_stub. ConnectPhaseTotal is always 4, so a clean connect renders four
// distinct integer percents (25/50/75/100) — none collide, so NewEmitter's
// same-percent throttle can't collapse them into one visible jump.
var ConnectPhaseOrdinal = map[espflasher.ConnectPhase]int{
	espflasher.ConnectPhaseReset:      1,
	espflasher.ConnectPhaseSync:       2,
	espflasher.ConnectPhaseDetectChip: 3,
	espflasher.ConnectPhaseLoadStub:   4,
}

// ConnectPhaseTotal is the fixed denominator for ConnectPhaseOrdinal's bar.
//
// Note: the connect-phase bar may legitimately cap at 50% or 75% and never
// reach 100%. Upstream only fires detect_chip when ChipType==ChipAuto and
// only fires load_stub when a stub exists for the detected chip, so a
// skipped tail phase on a given connect is expected, not a bug.
const ConnectPhaseTotal = 4

// ConnectStatusEmitter adapts a progress emitter (as returned by NewEmitter)
// into an espflasher.ConnectStatusFunc, for wiring onto session.AcquireForFlasher
// so connect-sequence status (reset/sync/detect_chip/load_stub) surfaces as
// MCP progress notifications.
//
// Callers MUST pass a dedicated emitter instance here, separate from any
// op-progress emitter (flash/erase/read byte progress) built for the same
// progress token. Connect-phase progress and op-progress bytes are two
// different, non-monotonic scales on the same token; NewEmitter drops any
// current < its own last-seen current, so sharing one emitter between
// connect and op progress would drop the op's early ticks (which
// legitimately start back at 0 after connect completes). Two independent
// emitter instances on the same token is protocol-legal — the client just
// renders two notification streams.
//
// The bar scale is the fixed phase ordinal (reset=1, sync=2, detect_chip=3,
// load_stub=4) out of a fixed total of 4 — not the attempt/maxAttempts retry
// counters espflasher reports. Retry detail (e.g. "attempt 3/7") is folded
// into the message text instead: it's meaningful context, but
// attempt/maxAttempts varies per phase (reset and sync retry against a real
// maxAttempts; detect_chip and load_stub are single-shot with maxAttempts=0)
// and previously drove the bar itself, which produced duplicate or dropped
// percents instead of a legible 4-step progression. Phases fire in order on
// a clean connect, so the ordinal is non-decreasing; on a retried reset, the
// ordinal repeats (1 again) and NewEmitter's monotonic guard is a no-op
// there (current isn't decreasing), while the same-percent throttle may
// drop the repeated message — acceptable, since the bar already showed the
// reset step and this is optimizing clean-connect legibility, not retry
// visualization.
func ConnectStatusEmitter(emit func(current, total int, msg string)) espflasher.ConnectStatusFunc {
	return func(phase espflasher.ConnectPhase, attempt, maxAttempts int, message string) {
		ordinal, ok := ConnectPhaseOrdinal[phase]
		if !ok {
			// Unrecognized phase: skip rather than emit a spurious 0/4 (0%) tick.
			return
		}
		msg := fmt.Sprintf("%s: %s", phase, message)
		if maxAttempts > 0 {
			msg = fmt.Sprintf("%s: %s (attempt %d/%d)", phase, message, attempt, maxAttempts)
		}
		emit(ordinal, ConnectPhaseTotal, msg)
	}
}

// SequentialStatusEmitter adapts an emit func onto a purely-discrete phase
// orchestration — one with no byte-denominated phase. Every distinct tick
// received is assigned the next sequential ordinal (1-based) out of a fixed
// stepsTotal, in call order. This removes the need for a hand-maintained
// phase->ordinal map per tool: callers just supply the known step count for
// their orchestration, and the phase label always rides along as the
// notification message. Shared by esp_reset, esp_read_flash's md5 branch,
// and flash_external's phase markers.
//
// Both esp.StatusFunc and flash.StatusFunc share the same
// func(phase string, current, total int) shape, so the returned func is
// directly assignable to either.
func SequentialStatusEmitter(emit func(current, total int, msg string), stepsTotal int) func(phase string, current, total int) {
	step := 0
	return func(phase string, current, total int) {
		if step < stepsTotal {
			step++
		}
		emit(step, stepsTotal, phase)
	}
}

// GPIOSweepStatusEmitter adapts an esp.StatusFunc directly onto a progress
// emitter for esp_gpio_sweep. Unlike SequentialStatusEmitter (purely
// discrete phases with no byte/count denominator), esp.SweepGPIO's own
// StatusFunc ticks already carry a real current/total (pin index / total
// pin count) on every call, so this just forwards the three values straight
// through with no adaptation needed.
func GPIOSweepStatusEmitter(emit func(current, total int, msg string)) esp.StatusFunc {
	return func(phase string, current, total int) {
		emit(current, total, phase)
	}
}

// NVSBytePhases is the set of esp.StatusFunc phases that carry real
// current/total byte progress (esp.ReadNVS/WriteNVS/NVSSetBatch/NVSDelete's
// "reading partition", "writing", "reading back" steps). NVSStatusEmitter
// classifies a tick as byte-denominated at runtime via total>0, so this set
// isn't consulted on the hot path — it exists so a coverage guard (ported in
// a later commit) can assert every esp.StatusPhase* constant is classified
// exactly once, either here or in NVSPhaseOrdinal, never in neither or both.
var NVSBytePhases = map[string]struct{}{
	esp.StatusPhaseReadingPartition: {},
	esp.StatusPhaseWriting:          {},
	esp.StatusPhaseReadingBack:      {},
}

// NVSPhaseOrdinal fixes each non-byte esp.StatusFunc phase from the NVS
// read-modify-write orchestration (esp.WriteNVS/NVSSetBatch/NVSDelete) to a
// step on a 6-step bar, in the order those steps actually fire. Byte phases
// (NVSBytePhases) carry a real current/total instead and skip this map
// entirely — see NVSStatusEmitter.
var NVSPhaseOrdinal = map[string]int{
	esp.StatusPhaseParsing:               1,
	esp.StatusPhaseVerifyingCompleteness: 2,
	esp.StatusPhaseVerifying:             3,
}

// NVSPhaseTotal is the fixed denominator for NVSPhaseOrdinal's bar.
const NVSPhaseTotal = 3

// NVSStatusEmitter adapts an esp.StatusFunc onto two independent progress
// emitters: byteEmit drives the numeric bar with real bytes during the
// byte-denominated phases ("reading partition", "writing", "reading back");
// phaseEmit renders the non-byte phase transitions ("parsing", "verifying
// completeness", "verifying") on a fixed ordinal/3 scale so their message
// text still surfaces instead of being dropped by NewEmitter's total<=0
// guard. Two separate emitter instances are required here for the same
// reason connect-phase and op-progress use separate instances (see
// ConnectStatusEmitter's doc comment): byte totals and phase ordinals are
// two different, non-monotonic scales on the same token.
func NVSStatusEmitter(byteEmit, phaseEmit func(current, total int, msg string)) esp.StatusFunc {
	return func(phase string, current, total int) {
		if total > 0 {
			byteEmit(current, total, phase)
			return
		}
		ordinal, ok := NVSPhaseOrdinal[phase]
		if !ok {
			// A byte phase's own start tick (current=0, total=0, emitted
			// before the real byte callback fires) or an unrecognized
			// phase: skip rather than emit a spurious 0/3 tick.
			return
		}
		phaseEmit(ordinal, NVSPhaseTotal, phase)
	}
}

// LifecycleStatus emits a start tick (1/2) then returns a done func that
// emits a completion tick (2/2). This is the minimal uniform start+completion
// status signal every tool emits — including fast (<1s) tools whose MCP
// progress bar the client won't visibly paint. Callers typically
// `defer done()` right after acquiring it. A nil progress token (the caller
// didn't request notifications) makes both ticks a no-op via Emitter.
func LifecycleStatus(ctx context.Context, req *mcpx.CallToolRequest, label string) func() {
	emit := NewEmitter(Emitter(ctx, req))
	emit(1, 2, "start: "+label)
	return func() {
		emit(2, 2, "complete: "+label)
	}
}
