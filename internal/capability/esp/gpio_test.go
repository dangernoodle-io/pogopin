package esp

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/dangernoodle-io/shesha/testkit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"dangernoodle.io/pogopin/internal/esp"
	"dangernoodle.io/pogopin/internal/session"
	"dangernoodle.io/pogopin/internal/testutil"
)

// raceSafeMockFlasher wraps testutil.MockFlasher with a mutex guarding
// Close/Reset and their observability, for tests that poll
// closeCalled/resetCalled from the test goroutine while session's
// deferred-restart timer goroutine concurrently calls Close()/Reset() on
// expiry (MockFlasher's plain bool fields are otherwise unsynchronized,
// which -race correctly flags across goroutines). Mirrors
// internal/mcpserver/esp_gpio_handlers_test.go's fixture of the same name.
type raceSafeMockFlasher struct {
	*testutil.MockFlasher
	mu sync.Mutex
}

func (m *raceSafeMockFlasher) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.MockFlasher.Close()
}

func (m *raceSafeMockFlasher) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.MockFlasher.Reset()
}

func (m *raceSafeMockFlasher) getCloseCalled() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.CloseCalled
}

func (m *raceSafeMockFlasher) getResetCalled() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.ResetCalled
}

func TestHandleGPIOReadSuccess(t *testing.T) {
	testutil.SetupTestPorts(t)
	testutil.SetupTestFlasherFactory(t)
	setFlasher(t, &testutil.MockFlasher{ReadGPIOVal: true})

	h := newHarness(t)
	result, err := h.CallTool(context.Background(), "esp_gpio_read", map[string]any{
		"port": "/dev/ttyUSB0", "pin": float64(4),
	})
	require.NoError(t, err)
	require.False(t, result.IsError)

	var got esp.GPIOReadResult
	require.NoError(t, json.Unmarshal([]byte(testkit.ResultText(result)), &got))
	assert.Equal(t, 4, got.Pin)
	assert.True(t, got.Level)
}

func TestHandleGPIOReadError(t *testing.T) {
	testutil.SetupTestPorts(t)
	testutil.SetupTestFlasherFactory(t)
	setFlasherErr(t, assertErr("connection failed"))

	h := newHarness(t)
	result, err := h.CallTool(context.Background(), "esp_gpio_read", map[string]any{
		"port": "/dev/ttyUSB0", "pin": float64(4),
	})
	require.NoError(t, err)
	require.True(t, result.IsError)
}

// TestHandleGPIOReadNoResetOnExpiry confirms handleGPIORead releases via
// session.ReleaseFlasherDeferredNoReset (not ReleaseFlasherDeferred): once
// the deferred-restart timer fires, the underlying flasher's Reset() must
// NOT have been called, unlike every mutating esp_ handler.
func TestHandleGPIOReadNoResetOnExpiry(t *testing.T) {
	testutil.SetupTestPorts(t)
	testutil.SetupTestFlasherFactory(t)
	testutil.SetupFastWaitForPort(t)
	orig := session.SetDeferredRestartTimeout(5 * time.Millisecond)
	t.Cleanup(func() { session.SetDeferredRestartTimeout(orig) })

	mock := &raceSafeMockFlasher{MockFlasher: &testutil.MockFlasher{ReadGPIOVal: true}}
	setFlasher(t, mock)

	h := newHarness(t)
	result, err := h.CallTool(context.Background(), "esp_gpio_read", map[string]any{
		// Real path (matches internal/mcpserver's original comment): expireSession's
		// post-expiry WaitForPort os.Stat-checks this path, and a real path
		// returns immediately instead of paying the full 3s deadline.
		"port": t.TempDir(),
		"pin":  float64(4),
	})
	require.NoError(t, err)
	require.False(t, result.IsError)

	require.Eventually(t, mock.getCloseCalled, time.Second, time.Millisecond,
		"deferred timer should have expired and closed the cached flasher")
	assert.False(t, mock.getResetCalled(), "no-reset hold must skip Reset() on deferred expiry")
}

func TestHandleGPIOSetSuccess(t *testing.T) {
	testutil.SetupTestPorts(t)
	testutil.SetupTestFlasherFactory(t)
	mock := &testutil.MockFlasher{}
	setFlasher(t, mock)

	h := newHarness(t)
	result, err := h.CallTool(context.Background(), "esp_gpio_set", map[string]any{
		"port": "/dev/ttyUSB0", "pin": float64(4), "level": true,
	})
	require.NoError(t, err)
	require.False(t, result.IsError)
	require.Len(t, mock.SetGPIOCalls, 1)
	assert.Equal(t, 4, mock.SetGPIOCalls[0].Pin)
	assert.True(t, mock.SetGPIOCalls[0].Level)
}

