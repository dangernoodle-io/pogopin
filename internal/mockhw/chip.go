// Package mockhw provides an in-process virtual ESP chip: a fake
// go.bug.st/serial.Port that speaks just enough of the ESP ROM bootloader's
// SLIP-framed protocol for real espflasher code (connect, detect chip,
// register read/write, GPIO, SPI/FlashID probing) to run against it with no
// hardware attached.
package mockhw

// chipProfile describes the register layout of one ESP chip family — enough
// for the SLIP dispatcher and register file to emulate chip-magic detection
// and GPIO register sequences for that chip. Four profiles ship: ESP32,
// ESP32-S2, ESP32-C3, ESP32-S3 (addresses from espflasher's gpio.go /
// target_esp32*.go).
type chipProfile struct {
	name string

	// magicRegAddr/magicValue back espflasher's chip-magic detection path
	// (READ_REG at chipDetectMagicRegAddr, 0x40001000 on every supported
	// chip family). Only meaningful when securityInfoOK is false — C3/S3
	// have no magic value and are detected via GET_SECURITY_INFO ChipID
	// instead (espflasher's UsesMagicValue:false).
	magicRegAddr uint32
	magicValue   uint32

	// securityInfoOK selects the GET_SECURITY_INFO (opcode 0x14) mock
	// response shape. false (ESP32, ESP32-S2): always answer with an error
	// status, forcing espflasher's detectChip to fall through to the
	// chip-magic path — these chips are magic-detected upstream. true
	// (ESP32-C3, ESP32-S3): answer with a real 20-byte payload carrying
	// chipID, matching espflasher's ImageChipID-based detection for chips
	// with UsesMagicValue:false.
	securityInfoOK bool
	chipID         uint32

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
// defESP32S2.SPIRegBase. Magic-detected (UsesMagicValue:true); no ChipID.
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

// profileESP32 is the ESP32 (classic) register layout: chip magic from
// espflasher's target_esp32.go (defESP32), GPIO registers from
// espflasher's gpio.go (defESP32GPIO), SPI CMD register from
// defESP32.SPIRegBase. Magic-detected (UsesMagicValue:true); no ChipID.
var profileESP32 = &chipProfile{
	name: "ESP32",

	magicRegAddr: 0x40001000,
	magicValue:   0x00F01D83,

	outW1TS:  0x3FF44008,
	outW1TC:  0x3FF4400C,
	out1W1TS: 0x3FF44014,
	out1W1TC: 0x3FF44018,

	enableW1TS:  0x3FF44024,
	enableW1TC:  0x3FF44028,
	enable1W1TS: 0x3FF44030,
	enable1W1TC: 0x3FF44034,

	inAddr:  0x3FF4403C,
	in1Addr: 0x3FF44040,

	spiCMDReg: 0x3FF42000,
}

// profileESP32C3 is the ESP32-C3 register layout: no chip-magic support
// (espflasher target_esp32c3.go: UsesMagicValue:false, ImageChipID:5) — the
// virtual chip is detected via GET_SECURITY_INFO ChipID instead. GPIO
// registers from espflasher's gpio.go (defESP32C3GPIO); C3 has no
// high-word GPIO bank (hasHighWord:false), so the out1/enable1/in1
// addresses are 0 (unused). SPI CMD register from defESP32C3.SPIRegBase.
var profileESP32C3 = &chipProfile{
	name: "ESP32-C3",

	securityInfoOK: true,
	chipID:         5,

	outW1TS: 0x60004008,
	outW1TC: 0x6000400C,

	enableW1TS: 0x60004024,
	enableW1TC: 0x60004028,

	inAddr: 0x6000403C,

	spiCMDReg: 0x60002000,
}

// profileESP32S3 is the ESP32-S3 register layout: no chip-magic support
// (espflasher target_esp32s3.go: UsesMagicValue:false, ImageChipID:9) — the
// virtual chip is detected via GET_SECURITY_INFO ChipID instead. GPIO
// registers from espflasher's gpio.go (defESP32S3GPIO); SPI CMD register
// from defESP32S3.SPIRegBase.
var profileESP32S3 = &chipProfile{
	name: "ESP32-S3",

	securityInfoOK: true,
	chipID:         9,

	outW1TS:  0x60004008,
	outW1TC:  0x6000400C,
	out1W1TS: 0x60004014,
	out1W1TC: 0x60004018,

	enableW1TS:  0x60004024,
	enableW1TC:  0x60004028,
	enable1W1TS: 0x60004030,
	enable1W1TC: 0x60004034,

	inAddr:  0x6000403C,
	in1Addr: 0x60004040,

	spiCMDReg: 0x60002000,
}
