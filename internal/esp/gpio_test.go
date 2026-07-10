package esp

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	espflasher "tinygo.org/x/espflasher/pkg/espflasher"
)

// withNoGPIOSleep replaces gpioSleep with a recorder for the duration of the
// test, avoiding real sleeps, and returns the recorded durations.
func withNoGPIOSleep(t *testing.T) *[]time.Duration {
	t.Helper()
	var durations []time.Duration
	orig := gpioSleep
	gpioSleep = func(d time.Duration) {
		durations = append(durations, d)
	}
	t.Cleanup(func() { gpioSleep = orig })
	return &durations
}

// mockFlasherSetGPIOFailsOnLow wraps mockFlasher, succeeding the first
// SetGPIO call per pin (the "drive high" call) and failing every
// subsequent one (the "drive low" call), to exercise the low-polarity error
// path in SweepGPIO.
type mockFlasherSetGPIOFailsOnLow struct {
	*mockFlasher
	failErr   error
	callCount *int
}

func (m *mockFlasherSetGPIOFailsOnLow) SetGPIO(pin int, level bool) error {
	*m.callCount++
	m.setGPIOCalls = append(m.setGPIOCalls, setGPIOCall{pin: pin, level: level})
	if *m.callCount > 1 {
		return m.failErr
	}
	return nil
}

func TestReadGPIOSuccess(t *testing.T) {
	mock := &mockFlasher{readGPIOVal: true}
	factory := func(port string, opts *espflasher.FlasherOptions) (Flasher, error) {
		return mock, nil
	}

	result, err := ReadGPIO(factory, "/dev/ttyUSB0", 4, 0, "", false)
	require.NoError(t, err)
	assert.Equal(t, 4, result.Pin)
	assert.True(t, result.Level)
	assert.True(t, mock.closeCalled)
	assert.False(t, mock.resetCalled)
}

func TestReadGPIOResetAfter(t *testing.T) {
	mock := &mockFlasher{readGPIOVal: true}
	factory := func(port string, opts *espflasher.FlasherOptions) (Flasher, error) {
		return mock, nil
	}

	_, err := ReadGPIO(factory, "/dev/ttyUSB0", 4, 0, "", true)
	require.NoError(t, err)
	assert.True(t, mock.closeCalled)
	assert.Equal(t, 1, mock.resetCallCount)
}

func TestReadGPIOError(t *testing.T) {
	mock := &mockFlasher{readGPIOErr: os.ErrPermission}
	factory := func(port string, opts *espflasher.FlasherOptions) (Flasher, error) {
		return mock, nil
	}

	_, err := ReadGPIO(factory, "/dev/ttyUSB0", 4, 0, "", false)
	require.Error(t, err)
}

func TestReadGPIOFactoryError(t *testing.T) {
	factory := func(port string, opts *espflasher.FlasherOptions) (Flasher, error) {
		return nil, os.ErrPermission
	}

	_, err := ReadGPIO(factory, "/dev/ttyUSB0", 4, 0, "", false)
	require.Error(t, err)
}

func TestSetGPIOSuccess(t *testing.T) {
	mock := &mockFlasher{}
	factory := func(port string, opts *espflasher.FlasherOptions) (Flasher, error) {
		return mock, nil
	}

	err := SetGPIO(factory, "/dev/ttyUSB0", 4, true, 0, "", false, false)
	require.NoError(t, err)
	require.Len(t, mock.setGPIOCalls, 1)
	assert.Equal(t, 4, mock.setGPIOCalls[0].pin)
	assert.True(t, mock.setGPIOCalls[0].level)
	assert.True(t, mock.closeCalled)
	assert.False(t, mock.resetCalled)
}

