package status

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

var statusFilePath = DefaultPath()

// DefaultPath returns the default status file path: ~/.cache/pogopin/status.json.
// Falls back to os.TempDir() if UserCacheDir errors.
func DefaultPath() string {
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		return filepath.Join(os.TempDir(), "pogopin", "status.json")
	}
	return filepath.Join(cacheDir, "pogopin", "status.json")
}

// SetStatusFilePath sets the status file path for testing and returns the previous value.
func SetStatusFilePath(p string) string {
	prev := statusFilePath
	statusFilePath = p
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
}

// StatusFile represents the entire status file structure.
type StatusFile struct {
	Ports     []PortState `json:"ports"`
	UpdatedAt int64       `json:"updated_at"` // Unix seconds
}

// Write marshals ports to the status file using atomic rename.
// Errors are silently discarded; never panics or returns anything.
func Write(ports []PortState) {
	if ports == nil {
		ports = []PortState{}
	}

	statusData := StatusFile{
		Ports:     ports,
		UpdatedAt: time.Now().Unix(),
	}

	data, err := json.Marshal(statusData)
	if err != nil {
		return
	}

	dir := filepath.Dir(statusFilePath)
	_ = os.MkdirAll(dir, 0755)

	tmp, err := os.CreateTemp(dir, "status-*.json")
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

	_ = os.Rename(tmpName, statusFilePath)
}
