package mockhw

import (
	"sync"
	"time"

	"go.bug.st/serial"
)

// bootBanner is the synthetic boot-log a virtualMonitorPort emits as soon
// as it's opened, so a serial_start immediately followed by serial_read has
// something real to observe. Deliberately generic and obviously synthetic
// (mentions "mock"/"virtual") — not a copy of any real device's boot log.
var bootBanner = []string{
	"mock-esp32: virtual boot, reset reason=POWERON",
	"mock-esp32: virtual chip ready",
}

// virtualMonitorPort implements go.bug.st/serial.Port as a hardware-free
// stand-in for the monitor path (session.Manager.readLoop reading raw
// bytes, splitting on '\n'), separate from virtualPort (port.go), which
// speaks the ESP ROM bootloader's SLIP-framed register protocol for the
// flasher path. A virtualMonitorPort has no SLIP/register model at all: it
// seeds a canned boot banner on construction, then echoes every Write back
// onto its outbound queue (loopback), which is enough for the monitor
// scenarios (serial_start/read/write/stop) to observe real behavior without
// a board.
//
// Read/Write run on different goroutines (Manager.readLoop vs the
// serial_write handler), so the outbound queue and capture buffer are
// mutex-guarded.
type virtualMonitorPort struct {
	mu sync.Mutex

	out     []byte // bytes awaiting Read, '\n'-terminated lines
	capture []byte // copy of every byte ever Write()n, for test assertions

	readTimeout time.Duration
}

var _ serial.Port = (*virtualMonitorPort)(nil)

// newVirtualMonitorPort creates a virtual monitor port pre-seeded with
// bootBanner.
func newVirtualMonitorPort() *virtualMonitorPort {
	v := &virtualMonitorPort{}
	for _, line := range bootBanner {
		v.out = append(v.out, line...)
		v.out = append(v.out, '\n')
	}
	return v
}

// SetMode is a no-op; the virtual monitor has no real UART parameters to
// configure.
func (v *virtualMonitorPort) SetMode(mode *serial.Mode) error { return nil }

// Read copies buffered outbound bytes into p when available. When the
// outbound queue is empty it sleeps briefly (never blocking indefinitely,
// never returning io.EOF) and returns (0, nil) -- the contract
// Manager.readLoop needs: n==0 means "nothing yet, keep polling".
func (v *virtualMonitorPort) Read(p []byte) (int, error) {
	v.mu.Lock()
	if len(v.out) > 0 {
		n := copy(p, v.out)
		v.out = v.out[n:]
		v.mu.Unlock()
		return n, nil
	}
	timeout := v.readTimeout
	v.mu.Unlock()

	sleep := 2 * time.Millisecond
	if timeout > 0 && timeout < sleep {
		sleep = timeout
	}
	time.Sleep(sleep)
	return 0, nil
}

// Write captures a copy of p (for optional in-process assertion) and
// echoes p back onto the outbound queue (loopback), so a serial_write
// observably round-trips through the next serial_read.
func (v *virtualMonitorPort) Write(p []byte) (int, error) {
	v.mu.Lock()
	defer v.mu.Unlock()

	captured := make([]byte, len(p))
	copy(captured, p)
	v.capture = append(v.capture, captured...)

	echoed := make([]byte, len(p))
	copy(echoed, p)
	v.out = append(v.out, echoed...)

	return len(p), nil
}

// Captured returns a copy of every byte written to this port so far, for
// tests that want to assert exact bytes rather than relying solely on the
// Read-side loopback.
func (v *virtualMonitorPort) Captured() []byte {
	v.mu.Lock()
	defer v.mu.Unlock()
	out := make([]byte, len(v.capture))
	copy(out, v.capture)
	return out
}

// Drain is a no-op; writes are applied synchronously.
func (v *virtualMonitorPort) Drain() error { return nil }

// ResetInputBuffer clears the outbound queue.
func (v *virtualMonitorPort) ResetInputBuffer() error {
	v.mu.Lock()
	v.out = nil
	v.mu.Unlock()
	return nil
}

// ResetOutputBuffer is a no-op; there is no separate host-side write buffer
// to purge.
func (v *virtualMonitorPort) ResetOutputBuffer() error { return nil }

// SetDTR is a no-op; the virtual monitor has no reset line to toggle.
func (v *virtualMonitorPort) SetDTR(dtr bool) error { return nil }

// SetRTS is a no-op; the virtual monitor has no reset line to toggle.
func (v *virtualMonitorPort) SetRTS(rts bool) error { return nil }

// GetModemStatusBits is a no-op; the virtual monitor has no modem status
// bits.
func (v *virtualMonitorPort) GetModemStatusBits() (*serial.ModemStatusBits, error) {
	return nil, nil
}

// SetReadTimeout stores the timeout used to bound Read's idle sleep.
func (v *virtualMonitorPort) SetReadTimeout(t time.Duration) error {
	v.mu.Lock()
	v.readTimeout = t
	v.mu.Unlock()
	return nil
}

// Close is a no-op; there is no real file descriptor to release.
func (v *virtualMonitorPort) Close() error { return nil }

// Break is a no-op; the virtual monitor has no line to hold low.
func (v *virtualMonitorPort) Break(t time.Duration) error { return nil }
