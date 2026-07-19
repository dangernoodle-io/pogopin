package serial

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/dangernoodle-io/shesha/testkit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	goSerial "go.bug.st/serial"
	espflasher "tinygo.org/x/espflasher/pkg/espflasher"

	"dangernoodle.io/pogopin/internal/esp"
	"dangernoodle.io/pogopin/internal/serial"
	"dangernoodle.io/pogopin/internal/session"
	"dangernoodle.io/pogopin/internal/testutil"
)

func TestHandleSerialList(t *testing.T) {
	c := &Capability{}
	h := newHarness(t, c)

	result, err := h.CallTool(context.Background(), "serial_list", map[string]any{})
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Len(t, result.Content, 1)
}

func TestSerialListUnlocksHardware(t *testing.T) {
	called := false
	c := &Capability{UnlockHardware: func() error {
		called = true
		return nil
	}}
	h := newHarness(t, c)

	_, err := h.CallTool(context.Background(), "serial_list", map[string]any{})
	require.NoError(t, err)
	assert.True(t, called, "serial_list must unlock the hardware tier")
}

func TestSerialListNilUnlockHardwareIsSafe(t *testing.T) {
	c := &Capability{}
	h := newHarness(t, c)

	assert.NotPanics(t, func() {
		_, err := h.CallTool(context.Background(), "serial_list", map[string]any{})
		require.NoError(t, err)
	})
}

func TestReadAfterDisconnect(t *testing.T) {
	setupTestPorts(t)

	testMgr := serial.NewManager()
	testMgr.AddToBuffer("line before disconnect")
	testMgr.AddToBuffer("second line")
	testMgr.SetTestState(false, "test-port", 115200, fmt.Errorf("device removed"))
	session.InsertPort("test-port", session.NewPortSession(testMgr, "test-port", 115200, session.ModeReader))

	h := newHarness(t, &Capability{})
	result, err := h.CallTool(context.Background(), "serial_read", map[string]any{})
	require.NoError(t, err)

	text := testkit.ResultText(result)
	assert.Contains(t, text, "line before disconnect")
	assert.Contains(t, text, "second line")
	assert.Contains(t, text, "[serial reader stopped: device removed]")
}

func TestSerialReadPatternFilter(t *testing.T) {
	setupTestPorts(t)

	testMgr := serial.NewManager()
	testMgr.AddToBuffer("INFO: starting up")
	testMgr.AddToBuffer("DEBUG: verbose stuff")
	testMgr.AddToBuffer("INFO: ready")
	testMgr.AddToBuffer("ERROR: something broke")
	testMgr.SetTestState(true, "test-port", 115200, nil)
	session.InsertPort("test-port", session.NewPortSession(testMgr, "test-port", testMgr.Baud(), session.ModeReader))

	h := newHarness(t, &Capability{})
	result, err := h.CallTool(context.Background(), "serial_read", map[string]any{"pattern": "^INFO:"})
	require.NoError(t, err)

	text := testkit.ResultText(result)
	assert.Contains(t, text, "INFO: starting up")
	assert.Contains(t, text, "INFO: ready")
	assert.NotContains(t, text, "DEBUG:")
	assert.NotContains(t, text, "ERROR:")
}

func TestSerialReadInvalidPattern(t *testing.T) {
	setupTestPorts(t)

	testMgr := serial.NewManager()
	testMgr.AddToBuffer("some line")
	testMgr.SetTestState(true, "test-port", 115200, nil)
	session.InsertPort("test-port", session.NewPortSession(testMgr, "test-port", testMgr.Baud(), session.ModeReader))

	h := newHarness(t, &Capability{})
	result, err := h.CallTool(context.Background(), "serial_read", map[string]any{"pattern": "[invalid"})
	require.NoError(t, err)
	assert.Contains(t, testkit.ResultText(result), "invalid pattern")
	assert.True(t, result.IsError)
}

