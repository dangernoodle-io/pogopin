package mockhw

import "encoding/binary"

// SLIP (Serial Line Internet Protocol) framing constants — mirrors
// espflasher's slip.go exactly, since the virtual chip must speak the
// identical wire format.
const (
	slipEnd    byte = 0xC0 // Frame delimiter
	slipEsc    byte = 0xDB // Escape character
	slipEscEnd byte = 0xDC // Escaped 0xC0
	slipEscEsc byte = 0xDD // Escaped 0xDB
)

// ROM bootloader command opcodes handled by the dispatcher (subset needed
// for espflasher's SkipStub register-only connect + GPIO + FlashID path).
const (
	opFlashBegin      byte = 0x02
	opFlashData       byte = 0x03
	opFlashEnd        byte = 0x04
	opMemBegin        byte = 0x05
	opMemEnd          byte = 0x06
	opMemData         byte = 0x07
	opSync            byte = 0x08
	opWriteReg        byte = 0x09
	opReadReg         byte = 0x0A
	opSPISetParams    byte = 0x0B
	opSPIAttach       byte = 0x0D
	opChangeBaud      byte = 0x0F
	opSecurityInfoReg byte = 0x14

	dirRequest  byte = 0x00
	dirResponse byte = 0x01
)

// slipEncode wraps data in a SLIP frame, escaping special bytes. Frame
// format: [0xC0] [escaped data] [0xC0].
func slipEncode(data []byte) []byte {
	frame := make([]byte, 0, len(data)+2)
	frame = append(frame, slipEnd)
	for _, b := range data {
		switch b {
		case slipEnd:
			frame = append(frame, slipEsc, slipEscEnd)
		case slipEsc:
			frame = append(frame, slipEsc, slipEscEsc)
		default:
			frame = append(frame, b)
		}
	}
	frame = append(frame, slipEnd)
	return frame
}

// slipDecode removes SLIP delimiters and unescapes special bytes from a
// frame (with or without the surrounding 0xC0 delimiters).
func slipDecode(frame []byte) []byte {
	result := make([]byte, 0, len(frame))
	inEscape := false
	for _, b := range frame {
		if inEscape {
			switch b {
			case slipEscEnd:
				result = append(result, slipEnd)
			case slipEscEsc:
				result = append(result, slipEsc)
			default:
				result = append(result, slipEsc, b)
			}
			inEscape = false
			continue
		}
		switch b {
		case slipEsc:
			inEscape = true
		case slipEnd:
			// skip frame delimiters
		default:
			result = append(result, b)
		}
	}
	return result
}

// extractFrame scans buf for a complete 0xC0 ... 0xC0 delimited frame
// (inclusive of both delimiters) and returns it along with the remaining
// unconsumed bytes. Works identically whether buf accumulated one
// single-shot UART write or several 64-byte-chunked USB writes, since the
// caller buffers until a full frame is present. Real 0xC0 bytes only ever
// appear as delimiters — SLIP escaping guarantees a literal 0xC0 inside the
// payload is never present unescaped — so a raw byte scan is safe.
func extractFrame(buf []byte) (frame, rest []byte, ok bool) {
	start := -1
	for i, b := range buf {
		if b == slipEnd {
			start = i
			break
		}
	}
	if start == -1 {
		return nil, buf, false
	}
	end := -1
	for i := start + 1; i < len(buf); i++ {
		if buf[i] == slipEnd {
			end = i
			break
		}
	}
	if end == -1 {
		// Incomplete frame; drop any garbage before the opening delimiter
		// but keep waiting for the closing one.
		return nil, buf[start:], false
	}
	return buf[start : end+1], buf[end+1:], true
}

// dispatch decodes one SLIP-framed request payload (already SLIP-decoded,
// still including the 8-byte command header) and returns the SLIP-decoded
// response payload (header + data), or nil if the request is malformed.
func (v *virtualPort) dispatch(req []byte) []byte {
	if len(req) < 8 {
		return nil
	}

	opcode := req[1]
	length := binary.LittleEndian.Uint16(req[2:4])
	data := req[8:]
	if int(length) <= len(data) {
		data = data[:length]
	}

	var value uint32
	respData := []byte{0x00, 0x00} // OK status by default

	switch opcode {
	case opSync:
		value = 0 // "not a stub"

	case opSecurityInfoReg:
		if v.profile.securityInfoOK {
			respData = securityInfoPayload(v.profile.chipID)
		} else {
			// Force detectChip to fall through to the chip-magic path:
			// forces a non-zero status so espflasher's securityInfo()
			// fails for both the 20- and 12-byte payload attempts.
			respData = []byte{0x01, 0x00}
		}

	case opReadReg:
		if len(data) >= 4 {
			addr := binary.LittleEndian.Uint32(data[0:4])
			value = v.regs.read(v.profile, addr)
		}

	case opWriteReg:
		if len(data) >= 12 {
			addr := binary.LittleEndian.Uint32(data[0:4])
			val := binary.LittleEndian.Uint32(data[4:8])
			mask := binary.LittleEndian.Uint32(data[8:12])
			v.regs.write(v.profile, addr, val, mask)
		}

	case opSPIAttach, opSPISetParams, opChangeBaud:
		// Acknowledge; no state to model for these commands.

	default:
		// Unknown/unforeseen non-read opcode: OK ack so nothing wedges.
	}

	pkt := make([]byte, 8+len(respData))
	pkt[0] = dirResponse
	pkt[1] = opcode
	binary.LittleEndian.PutUint16(pkt[2:4], uint16(len(respData)))
	binary.LittleEndian.PutUint32(pkt[4:8], value)
	copy(pkt[8:], respData)
	return pkt
}

// securityInfoAPIVersion is the mock GET_SECURITY_INFO response's
// APIVersion field value (espflasher's SecurityInfo.APIVersion, offset
// [16:20] of the 20-byte payload).
const securityInfoAPIVersion uint32 = 1

// securityInfoPayload builds a mock GET_SECURITY_INFO (opcode 0x14)
// success response: 20-byte payload (espflasher's security_info.go 20-byte
// layout — Flags u32[0:4], B1..B8 u8[4:12], ChipID u32[12:16], APIVersion
// u32[16:20]) followed by the 2-byte OK status espflasher's checkCommand
// expects immediately after respDataLen bytes of data. Flags/B1-B8 are
// zeroed (no secure-boot/flash-encryption state modeled); chipID matches
// the requesting chip's espflasher ImageChipID (5 for C3, 9 for S3) so
// detectChip's ChipID-based match succeeds.
func securityInfoPayload(chipID uint32) []byte {
	payload := make([]byte, 22)
	binary.LittleEndian.PutUint32(payload[12:16], chipID)
	binary.LittleEndian.PutUint32(payload[16:20], securityInfoAPIVersion)
	payload[20] = 0x00 // status OK
	payload[21] = 0x00
	return payload
}