func TestSetGPIOResetAfter(t *testing.T) {
	mock := &mockFlasher{}
	factory := func(port string, opts *espflasher.FlasherOptions) (Flasher, error) {
		return mock, nil
	}

	err := SetGPIO(factory, "/dev/ttyUSB0", 4, true, 0, "", true, false)
	require.NoError(t, err)
	assert.Equal(t, 1, mock.resetCallCount)
}

func TestSetGPIOFactoryError(t *testing.T) {
	factory := func(port string, opts *espflasher.FlasherOptions) (Flasher, error) {
		return nil, os.ErrPermission
	}

	err := SetGPIO(factory, "/dev/ttyUSB0", 4, true, 0, "", false, false)
	require.Error(t, err)
}

func TestSetGPIOError(t *testing.T) {
	mock := &mockFlasher{setGPIOErr: os.ErrPermission}
	factory := func(port string, opts *espflasher.FlasherOptions) (Flasher, error) {
		return mock, nil
	}

	err := SetGPIO(factory, "/dev/ttyUSB0", 4, true, 0, "", false, false)
	require.Error(t, err)
}

// TestSetGPIOReservedRefusedByDefault confirms SetGPIO consults
// f.GPIOReserved and refuses a reserved pin WITHOUT ever calling the
// underlying f.SetGPIO drive — the hardware-safety contract esp_gpio_set's
// tool description claims.
func TestSetGPIOReservedRefusedByDefault(t *testing.T) {
	mock := &mockFlasher{
		gpioReservedFunc: func(pin int) (bool, string) {
			if pin == 6 {
				return true, "flash"
			}
			return false, ""
		},
	}
	factory := func(port string, opts *espflasher.FlasherOptions) (Flasher, error) {
		return mock, nil
	}

	err := SetGPIO(factory, "/dev/ttyUSB0", 6, true, 0, "", false, false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "GPIO 6 is reserved (flash)")
	assert.Empty(t, mock.setGPIOCalls, "reserved pin must never reach the underlying drive")
}

// TestSetGPIOIncludeReservedDrives confirms includeReserved=true overrides
// the reserved refusal and reaches the underlying drive.
func TestSetGPIOIncludeReservedDrives(t *testing.T) {
	mock := &mockFlasher{
		gpioReservedFunc: func(pin int) (bool, string) { return true, "flash" },
	}
	factory := func(port string, opts *espflasher.FlasherOptions) (Flasher, error) {
		return mock, nil
	}

	err := SetGPIO(factory, "/dev/ttyUSB0", 6, true, 0, "", false, true)
	require.NoError(t, err)
	require.Len(t, mock.setGPIOCalls, 1)
	assert.Equal(t, 6, mock.setGPIOCalls[0].pin)
}

// TestSetGPIOInputOnlyStillRefusedRegardlessOfIncludeReserved confirms an
// input-only/nonexistent pin (surfaced as a f.SetGPIO error, not
// f.GPIOReserved) is refused by the underlying flasher even when
// includeReserved is true.
func TestSetGPIOInputOnlyStillRefusedRegardlessOfIncludeReserved(t *testing.T) {
	mock := &mockFlasher{setGPIOErr: fmt.Errorf("pin 34 is input-only")}
	factory := func(port string, opts *espflasher.FlasherOptions) (Flasher, error) {
		return mock, nil
	}

	err := SetGPIO(factory, "/dev/ttyUSB0", 34, true, 0, "", false, true)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "input-only")
}

func TestSweepGPIOReservedSkippedByDefault(t *testing.T) {
	withNoGPIOSleep(t)
	mock := &mockFlasher{
		gpioReservedFunc: func(pin int) (bool, string) {
			if pin == 6 {
				return true, "flash"
			}
			return false, ""
		},
	}
	factory := func(port string, opts *espflasher.FlasherOptions) (Flasher, error) {
		return mock, nil
	}

	var phases []string
	status := func(phase string, current, total int) { phases = append(phases, phase) }

	result, err := SweepGPIO(factory, "/dev/ttyUSB0", []int{4, 6}, GPIOSweepOpts{BothPolarities: true, Restore: true}, 0, "", status, false)
	require.NoError(t, err)
	require.Len(t, result.Pins, 2)

	assert.False(t, result.Pins[0].Skipped)
	assert.True(t, result.Pins[1].Skipped)
	assert.Equal(t, "flash", result.Pins[1].Reason)

	// pin 6 (reserved) never had SetGPIO called.
	for _, call := range mock.setGPIOCalls {
		assert.NotEqual(t, 6, call.pin)
	}
	assert.Contains(t, phases, "skipping reserved GPIO 6 (flash)")
}

