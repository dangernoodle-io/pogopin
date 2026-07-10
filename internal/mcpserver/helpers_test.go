package mcpserver

import (
	"testing"
	"time"

	goSerial "go.bug.st/serial"
	espflasher "tinygo.org/x/espflasher/pkg/espflasher"

	"dangernoodle.io/pogopin/internal/esp"
	"dangernoodle.io/pogopin/internal/serial"
	"dangernoodle.io/pogopin/internal/session"
)

// noopPort implements go.bug.st/serial.Port with all no-op methods.
type noopPort struct{}

func (p *noopPort) Read(b []byte) (int, error) {
	return 0, nil
}

func (p *noopPort) Write(b []byte) (int, error) {
	return len(b), nil
}

func (p *noopPort) Close() error {
	return nil
}

func (p *noopPort) SetMode(mode *goSerial.Mode) error {
	return nil
}

func (p *noopPort) SetReadTimeout(t time.Duration) error {
	return nil
}

func (p *noopPort) SetDTR(dtr bool) error {
	return nil
}

func (p *noopPort) SetRTS(rts bool) error {
	return nil
}

func (p *noopPort) GetModemStatusBits() (*goSerial.ModemStatusBits, error) {
	return &goSerial.ModemStatusBits{}, nil
}

func (p *noopPort) Break(t time.Duration) error {
	return nil
}

func (p *noopPort) Drain() error {
	return nil
}

func (p *noopPort) ResetInputBuffer() error {
	return nil
}

func (p *noopPort) ResetOutputBuffer() error {
	return nil
}

// mockFlasher implements esp.Flasher interface for testing.
type mockFlasher struct {
	flashImagesErr      error
	eraseFlashErr       error
	eraseRegionErr      error
	flashIDErr          error
	flashIDMfg          uint8
	flashIDDev          uint16
	chipTypVal          espflasher.ChipType
	chipNameVal         string
	bootloaderOffsetVal uint32
	bootloaderOffsetOK  bool
	resetCalled         bool
	closeCalled         bool
	flashImagesCalled   bool
	eraseFlashCalled    bool
	eraseRegionCalled   bool
	eraseRegionOffset   uint32
	eraseRegionSize     uint32
	readRegisterErr     error
	readRegisterVal     uint32
	writeRegisterErr    error
	writeRegisterAddr   uint32
	writeRegisterVal    uint32
	getSecurityInfoErr  error
	getSecurityInfoVal  *espflasher.SecurityInfo
	flashMD5Err         error
	flashMD5Val         string
	readFlashErr        error
	readFlashVal        []byte
	flashImagesData     []espflasher.ImagePart

	// readFlashPostWriteOverride, if non-nil, is returned by ReadFlash for
	// every call after FlashImages has been called, instead of the
	// just-flashed data. Lets tests simulate a device whose post-write state
	// doesn't match what was written (verify-failure paths for NVS RMW).
	readFlashPostWriteOverride []byte

	// flashImagesProgress, eraseFlashProgress, and readFlashProgress, when
	// set, are invoked with the real progress.ProgressFunc handed down by
	// esp.FlashESP/EraseESP/ReadFlashData so tests can drive a controlled
	// progress sequence through the actual callback chain (used by
	// progress_notify_test.go).
	flashImagesProgress func(progress espflasher.ProgressFunc)
	eraseFlashProgress  func(progress espflasher.ProgressFunc)
	readFlashProgress   func(progress espflasher.ProgressFunc)
	flashMD5Progress    func(progress espflasher.ProgressFunc)

	// GPIO fields. readGPIOVal/readGPIOErr drive ReadGPIO's return.
	// setGPIOErr, when set, is returned by every SetGPIO call (e.g. to
	// simulate a reserved/input-only pin refusal); setGPIOCalls records
	// every call for assertions. releaseGPIOCalls records every
	// ReleaseGPIO call. gpioReservedFunc, when non-nil, backs GPIOReserved;
	// otherwise every pin reports not-reserved.
	readGPIOVal      bool
	readGPIOErr      error
	setGPIOErr       error
	setGPIOCalls     []mockGPIOCall
	releaseGPIOCalls []int
	gpioReservedFunc func(pin int) (bool, string)
}

