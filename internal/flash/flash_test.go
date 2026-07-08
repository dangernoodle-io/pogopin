package flash

import (
	"bytes"
	"debug/elf"
	"debug/macho"
	"debug/pe"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"dangernoodle.io/pogopin/internal/serial"
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
	_, err := Flash(mgr, "echo", []string{"hello"}, nil, nil)
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
	}, "echo", []string{"flash output"}, nil, nil)
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
// TestFlashStatusPhaseSequence verifies Flash() emits its three coarse
// status ticks -- stopping port, running command, restarting -- in order,
// with no byte denominator. CapturingBoot/Complete are emitted by callers
// (e.g. handleSerialFlash) after Flash returns, not by Flash itself.
func TestFlashStatusPhaseSequence(t *testing.T) {
	orig := retryDelays
	retryDelays = []time.Duration{time.Millisecond}
	defer func() { retryDelays = orig }()

	mgr := &mockManager{
		portName: "test-port",
		baud:     115200,
		running:  true,
	}

	var ticks []string
	_, err := Flash(mgr, "echo", []string{"hello"}, nil, func(phase string, current, total int) {
		ticks = append(ticks, phase)
		assert.Equal(t, 0, current)
		assert.Equal(t, 0, total)
	})
	require.NoError(t, err)
	assert.Equal(t, []string{
		StatusPhaseStoppingPort,
		StatusPhaseRunningCmd,
		StatusPhaseRestarting,
	}, ticks)
}

// TestFlashNilStatusNoop verifies a nil status callback is a silent no-op.
func TestFlashNilStatusNoop(t *testing.T) {
	orig := retryDelays
	retryDelays = []time.Duration{time.Millisecond}
	defer func() { retryDelays = orig }()

	mgr := &mockManager{
		portName: "test-port",
		baud:     115200,
		running:  true,
	}

	assert.NotPanics(t, func() {
		_, err := Flash(mgr, "echo", []string{"hello"}, nil, nil)
		require.NoError(t, err)
	})
}

func TestFlashCommandFailure(t *testing.T) {
	mgr := &mockManager{
		portName: "test-port",
		baud:     115200,
		running:  true,
	}

	result, err := Flash(mgr, "false", nil, nil, nil)
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
	}, "echo", []string{"flash"}, nil, nil)
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
	}, "echo", []string{"flash"}, nil, nil)
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
	findSimilarPortFn = func(port string, knownPorts map[string]bool) string {
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
	}, "echo", []string{"test"}, nil, nil)

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
	findSimilarPortFn = func(port string, knownPorts map[string]bool) string {
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
	}, "echo", []string{"test"}, nil, nil)

	require.NoError(t, err)
	assert.True(t, result.Success)
	assert.Contains(t, result.CommandOutput, "Warning: failed to restart serial after 2 attempts")
	assert.Equal(t, "test-device-port", mgr.PortName())
}

