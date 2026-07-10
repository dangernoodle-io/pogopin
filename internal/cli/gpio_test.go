package cli

import (
	"bytes"
	"fmt"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	espflasher "tinygo.org/x/espflasher/pkg/espflasher"

	"dangernoodle.io/pogopin/internal/esp"
)

// gpioMockFlasher implements esp.Flasher for CLI-layer tests. Only the GPIO
// methods are exercised; everything else is a harmless zero-value stub.
type gpioMockFlasher struct {
	readGPIOVal      bool
	readGPIOErr      error
	setGPIOErr       error
	setGPIOCalls     []bool
	releaseGPIOCalls []int
	gpioReservedFunc func(pin int) (bool, string)
	resetCallCount   int
}

func (m *gpioMockFlasher) FlashImages(images []espflasher.ImagePart, progress espflasher.ProgressFunc) error {
	return nil
}
func (m *gpioMockFlasher) EraseFlash(progress espflasher.ProgressFunc) error { return nil }
func (m *gpioMockFlasher) EraseRegion(offset, size uint32, progress espflasher.ProgressFunc) error {
	return nil
}
func (m *gpioMockFlasher) FlashID() (uint8, uint16, error)             { return 0, 0, nil }
func (m *gpioMockFlasher) ChipType() espflasher.ChipType               { return espflasher.ChipAuto }
func (m *gpioMockFlasher) ChipName() string                            { return "test-chip" }
func (m *gpioMockFlasher) BootloaderFlashOffset() (uint32, bool)       { return 0, false }
func (m *gpioMockFlasher) Reset()                                      { m.resetCallCount++ }
func (m *gpioMockFlasher) Close() error                                { return nil }
func (m *gpioMockFlasher) ReadRegister(address uint32) (uint32, error) { return 0, nil }
func (m *gpioMockFlasher) WriteRegister(address, value uint32) error   { return nil }
func (m *gpioMockFlasher) GetSecurityInfo() (*espflasher.SecurityInfo, error) {
	return &espflasher.SecurityInfo{}, nil
}
func (m *gpioMockFlasher) GetFlashMD5(offset, size uint32, progress espflasher.ProgressFunc) (string, error) {
	return "", nil
}
func (m *gpioMockFlasher) ReadFlash(offset, size uint32, progress espflasher.ProgressFunc) ([]byte, error) {
	return nil, nil
}
func (m *gpioMockFlasher) FlushInput() {}

func (m *gpioMockFlasher) ReadGPIO(pin int) (bool, error) {
	return m.readGPIOVal, m.readGPIOErr
}

func (m *gpioMockFlasher) SetGPIO(pin int, level bool) error {
	m.setGPIOCalls = append(m.setGPIOCalls, level)
	return m.setGPIOErr
}

func (m *gpioMockFlasher) ReleaseGPIO(pin int) error {
	m.releaseGPIOCalls = append(m.releaseGPIOCalls, pin)
	return nil
}

func (m *gpioMockFlasher) GPIOReserved(pin int) (bool, string) {
	if m.gpioReservedFunc != nil {
		return m.gpioReservedFunc(pin)
	}
	return false, ""
}

// withGPIOFactory swaps gpioFlasherFactory to return mock for the duration
// of the test and restores it on cleanup. gpio subcommands mutate shared
// package-level flag vars, so these tests must not run in parallel with
// each other.
func withGPIOFactory(t *testing.T, mock esp.Flasher) {
	t.Helper()
	orig := gpioFlasherFactory
	gpioFlasherFactory = func(port string, opts *espflasher.FlasherOptions) (esp.Flasher, error) {
		return mock, nil
	}
	t.Cleanup(func() { gpioFlasherFactory = orig })

	origResetAfter := gpioResetAfter
	gpioResetAfter = false
	t.Cleanup(func() { gpioResetAfter = origResetAfter })

	origSetIncludeRsv := gpioSetIncludeRsv
	gpioSetIncludeRsv = false
	t.Cleanup(func() { gpioSetIncludeRsv = origSetIncludeRsv })

	// Save/restore every remaining package-level gpio flag var so a test
	// that sets one (e.g. gpioPort, gpioDwell) can't leak it into a later
	// test — these are shared cobra-flag globals, not per-test state.
	origPort := gpioPort
	t.Cleanup(func() { gpioPort = origPort })

	origBaud := gpioBaud
	t.Cleanup(func() { gpioBaud = origBaud })

	origResetMode := gpioResetMode
	t.Cleanup(func() { gpioResetMode = origResetMode })

	origBoth := gpioBoth
	t.Cleanup(func() { gpioBoth = origBoth })

	origDwell := gpioDwell
	t.Cleanup(func() { gpioDwell = origDwell })

	origIncludeRsv := gpioIncludeRsv
	t.Cleanup(func() { gpioIncludeRsv = origIncludeRsv })
}

