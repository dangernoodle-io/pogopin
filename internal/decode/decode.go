package decode

import "encoding/json"

// Frame represents a stack frame in a panic backtrace.
type Frame struct {
	PC       string `json:"pc"`
	Function string `json:"function"`
	File     string `json:"file"`
	Line     int    `json:"line"`
}

// Arch represents the target architecture.
type Arch int

const (
	ArchUnknown Arch = iota
	ArchXtensa
	ArchRiscv32
)

// MarshalJSON implements json.Marshaler for Arch.
func (a Arch) MarshalJSON() ([]byte, error) {
	var s string
	switch a {
	case ArchXtensa:
		s = "xtensa"
	case ArchRiscv32:
		s = "riscv32"
	default:
		s = "unknown"
	}
	return json.Marshal(s)
}

// UnmarshalJSON implements json.Unmarshaler for Arch.
func (a *Arch) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return err
	}
	switch s {
	case "xtensa":
		*a = ArchXtensa
	case "riscv32":
		*a = ArchRiscv32
	default:
		*a = ArchUnknown
	}
	return nil
}

// Result is the structured output of a decode operation.
type Result struct {
	Arch          Arch    `json:"arch"`
	ToolchainPath string  `json:"toolchain_path"`
	Frames        []Frame `json:"frames"`
}

// Decode is the top-level entry point: reads an ELF, detects arch,
// parses the panic text, locates the right toolchain, and symbolizes.
func Decode(elfPath, panicText string) (*Result, error) {
	arch, err := DetectArch(elfPath)
	if err != nil {
		return nil, err
	}

	pcs, err := ParseBacktrace(panicText, arch)
	if err != nil {
		return nil, err
	}

	tc, err := FindToolchain(arch)
	if err != nil {
		return nil, err
	}

	if len(pcs) == 0 {
		return &Result{Arch: arch, ToolchainPath: tc.Path, Frames: nil}, nil
	}

	frames, err := tc.Symbolize(elfPath, pcs)
	if err != nil {
		return nil, err
	}

	return &Result{Arch: arch, ToolchainPath: tc.Path, Frames: frames}, nil
}