// TestFlashSnapshotsKnownPortsBeforeExternalOp verifies that Flash() snapshots
// the currently-listed ports (via listPortsFn) before running the external
// command, and passes that snapshot to findSimilarPortFn as knownPorts
// (BR-58) — so a re-enumeration match can be distinguished from an unrelated,
// pre-existing board's port.
func TestFlashSnapshotsKnownPortsBeforeExternalOp(t *testing.T) {
	origRetry := retryDelays
	retryDelays = []time.Duration{time.Millisecond}
	defer func() { retryDelays = origRetry }()

	origListPortsFn := listPortsFn
	listPortsFn = func() ([]serial.PortInfo, error) {
		return []serial.PortInfo{{Name: "test-device-port"}, {Name: "unrelated-board-port"}}, nil
	}
	defer func() { listPortsFn = origListPortsFn }()

	var gotKnownPorts map[string]bool
	origFindFn := findSimilarPortFn
	findSimilarPortFn = func(port string, knownPorts map[string]bool) string {
		gotKnownPorts = knownPorts
		return ""
	}
	defer func() { findSimilarPortFn = origFindFn }()

	mgr := &mockManager{
		portName: "test-device-port",
		baud:     115200,
		running:  true,
	}

	_, err := Flash(&wrappedManager{
		original: mgr,
		stopFn: func() error {
			mgr.running = false
			return nil
		},
		startFn: func(port string, baud int) error {
			return fmt.Errorf("port not found")
		},
	}, "echo", []string{"test"}, nil, nil)

	require.NoError(t, err)
	require.NotNil(t, gotKnownPorts)
	assert.True(t, gotKnownPorts["test-device-port"])
	assert.True(t, gotKnownPorts["unrelated-board-port"])
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
	result, err := Flash(mgr, "echo", []string{"-e", "line1\nline2\nline3\nline4\nline5"}, opts, nil)
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
	result, err := Flash(mgr, "echo", []string{"-e", "error: foo\ninfo: bar\nerror: baz\nwarning: qux"}, opts, nil)
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
	result, err := Flash(mgr, "echo", []string{"-e", "log1\nother\nlog2\ndata\nlog3\nstuff\nlog4"}, opts, nil)
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
	_, err := Flash(mgr, "echo", []string{"test"}, opts, nil)
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
	result, err := Flash(mgr, "echo hello && echo world", nil, opts, nil)
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
	result, err := Flash(mgr, "echo shell-only", []string{"ignored"}, opts, nil)
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
	result, err := Flash(mgr, "pwd", nil, opts, nil)
	require.NoError(t, err)
	assert.True(t, result.Success)
	// On macOS, /tmp resolves to /private/tmp, so check for either
	assert.True(t,
		strings.Contains(result.CommandOutput, "/tmp") || strings.Contains(result.CommandOutput, "/private/tmp"),
		"output should contain /tmp or /private/tmp, got: %s", result.CommandOutput)
}

// --- BR-51: preflight architecture check ---------------------------------

// TestPreflightFlashCommandMissingBinary verifies that preflightFlashCommand
// returns a clear error when the command can't be found on PATH.
func TestPreflightFlashCommandMissingBinary(t *testing.T) {
	_, _, err := preflightFlashCommand("definitely-not-a-real-flasher-xyz123")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found on PATH")
}

// TestPreflightFlashCommandSameArchBinary verifies that a binary matching
// the host architecture passes preflight with no error and no warning.
func TestPreflightFlashCommandSameArchBinary(t *testing.T) {
	resolved, warning, err := preflightFlashCommand("echo")
	require.NoError(t, err)
	assert.NotEmpty(t, resolved)
	assert.Empty(t, warning)
}

// TestFlashRejectsMissingCommand verifies that Flash() itself surfaces the
// preflight LookPath failure before attempting to exec anything.
func TestFlashRejectsMissingCommand(t *testing.T) {
	mgr := &mockManager{portName: "test-port", baud: 115200, running: true}
	_, err := Flash(mgr, "definitely-not-a-real-flasher-xyz123", nil, nil, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found on PATH")
}

// TestDetectBinaryArchsHostBinary verifies that detectBinaryArchs recognizes
// a real, native host binary and reports the running GOARCH among its
// detected architectures.
func TestDetectBinaryArchsHostBinary(t *testing.T) {
	path, err := os.Executable()
	require.NoError(t, err)

	archs, err := detectBinaryArchs(path)
	require.NoError(t, err)
	assert.Contains(t, archs, runtime.GOARCH)
}

// TestDetectBinaryArchsUnparseableHeader verifies that detectBinaryArchs
// returns an error (not a panic, not a false arch) for a file that isn't a
// recognized macho/elf/pe binary -- e.g. a shell-script wrapper.
func TestDetectBinaryArchsUnparseableHeader(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "wrapper.sh")
	require.NoError(t, os.WriteFile(script, []byte("#!/bin/sh\necho hi\n"), 0o755))

	archs, err := detectBinaryArchs(script)
	require.Error(t, err)
	assert.Nil(t, archs)
}

// TestPreflightFlashCommandUnparseableHeaderProceeds verifies that when the
// resolved binary's header can't be parsed (e.g. a shell-script wrapper),
// preflightFlashCommand does not block -- it skips the arch check and
// returns no error and no warning.
func TestPreflightFlashCommandUnparseableHeaderProceeds(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "my-flasher")
	require.NoError(t, os.WriteFile(script, []byte("#!/bin/sh\necho ok\n"), 0o755))
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	resolved, warning, err := preflightFlashCommand("my-flasher")
	require.NoError(t, err)
	assert.Equal(t, script, resolved)
	assert.Empty(t, warning)
}

