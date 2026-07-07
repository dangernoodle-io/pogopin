package mcpserver

import (
	"context"
	"testing"

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
