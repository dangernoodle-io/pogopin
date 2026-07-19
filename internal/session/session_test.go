package session

import (
	"fmt"
	"os"
	"testing"
	"time"

	"dangernoodle.io/pogopin/internal/esp"
	"dangernoodle.io/pogopin/internal/serial"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	goSerial "go.bug.st/serial"
	espflasher "tinygo.org/x/espflasher/pkg/espflasher"
)

// Test helpers

func setupTestPorts(t *testing.T) {
	orig := ports
	t.Cleanup(func() {
		// Best-effort stop every session's pending timer (releases
		// expireSessionWG's reservation when Stop() wins the race) and close
		// cached flashers.
		portsMu.Lock()
		for _, sess := range ports {
			stopSessionTimerLocked(sess)
			if sess.flasher != nil {
				sess.flasher.Reset()
				_ = sess.flasher.Close()
				sess.flasher = nil
			}
		}
		portsMu.Unlock()
		// Join any timer callback that had already fired (or was actively
		// running) when Stop() above returned false — guarantees no leaked
		// expireSession goroutine from this test can survive into the next
		// test and race its setup (BR-63). Must run before swapping ports
		// back: the in-flight goroutine still touches the current map.
		WaitForExpireSessions()
		ports = orig
	})
	ports = map[string]*PortSession{}
}

func setupFastDeferred(t *testing.T) {
	orig := SetDeferredRestartTimeout(10 * time.Millisecond)
	t.Cleanup(func() { SetDeferredRestartTimeout(orig) })
}

func setupFastWaitForPort(t *testing.T) {
	orig := SetWaitForPortInterval(time.Millisecond)
	t.Cleanup(func() { SetWaitForPortInterval(orig) })
}

func setupFastSyncRetry(t *testing.T) {
	orig := SetSyncRetryDelay(time.Millisecond)
	t.Cleanup(func() { SetSyncRetryDelay(orig) })
}

func setupTestManagersFunc(t *testing.T) {
	origFactory := newManagerFunc
	t.Cleanup(func() { newManagerFunc = origFactory })
}

func setupTestFlasherFactory(t *testing.T) {
	origFactory := newFlasherFactory
	t.Cleanup(func() { newFlasherFactory = origFactory })
}

func setupTestIsUSBPort(t *testing.T) {
	orig := isUSBPortFn
	t.Cleanup(func() { isUSBPortFn = orig })
	// Return true for macOS-style USB port patterns (e.g., /dev/cu.usbmodem*)
	isUSBPortFn = func(port string) bool {
		// Match macOS pattern for testing
		return len(port) > 7 && port[:7] == "/dev/cu"
	}
}

// BorrowedFlasher tests

func TestBorrowedFlasherResetIsNoop(t *testing.T) {
	mock := &mockFlasher{
		chipNameVal: "ESP32",
	}
	returnCalled := false
	borrowed := &BorrowedFlasher{
		Flasher: mock,
		onReturn: func(f esp.Flasher) {
			returnCalled = true
		},
	}

	borrowed.Reset()
	assert.False(t, mock.resetCalled, "underlying Reset should not be called")
	assert.False(t, returnCalled, "onReturn should not be called yet")
}

func TestBorrowedFlasherCloseCallsOnReturn(t *testing.T) {
	mock := &mockFlasher{
		chipNameVal: "ESP32",
	}
	returnedFlasher := false
	borrowed := &BorrowedFlasher{
		Flasher: mock,
		onReturn: func(f esp.Flasher) {
			returnedFlasher = true
			assert.Equal(t, mock, f)
		},
	}

	err := borrowed.Close()
	assert.NoError(t, err)
	assert.True(t, returnedFlasher, "onReturn should be called")
	assert.False(t, mock.closeCalled, "underlying Close should not be called by borrowed")
}

func TestBorrowedFlasherMethodsPassThrough(t *testing.T) {
	mock := &mockFlasher{
		chipNameVal: "ESP32",
	}
	borrowed := &BorrowedFlasher{
		Flasher:  mock,
		onReturn: func(f esp.Flasher) {},
	}

	// Test that methods delegate to underlying
	assert.Equal(t, "ESP32", borrowed.ChipName())
	assert.Equal(t, mock.chipTypVal, borrowed.ChipType())
}

// WaitForPort tests

func TestWaitForPortExistsImmediately(t *testing.T) {
	setupFastWaitForPort(t)

	// Create a temp file
	tmpfile, err := os.CreateTemp(t.TempDir(), "test-port-*")
	require.NoError(t, err)
	defer os.Remove(tmpfile.Name())
	tmpfile.Close()

	result := WaitForPort(tmpfile.Name(), 5*time.Second, nil)
	assert.Equal(t, tmpfile.Name(), result)
}

func TestWaitForPortTimeout(t *testing.T) {
	setupFastWaitForPort(t)

	result := WaitForPort("/nonexistent/port/path", 50*time.Millisecond, nil)
	assert.Equal(t, "", result)
}

func TestWaitForPortReenumerates(t *testing.T) {
	setupFastWaitForPort(t)
	origListPorts := listPortsFn
	t.Cleanup(func() { listPortsFn = origListPorts })

	originalPort := "/dev/ttyUSB0"
	reenumeratedPort := "/dev/ttyUSB1"

	// Mock ListPorts to return the reenumerated port after a few calls
	callCount := 0
	listPortsFn = func() ([]serial.PortInfo, error) {
		callCount++
		if callCount > 1 {
			return []serial.PortInfo{
				{Name: reenumeratedPort},
			}, nil
		}
		return []serial.PortInfo{}, nil
	}

	result := WaitForPort(originalPort, 100*time.Millisecond, nil)
	assert.Equal(t, reenumeratedPort, result)
}

// StartSession / StopSession tests

func TestStartSessionCreatesNew(t *testing.T) {
	setupTestPorts(t)
	setupTestManagersFunc(t)

	created := false
	newManagerFunc = func(bufSize int) *serial.Manager {
		created = true
		mgr := serial.NewManager()
		mgr.OpenFunc = func(portName string, mode *goSerial.Mode) (goSerial.Port, error) {
			return &noopPort{}, nil
		}
		return mgr
	}

	err := StartSession("/dev/test", 115200, 1000)
	assert.NoError(t, err)
	assert.True(t, created)

	portsMu.Lock()
	sess, exists := ports["/dev/test"]
	portsMu.Unlock()
	require.True(t, exists)
	assert.Equal(t, "/dev/test", sess.port)
	assert.Equal(t, 115200, sess.baud)
	assert.Equal(t, ModeReader, sess.mode)
}

func TestStartSessionRestartsExisting(t *testing.T) {
	setupTestPorts(t)
	setupTestManagersFunc(t)

	mgr := serial.NewManager()
	mgr.OpenFunc = func(portName string, mode *goSerial.Mode) (goSerial.Port, error) {
		return &noopPort{}, nil
	}
	mgr.SetTestState(true, "/dev/test", 115200, nil)

	portsMu.Lock()
	ports["/dev/test"] = &PortSession{
		mgr:  mgr,
		port: "/dev/test",
		baud: 115200,
		mode: ModeReader,
	}
	portsMu.Unlock()

	err := StartSession("/dev/test", 230400, 1000)
	assert.NoError(t, err)

	portsMu.Lock()
	sess := ports["/dev/test"]
	portsMu.Unlock()
	assert.Equal(t, 230400, sess.baud)
}

func TestStopSessionCleansUp(t *testing.T) {
	setupTestPorts(t)

	mgr := serial.NewManager()
	mgr.OpenFunc = func(portName string, mode *goSerial.Mode) (goSerial.Port, error) {
		return &noopPort{}, nil
	}
	mgr.SetTestState(true, "/dev/test", 115200, nil)

	portsMu.Lock()
	ports["/dev/test"] = &PortSession{
		mgr:  mgr,
		port: "/dev/test",
		baud: 115200,
		mode: ModeReader,
	}
	portsMu.Unlock()

	err := StopSession("/dev/test")
	assert.NoError(t, err)

	portsMu.Lock()
	_, exists := ports["/dev/test"]
	portsMu.Unlock()
	assert.False(t, exists)
}

func TestStopSessionNotFound(t *testing.T) {
	setupTestPorts(t)

	err := StopSession("/dev/nonexistent")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no serial port open")
}

// RestartSession tests

func TestRestartSessionOpenPortPreservesBaudAndTearsDown(t *testing.T) {
	setupTestPorts(t)
	setupTestManagersFunc(t)

	mgr := serial.NewManager()
	mgr.OpenFunc = func(portName string, mode *goSerial.Mode) (goSerial.Port, error) {
		return &noopPort{}, nil
	}
	mgr.SetTestState(true, "/dev/test", 57600, nil)

	fl := &mockFlasher{}
	timerFired := false
	timer := time.AfterFunc(time.Hour, func() { timerFired = true })

	portsMu.Lock()
	ports["/dev/test"] = &PortSession{
		mgr:     mgr,
		port:    "/dev/test",
		baud:    57600,
		mode:    ModePending,
		flasher: fl,
		timer:   timer,
	}
	portsMu.Unlock()

	var createdBufSize int
	newManagerFunc = func(bufSize int) *serial.Manager {
		createdBufSize = bufSize
		m := serial.NewManager()
		m.OpenFunc = func(portName string, mode *goSerial.Mode) (goSerial.Port, error) {
			return &noopPort{}, nil
		}
		return m
	}

	baud, err := RestartSession("/dev/test", nil, 500)
	require.NoError(t, err)
	assert.Equal(t, 57600, baud)
	assert.Equal(t, 500, createdBufSize)

	// teardownSessionLocked must have fired the old flasher's Reset/Close and
	// canceled the old timer.
	assert.True(t, fl.resetCalled)
	assert.True(t, fl.closeCalled)
	assert.False(t, timerFired)

	portsMu.Lock()
	sess, exists := ports["/dev/test"]
	portsMu.Unlock()
	require.True(t, exists)
	assert.Equal(t, 57600, sess.baud)
	assert.Equal(t, ModeReader, sess.mode)
	assert.NotSame(t, mgr, sess.mgr)
	assert.True(t, sess.mgr.IsRunning())
	_ = sess.mgr.Stop()
}

func TestRestartSessionClosedPortDefaultsBaud(t *testing.T) {
	setupTestPorts(t)
	setupTestManagersFunc(t)

	newManagerFunc = func(bufSize int) *serial.Manager {
		m := serial.NewManager()
		m.OpenFunc = func(portName string, mode *goSerial.Mode) (goSerial.Port, error) {
			return &noopPort{}, nil
		}
		return m
	}

	baud, err := RestartSession("/dev/unknown", nil, 1000)
	require.NoError(t, err)
	assert.Equal(t, 115200, baud)

	portsMu.Lock()
	sess, exists := ports["/dev/unknown"]
	portsMu.Unlock()
	require.True(t, exists)
	assert.Equal(t, 115200, sess.baud)
	assert.Equal(t, ModeReader, sess.mode)
	_ = sess.mgr.Stop()
}

