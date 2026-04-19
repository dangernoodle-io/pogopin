package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"dangernoodle.io/pogopin/internal/serial"
	"dangernoodle.io/pogopin/internal/status"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	goSerial "go.bug.st/serial"
)

// Helper to setup status file path for testing.
func setupStatusFile(t *testing.T) string {
	tmp := t.TempDir()
	statusPath := filepath.Join(tmp, "status.json")
	prev := status.SetStatusFilePath(statusPath)
	t.Cleanup(func() { status.SetStatusFilePath(prev) })
	return statusPath
}

// Helper to read and parse status file.
func readStatusFile(t *testing.T, path string) *status.StatusFile {
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	var sf status.StatusFile
	err = json.Unmarshal(data, &sf)
	require.NoError(t, err)
	return &sf
}

// TestStartSession_WritesStatus verifies that StartSession writes status on exit.
func TestStartSession_WritesStatus(t *testing.T) {
	setupTestPorts(t)
	statusPath := setupStatusFile(t)

	origNewMgr := newManagerFunc
	defer func() { newManagerFunc = origNewMgr }()

	mgr := serial.NewManagerWithBufferSize(1000)
	mgr.SetTestState(true, "/dev/ttyUSB0", 115200, nil)
	mgr.OpenFunc = func(string, *goSerial.Mode) (goSerial.Port, error) {
		return &noopPort{}, nil
	}

	newManagerFunc = func(bufSize int) *serial.Manager {
		return mgr
	}

	err := StartSession("/dev/ttyUSB0", 115200, 1000)
	require.NoError(t, err)

	// Verify status file was written
	sf := readStatusFile(t, statusPath)
	require.Len(t, sf.Ports, 1)
	assert.Equal(t, "/dev/ttyUSB0", sf.Ports[0].Port)
	assert.Equal(t, 115200, sf.Ports[0].Baud)
	assert.Equal(t, "reader", sf.Ports[0].Mode)
	assert.True(t, sf.Ports[0].Running)
}

// TestStopSession_WritesStatus verifies that StopSession writes status on exit.
func TestStopSession_WritesStatus(t *testing.T) {
	setupTestPorts(t)
	statusPath := setupStatusFile(t)

	origNewMgr := newManagerFunc
	defer func() { newManagerFunc = origNewMgr }()

	mgr := serial.NewManagerWithBufferSize(1000)
	mgr.SetTestState(true, "/dev/ttyUSB0", 115200, nil)
	mgr.OpenFunc = func(string, *goSerial.Mode) (goSerial.Port, error) {
		return &noopPort{}, nil
	}

	newManagerFunc = func(bufSize int) *serial.Manager {
		return mgr
	}

	// Start a session first
	err := StartSession("/dev/ttyUSB0", 115200, 1000)
	require.NoError(t, err)

	// Stop the session
	err = StopSession("/dev/ttyUSB0")
	require.NoError(t, err)

	// Verify status file has no ports
	sf := readStatusFile(t, statusPath)
	assert.Len(t, sf.Ports, 0)
}

// TestCleanupAllSessions_WritesStatus verifies that CleanupAllSessions writes status on exit.
func TestCleanupAllSessions_WritesStatus(t *testing.T) {
	setupTestPorts(t)
	statusPath := setupStatusFile(t)

	origNewMgr := newManagerFunc
	defer func() { newManagerFunc = origNewMgr }()

	mgr := serial.NewManagerWithBufferSize(1000)
	mgr.SetTestState(true, "/dev/ttyUSB0", 115200, nil)
	mgr.OpenFunc = func(string, *goSerial.Mode) (goSerial.Port, error) {
		return &noopPort{}, nil
	}

	newManagerFunc = func(bufSize int) *serial.Manager {
		return mgr
	}

	// Start a session
	err := StartSession("/dev/ttyUSB0", 115200, 1000)
	require.NoError(t, err)

	// Cleanup all
	CleanupAllSessions()

	// Verify status file is now empty
	sf := readStatusFile(t, statusPath)
	assert.Len(t, sf.Ports, 0)
}

// TestAcquireForFlasher_WritesStatus verifies that AcquireForFlasher writes status on exit.
func TestAcquireForFlasher_WritesStatus(t *testing.T) {
	setupTestPorts(t)
	statusPath := setupStatusFile(t)

	orig := isUSBPortFn
	defer func() { isUSBPortFn = orig }()
	isUSBPortFn = func(s string) bool { return false }

	sess, factory := AcquireForFlasher("/dev/ttyUSB0")
	require.NotNil(t, sess)
	require.NotNil(t, factory)

	// Verify status file was written
	sf := readStatusFile(t, statusPath)
	require.Len(t, sf.Ports, 1)
	assert.Equal(t, "/dev/ttyUSB0", sf.Ports[0].Port)
	assert.Equal(t, "flasher", sf.Ports[0].Mode)
}

