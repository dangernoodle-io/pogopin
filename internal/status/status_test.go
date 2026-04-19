package status

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultPath(t *testing.T) {
	path := DefaultPath()
	if !contains(path, "pogopin/status.json") {
		t.Errorf("DefaultPath() = %s, want path containing pogopin/status.json", path)
	}
	// Should contain either Caches/.cache/pogopin/status.json (macOS/Linux) or tmp-based path
	if !contains(path, "Caches") && !contains(path, ".cache") && !contains(path, filepath.Join(os.TempDir(), "pogopin")) {
		t.Errorf("DefaultPath() = %s, want Caches/.cache/pogopin or tempdir-based path, got %s", path, path)
	}
}

func TestSetStatusFilePath_RoundTrip(t *testing.T) {
	prev := statusFilePath
	defer func() { statusFilePath = prev }()

	testPath := "/tmp/test-status.json"
	returned := SetStatusFilePath(testPath)
	if returned != prev {
		t.Errorf("SetStatusFilePath() returned %s, want %s", returned, prev)
	}
	if statusFilePath != testPath {
		t.Errorf("SetStatusFilePath() did not set path, got %s want %s", statusFilePath, testPath)
	}
}

func TestWrite_CreatesFileWithJSON(t *testing.T) {
	tmpDir := t.TempDir()
	testPath := filepath.Join(tmpDir, "status.json")
	prev := SetStatusFilePath(testPath)
	defer SetStatusFilePath(prev)

	ps := PortState{
		Port:        "/dev/ttyUSB0",
		Baud:        115200,
		Mode:        "reader",
		BufferLines: 100,
		Running:     true,
	}

	Write([]PortState{ps})

	data, err := os.ReadFile(testPath)
	if err != nil {
		t.Fatalf("failed to read status file: %v", err)
	}

	var sf StatusFile
	if err := json.Unmarshal(data, &sf); err != nil {
		t.Fatalf("failed to unmarshal JSON: %v", err)
	}

	if len(sf.Ports) != 1 {
		t.Errorf("expected 1 port, got %d", len(sf.Ports))
	}
	if sf.Ports[0].Port != ps.Port {
		t.Errorf("port = %s, want %s", sf.Ports[0].Port, ps.Port)
	}
	if sf.UpdatedAt == 0 {
		t.Error("UpdatedAt should be non-zero")
	}
}

func TestWrite_MkdirAll(t *testing.T) {
	tmpDir := t.TempDir()
	testPath := filepath.Join(tmpDir, "does", "not", "exist", "status.json")
	prev := SetStatusFilePath(testPath)
	defer SetStatusFilePath(prev)

	Write([]PortState{})

	if _, err := os.Stat(testPath); err != nil {
		t.Fatalf("status file was not created: %v", err)
	}
}

func TestWrite_EmptyPorts(t *testing.T) {
	tmpDir := t.TempDir()
	testPath := filepath.Join(tmpDir, "status.json")
	prev := SetStatusFilePath(testPath)
	defer SetStatusFilePath(prev)

	// Test with nil
	Write(nil)
	data, err := os.ReadFile(testPath)
	if err != nil {
		t.Fatalf("failed to read status file after Write(nil): %v", err)
	}
	var sf StatusFile
	if err := json.Unmarshal(data, &sf); err != nil {
		t.Fatalf("failed to unmarshal JSON: %v", err)
	}
	if sf.Ports == nil || len(sf.Ports) != 0 {
		t.Errorf("Write(nil) produced invalid empty ports: %v", sf.Ports)
	}

	// Test with empty slice
	Write([]PortState{})
	data, err = os.ReadFile(testPath)
	if err != nil {
		t.Fatalf("failed to read status file after Write([]): %v", err)
	}
	if err := json.Unmarshal(data, &sf); err != nil {
		t.Fatalf("failed to unmarshal JSON: %v", err)
	}
	if sf.Ports == nil || len(sf.Ports) != 0 {
		t.Errorf("Write([]) produced invalid empty ports: %v", sf.Ports)
	}
}

func TestWrite_SilentOnUnwritableDir(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "fakefile")
	// Create a regular file
	if err := os.WriteFile(filePath, []byte("content"), 0644); err != nil {
		t.Fatalf("failed to create fake file: %v", err)
	}

	// Try to write to a path that treats the file as a directory
	badPath := filepath.Join(filePath, "status.json")
	prev := SetStatusFilePath(badPath)
	defer SetStatusFilePath(prev)

	// This should not panic or error
	Write([]PortState{})

	// File should not exist at the bad path
	if _, err := os.Stat(badPath); err == nil {
		t.Errorf("expected status file to not exist at %s", badPath)
	}
}

func TestWrite_AtomicRename(t *testing.T) {
	tmpDir := t.TempDir()
	testPath := filepath.Join(tmpDir, "status.json")
	prev := SetStatusFilePath(testPath)
	defer SetStatusFilePath(prev)

	// Write first content
	ps1 := PortState{Port: "/dev/ttyUSB0", Baud: 115200, Mode: "reader", Running: true}
	Write([]PortState{ps1})

	data1, err := os.ReadFile(testPath)
	if err != nil {
		t.Fatalf("failed to read first write: %v", err)
	}

	// Write second content
	ps2 := PortState{Port: "/dev/ttyUSB1", Baud: 460800, Mode: "flasher", Running: false}
	Write([]PortState{ps2})

	data2, err := os.ReadFile(testPath)
	if err != nil {
		t.Fatalf("failed to read second write: %v", err)
	}

	// Final file should have second content
	var sf StatusFile
	if err := json.Unmarshal(data2, &sf); err != nil {
		t.Fatalf("failed to unmarshal final JSON: %v", err)
	}
	if len(sf.Ports) != 1 || sf.Ports[0].Port != "/dev/ttyUSB1" {
		t.Errorf("final file should contain second content, got %v", sf.Ports)
	}

	// Content should be different
	if string(data1) == string(data2) {
		t.Error("first and second writes produced identical content, expected different")
	}

	// Check for leftover temp files
	entries, err := os.ReadDir(tmpDir)
	if err != nil {
		t.Fatalf("failed to read tmpdir: %v", err)
	}
	for _, e := range entries {
		if contains(e.Name(), "status-") {
			t.Errorf("found leftover temp file: %s", e.Name())
		}
	}
}

// contains is a helper to check if a string contains a substring.
func contains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
