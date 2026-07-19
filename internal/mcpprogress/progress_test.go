package mcpprogress

import (
	"context"
	"testing"

	"github.com/dangernoodle-io/shesha"
	"github.com/dangernoodle-io/shesha/mcpx"
	"github.com/dangernoodle-io/shesha/testkit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	espflasher "tinygo.org/x/espflasher/pkg/espflasher"

	"dangernoodle.io/pogopin/internal/esp"
	"dangernoodle.io/pogopin/internal/testutil"
)

// progressCall records one send from a fake leaf, mirroring
// internal/mcpserver/progress_test.go's fixture.
type progressCall struct {
	current, total int
	msg            string
}

func TestNewEmitterThrottlesByIntegerPercent(t *testing.T) {
	var calls []progressCall
	emit := NewEmitter(func(current, total int, msg string) {
		calls = append(calls, progressCall{current, total, msg})
	})

	// 0/100 -> 0%, 1/100 -> 1%, 1/100 again -> no change, 50/100 -> 50%,
	// 100/100 -> always emitted (completion).
	emit(0, 100, "flashing")
	emit(1, 100, "flashing")
	emit(1, 100, "flashing")
	emit(50, 100, "flashing")
	emit(50, 100, "flashing") // same percent, should be dropped
	emit(100, 100, "flashing")

	assert.Equal(t, []progressCall{
		{0, 100, "flashing"},
		{1, 100, "flashing"},
		{50, 100, "flashing"},
		{100, 100, "flashing"},
	}, calls)
}

func TestNewEmitterDropsRegression(t *testing.T) {
	var calls []progressCall
	emit := NewEmitter(func(current, total int, msg string) {
		calls = append(calls, progressCall{current, total, msg})
	})

	emit(50, 100, "flashing")
	emit(10, 100, "flashing") // regression, dropped
	emit(60, 100, "flashing")

	assert.Equal(t, []progressCall{
		{50, 100, "flashing"},
		{60, 100, "flashing"},
	}, calls)
}

func TestNewEmitterAlwaysEmitsCompletion(t *testing.T) {
	var calls []progressCall
	emit := NewEmitter(func(current, total int, msg string) {
		calls = append(calls, progressCall{current, total, msg})
	})

	emit(99, 100, "flashing")
	emit(99, 100, "flashing") // same percent as prior emit, dropped
	emit(100, 100, "flashing")
	emit(100, 100, "flashing") // current == total again: always emitted

	assert.Equal(t, []progressCall{
		{99, 100, "flashing"},
		{100, 100, "flashing"},
		{100, 100, "flashing"},
	}, calls)
}

func TestNewEmitterZeroTotalNoPanic(t *testing.T) {
	var calls []progressCall
	emit := NewEmitter(func(current, total int, msg string) {
		calls = append(calls, progressCall{current, total, msg})
	})

	assert.NotPanics(t, func() {
		emit(0, 0, "flashing")
		emit(5, -1, "flashing")
	})
	assert.Empty(t, calls)
}

func TestConnectStatusEmitterCleanConnectFourDistinctSteps(t *testing.T) {
	var calls []progressCall
	emit := NewEmitter(func(current, total int, msg string) {
		calls = append(calls, progressCall{current, total, msg})
	})
	connectStatus := ConnectStatusEmitter(emit)

	// Clean connect: reset/sync retry-capable (maxAttempts=7), detect_chip
	// and load_stub single-shot (maxAttempts=0). Each phase must land on
	// its own fixed ordinal/4 so every step is a distinct integer percent.
	connectStatus(espflasher.ConnectPhaseReset, 1, 7, "entering download mode")
	connectStatus(espflasher.ConnectPhaseSync, 1, 7, "syncing")
	connectStatus(espflasher.ConnectPhaseDetectChip, 0, 0, "detecting chip")
	connectStatus(espflasher.ConnectPhaseLoadStub, 0, 0, "loading stub")

	assert.Equal(t, []progressCall{
		{1, 4, "reset: entering download mode (attempt 1/7)"},
		{2, 4, "sync: syncing (attempt 1/7)"},
		{3, 4, "detect_chip: detecting chip"},
		{4, 4, "load_stub: loading stub"},
	}, calls)
}

func TestConnectStatusEmitterUnknownPhaseSkipped(t *testing.T) {
	var calls []progressCall
	emit := NewEmitter(func(current, total int, msg string) {
		calls = append(calls, progressCall{current, total, msg})
	})
	connectStatus := ConnectStatusEmitter(emit)

	// An unrecognized phase must be skipped entirely, not emitted as a
	// spurious 0/4 (0%) tick.
	connectStatus(espflasher.ConnectPhase("unknown"), 0, 0, "mystery phase")
	connectStatus(espflasher.ConnectPhaseSync, 1, 7, "syncing")

	assert.Equal(t, []progressCall{
		{2, 4, "sync: syncing (attempt 1/7)"},
	}, calls)
}

func TestNewSequentialStatusEmitterAssignsOrdinalsInCallOrder(t *testing.T) {
	var calls []progressCall
	emit := func(current, total int, msg string) {
		calls = append(calls, progressCall{current, total, msg})
	}
	status := SequentialStatusEmitter(emit, 3)

	status("resetting", 0, 0)
	status("capturing boot", 0, 0)
	status("complete", 0, 0)

	assert.Equal(t, []progressCall{
		{1, 3, "resetting"},
		{2, 3, "capturing boot"},
		{3, 3, "complete"},
	}, calls)
}