// TestReleaseFlasherDeferred_WritesStatus verifies that ReleaseFlasherDeferred writes status on exit.
func TestReleaseFlasherDeferred_WritesStatus(t *testing.T) {
	setupTestPorts(t)
	statusPath := setupStatusFile(t)
	setupFastDeferred(t)
	setupFastWaitForPort(t)

	orig := newManagerFunc
	defer func() { newManagerFunc = orig }()

	mgr := serial.NewManagerWithBufferSize(1000)
	mgr.SetTestState(true, "/dev/ttyUSB0", 115200, nil)

	newManagerFunc = func(bufSize int) *serial.Manager {
		return mgr
	}

	// Create a session
	sess := &PortSession{
		mgr:  mgr,
		port: "/dev/ttyUSB0",
		baud: 115200,
		mode: ModeFlasher,
	}
	InsertPort("/dev/ttyUSB0", sess)

	// Release with deferred
	ReleaseFlasherDeferred(sess, "/dev/ttyUSB0")

	// Verify status file shows pending mode
	sf := readStatusFile(t, statusPath)
	require.Len(t, sf.Ports, 1)
	assert.Equal(t, "pending", sf.Ports[0].Mode)
}

// TestReleaseFlasherImmediate_WritesStatus verifies that ReleaseFlasherImmediate writes status on exit.
func TestReleaseFlasherImmediate_WritesStatus(t *testing.T) {
	setupTestPorts(t)
	statusPath := setupStatusFile(t)
	setupFastWaitForPort(t)

	origNewMgr := newManagerFunc
	defer func() { newManagerFunc = origNewMgr }()

	mgr := serial.NewManagerWithBufferSize(1000)
	mgr.SetTestState(true, "/dev/ttyUSB0", 115200, nil)
	mgr.OpenFunc = func(string, *goSerial.Mode) (goSerial.Port, error) {
		return &noopPort{}, nil
	}

	newManagerFunc = func(bufSize int) *serial.Manager {
		return mgr
	}

	// Create temp file to simulate port existence
	tmpfile, err := os.CreateTemp(t.TempDir(), "port-*")
	require.NoError(t, err)
	tmpfile.Close()

	// Create a session with the temp file as the port
	sess := &PortSession{
		mgr:  mgr,
		port: tmpfile.Name(),
		baud: 115200,
		mode: ModeFlasher,
	}
	InsertPort(tmpfile.Name(), sess)

	newPort := ReleaseFlasherImmediate(sess, tmpfile.Name())
	assert.Equal(t, "", newPort)

	// Verify status file shows reader mode
	sf := readStatusFile(t, statusPath)
	require.Len(t, sf.Ports, 1)
	assert.Equal(t, "reader", sf.Ports[0].Mode)
}

// TestAcquireForExternal_WritesStatus verifies that AcquireForExternal writes status on exit.
func TestAcquireForExternal_WritesStatus(t *testing.T) {
	setupTestPorts(t)
	statusPath := setupStatusFile(t)

	sess := AcquireForExternal("/dev/ttyUSB0")
	require.NotNil(t, sess)

	// Verify status file was written
	sf := readStatusFile(t, statusPath)
	require.Len(t, sf.Ports, 1)
	assert.Equal(t, "/dev/ttyUSB0", sf.Ports[0].Port)
	assert.Equal(t, "external", sf.Ports[0].Mode)
}

// TestReleaseExternal_WritesStatus verifies that ReleaseExternal writes status on exit.
func TestReleaseExternal_WritesStatus(t *testing.T) {
	setupTestPorts(t)
	statusPath := setupStatusFile(t)
	setupFastWaitForPort(t)

	origNewMgr := newManagerFunc
	defer func() { newManagerFunc = origNewMgr }()

	mgr := serial.NewManagerWithBufferSize(1000)
	mgr.SetTestState(true, "/dev/ttyUSB0", 115200, nil)
	mgr.OpenFunc = func(string, *goSerial.Mode) (goSerial.Port, error) {
		return &noopPort{}, nil
	}

	newManagerFunc = func(bufSize int) *serial.Manager {
		return mgr
	}

	// Create temp file to simulate port existence
	tmpfile, err := os.CreateTemp(t.TempDir(), "port-*")
	require.NoError(t, err)
	tmpfile.Close()

	// Create a session with the temp file as the port
	sess := &PortSession{
		mgr:  mgr,
		port: tmpfile.Name(),
		baud: 115200,
		mode: ModeExternal,
	}
	InsertPort(tmpfile.Name(), sess)

	newPort := ReleaseExternal(sess, tmpfile.Name())
	assert.Equal(t, "", newPort)

	// Verify status file shows reader mode
	sf := readStatusFile(t, statusPath)
	require.Len(t, sf.Ports, 1)
	assert.Equal(t, "reader", sf.Ports[0].Mode)
}

