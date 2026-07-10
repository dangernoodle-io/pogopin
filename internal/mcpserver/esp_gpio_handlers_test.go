package mcpserver

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"dangernoodle.io/pogopin/internal/esp"
	"dangernoodle.io/pogopin/internal/serial"
	"dangernoodle.io/pogopin/internal/session"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	espflasher "tinygo.org/x/espflasher/pkg/espflasher"
)

// raceSafeMockFlasher wraps mockFlasher with a mutex guarding Close/Reset
// and their observability, for tests that poll closeCalled/resetCalled from
// the test goroutine while session's deferred-restart timer goroutine
// concurrently calls Close()/Reset() on expiry (mockFlasher's plain bool
// fields are otherwise unsynchronized, which -race correctly flags across
// goroutines).
type raceSafeMockFlasher struct {
	*mockFlasher
	mu sync.Mutex
}

func (m *raceSafeMockFlasher) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.mockFlasher.Close()
}

func (m *raceSafeMockFlasher) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.mockFlasher.Reset()
}

func (m *raceSafeMockFlasher) getCloseCalled() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.closeCalled
}

func (m *raceSafeMockFlasher) getResetCalled() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.resetCalled
}

func TestHandleGPIOReadSuccess(t *testing.T) {
	setupTestPorts(t)
	setupTestFlasherFactory(t)

	mock := &mockFlasher{readGPIOVal: true}
	session.SetFlasherFactory(func(port string, opts *espflasher.FlasherOptions) (esp.Flasher, error) {
		return mock, nil
	})
	t.Cleanup(func() { session.SetFlasherFactory(esp.DefaultFlasherFactory) })

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{
		"port": "/dev/ttyUSB0",
		"pin":  float64(4),
	}

	result, err := handleGPIORead(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.False(t, result.IsError)

	tc, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)

	var got esp.GPIOReadResult
	require.NoError(t, json.Unmarshal([]byte(tc.Text), &got))
	assert.Equal(t, 4, got.Pin)
	assert.True(t, got.Level)
}

func TestHandleGPIOReadMissingPort(t *testing.T) {
	setupTestPorts(t)

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{
		"pin": float64(4),
	}
	result, err := handleGPIORead(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, result.IsError)
}

func TestHandleGPIOReadMissingPin(t *testing.T) {
	setupTestPorts(t)
	setupTestFlasherFactory(t)

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{
		"port": "/dev/ttyUSB0",
	}
	result, err := handleGPIORead(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, result.IsError)

	tc, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, tc.Text, "pin")
}

func TestHandleGPIOReadError(t *testing.T) {
	setupTestPorts(t)
	setupTestFlasherFactory(t)

	session.SetFlasherFactory(func(port string, opts *espflasher.FlasherOptions) (esp.Flasher, error) {
		return nil, assertErr("connection failed")
	})
	t.Cleanup(func() { session.SetFlasherFactory(esp.DefaultFlasherFactory) })

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{
		"port": "/dev/ttyUSB0",
		"pin":  float64(4),
	}
	result, err := handleGPIORead(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, result.IsError)
}

func TestHandleGPIOReadUsesSessionAcquire(t *testing.T) {
	setupTestPorts(t)
	setupTestFlasherFactory(t)
	setupFastWaitForPort(t)

	testMgr := serial.NewManager()
	testMgr.SetTestState(true, "/dev/ttyUSB0", 115200, nil)
	session.InsertPort("/dev/ttyUSB0", session.NewPortSession(testMgr, "/dev/ttyUSB0", 115200, session.ModeReader))

	mock := &mockFlasher{readGPIOVal: true}
	session.SetFlasherFactory(func(port string, opts *espflasher.FlasherOptions) (esp.Flasher, error) {
		return mock, nil
	})
	t.Cleanup(func() { session.SetFlasherFactory(esp.DefaultFlasherFactory) })

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{
		"port": "/dev/ttyUSB0",
		"pin":  float64(4),
	}
	result, err := handleGPIORead(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, result)

	// Should NOT get "managed by serial_start" error — port is auto-stopped
	// by session.AcquireForFlasher, same as every other esp_ handler.
	if result.IsError {
		tc, ok := result.Content[0].(mcp.TextContent)
		require.True(t, ok)
		assert.NotContains(t, tc.Text, "managed by serial_start")
	}
}