func TestHandleSerialReadNotRunningNoBuffer(t *testing.T) {
	setupTestPorts(t)

	testMgr := serial.NewManager()
	testMgr.SetTestState(false, "test-port", 115200, nil)
	session.InsertPort("test-port", session.NewPortSession(testMgr, "test-port", testMgr.Baud(), session.ModeReader))

	h := newHarness(t, &Capability{})
	result, err := h.CallTool(context.Background(), "serial_read", map[string]any{})
	require.NoError(t, err)
	require.True(t, result.IsError)
	// ResolveSession evicts a dead session (not running, not reconnecting,
	// zero buffered lines) before the handler's own not-running branch runs.
	assert.Contains(t, testkit.ResultText(result), "has stopped")
}

func TestHandleSerialReadNotRunningWithError(t *testing.T) {
	setupTestPorts(t)

	testMgr := serial.NewManager()
	testMgr.SetTestState(false, "test-port", 115200, fmt.Errorf("connection lost"))
	session.InsertPort("test-port", session.NewPortSession(testMgr, "test-port", testMgr.Baud(), session.ModeReader))

	h := newHarness(t, &Capability{})
	result, err := h.CallTool(context.Background(), "serial_read", map[string]any{})
	require.NoError(t, err)
	require.True(t, result.IsError)
	assert.Contains(t, testkit.ResultText(result), "has stopped")
}

func TestHandleSerialReadWithClear(t *testing.T) {
	setupTestPorts(t)

	testMgr := serial.NewManager()
	testMgr.AddToBuffer("line 1")
	testMgr.AddToBuffer("line 2")
	testMgr.AddToBuffer("line 3")
	testMgr.SetTestState(true, "test-port", 115200, nil)
	session.InsertPort("test-port", session.NewPortSession(testMgr, "test-port", testMgr.Baud(), session.ModeReader))

	h := newHarness(t, &Capability{})

	result, err := h.CallTool(context.Background(), "serial_read", map[string]any{"clear": true})
	require.NoError(t, err)
	text := testkit.ResultText(result)
	assert.Contains(t, text, "line 1")
	assert.Contains(t, text, "line 2")
	assert.Contains(t, text, "line 3")

	result2, err2 := h.CallTool(context.Background(), "serial_read", map[string]any{})
	require.NoError(t, err2)
	assert.Equal(t, "", testkit.ResultText(result2))
}

func TestHandleSerialReadRawBypassesFiltering(t *testing.T) {
	setupTestPorts(t)

	noisy := "\x01\x02\x03\x04\x05\x06\x07\x08"
	testMgr := serial.NewManager()
	testMgr.AddToBuffer(noisy)
	testMgr.SetTestState(true, "test-port", 115200, nil)
	session.InsertPort("test-port", session.NewPortSession(testMgr, "test-port", testMgr.Baud(), session.ModeReader))

	h := newHarness(t, &Capability{})
	result, err := h.CallTool(context.Background(), "serial_read", map[string]any{"raw": true})
	require.NoError(t, err)
	text := testkit.ResultText(result)
	assert.NotContains(t, text, "elided")
	assert.Equal(t, noisy, text)
}

func TestHandleSerialReadFiltersNoiseByDefault(t *testing.T) {
	setupTestPorts(t)

	testMgr := serial.NewManager()
	testMgr.AddToBuffer("\x01\x02\x03\x04\x05\x06\x07\x08")
	testMgr.AddToBuffer("INFO: normal line")
	testMgr.SetTestState(true, "test-port", 115200, nil)
	session.InsertPort("test-port", session.NewPortSession(testMgr, "test-port", testMgr.Baud(), session.ModeReader))

	h := newHarness(t, &Capability{})
	result, err := h.CallTool(context.Background(), "serial_read", map[string]any{})
	require.NoError(t, err)
	text := testkit.ResultText(result)
	assert.Contains(t, text, "bytes of framing noise elided")
	assert.Contains(t, text, "INFO: normal line")
}

func TestHandleSerialStop(t *testing.T) {
	setupTestPorts(t)

	testMgr := serial.NewManager()
	testMgr.SetTestState(true, "test-port", 115200, nil)
	session.InsertPort("test-port", session.NewPortSession(testMgr, "test-port", testMgr.Baud(), session.ModeReader))

	h := newHarness(t, &Capability{})
	result, err := h.CallTool(context.Background(), "serial_stop", map[string]any{})
	require.NoError(t, err)
	assert.Contains(t, testkit.ResultText(result), "Stopped reading from test-port")
	assert.Equal(t, 0, session.PortCount())
}