// TestResolveSession_WritesStatus verifies that ResolveSession writes status on exit (best effort).
func TestResolveSession_WritesStatus(t *testing.T) {
	setupTestPorts(t)
	statusPath := setupStatusFile(t)

	orig := newManagerFunc
	defer func() { newManagerFunc = orig }()

	mgr := serial.NewManagerWithBufferSize(1000)
	mgr.SetTestState(true, "/dev/ttyUSB0", 115200, nil)

	newManagerFunc = func(bufSize int) *serial.Manager {
		return mgr
	}

	// Create and insert a session
	sess := &PortSession{
		mgr:  mgr,
		port: "/dev/ttyUSB0",
		baud: 115200,
		mode: ModeReader,
	}
	InsertPort("/dev/ttyUSB0", sess)

	// Resolve the session
	m, port, err := ResolveSession(map[string]interface{}{"port": "/dev/ttyUSB0"})
	require.NoError(t, err)
	require.NotNil(t, m)
	assert.Equal(t, "/dev/ttyUSB0", port)

	// Verify status file was written
	sf := readStatusFile(t, statusPath)
	require.Len(t, sf.Ports, 1)
	assert.Equal(t, "/dev/ttyUSB0", sf.Ports[0].Port)
}

// TestAllPortStates returns all port states.
func TestAllPortStates(t *testing.T) {
	setupTestPorts(t)

	orig := newManagerFunc
	defer func() { newManagerFunc = orig }()

	mgr := serial.NewManagerWithBufferSize(1000)
	mgr.SetTestState(true, "/dev/ttyUSB0", 115200, nil)

	newManagerFunc = func(bufSize int) *serial.Manager {
		return mgr
	}

	// Create and insert a session
	sess := &PortSession{
		mgr:  mgr,
		port: "/dev/ttyUSB0",
		baud: 115200,
		mode: ModeReader,
	}
	InsertPort("/dev/ttyUSB0", sess)

	// Get all states
	states := AllPortStates()
	require.Len(t, states, 1)
	assert.Equal(t, "/dev/ttyUSB0", states[0].Port)
	assert.Equal(t, 115200, states[0].Baud)
	assert.Equal(t, "reader", states[0].Mode)
	assert.True(t, states[0].Running)
}

// TestExpireSession_WritesStatus verifies that expireSession writes status on all exits (best effort)
// Note: This test directly calls expireSession rather than testing via timer.
func TestExpireSession_WritesStatus(t *testing.T) {
	setupTestPorts(t)
	statusPath := setupStatusFile(t)
	setupFastWaitForPort(t)

	origNewMgr := newManagerFunc
	defer func() { newManagerFunc = origNewMgr }()

	mgr := serial.NewManagerWithBufferSize(1000)
	mgr.SetTestState(true, "/dev/ttyUSB0", 115200, nil)
	mgr.OpenFunc = func(string, *goSerial.Mode) (goSerial.Port, error) {
		return &noopPort{}, nil
	}

	newManagerFunc = func(bufSize int) *serial.Manager {
		return mgr
	}

	// Create temp file to simulate port existence
	tmpfile, err := os.CreateTemp(t.TempDir(), "port-*")
	require.NoError(t, err)
	tmpfile.Close()

	// Create a session in pending mode
	sess := &PortSession{
		mgr:  mgr,
		port: tmpfile.Name(),
		baud: 115200,
		mode: ModePending,
	}
	InsertPort(tmpfile.Name(), sess)

	// Manually call expireSession (simulating timer expiration)
	expireSession(sess, tmpfile.Name())

	// Verify status file was written - should restart the reader
	sf := readStatusFile(t, statusPath)
	require.Len(t, sf.Ports, 1)
	assert.Equal(t, "reader", sf.Ports[0].Mode)
}