// TestHandleGPIOReadNoResetOnExpiry confirms handleGPIORead releases via
// session.ReleaseFlasherDeferredNoReset (not ReleaseFlasherDeferred): once
// the deferred-restart timer fires, the underlying flasher's Reset() must
// NOT have been called, unlike every mutating esp_ handler.
func TestHandleGPIOReadNoResetOnExpiry(t *testing.T) {
	setupTestPorts(t)
	setupTestFlasherFactory(t)
	setupFastWaitForPort(t)
	orig := session.SetDeferredRestartTimeout(5 * time.Millisecond)
	t.Cleanup(func() { session.SetDeferredRestartTimeout(orig) })

	mock := &raceSafeMockFlasher{mockFlasher: &mockFlasher{readGPIOVal: true}}
	session.SetFlasherFactory(func(port string, opts *espflasher.FlasherOptions) (esp.Flasher, error) {
		return mock, nil
	})
	t.Cleanup(func() { session.SetFlasherFactory(esp.DefaultFlasherFactory) })

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{
		// Real path (see comment in TestHandleFlashEmitsRealProgressNotifications):
		// expireSession's post-expiry WaitForPort os.Stat-checks this path, and a
		// real path returns immediately instead of paying the full 3s deadline —
		// otherwise the timer goroutine outlives this test and races the next
		// test's setupFastWaitForPort mutating the shared package-level interval.
		"port": t.TempDir(),
		"pin":  float64(4),
	}
	result, err := handleGPIORead(context.Background(), req)
	require.NoError(t, err)
	require.False(t, result.IsError)

	require.Eventually(t, mock.getCloseCalled, time.Second, time.Millisecond,
		"deferred timer should have expired and closed the cached flasher")
	assert.False(t, mock.getResetCalled(), "no-reset hold must skip Reset() on deferred expiry")
}

func TestHandleGPIOSetSuccess(t *testing.T) {
	setupTestPorts(t)
	setupTestFlasherFactory(t)

	mock := &mockFlasher{}
	session.SetFlasherFactory(func(port string, opts *espflasher.FlasherOptions) (esp.Flasher, error) {
		return mock, nil
	})
	t.Cleanup(func() { session.SetFlasherFactory(esp.DefaultFlasherFactory) })

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{
		"port":  "/dev/ttyUSB0",
		"pin":   float64(4),
		"level": true,
	}
	result, err := handleGPIOSet(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.False(t, result.IsError)

	require.Len(t, mock.setGPIOCalls, 1)
	assert.Equal(t, 4, mock.setGPIOCalls[0].pin)
	assert.True(t, mock.setGPIOCalls[0].level)
}

func TestHandleGPIOSetNumericLevel(t *testing.T) {
	setupTestPorts(t)
	setupTestFlasherFactory(t)

	mock := &mockFlasher{}
	session.SetFlasherFactory(func(port string, opts *espflasher.FlasherOptions) (esp.Flasher, error) {
		return mock, nil
	})
	t.Cleanup(func() { session.SetFlasherFactory(esp.DefaultFlasherFactory) })

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{
		"port":  "/dev/ttyUSB0",
		"pin":   float64(4),
		"level": float64(0),
	}
	result, err := handleGPIOSet(context.Background(), req)
	require.NoError(t, err)
	require.False(t, result.IsError)
	require.Len(t, mock.setGPIOCalls, 1)
	assert.False(t, mock.setGPIOCalls[0].level)
}

func TestHandleGPIOSetMissingLevel(t *testing.T) {
	setupTestPorts(t)
	setupTestFlasherFactory(t)

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{
		"port": "/dev/ttyUSB0",
		"pin":  float64(4),
	}
	result, err := handleGPIOSet(context.Background(), req)
	require.NoError(t, err)
	require.True(t, result.IsError)

	tc, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, tc.Text, "level")
}

// TestHandleGPIOSetRefusesInputOnlyPin confirms the underlying esp.SetGPIO
// error (e.g. an input-only pin refusal from f.SetGPIO itself, not
// f.GPIOReserved) is surfaced verbatim.
func TestHandleGPIOSetRefusesInputOnlyPin(t *testing.T) {
	setupTestPorts(t)
	setupTestFlasherFactory(t)

	mock := &mockFlasher{setGPIOErr: assertErr("pin 34 is input-only")}
	session.SetFlasherFactory(func(port string, opts *espflasher.FlasherOptions) (esp.Flasher, error) {
		return mock, nil
	})
	t.Cleanup(func() { session.SetFlasherFactory(esp.DefaultFlasherFactory) })

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{
		"port":  "/dev/ttyUSB0",
		"pin":   float64(34),
		"level": true,
	}
	result, err := handleGPIOSet(context.Background(), req)
	require.NoError(t, err)
	require.True(t, result.IsError)

	tc, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, tc.Text, "input-only")
}

