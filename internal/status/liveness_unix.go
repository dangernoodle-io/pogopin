//go:build !windows

package status

import (
	"errors"
	"syscall"
)

// pidAlive returns true if pid names a live process (or one we can't signal
// due to permissions, which still means it's alive). Returns false for
// non-positive pids and confirmed-dead (ESRCH) pids. Uses a raw kill(pid, 0)
// syscall directly rather than os.FindProcess/Signal, which on some
// platforms wraps process state in ways that misreport liveness for PIDs
// not spawned by this process.
func pidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, syscall.Signal(0))
	if err == nil {
		return true
	}
	if errors.Is(err, syscall.ESRCH) {
		return false
	}
	if errors.Is(err, syscall.EPERM) {
		return true
	}
	// Unknown error: fail safe and assume alive rather than drop live data.
	return true
}