func TestHandleSerialStopNoPort(t *testing.T) {
	setupTestPorts(t)

	h := newHarness(t, &Capability{})
	result, err := h.CallTool(context.Background(), "serial_stop", map[string]any{})
	require.NoError(t, err)
	require.True(t, result.IsError)
}

func TestHandleSerialWriteRaw(t *testing.T) {
	setupTestPorts(t)

	testMgr := serial.NewManager()
	testMgr.SetTestState(true, "test-port", 115200, nil)
	session.InsertPort("test-port", session.NewPortSession(testMgr, "test-port", testMgr.Baud(), session.ModeReader))

	h := newHarness(t, &Capability{})
	result, err := h.CallTool(context.Background(), "serial_write", map[string]any{"data": "hello", "raw": true})
	require.NoError(t, err)
	require.True(t, result.IsError)
	assert.Contains(t, testkit.ResultText(result), "serial port is not running")
}

func TestHandleSerialWriteNotRunning(t *testing.T) {
	setupTestPorts(t)

	testMgr := serial.NewManager()
	testMgr.SetTestState(false, "test-port", 115200, nil)
	session.InsertPort("test-port", session.NewPortSession(testMgr, "test-port", testMgr.Baud(), session.ModeReader))

	h := newHarness(t, &Capability{})
	result, err := h.CallTool(context.Background(), "serial_write", map[string]any{"data": "hello"})
	require.NoError(t, err)
	assert.Contains(t, testkit.ResultText(result), "has stopped")
}

func TestHandleSerialWriteMissingDataIsSchemaRejected(t *testing.T) {
	setupTestPorts(t)

	h := newHarness(t, &Capability{})
	result, err := h.CallTool(context.Background(), "serial_write", map[string]any{})
	require.NoError(t, err)
	require.True(t, result.IsError)
	assert.Contains(t, testkit.ResultText(result), "data")
}

func TestHandleSerialStatusSinglePort(t *testing.T) {
	setupTestPorts(t)

	testMgr := serial.NewManager()
	testMgr.SetTestState(true, "test-port", 115200, nil)
	testMgr.AddToBuffer("line 1")
	testMgr.AddToBuffer("line 2")
	testMgr.AddToBuffer("line 3")
	session.InsertPort("test-port", session.NewPortSession(testMgr, "test-port", testMgr.Baud(), session.ModeReader))

	h := newHarness(t, &Capability{})
	result, err := h.CallTool(context.Background(), "serial_status", map[string]any{})
	require.NoError(t, err)
	text := testkit.ResultText(result)
	assert.Contains(t, text, "\"running\": true")
	assert.Contains(t, text, "\"port\": \"test-port\"")
	assert.Contains(t, text, "\"baud\": 115200")
	assert.Contains(t, text, "\"buffer_lines\": 3")
	assert.Contains(t, text, "\"reconnecting\":")
}

func TestHandleSerialStatusMultiplePorts(t *testing.T) {
	setupTestPorts(t)

	testMgrA := serial.NewManager()
	testMgrA.SetTestState(true, "port-a", 9600, nil)
	session.InsertPort("port-a", session.NewPortSession(testMgrA, "port-a", testMgrA.Baud(), session.ModeReader))

	testMgrB := serial.NewManager()
	testMgrB.SetTestState(true, "port-b", 115200, nil)
	session.InsertPort("port-b", session.NewPortSession(testMgrB, "port-b", testMgrB.Baud(), session.ModeReader))

	h := newHarness(t, &Capability{})
	result, err := h.CallTool(context.Background(), "serial_status", map[string]any{})
	require.NoError(t, err)
	text := testkit.ResultText(result)
	assert.Contains(t, text, "\"ports\"")
	assert.Contains(t, text, "\"port-a\"")
	assert.Contains(t, text, "\"port-b\"")
}

