package mockhw

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.bug.st/serial"
)

// TestVirtualMonitorPortBootBanner drives a fresh virtualMonitorPort
// directly (no session/mockhw wiring): the seeded boot banner must be
// readable immediately after construction.
func TestVirtualMonitorPortBootBanner(t *testing.T) {
	v := newVirtualMonitorPort()

	buf := make([]byte, 4096)
	var got []byte
	// Drain until the seeded banner has been fully read; Read never blocks
	// indefinitely (idle sleep + (0, nil)) so bound the loop.
	for i := 0; i < 100 && len(got) < 1; i++ {
		n, err := v.Read(buf)
		require.NoError(t, err)
		got = append(got, buf[:n]...)
	}

	text := string(got)
	for _, line := range bootBanner {
		assert.Contains(t, text, line)
	}
}

// TestVirtualMonitorPortWriteEchoesAndCaptures asserts Write both echoes
// the written bytes back onto the outbound queue (loopback, what
// serial_read observes) and records them in the capture buffer (what an
// in-process test can assert exact bytes against).
func TestVirtualMonitorPortWriteEchoesAndCaptures(t *testing.T) {
	v := newVirtualMonitorPort()

	// Drain the seeded boot banner first so it doesn't interfere with the
	// echo assertion below.
	drain := make([]byte, 4096)
	for i := 0; i < 100; i++ {
		n, err := v.Read(drain)
		require.NoError(t, err)
		if n == 0 {
			break
		}
	}

	payload := []byte("PING-mock-test\n")
	n, err := v.Write(payload)
	require.NoError(t, err)
	assert.Equal(t, len(payload), n)

	assert.Equal(t, payload, v.Captured())

	buf := make([]byte, 4096)
	var got []byte
	for i := 0; i < 100 && len(got) < len(payload); i++ {
		rn, rerr := v.Read(buf)
		require.NoError(t, rerr)
		got = append(got, buf[:rn]...)
	}
	assert.Equal(t, payload, got)
}

// TestVirtualMonitorPortReadIdleNeverBlocksOrEOF asserts Read on an empty
// outbound queue returns (0, nil) promptly rather than blocking
// indefinitely or returning io.EOF -- the exact contract
// session.Manager.readLoop depends on.
func TestVirtualMonitorPortReadIdleNeverBlocksOrEOF(t *testing.T) {
	v := newVirtualMonitorPort()

	// Drain the seeded banner.
	drain := make([]byte, 4096)
	for i := 0; i < 100; i++ {
		n, err := v.Read(drain)
		require.NoError(t, err)
		if n == 0 {
			break
		}
	}

	require.NoError(t, v.SetReadTimeout(5*time.Millisecond))

	start := time.Now()
	n, err := v.Read(drain)
	elapsed := time.Since(start)

	assert.NoError(t, err)
	assert.Equal(t, 0, n)
	assert.Less(t, elapsed, time.Second, "idle Read must not block indefinitely")
}

// TestVirtualMonitorPortNoOpMethods drives every no-op method to keep them
// covered and pins their nil-error contract.
func TestVirtualMonitorPortNoOpMethods(t *testing.T) {
	v := newVirtualMonitorPort()

	assert.NoError(t, v.SetMode(&serial.Mode{}))
	assert.NoError(t, v.Drain())
	assert.NoError(t, v.ResetInputBuffer())
	assert.NoError(t, v.ResetOutputBuffer())
	assert.NoError(t, v.SetDTR(true))
	assert.NoError(t, v.SetRTS(true))
	bits, err := v.GetModemStatusBits()
	assert.NoError(t, err)
	assert.Nil(t, bits)
	assert.NoError(t, v.Close())
	assert.NoError(t, v.Break(time.Millisecond))
}

var _ serial.Port = (*virtualMonitorPort)(nil)
