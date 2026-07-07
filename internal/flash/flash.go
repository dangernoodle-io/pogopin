package flash

import (
	"bytes"
	"debug/elf"
	"debug/macho"
	"debug/pe"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"

	"dangernoodle.io/pogopin/internal/serial"
)

var (
	resolvedPathOnce sync.Once
	resolvedPath     string
)

var retryDelays = []time.Duration{
	100 * time.Millisecond,
	200 * time.Millisecond,
	400 * time.Millisecond,
	800 * time.Millisecond,
	1600 * time.Millisecond,
}

// findSimilarPortFn locates a re-enumerated port after an external flash
// command. knownPorts excludes ports that already existed before the flash
// op — see serial.FindSimilarPort — so a coincidental prefix match against an
// unrelated board isn't mistaken for the flashed device re-enumerating (BR-58).
var findSimilarPortFn func(port string, knownPorts map[string]bool) string

// listPortsFn enumerates system ports; overridable for testing.
var listPortsFn = serial.ListPorts

// SetFindSimilarPortFn sets the callback used to find re-enumerated ports after flash.
func SetFindSimilarPortFn(fn func(port string, knownPorts map[string]bool) string) {
	findSimilarPortFn = fn
}

type PortManager interface {
	PortName() string
	Baud() int
	IsRunning() bool
	Stop() error
	Start(port string, baud int) error
	Read(n int) []string
	ClearBuffer()
	SetPortName(string)
}

type Result struct {
	CommandOutput string `json:"command_output"`
	SerialOutput  string `json:"serial_output"`
	Success       bool   `json:"success"`
}

type Options struct {
	OutputLines  int    // tail N lines; 0 = unlimited
	OutputFilter string // regex to filter output lines; "" = no filter
	Shell        bool   // run command via sh -c (enables &&, pipes, globs)
	Cwd          string // working directory for command
}

func resolveShellPath() string {
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/sh"
	}
	out, err := exec.Command(shell, "-l", "-c", "echo $PATH").Output()
	if err != nil || strings.TrimSpace(string(out)) == "" {
		return os.Getenv("PATH")
	}
	return strings.TrimSpace(string(out))
}

func envWithPath(path string) []string {
	env := os.Environ()
	for i, e := range env {
		if strings.HasPrefix(e, "PATH=") {
			env[i] = "PATH=" + path
			return env
		}
	}
	return append(env, "PATH="+path)
}

// archFromMachoCpu maps a Mach-O CPU type to the equivalent Go GOARCH string.
func archFromMachoCpu(cpu macho.Cpu) (string, bool) {
	switch cpu {
	case macho.CpuAmd64:
		return "amd64", true
	case macho.Cpu386:
		return "386", true
	case macho.CpuArm:
		return "arm", true
	case macho.CpuArm64:
		return "arm64", true
	default:
		return "", false
	}
}

// archFromElfMachine maps an ELF e_machine value to the equivalent Go GOARCH
// string.
func archFromElfMachine(m elf.Machine) (string, bool) {
	switch m {
	case elf.EM_X86_64:
		return "amd64", true
	case elf.EM_386:
		return "386", true
	case elf.EM_AARCH64:
		return "arm64", true
	case elf.EM_ARM:
		return "arm", true
	case elf.EM_PPC64:
		return "ppc64", true
	default:
		return "", false
	}
}

// archFromPeMachine maps a PE Machine field to the equivalent Go GOARCH
// string.
func archFromPeMachine(m uint16) (string, bool) {
	switch m {
	case pe.IMAGE_FILE_MACHINE_AMD64:
		return "amd64", true
	case pe.IMAGE_FILE_MACHINE_I386:
		return "386", true
	case pe.IMAGE_FILE_MACHINE_ARM64:
		return "arm64", true
	case pe.IMAGE_FILE_MACHINE_ARMNT:
		return "arm", true
	default:
		return "", false
	}
}