func TestRestartSessionBaudOverrideWins(t *testing.T) {
	setupTestPorts(t)
	setupTestManagersFunc(t)

	mgr := serial.NewManager()
	mgr.OpenFunc = func(portName string, mode *goSerial.Mode) (goSerial.Port, error) {
		return &noopPort{}, nil
	}
	mgr.SetTestState(true, "/dev/test", 9600, nil)

	portsMu.Lock()
	ports["/dev/test"] = &PortSession{
		mgr:  mgr,
		port: "/dev/test",
		baud: 9600,
		mode: ModeReader,
	}
	portsMu.Unlock()

	newManagerFunc = func(bufSize int) *serial.Manager {
		m := serial.NewManager()
		m.OpenFunc = func(portName string, mode *goSerial.Mode) (goSerial.Port, error) {
			return &noopPort{}, nil
		}
		return m
	}

	override := 230400
	baud, err := RestartSession("/dev/test", &override, 1000)
	require.NoError(t, err)
	assert.Equal(t, 230400, baud)

	portsMu.Lock()
	sess, exists := ports["/dev/test"]
	portsMu.Unlock()
	require.True(t, exists)
	assert.Equal(t, 230400, sess.baud)
	_ = sess.mgr.Stop()
}

func TestRestartSessionStartError(t *testing.T) {
	setupTestPorts(t)
	setupTestManagersFunc(t)

	newManagerFunc = func(bufSize int) *serial.Manager {
		m := serial.NewManager()
		m.OpenFunc = func(portName string, mode *goSerial.Mode) (goSerial.Port, error) {
			return nil, fmt.Errorf("open failed")
		}
		return m
	}

	baud, err := RestartSession("/dev/broken", nil, 1000)
	assert.Error(t, err)
	assert.Equal(t, 115200, baud)

	portsMu.Lock()
	_, exists := ports["/dev/broken"]
	portsMu.Unlock()
	assert.True(t, exists, "session should still be tracked even though Start failed")
}

// ResolveSession tests

func TestResolveSessionSinglePort(t *testing.T) {
	setupTestPorts(t)

	mgr := serial.NewManager()
	mgr.SetTestState(true, "/dev/test", 115200, nil)

	portsMu.Lock()
	ports["/dev/test"] = &PortSession{
		mgr:  mgr,
		port: "/dev/test",
		baud: 115200,
		mode: ModeReader,
	}
	portsMu.Unlock()

	m, port, err := ResolveSession(map[string]interface{}{})
	require.NoError(t, err)
	assert.Equal(t, "/dev/test", port)
	assert.Equal(t, mgr, m)
}

