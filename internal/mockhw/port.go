package mockhw

import (
	"sync"
	"time"

	"go.bug.st/serial"
)

// virtualPort implements go.bug.st/serial.Port entirely in-process, backed
// by a chipProfile + registerFile. It speaks just enough of the ESP ROM
// bootloader's SLIP-framed protocol (SYNC, READ_REG, WRITE_REG,
// GET_SECURITY_INFO, SPI_ATTACH, SPI_SET_PARAMS, CHANGE_BAUD) for
// espflasher's SkipStub register-only path to connect, detect the chip via
// magic value, and drive GPIO/FlashID register sequences — no hardware, no
// subprocess, no real serial device.
//
// Only Read/Write/SetReadTimeout/SetMode/ResetInputBuffer have real
// behavior; the rest are nil no-ops matching an idle-but-present port.
type virtualPort struct {
	mu sync.Mutex

	profile *chipProfile
	regs    *registerFile

	in  []byte // bytes written by the caller, awaiting a complete SLIP frame
	out []byte // SLIP-encoded response bytes awaiting Read

	readTimeout time.Duration
}

var _ serial.Port = (*virtualPort)(nil)

// newVirtualPort creates a virtual ESP chip for profile p.
func newVirtualPort(p *chipProfile) *virtualPort {
	return &virtualPort{
		profile: p,
		regs:    newRegisterFile(),
	}
}

// SetMode is a no-op; the virtual chip has no real UART parameters to
// configure.
func (v *virtualPort) SetMode(mode *serial.Mode) error { return nil }

// Read copies buffered response bytes into p when available. When the
// output buffer is empty it sleeps briefly (never blocking indefinitely,
// never returning io.EOF) and returns (0, nil) — the exact contract
// espflasher's slipReader.ReadFrame needs: n==0 means "keep waiting" until
// its own deadline.
func (v *virtualPort) Read(p []byte) (int, error) {
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

// Write accumulates incoming bytes and repeatedly extracts complete
// 0xC0...0xC0 SLIP frames — this works identically for a one-shot UART
// write and for 64-byte-chunked USB writes, since bytes are buffered until
// a full frame is present. Each decoded frame is dispatched and its
// SLIP-encoded response appended to the output buffer for Read to drain.
func (v *virtualPort) Write(p []byte) (int, error) {
	v.mu.Lock()
	defer v.mu.Unlock()

	v.in = append(v.in, p...)

	for {
		frame, rest, ok := extractFrame(v.in)
		if !ok {
			break
		}
		v.in = rest

		resp := v.dispatch(slipDecode(frame))
		if resp != nil {
			v.out = append(v.out, slipEncode(resp)...)
		}
	}

	return len(p), nil
}

// Drain is a no-op; writes are applied synchronously.
func (v *virtualPort) Drain() error { return nil }

// ResetInputBuffer clears both the pending-request and pending-response
// buffers.
func (v *virtualPort) ResetInputBuffer() error {
	v.mu.Lock()
	v.in = nil
	v.out = nil
	v.mu.Unlock()
	return nil
}

// ResetOutputBuffer is a no-op; there is no separate host-side write buffer
// to purge.
func (v *virtualPort) ResetOutputBuffer() error { return nil }

// SetDTR is a no-op; the virtual chip has no reset line to toggle.
func (v *virtualPort) SetDTR(dtr bool) error { return nil }

// SetRTS is a no-op; the virtual chip has no reset line to toggle.
func (v *virtualPort) SetRTS(rts bool) error { return nil }

// GetModemStatusBits is a no-op; the virtual chip has no modem status bits.
func (v *virtualPort) GetModemStatusBits() (*serial.ModemStatusBits, error) { return nil, nil }

// SetReadTimeout stores the timeout used to bound Read's idle sleep.
func (v *virtualPort) SetReadTimeout(t time.Duration) error {
	v.mu.Lock()
	v.readTimeout = t
	v.mu.Unlock()
	return nil
}

// Close is a no-op; there is no real file descriptor to release.
func (v *virtualPort) Close() error { return nil }

// Break is a no-op; the virtual chip has no line to hold low.
func (v *virtualPort) Break(t time.Duration) error { return nil }
