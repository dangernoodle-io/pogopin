package session

import (
	"time"

	goSerial "go.bug.st/serial"
	espflasher "tinygo.org/x/espflasher/pkg/espflasher"
)

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
}

func (m *mockFlasher) FlashImages(images []espflasher.ImagePart, progress espflasher.ProgressFunc) error {
	m.flashImagesCalled = true
	return m.flashImagesErr
}

func (m *mockFlasher) EraseFlash() error {
	m.eraseFlashCalled = true
	return m.eraseFlashErr
}

func (m *mockFlasher) EraseRegion(offset, size uint32) error {
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

func (m *mockFlasher) FlashMD5(offset, size uint32) (string, error) {
	return m.flashMD5Val, m.flashMD5Err
}

func (m *mockFlasher) ReadFlash(offset, size uint32) ([]byte, error) {
	return m.readFlashVal, m.readFlashErr
}

func (m *mockFlasher) FlushInput() {}