func TestHandleSerialStatusExplicitPort(t *testing.T) {
	setupTestPorts(t)

	testMgrA := serial.NewManager()
	testMgrA.SetTestState(true, "port-a", 9600, nil)
	session.InsertPort("port-a", session.NewPortSession(testMgrA, "port-a", testMgrA.Baud(), session.ModeReader))

	testMgrB := serial.NewManager()
	testMgrB.SetTestState(true, "port-b", 115200, nil)
	session.InsertPort("port-b", session.NewPortSession(testMgrB, "port-b", testMgrB.Baud(), session.ModeReader))

	h := newHarness(t, &Capability{})
	result, err := h.CallTool(context.Background(), "serial_status", map[string]any{"port": "port-a"})
	require.NoError(t, err)
	text := testkit.ResultText(result)
	assert.Contains(t, text, "\"port\": \"port-a\"")
	assert.NotContains(t, text, "\"ports\"")
	assert.NotContains(t, text, "port-b")
}

func TestHandleSerialStartSuccess(t *testing.T) {
	setupTestPorts(t)
	setupTestManagersFunc(t)

	session.SetNewManagerFunc(func(bufSize int) *serial.Manager {
		m := serial.NewManagerWithBufferSize(bufSize)
		m.OpenFunc = func(name string, mode *goSerial.Mode) (goSerial.Port, error) {
			return &noopPort{}, nil
		}
		return m
	})

	h := newHarness(t, &Capability{})
	result, err := h.CallTool(context.Background(), "serial_start", map[string]any{
		"port": "test-port", "baud": 9600, "buffer_size": 500,
	})
	require.NoError(t, err)
	require.False(t, result.IsError)
	assert.Contains(t, testkit.ResultText(result), "Started reading from test-port at 9600 baud")
}

func TestHandleSerialStartMissingPortIsSchemaRejected(t *testing.T) {
	setupTestPorts(t)

	h := newHarness(t, &Capability{})
	result, err := h.CallTool(context.Background(), "serial_start", map[string]any{})
	require.NoError(t, err)
	require.True(t, result.IsError)
	assert.Contains(t, testkit.ResultText(result), "port")
}

func TestSerialStartUnlocksHardware(t *testing.T) {
	setupTestPorts(t)
	setupTestManagersFunc(t)

	session.SetNewManagerFunc(func(bufSize int) *serial.Manager {
		m := serial.NewManagerWithBufferSize(bufSize)
		m.OpenFunc = func(name string, mode *goSerial.Mode) (goSerial.Port, error) {
			return &noopPort{}, nil
		}
		return m
	})

	called := false
	h := newHarness(t, &Capability{UnlockHardware: func() error {
		called = true
		return nil
	}})

	// Even if the call itself errors, the unlock must still run.
	_, _ = h.CallTool(context.Background(), "serial_start", map[string]any{"port": "test-port"})
	assert.True(t, called)
}

func TestHandleSerialStartOpenError(t *testing.T) {
	setupTestPorts(t)
	setupTestManagersFunc(t)

	session.SetNewManagerFunc(func(bufSize int) *serial.Manager {
		m := serial.NewManagerWithBufferSize(bufSize)
		m.OpenFunc = func(name string, mode *goSerial.Mode) (goSerial.Port, error) {
			return nil, fmt.Errorf("device busy")
		}
		return m
	})

	h := newHarness(t, &Capability{})
	result, err := h.CallTool(context.Background(), "serial_start", map[string]any{"port": "test-port"})
	require.NoError(t, err)
	require.True(t, result.IsError)
	assert.Contains(t, testkit.ResultText(result), "device busy")
}

func TestHandleSerialStartReusesExistingManager(t *testing.T) {
	setupTestPorts(t)
	setupTestManagersFunc(t)

	m := serial.NewManagerWithBufferSize(1000)
	m.OpenFunc = func(name string, mode *goSerial.Mode) (goSerial.Port, error) {
		return &noopPort{}, nil
	}
	session.InsertPort("test-port", session.NewPortSession(m, "test-port", m.Baud(), session.ModeReader))

	h := newHarness(t, &Capability{})
	result, err := h.CallTool(context.Background(), "serial_start", map[string]any{"port": "test-port", "baud": 9600})
	require.NoError(t, err)
	require.False(t, result.IsError)
	assert.Equal(t, 1, session.PortCount())

	_ = m.Stop()
}