// newTestGPIOCmd returns a bare cobra.Command wired with stdout/stderr
// buffers, suitable for exercising the run* functions' output paths.
func newTestGPIOCmd() (*cobra.Command, *bytes.Buffer, *bytes.Buffer) {
	var out, errBuf bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&out)
	cmd.SetErr(&errBuf)
	return cmd, &out, &errBuf
}

func TestRunGPIORead(t *testing.T) {
	mock := &gpioMockFlasher{readGPIOVal: true}
	withGPIOFactory(t, mock)
	gpioPort = "/dev/ttyUSB0"

	cmd, out, _ := newTestGPIOCmd()
	err := runGPIORead(cmd, []string{"4"})
	require.NoError(t, err)
	assert.Contains(t, out.String(), "GPIO 4: high")
	assert.Equal(t, 0, mock.resetCallCount)
}

func TestRunGPIOReadResetAfter(t *testing.T) {
	mock := &gpioMockFlasher{readGPIOVal: true}
	withGPIOFactory(t, mock)
	gpioPort = "/dev/ttyUSB0"
	gpioResetAfter = true

	cmd, out, _ := newTestGPIOCmd()
	err := runGPIORead(cmd, []string{"4"})
	require.NoError(t, err)
	assert.Contains(t, out.String(), "GPIO 4: high")
	assert.Equal(t, 1, mock.resetCallCount)
}

func TestRunGPIOReadInvalidPin(t *testing.T) {
	cmd, _, _ := newTestGPIOCmd()
	err := runGPIORead(cmd, []string{"nope"})
	require.Error(t, err)
}

func TestRunGPIOReadError(t *testing.T) {
	mock := &gpioMockFlasher{readGPIOErr: fmt.Errorf("boom")}
	withGPIOFactory(t, mock)
	gpioPort = "/dev/ttyUSB0"

	cmd, _, _ := newTestGPIOCmd()
	err := runGPIORead(cmd, []string{"4"})
	require.Error(t, err)
}

func TestRunGPIOSet(t *testing.T) {
	mock := &gpioMockFlasher{}
	withGPIOFactory(t, mock)
	gpioPort = "/dev/ttyUSB0"

	cmd, out, _ := newTestGPIOCmd()
	err := runGPIOSet(cmd, []string{"4", "1"})
	require.NoError(t, err)
	assert.Contains(t, out.String(), "GPIO 4 set high")
	require.Len(t, mock.setGPIOCalls, 1)
	assert.True(t, mock.setGPIOCalls[0])
	assert.Equal(t, 0, mock.resetCallCount)
}

func TestRunGPIOSetResetAfter(t *testing.T) {
	mock := &gpioMockFlasher{}
	withGPIOFactory(t, mock)
	gpioPort = "/dev/ttyUSB0"
	gpioResetAfter = true

	cmd, _, _ := newTestGPIOCmd()
	err := runGPIOSet(cmd, []string{"4", "1"})
	require.NoError(t, err)
	assert.Equal(t, 1, mock.resetCallCount)
}

func TestRunGPIOSetInvalidPin(t *testing.T) {
	cmd, _, _ := newTestGPIOCmd()
	err := runGPIOSet(cmd, []string{"nope", "1"})
	require.Error(t, err)
}

func TestRunGPIOSetInvalidLevel(t *testing.T) {
	cmd, _, _ := newTestGPIOCmd()
	err := runGPIOSet(cmd, []string{"4", "high"})
	require.Error(t, err)
}

