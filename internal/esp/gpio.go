package esp

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	espflasher "tinygo.org/x/espflasher/pkg/espflasher"
)

// gpioSleep is the sleep function SweepGPIO uses to honor dwell durations.
// Overridden in tests to avoid real sleeps.
var gpioSleep = time.Sleep

// GPIOReadResult reports the level read from a single GPIO pin.
type GPIOReadResult struct {
	Pin   int  `json:"pin"`
	Level bool `json:"level"`
}

// GPIOSweepOpts configures a GPIOSweep operation.
type GPIOSweepOpts struct {
	// BothPolarities drives each pin high then low (default true when unset
	// via NewGPIOSweepOpts / zero-value callers should set explicitly).
	BothPolarities bool
	// Dwell is how long to hold each polarity before advancing. Defaults to
	// 5s when zero.
	Dwell time.Duration
	// IncludeReserved, when true, sweeps pins GPIOReserved flags instead of
	// skipping them.
	IncludeReserved bool
	// Restore, when true (default), releases each pin (disables the output
	// driver, restores input/hi-Z) before advancing to the next pin.
	Restore bool
}

// GPIOPinOutcome records what happened for a single pin during a sweep.
type GPIOPinOutcome struct {
	Pin     int    `json:"pin"`
	Skipped bool   `json:"skipped"`
	Reason  string `json:"reason,omitempty"`
}

// GPIOSweepResult reports the per-pin outcome of a GPIOSweep operation.
type GPIOSweepResult struct {
	Pins []GPIOPinOutcome `json:"pins"`
}

// ReadGPIO reads the current level of a single GPIO pin over a fresh
// connection. By default the chip is left in the ROM/download state on
// exit (no reset); pass resetAfter=true to reboot into the app before the
// port closes.
func ReadGPIO(factory FlasherFactory, port string, pin int, baudRate int, resetMode string, resetAfter bool) (GPIOReadResult, error) {
	if baudRate == 0 {
		baudRate = 115200
	}

	flashOpts := espflasher.DefaultOptions()
	flashOpts.BaudRate = baudRate
	flashOpts.ChipType = espflasher.ChipAuto
	flashOpts.ResetMode = parseResetMode(resetMode)
	// Register-level GPIO ops go straight through the ROM bootloader; they
	// don't need the stub loader uploaded, so skip it to save a connect
	// round trip.
	flashOpts.SkipStub = true

	f, err := factory(port, flashOpts)
	if err != nil {
		return GPIOReadResult{}, err
	}
	defer func() {
		if resetAfter {
			f.Reset()
		}
		_ = f.Close()
	}()

	level, err := f.ReadGPIO(pin)
	if err != nil {
		return GPIOReadResult{}, err
	}

	return GPIOReadResult{Pin: pin, Level: level}, nil
}

// SetGPIO drives a single GPIO pin high or low over a fresh connection. By
// default the chip is left in the ROM/download state on exit (no reset);
// pass resetAfter=true to reboot into the app before the port closes.
// Reserved pins (flash/PSRAM, strapping, UART0, USB-JTAG — per
// f.GPIOReserved) are refused unless includeReserved is true, mirroring
// SweepGPIO's IncludeReserved gate; input-only/nonexistent pins remain
// hard-refused by the underlying f.SetGPIO regardless of includeReserved.
func SetGPIO(factory FlasherFactory, port string, pin int, level bool, baudRate int, resetMode string, resetAfter bool, includeReserved bool) error {
	if baudRate == 0 {
		baudRate = 115200
	}

	flashOpts := espflasher.DefaultOptions()
	flashOpts.BaudRate = baudRate
	flashOpts.ChipType = espflasher.ChipAuto
	flashOpts.ResetMode = parseResetMode(resetMode)
	// Register-level GPIO ops go straight through the ROM bootloader; they
	// don't need the stub loader uploaded, so skip it to save a connect
	// round trip.
	flashOpts.SkipStub = true

	f, err := factory(port, flashOpts)
	if err != nil {
		return err
	}
	defer func() {
		if resetAfter {
			f.Reset()
		}
		_ = f.Close()
	}()

	if reserved, reason := f.GPIOReserved(pin); reserved && !includeReserved {
		return fmt.Errorf("GPIO %d is reserved (%s); pass include_reserved to override", pin, reason)
	}

	return f.SetGPIO(pin, level)
}

