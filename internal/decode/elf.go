package decode

import (
	"debug/elf"
	"fmt"
)

// DetectArch opens an ELF file and returns its target architecture.
func DetectArch(elfPath string) (Arch, error) {
	f, err := elf.Open(elfPath)
	if err != nil {
		return ArchUnknown, fmt.Errorf("open ELF %q: %w", elfPath, err)
	}
	defer f.Close() //nolint:errcheck // elf.File.Close on a read-only file never returns a meaningful error

	switch f.Machine {
	case elf.EM_XTENSA:
		return ArchXtensa, nil
	case elf.EM_RISCV:
		if f.Class != elf.ELFCLASS32 {
			return ArchUnknown, fmt.Errorf("ELF %q: RISC-V is not 32-bit (class %v)", elfPath, f.Class)
		}
		return ArchRiscv32, nil
	default:
		return ArchUnknown, fmt.Errorf("ELF %q: unrecognized machine type %v", elfPath, f.Machine)
	}
}
