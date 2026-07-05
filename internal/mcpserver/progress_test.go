package mcpserver

import (
	"context"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
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
