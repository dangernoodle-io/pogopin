package decode

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// Toolchain represents a located addr2line binary for a specific arch.
type Toolchain struct {
	Path string
	Arch Arch
}

// binaryName returns the addr2line binary name for the arch.
func binaryName(arch Arch) string {
	switch arch {
	case ArchXtensa:
		return "xtensa-esp-elf-addr2line"
	case ArchRiscv32:
		return "riscv32-esp-elf-addr2line"
	default:
		return ""
	}
}

// archLabel returns a human-readable label for the arch.
func archLabel(arch Arch) string {
	switch arch {
	case ArchXtensa:
		return "xtensa"
	case ArchRiscv32:
		return "riscv32"
	default:
		return "unknown"
	}
}

// toolDirName returns the tools/ subdirectory name for the arch.
func toolDirName(arch Arch) string {
	switch arch {
	case ArchXtensa:
		return "xtensa-esp-elf"
	case ArchRiscv32:
		return "riscv32-esp-elf"
	default:
		return ""
	}
}

// FindToolchain locates an addr2line binary for the given arch.
// Search order:
//  1. $IDF_PATH/tools/{xtensa-esp-elf|riscv32-esp-elf}/*/bin/<name>-addr2line
//  2. ~/.espressif/tools/{xtensa-esp-elf|riscv32-esp-elf}/*/{...}/bin/<name>-addr2line
//     (standard espressif-installed toolchain, not sourced into PATH)
//  3. $PATH (xtensa-esp-elf-addr2line or riscv32-esp-elf-addr2line)
//
// Returns a helpful error with install hints if not found.
func FindToolchain(arch Arch) (*Toolchain, error) {
	name := binaryName(arch)
	if name == "" {
		return nil, fmt.Errorf("unsupported arch: %v", arch)
	}

	dir := toolDirName(arch)

	// 1. Try $IDF_PATH glob.
	if idfPath := os.Getenv("IDF_PATH"); idfPath != "" {
		pattern := filepath.Join(idfPath, "tools", dir, "*", dir, "bin", name)
		if matches, err := filepath.Glob(pattern); err == nil && len(matches) > 0 {
			return &Toolchain{Path: matches[0], Arch: arch}, nil
		}
	}

	// 2. Try ~/.espressif/tools (default espressif install prefix).
	if home, err := os.UserHomeDir(); err == nil {
		pattern := filepath.Join(home, ".espressif", "tools", dir, "*", dir, "bin", name)
		if matches, err := filepath.Glob(pattern); err == nil && len(matches) > 0 {
			return &Toolchain{Path: matches[0], Arch: arch}, nil
		}
	}

	// 3. Fall back to $PATH.
	if p, err := exec.LookPath(name); err == nil {
		return &Toolchain{Path: p, Arch: arch}, nil
	}

	label := archLabel(arch)
	toolPkg := toolDirName(arch)
	return nil, fmt.Errorf(
		"%s addr2line not found\ninstall via one of:\n  - idf_tools.py install %s\n  - brew install %s-gcc\n  - apt install gcc-%s-esp32-elf\nor set $IDF_PATH to your ESP-IDF install",
		label, toolPkg, toolPkg, label,
	)
}

// reFrame matches a single addr2line -p output line.
// Groups: 1=function, 2=file, 3=line.
// Normal form:  "func at file:line"
// Unknown form: "?? ??:0"   (no "at" separator; emitted when nothing resolves).
var reFrame = regexp.MustCompile(`^\s*(?:\(inlined by\)\s+)?(.*?)\s(?:at\s)?(.*?):(\d+|\?)$`)

// Symbolize runs addr2line against the given PCs and returns decoded frames.
// pcs is a slice of 0x-prefixed hex strings; elfPath is the ELF to resolve against.
func (t *Toolchain) Symbolize(elfPath string, pcs []string) ([]Frame, error) {
	if len(pcs) == 0 {
		return nil, nil
	}

	args := append([]string{"-e", elfPath, "-f", "-C", "-i", "-p"}, pcs...)
	cmd := exec.CommandContext(context.Background(), t.Path, args...)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("addr2line: %w", err)
	}

	lines := strings.Split(strings.TrimRight(string(out), "\n"), "\n")
	var frames []Frame
	pcIdx := -1

	for _, line := range lines {
		if line == "" {
			continue
		}

		inlined := strings.HasPrefix(strings.TrimLeft(line, " \t"), "(inlined by)")
		if !inlined {
			pcIdx++
		}

		m := reFrame.FindStringSubmatch(line)
		if m == nil {
			fmt.Fprintf(os.Stderr, "addr2line: unparseable line: %q\n", line)
			continue
		}

		fn := m[1]
		if fn == "??" {
			fn = "<unknown>"
		}
		file := m[2]
		if file == "??" {
			file = ""
		}
		lineNum := 0
		if m[3] != "?" {
			lineNum, _ = strconv.Atoi(m[3])
		}

		pc := ""
		if pcIdx >= 0 && pcIdx < len(pcs) {
			pc = pcs[pcIdx]
		}

		frames = append(frames, Frame{
			PC:       pc,
			Function: fn,
			File:     file,
			Line:     lineNum,
		})
	}

	return frames, nil
}