// mockGPIOCall records a single SetGPIO invocation on mockFlasher.
type mockGPIOCall struct {
	pin   int
	level bool
}

func (m *mockFlasher) FlashImages(images []espflasher.ImagePart, progress espflasher.ProgressFunc) error {
	m.flashImagesCalled = true
	m.flashImagesData = images
	if m.flashImagesProgress != nil {
		m.flashImagesProgress(progress)
	}
	return m.flashImagesErr
}

func (m *mockFlasher) EraseFlash(progress espflasher.ProgressFunc) error {
	m.eraseFlashCalled = true
	if m.eraseFlashProgress != nil {
		m.eraseFlashProgress(progress)
	}
	return m.eraseFlashErr
}

func (m *mockFlasher) EraseRegion(offset, size uint32, progress espflasher.ProgressFunc) error {
	m.eraseRegionCalled = true
	m.eraseRegionOffset = offset
	m.eraseRegionSize = size
	return m.eraseRegionErr
}

func (m *mockFlasher) FlashID() (uint8, uint16, error) {
	return m.flashIDMfg, m.flashIDDev, m.flashIDErr
}

func (m *mockFlasher) ChipType() espflasher.ChipType {
	return m.chipTypVal
}

func (m *mockFlasher) ChipName() string {
	return m.chipNameVal
}

func (m *mockFlasher) BootloaderFlashOffset() (uint32, bool) {
	return m.bootloaderOffsetVal, m.bootloaderOffsetOK
}

func (m *mockFlasher) Reset() {
	m.resetCalled = true
}

func (m *mockFlasher) Close() error {
	m.closeCalled = true
	return nil
}

func (m *mockFlasher) ReadRegister(address uint32) (uint32, error) {
	return m.readRegisterVal, m.readRegisterErr
}

func (m *mockFlasher) WriteRegister(address, value uint32) error {
	m.writeRegisterAddr = address
	m.writeRegisterVal = value
	return m.writeRegisterErr
}

func (m *mockFlasher) GetSecurityInfo() (*espflasher.SecurityInfo, error) {
	return m.getSecurityInfoVal, m.getSecurityInfoErr
}

func (m *mockFlasher) GetFlashMD5(offset, size uint32, progress espflasher.ProgressFunc) (string, error) {
	if m.flashMD5Progress != nil {
		m.flashMD5Progress(progress)
	}
	return m.flashMD5Val, m.flashMD5Err
}

func (m *mockFlasher) ReadFlash(offset, size uint32, progress espflasher.ProgressFunc) ([]byte, error) {
	if m.readFlashProgress != nil {
		m.readFlashProgress(progress)
	}
	if m.readFlashErr != nil {
		return nil, m.readFlashErr
	}
	if m.flashImagesCalled {
		if m.readFlashPostWriteOverride != nil {
			return m.readFlashPostWriteOverride, nil
		}
		// Simulate a real device: serve back the bytes that were actually
		// flashed to this offset, so post-write verification observes the
		// genuine round trip instead of stale pre-write data.
		for _, img := range m.flashImagesData {
			if img.Offset == offset && uint32(len(img.Data)) >= size {
				return img.Data[:size], nil
			}
		}
	}
	return m.readFlashVal, m.readFlashErr
}

func (m *mockFlasher) FlushInput() {
}

func (m *mockFlasher) ReadGPIO(pin int) (bool, error) {
	return m.readGPIOVal, m.readGPIOErr
}

func (m *mockFlasher) SetGPIO(pin int, level bool) error {
	m.setGPIOCalls = append(m.setGPIOCalls, mockGPIOCall{pin: pin, level: level})
	return m.setGPIOErr
}

func (m *mockFlasher) ReleaseGPIO(pin int) error {
	m.releaseGPIOCalls = append(m.releaseGPIOCalls, pin)
	return nil
}

func (m *mockFlasher) GPIOReserved(pin int) (bool, string) {
	if m.gpioReservedFunc != nil {
		return m.gpioReservedFunc(pin)
	}
	return false, ""
}

// mockESPFlasher is a simplified flasher that tracks only resetCalled.
type mockESPFlasher struct {
	resetCalled bool
}