// writeMinimalMachO64 appends a minimal, valid single-arch 64-bit Mach-O
// header (no load commands) for the given cpu -- just enough for
// macho.NewFile to parse successfully.
func writeMinimalMachO64(t *testing.T, buf *bytes.Buffer, cpu macho.Cpu) {
	t.Helper()
	require.NoError(t, binary.Write(buf, binary.LittleEndian, macho.Magic64))
	require.NoError(t, binary.Write(buf, binary.LittleEndian, cpu))       // Cpu
	require.NoError(t, binary.Write(buf, binary.LittleEndian, uint32(0))) // SubCpu
	require.NoError(t, binary.Write(buf, binary.LittleEndian, uint32(2))) // Type: MH_EXECUTE
	require.NoError(t, binary.Write(buf, binary.LittleEndian, uint32(0))) // Ncmd
	require.NoError(t, binary.Write(buf, binary.LittleEndian, uint32(0))) // Cmdsz
	require.NoError(t, binary.Write(buf, binary.LittleEndian, uint32(0))) // Flags
	require.NoError(t, binary.Write(buf, binary.LittleEndian, uint32(0))) // Reserved (64-bit pad)
}

// TestDetectBinaryArchsFatMachO verifies that detectBinaryArchs parses a
// synthesized fat/universal Mach-O binary via the macho.NewFatFile branch,
// returning one GOARCH-equivalent entry per embedded architecture -- not
// just exercised indirectly through checkArchCompat with a hand-built
// []string.
func TestDetectBinaryArchsFatMachO(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "universal-flasher")

	const (
		fatHeaderSize  = 8  // magic(4) + narch(4)
		fatArchHdrSize = 20 // cpu, subcpu, offset, size, align (5 * uint32)
		archSize       = 32 // fileHeaderSize64
	)
	amd64Offset := uint32(fatHeaderSize + 2*fatArchHdrSize)
	arm64Offset := amd64Offset + archSize

	var buf bytes.Buffer
	require.NoError(t, binary.Write(&buf, binary.BigEndian, uint32(0xcafebabe))) // MagicFat
	require.NoError(t, binary.Write(&buf, binary.BigEndian, uint32(2)))          // narch

	// fat_arch header for amd64
	require.NoError(t, binary.Write(&buf, binary.BigEndian, macho.CpuAmd64))
	require.NoError(t, binary.Write(&buf, binary.BigEndian, uint32(0)))
	require.NoError(t, binary.Write(&buf, binary.BigEndian, amd64Offset))
	require.NoError(t, binary.Write(&buf, binary.BigEndian, uint32(archSize)))
	require.NoError(t, binary.Write(&buf, binary.BigEndian, uint32(0)))

	// fat_arch header for arm64
	require.NoError(t, binary.Write(&buf, binary.BigEndian, macho.CpuArm64))
	require.NoError(t, binary.Write(&buf, binary.BigEndian, uint32(0)))
	require.NoError(t, binary.Write(&buf, binary.BigEndian, arm64Offset))
	require.NoError(t, binary.Write(&buf, binary.BigEndian, uint32(archSize)))
	require.NoError(t, binary.Write(&buf, binary.BigEndian, uint32(0)))

	writeMinimalMachO64(t, &buf, macho.CpuAmd64)
	writeMinimalMachO64(t, &buf, macho.CpuArm64)

	require.NoError(t, os.WriteFile(path, buf.Bytes(), 0o644))

	archs, err := detectBinaryArchs(path)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"amd64", "arm64"}, archs)
}