func TestRunGPIOSetError(t *testing.T) {
	mock := &gpioMockFlasher{setGPIOErr: fmt.Errorf("boom")}
	withGPIOFactory(t, mock)
	gpioPort = "/dev/ttyUSB0"

	cmd, _, _ := newTestGPIOCmd()
	err := runGPIOSet(cmd, []string{"4", "1"})
	require.Error(t, err)
}

func TestRunGPIOSetReserved(t *testing.T) {
	t.Run("reserved pin refused by default", func(t *testing.T) {
		mock := &gpioMockFlasher{
			gpioReservedFunc: func(pin int) (bool, string) {
				if pin == 6 {
					return true, "flash"
				}
				return false, ""
			},
		}
		withGPIOFactory(t, mock)
		gpioPort = "/dev/ttyUSB0"
		gpioSetIncludeRsv = false

		cmd, _, _ := newTestGPIOCmd()
		err := runGPIOSet(cmd, []string{"6", "1"})
		require.Error(t, err)
		assert.Empty(t, mock.setGPIOCalls)
	})

	t.Run("reserved pin driven with include-reserved", func(t *testing.T) {
		mock := &gpioMockFlasher{
			gpioReservedFunc: func(pin int) (bool, string) {
				if pin == 6 {
					return true, "flash"
				}
				return false, ""
			},
		}
		withGPIOFactory(t, mock)
		gpioPort = "/dev/ttyUSB0"
		gpioSetIncludeRsv = true

		cmd, out, _ := newTestGPIOCmd()
		err := runGPIOSet(cmd, []string{"6", "1"})
		require.NoError(t, err)
		assert.Contains(t, out.String(), "GPIO 6 set high")
		require.Len(t, mock.setGPIOCalls, 1)
		assert.True(t, mock.setGPIOCalls[0])
	})

	t.Run("non-reserved pin driven regardless of flag", func(t *testing.T) {
		mock := &gpioMockFlasher{}
		withGPIOFactory(t, mock)
		gpioPort = "/dev/ttyUSB0"
		gpioSetIncludeRsv = false

		cmd, out, _ := newTestGPIOCmd()
		err := runGPIOSet(cmd, []string{"4", "1"})
		require.NoError(t, err)
		assert.Contains(t, out.String(), "GPIO 4 set high")
		require.Len(t, mock.setGPIOCalls, 1)
	})
}

func TestRunGPIOSweep(t *testing.T) {
	mock := &gpioMockFlasher{
		gpioReservedFunc: func(pin int) (bool, string) {
			if pin == 6 {
				return true, "flash"
			}
			return false, ""
		},
	}
	withGPIOFactory(t, mock)
	gpioPort = "/dev/ttyUSB0"
	gpioBoth = true
	gpioDwell = time.Millisecond
	gpioIncludeRsv = false

	cmd, out, errBuf := newTestGPIOCmd()
	err := runGPIOSweep(cmd, []string{"4,6"})
	require.NoError(t, err)
	assert.Contains(t, out.String(), "GPIO 4: swept")
	assert.Contains(t, out.String(), "GPIO 6: skipped (flash)")
	assert.Contains(t, errBuf.String(), "driving GPIO 4 high")
	assert.Equal(t, 0, mock.resetCallCount)
}

func TestRunGPIOSweepResetAfter(t *testing.T) {
	mock := &gpioMockFlasher{}
	withGPIOFactory(t, mock)
	gpioPort = "/dev/ttyUSB0"
	gpioBoth = false
	gpioDwell = time.Millisecond
	gpioIncludeRsv = false
	gpioResetAfter = true

	cmd, _, _ := newTestGPIOCmd()
	err := runGPIOSweep(cmd, []string{"4"})
	require.NoError(t, err)
	assert.Equal(t, 1, mock.resetCallCount)
}

