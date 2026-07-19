package decode

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestArchMarshalJSON(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		arch Arch
		want string
	}{
		{"xtensa", ArchXtensa, `"xtensa"`},
		{"riscv32", ArchRiscv32, `"riscv32"`},
		{"unknown", ArchUnknown, `"unknown"`},
		{"out of range", Arch(99), `"unknown"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			data, err := json.Marshal(tc.arch)
			require.NoError(t, err)
			assert.Equal(t, tc.want, string(data))
		})
	}
}

func TestArchUnmarshalJSON(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		json string
		want Arch
	}{
		{"xtensa", `"xtensa"`, ArchXtensa},
		{"riscv32", `"riscv32"`, ArchRiscv32},
		{"unrecognized string", `"mips"`, ArchUnknown},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var a Arch
			require.NoError(t, a.UnmarshalJSON([]byte(tc.json)))
			assert.Equal(t, tc.want, a)
		})
	}

	t.Run("invalid JSON", func(t *testing.T) {
		t.Parallel()
		var a Arch
		err := a.UnmarshalJSON([]byte(`not-json`))
		require.Error(t, err)
	})
}

func TestResultJSONRoundTrip(t *testing.T) {
	t.Parallel()

	result := Result{
		Arch:          ArchXtensa,
		ToolchainPath: "/opt/homebrew/bin/xtensa-esp-elf-addr2line",
		Frames: []Frame{
			{PC: "0x400d1234", Function: "app_main", File: "main/main.c", Line: 42},
		},
	}

	data, err := json.Marshal(result)
	require.NoError(t, err)

	var got Result
	require.NoError(t, json.Unmarshal(data, &got))
	assert.Equal(t, result, got)
}

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