// TestDetectBinaryArchsPE verifies that detectBinaryArchs parses a
// synthesized minimal PE (COFF) header via the pe.NewFile branch.
func TestDetectBinaryArchsPE(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "flasher.exe")

	var buf bytes.Buffer
	require.NoError(t, binary.Write(&buf, binary.LittleEndian, uint16(pe.IMAGE_FILE_MACHINE_AMD64))) // Machine
	require.NoError(t, binary.Write(&buf, binary.LittleEndian, uint16(0)))                           // NumberOfSections
	require.NoError(t, binary.Write(&buf, binary.LittleEndian, uint32(0)))                           // TimeDateStamp
	require.NoError(t, binary.Write(&buf, binary.LittleEndian, uint32(0)))                           // PointerToSymbolTable
	require.NoError(t, binary.Write(&buf, binary.LittleEndian, uint32(0)))                           // NumberOfSymbols
	require.NoError(t, binary.Write(&buf, binary.LittleEndian, uint16(0)))                           // SizeOfOptionalHeader
	require.NoError(t, binary.Write(&buf, binary.LittleEndian, uint16(0)))                           // Characteristics

	// pe.NewFile reads a 96-byte DOS header stub up front; pad so ReadAt
	// succeeds. First two bytes intentionally aren't "MZ" so it falls back
	// to reading the COFF header directly at offset 0.
	for buf.Len() < 96 {
		buf.WriteByte(0)
	}

	require.NoError(t, os.WriteFile(path, buf.Bytes(), 0o644))

	archs, err := detectBinaryArchs(path)
	require.NoError(t, err)
	assert.Equal(t, []string{"amd64"}, archs)
}

// TestDetectBinaryArchsTruncatedFatMachOProceeds verifies that a truncated
// / garbage fat Mach-O header doesn't panic -- detectBinaryArchs falls
// through to "unrecognized" and callers treat that as unknown/proceed.
func TestDetectBinaryArchsTruncatedFatMachOProceeds(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "truncated-fat")

	var buf bytes.Buffer
	require.NoError(t, binary.Write(&buf, binary.BigEndian, uint32(0xcafebabe))) // MagicFat
	require.NoError(t, binary.Write(&buf, binary.BigEndian, uint32(2)))          // narch: claims 2, but no arch headers follow

	require.NoError(t, os.WriteFile(path, buf.Bytes(), 0o644))

	assert.NotPanics(t, func() {
		archs, err := detectBinaryArchs(path)
		require.Error(t, err)
		assert.Nil(t, archs)
	})
}

// TestArchFromMachoCpu is a table-driven test of the Mach-O CPU type to
// GOARCH mapping.
func TestArchFromMachoCpu(t *testing.T) {
	cases := []struct {
		name   string
		cpu    macho.Cpu
		want   string
		wantOK bool
	}{
		{"amd64", macho.CpuAmd64, "amd64", true},
		{"386", macho.Cpu386, "386", true},
		{"arm", macho.CpuArm, "arm", true},
		{"arm64", macho.CpuArm64, "arm64", true},
		{"unknown", macho.Cpu(0xffff), "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := archFromMachoCpu(c.cpu)
			assert.Equal(t, c.want, got)
			assert.Equal(t, c.wantOK, ok)
		})
	}
}

// TestArchFromElfMachine is a table-driven test of the ELF e_machine value
// to GOARCH mapping.
func TestArchFromElfMachine(t *testing.T) {
	cases := []struct {
		name   string
		m      elf.Machine
		want   string
		wantOK bool
	}{
		{"amd64", elf.EM_X86_64, "amd64", true},
		{"386", elf.EM_386, "386", true},
		{"arm64", elf.EM_AARCH64, "arm64", true},
		{"arm", elf.EM_ARM, "arm", true},
		{"ppc64", elf.EM_PPC64, "ppc64", true},
		{"unknown", elf.EM_SPARC, "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := archFromElfMachine(c.m)
			assert.Equal(t, c.want, got)
			assert.Equal(t, c.wantOK, ok)
		})
	}
}

// TestArchFromPeMachine is a table-driven test of the PE Machine field to
// GOARCH mapping.
func TestArchFromPeMachine(t *testing.T) {
	cases := []struct {
		name   string
		m      uint16
		want   string
		wantOK bool
	}{
		{"amd64", pe.IMAGE_FILE_MACHINE_AMD64, "amd64", true},
		{"386", pe.IMAGE_FILE_MACHINE_I386, "386", true},
		{"arm64", pe.IMAGE_FILE_MACHINE_ARM64, "arm64", true},
		{"arm", pe.IMAGE_FILE_MACHINE_ARMNT, "arm", true},
		{"unknown", 0x9999, "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := archFromPeMachine(c.m)
			assert.Equal(t, c.want, got)
			assert.Equal(t, c.wantOK, ok)
		})
	}
}

