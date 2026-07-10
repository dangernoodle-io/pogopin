// Package mockhw provides an in-process virtual ESP chip: a fake
// go.bug.st/serial.Port that speaks just enough of the ESP ROM bootloader's
// SLIP-framed protocol for real espflasher code (connect, detect chip,
// register read/write, GPIO, SPI/FlashID probing) to run against it with no
// hardware attached.
package mockhw

// chipProfile describes the register layout of one ESP chip family — enough
// for the SLIP dispatcher and register file to emulate chip-magic detection
// and GPIO register sequences for that chip. Only the ESP32-S2 profile
// ships today; C3/S3/ESP32 profiles can be added later by filling in a new
// chipProfile (addresses already known from espflasher's gpio.go /
// target_esp32*.go).
type chipProfile struct {
	name string

	// magicRegAddr/magicValue back espflasher's chip-magic detection path
	// (READ_REG at chipDetectMagicRegAddr, 0x40001000 on every supported
	// chip family).
	magicRegAddr uint32
	magicValue   uint32

	// GPIO OUT/OUT1 W1TS/W1TC registers (espflasher gpio.go outEnableRegs).
	outW1TS, outW1TC   uint32
	out1W1TS, out1W1TC uint32

	// GPIO ENABLE/ENABLE1 W1TS/W1TC registers. Writes are accepted and
	// stored generically; no loopback effect is modeled (ReadGPIO disables
	// the output driver before reading IN, and IN mirrors OUT
	// unconditionally regardless of ENABLE state).
	enableW1TS, enableW1TC   uint32
	enable1W1TS, enable1W1TC uint32

	// GPIO IN/IN1 registers (espflasher gpio.go inReg). Reads return the
	// OUT/OUT1 shadow words unconditionally.
	inAddr, in1Addr uint32

	// spiCMDReg is the SPI peripheral CMD register (== SPIRegBase in
	// espflasher's chipDef). runSPIFlashCommand polls this register,
	// waiting for bit 18 (SPI_CMD_USR) to clear; the register model
	// auto-clears it on read so the poll observes completion promptly
	// (typically on an early iteration) instead of sleeping out its full
	// retry budget.
	spiCMDReg uint32
}

// profileESP32S2 is the ESP32-S2 register layout: chip magic from
// espflasher's target_esp32s2.go (defESP32S2), GPIO registers from
// espflasher's gpio.go (defESP32S2GPIO), SPI CMD register from
// defESP32S2.SPIRegBase.
var profileESP32S2 = &chipProfile{
	name: "ESP32-S2",

	magicRegAddr: 0x40001000,
	magicValue:   0x000007C6,

	outW1TS:  0x3F404008,
	outW1TC:  0x3F40400C,
	out1W1TS: 0x3F404014,
	out1W1TC: 0x3F404018,

	enableW1TS:  0x3F404024,
	enableW1TC:  0x3F404028,
	enable1W1TS: 0x3F404030,
	enable1W1TC: 0x3F404034,

	inAddr:  0x3F40403C,
	in1Addr: 0x3F404040,

	spiCMDReg: 0x3F402000,
}
