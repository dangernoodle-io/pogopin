// Package testutil holds test-only fixtures shared across the shesha
// capability packages (internal/capability/esp, internal/capability/flash,
// and friends): a fake esp.Flasher, a no-op go.bug.st/serial.Port, and the
// session/serial package-global setup helpers every handler test needs.
// This is the MC-12 port of internal/mcpserver/helpers_test.go's
// mockFlasher/noopPort/setupTest* fixtures onto a package the split
// capability packages can all import (a _test.go file can't be imported
// across package boundaries, hence this lives as a plain, non-_test.go
// file). Not used by any production code path.
package testutil

import (
	"strings"
	"testing"
	"time"

	"github.com/dangernoodle-io/shesha"
	"github.com/dangernoodle-io/shesha/host/generic"
	"github.com/dangernoodle-io/shesha/testkit"
	goSerial "go.bug.st/serial"
	espflasher "tinygo.org/x/espflasher/pkg/espflasher"

	"dangernoodle.io/pogopin/internal/esp"
	"dangernoodle.io/pogopin/internal/serial"
	"dangernoodle.io/pogopin/internal/session"
)

// progressPollInterval and progressWaitTimeout bound WaitForProgressComplete's
// poll loop. The interval is deliberately tight (progress delivery is
// in-memory and normally sub-millisecond); the timeout is a generous safety
// net so a genuine regression (no completion tick ever delivered) still
// fails the test instead of hanging, rather than governing the common case.
const (
	progressPollInterval = time.Millisecond
	progressWaitTimeout  = 5 * time.Second
)

// NoopPort implements go.bug.st/serial.Port with all no-op methods,
// mirroring internal/mcpserver/helpers_test.go's fixture of the same name.
type NoopPort struct{}

func (p *NoopPort) Read(b []byte) (int, error)  { return 0, nil }
func (p *NoopPort) Write(b []byte) (int, error) { return len(b), nil }
func (p *NoopPort) Close() error                { return nil }
func (p *NoopPort) SetMode(mode *goSerial.Mode) error {
	return nil
}
func (p *NoopPort) SetReadTimeout(t time.Duration) error { return nil }
func (p *NoopPort) SetDTR(dtr bool) error                { return nil }
func (p *NoopPort) SetRTS(rts bool) error                { return nil }
func (p *NoopPort) GetModemStatusBits() (*goSerial.ModemStatusBits, error) {
	return &goSerial.ModemStatusBits{}, nil
}
func (p *NoopPort) Break(t time.Duration) error { return nil }
func (p *NoopPort) Drain() error                { return nil }
func (p *NoopPort) ResetInputBuffer() error     { return nil }
func (p *NoopPort) ResetOutputBuffer() error    { return nil }

// GPIOCall records a single SetGPIO invocation on MockFlasher.
type GPIOCall struct {
	Pin   int
	Level bool
}

// MockFlasher implements esp.Flasher for capability-layer handler tests.
// Exported port of internal/mcpserver/helpers_test.go's mockFlasher.
type MockFlasher struct {
	FlashImagesErr      error
	EraseFlashErr       error
	EraseRegionErr      error
	FlashIDErr          error
	FlashIDMfg          uint8
	FlashIDDev          uint16
	ChipTypeVal         espflasher.ChipType
	ChipNameVal         string
	BootloaderOffsetVal uint32
	BootloaderOffsetOK  bool
	ResetCalled         bool
	CloseCalled         bool
	FlashImagesCalled   bool
	EraseFlashCalled    bool
	EraseRegionCalled   bool
	EraseRegionOffset   uint32
	EraseRegionSize     uint32
	ReadRegisterErr     error
	ReadRegisterVal     uint32
	WriteRegisterErr    error
	WriteRegisterAddr   uint32
	WriteRegisterVal    uint32
	GetSecurityInfoErr  error
	GetSecurityInfoVal  *espflasher.SecurityInfo
	FlashMD5Err         error
	FlashMD5Val         string
	ReadFlashErr        error
	ReadFlashVal        []byte
	FlashImagesData     []espflasher.ImagePart

	// ReadFlashPostWriteOverride, if non-nil, is returned by ReadFlash for
	// every call after FlashImages has been called, instead of the
	// just-flashed data. Lets tests simulate a device whose post-write
	// state doesn't match what was written (verify-failure paths for NVS
	// RMW).
	ReadFlashPostWriteOverride []byte

	// FlashImagesProgress, EraseFlashProgress, ReadFlashProgress, and
	// FlashMD5Progress, when set, are invoked with the real
	// espflasher.ProgressFunc handed down by esp.FlashESP/EraseESP/
	// ReadFlashData/GetFlashMD5 so tests can drive a controlled progress
	// sequence through the actual callback chain.
	FlashImagesProgress func(progress espflasher.ProgressFunc)
	EraseFlashProgress  func(progress espflasher.ProgressFunc)
	ReadFlashProgress   func(progress espflasher.ProgressFunc)
	FlashMD5Progress    func(progress espflasher.ProgressFunc)

	// GPIO fields. ReadGPIOVal/ReadGPIOErr drive ReadGPIO's return.
	// SetGPIOErr, when set, is returned by every SetGPIO call (e.g. to
	// simulate a reserved/input-only pin refusal); SetGPIOCalls records
	// every call for assertions. ReleaseGPIOCalls records every
	// ReleaseGPIO call. GPIOReservedFunc, when non-nil, backs GPIOReserved;
	// otherwise every pin reports not-reserved.
	ReadGPIOVal      bool
	ReadGPIOErr      error
	SetGPIOErr       error
	SetGPIOCalls     []GPIOCall
	ReleaseGPIOCalls []int
	GPIOReservedFunc func(pin int) (bool, string)
}

