package serial

import (
	"testing"
	"time"

	"github.com/dangernoodle-io/shesha"
	"github.com/dangernoodle-io/shesha/host/generic"
	"github.com/dangernoodle-io/shesha/testkit"
	"github.com/stretchr/testify/require"
	goSerial "go.bug.st/serial"
	espflasher "tinygo.org/x/espflasher/pkg/espflasher"

	"dangernoodle.io/pogopin/internal/serial"
	"dangernoodle.io/pogopin/internal/session"
)

// newHarness composes a minimal shesha App around c alone (no serial/esp/
// flash/decode composition — that's mcpapp's job) and returns a ready
// testkit.Harness, so serial capability tests exercise real
// *mcpx.CallToolRequest values (valid Params, real progress-token plumbing)
// instead of hand-built zero-value requests, which panic on nil Params.
func newHarness(t *testing.T, c *Capability) *testkit.Harness {
	t.Helper()
	app, err := shesha.New(shesha.Info{Name: "serial-test", Version: "0.0.0"}, generic.New(), c)
	require.NoError(t, err)
	return testkit.New(t, app)
}

// noopPort implements go.bug.st/serial.Port with all no-op methods, mirroring
// internal/mcpserver/helpers_test.go's fixture of the same name.
type noopPort struct{}

func (p *noopPort) Read(b []byte) (int, error)  { return 0, nil }
func (p *noopPort) Write(b []byte) (int, error) { return len(b), nil }
func (p *noopPort) Close() error                { return nil }
func (p *noopPort) SetMode(mode *goSerial.Mode) error {
	return nil
}
func (p *noopPort) SetReadTimeout(t time.Duration) error { return nil }
func (p *noopPort) SetDTR(dtr bool) error                { return nil }
func (p *noopPort) SetRTS(rts bool) error                { return nil }
func (p *noopPort) GetModemStatusBits() (*goSerial.ModemStatusBits, error) {
	return &goSerial.ModemStatusBits{}, nil
}
func (p *noopPort) Break(t time.Duration) error { return nil }
func (p *noopPort) Drain() error                { return nil }
func (p *noopPort) ResetInputBuffer() error     { return nil }
func (p *noopPort) ResetOutputBuffer() error    { return nil }

// setupTestPorts sets up an empty ports map for testing.
func setupTestPorts(t *testing.T) {
	t.Helper()
	orig := session.SetPorts(map[string]*session.PortSession{})
	t.Cleanup(func() {
		session.CleanupAllSessions()
		session.SetPorts(orig)
	})
}

// setupTestManagersFunc sets up the manager factory for testing.
func setupTestManagersFunc(t *testing.T) {
	t.Helper()
	orig := session.SetNewManagerFunc(func(bufSize int) *serial.Manager {
		return serial.NewManagerWithBufferSize(bufSize)
	})
	t.Cleanup(func() { session.SetNewManagerFunc(orig) })
}

// resetMockFlasher implements esp.Flasher with harmless zero-value stubs,
// mirroring internal/mcpapp/esp_smoke_test.go's smokeMockFlasher — only
// Reset/Close need to be no-op-safe for the startSessionWithAutoReset dance
// exercised by TestHandleSerialStartAutoResetPortUnchanged/PortChanged.
type resetMockFlasher struct{}

func (m *resetMockFlasher) FlashImages(images []espflasher.ImagePart, progress espflasher.ProgressFunc) error {
	return nil
}
func (m *resetMockFlasher) EraseFlash(progress espflasher.ProgressFunc) error { return nil }
func (m *resetMockFlasher) EraseRegion(offset, size uint32, progress espflasher.ProgressFunc) error {
	return nil
}
func (m *resetMockFlasher) FlashID() (uint8, uint16, error)       { return 0, 0, nil }
func (m *resetMockFlasher) ChipType() espflasher.ChipType         { return espflasher.ChipAuto }
func (m *resetMockFlasher) ChipName() string                      { return "esp32-reset-mock" }
func (m *resetMockFlasher) BootloaderFlashOffset() (uint32, bool) { return 0, false }
func (m *resetMockFlasher) Reset()                                {}
func (m *resetMockFlasher) Close() error                          { return nil }
func (m *resetMockFlasher) ReadRegister(address uint32) (uint32, error) {
	return 0, nil
}
func (m *resetMockFlasher) WriteRegister(address, value uint32) error { return nil }
func (m *resetMockFlasher) ReadGPIO(pin int) (bool, error)            { return false, nil }
func (m *resetMockFlasher) SetGPIO(pin int, level bool) error         { return nil }
func (m *resetMockFlasher) ReleaseGPIO(pin int) error                 { return nil }
func (m *resetMockFlasher) GPIOReserved(pin int) (bool, string)       { return false, "" }
func (m *resetMockFlasher) GetSecurityInfo() (*espflasher.SecurityInfo, error) {
	return &espflasher.SecurityInfo{}, nil
}
func (m *resetMockFlasher) GetFlashMD5(offset, size uint32, progress espflasher.ProgressFunc) (string, error) {
	return "", nil
}
func (m *resetMockFlasher) ReadFlash(offset, size uint32, progress espflasher.ProgressFunc) ([]byte, error) {
	return nil, nil
}
func (m *resetMockFlasher) FlushInput() {}
