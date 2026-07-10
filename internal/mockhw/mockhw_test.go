package mockhw

import (
	"encoding/binary"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.bug.st/serial"
	"tinygo.org/x/espflasher/pkg/espflasher"
)

// TestVirtualPortAgainstRealEspflasher is the load-bearing proof: it drives
// real espflasher (SkipStub, register-only path) against the virtual chip
// in-process, with no hardware and no subprocess. If this passes, the
// dispatcher/register model is wire-compatible with espflasher's actual
// protocol implementation, not just with assumptions about it.
func TestVirtualPortAgainstRealEspflasher(t *testing.T) {
	opts := espflasher.DefaultOptions()
	opts.ChipType = espflasher.ChipAuto
	opts.ResetMode = espflasher.ResetNoReset
	opts.SkipStub = true
	opts.SerialOpener = func(string, *serial.Mode) (serial.Port, error) {
		return newVirtualPort(profileESP32S2), nil
	}

	f, err := espflasher.New(MockPortName, opts)
	require.NoError(t, err)
	t.Cleanup(func() { _ = f.Close() })

	assert.Equal(t, "ESP32-S2", f.ChipName())

	require.NoError(t, f.SetGPIO(15, true))
	level, err := f.ReadGPIO(15)
	require.NoError(t, err)
	assert.True(t, level)

	require.NoError(t, f.SetGPIO(15, false))
	level, err = f.ReadGPIO(15)
	require.NoError(t, err)
	assert.False(t, level)

	reserved, reason := f.GPIOReserved(0)
	assert.True(t, reserved)
	assert.Equal(t, "strap", reason)

	_, _, err = f.FlashID()
	assert.NoError(t, err)
}

// TestVirtualPortInterfaceCompliance pins down that virtualPort implements
// the full serial.Port surface at compile time (also asserted as a package
// var in port.go); this test exists so a future signature drift in
// go.bug.st/serial fails loudly here too.
func TestVirtualPortInterfaceCompliance(t *testing.T) {
	var p serial.Port = newVirtualPort(profileESP32S2)
	require.NotNil(t, p)
}

// TestSLIPFraming exercises slipEncode/slipDecode/extractFrame directly:
// escaping of both special bytes, and frame extraction from a buffer that
// accumulated multiple 64-byte USB-style chunks.
func TestSLIPFraming(t *testing.T) {
	t.Run("round trip with escapes", func(t *testing.T) {
		data := []byte{0x00, 0xC0, 0x01, 0xDB, 0x02}
		encoded := slipEncode(data)

		assert.Equal(t, byte(0xC0), encoded[0])
		assert.Equal(t, byte(0xC0), encoded[len(encoded)-1])
		// 0xC0 -> 0xDB 0xDC, 0xDB -> 0xDB 0xDD: 5 data bytes + 2 escaped
		// bytes (each growing by 1) + 2 delimiters = 9.
		assert.Len(t, encoded, 9)

		decoded := slipDecode(encoded)
		assert.Equal(t, data, decoded)
	})

	t.Run("extractFrame single frame", func(t *testing.T) {
		frame := slipEncode([]byte{0xAA, 0xBB})
		got, rest, ok := extractFrame(frame)
		assert.True(t, ok)
		assert.Equal(t, frame, got)
		assert.Empty(t, rest)
	})

	t.Run("extractFrame chunked accumulation", func(t *testing.T) {
		frame := slipEncode([]byte{0xAA, 0xBB, 0xCC})
		// Simulate 64-byte-chunked USB writes landing in the buffer in two
		// pieces, mid-frame.
		var buf []byte
		buf = append(buf, frame[:2]...)
		got, _, ok := extractFrame(buf)
		assert.False(t, ok, "incomplete frame must not extract")
		assert.Nil(t, got)

		buf = append(buf, frame[2:]...)
		got, rest, ok := extractFrame(buf)
		assert.True(t, ok)
		assert.Equal(t, frame, got)
		assert.Empty(t, rest)
	})

	t.Run("extractFrame back-to-back frames", func(t *testing.T) {
		f1 := slipEncode([]byte{0x01})
		f2 := slipEncode([]byte{0x02})
		buf := append(append([]byte{}, f1...), f2...)

		got1, rest, ok := extractFrame(buf)
		require.True(t, ok)
		assert.Equal(t, f1, got1)

		got2, rest2, ok := extractFrame(rest)
		require.True(t, ok)
		assert.Equal(t, f2, got2)
		assert.Empty(t, rest2)
	})
}