// TestHandleGPIOSetRefusesReservedPinByDefault confirms handleGPIOSet gates
// on f.GPIOReserved via the real esp.SetGPIO gating logic — the underlying
// drive is NEVER called for a reserved pin when include_reserved is omitted.
func TestHandleGPIOSetRefusesReservedPinByDefault(t *testing.T) {
	setupTestPorts(t)
	setupTestFlasherFactory(t)

	mock := &mockFlasher{
		gpioReservedFunc: func(pin int) (bool, string) {
			if pin == 6 {
				return true, "flash"
			}
			return false, ""
		},
	}
	session.SetFlasherFactory(func(port string, opts *espflasher.FlasherOptions) (esp.Flasher, error) {
		return mock, nil
	})
	t.Cleanup(func() { session.SetFlasherFactory(esp.DefaultFlasherFactory) })

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{
		"port":  "/dev/ttyUSB0",
		"pin":   float64(6),
		"level": true,
	}
	result, err := handleGPIOSet(context.Background(), req)
	require.NoError(t, err)
	require.True(t, result.IsError)

	tc, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, tc.Text, "GPIO 6 is reserved (flash)")
	assert.Empty(t, mock.setGPIOCalls, "reserved pin must never reach the underlying drive")
}

// TestHandleGPIOSetIncludeReservedDrives confirms include_reserved=true
// plumbs through the handler and overrides the reserved refusal, reaching
// the underlying drive.
func TestHandleGPIOSetIncludeReservedDrives(t *testing.T) {
	setupTestPorts(t)
	setupTestFlasherFactory(t)

	mock := &mockFlasher{
		gpioReservedFunc: func(pin int) (bool, string) { return true, "flash" },
	}
	session.SetFlasherFactory(func(port string, opts *espflasher.FlasherOptions) (esp.Flasher, error) {
		return mock, nil
	})
	t.Cleanup(func() { session.SetFlasherFactory(esp.DefaultFlasherFactory) })

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{
		"port":             "/dev/ttyUSB0",
		"pin":              float64(6),
		"level":            true,
		"include_reserved": true,
	}
	result, err := handleGPIOSet(context.Background(), req)
	require.NoError(t, err)
	require.False(t, result.IsError)

	require.Len(t, mock.setGPIOCalls, 1)
	assert.Equal(t, 6, mock.setGPIOCalls[0].pin)
	assert.True(t, mock.setGPIOCalls[0].level)
}

func TestHandleGPIOSetMissingPort(t *testing.T) {
	setupTestPorts(t)

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{
		"pin":   float64(4),
		"level": true,
	}
	result, err := handleGPIOSet(context.Background(), req)
	require.NoError(t, err)
	require.True(t, result.IsError)
}

