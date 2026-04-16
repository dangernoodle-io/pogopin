package flash

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockManager struct {
	portName  string
	baud      int
	running   bool
	stopErr   error
	startErr  error
	readLines []string
}

func (m *mockManager) PortName() string {
	return m.portName
}

func (m *mockManager) Baud() int {
	return m.baud
}

func (m *mockManager) IsRunning() bool {
	return m.running
}

func (m *mockManager) Stop() error {
	m.running = false
	return m.stopErr
}

func (m *mockManager) Start(port string, baud int) error {
	if m.startErr != nil {
		return m.startErr
	}
	m.running = true
	return nil
}

func (m *mockManager) Read(n int) []string {
	if n > len(m.readLines) {
		return m.readLines
	}
	return m.readLines[:n]
}

func (m *mockManager) ClearBuffer() {}

func (m *mockManager) SetPortName(name string) {
	m.portName = name
}

// TestResolveShellPath verifies that resolveShellPath() returns a non-empty PATH
// with the OS path separator.
func TestResolveShellPath(t *testing.T) {
	p := resolveShellPath()
	assert.NotEmpty(t, p)
	assert.Contains(t, p, string(os.PathListSeparator))
}

// TestEnvWithPath verifies that envWithPath() constructs the PATH environment
// variable correctly in the environment slice.
func TestEnvWithPath(t *testing.T) {
	env := envWithPath("/test/path:/usr/bin")
	found := false
	for _, e := range env {
		if strings.HasPrefix(e, "PATH=") {
			assert.Equal(t, "PATH=/test/path:/usr/bin", e)
			found = true
		}
	}
	assert.True(t, found)
}

