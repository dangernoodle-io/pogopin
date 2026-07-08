package status

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// staleWindow guards against PID reuse: a status file older than this is
// treated as dead even if its recorded PID happens to match a live process.
// 3x the 15s heartbeat interval.
const staleWindow = 45 * time.Second

var statusDir = DefaultDir()

// legacyCleanupOnce ensures the best-effort removal of the old single-file
// status.json only happens once per process.
var legacyCleanupOnce sync.Once

// DefaultDir returns the default status directory:
// ~/.cache/pogopin/status/ (or platform equivalent via os.UserCacheDir()).
// Falls back to os.TempDir() if UserCacheDir errors.
func DefaultDir() string {
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		return filepath.Join(os.TempDir(), "pogopin", "status")
	}
	return filepath.Join(cacheDir, "pogopin", "status")
}

// legacyPath returns the old single-file status.json path, sibling to the
// status/ directory (<cacheDir>/pogopin/status.json).
func legacyPath() string {
	return filepath.Join(filepath.Dir(statusDir), "status.json")
}

// SetStatusDir sets the status directory for testing and returns the previous value.
func SetStatusDir(dir string) string {
	prev := statusDir
	statusDir = dir
	return prev
}

// PortState represents the state of a serial port.
type PortState struct {
	Port         string  `json:"port"`
	Baud         int     `json:"baud"`
	Mode         string  `json:"mode"` // reader|flasher|external|pending
	BufferLines  int     `json:"buffer_lines"`
	Running      bool    `json:"running"`
	Reconnecting bool    `json:"reconnecting"`
	LastError    *string `json:"last_error,omitempty"`
	// SessionID is the CLAUDE_CODE_SESSION_ID of the pogo server process that
	// owns this port entry. Empty (omitted) when the env var is unset, e.g.
	// standalone runs outside Claude Code or older callers.
	SessionID string `json:"session_id,omitempty"`
	// PID is the OS process ID of the pogo server process that owns this
	// port entry.
	PID int `json:"pid,omitempty"`
}

// StatusFile represents the entire status file structure.
type StatusFile struct {
	Ports     []PortState `json:"ports"`
	UpdatedAt int64       `json:"updated_at"` // Unix seconds
}

// ownFilePath returns this process's own status file path within statusDir.
func ownFilePath() string {
	return filepath.Join(statusDir, strconv.Itoa(os.Getpid())+".json")
}

// Write marshals ports to this process's own status file using atomic
// rename. Writes even when ports is empty, so an idle process's own file
// doesn't retain stale port entries — other processes' files are untouched.
// Errors are silently discarded; never panics or returns anything.
func Write(ports []PortState) {
	if ports == nil {
		ports = []PortState{}
	}

	legacyCleanupOnce.Do(func() {
		_ = os.Remove(legacyPath())
	})

	statusData := StatusFile{
		Ports:     ports,
		UpdatedAt: time.Now().Unix(),
	}

	data, err := json.Marshal(statusData)
	if err != nil {
		return
	}

	_ = os.MkdirAll(statusDir, 0755)

	tmp, err := os.CreateTemp(statusDir, "status-*.json")
	if err != nil {
		return
	}
	tmpName := tmp.Name()

	_, err = tmp.Write(data)
	_ = tmp.Close()

	if err != nil {
		_ = os.Remove(tmpName)
		return
	}

	_ = os.Rename(tmpName, ownFilePath())
}

// Remove best-effort deletes this process's own status file. Errors are
// silently discarded.
func Remove() {
	_ = os.Remove(ownFilePath())
}

// ReadLivePorts globs statusDir for per-process status files, drops ports
// belonging to a file whose owning process is dead or whose UpdatedAt is
// older than staleWindow, and merges the surviving ports from all files
// into one slice. Unparseable/partial files are skipped. Never panics;
// returns nil on any directory-level error.
func ReadLivePorts() []PortState {
	entries, err := os.ReadDir(statusDir)
	if err != nil {
		return nil
	}

	now := time.Now()
	var merged []PortState

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}

		path := filepath.Join(statusDir, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}

		var sf StatusFile
		if err := json.Unmarshal(data, &sf); err != nil {
			continue
		}

		if now.Sub(time.Unix(sf.UpdatedAt, 0)) > staleWindow {
			continue
		}

		pid := filePID(entry.Name(), sf)
		if !pidAlive(pid) {
			continue
		}

		merged = append(merged, sf.Ports...)
	}

	return merged
}

// filePID derives the owning PID for a status file: prefer a PortState.PID
// value if present, otherwise fall back to the filename stem.
func filePID(name string, sf StatusFile) int {
	for _, p := range sf.Ports {
		if p.PID > 0 {
			return p.PID
		}
	}
	stem := strings.TrimSuffix(name, ".json")
	pid, err := strconv.Atoi(stem)
	if err != nil {
		return 0
	}
	return pid
}

// SweepStale globs statusDir for per-process status files and deletes any
// whose owning process is dead or whose UpdatedAt is older than staleWindow
// — the same liveness rule ReadLivePorts uses to drop entries from the
// merged view. Intended to be called periodically from a live server's
// heartbeat so sibling files left behind by crashed/killed processes don't
// accumulate unbounded. Best-effort: errors are silently discarded, and it
// never removes a file that's fresh and live, so it never races a
// concurrently-running server's own file. Never panics.
func SweepStale() {
	entries, err := os.ReadDir(statusDir)
	if err != nil {
		return
	}

	now := time.Now()

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}

		path := filepath.Join(statusDir, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}

		var sf StatusFile
		if err := json.Unmarshal(data, &sf); err != nil {
			continue
		}

		stale := now.Sub(time.Unix(sf.UpdatedAt, 0)) > staleWindow
		dead := !pidAlive(filePID(entry.Name(), sf))
		if stale || dead {
			_ = os.Remove(path)
		}
	}
}