func (m *mockESPFlasher) FlashImages(images []espflasher.ImagePart, progress espflasher.ProgressFunc) error {
	return nil
}

func (m *mockESPFlasher) EraseFlash(progress espflasher.ProgressFunc) error {
	return nil
}

func (m *mockESPFlasher) EraseRegion(offset, size uint32, progress espflasher.ProgressFunc) error {
	return nil
}

func (m *mockESPFlasher) FlashID() (uint8, uint16, error) {
	return 0, 0, nil
}

func (m *mockESPFlasher) ChipType() espflasher.ChipType {
	return 0
}

func (m *mockESPFlasher) ChipName() string {
	return "test-chip"
}

func (m *mockESPFlasher) BootloaderFlashOffset() (uint32, bool) {
	return 0, false
}

func (m *mockESPFlasher) Reset() {
	m.resetCalled = true
}

func (m *mockESPFlasher) Close() error {
	return nil
}

func (m *mockESPFlasher) ReadRegister(address uint32) (uint32, error) {
	return 0, nil
}

func (m *mockESPFlasher) WriteRegister(address, value uint32) error {
	return nil
}

func (m *mockESPFlasher) GetSecurityInfo() (*espflasher.SecurityInfo, error) {
	return &espflasher.SecurityInfo{}, nil
}

func (m *mockESPFlasher) GetFlashMD5(offset, size uint32, progress espflasher.ProgressFunc) (string, error) {
	return "", nil
}

func (m *mockESPFlasher) ReadFlash(offset, size uint32, progress espflasher.ProgressFunc) ([]byte, error) {
	return nil, nil
}

func (m *mockESPFlasher) FlushInput() {
}

func (m *mockESPFlasher) ReadGPIO(pin int) (bool, error) {
	return false, nil
}

func (m *mockESPFlasher) SetGPIO(pin int, level bool) error {
	return nil
}

func (m *mockESPFlasher) ReleaseGPIO(pin int) error {
	return nil
}

func (m *mockESPFlasher) GPIOReserved(pin int) (bool, string) {
	return false, ""
}

// setupTestPorts sets up an empty ports map for testing.
func setupTestPorts(t *testing.T) {
	orig := session.SetPorts(map[string]*session.PortSession{})
	t.Cleanup(func() {
		session.CleanupAllSessions()
		session.SetPorts(orig)
	})
}

// setupTestFlasherFactory sets up the flasher factory for testing.
func setupTestFlasherFactory(t *testing.T) {
	orig := session.SetFlasherFactory(esp.DefaultFlasherFactory)
	t.Cleanup(func() { session.SetFlasherFactory(orig) })
	origDelay := session.SetSyncRetryDelay(time.Millisecond)
	t.Cleanup(func() { session.SetSyncRetryDelay(origDelay) })
}

// setupTestManagersFunc sets up the manager factory for testing.
func setupTestManagersFunc(t *testing.T) {
	orig := session.SetNewManagerFunc(func(bufSize int) *serial.Manager {
		return serial.NewManagerWithBufferSize(bufSize)
	})
	t.Cleanup(func() { session.SetNewManagerFunc(orig) })
}

// setupTestListPorts sets up the list ports function for testing.
func setupTestListPorts(t *testing.T) {
	orig := session.SetListPortsFn(serial.ListPorts)
	t.Cleanup(func() { session.SetListPortsFn(orig) })
}

// setupTestIsUSBPort sets up the USB port detection function for testing.
func setupTestIsUSBPort(t *testing.T) {
	orig := session.SetIsUSBPortFn(func(port string) bool {
		return len(port) > 7 && port[:7] == "/dev/cu"
	})
	t.Cleanup(func() { session.SetIsUSBPortFn(orig) })
}

// setupFastWaitForPort sets up a fast wait interval for port detection.
func setupFastWaitForPort(t *testing.T) {
	orig := session.SetWaitForPortInterval(time.Millisecond)
	t.Cleanup(func() { session.SetWaitForPortInterval(orig) })
}

// setupFastBootCapture sets up a no-op boot capture wait function.
func setupFastBootCapture(t *testing.T) {
	orig := bootCaptureWait
	bootCaptureWait = func(d time.Duration) {}
	t.Cleanup(func() { bootCaptureWait = orig })
}