func TestHandleSerialStartAutoResetFalse(t *testing.T) {
	setupTestPorts(t)
	setupTestManagersFunc(t)

	session.SetNewManagerFunc(func(bufSize int) *serial.Manager {
		m := serial.NewManagerWithBufferSize(bufSize)
		m.OpenFunc = func(name string, mode *goSerial.Mode) (goSerial.Port, error) {
			return &noopPort{}, nil
		}
		return m
	})

	h := newHarness(t, &Capability{})
	result, err := h.CallTool(context.Background(), "serial_start", map[string]any{
		"port": "/dev/cu.usbmodem1101", "auto_reset": false,
	})
	require.NoError(t, err)
	require.False(t, result.IsError)
	text := testkit.ResultText(result)
	assert.Contains(t, text, "Started reading from /dev/cu.usbmodem1101")
	assert.NotContains(t, text, "auto-reset")
}

func TestHandleSerialStartAutoResetNonUSB(t *testing.T) {
	setupTestPorts(t)
	setupTestManagersFunc(t)

	origIsUSB := session.SetIsUSBPortFn(func(port string) bool { return false })
	t.Cleanup(func() { session.SetIsUSBPortFn(origIsUSB) })

	session.SetNewManagerFunc(func(bufSize int) *serial.Manager {
		m := serial.NewManagerWithBufferSize(bufSize)
		m.OpenFunc = func(name string, mode *goSerial.Mode) (goSerial.Port, error) {
			return &noopPort{}, nil
		}
		return m
	})

	h := newHarness(t, &Capability{})
	result, err := h.CallTool(context.Background(), "serial_start", map[string]any{"port": "/dev/ttyS0"})
	require.NoError(t, err)
	require.False(t, result.IsError)
	text := testkit.ResultText(result)
	assert.Contains(t, text, "Started reading from /dev/ttyS0")
	assert.NotContains(t, text, "auto-reset")
}

// TestHandleSerialStartAutoResetPortUnchanged exercises
// startSessionWithAutoReset's reset branch (session.IsUSBPort true) on a USB
// CDC device whose port name survives the reset: AcquireForFlasher ->
// esp.ResetESP -> ReleaseFlasherImmediate, with WaitForPort re-finding the
// same path via os.Stat, so ReleaseFlasherImmediate returns "" and the
// "rebooted for output" (port-unchanged) message branch fires.
func TestHandleSerialStartAutoResetPortUnchanged(t *testing.T) {
	setupTestPorts(t)
	setupTestManagersFunc(t)

	tmpfile, err := os.CreateTemp(t.TempDir(), "test-port-*")
	require.NoError(t, err)
	require.NoError(t, tmpfile.Close())

	session.SetNewManagerFunc(func(bufSize int) *serial.Manager {
		m := serial.NewManagerWithBufferSize(bufSize)
		m.OpenFunc = func(name string, mode *goSerial.Mode) (goSerial.Port, error) {
			return &noopPort{}, nil
		}
		return m
	})

	origUSB := session.SetIsUSBPortFn(func(port string) bool { return true })
	t.Cleanup(func() { session.SetIsUSBPortFn(origUSB) })

	origFactory := session.SetFlasherFactory(func(port string, opts *espflasher.FlasherOptions) (esp.Flasher, error) {
		return &resetMockFlasher{}, nil
	})
	t.Cleanup(func() { session.SetFlasherFactory(origFactory) })

	origWait := session.SetWaitForPortInterval(time.Millisecond)
	t.Cleanup(func() { session.SetWaitForPortInterval(origWait) })

	h := newHarness(t, &Capability{})
	result, err := h.CallTool(context.Background(), "serial_start", map[string]any{"port": tmpfile.Name()})
	require.NoError(t, err)
	require.False(t, result.IsError)
	assert.Contains(t, testkit.ResultText(result), "auto-reset: USB CDC device rebooted for output")
}