func (m *MockFlasher) FlashImages(images []espflasher.ImagePart, progress espflasher.ProgressFunc) error {
	m.FlashImagesCalled = true
	m.FlashImagesData = images
	if m.FlashImagesProgress != nil {
		m.FlashImagesProgress(progress)
	}
	return m.FlashImagesErr
}

func (m *MockFlasher) EraseFlash(progress espflasher.ProgressFunc) error {
	m.EraseFlashCalled = true
	if m.EraseFlashProgress != nil {
		m.EraseFlashProgress(progress)
	}
	return m.EraseFlashErr
}

func (m *MockFlasher) EraseRegion(offset, size uint32, progress espflasher.ProgressFunc) error {
	m.EraseRegionCalled = true
	m.EraseRegionOffset = offset
	m.EraseRegionSize = size
	return m.EraseRegionErr
}

func (m *MockFlasher) FlashID() (uint8, uint16, error) {
	return m.FlashIDMfg, m.FlashIDDev, m.FlashIDErr
}

func (m *MockFlasher) ChipType() espflasher.ChipType { return m.ChipTypeVal }

func (m *MockFlasher) ChipName() string { return m.ChipNameVal }

func (m *MockFlasher) BootloaderFlashOffset() (uint32, bool) {
	return m.BootloaderOffsetVal, m.BootloaderOffsetOK
}

func (m *MockFlasher) Reset() { m.ResetCalled = true }

func (m *MockFlasher) Close() error {
	m.CloseCalled = true
	return nil
}

func (m *MockFlasher) ReadRegister(address uint32) (uint32, error) {
	return m.ReadRegisterVal, m.ReadRegisterErr
}

func (m *MockFlasher) WriteRegister(address, value uint32) error {
	m.WriteRegisterAddr = address
	m.WriteRegisterVal = value
	return m.WriteRegisterErr
}

func (m *MockFlasher) GetSecurityInfo() (*espflasher.SecurityInfo, error) {
	return m.GetSecurityInfoVal, m.GetSecurityInfoErr
}

func (m *MockFlasher) GetFlashMD5(offset, size uint32, progress espflasher.ProgressFunc) (string, error) {
	if m.FlashMD5Progress != nil {
		m.FlashMD5Progress(progress)
	}
	return m.FlashMD5Val, m.FlashMD5Err
}

func (m *MockFlasher) ReadFlash(offset, size uint32, progress espflasher.ProgressFunc) ([]byte, error) {
	if m.ReadFlashProgress != nil {
		m.ReadFlashProgress(progress)
	}
	if m.ReadFlashErr != nil {
		return nil, m.ReadFlashErr
	}
	if m.FlashImagesCalled {
		if m.ReadFlashPostWriteOverride != nil {
			return m.ReadFlashPostWriteOverride, nil
		}
		// Simulate a real device: serve back the bytes that were actually
		// flashed to this offset, so post-write verification observes the
		// genuine round trip instead of stale pre-write data.
		for _, img := range m.FlashImagesData {
			if img.Offset == offset && uint32(len(img.Data)) >= size {
				return img.Data[:size], nil
			}
		}
	}
	return m.ReadFlashVal, m.ReadFlashErr
}

func (m *MockFlasher) FlushInput() {}

func (m *MockFlasher) ReadGPIO(pin int) (bool, error) {
	return m.ReadGPIOVal, m.ReadGPIOErr
}

func (m *MockFlasher) SetGPIO(pin int, level bool) error {
	m.SetGPIOCalls = append(m.SetGPIOCalls, GPIOCall{Pin: pin, Level: level})
	return m.SetGPIOErr
}