func TestSweepGPIOIncludeReserved(t *testing.T) {
	withNoGPIOSleep(t)
	mock := &mockFlasher{
		gpioReservedFunc: func(pin int) (bool, string) {
			return true, "flash"
		},
	}
	factory := func(port string, opts *espflasher.FlasherOptions) (Flasher, error) {
		return mock, nil
	}

	result, err := SweepGPIO(factory, "/dev/ttyUSB0", []int{6}, GPIOSweepOpts{BothPolarities: true, Restore: true, IncludeReserved: true}, 0, "", nil, false)
	require.NoError(t, err)
	require.Len(t, result.Pins, 1)
	assert.False(t, result.Pins[0].Skipped)
	require.Len(t, mock.setGPIOCalls, 2)
	assert.Equal(t, 6, mock.setGPIOCalls[0].pin)
}

func TestSweepGPIOSetGPIOErrorSkipsAndContinues(t *testing.T) {
	withNoGPIOSleep(t)
	mock := &mockFlasher{
		setGPIOErr: fmt.Errorf("pin 34 is input-only"),
	}
	factory := func(port string, opts *espflasher.FlasherOptions) (Flasher, error) {
		return mock, nil
	}

	var phases []string
	status := func(phase string, current, total int) { phases = append(phases, phase) }

	result, err := SweepGPIO(factory, "/dev/ttyUSB0", []int{34, 35}, GPIOSweepOpts{BothPolarities: true, Restore: true}, 0, "", status, false)
	require.NoError(t, err)
	require.Len(t, result.Pins, 2)
	assert.True(t, result.Pins[0].Skipped)
	assert.Contains(t, result.Pins[0].Reason, "input-only")
	assert.True(t, result.Pins[1].Skipped)
	// A SetGPIO failure on a non-reserved pin must still emit a status tick,
	// same as the reserved-skip path, so every pin produces exactly one line.
	assert.Equal(t, []string{
		"skipping GPIO 34 (pin 34 is input-only)",
		"skipping GPIO 35 (pin 34 is input-only)",
	}, phases)
}

func TestSweepGPIOLowSetGPIOErrorSkipsAndContinues(t *testing.T) {
	withNoGPIOSleep(t)
	callCount := 0
	mock := &mockFlasher{}
	// Fail only on the second SetGPIO call (the "drive low" call) for each
	// pin, so the "drive high" call succeeds first.
	failErr := fmt.Errorf("low drive failed")
	wrapped := &mockFlasherSetGPIOFailsOnLow{mockFlasher: mock, failErr: failErr, callCount: &callCount}
	factory := func(port string, opts *espflasher.FlasherOptions) (Flasher, error) {
		return wrapped, nil
	}

	result, err := SweepGPIO(factory, "/dev/ttyUSB0", []int{4}, GPIOSweepOpts{BothPolarities: true, Restore: false}, 0, "", nil, false)
	require.NoError(t, err)
	require.Len(t, result.Pins, 1)
	assert.True(t, result.Pins[0].Skipped)
	assert.Contains(t, result.Pins[0].Reason, "low drive failed")
	assert.Empty(t, mock.releaseGPIOCalls)
}