// TestDispatchOpcodes table-tests each opcode's response bytes directly
// against virtualPort.dispatch, independent of espflasher.
func TestDispatchOpcodes(t *testing.T) {
	reqHeader := func(opcode byte, data []byte) []byte {
		pkt := make([]byte, 8+len(data))
		pkt[0] = dirRequest
		pkt[1] = opcode
		binary.LittleEndian.PutUint16(pkt[2:4], uint16(len(data)))
		binary.LittleEndian.PutUint32(pkt[4:8], 0) // checksum unused by dispatch
		copy(pkt[8:], data)
		return pkt
	}

	t.Run("SYNC acks with value 0", func(t *testing.T) {
		v := newVirtualPort(profileESP32S2)
		resp := v.dispatch(reqHeader(opSync, make([]byte, 36)))
		require.NotNil(t, resp)
		assert.Equal(t, dirResponse, resp[0])
		assert.Equal(t, opSync, resp[1])
		assert.Equal(t, uint32(0), binary.LittleEndian.Uint32(resp[4:8]))
		assert.Equal(t, []byte{0x00, 0x00}, resp[8:])
	})

	t.Run("GET_SECURITY_INFO returns error status", func(t *testing.T) {
		v := newVirtualPort(profileESP32S2)
		resp := v.dispatch(reqHeader(opSecurityInfoReg, make([]byte, 20)))
		require.NotNil(t, resp)
		assert.NotEqual(t, byte(0), resp[8], "status byte must be non-zero")
	})

	t.Run("READ_REG magic value", func(t *testing.T) {
		v := newVirtualPort(profileESP32S2)
		addr := make([]byte, 4)
		binary.LittleEndian.PutUint32(addr, profileESP32S2.magicRegAddr)
		resp := v.dispatch(reqHeader(opReadReg, addr))
		require.NotNil(t, resp)
		assert.Equal(t, []byte{0x00, 0x00}, resp[8:])
		assert.Equal(t, profileESP32S2.magicValue, binary.LittleEndian.Uint32(resp[4:8]))
	})

	t.Run("WRITE_REG then READ_REG round trip via OUT/IN", func(t *testing.T) {
		v := newVirtualPort(profileESP32S2)

		writeData := make([]byte, 16)
		binary.LittleEndian.PutUint32(writeData[0:4], profileESP32S2.outW1TS)
		binary.LittleEndian.PutUint32(writeData[4:8], 1<<15)
		binary.LittleEndian.PutUint32(writeData[8:12], 0xFFFFFFFF)
		resp := v.dispatch(reqHeader(opWriteReg, writeData))
		require.NotNil(t, resp)
		assert.Equal(t, []byte{0x00, 0x00}, resp[8:])

		readData := make([]byte, 4)
		binary.LittleEndian.PutUint32(readData, profileESP32S2.inAddr)
		resp = v.dispatch(reqHeader(opReadReg, readData))
		require.NotNil(t, resp)
		assert.Equal(t, uint32(1<<15), binary.LittleEndian.Uint32(resp[4:8])&(1<<15))
	})

	t.Run("SPI_ATTACH and SPI_SET_PARAMS and CHANGE_BAUD ack OK", func(t *testing.T) {
		v := newVirtualPort(profileESP32S2)
		for _, op := range []byte{opSPIAttach, opSPISetParams, opChangeBaud} {
			resp := v.dispatch(reqHeader(op, nil))
			require.NotNil(t, resp)
			assert.Equal(t, []byte{0x00, 0x00}, resp[8:], "opcode 0x%02X", op)
		}
	})

	t.Run("unknown opcode acks OK", func(t *testing.T) {
		v := newVirtualPort(profileESP32S2)
		resp := v.dispatch(reqHeader(0x7F, nil))
		require.NotNil(t, resp)
		assert.Equal(t, []byte{0x00, 0x00}, resp[8:])
	})

	t.Run("SPI CMD register auto-clears bit 18 on read", func(t *testing.T) {
		v := newVirtualPort(profileESP32S2)

		writeData := make([]byte, 16)
		binary.LittleEndian.PutUint32(writeData[0:4], profileESP32S2.spiCMDReg)
		binary.LittleEndian.PutUint32(writeData[4:8], 1<<18)
		binary.LittleEndian.PutUint32(writeData[8:12], 0xFFFFFFFF)
		resp := v.dispatch(reqHeader(opWriteReg, writeData))
		require.NotNil(t, resp)

		readData := make([]byte, 4)
		binary.LittleEndian.PutUint32(readData, profileESP32S2.spiCMDReg)
		resp = v.dispatch(reqHeader(opReadReg, readData))
		require.NotNil(t, resp)
		assert.Equal(t, uint32(1<<18), binary.LittleEndian.Uint32(resp[4:8]), "first read observes the set bit")

		resp = v.dispatch(reqHeader(opReadReg, readData))
		require.NotNil(t, resp)
		assert.Equal(t, uint32(0), binary.LittleEndian.Uint32(resp[4:8]), "second read observes the auto-cleared bit")
	})
}

// TestVirtualPortNoopMethods exercises every serial.Port method that is a
// documented no-op on virtualPort (idle-but-present port semantics): each
// must return nil (or a nil ModemStatusBits, nil error) without touching
// dispatch/register state.
func TestVirtualPortNoopMethods(t *testing.T) {
	v := newVirtualPort(profileESP32S2)

	assert.NoError(t, v.SetMode(&serial.Mode{}))
	assert.NoError(t, v.Drain())
	assert.NoError(t, v.ResetOutputBuffer())
	assert.NoError(t, v.SetDTR(true))
	assert.NoError(t, v.SetRTS(false))

	bits, err := v.GetModemStatusBits()
	assert.Nil(t, bits)
	assert.NoError(t, err)

	assert.NoError(t, v.Break(5*time.Millisecond))
	assert.NoError(t, v.Close())
}

// TestReadNeverBlocksOrEOF exercises virtualPort.Read's empty-output-buffer
// contract directly: it must return promptly (bounded by the configured
// read timeout) with (0, nil), never io.EOF, matching what espflasher's
// slipReader.ReadFrame requires.
func TestReadNeverBlocksOrEOF(t *testing.T) {
	v := newVirtualPort(profileESP32S2)
	require.NoError(t, v.SetReadTimeout(10*time.Millisecond))

	buf := make([]byte, 16)
	start := time.Now()
	n, err := v.Read(buf)
	elapsed := time.Since(start)

	assert.Equal(t, 0, n)
	assert.NoError(t, err)
	assert.Less(t, elapsed, 200*time.Millisecond, "Read must not block")
}
