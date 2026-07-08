//go:build windows

package status

// pidAlive on Windows always reports true. There's no cheap syscall-only
// liveness check on Windows without pulling in golang.org/x/sys/windows
// (not a dependency here), so on this platform we rely solely on the 45s
// updated_at staleness window (staleWindow) to prune stale files. The JS
// reader's process.kill(pid, 0), which works for a liveness probe on
// Windows in Node, still prunes dead PIDs on the read side. Correctness is
// preserved cross-platform; only the Go-side pid check degrades to
// staleness-only on Windows.
func pidAlive(pid int) bool {
	return true
}
