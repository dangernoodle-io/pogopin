package decode

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseBacktrace(t *testing.T) {
	t.Parallel()

	xtensaFixture := mustReadFile(t, "../../testdata/xtensa-panic.txt")
	riscvFixture := mustReadFile(t, "../../testdata/riscv-panic.txt")

	tests := []struct {
		name      string
		input     string
		arch      Arch
		want      []string
		wantErr   bool
		wantEmpty bool
	}{
		{
			name:  "xtensa fixture",
			input: xtensaFixture,
			arch:  ArchXtensa,
			want:  []string{"0x400d1234", "0x400d5678", "0x400d9abc"},
		},
		{
			name:  "riscv fixture",
			input: riscvFixture,
			arch:  ArchRiscv32,
			want:  []string{"0x42001234", "0x42005678", "0x42009abc"},
		},
		{
			name:  "xtensa inline",
			input: "Backtrace: 0x400d1234:0x3ffb0000 0x400d5678:0x3ffb1000 0x400d9abc:0x3ffb2000",
			arch:  ArchXtensa,
			want:  []string{"0x400d1234", "0x400d5678", "0x400d9abc"},
		},
		{
			name:  "riscv inline",
			input: "Backtrace: 0x42001234 0x42005678 0x42009abc",
			arch:  ArchRiscv32,
			want:  []string{"0x42001234", "0x42005678", "0x42009abc"},
		},
		{
			name:  "sentinel feefeffe stripped",
			input: "Backtrace: 0x400d1234:0x3ffb0000 0xfeefeffe:0xfeefeffe",
			arch:  ArchXtensa,
			want:  []string{"0x400d1234"},
		},
		{
			name:  "sentinel zero stripped",
			input: "Backtrace: 0x42001234 0x00000000 0x42005678",
			arch:  ArchRiscv32,
			want:  []string{"0x42001234", "0x42005678"},
		},
		{
			name:    "no backtrace line",
			input:   "Guru Meditation Error\nRegister dump:\nA0: 0x12345678",
			arch:    ArchXtensa,
			wantErr: true,
		},
		{
			name:  "mixed-case hex normalized to lowercase",
			input: "Backtrace: 0x400D1234:0x3FFB0000 0x400D5678:0x3FFB1000",
			arch:  ArchXtensa,
			want:  []string{"0x400d1234", "0x400d5678"},
		},
		{
			name:  "backtrace not at start of input",
			input: "Guru Meditation Error: Core 0 panic'ed\nPC: 0x400d0000\nBacktrace: 0x400d1234:0x3ffb0000 0x400d5678:0x3ffb1000\n",
			arch:  ArchXtensa,
			want:  []string{"0x400d1234", "0x400d5678"},
		},
		{
			name:  "trailing whitespace",
			input: "Backtrace: 0x400d1234:0x3ffb0000 0x400d5678:0x3ffb1000   ",
			arch:  ArchXtensa,
			want:  []string{"0x400d1234", "0x400d5678"},
		},
		{
			name:  "crlf line endings",
			input: "Guru Meditation Error\r\nBacktrace: 0x400d1234:0x3ffb0000 0x400d5678:0x3ffb1000\r\n",
			arch:  ArchXtensa,
			want:  []string{"0x400d1234", "0x400d5678"},
		},
		{
			name:      "backtrace line with only sentinels",
			input:     "Backtrace: 0xfeefeffe:0xfeefeffe 0x00000000:0x00000000",
			arch:      ArchXtensa,
			wantEmpty: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := ParseBacktrace(tc.input, tc.arch)
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			if tc.wantEmpty {
				require.Empty(t, got)
				return
			}
			require.Equal(t, tc.want, got)
		})
	}
}

func mustReadFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	return string(data)
}