func TestRunGPIOSweepNoArgsDefaultsToDrivableSet(t *testing.T) {
	mock := &gpioMockFlasher{
		gpioReservedFunc: func(pin int) (bool, string) {
			if pin >= 3 {
				return true, "nonexistent"
			}
			return false, ""
		},
	}
	withGPIOFactory(t, mock)
	gpioPort = "/dev/ttyUSB0"
	gpioBoth = false
	gpioDwell = time.Millisecond
	gpioIncludeRsv = false

	cmd, out, errBuf := newTestGPIOCmd()
	err := runGPIOSweep(cmd, nil)
	require.NoError(t, err)
	assert.Contains(t, out.String(), "GPIO 0: swept")
	assert.Contains(t, out.String(), "GPIO 1: swept")
	assert.Contains(t, out.String(), "GPIO 2: swept")
	assert.Contains(t, errBuf.String(), "sweeping 3 drivable pins")
}

func TestRunGPIOSweepInvalidPins(t *testing.T) {
	cmd, _, _ := newTestGPIOCmd()
	err := runGPIOSweep(cmd, []string{""})
	require.Error(t, err)
}

func TestRunGPIOSweepError(t *testing.T) {
	factory := func(port string, opts *espflasher.FlasherOptions) (esp.Flasher, error) {
		return nil, fmt.Errorf("boom")
	}
	orig := gpioFlasherFactory
	gpioFlasherFactory = factory
	t.Cleanup(func() { gpioFlasherFactory = orig })
	gpioPort = "/dev/ttyUSB0"

	cmd, _, _ := newTestGPIOCmd()
	err := runGPIOSweep(cmd, []string{"4"})
	require.Error(t, err)
}

func TestNewGPIOStatusFunc(t *testing.T) {
	cmd, _, errBuf := newTestGPIOCmd()
	status := newGPIOStatusFunc(cmd)

	status("driving GPIO 4 high", 1, 3)
	assert.Contains(t, errBuf.String(), "driving GPIO 4 high (1/3)")

	status("skipping reserved GPIO 6 (flash)", 0, 0)
	assert.Contains(t, errBuf.String(), "skipping reserved GPIO 6 (flash)\n")
}

func TestParsePinList(t *testing.T) {
	t.Parallel()

	t.Run("comma-separated list", func(t *testing.T) {
		pins, err := parsePinList("4,16,17")
		require.NoError(t, err)
		assert.Equal(t, []int{4, 16, 17}, pins)
	})

	t.Run("range", func(t *testing.T) {
		pins, err := parsePinList("0-5")
		require.NoError(t, err)
		assert.Equal(t, []int{0, 1, 2, 3, 4, 5}, pins)
	})

	t.Run("mixed list and range", func(t *testing.T) {
		pins, err := parsePinList("4,16-18,21")
		require.NoError(t, err)
		assert.Equal(t, []int{4, 16, 17, 18, 21}, pins)
	})

	t.Run("single pin", func(t *testing.T) {
		pins, err := parsePinList("4")
		require.NoError(t, err)
		assert.Equal(t, []int{4}, pins)
	})

	t.Run("empty", func(t *testing.T) {
		_, err := parsePinList("")
		require.Error(t, err)
	})

	t.Run("invalid pin", func(t *testing.T) {
		_, err := parsePinList("abc")
		require.Error(t, err)
	})

	t.Run("invalid range", func(t *testing.T) {
		_, err := parsePinList("5-abc")
		require.Error(t, err)
	})

	t.Run("range end before start", func(t *testing.T) {
		_, err := parsePinList("10-5")
		require.Error(t, err)
	})

	t.Run("normal range within bounds", func(t *testing.T) {
		pins, err := parsePinList("0-21")
		require.NoError(t, err)
		assert.Len(t, pins, 22)
	})

	t.Run("oversized range rejected", func(t *testing.T) {
		_, err := parsePinList("0-999999999")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "exceeds max")
	})
}

func TestParseLevel(t *testing.T) {
	t.Parallel()

	t.Run("high", func(t *testing.T) {
		level, err := parseLevel("1")
		require.NoError(t, err)
		assert.True(t, level)
	})

	t.Run("low", func(t *testing.T) {
		level, err := parseLevel("0")
		require.NoError(t, err)
		assert.False(t, level)
	})

	t.Run("invalid", func(t *testing.T) {
		_, err := parseLevel("high")
		require.Error(t, err)
	})
}

func TestLevelString(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "high", levelString(true))
	assert.Equal(t, "low", levelString(false))
}
