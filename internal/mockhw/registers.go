package mockhw

// registerFile models the minimal register space backing one virtual chip
// instance: OUT/OUT1 shadow words feeding GPIO IN mirroring, plus a generic
// address -> value map for everything else (IO_MUX, FUNCn_OUT_SEL_CFG, SPI
// registers, etc). Not safe for concurrent use; callers (virtualPort)
// serialize access.
type registerFile struct {
	out, out1 uint32
	regs      map[uint32]uint32
}

func newRegisterFile() *registerFile {
	return &registerFile{regs: make(map[uint32]uint32)}
}

// read returns the current value of addr under profile p.
func (r *registerFile) read(p *chipProfile, addr uint32) uint32 {
	switch addr {
	case p.magicRegAddr:
		return p.magicValue
	case p.inAddr:
		// IN mirrors OUT unconditionally: espflasher's ReadGPIO disables
		// the output driver (ENABLE W1TC) immediately before reading IN,
		// so gating this on ENABLE state would always read back 0.
		return r.out
	case p.in1Addr:
		return r.out1
	case p.spiCMDReg:
		// Auto-clear bit 18 (SPI_CMD_USR) on read so runSPIFlashCommand's
		// up-to-10-iteration poll for completion observes it cleared
		// promptly — typically on an early iteration — rather than
		// sleeping out the full retry budget.
		v := r.regs[addr]
		r.regs[addr] = v &^ (1 << 18)
		return v
	default:
		return r.regs[addr]
	}
}

// write applies a masked read-modify-write of val to addr under profile p.
func (r *registerFile) write(p *chipProfile, addr, val, mask uint32) {
	switch addr {
	case p.outW1TS:
		r.out |= val
	case p.outW1TC:
		r.out &^= val
	case p.out1W1TS:
		r.out1 |= val
	case p.out1W1TC:
		r.out1 &^= val
	case p.enableW1TS, p.enableW1TC, p.enable1W1TS, p.enable1W1TC:
		// Accepted; no loopback effect modeled.
	default:
		r.regs[addr] = (r.regs[addr] &^ mask) | (val & mask)
	}
}
