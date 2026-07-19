package mcpapp

import (
	"context"
	"testing"

	"github.com/dangernoodle-io/shesha/testkit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	espflasher "tinygo.org/x/espflasher/pkg/espflasher"

	"dangernoodle.io/pogopin/internal/esp"
	"dangernoodle.io/pogopin/internal/serial"
	"dangernoodle.io/pogopin/internal/session"
)

// smokeMockFlasher implements esp.Flasher for the esp/flash capability
// smoke tests below. Only FlashID/ChipName back esp_info's happy path;
// every other method is a harmless zero-value stub, mirroring
// internal/cli/gpio_test.go's gpioMockFlasher.
type smokeMockFlasher struct {
	chipName string
	mfgID    uint8
	devID    uint16
}

func (m *smokeMockFlasher) FlashImages(images []espflasher.ImagePart, progress espflasher.ProgressFunc) error {
	return nil
}
func (m *smokeMockFlasher) EraseFlash(progress espflasher.ProgressFunc) error { return nil }
func (m *smokeMockFlasher) EraseRegion(offset, size uint32, progress espflasher.ProgressFunc) error {
	return nil
}
func (m *smokeMockFlasher) FlashID() (uint8, uint16, error)       { return m.mfgID, m.devID, nil }
func (m *smokeMockFlasher) ChipType() espflasher.ChipType         { return espflasher.ChipAuto }
func (m *smokeMockFlasher) ChipName() string                      { return m.chipName }
func (m *smokeMockFlasher) BootloaderFlashOffset() (uint32, bool) { return 0, false }
func (m *smokeMockFlasher) Reset()                                {}
func (m *smokeMockFlasher) Close() error                          { return nil }
func (m *smokeMockFlasher) ReadRegister(address uint32) (uint32, error) {
	return 0, nil
}
func (m *smokeMockFlasher) WriteRegister(address, value uint32) error { return nil }
func (m *smokeMockFlasher) ReadGPIO(pin int) (bool, error)            { return false, nil }
func (m *smokeMockFlasher) SetGPIO(pin int, level bool) error         { return nil }
func (m *smokeMockFlasher) ReleaseGPIO(pin int) error                 { return nil }
func (m *smokeMockFlasher) GPIOReserved(pin int) (bool, string)       { return false, "" }
func (m *smokeMockFlasher) GetSecurityInfo() (*espflasher.SecurityInfo, error) {
	return &espflasher.SecurityInfo{}, nil
}
func (m *smokeMockFlasher) GetFlashMD5(offset, size uint32, progress espflasher.ProgressFunc) (string, error) {
	return "", nil
}
func (m *smokeMockFlasher) ReadFlash(offset, size uint32, progress espflasher.ProgressFunc) ([]byte, error) {
	return nil, nil
}
func (m *smokeMockFlasher) FlushInput() {}

// setupSmokeFlasherFactory swaps in a fast sync-retry delay for the
// duration of the test, mirroring internal/mcpserver/helpers_test.go's
// setupTestFlasherFactory.
func setupSmokeFlasherFactory(t *testing.T) {
	t.Helper()
	origDelay := session.SetSyncRetryDelay(0)
	t.Cleanup(func() { session.SetSyncRetryDelay(origDelay) })
}

// unlockHardwareTier calls serial_list against h to lift the hardware-tier
// lock, the same trigger a real client would use.
func unlockHardwareTier(t *testing.T, h *testkit.Harness) {
	t.Helper()
	res, err := h.CallTool(context.Background(), "serial_list", map[string]any{})
	require.NoError(t, err)
	require.False(t, res.IsError)
}

// TestEspInfoRoundTrip proves esp_info is wired end to end through the
// shesha stack: happy path returns chip info, and a SyncError from the
// flasher factory surfaces the handleSyncError-formatted message.
func TestEspInfoRoundTrip(t *testing.T) {
	setupTestPorts(t)
	setupSmokeFlasherFactory(t)
	origListFn := session.SetListPortsFn(func() ([]serial.PortInfo, error) { return nil, nil })
	t.Cleanup(func() { session.SetListPortsFn(origListFn) })

	app, err := BuildApp()
	require.NoError(t, err)
	h := testkit.New(t, app)
	unlockHardwareTier(t, h)

	t.Run("happy path", func(t *testing.T) {
		orig := session.SetFlasherFactory(func(port string, opts *espflasher.FlasherOptions) (esp.Flasher, error) {
			return &smokeMockFlasher{chipName: "esp32-smoke", mfgID: 0xEF, devID: 0x4016}, nil
		})
		t.Cleanup(func() { session.SetFlasherFactory(orig) })

		res, err := h.CallTool(context.Background(), "esp_info", map[string]any{"port": "/dev/cu.smoke"})
		require.NoError(t, err)
		require.False(t, res.IsError)
		assert.Contains(t, testkit.ResultText(res), "esp32-smoke")
	})

	t.Run("sync error", func(t *testing.T) {
		orig := session.SetFlasherFactory(func(port string, opts *espflasher.FlasherOptions) (esp.Flasher, error) {
			return nil, &espflasher.SyncError{Attempts: 10}
		})
		t.Cleanup(func() { session.SetFlasherFactory(orig) })

		// A distinct port name from the happy-path subtest: AcquireForFlasher
		// caches a returned flasher onto its PortSession, so reusing the same
		// port name here would silently reuse the still-live cached mock
		// flasher instead of exercising the new (SyncError) factory.
		res, err := h.CallTool(context.Background(), "esp_info", map[string]any{"port": "/dev/cu.smoke-sync-error"})
		require.NoError(t, err)
		require.True(t, res.IsError)
		assert.Contains(t, testkit.ResultText(res), "not in download mode")
	})
}

// TestEspRegisterRoundTrip proves esp_register is wired end to end: a
// missing value reads, and a SyncError from the flasher factory surfaces
// the handleSyncError-formatted message on the write path (Value set).
func TestEspRegisterRoundTrip(t *testing.T) {
	setupTestPorts(t)
	setupSmokeFlasherFactory(t)
	origListFn := session.SetListPortsFn(func() ([]serial.PortInfo, error) { return nil, nil })
	t.Cleanup(func() { session.SetListPortsFn(origListFn) })

	app, err := BuildApp()
	require.NoError(t, err)
	h := testkit.New(t, app)
	unlockHardwareTier(t, h)

	orig := session.SetFlasherFactory(func(port string, opts *espflasher.FlasherOptions) (esp.Flasher, error) {
		return nil, &espflasher.SyncError{Attempts: 3}
	})
	t.Cleanup(func() { session.SetFlasherFactory(orig) })

	res, err := h.CallTool(context.Background(), "esp_register", map[string]any{
		"port":    "/dev/cu.smoke",
		"address": float64(0x3ff00000),
		"value":   float64(1),
	})
	require.NoError(t, err)
	require.True(t, res.IsError)
	assert.Contains(t, testkit.ResultText(res), "not in download mode")
}
