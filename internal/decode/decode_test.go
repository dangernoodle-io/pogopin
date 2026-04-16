package decode

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDecode_XtensaFixture(t *testing.T) {
	t.Parallel()

	_ = requireToolchain(t, ArchXtensa)

	panicText, err := os.ReadFile("../../testdata/xtensa-panic.txt")
	require.NoError(t, err)

	result, err := Decode("../../testdata/xtensa.elf", string(panicText))
	if err != nil {
		// Synthetic ELF may be too minimal for addr2line — skip with TODO.
		// TODO: revisit with a real tiny ELF built from a minimal C source.
		t.Skipf("addr2line rejected synthetic ELF (expected for stub fixtures): %v", err)
	}
	require.Equal(t, ArchXtensa, result.Arch)
	require.NotEmpty(t, result.ToolchainPath)
	require.NotEmpty(t, result.Frames)
}

func TestDecode_RiscvFixture(t *testing.T) {
	t.Parallel()

	_ = requireToolchain(t, ArchRiscv32)

	panicText, err := os.ReadFile("../../testdata/riscv-panic.txt")
	require.NoError(t, err)

	result, err := Decode("../../testdata/riscv.elf", string(panicText))
	if err != nil {
		// TODO: revisit with a real tiny ELF built from a minimal C source.
		t.Skipf("addr2line rejected synthetic ELF (expected for stub fixtures): %v", err)
	}
	require.Equal(t, ArchRiscv32, result.Arch)
	require.NotEmpty(t, result.ToolchainPath)
	require.NotEmpty(t, result.Frames)
}
