package flash

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"
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

var findSimilarPortFn func(string) string

// SetFindSimilarPortFn sets the callback used to find re-enumerated ports after flash.
func SetFindSimilarPortFn(fn func(string) string) {
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

func Flash(mgr PortManager, command string, args []string, opts *Options) (Result, error) {
	result := Result{Success: false}

	portName := mgr.PortName()
	baud := mgr.Baud()

	if portName == "" {
		return Result{}, fmt.Errorf("no serial port configured; call serial_start first")
	}

	if mgr.IsRunning() {
		_ = mgr.Stop()
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
			if newPort := findSimilarPortFn(currentPort); newPort != "" {
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

	return result, nil
}