func TestResolveSessionMultiplePorts(t *testing.T) {
	setupTestPorts(t)

	mgr1 := serial.NewManager()
	mgr1.SetTestState(true, "/dev/test1", 115200, nil)
	mgr2 := serial.NewManager()
	mgr2.SetTestState(true, "/dev/test2", 115200, nil)

	portsMu.Lock()
	ports["/dev/test1"] = &PortSession{
		mgr:  mgr1,
		port: "/dev/test1",
		baud: 115200,
		mode: ModeReader,
	}
	ports["/dev/test2"] = &PortSession{
		mgr:  mgr2,
		port: "/dev/test2",
		baud: 115200,
		mode: ModeReader,
	}
	portsMu.Unlock()

	_, _, err := ResolveSession(map[string]interface{}{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "multiple ports open")
}

func TestResolveSessionNoPorts(t *testing.T) {
	setupTestPorts(t)

	_, _, err := ResolveSession(map[string]interface{}{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no serial port open")
}

func TestResolveSessionExplicitPort(t *testing.T) {
	setupTestPorts(t)

	mgr := serial.NewManager()
	mgr.SetTestState(true, "/dev/test", 115200, nil)

	portsMu.Lock()
	ports["/dev/test"] = &PortSession{
		mgr:  mgr,
		port: "/dev/test",
		baud: 115200,
		mode: ModeReader,
	}
	portsMu.Unlock()

	m, port, err := ResolveSession(map[string]interface{}{"port": "/dev/test"})
	require.NoError(t, err)
	assert.Equal(t, "/dev/test", port)
	assert.Equal(t, mgr, m)
}

func TestResolveSessionEvictsDeadSession(t *testing.T) {
	setupTestPorts(t)

	mgr := serial.NewManager()
	mgr.SetTestState(false, "/dev/test", 115200, nil)

	portsMu.Lock()
	ports["/dev/test"] = &PortSession{
		mgr:  mgr,
		port: "/dev/test",
		baud: 115200,
		mode: ModeReader,
	}
	portsMu.Unlock()

	_, _, err := ResolveSession(map[string]interface{}{"port": "/dev/test"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "has stopped")

	portsMu.Lock()
	_, exists := ports["/dev/test"]
	portsMu.Unlock()
	assert.False(t, exists)
}

func TestResolveSessionRetainsWithBuffer(t *testing.T) {
	setupTestPorts(t)

	mgr := serial.NewManager()
	mgr.AddToBuffer("buffered line")
	mgr.SetTestState(false, "/dev/test", 115200, nil)

	portsMu.Lock()
	ports["/dev/test"] = &PortSession{
		mgr:  mgr,
		port: "/dev/test",
		baud: 115200,
		mode: ModeReader,
	}
	portsMu.Unlock()

	m, port, err := ResolveSession(map[string]interface{}{"port": "/dev/test"})
	require.NoError(t, err)
	assert.Equal(t, "/dev/test", port)
	assert.Equal(t, mgr, m)

	portsMu.Lock()
	_, exists := ports["/dev/test"]
	portsMu.Unlock()
	assert.True(t, exists)
}

func TestResolveSessionTriggersDeferredRestart(t *testing.T) {
	setupTestPorts(t)
	setupFastWaitForPort(t)

	// Create a temp file for the port
	tmpfile, err := os.CreateTemp(t.TempDir(), "test-port-*")
	require.NoError(t, err)
	defer os.Remove(tmpfile.Name())
	tmpfile.Close()

	mgr := serial.NewManager()
	mgr.OpenFunc = func(portName string, mode *goSerial.Mode) (goSerial.Port, error) {
		return &noopPort{}, nil
	}
	mgr.SetTestState(true, tmpfile.Name(), 115200, nil)

	mock := &mockFlasher{chipNameVal: "ESP32"}

	portsMu.Lock()
	ports[tmpfile.Name()] = &PortSession{
		mgr:     mgr,
		port:    tmpfile.Name(),
		baud:    115200,
		mode:    ModePending,
		flasher: mock,
	}
	portsMu.Unlock()

	m, port, err := ResolveSession(map[string]interface{}{"port": tmpfile.Name()})
	require.NoError(t, err)
	assert.Equal(t, tmpfile.Name(), port)
	assert.Equal(t, mgr, m)

	portsMu.Lock()
	sess := ports[tmpfile.Name()]
	portsMu.Unlock()
	assert.Equal(t, ModeReader, sess.mode)
	assert.Nil(t, sess.flasher)
}

// AcquireForFlasher tests

func TestAcquireForFlasherNewSession(t *testing.T) {
	setupTestPorts(t)
	setupTestManagersFunc(t)
	setupTestFlasherFactory(t)

	newManagerFunc = func(bufSize int) *serial.Manager {
		mgr := serial.NewManager()
		mgr.OpenFunc = func(portName string, mode *goSerial.Mode) (goSerial.Port, error) {
			return &noopPort{}, nil
		}
		return mgr
	}

	newFlasherFactory = func(port string, opts *espflasher.FlasherOptions) (esp.Flasher, error) {
		return &mockFlasher{chipNameVal: "ESP32"}, nil
	}

	sess, factory := AcquireForFlasher("/dev/test", nil)
	require.NotNil(t, sess)
	assert.Equal(t, "/dev/test", sess.port)
	assert.Equal(t, ModeFlasher, sess.mode)

	portsMu.Lock()
	_, exists := ports["/dev/test"]
	portsMu.Unlock()
	assert.True(t, exists)

	// Factory should work
	f, err := factory("/dev/test", &espflasher.FlasherOptions{})
	require.NoError(t, err)
	assert.NotNil(t, f)
}

func TestAcquireForFlasherStopsReader(t *testing.T) {
	setupTestPorts(t)
	setupTestFlasherFactory(t)

	mgr := serial.NewManager()
	mgr.OpenFunc = func(portName string, mode *goSerial.Mode) (goSerial.Port, error) {
		return &noopPort{}, nil
	}
	mgr.SetTestState(true, "/dev/test", 115200, nil)

	portsMu.Lock()
	ports["/dev/test"] = &PortSession{
		mgr:  mgr,
		port: "/dev/test",
		baud: 115200,
		mode: ModeReader,
	}
	portsMu.Unlock()

	sess, _ := AcquireForFlasher("/dev/test", nil)
	assert.Equal(t, ModeFlasher, sess.mode)
}

func TestAcquireForFlasherReusesCachedFlasher(t *testing.T) {
	setupTestPorts(t)
	setupTestFlasherFactory(t)

	mgr := serial.NewManager()
	mgr.OpenFunc = func(portName string, mode *goSerial.Mode) (goSerial.Port, error) {
		return &noopPort{}, nil
	}

	mock := &mockFlasher{chipNameVal: "ESP32"}

	portsMu.Lock()
	ports["/dev/test"] = &PortSession{
		mgr:     mgr,
		port:    "/dev/test",
		baud:    115200,
		mode:    ModePending,
		flasher: mock,
	}
	portsMu.Unlock()

	sess, factory := AcquireForFlasher("/dev/test", nil)
	require.NotNil(t, factory)
	require.NotNil(t, sess)

	// Factory should return borrowed flasher
	f, err := factory("/dev/test", &espflasher.FlasherOptions{})
	require.NoError(t, err)

	borrowed, ok := f.(*BorrowedFlasher)
	require.True(t, ok)
	assert.Equal(t, mock, borrowed.Flasher)
	assert.True(t, mock.flashIDCalled, "factory should probe the cached flasher's liveness before reuse (BR-57)")
	assert.False(t, mock.closeCalled, "a live cached flasher must not be closed")
}

// TestAcquireForFlasherDiscardsDeadCachedFlasher verifies BR-57: a cached
// flasher whose liveness probe (FlashID) fails is treated as stale — e.g. the
// board reset/re-enumerated since it was cached — and is closed and
// discarded rather than reused, falling through to a fresh flasher create.
func TestAcquireForFlasherDiscardsDeadCachedFlasher(t *testing.T) {
	setupTestPorts(t)
	setupTestFlasherFactory(t)
	setupTestManagersFunc(t)
	setupTestIsUSBPort(t)

	newManagerFunc = func(bufSize int) *serial.Manager {
		mgr := serial.NewManager()
		mgr.OpenFunc = func(portName string, mode *goSerial.Mode) (goSerial.Port, error) {
			return &noopPort{}, nil
		}
		return mgr
	}

	deadCached := &mockFlasher{chipNameVal: "ESP32-dead", flashIDErr: fmt.Errorf("sync timeout")}
	freshMock := &mockFlasher{chipNameVal: "ESP32-fresh"}

	newFlasherFactory = func(portArg string, opts *espflasher.FlasherOptions) (esp.Flasher, error) {
		return freshMock, nil
	}

	portsMu.Lock()
	ports["/dev/test"] = &PortSession{
		mgr:     newManagerFunc(1000),
		port:    "/dev/test",
		baud:    115200,
		mode:    ModePending,
		flasher: deadCached,
	}
	portsMu.Unlock()

	sess, factory := AcquireForFlasher("/dev/test", nil)
	require.NotNil(t, sess)

	f, err := factory("/dev/test", &espflasher.FlasherOptions{})
	require.NoError(t, err)

	borrowed, ok := f.(*BorrowedFlasher)
	require.True(t, ok, "factory should return BorrowedFlasher")
	assert.Equal(t, freshMock, borrowed.Flasher, "should have discarded the dead cached flasher and created a fresh one")
	assert.True(t, deadCached.flashIDCalled, "liveness probe should have been attempted on the cached flasher")
	assert.True(t, deadCached.closeCalled, "dead cached flasher should be closed")

	portsMu.Lock()
	assert.Nil(t, sess.flasher, "dead cached flasher reference should be cleared")
	portsMu.Unlock()
}

func TestAcquireForFlasherUsesRealFactory(t *testing.T) {
	setupTestPorts(t)
	setupTestManagersFunc(t)
	setupTestFlasherFactory(t)

	newManagerFunc = func(bufSize int) *serial.Manager {
		mgr := serial.NewManager()
		mgr.OpenFunc = func(portName string, mode *goSerial.Mode) (goSerial.Port, error) {
			return &noopPort{}, nil
		}
		return mgr
	}

	realMock := &mockFlasher{chipNameVal: "ESP32"}
	newFlasherFactory = func(port string, opts *espflasher.FlasherOptions) (esp.Flasher, error) {
		return realMock, nil
	}

	sess, factory := AcquireForFlasher("/dev/test", nil)
	require.NotNil(t, sess)
	require.NotNil(t, factory)

	f, err := factory("/dev/test", &espflasher.FlasherOptions{})
	require.NoError(t, err)
	borrowed, ok := f.(*BorrowedFlasher)
	require.True(t, ok, "factory should return BorrowedFlasher")
	assert.Equal(t, realMock, borrowed.Flasher)
}

// TestAcquireForFlasherWiresConnectStatusOnRealConstruction drives a non-nil
// connectStatus through the real flasher-construction path and asserts it
// lands on the FlasherOptions handed to the factory before New is called.
func TestAcquireForFlasherWiresConnectStatusOnRealConstruction(t *testing.T) {
	setupTestPorts(t)
	setupTestManagersFunc(t)
	setupTestFlasherFactory(t)

	newManagerFunc = func(bufSize int) *serial.Manager {
		mgr := serial.NewManager()
		mgr.OpenFunc = func(portName string, mode *goSerial.Mode) (goSerial.Port, error) {
			return &noopPort{}, nil
		}
		return mgr
	}

	realMock := &mockFlasher{chipNameVal: "ESP32"}
	var gotOpts *espflasher.FlasherOptions
	newFlasherFactory = func(port string, opts *espflasher.FlasherOptions) (esp.Flasher, error) {
		gotOpts = opts
		return realMock, nil
	}

	var spyCalls []string
	spy := espflasher.ConnectStatusFunc(func(phase espflasher.ConnectPhase, attempt, maxAttempts int, message string) {
		spyCalls = append(spyCalls, string(phase))
	})

	sess, factory := AcquireForFlasher("/dev/test", spy)
	require.NotNil(t, sess)
	require.NotNil(t, factory)

	f, err := factory("/dev/test", &espflasher.FlasherOptions{})
	require.NoError(t, err)
	_, ok := f.(*BorrowedFlasher)
	require.True(t, ok, "factory should return BorrowedFlasher")

	require.NotNil(t, gotOpts)
	require.NotNil(t, gotOpts.ConnectStatus, "ConnectStatus must be wired onto FlasherOptions before real construction")

	gotOpts.ConnectStatus(espflasher.ConnectPhaseReset, 1, 7, "entering download mode")
	assert.Equal(t, []string{"reset"}, spyCalls, "connectStatus wired onto opts should be the caller-supplied spy")
}

// ReleaseFlasherImmediate tests

func TestReleaseFlasherImmediateClosesFlasher(t *testing.T) {
	setupTestPorts(t)
	setupFastWaitForPort(t)

	// Create a temp file for the port
	tmpfile, err := os.CreateTemp(t.TempDir(), "test-port-*")
	require.NoError(t, err)
	defer os.Remove(tmpfile.Name())
	tmpfile.Close()

	mgr := serial.NewManager()
	mgr.OpenFunc = func(portName string, mode *goSerial.Mode) (goSerial.Port, error) {
		return &noopPort{}, nil
	}

	mock := &mockFlasher{chipNameVal: "ESP32"}

	portsMu.Lock()
	ports[tmpfile.Name()] = &PortSession{
		mgr:     mgr,
		port:    tmpfile.Name(),
		baud:    115200,
		mode:    ModeFlasher,
		flasher: mock,
	}
	portsMu.Unlock()

	sess := ports[tmpfile.Name()]
	newPort := ReleaseFlasherImmediate(sess, tmpfile.Name())

	assert.Equal(t, "", newPort)
	portsMu.Lock()
	assert.Nil(t, sess.flasher)
	assert.Equal(t, ModeReader, sess.mode)
	portsMu.Unlock()
}

// TestReleaseFlasherImmediateForceResetsHeldSession verifies that an
// immediate release always resets the cached flasher and clears the
// no-reset hold, even when a prior gpio op armed noResetOnExpire via
// ReleaseFlasherDeferredNoReset — a mutating op (flash/erase/reset) must
// explicitly return the chip to run mode and must not let the flag leak
// into a later deferred release on a reused session.
func TestReleaseFlasherImmediateForceResetsHeldSession(t *testing.T) {
	setupTestPorts(t)
	setupFastWaitForPort(t)

	tmpfile, err := os.CreateTemp(t.TempDir(), "test-port-*")
	require.NoError(t, err)
	defer os.Remove(tmpfile.Name())
	tmpfile.Close()

	mgr := serial.NewManager()
	mgr.OpenFunc = func(portName string, mode *goSerial.Mode) (goSerial.Port, error) {
		return &noopPort{}, nil
	}

	mock := &mockFlasher{chipNameVal: "ESP32"}

	portsMu.Lock()
	ports[tmpfile.Name()] = &PortSession{
		mgr:             mgr,
		port:            tmpfile.Name(),
		baud:            115200,
		mode:            ModeFlasher,
		flasher:         mock,
		noResetOnExpire: true,
	}
	portsMu.Unlock()

	sess := ports[tmpfile.Name()]
	newPort := ReleaseFlasherImmediate(sess, tmpfile.Name())

	assert.Equal(t, "", newPort)
	assert.True(t, mock.resetCalled, "immediate release must force Reset() even with the no-reset hold armed")
	assert.True(t, mock.closeCalled, "immediate release must Close() the cached flasher")
	portsMu.Lock()
	assert.Nil(t, sess.flasher)
	assert.Equal(t, ModeReader, sess.mode)
	assert.False(t, sess.noResetOnExpire, "hold must be cleared so it can't leak into a reused session")
	portsMu.Unlock()
}

func TestReleaseFlasherImmediateRestartsReader(t *testing.T) {
	setupTestPorts(t)
	setupFastWaitForPort(t)

	// Create a temp file to represent the port
	tmpfile, err := os.CreateTemp(t.TempDir(), "test-port-*")
	require.NoError(t, err)
	defer os.Remove(tmpfile.Name())
	tmpfile.Close()

	mgr := serial.NewManager()
	mgr.OpenFunc = func(portName string, mode *goSerial.Mode) (goSerial.Port, error) {
		return &noopPort{}, nil
	}

	portsMu.Lock()
	ports[tmpfile.Name()] = &PortSession{
		mgr:  mgr,
		port: tmpfile.Name(),
		baud: 115200,
		mode: ModeFlasher,
	}
	portsMu.Unlock()

	sess := ports[tmpfile.Name()]
	newPort := ReleaseFlasherImmediate(sess, tmpfile.Name())

	assert.Equal(t, "", newPort)
	assert.Equal(t, ModeReader, sess.mode)
}

// TestReleaseFlasherImmediateWaitForPortTimeout covers ReleaseFlasherImmediate's
// early-return branch when the port never reappears (WaitForPort times out):
// the session must stay unmodified (not flipped to ModeReader) and the
// caller gets "" back exactly like a successful-but-unchanged release would,
// but without ever starting the manager.
func TestReleaseFlasherImmediateWaitForPortTimeout(t *testing.T) {
	setupTestPorts(t)
	setupFastWaitForPort(t)

	mgr := serial.NewManager()
	mgr.OpenFunc = func(portName string, mode *goSerial.Mode) (goSerial.Port, error) {
		return &noopPort{}, nil
	}

	missingPort := "/dev/pogopin-never-reappears"
	portsMu.Lock()
	sess := &PortSession{
		mgr:  mgr,
		port: missingPort,
		baud: 115200,
		mode: ModeFlasher,
	}
	ports[missingPort] = sess
	portsMu.Unlock()

	origList := SetListPortsFn(func() ([]serial.PortInfo, error) { return nil, nil })
	t.Cleanup(func() { SetListPortsFn(origList) })

	newPort := ReleaseFlasherImmediate(sess, missingPort)

	assert.Equal(t, "", newPort)
	assert.Equal(t, ModeFlasher, sess.mode, "mode must not flip to ModeReader when the port never reappears")
	assert.False(t, mgr.IsRunning())
}

// ReleaseFlasherDeferred tests

func TestReleaseFlasherDeferredStartsTimer(t *testing.T) {
	setupTestPorts(t)

	mgr := serial.NewManager()
	mgr.OpenFunc = func(portName string, mode *goSerial.Mode) (goSerial.Port, error) {
		return &noopPort{}, nil
	}

	// Create a temp file for the port
	tmpfile, err := os.CreateTemp(t.TempDir(), "test-port-*")
	require.NoError(t, err)
	defer os.Remove(tmpfile.Name())
	tmpfile.Close()

	portsMu.Lock()
	sess := &PortSession{
		mgr:  mgr,
		port: tmpfile.Name(),
		baud: 115200,
		mode: ModeFlasher,
	}
	ports[tmpfile.Name()] = sess
	portsMu.Unlock()

	// Deliberately does NOT use setupFastDeferred: this test only checks
	// state immediately after the call returns, and the real (multi-second)
	// default deferredRestartTimeout guarantees the timer cannot possibly
	// fire before that check runs, however loaded the scheduler is. A fast
	// (ms-scale) timeout can race this exact assertion under heavy parallel
	// test load — that was BR-63's second flake, caught by `-race
	// -count=20`: TestReleaseFlasherDeferredStartsTimer's own goroutine got
	// descheduled long enough for the 10ms timer to fire and complete
	// before the "still Pending" check ran, observing ModeReader instead.
	// The eventual-fire-and-restart behavior (which does need a fast
	// timeout to stay quick) is covered separately by
	// TestModeTransitionReaderToFlasherToPendingToReader via the race-free
	// WaitForExpireSessions join. setupTestPorts' cleanup stops this real
	// timer for us.
	ReleaseFlasherDeferred(sess, tmpfile.Name())

	portsMu.Lock()
	assert.Equal(t, ModePending, sess.mode)
	assert.NotNil(t, sess.timer)
	portsMu.Unlock()
}

// TestWaitForExpireSessionsJoinsDeletePath verifies the MEDIUM finding fix:
// WaitForExpireSessions must join an expireSession goroutine that deletes
// its own session from ports (mgr.Start failure, so expireSession's
// "foundPort found but restart failed" -> "Could not restart, delete
// session" branch runs) before closing its done channel -- not just
// goroutines whose session is still present in ports at join time (BR-63
// delete-path fix: the join now tracks outstanding callbacks via the
// expireTimers registry, independent of ports map membership).
//
// Combined with setupStatusFile so the callback's status.Write reads the
// package-global status.statusDir this test's t.Cleanup(status.SetStatusDir)
// later mutates: under `go test -race`, if WaitForExpireSessions returned
// before the callback's status.Write actually finished (the old
// ports-map-scan bug, which finds nothing to wait on once the session is
// already deleted), that in-flight read would race the cleanup's write and
// the race detector would catch it. A clean `-race` run here is the proof
// the join is real, not just that the delete-from-ports side effect landed.
func TestWaitForExpireSessionsJoinsDeletePath(t *testing.T) {
	setupTestPorts(t)
	setupFastDeferred(t)
	setupFastWaitForPort(t)
	setupStatusFile(t)

	// Port exists on disk so WaitForPort's os.Stat check finds it on its
	// very first poll instead of blocking on expireSession's hardcoded 3s
	// deadline -- keeps this test fast while still reaching the delete
	// branch below (mgr.Start fails once the port is "found").
	tmpfile, err := os.CreateTemp(t.TempDir(), "test-port-*")
	require.NoError(t, err)
	defer os.Remove(tmpfile.Name())
	tmpfile.Close()

	mgr := serial.NewManager()
	mgr.OpenFunc = func(portName string, mode *goSerial.Mode) (goSerial.Port, error) {
		return nil, fmt.Errorf("simulated open failure")
	}

	portsMu.Lock()
	sess := &PortSession{
		mgr:  mgr,
		port: tmpfile.Name(),
		baud: 115200,
		mode: ModeFlasher,
	}
	ports[tmpfile.Name()] = sess
	portsMu.Unlock()

	ReleaseFlasherDeferred(sess, tmpfile.Name())

	// Deterministically join the deferred callback. Must actually block
	// until expireSession's delete-from-ports branch (and its status.Write)
	// has finished, even though the session it deletes is no longer in
	// ports by the time the callback's defer closes the channel.
	WaitForExpireSessions()

	portsMu.Lock()
	_, stillPresent := ports[tmpfile.Name()]
	portsMu.Unlock()
	assert.False(t, stillPresent, "expireSession's delete-from-ports path should have removed the session")
}

// AcquireForExternal / ReleaseExternal tests

func TestAcquireForExternalStopsReader(t *testing.T) {
	setupTestPorts(t)

	mgr := serial.NewManager()
	mgr.OpenFunc = func(portName string, mode *goSerial.Mode) (goSerial.Port, error) {
		return &noopPort{}, nil
	}
	mgr.SetTestState(true, "/dev/test", 115200, nil)

	portsMu.Lock()
	ports["/dev/test"] = &PortSession{
		mgr:  mgr,
		port: "/dev/test",
		baud: 115200,
		mode: ModeReader,
	}
	portsMu.Unlock()

	sess := AcquireForExternal("/dev/test")
	assert.Equal(t, ModeExternal, sess.mode)
}

func TestAcquireForExternalCreatesNew(t *testing.T) {
	setupTestPorts(t)
	setupTestManagersFunc(t)

	newManagerFunc = func(bufSize int) *serial.Manager {
		mgr := serial.NewManager()
		mgr.OpenFunc = func(portName string, mode *goSerial.Mode) (goSerial.Port, error) {
			return &noopPort{}, nil
		}
		return mgr
	}

	sess := AcquireForExternal("/dev/test")
	assert.Equal(t, "/dev/test", sess.port)
	assert.Equal(t, ModeExternal, sess.mode)
}

// TestAcquireForExternalSetsPortsAtAcquire verifies the HIGH finding fix:
// AcquireForExternal must populate portsAtAcquire (like AcquireForFlasher
// does) so ReleaseExternal's re-enumeration match excludes ports that
// already existed at acquire time (BR-58 protection for the flash_external
// path).
func TestAcquireForExternalSetsPortsAtAcquire(t *testing.T) {
	setupTestPorts(t)
	setupTestManagersFunc(t)

	newManagerFunc = func(bufSize int) *serial.Manager {
		mgr := serial.NewManager()
		mgr.OpenFunc = func(portName string, mode *goSerial.Mode) (goSerial.Port, error) {
			return &noopPort{}, nil
		}
		return mgr
	}

	unrelatedPort := t.TempDir() + "/cu.usbmodem2001"
	origListPorts := listPortsFn
	t.Cleanup(func() { listPortsFn = origListPorts })
	listPortsFn = func() ([]serial.PortInfo, error) {
		return []serial.PortInfo{{Name: unrelatedPort}}, nil
	}

	sess := AcquireForExternal("/dev/test")

	portsMu.Lock()
	knownPorts := sess.portsAtAcquire
	portsMu.Unlock()

	require.NotNil(t, knownPorts, "AcquireForExternal must snapshot portsAtAcquire")
	assert.True(t, knownPorts[unrelatedPort], "pre-existing port must be recorded in the snapshot")
}

// TestReleaseExternalIgnoresPreExistingUnrelatedPort verifies BR-58 for the
// flash_external path (the HIGH finding): when the acquired port vanishes
// and only an unrelated board's pre-existing port remains, ReleaseExternal's
// WaitForPort must not hijack it.
func TestReleaseExternalIgnoresPreExistingUnrelatedPort(t *testing.T) {
	setupTestPorts(t)
	setupTestManagersFunc(t)
	setupFastWaitForPort(t)

	// Use paths under a fresh temp dir (guaranteed not to exist on disk) so
	// WaitForPort's os.Stat check can't short-circuit by hitting a real
	// device node that happens to share this name on the test machine.
	base := t.TempDir()
	originalPort := base + "/cu.usbmodem1101"
	unrelatedBoardPort := base + "/cu.usbmodem1102"

	newManagerFunc = func(bufSize int) *serial.Manager {
		mgr := serial.NewManager()
		mgr.OpenFunc = func(portName string, mode *goSerial.Mode) (goSerial.Port, error) {
			return &noopPort{}, nil
		}
		return mgr
	}

	// At acquire time, the unrelated board's port already exists.
	origListPorts := listPortsFn
	t.Cleanup(func() { listPortsFn = origListPorts })
	listPortsFn = func() ([]serial.PortInfo, error) {
		return []serial.PortInfo{
			{Name: originalPort},
			{Name: unrelatedBoardPort},
		}, nil
	}

	sess := AcquireForExternal(originalPort)

	// The original port vanishes; only the pre-existing unrelated port remains.
	listPortsFn = func() ([]serial.PortInfo, error) {
		return []serial.PortInfo{{Name: unrelatedBoardPort}}, nil
	}

	newPort := ReleaseExternal(sess, originalPort)

	assert.Equal(t, "", newPort, "must not hijack the unrelated board's pre-existing port")
	assert.Equal(t, ModeExternal, sess.mode, "session should remain unreleased rather than adopt the wrong port")
}

func TestReleaseExternalRestartsReader(t *testing.T) {
	setupTestPorts(t)
	setupFastWaitForPort(t)

	// Create a temp file for the port
	tmpfile, err := os.CreateTemp(t.TempDir(), "test-port-*")
	require.NoError(t, err)
	defer os.Remove(tmpfile.Name())
	tmpfile.Close()

	mgr := serial.NewManager()
	mgr.OpenFunc = func(portName string, mode *goSerial.Mode) (goSerial.Port, error) {
		return &noopPort{}, nil
	}

	portsMu.Lock()
	sess := &PortSession{
		mgr:  mgr,
		port: tmpfile.Name(),
		baud: 115200,
		mode: ModeExternal,
	}
	ports[tmpfile.Name()] = sess
	portsMu.Unlock()

	newPort := ReleaseExternal(sess, tmpfile.Name())

	assert.Equal(t, "", newPort)
	assert.Equal(t, ModeReader, sess.mode)
}

// TestReleaseExternalPortChanged covers ReleaseExternal's re-enumeration
// branch (foundPort != oldPort): the port map must move the session under
// its new name and the returned newPort must be non-empty, mirroring
// ReleaseFlasherImmediate's equivalent branch.
func TestReleaseExternalPortChanged(t *testing.T) {
	setupTestPorts(t)
	setupFastWaitForPort(t)

	mgr := serial.NewManager()
	mgr.OpenFunc = func(portName string, mode *goSerial.Mode) (goSerial.Port, error) {
		return &noopPort{}, nil
	}

	origPort := "/dev/ttyUSB0"
	portsMu.Lock()
	sess := &PortSession{
		mgr:  mgr,
		port: origPort,
		baud: 115200,
		mode: ModeExternal,
	}
	ports[origPort] = sess
	portsMu.Unlock()

	callCount := 0
	origList := SetListPortsFn(func() ([]serial.PortInfo, error) {
		callCount++
		if callCount == 1 {
			return nil, nil
		}
		return []serial.PortInfo{{Name: "/dev/ttyUSB77"}}, nil
	})
	t.Cleanup(func() { SetListPortsFn(origList) })

	newPort := ReleaseExternal(sess, origPort)

	assert.Equal(t, "/dev/ttyUSB77", newPort)
	assert.Equal(t, ModeReader, sess.mode)
	assert.Equal(t, "/dev/ttyUSB77", sess.port)

	portsMu.Lock()
	_, stillHasOld := ports[origPort]
	_, hasNew := ports["/dev/ttyUSB77"]
	portsMu.Unlock()
	assert.False(t, stillHasOld)
	assert.True(t, hasNew)
}

// Mode transition tests

func TestModeTransitionReaderToFlasherToPendingToReader(t *testing.T) {
	setupTestPorts(t)
	setupFastDeferred(t)
	setupFastWaitForPort(t)
	setupTestFlasherFactory(t)

	// Create a temp file for the port
	tmpfile, err := os.CreateTemp(t.TempDir(), "test-port-*")
	require.NoError(t, err)
	defer os.Remove(tmpfile.Name())
	tmpfile.Close()

	mgr := serial.NewManager()
	mgr.OpenFunc = func(portName string, mode *goSerial.Mode) (goSerial.Port, error) {
		return &noopPort{}, nil
	}
	mgr.SetTestState(true, tmpfile.Name(), 115200, nil)

	// Start in ModeReader
	portsMu.Lock()
	ports[tmpfile.Name()] = &PortSession{
		mgr:  mgr,
		port: tmpfile.Name(),
		baud: 115200,
		mode: ModeReader,
	}
	portsMu.Unlock()

	// Transition to ModeFlasher
	sess, _ := AcquireForFlasher(tmpfile.Name(), nil)
	assert.Equal(t, ModeFlasher, sess.mode)

	// Cache a flasher
	mock := &mockFlasher{chipNameVal: "ESP32"}
	portsMu.Lock()
	sess.flasher = mock
	portsMu.Unlock()

	// Transition to ModePending. Deliberately doesn't assert ModePending
	// immediately after this call: with the fast (setupFastDeferred)
	// timeout, that would race the real background timer under heavy
	// parallel test load (BR-63) — see
	// TestReleaseFlasherDeferredStartsTimer, which covers the immediate
	// post-call Pending state without that race using the real (slow)
	// default timeout instead. This test's job is the full
	// Reader->Flasher->Pending->Reader round trip, verified deterministically
	// below via WaitForExpireSessions.
	ReleaseFlasherDeferred(sess, tmpfile.Name())

	// Deterministically wait for the deferred restart's callback to finish.
	WaitForExpireSessions()

	// Should be back to ModeReader
	portsMu.Lock()
	assert.Equal(t, ModeReader, sess.mode)
	portsMu.Unlock()
}

// retryFlasherCreate tests

func TestFactoryRetriesOnSyncErrorUSB(t *testing.T) {
	setupTestPorts(t)
	setupTestManagersFunc(t)
	setupTestFlasherFactory(t)
	setupTestIsUSBPort(t)
	setupFastSyncRetry(t)

	port := "/dev/cu.usbmodem1101"
	realMock := &mockFlasher{chipNameVal: "ESP32"}
	callCount := 0

	newManagerFunc = func(bufSize int) *serial.Manager {
		mgr := serial.NewManager()
		mgr.OpenFunc = func(portName string, mode *goSerial.Mode) (goSerial.Port, error) {
			return &noopPort{}, nil
		}
		return mgr
	}

	// First 3 calls fail with SyncError, 4th succeeds (initial + 3 retries)
	newFlasherFactory = func(portArg string, opts *espflasher.FlasherOptions) (esp.Flasher, error) {
		callCount++
		if callCount <= 3 {
			return nil, &espflasher.SyncError{Attempts: 7}
		}
		return realMock, nil
	}

	sess, factory := AcquireForFlasher(port, nil)
	require.NotNil(t, sess)

	f, err := factory(port, &espflasher.FlasherOptions{})
	require.NoError(t, err)
	borrowed, ok := f.(*BorrowedFlasher)
	require.True(t, ok, "factory should return BorrowedFlasher")
	assert.Equal(t, realMock, borrowed.Flasher)
	assert.Equal(t, 4, callCount, "factory should be called 4 times (initial + 3 retries)")
}

func TestFactoryTriesFindSimilarPortOnSyncError(t *testing.T) {
	setupTestPorts(t)
	setupTestManagersFunc(t)
	setupTestFlasherFactory(t)
	setupTestIsUSBPort(t)
	setupFastSyncRetry(t)

	originalPort := "/dev/cu.usbmodem1101"
	newPort := "/dev/cu.usbmodem1102"
	realMock := &mockFlasher{chipNameVal: "ESP32"}

	newManagerFunc = func(bufSize int) *serial.Manager {
		mgr := serial.NewManager()
		mgr.OpenFunc = func(portName string, mode *goSerial.Mode) (goSerial.Port, error) {
			return &noopPort{}, nil
		}
		return mgr
	}

	// Always fail for original port, succeed for new port
	newFlasherFactory = func(portArg string, opts *espflasher.FlasherOptions) (esp.Flasher, error) {
		if portArg == originalPort {
			return nil, &espflasher.SyncError{Attempts: 7}
		}
		return realMock, nil
	}

	// At acquire time only the original port is enumerated — this becomes the
	// portsAtAcquire snapshot (BR-58).
	origListPorts := listPortsFn
	t.Cleanup(func() { listPortsFn = origListPorts })

	listPortsFn = func() ([]serial.PortInfo, error) {
		return []serial.PortInfo{
			{Name: originalPort},
		}, nil
	}

	sess, factory := AcquireForFlasher(originalPort, nil)
	require.NotNil(t, sess)

	// The device re-enumerates: newPort newly appears, originalPort is gone.
	listPortsFn = func() ([]serial.PortInfo, error) {
		return []serial.PortInfo{
			{Name: newPort},
		}, nil
	}

	f, err := factory(originalPort, &espflasher.FlasherOptions{})
	require.NoError(t, err)
	borrowed, ok := f.(*BorrowedFlasher)
	require.True(t, ok, "factory should return BorrowedFlasher")
	assert.Equal(t, realMock, borrowed.Flasher)

	// Verify that the session's port was updated
	portsMu.Lock()
	updatedSess, exists := ports[newPort]
	_, oldPortExists := ports[originalPort]
	portsMu.Unlock()

	assert.True(t, exists, "new port should be in ports map")
	assert.False(t, oldPortExists, "original port should be removed from ports map")
	assert.Equal(t, newPort, updatedSess.port)
}

// TestFactoryIgnoresPreExistingUnrelatedPortOnSyncError verifies BR-58: when
// the board's port vanishes and only an unrelated board's port (already
// present at acquire time, sharing the same USB-serial prefix) remains,
// retryFlasherCreate must not hijack it — it should give up rather than
// start monitoring the wrong device.
func TestFactoryIgnoresPreExistingUnrelatedPortOnSyncError(t *testing.T) {
	setupTestPorts(t)
	setupTestManagersFunc(t)
	setupTestFlasherFactory(t)
	setupTestIsUSBPort(t)
	setupFastSyncRetry(t)

	originalPort := "/dev/cu.usbmodem1101"
	unrelatedBoardPort := "/dev/cu.usbmodem1102"

	newManagerFunc = func(bufSize int) *serial.Manager {
		mgr := serial.NewManager()
		mgr.OpenFunc = func(portName string, mode *goSerial.Mode) (goSerial.Port, error) {
			return &noopPort{}, nil
		}
		return mgr
	}

	newFlasherFactory = func(portArg string, opts *espflasher.FlasherOptions) (esp.Flasher, error) {
		return nil, &espflasher.SyncError{Attempts: 7}
	}

	// At acquire time, the unrelated board's port already exists.
	origListPorts := listPortsFn
	t.Cleanup(func() { listPortsFn = origListPorts })
	listPortsFn = func() ([]serial.PortInfo, error) {
		return []serial.PortInfo{
			{Name: originalPort},
			{Name: unrelatedBoardPort},
		}, nil
	}

	sess, factory := AcquireForFlasher(originalPort, nil)
	require.NotNil(t, sess)

	// After the original port vanishes, only the pre-existing unrelated
	// board's port is left — it must not be treated as a re-enumeration.
	listPortsFn = func() ([]serial.PortInfo, error) {
		return []serial.PortInfo{
			{Name: unrelatedBoardPort},
		}, nil
	}

	_, err := factory(originalPort, &espflasher.FlasherOptions{})
	require.Error(t, err, "should give up rather than match the unrelated board's pre-existing port")

	portsMu.Lock()
	_, hijacked := ports[unrelatedBoardPort]
	_, originalStillPresent := ports[originalPort]
	portsMu.Unlock()

	assert.False(t, hijacked, "unrelated board's port must not be adopted into the session")
	assert.True(t, originalStillPresent, "original port mapping should be left untouched")
}

func TestFactoryNoRetryOnNonSyncError(t *testing.T) {
	setupTestPorts(t)
	setupTestManagersFunc(t)
	setupTestFlasherFactory(t)

	port := "/dev/cu.usbmodem1101"
	callCount := 0

	newManagerFunc = func(bufSize int) *serial.Manager {
		mgr := serial.NewManager()
		mgr.OpenFunc = func(portName string, mode *goSerial.Mode) (goSerial.Port, error) {
			return &noopPort{}, nil
		}
		return mgr
	}

	// Always fail with non-SyncError
	newFlasherFactory = func(portArg string, opts *espflasher.FlasherOptions) (esp.Flasher, error) {
		callCount++
		return nil, fmt.Errorf("permission denied")
	}

	sess, factory := AcquireForFlasher(port, nil)
	require.NotNil(t, sess)

	f, err := factory(port, &espflasher.FlasherOptions{})
	require.Error(t, err)
	assert.Nil(t, f)
	assert.Equal(t, 1, callCount, "factory should be called only once for non-SyncError")
	assert.Contains(t, err.Error(), "permission denied")
}

func TestFactoryOverridesResetModeForUSBCDC(t *testing.T) {
	setupTestPorts(t)
	setupTestManagersFunc(t)
	setupTestFlasherFactory(t)
	setupTestIsUSBPort(t)
	setupFastSyncRetry(t)

	port := "/dev/cu.usbmodem1101"
	var capturedOpts *espflasher.FlasherOptions

	newManagerFunc = func(bufSize int) *serial.Manager {
		mgr := serial.NewManager()
		mgr.OpenFunc = func(portName string, mode *goSerial.Mode) (goSerial.Port, error) {
			return &noopPort{}, nil
		}
		return mgr
	}

	newFlasherFactory = func(portArg string, opts *espflasher.FlasherOptions) (esp.Flasher, error) {
		capturedOpts = opts
		return &mockFlasher{chipNameVal: "ESP32-S3"}, nil
	}

	sess, factory := AcquireForFlasher(port, nil)
	require.NotNil(t, sess)

	// Pass ResetAuto (the default) — should be overridden to ResetUSBJTAG for USB CDC
	_, err := factory(port, &espflasher.FlasherOptions{ResetMode: espflasher.ResetAuto})
	require.NoError(t, err)
	assert.Equal(t, espflasher.ResetUSBJTAG, capturedOpts.ResetMode, "USB CDC port should use usb_jtag reset mode")
}

func TestFactoryKeepsExplicitResetModeForUSBCDC(t *testing.T) {
	setupTestPorts(t)
	setupTestManagersFunc(t)
	setupTestFlasherFactory(t)
	setupTestIsUSBPort(t)
	setupFastSyncRetry(t)

	port := "/dev/cu.usbmodem1101"
	var capturedOpts *espflasher.FlasherOptions

	newManagerFunc = func(bufSize int) *serial.Manager {
		mgr := serial.NewManager()
		mgr.OpenFunc = func(portName string, mode *goSerial.Mode) (goSerial.Port, error) {
			return &noopPort{}, nil
		}
		return mgr
	}

	newFlasherFactory = func(portArg string, opts *espflasher.FlasherOptions) (esp.Flasher, error) {
		capturedOpts = opts
		return &mockFlasher{chipNameVal: "ESP32-S3"}, nil
	}

	sess, factory := AcquireForFlasher(port, nil)
	require.NotNil(t, sess)

	// Pass explicit ResetNoReset — should NOT be overridden
	_, err := factory(port, &espflasher.FlasherOptions{ResetMode: espflasher.ResetNoReset})
	require.NoError(t, err)
	assert.Equal(t, espflasher.ResetNoReset, capturedOpts.ResetMode, "explicit reset mode should not be overridden")
}

// expireSession tests

func TestExpireSessionResetsFlasherBeforeClose(t *testing.T) {
	setupTestPorts(t)
	setupTestManagersFunc(t)
	setupFastWaitForPort(t)

	port := "/dev/cu.usbmodem1101"
	mock := &mockFlasher{chipNameVal: "ESP32"}

	// Create a temp file to simulate the port existing
	tmpFile, err := os.CreateTemp(t.TempDir(), "serial-test-port-*")
	require.NoError(t, err)
	tmpPort := tmpFile.Name()
	tmpFile.Close()

	newManagerFunc = func(bufSize int) *serial.Manager {
		mgr := serial.NewManager()
		mgr.OpenFunc = func(portName string, mode *goSerial.Mode) (goSerial.Port, error) {
			return &noopPort{}, nil
		}
		return mgr
	}

	// Set up session with cached flasher (simulates deferred state where no tool consumed it)
	portsMu.Lock()
	sess := &PortSession{
		mgr:     newManagerFunc(1000),
		port:    tmpPort,
		baud:    115200,
		mode:    ModePending,
		flasher: mock,
	}
	ports[port] = sess
	portsMu.Unlock()

	expireSession(sess, tmpPort)

	// Reset should be called — device is in bootloader/stub mode (BorrowedFlasher always caches)
	assert.True(t, mock.resetCalled, "Reset() should be called to return device to user code")
	// Close should have been called
	assert.True(t, mock.closeCalled, "Close() should be called to release flasher resources")
}

func TestExpireSessionWaitsForPort(t *testing.T) {
	setupTestPorts(t)
	setupTestManagersFunc(t)
	setupFastWaitForPort(t)

	port := "/dev/cu.usbmodem1101"

	newManagerFunc = func(bufSize int) *serial.Manager {
		mgr := serial.NewManager()
		mgr.OpenFunc = func(portName string, mode *goSerial.Mode) (goSerial.Port, error) {
			return &noopPort{}, nil
		}
		return mgr
	}

	// Create a temp file that appears after a short delay (simulates re-enumeration)
	tmpFile, err := os.CreateTemp(t.TempDir(), "serial-test-port-*")
	require.NoError(t, err)
	tmpPort := tmpFile.Name()
	tmpFile.Close()
	// Remove it first, then recreate after a delay
	os.Remove(tmpPort)

	go func() {
		time.Sleep(50 * time.Millisecond)
		f, _ := os.Create(tmpPort)
		f.Close()
	}()

	portsMu.Lock()
	sess := &PortSession{
		mgr:  newManagerFunc(1000),
		port: tmpPort,
		baud: 115200,
		mode: ModePending,
	}
	ports[port] = sess
	portsMu.Unlock()

	expireSession(sess, tmpPort)

	// Session should have been restarted (port appeared within 3s wait)
	portsMu.Lock()
	_, exists := ports[tmpPort]
	portsMu.Unlock()

	assert.True(t, exists || sess.mode == ModeReader, "session should restart after port reappears")
}

func TestOpenForFlasher(t *testing.T) {
	setupTestPorts(t)

	port := "/dev/cu.test"
	portCalled := false
	nameCalled := ""
	var modeCalled *goSerial.Mode

	prevSerialOpen := SetSerialOpenFn(func(name string, mode *goSerial.Mode) (goSerial.Port, error) {
		portCalled = true
		nameCalled = name
		modeCalled = mode
		return &noopPort{}, nil
	})
	t.Cleanup(func() { serialOpen = prevSerialOpen })

	// Case 1: port in ModeFlasher — opener delegates to serialOpen
	sess := NewPortSession(serial.NewManager(), port, 115200, ModeFlasher)
	InsertPort(port, sess)

	opener := OpenForFlasher(port)
	require.NotNil(t, opener)

	mode := &goSerial.Mode{BaudRate: 115200}
	p, err := opener(port, mode)

	assert.NoError(t, err)
	assert.NotNil(t, p)
	assert.True(t, portCalled, "serialOpen should have been called")
	assert.Equal(t, port, nameCalled)
	assert.Equal(t, mode, modeCalled)

	// Case 2: port not registered — opener returns error
	portCalled = false
	opener2 := OpenForFlasher("/dev/cu.nonexistent")
	p2, err2 := opener2("/dev/cu.nonexistent", mode)

	assert.Error(t, err2)
	assert.Nil(t, p2)
	assert.False(t, portCalled, "serialOpen should not be called for unregistered port")
	assert.Contains(t, err2.Error(), "not in ModeFlasher")

	// Case 3: port in ModeReader — opener returns error
	portCalled = false
	sessReader := NewPortSession(serial.NewManager(), "/dev/cu.reader", 115200, ModeReader)
	InsertPort("/dev/cu.reader", sessReader)

	opener3 := OpenForFlasher("/dev/cu.reader")
	p3, err3 := opener3("/dev/cu.reader", mode)

	assert.Error(t, err3)
	assert.Nil(t, p3)
	assert.False(t, portCalled, "serialOpen should not be called for ModeReader port")
	assert.Contains(t, err3.Error(), "not in ModeFlasher")
}

// Flasher-options fingerprint tests

// TestAcquireForFlasherFingerprintMismatchDiscardsCache verifies that a
// cached flasher built for a different requested baud is treated exactly
// like a failed liveness probe: closed and discarded, with a fresh flasher
// constructed for the new caller's opts, instead of being reused.
func TestAcquireForFlasherFingerprintMismatchDiscardsCache(t *testing.T) {
	setupTestPorts(t)
	setupTestFlasherFactory(t)

	mgr := serial.NewManager()
	mgr.OpenFunc = func(portName string, mode *goSerial.Mode) (goSerial.Port, error) {
		return &noopPort{}, nil
	}

	cachedMock := &mockFlasher{chipNameVal: "ESP32-cached"}
	freshMock := &mockFlasher{chipNameVal: "ESP32-fresh"}

	newFlasherFactory = func(portArg string, opts *espflasher.FlasherOptions) (esp.Flasher, error) {
		return freshMock, nil
	}

	portsMu.Lock()
	ports["/dev/test"] = &PortSession{
		mgr:       mgr,
		port:      "/dev/test",
		baud:      115200,
		mode:      ModePending,
		flasher:   cachedMock,
		flasherFP: flasherFingerprint{baud: 115200},
	}
	portsMu.Unlock()

	sess, factory := AcquireForFlasher("/dev/test", nil)
	require.NotNil(t, sess)

	// op2 requests a different baud than the cached flasher was built with.
	f, err := factory("/dev/test", &espflasher.FlasherOptions{BaudRate: 230400})
	require.NoError(t, err)

	borrowed, ok := f.(*BorrowedFlasher)
	require.True(t, ok, "factory should return BorrowedFlasher")
	assert.Equal(t, freshMock, borrowed.Flasher, "fingerprint mismatch should discard the cached flasher and construct a fresh one")
	assert.True(t, cachedMock.closeCalled, "mismatched cached flasher must be closed")
	assert.False(t, cachedMock.flashIDCalled, "liveness probe should be skipped once a fingerprint mismatch is known")

	// onReturn (fired by Close, mirroring the real caller's flow) records the
	// new opts' fingerprint on the session.
	require.NoError(t, borrowed.Close())
	portsMu.Lock()
	assert.Equal(t, flasherFingerprint{baud: 230400}, sess.flasherFP, "fresh construction should record the new opts' fingerprint")
	portsMu.Unlock()
}

// TestAcquireForFlasherFingerprintMatchReusesCachedFlasher verifies that a
// cached flasher built with the same baud as the new request is reused via
// the normal BorrowedFlasher liveness-probe path, with no fresh flasher
// construction.
func TestAcquireForFlasherFingerprintMatchReusesCachedFlasher(t *testing.T) {
	setupTestPorts(t)
	setupTestFlasherFactory(t)

	mgr := serial.NewManager()
	mgr.OpenFunc = func(portName string, mode *goSerial.Mode) (goSerial.Port, error) {
		return &noopPort{}, nil
	}

	cachedMock := &mockFlasher{chipNameVal: "ESP32"}
	freshCalled := false
	newFlasherFactory = func(portArg string, opts *espflasher.FlasherOptions) (esp.Flasher, error) {
		freshCalled = true
		return &mockFlasher{}, nil
	}

	portsMu.Lock()
	ports["/dev/test"] = &PortSession{
		mgr:       mgr,
		port:      "/dev/test",
		baud:      115200,
		mode:      ModePending,
		flasher:   cachedMock,
		flasherFP: flasherFingerprint{baud: 115200},
	}
	portsMu.Unlock()

	sess, factory := AcquireForFlasher("/dev/test", nil)
	require.NotNil(t, sess)

	f, err := factory("/dev/test", &espflasher.FlasherOptions{BaudRate: 115200})
	require.NoError(t, err)

	borrowed, ok := f.(*BorrowedFlasher)
	require.True(t, ok, "factory should return BorrowedFlasher")
	assert.Equal(t, cachedMock, borrowed.Flasher, "matching fingerprint should reuse the cached flasher")
	assert.True(t, cachedMock.flashIDCalled, "liveness probe should still run when fingerprint matches")
	assert.False(t, cachedMock.closeCalled, "a reused flasher must not be closed")
	assert.False(t, freshCalled, "newFlasherFactory should not be called when the cache is reused")
}

// TestAcquireForFlasherSkipStubMismatchDiscardsCache verifies that a cached
// flasher connected with the stub loader (SkipStub=false) is treated exactly
// like a baud mismatch when a new caller requests SkipStub=true within the
// window: closed and discarded, with a fresh flasher constructed for the new
// caller's opts, instead of being reused. Reusing a stub-loaded handle for a
// SkipStub=true caller would resurrect the magic-0x9 reconnect failure.
func TestAcquireForFlasherSkipStubMismatchDiscardsCache(t *testing.T) {
	setupTestPorts(t)
	setupTestFlasherFactory(t)

	mgr := serial.NewManager()
	mgr.OpenFunc = func(portName string, mode *goSerial.Mode) (goSerial.Port, error) {
		return &noopPort{}, nil
	}

	cachedMock := &mockFlasher{chipNameVal: "ESP32-cached"}
	freshMock := &mockFlasher{chipNameVal: "ESP32-fresh"}

	newFlasherFactory = func(portArg string, opts *espflasher.FlasherOptions) (esp.Flasher, error) {
		return freshMock, nil
	}

	portsMu.Lock()
	ports["/dev/test"] = &PortSession{
		mgr:       mgr,
		port:      "/dev/test",
		baud:      115200,
		mode:      ModePending,
		flasher:   cachedMock,
		flasherFP: flasherFingerprint{baud: 115200, skipStub: false},
	}
	portsMu.Unlock()

	sess, factory := AcquireForFlasher("/dev/test", nil)
	require.NotNil(t, sess)

	// The new caller requests SkipStub=true, but the cached flasher was
	// stub-loaded (SkipStub=false).
	f, err := factory("/dev/test", &espflasher.FlasherOptions{BaudRate: 115200, SkipStub: true})
	require.NoError(t, err)

	borrowed, ok := f.(*BorrowedFlasher)
	require.True(t, ok, "factory should return BorrowedFlasher")
	assert.Equal(t, freshMock, borrowed.Flasher, "SkipStub mismatch should discard the cached flasher and construct a fresh one")
	assert.True(t, cachedMock.closeCalled, "mismatched cached flasher must be closed")
	assert.False(t, cachedMock.flashIDCalled, "liveness probe should be skipped once a fingerprint mismatch is known")

	require.NoError(t, borrowed.Close())
	portsMu.Lock()
	assert.Equal(t, flasherFingerprint{baud: 115200, skipStub: true}, sess.flasherFP, "fresh construction should record the new opts' fingerprint")
	portsMu.Unlock()
}

// No-reset-on-expire tests

// TestReleaseFlasherDeferredNoResetSkipsUnderlyingReset verifies that
// ReleaseFlasherDeferredNoReset causes expireSession to skip the underlying
// flasher.Reset() call (Close() still runs), and that the flag is cleared
// afterward so it can't leak into a later session lifecycle.
func TestReleaseFlasherDeferredNoResetSkipsUnderlyingReset(t *testing.T) {
	setupTestPorts(t)
	setupFastDeferred(t)
	setupFastWaitForPort(t)

	tmpfile, err := os.CreateTemp(t.TempDir(), "test-port-*")
	require.NoError(t, err)
	defer os.Remove(tmpfile.Name())
	tmpfile.Close()

	mgr := serial.NewManager()
	mgr.OpenFunc = func(portName string, mode *goSerial.Mode) (goSerial.Port, error) {
		return &noopPort{}, nil
	}

	mock := &mockFlasher{chipNameVal: "ESP32"}

	portsMu.Lock()
	sess := &PortSession{
		mgr:     mgr,
		port:    tmpfile.Name(),
		baud:    115200,
		mode:    ModeFlasher,
		flasher: mock,
	}
	ports[tmpfile.Name()] = sess
	portsMu.Unlock()

	ReleaseFlasherDeferredNoReset(sess, tmpfile.Name())

	portsMu.Lock()
	assert.Equal(t, ModePending, sess.mode)
	assert.True(t, sess.noResetOnExpire, "flag should be set immediately by ReleaseFlasherDeferredNoReset")
	portsMu.Unlock()

	// Wait for the (fast) deferred timer to fire and expireSession to run.
	// Poll under portsMu (rather than a bare time.Sleep) so the successful
	// Lock/Unlock pair establishes a happens-before edge with the timer
	// goroutine's closeCachedFlasher call — a plain sleep gives no such
	// synchronization and races under -race with mock.closeCalled/
	// resetCalled, which are plain fields mutated by that other goroutine.
	require.Eventually(t, func() bool {
		portsMu.Lock()
		defer portsMu.Unlock()
		return sess.flasher == nil
	}, time.Second, time.Millisecond, "expireSession should reap the cached flasher")

	assert.True(t, mock.closeCalled, "Close() should still be called on no-reset expiry")
	assert.False(t, mock.resetCalled, "Reset() should be skipped on no-reset expiry")

	portsMu.Lock()
	assert.False(t, sess.noResetOnExpire, "flag should be cleared after being consumed by expireSession")
	portsMu.Unlock()
}

// TestReleaseFlasherDeferredResetsUnderlyingFlasherNormally is the control
// case for TestReleaseFlasherDeferredNoResetSkipsUnderlyingReset: the normal
// ReleaseFlasherDeferred path must still call Reset() on expiry, proving the
// no-reset sibling didn't change existing behavior.
func TestReleaseFlasherDeferredResetsUnderlyingFlasherNormally(t *testing.T) {
	setupTestPorts(t)
	setupFastDeferred(t)
	setupFastWaitForPort(t)

	tmpfile, err := os.CreateTemp(t.TempDir(), "test-port-*")
	require.NoError(t, err)
	defer os.Remove(tmpfile.Name())
	tmpfile.Close()

	mgr := serial.NewManager()
	mgr.OpenFunc = func(portName string, mode *goSerial.Mode) (goSerial.Port, error) {
		return &noopPort{}, nil
	}

	mock := &mockFlasher{chipNameVal: "ESP32"}

	portsMu.Lock()
	sess := &PortSession{
		mgr:     mgr,
		port:    tmpfile.Name(),
		baud:    115200,
		mode:    ModeFlasher,
		flasher: mock,
	}
	ports[tmpfile.Name()] = sess
	portsMu.Unlock()

	ReleaseFlasherDeferred(sess, tmpfile.Name())

	// Poll under portsMu rather than a bare sleep — see the no-reset sibling
	// test above for why an unsynchronized sleep-then-read races under -race.
	require.Eventually(t, func() bool {
		portsMu.Lock()
		defer portsMu.Unlock()
		return sess.flasher == nil
	}, time.Second, time.Millisecond, "expireSession should reap the cached flasher")

	assert.True(t, mock.resetCalled, "Reset() should be called on normal deferred expiry (control case)")
	assert.True(t, mock.closeCalled, "Close() should be called on normal deferred expiry")
}

// TestExpireSessionHeldExpirySkipsMonitorRestart verifies the HW-validated
// design fix: on a no-reset expiry, expireSession must NOT restart the
// serial monitor/reader (Start is never called) — restarting it reopens the
// port, which on native-USB chips disturbs the ROM bootloader's
// download-mode state and breaks a subsequent no_reset GPIO reattach. The
// held session is instead torn down (removed from the ports map) so it
// looks, to a later AcquireForFlasher, exactly like a port with no existing
// session — the next acquire must build a fresh flasher connection rather
// than reuse anything.
func TestExpireSessionHeldExpirySkipsMonitorRestart(t *testing.T) {
	setupTestPorts(t)
	setupTestManagersFunc(t)
	setupFastWaitForPort(t)
	setupTestFlasherFactory(t)

	port := "/dev/cu.usbmodem1101"
	mock := &mockFlasher{chipNameVal: "ESP32"}

	startCalled := false
	newManagerFunc = func(bufSize int) *serial.Manager {
		mgr := serial.NewManager()
		mgr.OpenFunc = func(portName string, mode *goSerial.Mode) (goSerial.Port, error) {
			startCalled = true
			return &noopPort{}, nil
		}
		return mgr
	}

	freshMock := &mockFlasher{chipNameVal: "ESP32-fresh"}
	newFlasherFactory = func(portArg string, opts *espflasher.FlasherOptions) (esp.Flasher, error) {
		return freshMock, nil
	}

	portsMu.Lock()
	sess := &PortSession{
		mgr:             newManagerFunc(1000),
		port:            port,
		baud:            115200,
		mode:            ModePending,
		flasher:         mock,
		noResetOnExpire: true,
	}
	ports[port] = sess
	portsMu.Unlock()

	// Reset the OpenFunc call flag: newManagerFunc above was invoked once to
	// build sess.mgr, which does not itself call OpenFunc — only mgr.Start
	// would. Confirm no start happened yet before calling expireSession.
	require.False(t, startCalled, "constructing the manager must not open the port")

	expireSession(sess, port)

	assert.True(t, mock.closeCalled, "cached flasher must still be Close()d on a held expiry")
	assert.False(t, mock.resetCalled, "Reset() must be skipped on a held (no-reset) expiry")
	assert.False(t, startCalled, "the serial monitor/reader must NOT be restarted on a held expiry — reopening the port breaks no_reset reattach on native-USB chips")

	portsMu.Lock()
	_, exists := ports[port]
	portsMu.Unlock()
	assert.False(t, exists, "held expiry must remove the session from the ports map, leaving the port idle for a clean reattach")

	// A subsequent AcquireForFlasher for the same port must construct a
	// fresh flasher rather than reuse anything from the reaped session.
	newSess, factory := AcquireForFlasher(port, nil)
	require.NotNil(t, newSess)
	f, err := factory(port, &espflasher.FlasherOptions{})
	require.NoError(t, err)
	borrowed, ok := f.(*BorrowedFlasher)
	require.True(t, ok)
	assert.Equal(t, freshMock, borrowed.Flasher, "reattach after a held expiry must build a fresh flasher connection")
}

// TestExpireSessionNormalExpiryRestartsMonitor is the control case for
// TestExpireSessionHeldExpirySkipsMonitorRestart: a normal (non-hold)
// expiry must still Reset() the device and restart the serial monitor,
// preserving existing behavior.
func TestExpireSessionNormalExpiryRestartsMonitor(t *testing.T) {
	setupTestPorts(t)
	setupTestManagersFunc(t)
	setupFastWaitForPort(t)

	port := "/dev/cu.usbmodem1101"
	mock := &mockFlasher{chipNameVal: "ESP32"}

	tmpFile, err := os.CreateTemp(t.TempDir(), "serial-test-port-*")
	require.NoError(t, err)
	tmpPort := tmpFile.Name()
	tmpFile.Close()

	startCalled := false
	newManagerFunc = func(bufSize int) *serial.Manager {
		mgr := serial.NewManager()
		mgr.OpenFunc = func(portName string, mode *goSerial.Mode) (goSerial.Port, error) {
			startCalled = true
			return &noopPort{}, nil
		}
		return mgr
	}

	portsMu.Lock()
	sess := &PortSession{
		mgr:     newManagerFunc(1000),
		port:    tmpPort,
		baud:    115200,
		mode:    ModePending,
		flasher: mock,
	}
	ports[port] = sess
	portsMu.Unlock()

	expireSession(sess, tmpPort)

	assert.True(t, mock.resetCalled, "Reset() must run on a normal (non-held) expiry")
	assert.True(t, mock.closeCalled, "Close() must run on a normal expiry")
	assert.True(t, startCalled, "the serial monitor/reader must be restarted on a normal expiry")

	portsMu.Lock()
	assert.Equal(t, ModeReader, sess.mode, "session should be back in ModeReader after a normal expiry restarts the monitor")
	portsMu.Unlock()
}

// TestExpireSessionSkipsTeardownWhenReclaimedByConcurrentAcquire verifies the
// BR-64 fix: if a concurrent AcquireForFlasher wins portsMu first and flips
// mode away from ModePending (intending to reuse the cached flasher),
// expireSession must return immediately WITHOUT calling closeCachedFlasher —
// closing/nilling sess.flasher here would hand the racing acquirer a dead
// handle, losing the reuse optimization. This simulates the race directly:
// mode is already ModeFlasher (as AcquireForFlasher would have set it) by the
// time expireSession runs.
func TestExpireSessionSkipsTeardownWhenReclaimedByConcurrentAcquire(t *testing.T) {
	setupTestPorts(t)
	setupTestManagersFunc(t)
	setupFastWaitForPort(t)

	port := "/dev/cu.usbmodem1101"
	mock := &mockFlasher{chipNameVal: "ESP32"}

	portsMu.Lock()
	sess := &PortSession{
		mgr:     newManagerFunc(1000),
		port:    port,
		baud:    115200,
		mode:    ModeFlasher, // concurrent AcquireForFlasher already reclaimed this session
		flasher: mock,
	}
	ports[port] = sess
	portsMu.Unlock()

	expireSession(sess, port)

	assert.False(t, mock.resetCalled, "Reset() must not run when the session was reclaimed before expiry")
	assert.False(t, mock.closeCalled, "Close() must not run when the session was reclaimed before expiry — the flasher must remain live for reuse")

	portsMu.Lock()
	assert.Same(t, mock, sess.flasher, "the cached flasher must remain intact for the racing acquirer to reuse")
	assert.Equal(t, ModeFlasher, sess.mode, "expireSession must not alter mode when it wasn't the reclaiming session's owner")
	_, exists := ports[port]
	portsMu.Unlock()
	assert.True(t, exists, "expireSession must not remove a session it doesn't own from the ports map")
}

// TestExpireSessionHeldSkipsTeardownWhenReclaimedByConcurrentAcquire is the
// held-hold variant of the BR-64 fix: even when noResetOnExpire is armed, a
// concurrent acquire that reclaimed the session before expiry must still win
// — closeCachedFlasher must not run, regardless of the hold flag.
func TestExpireSessionHeldSkipsTeardownWhenReclaimedByConcurrentAcquire(t *testing.T) {
	setupTestPorts(t)
	setupTestManagersFunc(t)
	setupFastWaitForPort(t)

	port := "/dev/cu.usbmodem1101"
	mock := &mockFlasher{chipNameVal: "ESP32"}

	portsMu.Lock()
	sess := &PortSession{
		mgr:             newManagerFunc(1000),
		port:            port,
		baud:            115200,
		mode:            ModeFlasher, // concurrent AcquireForFlasher already reclaimed this session
		flasher:         mock,
		noResetOnExpire: true,
	}
	ports[port] = sess
	portsMu.Unlock()

	expireSession(sess, port)

	assert.False(t, mock.resetCalled, "Reset() must not run when the session was reclaimed before expiry")
	assert.False(t, mock.closeCalled, "Close() must not run when the session was reclaimed before expiry")

	portsMu.Lock()
	assert.Same(t, mock, sess.flasher, "the cached flasher must remain intact for the racing acquirer to reuse")
	assert.True(t, sess.noResetOnExpire, "the hold flag must not be consumed by an expiry that didn't own the session")
	_, exists := ports[port]
	portsMu.Unlock()
	assert.True(t, exists, "expireSession must not remove a session it doesn't own from the ports map")
}

// TestResolveSessionHonorsNoResetOnExpireHold verifies the CRITICAL finding
// fix: a statusline/serial_read/serial_write/serial_status poll that routes
// through ResolveSession while a pending session's no-reset hold is armed
// (ReleaseFlasherDeferredNoReset) must reap the cached flasher via
// closeCachedFlasher — Close() only, never Reset() — so a mid-gpio-probe
// poll can't perturb pin state. The hold must also be consumed (cleared)
// so it can't leak into a later session lifecycle.
func TestResolveSessionHonorsNoResetOnExpireHold(t *testing.T) {
	setupTestPorts(t)
	setupFastWaitForPort(t)

	tmpfile, err := os.CreateTemp(t.TempDir(), "test-port-*")
	require.NoError(t, err)
	defer os.Remove(tmpfile.Name())
	tmpfile.Close()

	mgr := serial.NewManager()
	mgr.OpenFunc = func(portName string, mode *goSerial.Mode) (goSerial.Port, error) {
		return &noopPort{}, nil
	}
	mgr.SetTestState(true, tmpfile.Name(), 115200, nil)

	mock := &mockFlasher{chipNameVal: "ESP32"}

	portsMu.Lock()
	sess := &PortSession{
		mgr:             mgr,
		port:            tmpfile.Name(),
		baud:            115200,
		mode:            ModePending,
		flasher:         mock,
		noResetOnExpire: true,
	}
	ports[tmpfile.Name()] = sess
	portsMu.Unlock()

	m, port, err := ResolveSession(map[string]interface{}{"port": tmpfile.Name()})
	require.NoError(t, err)
	assert.Equal(t, tmpfile.Name(), port)
	assert.Equal(t, mgr, m)

	portsMu.Lock()
	assert.Equal(t, ModeReader, sess.mode)
	assert.Nil(t, sess.flasher)
	assert.False(t, sess.noResetOnExpire, "hold must be consumed by the reap so it can't leak into a later session")
	portsMu.Unlock()

	assert.True(t, mock.closeCalled, "Close() must still run when reaping a held session via ResolveSession")
	assert.False(t, mock.resetCalled, "Reset() must be skipped when ResolveSession reaps a no-reset-held pending session")
}

// TestResolveSessionResetsCachedFlasherWithoutHold is the control case for
// TestResolveSessionHonorsNoResetOnExpireHold: the same ResolveSession
// pending-reap path, without the hold armed, must still Reset() the cached
// flasher as before — proving centralizing the reap through
// closeCachedFlasher didn't change the normal-case behavior.
func TestResolveSessionResetsCachedFlasherWithoutHold(t *testing.T) {
	setupTestPorts(t)
	setupFastWaitForPort(t)

	tmpfile, err := os.CreateTemp(t.TempDir(), "test-port-*")
	require.NoError(t, err)
	defer os.Remove(tmpfile.Name())
	tmpfile.Close()

	mgr := serial.NewManager()
	mgr.OpenFunc = func(portName string, mode *goSerial.Mode) (goSerial.Port, error) {
		return &noopPort{}, nil
	}
	mgr.SetTestState(true, tmpfile.Name(), 115200, nil)

	mock := &mockFlasher{chipNameVal: "ESP32"}

	portsMu.Lock()
	sess := &PortSession{
		mgr:     mgr,
		port:    tmpfile.Name(),
		baud:    115200,
		mode:    ModePending,
		flasher: mock,
	}
	ports[tmpfile.Name()] = sess
	portsMu.Unlock()

	_, _, err = ResolveSession(map[string]interface{}{"port": tmpfile.Name()})
	require.NoError(t, err)

	assert.True(t, mock.resetCalled, "Reset() should still run on the normal (non-held) ResolveSession reap path")
	assert.True(t, mock.closeCalled, "Close() should still run on the normal ResolveSession reap path")
}

// TestCleanupAllSessionsForceResetsHeldSession mirrors
// TestReleaseFlasherImmediateForceResetsHeldSession: process shutdown must
// leave boards in a clean run state, never stuck in the bootloader, so
// CleanupAllSessions clears any armed no-reset hold before reaping a cached
// flasher — Reset() and Close() both run regardless of noResetOnExpire.
func TestCleanupAllSessionsForceResetsHeldSession(t *testing.T) {
	setupTestPorts(t)

	mgr := serial.NewManager()
	mgr.OpenFunc = func(portName string, mode *goSerial.Mode) (goSerial.Port, error) {
		return &noopPort{}, nil
	}
	mgr.SetTestState(true, "/dev/test", 115200, nil)

	mock := &mockFlasher{chipNameVal: "ESP32"}

	portsMu.Lock()
	ports["/dev/test"] = &PortSession{
		mgr:             mgr,
		port:            "/dev/test",
		baud:            115200,
		mode:            ModePending,
		flasher:         mock,
		noResetOnExpire: true,
	}
	portsMu.Unlock()

	CleanupAllSessions()

	assert.True(t, mock.resetCalled, "CleanupAllSessions must force Reset() even with the no-reset hold armed")
	assert.True(t, mock.closeCalled, "CleanupAllSessions must Close() the cached flasher")

	portsMu.Lock()
	_, exists := ports["/dev/test"]
	portsMu.Unlock()
	assert.False(t, exists, "session should be removed from the ports map")
}

// TestAcquireForExternalHonorsNoResetOnExpireHold verifies that
// AcquireForExternal's reap of an existing session's cached flasher honors
// an armed no-reset hold (ReleaseFlasherDeferredNoReset) the same way
// ResolveSession's reap does: Close() runs, Reset() is skipped, and the
// hold is consumed so it can't leak into the newly acquired external mode.
func TestAcquireForExternalHonorsNoResetOnExpireHold(t *testing.T) {
	setupTestPorts(t)

	mgr := serial.NewManager()
	mgr.OpenFunc = func(portName string, mode *goSerial.Mode) (goSerial.Port, error) {
		return &noopPort{}, nil
	}
	mgr.SetTestState(true, "/dev/test", 115200, nil)

	mock := &mockFlasher{chipNameVal: "ESP32"}

	portsMu.Lock()
	ports["/dev/test"] = &PortSession{
		mgr:             mgr,
		port:            "/dev/test",
		baud:            115200,
		mode:            ModePending,
		flasher:         mock,
		noResetOnExpire: true,
	}
	portsMu.Unlock()

	sess := AcquireForExternal("/dev/test")
	require.NotNil(t, sess)
	assert.Equal(t, ModeExternal, sess.mode)

	assert.False(t, mock.resetCalled, "AcquireForExternal must skip Reset() when the no-reset hold is armed")
	assert.True(t, mock.closeCalled, "AcquireForExternal must still Close() the cached flasher")

	portsMu.Lock()
	assert.False(t, sess.noResetOnExpire, "hold must be consumed so it can't leak into the acquired external session")
	portsMu.Unlock()
}

// TestStartSessionHonorsNoResetOnExpireHold verifies that StartSession's
// reap of an existing session's cached flasher honors an armed no-reset
// hold the same way ResolveSession's and AcquireForExternal's reaps do:
// Close() runs, Reset() is skipped.
func TestStartSessionHonorsNoResetOnExpireHold(t *testing.T) {
	setupTestPorts(t)

	mgr := serial.NewManager()
	mgr.OpenFunc = func(portName string, mode *goSerial.Mode) (goSerial.Port, error) {
		return &noopPort{}, nil
	}
	mgr.SetTestState(true, "/dev/test", 115200, nil)

	mock := &mockFlasher{chipNameVal: "ESP32"}

	portsMu.Lock()
	ports["/dev/test"] = &PortSession{
		mgr:             mgr,
		port:            "/dev/test",
		baud:            115200,
		mode:            ModePending,
		flasher:         mock,
		noResetOnExpire: true,
	}
	portsMu.Unlock()

	err := StartSession("/dev/test", 115200, 1000)
	require.NoError(t, err)

	assert.False(t, mock.resetCalled, "StartSession must skip Reset() when the no-reset hold is armed")
	assert.True(t, mock.closeCalled, "StartSession must still Close() the cached flasher")

	portsMu.Lock()
	sess, exists := ports["/dev/test"]
	portsMu.Unlock()
	require.True(t, exists)
	assert.False(t, sess.noResetOnExpire, "hold must be consumed so it can't leak into the restarted session")
}

func TestResolveProducerSessionID_Precedence(t *testing.T) {
	cases := []struct {
		name       string
		pogopinEnv string
		claudeEnv  string
		want       string
	}{
		{"POGOPIN_SESSION_ID wins over CLAUDE_CODE_SESSION_ID", "sess-pogopin", "sess-claude", "sess-pogopin"},
		{"falls back to CLAUDE_CODE_SESSION_ID when POGOPIN unset", "", "sess-claude", "sess-claude"},
		{"empty when neither set", "", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("POGOPIN_SESSION_ID", tc.pogopinEnv)
			t.Setenv("CLAUDE_CODE_SESSION_ID", tc.claudeEnv)
			assert.Equal(t, tc.want, resolveProducerSessionID())
		})
	}
}

func TestPortStateFor_SessionIDReflectsResolver(t *testing.T) {
	t.Setenv("POGOPIN_SESSION_ID", "sess-producer")
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")

	mgr := serial.NewManagerWithBufferSize(10)
	sess := &PortSession{mgr: mgr, mode: ModeReader}

	ps := portStateFor("/dev/test", sess)
	assert.Equal(t, "sess-producer", ps.SessionID)
	assert.Equal(t, os.Getpid(), ps.PID)
}

// TestModeString covers modeString's full switch, including the
// no-matching-case "unknown" default branch (an out-of-range PortMode value
// never occurs on a real session, but the fallback must not panic or
// mis-render).
func TestModeString(t *testing.T) {
	assert.Equal(t, "reader", modeString(ModeReader))
	assert.Equal(t, "flasher", modeString(ModeFlasher))
	assert.Equal(t, "external", modeString(ModeExternal))
	assert.Equal(t, "pending", modeString(ModePending))
	assert.Equal(t, "unknown", modeString(PortMode(99)))
}

// TestPortStateFor_LastErrorPopulated covers portStateFor's lastErr branch:
// when the manager reports a non-nil LastError, PortState.LastError must
// carry its message (not be left nil).
func TestPortStateFor_LastErrorPopulated(t *testing.T) {
	mgr := serial.NewManager()
	mgr.SetTestState(false, "/dev/test", 115200, fmt.Errorf("device removed"))
	sess := &PortSession{mgr: mgr, mode: ModeReader}

	ps := portStateFor("/dev/test", sess)
	require.NotNil(t, ps.LastError)
	assert.Equal(t, "device removed", *ps.LastError)
}