func TestSweepGPIODwellHonored(t *testing.T) {
	durations := withNoGPIOSleep(t)
	mock := &mockFlasher{}
	factory := func(port string, opts *espflasher.FlasherOptions) (Flasher, error) {
		return mock, nil
	}

	_, err := SweepGPIO(factory, "/dev/ttyUSB0", []int{4}, GPIOSweepOpts{BothPolarities: true, Dwell: 250 * time.Millisecond, Restore: true}, 0, "", nil, false)
	require.NoError(t, err)
	require.Len(t, *durations, 2)
	assert.Equal(t, 250*time.Millisecond, (*durations)[0])
	assert.Equal(t, 250*time.Millisecond, (*durations)[1])
}

func TestSweepGPIODwellDefault(t *testing.T) {
	durations := withNoGPIOSleep(t)
	mock := &mockFlasher{}
	factory := func(port string, opts *espflasher.FlasherOptions) (Flasher, error) {
		return mock, nil
	}

	_, err := SweepGPIO(factory, "/dev/ttyUSB0", []int{4}, GPIOSweepOpts{BothPolarities: false, Restore: true}, 0, "", nil, false)
	require.NoError(t, err)
	require.Len(t, *durations, 1)
	assert.Equal(t, 5*time.Second, (*durations)[0])
}

func TestSweepGPIOBothPolaritiesFalse(t *testing.T) {
	withNoGPIOSleep(t)
	mock := &mockFlasher{}
	factory := func(port string, opts *espflasher.FlasherOptions) (Flasher, error) {
		return mock, nil
	}

	var phases []string
	status := func(phase string, current, total int) { phases = append(phases, phase) }

	_, err := SweepGPIO(factory, "/dev/ttyUSB0", []int{4}, GPIOSweepOpts{BothPolarities: false, Restore: true}, 0, "", status, false)
	require.NoError(t, err)
	assert.Equal(t, []string{"driving GPIO 4 high"}, phases)
	require.Len(t, mock.setGPIOCalls, 1)
	assert.True(t, mock.setGPIOCalls[0].level)
}

func TestSweepGPIOStatusSequenceBothPolarities(t *testing.T) {
	withNoGPIOSleep(t)
	mock := &mockFlasher{}
	factory := func(port string, opts *espflasher.FlasherOptions) (Flasher, error) {
		return mock, nil
	}

	var phases []string
	status := func(phase string, current, total int) { phases = append(phases, phase) }

	_, err := SweepGPIO(factory, "/dev/ttyUSB0", []int{4, 5}, GPIOSweepOpts{BothPolarities: true, Restore: true}, 0, "", status, false)
	require.NoError(t, err)
	assert.Equal(t, []string{
		"driving GPIO 4 high", "driving GPIO 4 low",
		"driving GPIO 5 high", "driving GPIO 5 low",
	}, phases)
}

func TestSweepGPIOReleaseCalledOnAdvance(t *testing.T) {
	withNoGPIOSleep(t)
	mock := &mockFlasher{}
	factory := func(port string, opts *espflasher.FlasherOptions) (Flasher, error) {
		return mock, nil
	}

	_, err := SweepGPIO(factory, "/dev/ttyUSB0", []int{4, 5}, GPIOSweepOpts{BothPolarities: true, Restore: true}, 0, "", nil, false)
	require.NoError(t, err)
	assert.Equal(t, []int{4, 5}, mock.releaseGPIOCalls)
}

func TestSweepGPIORestoreFalseSkipsRelease(t *testing.T) {
	withNoGPIOSleep(t)
	mock := &mockFlasher{}
	factory := func(port string, opts *espflasher.FlasherOptions) (Flasher, error) {
		return mock, nil
	}

	_, err := SweepGPIO(factory, "/dev/ttyUSB0", []int{4}, GPIOSweepOpts{BothPolarities: true, Restore: false}, 0, "", nil, false)
	require.NoError(t, err)
	assert.Empty(t, mock.releaseGPIOCalls)
}