// TestHandleSerialStartAutoResetPortChanged exercises the same reset branch
// but where the port re-enumerates under a new name (e.g. macOS/Linux USB
// CDC devices commonly do), so ReleaseFlasherImmediate's WaitForPort finds a
// different name via serial.FindSimilarPort and the "port changed to %s"
// message branch fires.
func TestHandleSerialStartAutoResetPortChanged(t *testing.T) {
	setupTestPorts(t)
	setupTestManagersFunc(t)

	session.SetNewManagerFunc(func(bufSize int) *serial.Manager {
		m := serial.NewManagerWithBufferSize(bufSize)
		m.OpenFunc = func(name string, mode *goSerial.Mode) (goSerial.Port, error) {
			return &noopPort{}, nil
		}
		return m
	})

	origUSB := session.SetIsUSBPortFn(func(port string) bool { return true })
	t.Cleanup(func() { session.SetIsUSBPortFn(origUSB) })

	origFactory := session.SetFlasherFactory(func(port string, opts *espflasher.FlasherOptions) (esp.Flasher, error) {
		return &resetMockFlasher{}, nil
	})
	t.Cleanup(func() { session.SetFlasherFactory(origFactory) })

	origWait := session.SetWaitForPortInterval(time.Millisecond)
	t.Cleanup(func() { session.SetWaitForPortInterval(origWait) })

	callCount := 0
	origList := session.SetListPortsFn(func() ([]serial.PortInfo, error) {
		callCount++
		if callCount == 1 {
			// Pre-reset snapshot (AcquireForFlasher.snapshotPortNames): the
			// re-enumerated name below must not be "already known".
			return nil, nil
		}
		return []serial.PortInfo{{Name: "/dev/ttyUSB99"}}, nil
	})
	t.Cleanup(func() { session.SetListPortsFn(origList) })

	h := newHarness(t, &Capability{})
	result, err := h.CallTool(context.Background(), "serial_start", map[string]any{"port": "/dev/ttyUSB0"})
	require.NoError(t, err)
	require.False(t, result.IsError)
	assert.Contains(t, testkit.ResultText(result), "auto-reset: USB CDC device rebooted, port changed to /dev/ttyUSB99")
}

func TestHandleSerialRestartOpenPortPreservesBaud(t *testing.T) {
	setupTestPorts(t)
	setupTestManagersFunc(t)

	session.SetNewManagerFunc(func(bufSize int) *serial.Manager {
		m := serial.NewManagerWithBufferSize(bufSize)
		m.OpenFunc = func(name string, mode *goSerial.Mode) (goSerial.Port, error) {
			return &noopPort{}, nil
		}
		return m
	})

	m := serial.NewManagerWithBufferSize(1000)
	m.OpenFunc = func(name string, mode *goSerial.Mode) (goSerial.Port, error) {
		return &noopPort{}, nil
	}
	session.InsertPort("test-port", session.NewPortSession(m, "test-port", 57600, session.ModeReader))
	require.NoError(t, m.Start("test-port", 57600))
	require.True(t, m.IsRunning())

	h := newHarness(t, &Capability{})
	result, err := h.CallTool(context.Background(), "serial_restart", map[string]any{"port": "test-port"})
	require.NoError(t, err)
	require.False(t, result.IsError)
	assert.Contains(t, testkit.ResultText(result), "Restarted reading from test-port at 57600 baud")
	assert.Equal(t, 1, session.PortCount())

	newMgr, resolvedPort, err := session.ResolveSession(map[string]interface{}{"port": "test-port"})
	require.NoError(t, err)
	assert.Equal(t, "test-port", resolvedPort)
	assert.True(t, newMgr.IsRunning())
	assert.Equal(t, 57600, newMgr.Baud())
	_ = newMgr.Stop()
}

func TestHandleSerialRestartClosedPortBehavesLikeStart(t *testing.T) {
	setupTestPorts(t)
	setupTestManagersFunc(t)

	session.SetNewManagerFunc(func(bufSize int) *serial.Manager {
		m := serial.NewManagerWithBufferSize(bufSize)
		m.OpenFunc = func(name string, mode *goSerial.Mode) (goSerial.Port, error) {
			return &noopPort{}, nil
		}
		return m
	})

	h := newHarness(t, &Capability{})
	result, err := h.CallTool(context.Background(), "serial_restart", map[string]any{"port": "test-port"})
	require.NoError(t, err)
	require.False(t, result.IsError)
	assert.Contains(t, testkit.ResultText(result), "Restarted reading from test-port at 115200 baud")
	assert.Equal(t, 1, session.PortCount())

	m, _, err := session.ResolveSession(map[string]interface{}{"port": "test-port"})
	require.NoError(t, err)
	_ = m.Stop()
}