// SweepGPIO opens a single flasher connection and drives each pin in turn,
// dwelling on each polarity, reporting per-pin status via status and
// recording a per-pin outcome. Reserved pins are skipped unless
// opts.IncludeReserved is set. A SetGPIO failure (e.g. an input-only pin) is
// recorded as skipped-with-reason and the sweep continues with the next pin.
// By default the chip is left in the ROM/download state on exit (no reset);
// pass resetAfter=true to reboot into the app before the port closes.
func SweepGPIO(factory FlasherFactory, port string, pins []int, opts GPIOSweepOpts, baudRate int, resetMode string, status StatusFunc, resetAfter bool) (GPIOSweepResult, error) {
	if baudRate == 0 {
		baudRate = 115200
	}
	if opts.Dwell == 0 {
		opts.Dwell = 5 * time.Second
	}

	flashOpts := espflasher.DefaultOptions()
	flashOpts.BaudRate = baudRate
	flashOpts.ChipType = espflasher.ChipAuto
	flashOpts.ResetMode = parseResetMode(resetMode)
	// Register-level GPIO ops go straight through the ROM bootloader; they
	// don't need the stub loader uploaded, so skip it to save a connect
	// round trip.
	flashOpts.SkipStub = true

	f, err := factory(port, flashOpts)
	if err != nil {
		return GPIOSweepResult{}, err
	}
	defer func() {
		if resetAfter {
			f.Reset()
		}
		_ = f.Close()
	}()

	if len(pins) == 0 {
		pins = defaultSweepPins(f)
		if status != nil {
			status(fmt.Sprintf("sweeping %d drivable pins", len(pins)), 0, len(pins))
		}
	}

	result := GPIOSweepResult{Pins: make([]GPIOPinOutcome, 0, len(pins))}

	for i, pin := range pins {
		if reserved, reason := f.GPIOReserved(pin); reserved && !opts.IncludeReserved {
			emitStatus(status, fmt.Sprintf("skipping reserved GPIO %d (%s)", pin, reason))
			result.Pins = append(result.Pins, GPIOPinOutcome{Pin: pin, Skipped: true, Reason: reason})
			continue
		}

		if err := f.SetGPIO(pin, true); err != nil {
			if status != nil {
				status(fmt.Sprintf("skipping GPIO %d (%v)", pin, err), i+1, len(pins))
			}
			result.Pins = append(result.Pins, GPIOPinOutcome{Pin: pin, Skipped: true, Reason: err.Error()})
			continue
		}
		if status != nil {
			status(fmt.Sprintf("driving GPIO %d high", pin), i+1, len(pins))
		}
		gpioSleep(opts.Dwell)

		if opts.BothPolarities {
			if err := f.SetGPIO(pin, false); err != nil {
				result.Pins = append(result.Pins, GPIOPinOutcome{Pin: pin, Skipped: true, Reason: err.Error()})
				if opts.Restore {
					_ = f.ReleaseGPIO(pin)
				}
				continue
			}
			if status != nil {
				status(fmt.Sprintf("driving GPIO %d low", pin), i+1, len(pins))
			}
			gpioSleep(opts.Dwell)
		}

		if opts.Restore {
			_ = f.ReleaseGPIO(pin)
		}

		result.Pins = append(result.Pins, GPIOPinOutcome{Pin: pin})
	}

	return result, nil
}

// maxCandidateGPIOPin is the highest GPIO number any supported ESP chip
// exposes. ESP32-S3 tops out at GPIO48, the largest of any chip this tool
// targets, so 0..48 covers every chip's numbering space; f.GPIOReserved
// filters each candidate down to what the auto-detected chip actually
// drives.
const maxCandidateGPIOPin = 48

// defaultSweepPins enumerates the full drivable pin set for the chip f is
// connected to: every candidate pin number in range that f.GPIOReserved does
// not flag as reserved. GPIOReserved is a pure local classification against
// the chip's pin definition table (no serial I/O), so this enumeration is
// cheap. It also reports "nonexistent" (reserved=true) for pin numbers a
// smaller chip doesn't have, so gaps below maxCandidateGPIOPin are excluded
// along with UART0/flash/strap/input-only pins. Any pin that slips through
// (e.g. a chip-specific quirk GPIOReserved doesn't model) is still caught by
// the SetGPIO-error skip in the sweep loop below.
func defaultSweepPins(f Flasher) []int {
	var pins []int
	for pin := 0; pin <= maxCandidateGPIOPin; pin++ {
		if reserved, _ := f.GPIOReserved(pin); !reserved {
			pins = append(pins, pin)
		}
	}
	return pins
}

// MaxSweepPins caps the total number of pins ParsePinList will expand a
// list/range into. No ESP chip exceeds GPIO46, so any parsed count beyond
// this is a malformed range (e.g. "0-999999999") rather than a legitimate
// sweep, and is rejected before the unbounded allocation it would otherwise
// require. Shared by the CLI (internal/cli/gpio.go) and the esp_gpio_sweep
// MCP tool so both surfaces reject oversized ranges identically.
const MaxSweepPins = 64

// ParsePinList parses a comma-separated list of pins and/or inclusive
// ranges, e.g. "4,16,17" or "0-21" or "4,16-17,21". Duplicates and ordering
// from the input are preserved. Shared by the CLI gpio subcommands and the
// esp_gpio_sweep MCP tool so pin-list syntax stays identical across both
// surfaces.
func ParsePinList(s string) ([]int, error) {
	var pins []int
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if lo, hi, ok := strings.Cut(part, "-"); ok {
			loN, err := strconv.Atoi(strings.TrimSpace(lo))
			if err != nil {
				return nil, fmt.Errorf("invalid pin range %q: %w", part, err)
			}
			hiN, err := strconv.Atoi(strings.TrimSpace(hi))
			if err != nil {
				return nil, fmt.Errorf("invalid pin range %q: %w", part, err)
			}
			if hiN < loN {
				return nil, fmt.Errorf("invalid pin range %q: end before start", part)
			}
			if hiN-loN+1 > MaxSweepPins {
				return nil, fmt.Errorf("invalid pin range %q: spans %d pins, exceeds max of %d", part, hiN-loN+1, MaxSweepPins)
			}
			for p := loN; p <= hiN; p++ {
				pins = append(pins, p)
			}
			continue
		}
		p, err := strconv.Atoi(part)
		if err != nil {
			return nil, fmt.Errorf("invalid pin %q: %w", part, err)
		}
		pins = append(pins, p)
	}
	if len(pins) == 0 {
		return nil, fmt.Errorf("no pins specified")
	}
	if len(pins) > MaxSweepPins {
		return nil, fmt.Errorf("too many pins specified: %d, exceeds max of %d", len(pins), MaxSweepPins)
	}
	return pins, nil
}