func TestHandleGPIOSweepSuccess(t *testing.T) {
	setupTestPorts(t)
	setupTestFlasherFactory(t)

	mock := &mockFlasher{}
	session.SetFlasherFactory(func(port string, opts *espflasher.FlasherOptions) (esp.Flasher, error) {
		return mock, nil
	})
	t.Cleanup(func() { session.SetFlasherFactory(esp.DefaultFlasherFactory) })

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{
		"port":  "/dev/ttyUSB0",
		"pins":  "4,5",
		"dwell": float64(0.001),
		"both":  false,
	}
	result, err := handleGPIOSweep(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.False(t, result.IsError)

	tc, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	var got esp.GPIOSweepResult
	require.NoError(t, json.Unmarshal([]byte(tc.Text), &got))
	require.Len(t, got.Pins, 2)
	assert.Equal(t, 4, got.Pins[0].Pin)
	assert.Equal(t, 5, got.Pins[1].Pin)
	assert.False(t, got.Pins[0].Skipped)
	assert.False(t, got.Pins[1].Skipped)

	require.Len(t, mock.setGPIOCalls, 2)
	assert.Equal(t, 4, mock.setGPIOCalls[0].pin)
	assert.Equal(t, 5, mock.setGPIOCalls[1].pin)
}

func TestHandleGPIOSweepSkipsReservedByDefault(t *testing.T) {
	setupTestPorts(t)
	setupTestFlasherFactory(t)

	mock := &mockFlasher{
		gpioReservedFunc: func(pin int) (bool, string) {
			if pin == 6 {
				return true, "flash"
			}
			return false, ""
		},
	}
	session.SetFlasherFactory(func(port string, opts *espflasher.FlasherOptions) (esp.Flasher, error) {
		return mock, nil
	})
	t.Cleanup(func() { session.SetFlasherFactory(esp.DefaultFlasherFactory) })

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{
		"port":  "/dev/ttyUSB0",
		"pins":  "4,6",
		"dwell": float64(0.001),
		"both":  false,
	}
	result, err := handleGPIOSweep(context.Background(), req)
	require.NoError(t, err)
	require.False(t, result.IsError)

	tc, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	var got esp.GPIOSweepResult
	require.NoError(t, json.Unmarshal([]byte(tc.Text), &got))
	require.Len(t, got.Pins, 2)
	assert.False(t, got.Pins[0].Skipped)
	assert.True(t, got.Pins[1].Skipped)
	assert.Equal(t, "flash", got.Pins[1].Reason)

	for _, call := range mock.setGPIOCalls {
		assert.NotEqual(t, 6, call.pin)
	}
}

func TestHandleGPIOSweepIncludeReserved(t *testing.T) {
	setupTestPorts(t)
	setupTestFlasherFactory(t)

	mock := &mockFlasher{
		gpioReservedFunc: func(pin int) (bool, string) { return true, "flash" },
	}
	session.SetFlasherFactory(func(port string, opts *espflasher.FlasherOptions) (esp.Flasher, error) {
		return mock, nil
	})
	t.Cleanup(func() { session.SetFlasherFactory(esp.DefaultFlasherFactory) })

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{
		"port":             "/dev/ttyUSB0",
		"pins":             "6",
		"dwell":            float64(0.001),
		"both":             false,
		"include_reserved": true,
	}
	result, err := handleGPIOSweep(context.Background(), req)
	require.NoError(t, err)
	require.False(t, result.IsError)
	require.Len(t, mock.setGPIOCalls, 1)
	assert.Equal(t, 6, mock.setGPIOCalls[0].pin)
}

func TestHandleGPIOSweepInvalidPinRange(t *testing.T) {
	setupTestPorts(t)
	setupTestFlasherFactory(t)

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{
		"port": "/dev/ttyUSB0",
		"pins": "0-999999999",
	}
	result, err := handleGPIOSweep(context.Background(), req)
	require.NoError(t, err)
	require.True(t, result.IsError)
}

func TestHandleGPIOSweepMissingPort(t *testing.T) {
	setupTestPorts(t)

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{}
	result, err := handleGPIOSweep(context.Background(), req)
	require.NoError(t, err)
	require.True(t, result.IsError)
}

func TestHandleGPIOSweepDefaultPinsWhenOmitted(t *testing.T) {
	setupTestPorts(t)
	setupTestFlasherFactory(t)

	mock := &mockFlasher{
		gpioReservedFunc: func(pin int) (bool, string) {
			if pin >= 3 {
				return true, "nonexistent"
			}
			return false, ""
		},
	}
	session.SetFlasherFactory(func(port string, opts *espflasher.FlasherOptions) (esp.Flasher, error) {
		return mock, nil
	})
	t.Cleanup(func() { session.SetFlasherFactory(esp.DefaultFlasherFactory) })

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{
		"port":  "/dev/ttyUSB0",
		"dwell": float64(0.001),
		"both":  false,
	}
	result, err := handleGPIOSweep(context.Background(), req)
	require.NoError(t, err)
	require.False(t, result.IsError)

	tc, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	var got esp.GPIOSweepResult
	require.NoError(t, json.Unmarshal([]byte(tc.Text), &got))
	require.Len(t, got.Pins, 3)
	assert.Equal(t, []int{0, 1, 2}, []int{got.Pins[0].Pin, got.Pins[1].Pin, got.Pins[2].Pin})
}

// TestHandleGPIOSweepNoResetOnExpiry mirrors
// TestHandleGPIOReadNoResetOnExpiry for the sweep handler.
func TestHandleGPIOSweepNoResetOnExpiry(t *testing.T) {
	setupTestPorts(t)
	setupTestFlasherFactory(t)
	setupFastWaitForPort(t)
	orig := session.SetDeferredRestartTimeout(5 * time.Millisecond)
	t.Cleanup(func() { session.SetDeferredRestartTimeout(orig) })

	mock := &raceSafeMockFlasher{mockFlasher: &mockFlasher{}}
	session.SetFlasherFactory(func(port string, opts *espflasher.FlasherOptions) (esp.Flasher, error) {
		return mock, nil
	})
	t.Cleanup(func() { session.SetFlasherFactory(esp.DefaultFlasherFactory) })

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{
		// Real path — see comment in TestHandleGPIOReadNoResetOnExpiry.
		"port":  t.TempDir(),
		"pins":  "4",
		"dwell": float64(0.001),
		"both":  false,
	}
	result, err := handleGPIOSweep(context.Background(), req)
	require.NoError(t, err)
	require.False(t, result.IsError)

	require.Eventually(t, mock.getCloseCalled, time.Second, time.Millisecond,
		"deferred timer should have expired and closed the cached flasher")
	assert.False(t, mock.getResetCalled(), "no-reset hold must skip Reset() on deferred expiry")
}

// assertErr is a tiny error constructor used by these tests to avoid pulling
// in fmt.Errorf just for a static message.
type assertErr string

func (e assertErr) Error() string { return string(e) }