func TestHandleSerialRestartArgsOverridePreservedBaud(t *testing.T) {
	setupTestPorts(t)
	setupTestManagersFunc(t)

	session.SetNewManagerFunc(func(bufSize int) *serial.Manager {
		m := serial.NewManagerWithBufferSize(bufSize)
		m.OpenFunc = func(name string, mode *goSerial.Mode) (goSerial.Port, error) {
			return &noopPort{}, nil
		}
		return m
	})

	m := serial.NewManagerWithBufferSize(1000)
	m.OpenFunc = func(name string, mode *goSerial.Mode) (goSerial.Port, error) {
		return &noopPort{}, nil
	}
	session.InsertPort("test-port", session.NewPortSession(m, "test-port", 9600, session.ModeReader))
	require.NoError(t, m.Start("test-port", 9600))

	h := newHarness(t, &Capability{})
	result, err := h.CallTool(context.Background(), "serial_restart", map[string]any{"port": "test-port", "baud": 230400})
	require.NoError(t, err)
	require.False(t, result.IsError)
	assert.Contains(t, testkit.ResultText(result), "Restarted reading from test-port at 230400 baud")

	newMgr, _, err := session.ResolveSession(map[string]interface{}{"port": "test-port"})
	require.NoError(t, err)
	assert.Equal(t, 230400, newMgr.Baud())
	_ = newMgr.Stop()
}

func TestHandleSerialRestartMissingPortIsSchemaRejected(t *testing.T) {
	setupTestPorts(t)

	h := newHarness(t, &Capability{})
	result, err := h.CallTool(context.Background(), "serial_restart", map[string]any{})
	require.NoError(t, err)
	require.True(t, result.IsError)
}

func TestSerialRestartUnlocksHardware(t *testing.T) {
	setupTestPorts(t)
	setupTestManagersFunc(t)

	session.SetNewManagerFunc(func(bufSize int) *serial.Manager {
		m := serial.NewManagerWithBufferSize(bufSize)
		m.OpenFunc = func(name string, mode *goSerial.Mode) (goSerial.Port, error) {
			return &noopPort{}, nil
		}
		return m
	})

	called := false
	h := newHarness(t, &Capability{UnlockHardware: func() error {
		called = true
		return nil
	}})

	_, _ = h.CallTool(context.Background(), "serial_restart", map[string]any{"port": "test-port"})
	assert.True(t, called)
}

func TestHandleSerialRestartOpenError(t *testing.T) {
	setupTestPorts(t)
	setupTestManagersFunc(t)

	session.SetNewManagerFunc(func(bufSize int) *serial.Manager {
		m := serial.NewManagerWithBufferSize(bufSize)
		m.OpenFunc = func(name string, mode *goSerial.Mode) (goSerial.Port, error) {
			return nil, fmt.Errorf("device busy")
		}
		return m
	})

	h := newHarness(t, &Capability{})
	result, err := h.CallTool(context.Background(), "serial_restart", map[string]any{"port": "test-port", "buffer_size": 2000})
	require.NoError(t, err)
	require.True(t, result.IsError)
	assert.Contains(t, testkit.ResultText(result), "device busy")
}

// TestSerialCapabilityProgressLifecycle proves every serial_* handler
// wires mcpprogress.LifecycleStatus: a client-supplied progress token
// yields at least a start and completion notification.
func TestSerialCapabilityProgressLifecycle(t *testing.T) {
	setupTestPorts(t)

	h := newHarness(t, &Capability{})
	token := "tok-serial-list"
	_, err := h.CallToolWithProgressToken(context.Background(), "serial_list", map[string]any{}, token)
	require.NoError(t, err)

	// Progress notifications are delivered to the harness on the client's
	// async receive goroutine (see testutil.WaitForProgressComplete), so
	// poll for the terminal completion tick instead of reading
	// ProgressEvents immediately -- otherwise this races that delivery and
	// flakes under CI's slower/contended runners.
	events := testutil.WaitForProgressComplete(t, h, token, "serial_list")
	require.GreaterOrEqual(t, len(events), 2, "expected at least a start and completion tick")
	assert.Contains(t, events[0].Message, "start: serial_list")
	assert.Contains(t, events[len(events)-1].Message, "complete: serial_list")
}