// TestFlashRejectsFatalArchMismatchWithoutTouchingPort verifies that when the
// preflight check reports a fatal arch mismatch, Flash() returns the error
// and never stops the port -- i.e. the rejected command leaves port/session
// state exactly as it was before the call (BR-51 HIGH fix). The arch
// decision is injected via preflightFn so the test doesn't depend on a real
// mismatched binary or the actual runtime.GOARCH.
func TestFlashRejectsFatalArchMismatchWithoutTouchingPort(t *testing.T) {
	origPreflight := preflightFn
	preflightFn = func(command string) (string, string, error) {
		return "", "", fmt.Errorf("flasher command %q: binary arch arm64 incompatible with host linux/amd64", command)
	}
	defer func() { preflightFn = origPreflight }()

	var stopCalled bool
	mgr := &wrappedManager{
		original: &mockManager{portName: "test-port", baud: 115200, running: true},
		stopFn: func() error {
			stopCalled = true
			return nil
		},
		startFn: func(port string, baud int) error {
			return nil
		},
	}

	_, err := Flash(mgr, "wrong-arch-flasher", nil, nil, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "incompatible with host")
	assert.False(t, stopCalled, "Stop must not be called when preflight rejects the command")
	assert.True(t, mgr.IsRunning(), "port must remain running when preflight rejects the command")
}

// TestFlashProceedsWithRosettaWarning verifies that a non-fatal (Rosetta-
// style) arch mismatch reported by preflightFn does not block Flash() --
// it proceeds to run the command and prepends the warning to CommandOutput.
func TestFlashProceedsWithRosettaWarning(t *testing.T) {
	origPreflight := preflightFn
	preflightFn = func(command string) (string, string, error) {
		return "/usr/bin/" + command, "binary is amd64; running under Rosetta 2 on darwin/arm64", nil
	}
	defer func() { preflightFn = origPreflight }()

	mgr := &mockManager{portName: "test-port", baud: 115200, running: true}

	result, err := Flash(mgr, "echo", []string{"flash output"}, nil, nil)
	require.NoError(t, err)
	assert.True(t, result.Success)
	assert.Contains(t, result.CommandOutput, "Warning: binary is amd64; running under Rosetta 2")
	assert.Contains(t, result.CommandOutput, "flash output")
}

// TestCheckArchCompat is a table-driven test of the arch-mismatch decision
// logic: exact match, Rosetta-runnable amd64-on-arm64-darwin (warn, not
// fatal), genuine mismatch (fatal), and a fat/universal binary where one of
// several embedded architectures matches the host (proceeds, no warning).
func TestCheckArchCompat(t *testing.T) {
	cases := []struct {
		name        string
		binArchs    []string
		hostOS      string
		hostArch    string
		wantFatal   bool
		wantWarning bool
	}{
		{
			name:      "exact match",
			binArchs:  []string{"amd64"},
			hostOS:    "linux",
			hostArch:  "amd64",
			wantFatal: false,
		},
		{
			name:        "rosetta runnable amd64 on arm64 darwin",
			binArchs:    []string{"amd64"},
			hostOS:      "darwin",
			hostArch:    "arm64",
			wantFatal:   false,
			wantWarning: true,
		},
		{
			name:      "genuine mismatch arm64 binary on amd64 host",
			binArchs:  []string{"arm64"},
			hostOS:    "linux",
			hostArch:  "amd64",
			wantFatal: true,
		},
		{
			name:      "arm binary on arm64 darwin host is not the rosetta case",
			binArchs:  []string{"arm"},
			hostOS:    "darwin",
			hostArch:  "arm64",
			wantFatal: true,
		},
		{
			name:      "fat universal binary matches host among several archs",
			binArchs:  []string{"386", "arm64"},
			hostOS:    "darwin",
			hostArch:  "arm64",
			wantFatal: false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := checkArchCompat(c.binArchs, c.hostOS, c.hostArch)
			assert.Equal(t, c.wantFatal, got.Fatal)
			if c.wantWarning {
				assert.NotEmpty(t, got.Warning)
			}
			if !c.wantFatal && !c.wantWarning {
				assert.Empty(t, got.Warning)
			}
		})
	}
}
