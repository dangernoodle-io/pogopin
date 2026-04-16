package decode

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDetectArch(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		path    string
		want    Arch
		wantErr bool
	}{
		{
			name: "xtensa elf",
			path: "../../testdata/xtensa.elf",
			want: ArchXtensa,
		},
		{
			name: "riscv elf",
			path: "../../testdata/riscv.elf",
			want: ArchRiscv32,
		},
		{
			name:    "non-existent file",
			path:    "/no/such/file.elf",
			wantErr: true,
		},
		{
			name:    "non-elf file",
			path:    "../../testdata/xtensa-panic.txt",
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := DetectArch(tc.path)
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}