// TestHandleGPIOSetLevelAcceptsNumeric restores parity with the
// pre-migration mcp-go handler's parseGPIOLevel (MC-12 review): "level"
// must also accept a numeric 1/0, not just a JSON boolean.
func TestHandleGPIOSetLevelAcceptsNumeric(t *testing.T) {
	testutil.SetupTestPorts(t)
	testutil.SetupTestFlasherFactory(t)
	mock := &testutil.MockFlasher{}
	setFlasher(t, mock)

	h := newHarness(t)
	result, err := h.CallTool(context.Background(), "esp_gpio_set", map[string]any{
		"port": "/dev/ttyUSB0", "pin": float64(4), "level": float64(1),
	})
	require.NoError(t, err)
	require.False(t, result.IsError)
	require.Len(t, mock.SetGPIOCalls, 1)
	assert.True(t, mock.SetGPIOCalls[0].Level)

	result, err = h.CallTool(context.Background(), "esp_gpio_set", map[string]any{
		"port": "/dev/ttyUSB0", "pin": float64(4), "level": float64(0),
	})
	require.NoError(t, err)
	require.False(t, result.IsError)
	require.Len(t, mock.SetGPIOCalls, 2)
	assert.False(t, mock.SetGPIOCalls[1].Level)
}

// TestHandleGPIOSetRejectsUnsupportedLevelType exercises parseGPIOLevel's
// error path through the handler (a string "level" is neither a bool nor a
// number) — mirrors the pre-migration mcp-go handler's behavior.
func TestHandleGPIOSetRejectsUnsupportedLevelType(t *testing.T) {
	testutil.SetupTestPorts(t)
	testutil.SetupTestFlasherFactory(t)
	setFlasher(t, &testutil.MockFlasher{})

	h := newHarness(t)
	result, err := h.CallTool(context.Background(), "esp_gpio_set", map[string]any{
		"port": "/dev/ttyUSB0", "pin": float64(4), "level": "high",
	})
	require.NoError(t, err)
	require.True(t, result.IsError)
	assert.Contains(t, testkit.ResultText(result), "level must be a boolean or 0/1")
}

// TestHandleGPIOSetRefusesInputOnlyPin confirms the underlying esp.SetGPIO
// error (e.g. an input-only pin refusal from f.SetGPIO itself, not
// f.GPIOReserved) is surfaced verbatim.
func TestHandleGPIOSetRefusesInputOnlyPin(t *testing.T) {
	testutil.SetupTestPorts(t)
	testutil.SetupTestFlasherFactory(t)
	setFlasher(t, &testutil.MockFlasher{SetGPIOErr: assertErr("pin 34 is input-only")})

	h := newHarness(t)
	result, err := h.CallTool(context.Background(), "esp_gpio_set", map[string]any{
		"port": "/dev/ttyUSB0", "pin": float64(34), "level": true,
	})
	require.NoError(t, err)
	require.True(t, result.IsError)
	assert.Contains(t, testkit.ResultText(result), "input-only")
}

// TestHandleGPIOSetRefusesReservedPinByDefault confirms handleGPIOSet gates
// on f.GPIOReserved via the real esp.SetGPIO gating logic -- the underlying
// drive is NEVER called for a reserved pin when include_reserved is
// omitted.
func TestHandleGPIOSetRefusesReservedPinByDefault(t *testing.T) {
	testutil.SetupTestPorts(t)
	testutil.SetupTestFlasherFactory(t)
	mock := &testutil.MockFlasher{
		GPIOReservedFunc: func(pin int) (bool, string) {
			if pin == 6 {
				return true, "flash"
			}
			return false, ""
		},
	}
	setFlasher(t, mock)

	h := newHarness(t)
	result, err := h.CallTool(context.Background(), "esp_gpio_set", map[string]any{
		"port": "/dev/ttyUSB0", "pin": float64(6), "level": true,
	})
	require.NoError(t, err)
	require.True(t, result.IsError)
	text := testkit.ResultText(result)
	assert.Contains(t, text, "GPIO 6 is reserved (flash)")
	assert.Empty(t, mock.SetGPIOCalls, "reserved pin must never reach the underlying drive")
}

// TestHandleGPIOSetIncludeReservedDrives confirms include_reserved=true
// plumbs through the handler and overrides the reserved refusal, reaching
// the underlying drive.
func TestHandleGPIOSetIncludeReservedDrives(t *testing.T) {
	testutil.SetupTestPorts(t)
	testutil.SetupTestFlasherFactory(t)
	mock := &testutil.MockFlasher{GPIOReservedFunc: func(pin int) (bool, string) { return true, "flash" }}
	setFlasher(t, mock)

	h := newHarness(t)
	result, err := h.CallTool(context.Background(), "esp_gpio_set", map[string]any{
		"port": "/dev/ttyUSB0", "pin": float64(6), "level": true, "include_reserved": true,
	})
	require.NoError(t, err)
	require.False(t, result.IsError)
	require.Len(t, mock.SetGPIOCalls, 1)
	assert.Equal(t, 6, mock.SetGPIOCalls[0].Pin)
	assert.True(t, mock.SetGPIOCalls[0].Level)
}