func TestNewSequentialStatusEmitterCapsAtStepsTotal(t *testing.T) {
	var calls []progressCall
	emit := func(current, total int, msg string) {
		calls = append(calls, progressCall{current, total, msg})
	}
	status := SequentialStatusEmitter(emit, 2)

	status("resetting", 0, 0)
	status("complete", 0, 0)
	status("extra tick beyond declared total", 0, 0)

	assert.Equal(t, []progressCall{
		{1, 2, "resetting"},
		{2, 2, "complete"},
		{2, 2, "extra tick beyond declared total"},
	}, calls)
}

func TestGPIOSweepStatusEmitterForwardsUnchanged(t *testing.T) {
	var calls []progressCall
	emit := func(current, total int, msg string) {
		calls = append(calls, progressCall{current, total, msg})
	}
	status := GPIOSweepStatusEmitter(emit)

	status("sweeping 5 drivable pins", 2, 5)

	assert.Equal(t, []progressCall{
		{2, 5, "sweeping 5 drivable pins"},
	}, calls)
}

// probeCapability is a minimal shesha Capability, local to this test file,
// whose single tool drives Emitter/NVSStatusEmitter/LifecycleStatus through
// a real *mcpx.CallToolRequest (live Session, real progress-token plumbing)
// -- the same testkit-harness convention decode/esp/serial capability tests
// use. Emitter's send closure only reaches req.Session.NotifyProgress
// (mcpx.NotifyProgress) via a live session, so these three functions can't
// be unit-tested with a bare &mcpx.CallToolRequest{}.
type probeCapability struct{}

func (probeCapability) Attach(r *shesha.Registrar) error {
	shesha.AddTool(r, &mcpx.Tool{
		Name:        "progress_probe",
		Description: "test-only tool exercising Emitter/NVSStatusEmitter/LifecycleStatus",
	}, shesha.ReadOnly, handleProgressProbe)
	return nil
}

func handleProgressProbe(ctx context.Context, req *mcpx.CallToolRequest, in struct{}) (*mcpx.CallToolResult, any, error) {
	// Emitter: raw send, unthrottled.
	raw := Emitter(ctx, req)
	raw(1, 2, "raw tick")

	// NVSStatusEmitter: byte-denominated phase, known non-byte ordinal
	// phase, and an unrecognized phase (must be skipped, not emitted as a
	// spurious 0/3 tick).
	byteEmit := NewEmitter(Emitter(ctx, req))
	phaseEmit := NewEmitter(Emitter(ctx, req))
	status := NVSStatusEmitter(byteEmit, phaseEmit)
	status(esp.StatusPhaseReadingPartition, 5, 10)
	status(esp.StatusPhaseParsing, 0, 0)
	status("unrecognized-phase", 0, 0)

	// LifecycleStatus: start + completion ticks.
	done := LifecycleStatus(ctx, req, "progress_probe")
	done()

	return mcpx.TextResult("ok"), nil, nil
}

func newProbeHarness(t *testing.T) *testkit.Harness {
	t.Helper()
	return testutil.NewHarness(t, probeCapability{})
}

func TestEmitterNoTokenIsNoOp(t *testing.T) {
	h := newProbeHarness(t)
	result, err := h.CallTool(context.Background(), "progress_probe", map[string]any{})
	require.NoError(t, err)
	require.False(t, result.IsError)
}

func TestEmitterAndNVSStatusEmitterAndLifecycleStatusWithToken(t *testing.T) {
	h := newProbeHarness(t)
	token := "tok-progress-probe"
	result, err := h.CallToolWithProgressToken(context.Background(), "progress_probe", map[string]any{}, token)
	require.NoError(t, err)
	require.False(t, result.IsError)

	// Progress notifications are delivered to the harness on the client's
	// async receive goroutine (see testutil.WaitForProgressComplete), so
	// poll for the terminal completion tick instead of reading
	// ProgressEvents immediately -- otherwise this races that delivery and
	// flakes under CI's slower/contended runners.
	events := testutil.WaitForProgressComplete(t, h, token, "progress_probe")
	require.NotEmpty(t, events)

	var messages []string
	for _, ev := range events {
		messages = append(messages, ev.Message)
	}
	assert.Contains(t, messages, "raw tick")
	assert.Contains(t, messages, esp.StatusPhaseReadingPartition)
	assert.Contains(t, messages, esp.StatusPhaseParsing)
	assert.NotContains(t, messages, "unrecognized-phase")
	assert.Contains(t, messages, "start: progress_probe")
	assert.Contains(t, messages, "complete: progress_probe")
}

func TestLifecycleStatusBuildingBlocksStartAndCompletion(t *testing.T) {
	// LifecycleStatus itself requires a real *mcpx.CallToolRequest (with a
	// live Session for a non-nil progress token) to exercise end to end;
	// that integration path is covered by the serial capability's
	// testkit-based tests. Here we exercise the same start/completion shape
	// LifecycleStatus builds on top of NewEmitter directly.
	var calls []progressCall
	emit := NewEmitter(func(current, total int, msg string) {
		calls = append(calls, progressCall{current, total, msg})
	})
	emit(1, 2, "start: esp_info")
	done := func() { emit(2, 2, "complete: esp_info") }
	done()

	assert.Equal(t, []progressCall{
		{1, 2, "start: esp_info"},
		{2, 2, "complete: esp_info"},
	}, calls)
}
