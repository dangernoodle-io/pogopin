//go:build ignore

// gen_elfs generates minimal ELF files used as test fixtures.
// Run from the repo root: go run testdata/gen_elfs.go
package main

import (
	"bytes"
	"encoding/binary"
	"os"
)

func main() {
	writeELF("testdata/xtensa.elf", 0x5e) // EM_XTENSA = 94
	writeELF("testdata/riscv.elf", 0xf3)  // EM_RISCV  = 243
}

// writeELF writes a minimal valid 32-bit little-endian ELF header with the
// given e_machine value. debug/elf.NewFile only needs the 52-byte ELF32 header
// to be parseable; no sections or program headers are required.
func writeELF(path string, machine uint16) {
	buf := &bytes.Buffer{}
	w := func(v any) {
		if err := binary.Write(buf, binary.LittleEndian, v); err != nil {
			panic(err)
		}
	}

	// e_ident[16]
	buf.Write([]byte{0x7f, 'E', 'L', 'F'}) // magic
	buf.WriteByte(1)                         // ELFCLASS32
	buf.WriteByte(1)                         // ELFDATA2LSB (little-endian)
	buf.WriteByte(1)                         // EV_CURRENT
	buf.WriteByte(0)                         // ELFOSABI_NONE
	buf.Write(make([]byte, 8))               // padding

	w(uint16(2))       // e_type:      ET_EXEC
	w(machine)         // e_machine
	w(uint32(1))       // e_version:   EV_CURRENT
	w(uint32(0))       // e_entry
	w(uint32(0))       // e_phoff
	w(uint32(0))       // e_shoff
	w(uint32(0))       // e_flags
	w(uint16(52))      // e_ehsize:    ELF32 header size
	w(uint16(32))      // e_phentsize
	w(uint16(0))       // e_phnum
	w(uint16(40))      // e_shentsize
	w(uint16(0))       // e_shnum
	w(uint16(0))       // e_shstrndx

	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		panic(err)
	}
}