// detectBinaryArchs opens the file at path and returns the GOARCH-equivalent
// architecture(s) embedded in its header. Multiple entries are returned for
// fat/universal Mach-O binaries. Returns an error if the file can't be opened
// or its format isn't recognized by any of the supported parsers (macho, elf,
// pe) -- callers must treat that as "unknown, don't block" rather than a hard
// failure, since it also covers shell-script wrappers and other non-binary
// launchers.
func detectBinaryArchs(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	if fat, ferr := macho.NewFatFile(f); ferr == nil {
		defer fat.Close()
		archs := make([]string, 0, len(fat.Arches))
		for _, a := range fat.Arches {
			if arch, ok := archFromMachoCpu(a.Cpu); ok {
				archs = append(archs, arch)
			}
		}
		if len(archs) == 0 {
			return nil, fmt.Errorf("no recognized architectures in fat mach-o %s", path)
		}
		return archs, nil
	}
	if _, serr := f.Seek(0, 0); serr != nil {
		return nil, serr
	}

	if mo, merr := macho.NewFile(f); merr == nil {
		defer mo.Close()
		if arch, ok := archFromMachoCpu(mo.Cpu); ok {
			return []string{arch}, nil
		}
		return nil, fmt.Errorf("unrecognized mach-o cpu type in %s", path)
	}
	if _, serr := f.Seek(0, 0); serr != nil {
		return nil, serr
	}

	if ef, eerr := elf.NewFile(f); eerr == nil {
		defer ef.Close()
		if arch, ok := archFromElfMachine(ef.Machine); ok {
			return []string{arch}, nil
		}
		return nil, fmt.Errorf("unrecognized elf machine type in %s", path)
	}
	if _, serr := f.Seek(0, 0); serr != nil {
		return nil, serr
	}

	if pf, perr := pe.NewFile(f); perr == nil {
		defer pf.Close()
		if arch, ok := archFromPeMachine(pf.Machine); ok {
			return []string{arch}, nil
		}
		return nil, fmt.Errorf("unrecognized pe machine type in %s", path)
	}

	return nil, fmt.Errorf("unrecognized binary format: %s", path)
}

// archCompat describes the outcome of comparing a binary's detected
// architecture(s) against the host.
type archCompat struct {
	Fatal   bool
	Warning string // non-empty when notable but not fatal
}

// checkArchCompat decides whether a binary exposing binArchs is runnable on
// a host running hostOS/hostArch. An exact match is always fine. The one
// deliberate exception to "mismatch is fatal" is an amd64 binary on a
// darwin/arm64 host: Rosetta 2 can run that, so it is downgraded to a
// warning instead of a hard failure. Any other mismatch (e.g. an arm64
// binary on an amd64 host) is fatal.
func checkArchCompat(binArchs []string, hostOS, hostArch string) archCompat {
	for _, a := range binArchs {
		if a == hostArch {
			return archCompat{}
		}
	}

	if hostOS == "darwin" && hostArch == "arm64" {
		for _, a := range binArchs {
			if a == "amd64" {
				return archCompat{Warning: fmt.Sprintf(
					"binary is amd64; running under Rosetta 2 on %s/%s "+
						"(if flashing fails, install Rosetta with `softwareupdate --install-rosetta`)",
					hostOS, hostArch)}
			}
		}
	}

	return archCompat{
		Fatal: true,
		Warning: fmt.Sprintf("binary arch %s incompatible with host %s/%s",
			strings.Join(binArchs, ","), hostOS, hostArch),
	}
}

// preflightFlashCommand resolves command on PATH and checks its architecture
// against the host before it is exec'd (BR-51) -- catching a wrong-arch
// flasher tool with a clear error instead of a cryptic mid-flash "bad CPU
// type" / exec-format-error failure.
//
// A missing command is always a hard error. An architecture mismatch is a
// hard error EXCEPT the Rosetta-runnable amd64-on-arm64-darwin case, which
// returns a non-fatal warning instead. If the binary's header can't be
// parsed (unknown format, shell-script wrapper, etc.) the check is skipped
// entirely and exec proceeds as before -- a false-reject that blocks a
// working setup is worse than a slightly-later generic exec error.
func preflightFlashCommand(command string) (path string, warning string, err error) {
	path, err = exec.LookPath(command)
	if err != nil {
		return "", "", fmt.Errorf("flasher command %q not found on PATH", command)
	}

	archs, derr := detectBinaryArchs(path)
	if derr != nil {
		// Best-effort only: unparseable/unknown header never blocks (BR-51) --
		// a false-reject that blocks a working flasher is worse than a
		// slightly-later generic exec error.
		return path, "", nil //nolint:nilerr // intentional: proceed on unparseable header
	}

	compat := checkArchCompat(archs, runtime.GOOS, runtime.GOARCH)
	if compat.Fatal {
		return "", "", fmt.Errorf("flasher command %q (%s): %s", command, path, compat.Warning)
	}
	return path, compat.Warning, nil
}