func TestSweepGPIOSingleConnection(t *testing.T) {
	withNoGPIOSleep(t)
	mock := &mockFlasher{}
	openCount := 0
	factory := func(port string, opts *espflasher.FlasherOptions) (Flasher, error) {
		openCount++
		return mock, nil
	}

	_, err := SweepGPIO(factory, "/dev/ttyUSB0", []int{4, 5, 6}, GPIOSweepOpts{BothPolarities: true, Restore: true}, 0, "", nil, false)
	require.NoError(t, err)
	assert.Equal(t, 1, openCount)
	assert.True(t, mock.closeCalled)
}

func TestSweepGPIONoResetByDefault(t *testing.T) {
	withNoGPIOSleep(t)
	mock := &mockFlasher{}
	factory := func(port string, opts *espflasher.FlasherOptions) (Flasher, error) {
		return mock, nil
	}

	_, err := SweepGPIO(factory, "/dev/ttyUSB0", []int{4}, GPIOSweepOpts{BothPolarities: true, Restore: true}, 0, "", nil, false)
	require.NoError(t, err)
	assert.True(t, mock.closeCalled)
	assert.False(t, mock.resetCalled)
}

func TestSweepGPIOResetAfter(t *testing.T) {
	withNoGPIOSleep(t)
	mock := &mockFlasher{}
	factory := func(port string, opts *espflasher.FlasherOptions) (Flasher, error) {
		return mock, nil
	}

	_, err := SweepGPIO(factory, "/dev/ttyUSB0", []int{4}, GPIOSweepOpts{BothPolarities: true, Restore: true}, 0, "", nil, true)
	require.NoError(t, err)
	assert.True(t, mock.closeCalled)
	assert.Equal(t, 1, mock.resetCallCount)
}

func TestSweepGPIONilPinsSweepsDrivableDefaultSet(t *testing.T) {
	withNoGPIOSleep(t)
	// Mock a small chip: pins 0-9 are the only ones that "exist" (>=10 is
	// nonexistent), pin 1 is reserved (e.g. flash), everything else in range
	// is drivable.
	mock := &mockFlasher{
		gpioReservedFunc: func(pin int) (bool, string) {
			if pin >= 10 {
				return true, "nonexistent"
			}
			if pin == 1 {
				return true, "flash"
			}
			return false, ""
		},
	}
	factory := func(port string, opts *espflasher.FlasherOptions) (Flasher, error) {
		return mock, nil
	}

	var phases []string
	status := func(phase string, current, total int) { phases = append(phases, phase) }

	result, err := SweepGPIO(factory, "/dev/ttyUSB0", nil, GPIOSweepOpts{BothPolarities: false, Restore: true}, 0, "", status, false)
	require.NoError(t, err)

	wantPins := []int{0, 2, 3, 4, 5, 6, 7, 8, 9}
	require.Len(t, result.Pins, len(wantPins))
	for i, want := range wantPins {
		assert.Equal(t, want, result.Pins[i].Pin)
		assert.False(t, result.Pins[i].Skipped)
	}

	// Reserved (pin 1) and nonexistent (pins 10+) pins never had SetGPIO
	// called.
	for _, call := range mock.setGPIOCalls {
		assert.NotEqual(t, 1, call.pin)
		assert.Less(t, call.pin, 10)
	}

	require.NotEmpty(t, phases)
	assert.Equal(t, fmt.Sprintf("sweeping %d drivable pins", len(wantPins)), phases[0])
}

