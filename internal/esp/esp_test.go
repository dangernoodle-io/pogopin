package esp

import (
	"encoding/binary"
	"fmt"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	espflasher "tinygo.org/x/espflasher/pkg/espflasher"
	"tinygo.org/x/espflasher/pkg/nvs"
)

// mockFlasher implements the Flasher interface for testing.
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
}

func (m *mockFlasher) FlashImages(images []espflasher.ImagePart, progress espflasher.ProgressFunc) error {
	m.flashImagesCalled = true
	m.flashImagesData = images
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

func (m *mockFlasher) GetFlashMD5(offset, size uint32) (string, error) {
	return m.flashMD5Val, m.flashMD5Err
}

func (m *mockFlasher) ReadFlash(offset, size uint32) ([]byte, error) {
	return m.readFlashVal, m.readFlashErr
}

func (m *mockFlasher) FlushInput() {}

func TestFlashESPSuccess(t *testing.T) {
	// Create temp files for testing
	tmpDir := t.TempDir()
	fw1 := tmpDir + "/fw1.bin"
	fw2 := tmpDir + "/fw2.bin"

	err := os.WriteFile(fw1, []byte("firmware1"), 0644)
	require.NoError(t, err)
	err = os.WriteFile(fw2, []byte("firmware2"), 0644)
	require.NoError(t, err)

	mock := &mockFlasher{
		chipNameVal: "ESP32",
	}
	factory := func(port string, opts *espflasher.FlasherOptions) (Flasher, error) {
		return mock, nil
	}

	result, err := FlashESP(factory, "/dev/ttyUSB0", []ImageSpec{
		{Path: fw1, Offset: 0x1000},
		{Path: fw2, Offset: 0x5000},
	}, FlashOptions{})

	require.NoError(t, err)
	assert.Equal(t, 9+9, result.BytesWritten)
	assert.True(t, mock.flashImagesCalled)
	assert.True(t, mock.resetCalled)
	assert.True(t, mock.closeCalled)
}

func TestFlashESPMissingFile(t *testing.T) {
	mock := &mockFlasher{}
	factory := func(port string, opts *espflasher.FlasherOptions) (Flasher, error) {
		return mock, nil
	}

	_, err := FlashESP(factory, "/dev/ttyUSB0", []ImageSpec{
		{Path: "/nonexistent/file.bin", Offset: 0x1000},
	}, FlashOptions{})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to read image")
}

func TestFlashESPFactoryError(t *testing.T) {
	factory := func(port string, opts *espflasher.FlasherOptions) (Flasher, error) {
		return nil, os.ErrPermission
	}

	_, err := FlashESP(factory, "/dev/ttyUSB0", []ImageSpec{}, FlashOptions{})
	require.Error(t, err)
}

func TestFlashESPFlashError(t *testing.T) {
	tmpDir := t.TempDir()
	fw := tmpDir + "/fw.bin"
	err := os.WriteFile(fw, []byte("data"), 0644)
	require.NoError(t, err)

	mock := &mockFlasher{
		flashImagesErr: os.ErrPermission,
	}
	factory := func(port string, opts *espflasher.FlasherOptions) (Flasher, error) {
		return mock, nil
	}

	_, err = FlashESP(factory, "/dev/ttyUSB0", []ImageSpec{
		{Path: fw, Offset: 0},
	}, FlashOptions{})

	require.Error(t, err)
}

func TestFlashESPBaudRateDefaults(t *testing.T) {
	tmpDir := t.TempDir()
	fw := tmpDir + "/fw.bin"
	err := os.WriteFile(fw, []byte("data"), 0644)
	require.NoError(t, err)

	var capturedOpts *espflasher.FlasherOptions
	mock := &mockFlasher{}
	factory := func(port string, opts *espflasher.FlasherOptions) (Flasher, error) {
		capturedOpts = opts
		return mock, nil
	}

	_, err = FlashESP(factory, "/dev/ttyUSB0", []ImageSpec{
		{Path: fw, Offset: 0},
	}, FlashOptions{})

	require.NoError(t, err)
	assert.Equal(t, 115200, capturedOpts.BaudRate)
	assert.Equal(t, 460800, capturedOpts.FlashBaudRate)
}

func TestFlashESPCustomBaudRate(t *testing.T) {
	tmpDir := t.TempDir()
	fw := tmpDir + "/fw.bin"
	err := os.WriteFile(fw, []byte("data"), 0644)
	require.NoError(t, err)

	var capturedOpts *espflasher.FlasherOptions
	mock := &mockFlasher{}
	factory := func(port string, opts *espflasher.FlasherOptions) (Flasher, error) {
		capturedOpts = opts
		return mock, nil
	}

	_, err = FlashESP(factory, "/dev/ttyUSB0", []ImageSpec{
		{Path: fw, Offset: 0},
	}, FlashOptions{
		BaudRate:      9600,
		FlashBaudRate: 230400,
	})

	require.NoError(t, err)
	assert.Equal(t, 9600, capturedOpts.BaudRate)
	assert.Equal(t, 230400, capturedOpts.FlashBaudRate)
}

func TestEraseESPWholeChip(t *testing.T) {
	mock := &mockFlasher{}
	factory := func(port string, opts *espflasher.FlasherOptions) (Flasher, error) {
		return mock, nil
	}

	err := EraseESP(factory, "/dev/ttyUSB0", EraseOptions{})
	require.NoError(t, err)
	assert.True(t, mock.eraseFlashCalled)
	assert.False(t, mock.eraseRegionCalled)
	assert.True(t, mock.closeCalled)
}

func TestEraseESPRegion(t *testing.T) {
	offset := uint32(0x1000)
	size := uint32(0x1000)

	mock := &mockFlasher{}
	factory := func(port string, opts *espflasher.FlasherOptions) (Flasher, error) {
		return mock, nil
	}

	err := EraseESP(factory, "/dev/ttyUSB0", EraseOptions{
		Offset: &offset,
		Size:   &size,
	})

	require.NoError(t, err)
	assert.False(t, mock.eraseFlashCalled)
	assert.True(t, mock.eraseRegionCalled)
	assert.Equal(t, offset, mock.eraseRegionOffset)
	assert.Equal(t, size, mock.eraseRegionSize)
	assert.True(t, mock.closeCalled)
}

func TestEraseESPRegionMissingSize(t *testing.T) {
	offset := uint32(0x1000)

	mock := &mockFlasher{}
	factory := func(port string, opts *espflasher.FlasherOptions) (Flasher, error) {
		return mock, nil
	}

	err := EraseESP(factory, "/dev/ttyUSB0", EraseOptions{
		Offset: &offset,
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "offset and size")
}

func TestEraseESPFactoryError(t *testing.T) {
	factory := func(port string, opts *espflasher.FlasherOptions) (Flasher, error) {
		return nil, os.ErrPermission
	}

	err := EraseESP(factory, "/dev/ttyUSB0", EraseOptions{})
	require.Error(t, err)
}

func TestEraseESPEraseError(t *testing.T) {
	mock := &mockFlasher{
		eraseFlashErr: os.ErrPermission,
	}
	factory := func(port string, opts *espflasher.FlasherOptions) (Flasher, error) {
		return mock, nil
	}

	err := EraseESP(factory, "/dev/ttyUSB0", EraseOptions{})
	require.Error(t, err)
}

func TestGetChipInfoSuccess(t *testing.T) {
	mock := &mockFlasher{
		chipNameVal: "ESP32-S3",
		flashIDMfg:  0x20,
		flashIDDev:  0x0060,
	}
	factory := func(port string, opts *espflasher.FlasherOptions) (Flasher, error) {
		return mock, nil
	}

	result, err := GetChipInfo(factory, "/dev/ttyUSB0", 0, "")
	require.NoError(t, err)
	assert.Equal(t, "ESP32-S3", result.ChipName)
	assert.Equal(t, uint8(0x20), result.ManufacturerID)
	assert.Equal(t, uint16(0x0060), result.DeviceID)
	assert.True(t, mock.closeCalled)
}

func TestGetChipInfoBaudDefault(t *testing.T) {
	var capturedOpts *espflasher.FlasherOptions
	mock := &mockFlasher{
		chipNameVal: "ESP32",
	}
	factory := func(port string, opts *espflasher.FlasherOptions) (Flasher, error) {
		capturedOpts = opts
		return mock, nil
	}

	_, err := GetChipInfo(factory, "/dev/ttyUSB0", 0, "")
	require.NoError(t, err)
	assert.Equal(t, 115200, capturedOpts.BaudRate)
}

func TestGetChipInfoFlashIDError(t *testing.T) {
	mock := &mockFlasher{
		chipNameVal: "ESP32",
		flashIDErr:  os.ErrPermission,
	}
	factory := func(port string, opts *espflasher.FlasherOptions) (Flasher, error) {
		return mock, nil
	}

	_, err := GetChipInfo(factory, "/dev/ttyUSB0", 0, "")
	require.Error(t, err)
}

func TestGetChipInfoFactoryError(t *testing.T) {
	factory := func(port string, opts *espflasher.FlasherOptions) (Flasher, error) {
		return nil, os.ErrPermission
	}

	_, err := GetChipInfo(factory, "/dev/ttyUSB0", 0, "")
	require.Error(t, err)
}

func TestParseResetModeDefault(t *testing.T) {
	assert.Equal(t, espflasher.ResetDefault, parseResetMode("default"))
	assert.Equal(t, espflasher.ResetDefault, parseResetMode("DEFAULT"))
}

func TestParseResetModeAuto(t *testing.T) {
	assert.Equal(t, espflasher.ResetAuto, parseResetMode(""))
	assert.Equal(t, espflasher.ResetAuto, parseResetMode("auto"))
	assert.Equal(t, espflasher.ResetAuto, parseResetMode("AUTO"))
}

func TestParseResetModeUSBJTAG(t *testing.T) {
	assert.Equal(t, espflasher.ResetUSBJTAG, parseResetMode("usb_jtag"))
	assert.Equal(t, espflasher.ResetUSBJTAG, parseResetMode("usb-jtag"))
	assert.Equal(t, espflasher.ResetUSBJTAG, parseResetMode("USB_JTAG"))
	assert.Equal(t, espflasher.ResetUSBJTAG, parseResetMode("USB-JTAG"))
}

func TestParseResetModeNoReset(t *testing.T) {
	assert.Equal(t, espflasher.ResetNoReset, parseResetMode("no_reset"))
	assert.Equal(t, espflasher.ResetNoReset, parseResetMode("no-reset"))
	assert.Equal(t, espflasher.ResetNoReset, parseResetMode("NO_RESET"))
}

func TestParseChipTypeAuto(t *testing.T) {
	assert.Equal(t, espflasher.ChipAuto, parseChipType(""))
	assert.Equal(t, espflasher.ChipAuto, parseChipType("auto"))
}

func TestParseChipTypeVariants(t *testing.T) {
	assert.Equal(t, espflasher.ChipESP32, parseChipType("esp32"))
	assert.Equal(t, espflasher.ChipESP32S3, parseChipType("ESP32S3"))
	assert.Equal(t, espflasher.ChipESP32C6, parseChipType("esp32c6"))
	assert.Equal(t, espflasher.ChipESP32P4Rev1, parseChipType("esp32p4-rev1"))
	assert.Equal(t, espflasher.ChipESP8266, parseChipType("ESP8266"))
}

func TestNormalizeFlashModeDefault(t *testing.T) {
	assert.Equal(t, "", normalizeFlashMode(""))
	assert.Equal(t, "dio", normalizeFlashMode("dio"))
	assert.Equal(t, "dio", normalizeFlashMode("DIO"))
}

func TestNormalizeFlashModeVariants(t *testing.T) {
	assert.Equal(t, "qio", normalizeFlashMode("qio"))
	assert.Equal(t, "qout", normalizeFlashMode("QOUT"))
	assert.Equal(t, "dout", normalizeFlashMode("dout"))
}

func TestNormalizeFlashModeInvalid(t *testing.T) {
	assert.Equal(t, "", normalizeFlashMode("invalid"))
	assert.Equal(t, "", normalizeFlashMode("xyz"))
}

func TestFlashESPLogCapture(t *testing.T) {
	tmpDir := t.TempDir()
	fw := tmpDir + "/fw.bin"
	err := os.WriteFile(fw, []byte("firmware"), 0644)
	require.NoError(t, err)

	mock := &mockFlasher{}
	factory := func(port string, opts *espflasher.FlasherOptions) (Flasher, error) {
		// Simulate logging
		if opts.Logger != nil {
			opts.Logger.Logf("test log")
		}
		return mock, nil
	}

	result, err := FlashESP(factory, "/dev/ttyUSB0", []ImageSpec{
		{Path: fw, Offset: 0},
	}, FlashOptions{})

	require.NoError(t, err)
	assert.Contains(t, result.Log, "test log")
}

func TestFlashESPEmptyImages(t *testing.T) {
	mock := &mockFlasher{}
	factory := func(port string, opts *espflasher.FlasherOptions) (Flasher, error) {
		return mock, nil
	}

	result, err := FlashESP(factory, "/dev/ttyUSB0", []ImageSpec{}, FlashOptions{})
	require.NoError(t, err)
	assert.Equal(t, 0, result.BytesWritten)
	assert.True(t, mock.flashImagesCalled)
}

func TestReadRegisterSuccess(t *testing.T) {
	mock := &mockFlasher{
		readRegisterVal: 0xDEADBEEF,
	}
	factory := func(port string, opts *espflasher.FlasherOptions) (Flasher, error) {
		return mock, nil
	}

	result, err := ReadRegister(factory, "/dev/ttyUSB0", 0x3FF00000, 115200, "")
	require.NoError(t, err)
	assert.Equal(t, uint32(0x3FF00000), result.Address)
	assert.Equal(t, uint32(0xDEADBEEF), result.Value)
	assert.Equal(t, "0xDEADBEEF", result.Hex)
	assert.True(t, mock.closeCalled)
}

func TestReadRegisterBaudDefault(t *testing.T) {
	var capturedOpts *espflasher.FlasherOptions
	mock := &mockFlasher{
		readRegisterVal: 0x12345678,
	}
	factory := func(port string, opts *espflasher.FlasherOptions) (Flasher, error) {
		capturedOpts = opts
		return mock, nil
	}

	_, err := ReadRegister(factory, "/dev/ttyUSB0", 0x3FF00000, 0, "")
	require.NoError(t, err)
	assert.Equal(t, 115200, capturedOpts.BaudRate)
}

func TestReadRegisterError(t *testing.T) {
	mock := &mockFlasher{
		readRegisterErr: os.ErrPermission,
	}
	factory := func(port string, opts *espflasher.FlasherOptions) (Flasher, error) {
		return mock, nil
	}

	_, err := ReadRegister(factory, "/dev/ttyUSB0", 0x3FF00000, 0, "")
	require.Error(t, err)
}

func TestReadRegisterFactoryError(t *testing.T) {
	factory := func(port string, opts *espflasher.FlasherOptions) (Flasher, error) {
		return nil, os.ErrPermission
	}

	_, err := ReadRegister(factory, "/dev/ttyUSB0", 0x3FF00000, 0, "")
	require.Error(t, err)
}

func TestWriteRegisterSuccess(t *testing.T) {
	mock := &mockFlasher{}
	factory := func(port string, opts *espflasher.FlasherOptions) (Flasher, error) {
		return mock, nil
	}

	err := WriteRegister(factory, "/dev/ttyUSB0", 0x3FF00000, 0xABCD1234, 115200, "")
	require.NoError(t, err)
	assert.Equal(t, uint32(0x3FF00000), mock.writeRegisterAddr)
	assert.Equal(t, uint32(0xABCD1234), mock.writeRegisterVal)
	assert.True(t, mock.closeCalled)
}

func TestWriteRegisterBaudDefault(t *testing.T) {
	var capturedOpts *espflasher.FlasherOptions
	mock := &mockFlasher{}
	factory := func(port string, opts *espflasher.FlasherOptions) (Flasher, error) {
		capturedOpts = opts
		return mock, nil
	}

	err := WriteRegister(factory, "/dev/ttyUSB0", 0x3FF00000, 0x12345678, 0, "")
	require.NoError(t, err)
	assert.Equal(t, 115200, capturedOpts.BaudRate)
}

func TestWriteRegisterError(t *testing.T) {
	mock := &mockFlasher{
		writeRegisterErr: os.ErrPermission,
	}
	factory := func(port string, opts *espflasher.FlasherOptions) (Flasher, error) {
		return mock, nil
	}

	err := WriteRegister(factory, "/dev/ttyUSB0", 0x3FF00000, 0x12345678, 0, "")
	require.Error(t, err)
}

func TestWriteRegisterFactoryError(t *testing.T) {
	factory := func(port string, opts *espflasher.FlasherOptions) (Flasher, error) {
		return nil, os.ErrPermission
	}

	err := WriteRegister(factory, "/dev/ttyUSB0", 0x3FF00000, 0x12345678, 0, "")
	require.Error(t, err)
}

func TestResetESPSuccess(t *testing.T) {
	mock := &mockFlasher{}
	factory := func(port string, opts *espflasher.FlasherOptions) (Flasher, error) {
		return mock, nil
	}

	err := ResetESP(factory, "/dev/ttyUSB0", "")
	require.NoError(t, err)
	assert.True(t, mock.resetCalled)
	assert.True(t, mock.closeCalled)
}

func TestResetESPWithMode(t *testing.T) {
	var capturedOpts *espflasher.FlasherOptions
	mock := &mockFlasher{}
	factory := func(port string, opts *espflasher.FlasherOptions) (Flasher, error) {
		capturedOpts = opts
		return mock, nil
	}

	err := ResetESP(factory, "/dev/ttyUSB0", "usb_jtag")
	require.NoError(t, err)
	assert.Equal(t, espflasher.ResetUSBJTAG, capturedOpts.ResetMode)
}

func TestResetESPFactoryError(t *testing.T) {
	factory := func(port string, opts *espflasher.FlasherOptions) (Flasher, error) {
		return nil, os.ErrPermission
	}

	err := ResetESP(factory, "/dev/ttyUSB0", "")
	require.Error(t, err)
}

func TestGetSecurityInfoSuccess(t *testing.T) {
	chipID := uint32(0x12345678)
	apiVer := uint32(0x00000001)
	mock := &mockFlasher{
		getSecurityInfoVal: &espflasher.SecurityInfo{
			Flags:         0x12345678,
			FlashCryptCnt: 5,
			KeyPurposes:   [7]uint8{1, 2, 3, 4, 5, 6, 7},
			ChipID:        &chipID,
			APIVersion:    &apiVer,
		},
	}
	factory := func(port string, opts *espflasher.FlasherOptions) (Flasher, error) {
		return mock, nil
	}

	result, err := GetSecurityInfo(factory, "/dev/ttyUSB0", 0, "")
	require.NoError(t, err)
	assert.Equal(t, uint32(0x12345678), result.Flags)
	assert.Equal(t, uint8(5), result.FlashCryptCnt)
	assert.Equal(t, &chipID, result.ChipID)
	assert.Equal(t, &apiVer, result.APIVersion)
	assert.True(t, mock.closeCalled)
}

func TestGetSecurityInfoBaudDefault(t *testing.T) {
	var capturedOpts *espflasher.FlasherOptions
	mock := &mockFlasher{
		getSecurityInfoVal: &espflasher.SecurityInfo{},
	}
	factory := func(port string, opts *espflasher.FlasherOptions) (Flasher, error) {
		capturedOpts = opts
		return mock, nil
	}

	_, err := GetSecurityInfo(factory, "/dev/ttyUSB0", 0, "")
	require.NoError(t, err)
	assert.Equal(t, 115200, capturedOpts.BaudRate)
}

func TestGetSecurityInfoError(t *testing.T) {
	mock := &mockFlasher{
		getSecurityInfoErr: os.ErrPermission,
	}
	factory := func(port string, opts *espflasher.FlasherOptions) (Flasher, error) {
		return mock, nil
	}

	_, err := GetSecurityInfo(factory, "/dev/ttyUSB0", 0, "")
	require.Error(t, err)
}

func TestGetSecurityInfoFactoryError(t *testing.T) {
	factory := func(port string, opts *espflasher.FlasherOptions) (Flasher, error) {
		return nil, os.ErrPermission
	}

	_, err := GetSecurityInfo(factory, "/dev/ttyUSB0", 0, "")
	require.Error(t, err)
}

func TestGetFlashMD5Success(t *testing.T) {
	mock := &mockFlasher{
		flashMD5Val: "5d41402abc4b2a76b9719d911017c592",
	}
	factory := func(port string, opts *espflasher.FlasherOptions) (Flasher, error) {
		return mock, nil
	}

	result, err := GetFlashMD5(factory, "/dev/ttyUSB0", 0x1000, 0x1000, 0, "")
	require.NoError(t, err)
	assert.Equal(t, uint32(0x1000), result.Offset)
	assert.Equal(t, uint32(0x1000), result.Size)
	assert.Equal(t, "5d41402abc4b2a76b9719d911017c592", result.MD5)
	assert.True(t, mock.closeCalled)
}

func TestGetFlashMD5BaudDefault(t *testing.T) {
	var capturedOpts *espflasher.FlasherOptions
	mock := &mockFlasher{
		flashMD5Val: "abc123",
	}
	factory := func(port string, opts *espflasher.FlasherOptions) (Flasher, error) {
		capturedOpts = opts
		return mock, nil
	}

	_, err := GetFlashMD5(factory, "/dev/ttyUSB0", 0, 0, 0, "")
	require.NoError(t, err)
	assert.Equal(t, 115200, capturedOpts.BaudRate)
}

func TestGetFlashMD5Error(t *testing.T) {
	mock := &mockFlasher{
		flashMD5Err: os.ErrPermission,
	}
	factory := func(port string, opts *espflasher.FlasherOptions) (Flasher, error) {
		return mock, nil
	}

	_, err := GetFlashMD5(factory, "/dev/ttyUSB0", 0, 0, 0, "")
	require.Error(t, err)
}

func TestGetFlashMD5FactoryError(t *testing.T) {
	factory := func(port string, opts *espflasher.FlasherOptions) (Flasher, error) {
		return nil, os.ErrPermission
	}

	_, err := GetFlashMD5(factory, "/dev/ttyUSB0", 0, 0, 0, "")
	require.Error(t, err)
}

func TestReadFlashDataSuccess(t *testing.T) {
	testData := []byte{0xAA, 0xBB, 0xCC, 0xDD}
	mock := &mockFlasher{
		readFlashVal: testData,
	}
	factory := func(port string, opts *espflasher.FlasherOptions) (Flasher, error) {
		return mock, nil
	}

	result, err := ReadFlashData(factory, "/dev/ttyUSB0", 0x2000, 4, 0, "")
	require.NoError(t, err)
	assert.Equal(t, uint32(0x2000), result.Offset)
	assert.Equal(t, uint32(4), result.Size)
	assert.Equal(t, testData, result.Data)
	assert.True(t, mock.closeCalled)
}

func TestReadFlashDataBaudDefault(t *testing.T) {
	var capturedOpts *espflasher.FlasherOptions
	mock := &mockFlasher{
		readFlashVal: []byte{},
	}
	factory := func(port string, opts *espflasher.FlasherOptions) (Flasher, error) {
		capturedOpts = opts
		return mock, nil
	}

	_, err := ReadFlashData(factory, "/dev/ttyUSB0", 0, 0, 0, "")
	require.NoError(t, err)
	assert.Equal(t, 115200, capturedOpts.BaudRate)
}

func TestReadFlashDataError(t *testing.T) {
	mock := &mockFlasher{
		readFlashErr: os.ErrPermission,
	}
	factory := func(port string, opts *espflasher.FlasherOptions) (Flasher, error) {
		return mock, nil
	}

	_, err := ReadFlashData(factory, "/dev/ttyUSB0", 0, 0, 0, "")
	require.Error(t, err)
}

func TestReadFlashDataFactoryError(t *testing.T) {
	factory := func(port string, opts *espflasher.FlasherOptions) (Flasher, error) {
		return nil, os.ErrPermission
	}

	_, err := ReadFlashData(factory, "/dev/ttyUSB0", 0, 0, 0, "")
	require.Error(t, err)
}

func TestReadNVSSuccess(t *testing.T) {
	// Generate valid NVS data with some entries
	entries := []nvs.Entry{
		{Namespace: "test", Key: "key1", Type: "u32", Value: uint32(42)},
		{Namespace: "test", Key: "key2", Type: "string", Value: "hello"},
		{Namespace: "other", Key: "key3", Type: "u8", Value: uint8(1)},
	}
	nvsData, err := nvs.GenerateNVS(entries, nvs.DefaultPartSize)
	require.NoError(t, err)

	mock := &mockFlasher{
		readFlashVal: nvsData,
	}
	factory := func(port string, opts *espflasher.FlasherOptions) (Flasher, error) {
		return mock, nil
	}

	result, err := ReadNVS(factory, "/dev/ttyUSB0", 0x9000, uint32(nvs.DefaultPartSize), 115200, "", "")
	require.NoError(t, err)
	assert.Len(t, result, 3)
	// Verify entries are present (order may vary)
	var namespaces []string
	var keys []string
	for _, e := range result {
		namespaces = append(namespaces, e.Namespace)
		keys = append(keys, e.Key)
	}
	assert.Contains(t, namespaces, "test")
	assert.Contains(t, namespaces, "other")
	assert.Contains(t, keys, "key1")
	assert.Contains(t, keys, "key2")
	assert.Contains(t, keys, "key3")
}

func TestReadNVSNamespaceFilter(t *testing.T) {
	entries := []nvs.Entry{
		{Namespace: "test", Key: "key1", Type: "u32", Value: uint32(42)},
		{Namespace: "test", Key: "key2", Type: "string", Value: "hello"},
		{Namespace: "other", Key: "key3", Type: "u8", Value: uint8(1)},
	}
	nvsData, err := nvs.GenerateNVS(entries, nvs.DefaultPartSize)
	require.NoError(t, err)

	mock := &mockFlasher{
		readFlashVal: nvsData,
	}
	factory := func(port string, opts *espflasher.FlasherOptions) (Flasher, error) {
		return mock, nil
	}

	result, err := ReadNVS(factory, "/dev/ttyUSB0", 0x9000, uint32(nvs.DefaultPartSize), 115200, "test", "")
	require.NoError(t, err)
	assert.Len(t, result, 2)
	for _, e := range result {
		assert.Equal(t, "test", e.Namespace)
	}
}

func TestReadNVSReadError(t *testing.T) {
	mock := &mockFlasher{
		readFlashErr: os.ErrPermission,
	}
	factory := func(port string, opts *espflasher.FlasherOptions) (Flasher, error) {
		return mock, nil
	}

	_, err := ReadNVS(factory, "/dev/ttyUSB0", 0x9000, uint32(nvs.DefaultPartSize), 115200, "", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "read flash")
}

func TestNVSSetNewKey(t *testing.T) {
	// Start with one entry
	entries := []nvs.Entry{
		{Namespace: "test", Key: "key1", Type: "u32", Value: uint32(42)},
	}
	nvsData, err := nvs.GenerateNVS(entries, nvs.DefaultPartSize)
	require.NoError(t, err)

	mock := &mockFlasher{
		readFlashVal: nvsData,
	}
	factory := func(port string, opts *espflasher.FlasherOptions) (Flasher, error) {
		return mock, nil
	}

	err = NVSSet(factory, "/dev/ttyUSB0", "test", "key2", "string", "world", 0x9000, uint32(nvs.DefaultPartSize), 115200, "")
	require.NoError(t, err)

	// Verify FlashImages was called
	assert.True(t, mock.flashImagesCalled)
	assert.Len(t, mock.flashImagesData, 1)
}

func TestNVSSetUpdateKey(t *testing.T) {
	// Start with one entry
	entries := []nvs.Entry{
		{Namespace: "test", Key: "key1", Type: "u32", Value: uint32(42)},
	}
	nvsData, err := nvs.GenerateNVS(entries, nvs.DefaultPartSize)
	require.NoError(t, err)

	mock := &mockFlasher{
		readFlashVal: nvsData,
	}
	factory := func(port string, opts *espflasher.FlasherOptions) (Flasher, error) {
		return mock, nil
	}

	err = NVSSet(factory, "/dev/ttyUSB0", "test", "key1", "u32", uint32(100), 0x9000, uint32(nvs.DefaultPartSize), 115200, "")
	require.NoError(t, err)

	// Verify FlashImages was called
	assert.True(t, mock.flashImagesCalled)
}

func TestNVSDeleteKey(t *testing.T) {
	entries := []nvs.Entry{
		{Namespace: "test", Key: "key1", Type: "u32", Value: uint32(42)},
		{Namespace: "test", Key: "key2", Type: "string", Value: "hello"},
	}
	nvsData, err := nvs.GenerateNVS(entries, nvs.DefaultPartSize)
	require.NoError(t, err)

	mock := &mockFlasher{
		readFlashVal: nvsData,
	}
	factory := func(port string, opts *espflasher.FlasherOptions) (Flasher, error) {
		return mock, nil
	}

	err = NVSDelete(factory, "/dev/ttyUSB0", "test", "key1", 0x9000, uint32(nvs.DefaultPartSize), 115200, "")
	require.NoError(t, err)

	assert.True(t, mock.flashImagesCalled)
	assert.True(t, mock.resetCalled)
}

func TestNVSDeleteNamespace(t *testing.T) {
	entries := []nvs.Entry{
		{Namespace: "test", Key: "key1", Type: "u32", Value: uint32(42)},
		{Namespace: "test", Key: "key2", Type: "string", Value: "hello"},
		{Namespace: "other", Key: "key3", Type: "u8", Value: uint8(1)},
	}
	nvsData, err := nvs.GenerateNVS(entries, nvs.DefaultPartSize)
	require.NoError(t, err)

	mock := &mockFlasher{
		readFlashVal: nvsData,
	}
	factory := func(port string, opts *espflasher.FlasherOptions) (Flasher, error) {
		return mock, nil
	}

	err = NVSDelete(factory, "/dev/ttyUSB0", "test", "", 0x9000, uint32(nvs.DefaultPartSize), 115200, "")
	require.NoError(t, err)

	assert.True(t, mock.flashImagesCalled)
	assert.True(t, mock.resetCalled)
}

func TestNVSDeleteReadError(t *testing.T) {
	mock := &mockFlasher{
		readFlashErr: fmt.Errorf("read failed"),
	}
	factory := func(port string, opts *espflasher.FlasherOptions) (Flasher, error) {
		return mock, nil
	}

	err := NVSDelete(factory, "/dev/ttyUSB0", "test", "key1", 0x9000, uint32(nvs.DefaultPartSize), 0, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "read NVS")
}

func TestNVSSetBatch(t *testing.T) {
	// Set up mock with existing NVS data
	existingEntries := []nvs.Entry{
		{Namespace: "test", Key: "existing", Type: "u8", Value: uint8(1)},
	}
	existingData, err := nvs.GenerateNVS(existingEntries, nvs.DefaultPartSize)
	require.NoError(t, err)

	mock := &mockFlasher{
		readFlashVal: existingData,
	}

	factory := func(port string, opts *espflasher.FlasherOptions) (Flasher, error) {
		return mock, nil
	}

	updates := []NVSUpdate{
		{Namespace: "test", Key: "existing", Type: "u8", Value: uint8(42)},
		{Namespace: "test", Key: "new_key", Type: "string", Value: "hello"},
	}

	err = NVSSetBatch(factory, "test-port", updates, 0x9000, uint32(nvs.DefaultPartSize), 0, "")
	require.NoError(t, err)

	assert.True(t, mock.flashImagesCalled)
	assert.True(t, mock.resetCalled)
	require.Len(t, mock.flashImagesData, 1)
	assert.Equal(t, uint32(0x9000), mock.flashImagesData[0].Offset)

	// Verify the written NVS contains both entries with correct values
	written, err := nvs.ParseNVS(mock.flashImagesData[0].Data)
	require.NoError(t, err)

	writtenMap := make(map[string]nvs.Entry)
	for _, e := range written {
		writtenMap[e.Key] = e
	}

	require.Contains(t, writtenMap, "existing")
	assert.Equal(t, uint8(42), writtenMap["existing"].Value)
	require.Contains(t, writtenMap, "new_key")
	assert.Equal(t, "hello", writtenMap["new_key"].Value)
}

func TestNVSSetBatchReadError(t *testing.T) {
	mock := &mockFlasher{
		readFlashErr: fmt.Errorf("read failed"),
	}

	factory := func(port string, opts *espflasher.FlasherOptions) (Flasher, error) {
		return mock, nil
	}

	err := NVSSetBatch(factory, "test-port", []NVSUpdate{
		{Namespace: "test", Key: "k", Type: "u8", Value: uint8(1)},
	}, 0x9000, uint32(nvs.DefaultPartSize), 0, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "read NVS")
}

func TestNVSSetBatchFlashError(t *testing.T) {
	existingData, err := nvs.GenerateNVS([]nvs.Entry{}, nvs.DefaultPartSize)
	require.NoError(t, err)

	mock := &mockFlasher{
		readFlashVal:   existingData,
		flashImagesErr: fmt.Errorf("flash failed"),
	}

	factory := func(port string, opts *espflasher.FlasherOptions) (Flasher, error) {
		return mock, nil
	}

	err = NVSSetBatch(factory, "test-port", []NVSUpdate{
		{Namespace: "test", Key: "k", Type: "u8", Value: uint8(1)},
	}, 0x9000, uint32(nvs.DefaultPartSize), 0, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "write NVS")
}

func TestNVSSetUsesNVSSetBatch(t *testing.T) {
	existingData, err := nvs.GenerateNVS([]nvs.Entry{}, nvs.DefaultPartSize)
	require.NoError(t, err)

	mock := &mockFlasher{
		readFlashVal: existingData,
	}

	factory := func(port string, opts *espflasher.FlasherOptions) (Flasher, error) {
		return mock, nil
	}

	err = NVSSet(factory, "test-port", "ns", "key", "u32", uint32(999), 0x9000, uint32(nvs.DefaultPartSize), 0, "")
	require.NoError(t, err)

	assert.True(t, mock.flashImagesCalled)
	require.Len(t, mock.flashImagesData, 1)

	written, err := nvs.ParseNVS(mock.flashImagesData[0].Data)
	require.NoError(t, err)
	require.Len(t, written, 1)
	assert.Equal(t, "key", written[0].Key)
	assert.Equal(t, uint32(999), written[0].Value)
}

func TestFlashESPForceOffsetsSkipsValidation(t *testing.T) {
	tmpDir := t.TempDir()
	fw := tmpDir + "/fw.bin"
	err := os.WriteFile(fw, []byte("firmware"), 0644)
	require.NoError(t, err)

	mock := &mockFlasher{
		chipNameVal: "ESP32",
		// Mock returns stale partition table
		readFlashVal: []byte{0xFF, 0xFF},
	}
	factory := func(port string, opts *espflasher.FlasherOptions) (Flasher, error) {
		return mock, nil
	}

	// flash at 0x0 would normally fail validation because it doesn't match any partition
	result, err := FlashESP(factory, "/dev/ttyUSB0", []ImageSpec{
		{Path: fw, Offset: 0x0},
	}, FlashOptions{ForceOffsets: true})

	require.NoError(t, err)
	assert.True(t, mock.flashImagesCalled)
	assert.Equal(t, 8, result.BytesWritten)
}

func TestFlashESPPrefersInFlightPartitionTable(t *testing.T) {
	tmpDir := t.TempDir()

	// Helper to create partition entry (copied from partition_test.go)
	makeEntry := func(typ, subtype uint8, offset, size uint32, label string) []byte {
		entry := make([]byte, 32)
		binary.LittleEndian.PutUint16(entry[0:2], partitionMagic)
		entry[2] = typ
		entry[3] = subtype
		binary.LittleEndian.PutUint32(entry[4:8], offset)
		binary.LittleEndian.PutUint32(entry[8:12], size)
		copy(entry[12:28], label)
		return entry
	}

	// New partition table: app at 0x10000
	newPartitionTableData := make([]byte, 64)
	newEntry := makeEntry(0, 0x10, 0x10000, 0x100000, "ota_0")
	copy(newPartitionTableData[0:32], newEntry)
	newPartitionTableData[32] = 0xFF // MD5 marker

	err := os.WriteFile(tmpDir+"/new_partitions.bin", newPartitionTableData, 0644)
	require.NoError(t, err)

	fw := tmpDir + "/firmware.bin"
	err = os.WriteFile(fw, []byte("firmware"), 0644)
	require.NoError(t, err)

	// Device has stale partition table: app at 0x20000
	stalePartitionTableData := make([]byte, 64)
	staleEntry := makeEntry(0, 0x10, 0x20000, 0x100000, "ota_0")
	copy(stalePartitionTableData[0:32], staleEntry)

	mock := &mockFlasher{
		chipNameVal: "ESP32",
		// Mock returns stale partition table when ReadFlash is called
		readFlashVal: stalePartitionTableData,
	}
	factory := func(port string, opts *espflasher.FlasherOptions) (Flasher, error) {
		return mock, nil
	}

	// Flash new partition table at 0x8000 (in-flight) and firmware at 0x10000 (new app offset)
	// Validation should prefer the new partition table over the device's stale one
	result, err := FlashESP(factory, "/dev/ttyUSB0", []ImageSpec{
		{Path: tmpDir + "/new_partitions.bin", Offset: 0x8000},
		{Path: fw, Offset: 0x10000},
	}, FlashOptions{})

	require.NoError(t, err)
	assert.True(t, mock.flashImagesCalled)
	assert.Equal(t, len(newPartitionTableData)+8, result.BytesWritten)
}