// preflightFn is the hook Flash() calls to run the preflight arch/exists
// check; overridable in tests to inject an arch decision without depending
// on a real binary on PATH or the actual runtime.GOARCH.
var preflightFn = preflightFlashCommand

func Flash(mgr PortManager, command string, args []string, opts *Options) (Result, error) {
	result := Result{Success: false}

	portName := mgr.PortName()
	baud := mgr.Baud()

	if portName == "" {
		return Result{}, fmt.Errorf("no serial port configured; call serial_start first")
	}

	// Preflight the command (arch/exists check, BR-51) before touching port
	// state -- a rejected command must never stop the port or leave it
	// without a restart.
	var preflightWarning string
	if opts == nil || !opts.Shell {
		// Shell mode runs `command` as a full shell string via sh -c, not as
		// a binary path -- the arch preflight only applies to a direct exec.
		_, warn, perr := preflightFn(command)
		if perr != nil {
			return Result{}, perr
		}
		preflightWarning = warn
	}

	if mgr.IsRunning() {
		_ = mgr.Stop()
	}

	// Snapshot the ports that exist right now, before the external flash
	// command runs and potentially resets/re-enumerates the device. Used
	// below to tell a genuinely re-enumerated port apart from an unrelated
	// board's pre-existing port that merely shares a USB-serial prefix.
	var knownPorts map[string]bool
	if ports, err := listPortsFn(); err == nil {
		knownPorts = make(map[string]bool, len(ports))
		for _, p := range ports {
			knownPorts[p.Name] = true
		}
	}

	// Compile regex filter before running command if specified
	var re *regexp.Regexp
	if opts != nil && opts.OutputFilter != "" {
		var reErr error
		re, reErr = regexp.Compile(opts.OutputFilter)
		if reErr != nil {
			return Result{}, fmt.Errorf("invalid output filter regex: %v", reErr)
		}
	}

	var cmd *exec.Cmd
	if opts != nil && opts.Shell {
		cmd = exec.Command("sh", "-c", command)
	} else {
		cmd = exec.Command(command, args...)
	}
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out

	resolvedPathOnce.Do(func() { resolvedPath = resolveShellPath() })
	cmd.Env = envWithPath(resolvedPath)

	if opts != nil && opts.Cwd != "" {
		cmd.Dir = opts.Cwd
	}

	err := cmd.Run()
	result.CommandOutput = out.String()

	// Apply filtering and tailing to CommandOutput
	if opts != nil && (re != nil || opts.OutputLines > 0) {
		lines := strings.Split(strings.TrimRight(result.CommandOutput, "\n"), "\n")

		// Apply regex filter
		if re != nil {
			filtered := make([]string, 0, len(lines))
			for _, line := range lines {
				if re.MatchString(line) {
					filtered = append(filtered, line)
				}
			}
			lines = filtered
		}

		// Apply tail (keep last N lines)
		if opts.OutputLines > 0 && len(lines) > opts.OutputLines {
			lines = lines[len(lines)-opts.OutputLines:]
		}

		result.CommandOutput = strings.Join(lines, "\n")
	}

	mgr.ClearBuffer()

	currentPort := portName
	var startErr error
	for _, delay := range retryDelays {
		time.Sleep(delay)
		startErr = mgr.Start(currentPort, baud)
		if startErr == nil {
			break
		}
		// Try to find the re-enumerated port
		if findSimilarPortFn != nil {
			if newPort := findSimilarPortFn(currentPort, knownPorts); newPort != "" {
				currentPort = newPort
			}
		}
	}
	if startErr != nil {
		result.CommandOutput += fmt.Sprintf(
			"\nWarning: failed to restart serial after %d attempts: %v",
			len(retryDelays), startErr)
	} else if currentPort != portName {
		mgr.SetPortName(currentPort)
	}
	time.Sleep(3 * time.Second)

	lines := mgr.Read(100)
	result.SerialOutput = strings.Join(lines, "\n")

	result.Success = err == nil
	if err != nil {
		result.CommandOutput = fmt.Sprintf("Command failed: %v\n%s", err, result.CommandOutput)
	}
	if preflightWarning != "" {
		result.CommandOutput = fmt.Sprintf("Warning: %s\n%s", preflightWarning, result.CommandOutput)
	}

	return result, nil
}