func TestSweepGPIONilPinsSweepDefaultSetReachesS3Ceiling(t *testing.T) {
	withNoGPIOSleep(t)
	// Mock reports every pin up to and including 48 (ESP32-S3's max GPIO) as
	// drivable, so the default sweep's ceiling must reach 48 to include it.
	mock := &mockFlasher{
		gpioReservedFunc: func(pin int) (bool, string) {
			if pin > 48 {
				return true, "nonexistent"
			}
			return false, ""
		},
	}
	factory := func(port string, opts *espflasher.FlasherOptions) (Flasher, error) {
		return mock, nil
	}

	result, err := SweepGPIO(factory, "/dev/ttyUSB0", nil, GPIOSweepOpts{BothPolarities: false, Restore: true}, 0, "", nil, false)
	require.NoError(t, err)
	require.NotEmpty(t, result.Pins)
	assert.Equal(t, 48, result.Pins[len(result.Pins)-1].Pin)
}

func TestSweepGPIOEmptyPinsSweepsDrivableDefaultSet(t *testing.T) {
	withNoGPIOSleep(t)
	mock := &mockFlasher{
		gpioReservedFunc: func(pin int) (bool, string) {
			return pin >= 3, "nonexistent"
		},
	}
	factory := func(port string, opts *espflasher.FlasherOptions) (Flasher, error) {
		return mock, nil
	}

	result, err := SweepGPIO(factory, "/dev/ttyUSB0", []int{}, GPIOSweepOpts{BothPolarities: false, Restore: true}, 0, "", nil, false)
	require.NoError(t, err)
	require.Len(t, result.Pins, 3)
	assert.Equal(t, []int{0, 1, 2}, []int{result.Pins[0].Pin, result.Pins[1].Pin, result.Pins[2].Pin})
}

func TestSweepGPIOFactoryError(t *testing.T) {
	factory := func(port string, opts *espflasher.FlasherOptions) (Flasher, error) {
		return nil, os.ErrPermission
	}

	_, err := SweepGPIO(factory, "/dev/ttyUSB0", []int{4}, GPIOSweepOpts{}, 0, "", nil, false)
	require.Error(t, err)
}

func TestParsePinList(t *testing.T) {
	t.Run("single pin", func(t *testing.T) {
		pins, err := ParsePinList("4")
		require.NoError(t, err)
		assert.Equal(t, []int{4}, pins)
	})

	t.Run("comma list", func(t *testing.T) {
		pins, err := ParsePinList("4,16,17")
		require.NoError(t, err)
		assert.Equal(t, []int{4, 16, 17}, pins)
	})

	t.Run("range", func(t *testing.T) {
		pins, err := ParsePinList("0-21")
		require.NoError(t, err)
		require.Len(t, pins, 22)
		assert.Equal(t, 0, pins[0])
		assert.Equal(t, 21, pins[21])
	})

	t.Run("mixed list and range", func(t *testing.T) {
		pins, err := ParsePinList("4,16-17,21")
		require.NoError(t, err)
		assert.Equal(t, []int{4, 16, 17, 21}, pins)
	})

	t.Run("invalid range end before start", func(t *testing.T) {
		_, err := ParsePinList("21-4")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "end before start")
	})

	t.Run("invalid range non-numeric", func(t *testing.T) {
		_, err := ParsePinList("a-4")
		require.Error(t, err)
	})

	t.Run("invalid pin non-numeric", func(t *testing.T) {
		_, err := ParsePinList("abc")
		require.Error(t, err)
	})

	t.Run("range exceeds MaxSweepPins", func(t *testing.T) {
		_, err := ParsePinList("0-999999999")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "exceeds max")
	})

	t.Run("flat list exceeds MaxSweepPins", func(t *testing.T) {
		parts := make([]string, MaxSweepPins+1)
		for i := range parts {
			parts[i] = strconv.Itoa(i)
		}
		_, err := ParsePinList(strings.Join(parts, ","))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "too many pins")
	})

	t.Run("empty input", func(t *testing.T) {
		_, err := ParsePinList("")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no pins specified")
	})

	t.Run("dup and order preservation", func(t *testing.T) {
		pins, err := ParsePinList("5,3,5,1")
		require.NoError(t, err)
		assert.Equal(t, []int{5, 3, 5, 1}, pins)
	})
}