func TestHandleGPIOSweepSuccess(t *testing.T) {
	testutil.SetupTestPorts(t)
	testutil.SetupTestFlasherFactory(t)
	mock := &testutil.MockFlasher{}
	setFlasher(t, mock)

	h := newHarness(t)
	result, err := h.CallTool(context.Background(), "esp_gpio_sweep", map[string]any{
		"port": "/dev/ttyUSB0", "pins": "4,5", "dwell": float64(0.001), "both": false,
	})
	require.NoError(t, err)
	require.False(t, result.IsError)

	var got esp.GPIOSweepResult
	require.NoError(t, json.Unmarshal([]byte(testkit.ResultText(result)), &got))
	require.Len(t, got.Pins, 2)
	assert.Equal(t, 4, got.Pins[0].Pin)
	assert.Equal(t, 5, got.Pins[1].Pin)
	assert.False(t, got.Pins[0].Skipped)
	assert.False(t, got.Pins[1].Skipped)

	require.Len(t, mock.SetGPIOCalls, 2)
	assert.Equal(t, 4, mock.SetGPIOCalls[0].Pin)
	assert.Equal(t, 5, mock.SetGPIOCalls[1].Pin)
}

func TestHandleGPIOSweepSkipsReservedByDefault(t *testing.T) {
	testutil.SetupTestPorts(t)
	testutil.SetupTestFlasherFactory(t)
	mock := &testutil.MockFlasher{
		GPIOReservedFunc: func(pin int) (bool, string) {
			if pin == 6 {
				return true, "flash"
			}
			return false, ""
		},
	}
	setFlasher(t, mock)

	h := newHarness(t)
	result, err := h.CallTool(context.Background(), "esp_gpio_sweep", map[string]any{
		"port": "/dev/ttyUSB0", "pins": "4,6", "dwell": float64(0.001), "both": false,
	})
	require.NoError(t, err)
	require.False(t, result.IsError)

	var got esp.GPIOSweepResult
	require.NoError(t, json.Unmarshal([]byte(testkit.ResultText(result)), &got))
	require.Len(t, got.Pins, 2)
	assert.False(t, got.Pins[0].Skipped)
	assert.True(t, got.Pins[1].Skipped)
	assert.Equal(t, "flash", got.Pins[1].Reason)

	for _, call := range mock.SetGPIOCalls {
		assert.NotEqual(t, 6, call.Pin)
	}
}

func TestHandleGPIOSweepInvalidPinRange(t *testing.T) {
	testutil.SetupTestPorts(t)
	testutil.SetupTestFlasherFactory(t)

	h := newHarness(t)
	result, err := h.CallTool(context.Background(), "esp_gpio_sweep", map[string]any{
		"port": "/dev/ttyUSB0", "pins": "0-999999999",
	})
	require.NoError(t, err)
	require.True(t, result.IsError)
}

func TestHandleGPIOSweepDefaultPinsWhenOmitted(t *testing.T) {
	testutil.SetupTestPorts(t)
	testutil.SetupTestFlasherFactory(t)
	mock := &testutil.MockFlasher{
		GPIOReservedFunc: func(pin int) (bool, string) {
			if pin >= 3 {
				return true, "nonexistent"
			}
			return false, ""
		},
	}
	setFlasher(t, mock)

	h := newHarness(t)
	result, err := h.CallTool(context.Background(), "esp_gpio_sweep", map[string]any{
		"port": "/dev/ttyUSB0", "dwell": float64(0.001), "both": false,
	})
	require.NoError(t, err)
	require.False(t, result.IsError)

	var got esp.GPIOSweepResult
	require.NoError(t, json.Unmarshal([]byte(testkit.ResultText(result)), &got))
	require.Len(t, got.Pins, 3)
	assert.Equal(t, []int{0, 1, 2}, []int{got.Pins[0].Pin, got.Pins[1].Pin, got.Pins[2].Pin})
}

// TestHandleGPIOSweepNoResetOnExpiry mirrors
// TestHandleGPIOReadNoResetOnExpiry for the sweep handler.
func TestHandleGPIOSweepNoResetOnExpiry(t *testing.T) {
	testutil.SetupTestPorts(t)
	testutil.SetupTestFlasherFactory(t)
	testutil.SetupFastWaitForPort(t)
	orig := session.SetDeferredRestartTimeout(5 * time.Millisecond)
	t.Cleanup(func() { session.SetDeferredRestartTimeout(orig) })

	mock := &raceSafeMockFlasher{MockFlasher: &testutil.MockFlasher{}}
	setFlasher(t, mock)

	h := newHarness(t)
	result, err := h.CallTool(context.Background(), "esp_gpio_sweep", map[string]any{
		// Real path -- see comment in TestHandleGPIOReadNoResetOnExpiry.
		"port": t.TempDir(), "pins": "4", "dwell": float64(0.001), "both": false,
	})
	require.NoError(t, err)
	require.False(t, result.IsError)

	require.Eventually(t, mock.getCloseCalled, time.Second, time.Millisecond,
		"deferred timer should have expired and closed the cached flasher")
	assert.False(t, mock.getResetCalled(), "no-reset hold must skip Reset() on deferred expiry")
}

// assertErr is a tiny error constructor used by these tests to avoid
// pulling in fmt.Errorf just for a static message.
type assertErr string

func (e assertErr) Error() string { return string(e) }
