package session

import (
	"fmt"
	"os"
	"testing"
	"time"

	"dangernoodle.io/breadboard/internal/esp"
	"dangernoodle.io/breadboard/internal/serial"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	goSerial "go.bug.st/serial"
	espflasher "tinygo.org/x/espflasher/pkg/espflasher"
)

// Test helpers

func setupTestPorts(t *testing.T) {
	orig := ports
	t.Cleanup(func() {
		// Stop all timers and close cached flashers to prevent goroutine leaks
		portsMu.Lock()
		for _, sess := range ports {
			if sess.timer != nil {
				sess.timer.Stop()
				sess.timer = nil
			}
			if sess.flasher != nil {
				sess.flasher.Reset()
				_ = sess.flasher.Close()
				sess.flasher = nil
			}
		}
		portsMu.Unlock()
		ports = orig
	})
	ports = map[string]*PortSession{}
}

func setupFastDeferred(t *testing.T) {
	orig := deferredRestartTimeout
	deferredRestartTimeout = 10 * time.Millisecond
	t.Cleanup(func() { deferredRestartTimeout = orig })
}

func setupFastWaitForPort(t *testing.T) {
	orig := waitForPortInterval
	waitForPortInterval = time.Millisecond
	t.Cleanup(func() { waitForPortInterval = orig })
}

func setupFastSyncRetry(t *testing.T) {
	orig := syncRetryDelay
	syncRetryDelay = time.Millisecond
	t.Cleanup(func() { syncRetryDelay = orig })
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

	result := WaitForPort(tmpfile.Name(), 5*time.Second)
	assert.Equal(t, tmpfile.Name(), result)
}

func TestWaitForPortTimeout(t *testing.T) {
	setupFastWaitForPort(t)

	result := WaitForPort("/nonexistent/port/path", 50*time.Millisecond)
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
	listPortsFn = func(usbOnly bool) ([]serial.PortInfo, error) {
		callCount++
		if callCount > 1 {
			return []serial.PortInfo{
				{Name: reenumeratedPort},
			}, nil
		}
		return []serial.PortInfo{}, nil
	}

	result := WaitForPort(originalPort, 100*time.Millisecond)
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

	sess, factory := AcquireForFlasher("/dev/test")
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

	sess, _ := AcquireForFlasher("/dev/test")
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

	sess, factory := AcquireForFlasher("/dev/test")
	require.NotNil(t, factory)
	require.NotNil(t, sess)

	// Factory should return borrowed flasher
	f, err := factory("/dev/test", &espflasher.FlasherOptions{})
	require.NoError(t, err)

	borrowed, ok := f.(*BorrowedFlasher)
	require.True(t, ok)
	assert.Equal(t, mock, borrowed.Flasher)
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

	sess, factory := AcquireForFlasher("/dev/test")
	require.NotNil(t, sess)
	require.NotNil(t, factory)

	f, err := factory("/dev/test", &espflasher.FlasherOptions{})
	require.NoError(t, err)
	borrowed, ok := f.(*BorrowedFlasher)
	require.True(t, ok, "factory should return BorrowedFlasher")
	assert.Equal(t, realMock, borrowed.Flasher)
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

// ReleaseFlasherDeferred tests

func TestReleaseFlasherDeferredStartsTimer(t *testing.T) {
	setupTestPorts(t)
	setupFastDeferred(t)
	setupFastWaitForPort(t)

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

	ReleaseFlasherDeferred(sess, tmpfile.Name())

	portsMu.Lock()
	assert.Equal(t, ModePending, sess.mode)
	assert.NotNil(t, sess.timer)
	portsMu.Unlock()

	// Wait for timer to expire
	time.Sleep(50 * time.Millisecond)

	// Session should have been restarted
	portsMu.Lock()
	assert.Equal(t, ModeReader, sess.mode)
	portsMu.Unlock()
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
	sess, _ := AcquireForFlasher(tmpfile.Name())
	assert.Equal(t, ModeFlasher, sess.mode)

	// Cache a flasher
	mock := &mockFlasher{chipNameVal: "ESP32"}
	portsMu.Lock()
	sess.flasher = mock
	portsMu.Unlock()

	// Transition to ModePending
	ReleaseFlasherDeferred(sess, tmpfile.Name())
	portsMu.Lock()
	assert.Equal(t, ModePending, sess.mode)
	portsMu.Unlock()

	// Wait for deferred restart
	time.Sleep(50 * time.Millisecond)

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

	sess, factory := AcquireForFlasher(port)
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

	// Mock ListPorts to return the new port
	origListPorts := listPortsFn
	t.Cleanup(func() { listPortsFn = origListPorts })

	listPortsFn = func(usbOnly bool) ([]serial.PortInfo, error) {
		return []serial.PortInfo{
			{Name: newPort},
		}, nil
	}

	sess, factory := AcquireForFlasher(originalPort)
	require.NotNil(t, sess)

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

	sess, factory := AcquireForFlasher(port)
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

	sess, factory := AcquireForFlasher(port)
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

	sess, factory := AcquireForFlasher(port)
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
