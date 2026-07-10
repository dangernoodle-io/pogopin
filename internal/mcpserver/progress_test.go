package mcpserver

import (
	"context"
	"testing"

	"dangernoodle.io/pogopin/internal/esp"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	espflasher "tinygo.org/x/espflasher/pkg/espflasher"
)

func TestProgressTokenNilMeta(t *testing.T) {
	req := mcp.CallToolRequest{}
	assert.Nil(t, progressToken(req))
}

func TestProgressTokenPresent(t *testing.T) {
	req := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Meta: &mcp.Meta{ProgressToken: "tok-123"},
		},
	}
	assert.Equal(t, mcp.ProgressToken("tok-123"), progressToken(req))
}

type progressCall struct {
	current, total int
	msg            string
}

func TestNewProgressEmitterThrottlesByIntegerPercent(t *testing.T) {
	var calls []progressCall
	emit := newProgressEmitter(func(current, total int, msg string) {
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

func TestNewProgressEmitterDropsRegression(t *testing.T) {
	var calls []progressCall
	emit := newProgressEmitter(func(current, total int, msg string) {
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

func TestNewProgressEmitterAlwaysEmitsCompletion(t *testing.T) {
	var calls []progressCall
	emit := newProgressEmitter(func(current, total int, msg string) {
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

func TestNewProgressEmitterZeroTotalNoPanic(t *testing.T) {
	var calls []progressCall
	emit := newProgressEmitter(func(current, total int, msg string) {
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
	emit := newProgressEmitter(func(current, total int, msg string) {
		calls = append(calls, progressCall{current, total, msg})
	})
	connectStatus := connectStatusEmitter(emit)

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
	emit := newProgressEmitter(func(current, total int, msg string) {
		calls = append(calls, progressCall{current, total, msg})
	})
	connectStatus := connectStatusEmitter(emit)

	// An unrecognized phase must be skipped entirely, not emitted as a
	// spurious 0/4 (0%) tick.
	connectStatus(espflasher.ConnectPhase("unknown"), 0, 0, "mystery phase")
	connectStatus(espflasher.ConnectPhaseSync, 1, 7, "syncing")

	assert.Equal(t, []progressCall{
		{2, 4, "sync: syncing (attempt 1/7)"},
	}, calls)
}

func TestSendProgressNilTokenIsNoop(t *testing.T) {
	send := sendProgress(context.Background(), nil)
	assert.NotPanics(t, func() {
		send(1, 2, "flashing")
	})
}

func TestSendProgressNoServerInContextIsNoop(t *testing.T) {
	send := sendProgress(context.Background(), mcp.ProgressToken("tok-123"))
	assert.NotPanics(t, func() {
		send(1, 2, "flashing")
	})
}

func TestNVSStatusEmitterByteAndPhaseInterplay(t *testing.T) {
	var byteCalls, phaseCalls []progressCall
	byteEmit := func(current, total int, msg string) {
		byteCalls = append(byteCalls, progressCall{current, total, msg})
	}
	phaseEmit := func(current, total int, msg string) {
		phaseCalls = append(phaseCalls, progressCall{current, total, msg})
	}
	status := nvsStatusEmitter(byteEmit, phaseEmit)

	// Full NVSSetBatch/NVSDelete orchestration sequence: byte-phase start
	// ticks (current=0, total=0) are dropped entirely (no bar denominator
	// yet); the real byte tick that follows drives byteEmit; the three
	// non-byte phase transitions drive phaseEmit on the fixed ordinal scale.
	status(esp.StatusPhaseReadingPartition, 0, 0)
	status(esp.StatusPhaseReadingPartition, 128, 128)
	status(esp.StatusPhaseParsing, 0, 0)
	status(esp.StatusPhaseVerifyingCompleteness, 0, 0)
	status(esp.StatusPhaseWriting, 0, 0)
	status(esp.StatusPhaseWriting, 4096, 4096)
	status(esp.StatusPhaseReadingBack, 0, 0)
	status(esp.StatusPhaseReadingBack, 128, 128)
	status(esp.StatusPhaseVerifying, 0, 0)

	assert.Equal(t, []progressCall{
		{128, 128, esp.StatusPhaseReadingPartition},
		{4096, 4096, esp.StatusPhaseWriting},
		{128, 128, esp.StatusPhaseReadingBack},
	}, byteCalls)

	assert.Equal(t, []progressCall{
		{1, nvsPhaseTotal, esp.StatusPhaseParsing},
		{2, nvsPhaseTotal, esp.StatusPhaseVerifyingCompleteness},
		{3, nvsPhaseTotal, esp.StatusPhaseVerifying},
	}, phaseCalls)
}

func TestNVSStatusEmitterUnrecognizedNonBytePhaseSkipped(t *testing.T) {
	var phaseCalls []progressCall
	phaseEmit := func(current, total int, msg string) {
		phaseCalls = append(phaseCalls, progressCall{current, total, msg})
	}
	status := nvsStatusEmitter(func(int, int, string) {}, phaseEmit)

	status("mystery phase", 0, 0)
	status(esp.StatusPhaseParsing, 0, 0)

	assert.Equal(t, []progressCall{
		{1, nvsPhaseTotal, esp.StatusPhaseParsing},
	}, phaseCalls)
}

func TestNVSStatusEmitterThrottleInterplayWithRealEmitter(t *testing.T) {
	// Wire nvsStatusEmitter onto real newProgressEmitter instances (as the
	// handlers do) to confirm the byte bar still throttles correctly while
	// phase ticks land on their own distinct ordinal percents.
	var calls []progressCall
	send := func(current, total int, msg string) {
		calls = append(calls, progressCall{current, total, msg})
	}
	byteEmit := newProgressEmitter(send)
	phaseEmit := newProgressEmitter(send)
	status := nvsStatusEmitter(byteEmit, phaseEmit)

	status(esp.StatusPhaseReadingPartition, 0, 0)
	status(esp.StatusPhaseReadingPartition, 50, 100)
	status(esp.StatusPhaseReadingPartition, 100, 100)
	status(esp.StatusPhaseParsing, 0, 0)
	status(esp.StatusPhaseVerifyingCompleteness, 0, 0)

	assert.Equal(t, []progressCall{
		{50, 100, esp.StatusPhaseReadingPartition},
		{100, 100, esp.StatusPhaseReadingPartition},
		{1, nvsPhaseTotal, esp.StatusPhaseParsing},
		{2, nvsPhaseTotal, esp.StatusPhaseVerifyingCompleteness},
	}, calls)
}

// TestNVSPhaseClassificationCovered guards the coupling the review flagged:
// nvsPhaseOrdinal and nvsBytePhases are keyed by esp.StatusPhase* constants
// but matched against esp.go's runtime phase strings only by value, not by
// the compiler. A future esp.StatusPhase* added without updating either map
// would otherwise silently fall into nvsStatusEmitter's `!ok` skip branch.
// This test enumerates every current phase constant explicitly (not by
// reflection) so that omission is exactly the failure this test catches:
// forgetting to add a new constant here, or to classify it in one of the two
// maps, both fail loudly.
func TestNVSPhaseClassificationCovered(t *testing.T) {
	allPhases := []string{
		esp.StatusPhaseReadingPartition,
		esp.StatusPhaseParsing,
		esp.StatusPhaseVerifyingCompleteness,
		esp.StatusPhaseWriting,
		esp.StatusPhaseReadingBack,
		esp.StatusPhaseVerifying,
	}

	for _, phase := range allPhases {
		_, isByte := nvsBytePhases[phase]
		_, isOrdinal := nvsPhaseOrdinal[phase]
		assert.True(t, isByte != isOrdinal,
			"phase %q must be classified in exactly one of nvsBytePhases/nvsPhaseOrdinal (byte=%v, ordinal=%v)",
			phase, isByte, isOrdinal)
	}

	assert.Len(t, nvsBytePhases, 3)
	assert.Len(t, nvsPhaseOrdinal, 3)
	assert.Equal(t, len(allPhases), len(nvsBytePhases)+len(nvsPhaseOrdinal),
		"every esp.StatusPhase* constant must be classified exactly once")
}