func (m *MockFlasher) ReleaseGPIO(pin int) error {
	m.ReleaseGPIOCalls = append(m.ReleaseGPIOCalls, pin)
	return nil
}

func (m *MockFlasher) GPIOReserved(pin int) (bool, string) {
	if m.GPIOReservedFunc != nil {
		return m.GPIOReservedFunc(pin)
	}
	return false, ""
}

// SetupTestPorts sets up an empty ports map for testing.
func SetupTestPorts(t *testing.T) {
	t.Helper()
	orig := session.SetPorts(map[string]*session.PortSession{})
	t.Cleanup(func() {
		session.CleanupAllSessions()
		session.SetPorts(orig)
	})
}

// SetupTestFlasherFactory sets up the flasher factory for testing (the
// real esp.DefaultFlasherFactory, for tests that swap it again per-case)
// plus a fast sync-retry delay.
func SetupTestFlasherFactory(t *testing.T) {
	t.Helper()
	orig := session.SetFlasherFactory(esp.DefaultFlasherFactory)
	t.Cleanup(func() { session.SetFlasherFactory(orig) })
	origDelay := session.SetSyncRetryDelay(time.Millisecond)
	t.Cleanup(func() { session.SetSyncRetryDelay(origDelay) })
}

// SetupTestManagersFunc sets up the manager factory for testing.
func SetupTestManagersFunc(t *testing.T) {
	t.Helper()
	orig := session.SetNewManagerFunc(func(bufSize int) *serial.Manager {
		return serial.NewManagerWithBufferSize(bufSize)
	})
	t.Cleanup(func() { session.SetNewManagerFunc(orig) })
}

// SetupTestListPorts sets up the list ports function for testing.
func SetupTestListPorts(t *testing.T) {
	t.Helper()
	orig := session.SetListPortsFn(serial.ListPorts)
	t.Cleanup(func() { session.SetListPortsFn(orig) })
}

// SetupTestIsUSBPort sets up the USB port detection function for testing.
func SetupTestIsUSBPort(t *testing.T) {
	t.Helper()
	orig := session.SetIsUSBPortFn(func(port string) bool {
		return len(port) > 7 && port[:7] == "/dev/cu"
	})
	t.Cleanup(func() { session.SetIsUSBPortFn(orig) })
}

// SetupFastWaitForPort sets up a fast wait interval for port detection.
func SetupFastWaitForPort(t *testing.T) {
	t.Helper()
	orig := session.SetWaitForPortInterval(time.Millisecond)
	t.Cleanup(func() { session.SetWaitForPortInterval(orig) })
}

// NewHarness composes a minimal shesha App around caps alone and returns a
// ready testkit.Harness, mirroring internal/capability/serial's
// package-local newHarness so esp/flash capability tests can exercise real
// *mcpx.CallToolRequest values instead of hand-built zero-value requests.
func NewHarness(t *testing.T, caps ...shesha.Capability) *testkit.Harness {
	t.Helper()
	app, err := shesha.New(shesha.Info{Name: "capability-test", Version: "0.0.0"}, generic.New(), caps...)
	if err != nil {
		t.Fatalf("shesha.New: %v", err)
	}
	return testkit.New(t, app)
}

// WaitForProgressComplete polls h.ProgressEvents(token) until the most
// recently recorded event's message contains "complete: "+toolName -- the
// terminal tick every shesha capability handler emits via
// mcpprogress.LifecycleStatus's completion closure, always the last progress
// notification a handler sends before returning -- or progressWaitTimeout
// elapses, then returns the snapshot.
//
// This exists because testkit.Harness records progress notifications via an
// OnProgress callback invoked on the client's async receive goroutine
// (github.com/dangernoodle-io/shesha/testkit's Harness.recordProgress):
// CallTool/CallToolWithProgressToken returns once the RPC result is decoded,
// which is not guaranteed to happen-after the receive goroutine has
// processed every progress notification sent alongside it. Reading
// ProgressEvents(token) immediately after the call returns races that
// delivery -- harmless on a fast, uncontended machine (the receive goroutine
// usually wins the race), but a genuine flake under CI's slower/contended
// runners where the scheduler can delay it. Polling on the actual terminal
// marker synchronizes the test on what it asserts, rather than on wall-clock
// timing.
func WaitForProgressComplete(t *testing.T, h *testkit.Harness, token any, toolName string) []testkit.ProgressEvent {
	t.Helper()
	marker := "complete: " + toolName
	deadline := time.Now().Add(progressWaitTimeout)
	for {
		events := h.ProgressEvents(token)
		if n := len(events); n > 0 && strings.Contains(events[n-1].Message, marker) {
			return events
		}
		if time.Now().After(deadline) {
			return events
		}
		time.Sleep(progressPollInterval)
	}
}