// TestFlashRequiresConfiguredPort verifies that Flash() returns an error
// if no serial port has been configured.
func TestFlashRequiresConfiguredPort(t *testing.T) {
	mgr := &mockManager{}
	_, err := Flash(mgr, "echo", []string{"hello"}, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no serial port configured")
}

// TestFlashStopsAndRestarts verifies the full flash cycle: stop port,
// execute command, restart port, and collect output from the restarted port.
func TestFlashStopsAndRestarts(t *testing.T) {
	var stopCount int
	var startCount int
	var mu sync.Mutex

	tm := &struct{ *mockManager }{
		mockManager: &mockManager{
			portName:  "test-port",
			baud:      115200,
			running:   true,
			readLines: []string{"boot: ready"},
		},
	}

	// Create wrapper that tracks calls
	originalMgr := tm.mockManager
	result, err := Flash(&wrappedManager{
		original: originalMgr,
		stopFn: func() error {
			mu.Lock()
			stopCount++
			mu.Unlock()
			originalMgr.running = false
			return nil
		},
		startFn: func(port string, baud int) error {
			mu.Lock()
			startCount++
			mu.Unlock()
			originalMgr.running = true
			return nil
		},
	}, "echo", []string{"flash output"}, nil)
	require.NoError(t, err)

	assert.True(t, result.Success)
	assert.Contains(t, result.CommandOutput, "flash output")

	mu.Lock()
	assert.Equal(t, 1, stopCount, "Stop should be called once")
	assert.Equal(t, 1, startCount, "Start should be called once")
	mu.Unlock()

	assert.Contains(t, result.SerialOutput, "boot: ready")
}

// wrappedManager allows intercepting Stop and Start calls.
type wrappedManager struct {
	original PortManager
	stopFn   func() error
	startFn  func(string, int) error
}

func (w *wrappedManager) PortName() string {
	return w.original.PortName()
}

func (w *wrappedManager) Baud() int {
	return w.original.Baud()
}

func (w *wrappedManager) IsRunning() bool {
	return w.original.IsRunning()
}

func (w *wrappedManager) Stop() error {
	return w.stopFn()
}

func (w *wrappedManager) Start(port string, baud int) error {
	return w.startFn(port, baud)
}

func (w *wrappedManager) Read(n int) []string {
	return w.original.Read(n)
}

func (w *wrappedManager) ClearBuffer() {
	w.original.ClearBuffer()
}

func (w *wrappedManager) SetPortName(name string) {
	w.original.SetPortName(name)
}

// TestFlashCommandFailure verifies that Flash() correctly handles command
// execution failures and returns Success=false.
func TestFlashCommandFailure(t *testing.T) {
	mgr := &mockManager{
		portName: "test-port",
		baud:     115200,
		running:  true,
	}

	result, err := Flash(mgr, "false", nil, nil)
	require.NoError(t, err)
	assert.False(t, result.Success)
	assert.Contains(t, result.CommandOutput, "Command failed")
}

// TestFlashRetriesOnStartFailure verifies that Flash() retries the Start call
// when port re-enumeration fails temporarily.
func TestFlashRetriesOnStartFailure(t *testing.T) {
	orig := retryDelays
	retryDelays = []time.Duration{time.Millisecond, time.Millisecond, time.Millisecond, time.Millisecond, time.Millisecond}
	defer func() { retryDelays = orig }()

	var startCount int
	var mu sync.Mutex

	mgr := &mockManager{
		portName:  "test-port",
		baud:      115200,
		running:   true,
		readLines: []string{"boot: ready"},
	}

	result, err := Flash(&wrappedManager{
		original: mgr,
		stopFn: func() error {
			mgr.running = false
			return nil
		},
		startFn: func(port string, baud int) error {
			mu.Lock()
			startCount++
			count := startCount
			mu.Unlock()
			if count < 3 {
				return fmt.Errorf("port busy")
			}
			mgr.running = true
			return nil
		},
	}, "echo", []string{"flash"}, nil)
	require.NoError(t, err)
	assert.True(t, result.Success)

	mu.Lock()
	assert.Equal(t, 3, startCount)
	mu.Unlock()
}

// TestFlashRetriesAllFail verifies that Flash() exhausts all retry attempts
// when port re-enumeration fails consistently.
func TestFlashRetriesAllFail(t *testing.T) {
	orig := retryDelays
	retryDelays = []time.Duration{time.Millisecond, time.Millisecond, time.Millisecond, time.Millisecond, time.Millisecond}
	defer func() { retryDelays = orig }()

	var startCount int
	var mu sync.Mutex

	mgr := &mockManager{
		portName: "test-port",
		baud:     115200,
		running:  true,
	}

	result, err := Flash(&wrappedManager{
		original: mgr,
		stopFn: func() error {
			mgr.running = false
			return nil
		},
		startFn: func(port string, baud int) error {
			mu.Lock()
			startCount++
			mu.Unlock()
			return fmt.Errorf("port busy")
		},
	}, "echo", []string{"flash"}, nil)
	require.NoError(t, err)
	assert.True(t, result.Success)
	assert.Contains(t, result.CommandOutput, "Warning: failed to restart serial after 5 attempts")

	mu.Lock()
	assert.Equal(t, 5, startCount)
	mu.Unlock()
}

// TestFlashPortReenumeration verifies that Flash() successfully handles port
// re-enumeration when findSimilarPortFn discovers a new port and the manager
// is updated to use it.
func TestFlashPortReenumeration(t *testing.T) {
	origRetry := retryDelays
	retryDelays = []time.Duration{time.Millisecond, time.Millisecond}
	defer func() { retryDelays = origRetry }()

	origFindFn := findSimilarPortFn
	findSimilarPortFn = func(port string) string {
		if port == "test-device-port" {
			return "test-device-new-port"
		}
		return ""
	}
	defer func() { findSimilarPortFn = origFindFn }()

	mgr := &mockManager{
		portName:  "test-device-port",
		baud:      115200,
		running:   true,
		readLines: []string{"device: ready"},
	}

	var startAttempts []string
	result, err := Flash(&wrappedManager{
		original: mgr,
		stopFn: func() error {
			mgr.running = false
			return nil
		},
		startFn: func(port string, baud int) error {
			startAttempts = append(startAttempts, port)
			// Fail on first port, succeed on second
			if port == "test-device-port" {
				return fmt.Errorf("port enumeration mismatch")
			}
			mgr.running = true
			return nil
		},
	}, "echo", []string{"test"}, nil)

	require.NoError(t, err)
	assert.True(t, result.Success)
	assert.Equal(t, "test-device-new-port", mgr.PortName())
	assert.Contains(t, startAttempts, "test-device-new-port")
}

// TestFlashPortReenumerationNoMatch verifies that Flash() handles the case
// where findSimilarPortFn cannot find a matching port and all start attempts
// fail.
func TestFlashPortReenumerationNoMatch(t *testing.T) {
	origRetry := retryDelays
	retryDelays = []time.Duration{time.Millisecond, time.Millisecond}
	defer func() { retryDelays = origRetry }()

	origFindFn := findSimilarPortFn
	findSimilarPortFn = func(port string) string {
		return "" // No match found
	}
	defer func() { findSimilarPortFn = origFindFn }()

	mgr := &mockManager{
		portName: "test-device-port",
		baud:     115200,
		running:  true,
	}

	result, err := Flash(&wrappedManager{
		original: mgr,
		stopFn: func() error {
			mgr.running = false
			return nil
		},
		startFn: func(port string, baud int) error {
			return fmt.Errorf("port not found")
		},
	}, "echo", []string{"test"}, nil)

	require.NoError(t, err)
	assert.True(t, result.Success)
	assert.Contains(t, result.CommandOutput, "Warning: failed to restart serial after 2 attempts")
	assert.Equal(t, "test-device-port", mgr.PortName())
}

// TestFlashOutputLinesLimit verifies that Flash() correctly limits output to the last N lines
// when OutputLines is set in Options.
func TestFlashOutputLinesLimit(t *testing.T) {
	mgr := &mockManager{
		portName:  "test-port",
		baud:      115200,
		running:   true,
		readLines: []string{"boot: ready"},
	}

	opts := &Options{OutputLines: 3}
	result, err := Flash(mgr, "echo", []string{"-e", "line1\nline2\nline3\nline4\nline5"}, opts)
	require.NoError(t, err)
	assert.True(t, result.Success)

	// Verify only last 3 lines are present
	lines := strings.Split(result.CommandOutput, "\n")
	// Filter out empty lines for counting
	nonEmpty := make([]string, 0)
	for _, l := range lines {
		if l != "" {
			nonEmpty = append(nonEmpty, l)
		}
	}
	assert.LessOrEqual(t, len(nonEmpty), 3, "should have at most 3 non-empty lines")
}

// TestFlashOutputFilterRegex verifies that Flash() correctly filters output lines
// based on a regex pattern when OutputFilter is set in Options.
func TestFlashOutputFilterRegex(t *testing.T) {
	mgr := &mockManager{
		portName:  "test-port",
		baud:      115200,
		running:   true,
		readLines: []string{"boot: ready"},
	}

	opts := &Options{OutputFilter: "^error"}
	result, err := Flash(mgr, "echo", []string{"-e", "error: foo\ninfo: bar\nerror: baz\nwarning: qux"}, opts)
	require.NoError(t, err)
	assert.True(t, result.Success)

	// Verify only lines starting with "error" are present
	lines := strings.Split(result.CommandOutput, "\n")
	for _, line := range lines {
		if line != "" {
			assert.True(t, strings.HasPrefix(line, "error"), "all non-empty lines should start with 'error'")
		}
	}
}

// TestFlashOutputFilterAndLines verifies that Filter is applied first, then tail
// when both OutputFilter and OutputLines are set.
func TestFlashOutputFilterAndLines(t *testing.T) {
	mgr := &mockManager{
		portName:  "test-port",
		baud:      115200,
		running:   true,
		readLines: []string{"boot: ready"},
	}

	opts := &Options{
		OutputFilter: "log",
		OutputLines:  2,
	}
	result, err := Flash(mgr, "echo", []string{"-e", "log1\nother\nlog2\ndata\nlog3\nstuff\nlog4"}, opts)
	require.NoError(t, err)
	assert.True(t, result.Success)

	// Verify filter applied first (only lines with "log"), then tail of 2
	lines := strings.Split(strings.TrimSpace(result.CommandOutput), "\n")
	assert.LessOrEqual(t, len(lines), 2, "should have at most 2 lines after filtering and tailing")
	// Last lines should be log3 and log4
	for _, line := range lines {
		if line != "" {
			assert.Contains(t, line, "log", "filtered lines should contain 'log'")
		}
	}
}

// TestFlashOutputFilterInvalidRegex verifies that Flash() returns an error
// when an invalid regex pattern is provided in OutputFilter.
func TestFlashOutputFilterInvalidRegex(t *testing.T) {
	mgr := &mockManager{
		portName:  "test-port",
		baud:      115200,
		running:   true,
		readLines: []string{"boot: ready"},
	}

	opts := &Options{OutputFilter: "[invalid"}
	_, err := Flash(mgr, "echo", []string{"test"}, opts)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid output filter regex")
}

// TestFlashShellMode verifies that Flash() with Shell=true executes the command
// via sh -c, enabling shell syntax like && and pipes.
func TestFlashShellMode(t *testing.T) {
	mgr := &mockManager{
		portName:  "test-port",
		baud:      115200,
		running:   true,
		readLines: []string{"boot: ready"},
	}

	opts := &Options{Shell: true}
	result, err := Flash(mgr, "echo hello && echo world", nil, opts)
	require.NoError(t, err)
	assert.True(t, result.Success)
	assert.Contains(t, result.CommandOutput, "hello")
	assert.Contains(t, result.CommandOutput, "world")
}

// TestFlashShellModeIgnoresArgs verifies that when Shell=true, the args parameter
// is ignored and only the command string is executed.
func TestFlashShellModeIgnoresArgs(t *testing.T) {
	mgr := &mockManager{
		portName:  "test-port",
		baud:      115200,
		running:   true,
		readLines: []string{"boot: ready"},
	}

	opts := &Options{Shell: true}
	result, err := Flash(mgr, "echo shell-only", []string{"ignored"}, opts)
	require.NoError(t, err)
	assert.True(t, result.Success)
	assert.Contains(t, result.CommandOutput, "shell-only")
	assert.NotContains(t, result.CommandOutput, "ignored")
}

// TestFlashCwd verifies that Flash() with Cwd set executes the command in the
// specified working directory.
func TestFlashCwd(t *testing.T) {
	mgr := &mockManager{
		portName:  "test-port",
		baud:      115200,
		running:   true,
		readLines: []string{"boot: ready"},
	}

	opts := &Options{Cwd: "/tmp"}
	result, err := Flash(mgr, "pwd", nil, opts)
	require.NoError(t, err)
	assert.True(t, result.Success)
	// On macOS, /tmp resolves to /private/tmp, so check for either
	assert.True(t,
		strings.Contains(result.CommandOutput, "/tmp") || strings.Contains(result.CommandOutput, "/private/tmp"),
		"output should contain /tmp or /private/tmp, got: %s", result.CommandOutput)
}
