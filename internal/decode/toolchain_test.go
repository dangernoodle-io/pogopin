package decode

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// requireToolchain skips the test if the toolchain is not available.
func requireToolchain(t *testing.T, arch Arch) *Toolchain {
	t.Helper()
	tc, err := FindToolchain(arch)
	if err != nil {
		t.Skipf("toolchain not available: %v", err)
	}
	return tc
}

// writeFakeAddr2line creates a shell script in dir that behaves as a minimal addr2line stub.
// output is the fixed stdout it will emit when called.
func writeFakeAddr2line(t *testing.T, dir, name, output string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	// Write output to a side file and cat it; avoids quoting issues with newlines.
	outFile := filepath.Join(dir, name+".out")
	err := os.WriteFile(outFile, []byte(output), 0o644)
	require.NoError(t, err)
	script := fmt.Sprintf("#!/bin/sh\ncat %q\n", outFile)
	err = os.WriteFile(path, []byte(script), 0o755)
	require.NoError(t, err)
	return path
}

func TestFindToolchain_Xtensa_PathOverride(t *testing.T) {
	dir := t.TempDir()
	writeFakeAddr2line(t, dir, "xtensa-esp-elf-addr2line", "")

	t.Setenv("PATH", dir)
	t.Setenv("IDF_PATH", "")
	t.Setenv("HOME", t.TempDir())

	tc, err := FindToolchain(ArchXtensa)
	require.NoError(t, err)
	require.Equal(t, filepath.Join(dir, "xtensa-esp-elf-addr2line"), tc.Path)
	require.Equal(t, ArchXtensa, tc.Arch)
}

func TestFindToolchain_Riscv_PathOverride(t *testing.T) {
	dir := t.TempDir()
	writeFakeAddr2line(t, dir, "riscv32-esp-elf-addr2line", "")

	t.Setenv("PATH", dir)
	t.Setenv("IDF_PATH", "")
	t.Setenv("HOME", t.TempDir())

	tc, err := FindToolchain(ArchRiscv32)
	require.NoError(t, err)
	require.Equal(t, filepath.Join(dir, "riscv32-esp-elf-addr2line"), tc.Path)
	require.Equal(t, ArchRiscv32, tc.Arch)
}

func TestFindToolchain_NotFound(t *testing.T) {
	t.Setenv("PATH", "")
	t.Setenv("IDF_PATH", "")
	t.Setenv("HOME", t.TempDir())

	_, err := FindToolchain(ArchXtensa)
	require.Error(t, err)
	require.Contains(t, err.Error(), "xtensa addr2line not found")
	require.Contains(t, err.Error(), "idf_tools.py install xtensa-esp-elf")
	require.Contains(t, err.Error(), "brew install xtensa-esp-elf-gcc")
	require.Contains(t, err.Error(), "apt install gcc-xtensa-esp32-elf")
	require.Contains(t, err.Error(), "$IDF_PATH")
}

func TestSymbolize_ParsesOutput(t *testing.T) {
	t.Parallel()

	// Fake addr2line output: one normal frame followed by an inlined frame, then another normal frame.
	fakeOutput := "app_main at /home/user/project/main.c:42\n" +
		" (inlined by) helper at /home/user/project/util.c:10\n" +
		"?? at ??:0\n"

	dir := t.TempDir()
	stubPath := writeFakeAddr2line(t, dir, "xtensa-esp-elf-addr2line", fakeOutput)

	tc := &Toolchain{Path: stubPath, Arch: ArchXtensa}
	pcs := []string{"0x400d1234", "0x400d5678"}

	frames, err := tc.Symbolize("/dev/null", pcs)
	require.NoError(t, err)
	require.Len(t, frames, 3)

	// First non-inlined frame.
	require.Equal(t, "0x400d1234", frames[0].PC)
	require.Equal(t, "app_main", frames[0].Function)
	require.Equal(t, "/home/user/project/main.c", frames[0].File)
	require.Equal(t, 42, frames[0].Line)

	// Inlined frame shares same PC as caller.
	require.Equal(t, "0x400d1234", frames[1].PC)
	require.Equal(t, "helper", frames[1].Function)
	require.Equal(t, "/home/user/project/util.c", frames[1].File)
	require.Equal(t, 10, frames[1].Line)

	// Second non-inlined frame — unknown symbol.
	require.Equal(t, "0x400d5678", frames[2].PC)
	require.Equal(t, "<unknown>", frames[2].Function)
	require.Equal(t, "", frames[2].File)
	require.Equal(t, 0, frames[2].Line)
}
