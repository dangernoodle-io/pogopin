package status

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"
)

func TestDefaultDir(t *testing.T) {
	dir := DefaultDir()
	if !contains(dir, filepath.Join("pogopin", "status")) {
		t.Errorf("DefaultDir() = %s, want path containing pogopin/status", dir)
	}
	// Should contain either Caches/.cache/pogopin/status (macOS/Linux) or tmp-based path
	if !contains(dir, "Caches") && !contains(dir, ".cache") && !contains(dir, filepath.Join(os.TempDir(), "pogopin")) {
		t.Errorf("DefaultDir() = %s, want Caches/.cache/pogopin or tempdir-based path, got %s", dir, dir)
	}
}

func TestSetStatusDir_RoundTrip(t *testing.T) {
	prev := statusDir
	defer func() { statusDir = prev }()

	testDir := "/tmp/test-status-dir"
	returned := SetStatusDir(testDir)
	if returned != prev {
		t.Errorf("SetStatusDir() returned %s, want %s", returned, prev)
	}
	if statusDir != testDir {
		t.Errorf("SetStatusDir() did not set dir, got %s want %s", statusDir, testDir)
	}
}

func ownFile(dir string) string {
	return filepath.Join(dir, strconv.Itoa(os.Getpid())+".json")
}

func TestWrite_CreatesOwnFileWithJSON(t *testing.T) {
	tmpDir := t.TempDir()
	prev := SetStatusDir(tmpDir)
	defer SetStatusDir(prev)

	ps := PortState{
		Port:        "/dev/ttyUSB0",
		Baud:        115200,
		Mode:        "reader",
		BufferLines: 100,
		Running:     true,
	}

	Write([]PortState{ps})

	data, err := os.ReadFile(ownFile(tmpDir))
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
	dir := filepath.Join(tmpDir, "does", "not", "exist")
	prev := SetStatusDir(dir)
	defer SetStatusDir(prev)

	Write([]PortState{})

	if _, err := os.Stat(ownFile(dir)); err != nil {
		t.Fatalf("status file was not created: %v", err)
	}
}

func TestWrite_EmptyPorts(t *testing.T) {
	tmpDir := t.TempDir()
	prev := SetStatusDir(tmpDir)
	defer SetStatusDir(prev)

	// Test with nil
	Write(nil)
	data, err := os.ReadFile(ownFile(tmpDir))
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

	// Test with empty slice — must NOT clobber another process's file, but
	// does overwrite this process's own file (still empty here).
	Write([]PortState{})
	data, err = os.ReadFile(ownFile(tmpDir))
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

func TestWrite_EmptyOwnFileDoesNotClobberOthers(t *testing.T) {
	tmpDir := t.TempDir()
	prev := SetStatusDir(tmpDir)
	defer SetStatusDir(prev)

	// Simulate another process's file with a live port.
	other := StatusFile{
		Ports:     []PortState{{Port: "/dev/ttyUSB9", Baud: 115200, Mode: "reader", Running: true, PID: os.Getpid()}},
		UpdatedAt: time.Now().Unix(),
	}
	data, err := json.Marshal(other)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	otherPath := filepath.Join(tmpDir, "999999.json")
	if err := os.WriteFile(otherPath, data, 0644); err != nil {
		t.Fatalf("write other file: %v", err)
	}

	// This process writes empty ports (portless server heartbeat).
	Write([]PortState{})

	// Other file must be untouched.
	stillThere, err := os.ReadFile(otherPath)
	if err != nil {
		t.Fatalf("other file missing after Write: %v", err)
	}
	var sf StatusFile
	if err := json.Unmarshal(stillThere, &sf); err != nil {
		t.Fatalf("unmarshal other file: %v", err)
	}
	if len(sf.Ports) != 1 {
		t.Errorf("other process's ports were clobbered: %v", sf.Ports)
	}
}

func TestWrite_SilentOnUnwritableDir(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "fakefile")
	// Create a regular file
	if err := os.WriteFile(filePath, []byte("content"), 0644); err != nil {
		t.Fatalf("failed to create fake file: %v", err)
	}

	// Try to write to a dir that treats the file as a directory
	badDir := filepath.Join(filePath, "status")
	prev := SetStatusDir(badDir)
	defer SetStatusDir(prev)

	// This should not panic or error
	Write([]PortState{})

	// File should not exist at the bad path
	if _, err := os.Stat(ownFile(badDir)); err == nil {
		t.Errorf("expected status file to not exist at %s", badDir)
	}
}

func TestWrite_PortState_SessionIDAndPID_SetWhenEnvSet(t *testing.T) {
	t.Setenv("CLAUDE_CODE_SESSION_ID", "sess-abc123")

	tmpDir := t.TempDir()
	prev := SetStatusDir(tmpDir)
	defer SetStatusDir(prev)

	ps := PortState{
		Port:      "/dev/ttyUSB0",
		Baud:      115200,
		Mode:      "reader",
		Running:   true,
		SessionID: os.Getenv("CLAUDE_CODE_SESSION_ID"),
		PID:       12345,
	}
	Write([]PortState{ps})

	data, err := os.ReadFile(ownFile(tmpDir))
	if err != nil {
		t.Fatalf("failed to read status file: %v", err)
	}

	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("failed to unmarshal JSON: %v", err)
	}
	ports, ok := raw["ports"].([]interface{})
	if !ok || len(ports) != 1 {
		t.Fatalf("expected 1 port, got %v", raw["ports"])
	}
	entry, ok := ports[0].(map[string]interface{})
	if !ok {
		t.Fatalf("expected port entry to be an object, got %v", ports[0])
	}
	if entry["session_id"] != "sess-abc123" {
		t.Errorf("session_id = %v, want sess-abc123", entry["session_id"])
	}
	if entry["pid"] != float64(12345) {
		t.Errorf("pid = %v, want 12345", entry["pid"])
	}
}

func TestWrite_PortState_SessionIDOmittedWhenEmpty(t *testing.T) {
	tmpDir := t.TempDir()
	prev := SetStatusDir(tmpDir)
	defer SetStatusDir(prev)

	ps := PortState{
		Port:    "/dev/ttyUSB0",
		Baud:    115200,
		Mode:    "reader",
		Running: true,
		// SessionID and PID left zero-valued, as when CLAUDE_CODE_SESSION_ID
		// is unset (omitempty should drop session_id; pid=0 also omitted).
	}
	Write([]PortState{ps})

	data, err := os.ReadFile(ownFile(tmpDir))
	if err != nil {
		t.Fatalf("failed to read status file: %v", err)
	}

	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("failed to unmarshal JSON: %v", err)
	}
	ports, ok := raw["ports"].([]interface{})
	if !ok || len(ports) != 1 {
		t.Fatalf("expected 1 port, got %v", raw["ports"])
	}
	entry, ok := ports[0].(map[string]interface{})
	if !ok {
		t.Fatalf("expected port entry to be an object, got %v", ports[0])
	}
	if _, present := entry["session_id"]; present {
		t.Errorf("session_id should be omitted when empty, got %v", entry["session_id"])
	}
	if _, present := entry["pid"]; present {
		t.Errorf("pid should be omitted when zero, got %v", entry["pid"])
	}
}

func TestWrite_AtomicRename(t *testing.T) {
	tmpDir := t.TempDir()
	prev := SetStatusDir(tmpDir)
	defer SetStatusDir(prev)

	// Write first content
	ps1 := PortState{Port: "/dev/ttyUSB0", Baud: 115200, Mode: "reader", Running: true}
	Write([]PortState{ps1})

	data1, err := os.ReadFile(ownFile(tmpDir))
	if err != nil {
		t.Fatalf("failed to read first write: %v", err)
	}

	// Write second content
	ps2 := PortState{Port: "/dev/ttyUSB1", Baud: 460800, Mode: "flasher", Running: false}
	Write([]PortState{ps2})

	data2, err := os.ReadFile(ownFile(tmpDir))
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

func TestRemove_DeletesOwnFile(t *testing.T) {
	tmpDir := t.TempDir()
	prev := SetStatusDir(tmpDir)
	defer SetStatusDir(prev)

	Write([]PortState{{Port: "/dev/ttyUSB0", Baud: 115200, Mode: "reader", Running: true}})
	if _, err := os.Stat(ownFile(tmpDir)); err != nil {
		t.Fatalf("file should exist before Remove: %v", err)
	}

	Remove()

	if _, err := os.Stat(ownFile(tmpDir)); !os.IsNotExist(err) {
		t.Errorf("expected file removed, stat err = %v", err)
	}
}

func TestRemove_SilentWhenMissing(t *testing.T) {
	tmpDir := t.TempDir()
	prev := SetStatusDir(tmpDir)
	defer SetStatusDir(prev)

	// Should not panic even though nothing was ever written.
	Remove()
}

func TestWrite_RemovesLegacySingleFile(t *testing.T) {
	tmpDir := t.TempDir()
	statusDirPath := filepath.Join(tmpDir, "status")
	legacyFile := filepath.Join(tmpDir, "status.json")
	if err := os.WriteFile(legacyFile, []byte(`{"ports":[]}`), 0644); err != nil {
		t.Fatalf("failed to seed legacy file: %v", err)
	}

	// Reset the once-guard so this test's SetStatusDir triggers a fresh
	// legacy-cleanup attempt regardless of test execution order.
	legacyCleanupOnce = sync.Once{}

	prev := SetStatusDir(statusDirPath)
	defer SetStatusDir(prev)

	Write([]PortState{})

	if _, err := os.Stat(legacyFile); !os.IsNotExist(err) {
		t.Errorf("expected legacy status.json to be removed, stat err = %v", err)
	}
}

func TestMergeLivePorts_MergesMultipleFiles(t *testing.T) {
	tmpDir := t.TempDir()
	prev := SetStatusDir(tmpDir)
	defer SetStatusDir(prev)

	writeFileFor(t, tmpDir, os.Getpid(), []PortState{{Port: "/dev/ttyUSB0", Running: true, PID: os.Getpid()}}, time.Now())

	child := longRunningChildPID(t)
	defer killChild(child)
	writeFileFor(t, tmpDir, child, []PortState{{Port: "/dev/ttyUSB1", Running: true, PID: child}}, time.Now())

	ports := mergeLivePorts(staleWindow)
	if len(ports) != 2 {
		t.Fatalf("expected 2 merged ports, got %d: %v", len(ports), ports)
	}
}

func TestMergeLivePorts_DropsDeadPID(t *testing.T) {
	tmpDir := t.TempDir()
	prev := SetStatusDir(tmpDir)
	defer SetStatusDir(prev)

	writeFileFor(t, tmpDir, os.Getpid(), []PortState{{Port: "/dev/ttyUSB0", Running: true, PID: os.Getpid()}}, time.Now())
	// A PID that's essentially guaranteed to be dead.
	deadPID := 999999
	writeFileFor(t, tmpDir, deadPID, []PortState{{Port: "/dev/ttyUSB1", Running: true, PID: deadPID}}, time.Now())

	ports := mergeLivePorts(staleWindow)
	if len(ports) != 1 {
		t.Fatalf("expected 1 live port after dropping dead-pid file, got %d: %v", len(ports), ports)
	}
	if ports[0].Port != "/dev/ttyUSB0" {
		t.Errorf("unexpected surviving port: %v", ports[0])
	}
}

func TestMergeLivePorts_DropsStale(t *testing.T) {
	tmpDir := t.TempDir()
	prev := SetStatusDir(tmpDir)
	defer SetStatusDir(prev)

	writeFileFor(t, tmpDir, os.Getpid(), []PortState{{Port: "/dev/ttyUSB0", Running: true, PID: os.Getpid()}}, time.Now())
	// Own live pid, but stale UpdatedAt beyond the 45s window.
	staleTime := time.Now().Add(-time.Hour)
	writeFileFor(t, tmpDir, os.Getpid()+1, []PortState{{Port: "/dev/ttyUSB1", Running: true, PID: os.Getpid()}}, staleTime)

	ports := mergeLivePorts(staleWindow)
	if len(ports) != 1 {
		t.Fatalf("expected 1 live port after dropping stale file, got %d: %v", len(ports), ports)
	}
	if ports[0].Port != "/dev/ttyUSB0" {
		t.Errorf("unexpected surviving port: %v", ports[0])
	}
}

func TestMergeLivePorts_FreshOnlyDropsOlderThan30s(t *testing.T) {
	tmpDir := t.TempDir()
	prev := SetStatusDir(tmpDir)
	defer SetStatusDir(prev)

	writeFileFor(t, tmpDir, os.Getpid(), []PortState{{Port: "/dev/ttyUSB0", Running: true, PID: os.Getpid()}}, time.Now())
	// 40s old: survives the 45s default staleWindow but not the 30s fresh
	// window.
	writeFileFor(t, tmpDir, os.Getpid()+1, []PortState{{Port: "/dev/ttyUSB1", Running: true, PID: os.Getpid()}}, time.Now().Add(-40*time.Second))

	withDefault := mergeLivePorts(staleWindow)
	if len(withDefault) != 2 {
		t.Fatalf("expected 2 ports under default 45s window, got %d: %v", len(withDefault), withDefault)
	}

	fresh := mergeLivePorts(freshWindow)
	if len(fresh) != 1 {
		t.Fatalf("expected 1 port under 30s fresh window, got %d: %v", len(fresh), fresh)
	}
	if fresh[0].Port != "/dev/ttyUSB0" {
		t.Errorf("unexpected surviving port: %v", fresh[0])
	}
}

func TestMergeLivePorts_SkipsUnparseableFile(t *testing.T) {
	tmpDir := t.TempDir()
	prev := SetStatusDir(tmpDir)
	defer SetStatusDir(prev)

	writeFileFor(t, tmpDir, os.Getpid(), []PortState{{Port: "/dev/ttyUSB0", Running: true, PID: os.Getpid()}}, time.Now())
	if err := os.WriteFile(filepath.Join(tmpDir, "garbage.json"), []byte("not json"), 0644); err != nil {
		t.Fatalf("failed to write garbage file: %v", err)
	}

	ports := mergeLivePorts(staleWindow)
	if len(ports) != 1 {
		t.Fatalf("expected 1 live port, garbage file should be skipped, got %d: %v", len(ports), ports)
	}
}

func TestMergeLivePorts_FallsBackToFilenameStemWhenNoPortPID(t *testing.T) {
	tmpDir := t.TempDir()
	prev := SetStatusDir(tmpDir)
	defer SetStatusDir(prev)

	// Own live pid, but no PortState.PID set — must fall back to the
	// filename stem (this process's own pid) to determine liveness.
	writeFileFor(t, tmpDir, os.Getpid(), []PortState{{Port: "/dev/ttyUSB0", Running: true}}, time.Now())

	ports := mergeLivePorts(staleWindow)
	if len(ports) != 1 {
		t.Fatalf("expected 1 live port via filename-stem fallback, got %d: %v", len(ports), ports)
	}
}

func TestMergeLivePorts_DropsFileWithDeadPIDFallbackAndEmptyPorts(t *testing.T) {
	tmpDir := t.TempDir()
	prev := SetStatusDir(tmpDir)
	defer SetStatusDir(prev)

	// No ports at all: filePID falls through to the filename stem, a dead pid.
	writeFileFor(t, tmpDir, 999999, []PortState{}, time.Now())

	ports := mergeLivePorts(staleWindow)
	if len(ports) != 0 {
		t.Fatalf("expected 0 ports for dead-pid-via-filename-fallback file, got %d: %v", len(ports), ports)
	}
}

func TestMergeLivePorts_NonNumericFilenameStemTreatedAsDead(t *testing.T) {
	tmpDir := t.TempDir()
	prev := SetStatusDir(tmpDir)
	defer SetStatusDir(prev)

	writeFileFor(t, tmpDir, os.Getpid(), []PortState{{Port: "/dev/ttyUSB0", Running: true}}, time.Now())

	sf := StatusFile{Ports: []PortState{{Port: "/dev/ttyUSB9", Running: true}}, UpdatedAt: time.Now().Unix()}
	data, err := json.Marshal(sf)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "not-a-pid.json"), data, 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	ports := mergeLivePorts(staleWindow)
	if len(ports) != 1 {
		t.Fatalf("expected only the numeric-pid file's port, got %d: %v", len(ports), ports)
	}
	if ports[0].Port != "/dev/ttyUSB0" {
		t.Errorf("unexpected surviving port: %v", ports[0])
	}
}

func TestMergeLivePorts_MissingDirReturnsNil(t *testing.T) {
	tmpDir := t.TempDir()
	prev := SetStatusDir(filepath.Join(tmpDir, "does-not-exist"))
	defer SetStatusDir(prev)

	ports := mergeLivePorts(staleWindow)
	if ports != nil {
		t.Errorf("expected nil for missing dir, got %v", ports)
	}
}

func TestReadLivePorts_EmptySessionIDRendersNothing(t *testing.T) {
	tmpDir := t.TempDir()
	prev := SetStatusDir(tmpDir)
	defer SetStatusDir(prev)

	// Even though a live matching-session-less port exists, an empty
	// sessionID must return nothing — the cross-session leak fix.
	writeFileFor(t, tmpDir, os.Getpid(), []PortState{{Port: "/dev/ttyUSB0", Running: true, PID: os.Getpid(), SessionID: "sess-a"}}, time.Now())

	ports, err := ReadLivePorts("", ModeAlways)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ports) != 0 {
		t.Fatalf("expected 0 ports for empty sessionID, got %d: %v", len(ports), ports)
	}
}

func TestReadLivePorts_FiltersToMatchingSession(t *testing.T) {
	tmpDir := t.TempDir()
	prev := SetStatusDir(tmpDir)
	defer SetStatusDir(prev)

	writeFileFor(t, tmpDir, os.Getpid(), []PortState{
		{Port: "/dev/ttyUSB0", Running: true, PID: os.Getpid(), SessionID: "sess-a"},
		{Port: "/dev/ttyUSB1", Running: true, PID: os.Getpid(), SessionID: "sess-b"},
	}, time.Now())

	ports, err := ReadLivePorts("sess-a", ModeAlways)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ports) != 1 {
		t.Fatalf("expected 1 port matching sess-a, got %d: %v", len(ports), ports)
	}
	if ports[0].Port != "/dev/ttyUSB0" {
		t.Errorf("unexpected surviving port: %v", ports[0])
	}
}

func TestReadLivePorts_NoMatchingSessionReturnsEmpty(t *testing.T) {
	tmpDir := t.TempDir()
	prev := SetStatusDir(tmpDir)
	defer SetStatusDir(prev)

	writeFileFor(t, tmpDir, os.Getpid(), []PortState{{Port: "/dev/ttyUSB0", Running: true, PID: os.Getpid(), SessionID: "sess-a"}}, time.Now())

	ports, err := ReadLivePorts("sess-other", ModeAlways)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ports) != 0 {
		t.Fatalf("expected 0 ports for non-matching session, got %d: %v", len(ports), ports)
	}
}

func TestReadLivePorts_FailOpenOnMissingDir(t *testing.T) {
	tmpDir := t.TempDir()
	prev := SetStatusDir(filepath.Join(tmpDir, "does-not-exist"))
	defer SetStatusDir(prev)

	ports, err := ReadLivePorts("sess-a", ModeAlways)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ports) != 0 {
		t.Fatalf("expected 0 ports for missing dir, got %d: %v", len(ports), ports)
	}
}

func TestReadLivePorts_FailOpenOnUnparseableFile(t *testing.T) {
	tmpDir := t.TempDir()
	prev := SetStatusDir(tmpDir)
	defer SetStatusDir(prev)

	writeFileFor(t, tmpDir, os.Getpid(), []PortState{{Port: "/dev/ttyUSB0", Running: true, PID: os.Getpid(), SessionID: "sess-a"}}, time.Now())
	if err := os.WriteFile(filepath.Join(tmpDir, "garbage.json"), []byte("not json"), 0644); err != nil {
		t.Fatalf("failed to write garbage file: %v", err)
	}

	ports, err := ReadLivePorts("sess-a", ModeAlways)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ports) != 1 {
		t.Fatalf("expected 1 port, garbage file should be skipped, got %d: %v", len(ports), ports)
	}
}

func TestReadLivePorts_FailOpenOnStaleFile(t *testing.T) {
	tmpDir := t.TempDir()
	prev := SetStatusDir(tmpDir)
	defer SetStatusDir(prev)

	writeFileFor(t, tmpDir, os.Getpid(), []PortState{{Port: "/dev/ttyUSB0", Running: true, PID: os.Getpid(), SessionID: "sess-a"}}, time.Now().Add(-time.Hour))

	ports, err := ReadLivePorts("sess-a", ModeAlways)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ports) != 0 {
		t.Fatalf("expected 0 ports for stale file, got %d: %v", len(ports), ports)
	}
}

func TestReadLivePorts_FailOpenOnDeadPID(t *testing.T) {
	tmpDir := t.TempDir()
	prev := SetStatusDir(tmpDir)
	defer SetStatusDir(prev)

	deadPID := 999999
	writeFileFor(t, tmpDir, deadPID, []PortState{{Port: "/dev/ttyUSB0", Running: true, PID: deadPID, SessionID: "sess-a"}}, time.Now())

	ports, err := ReadLivePorts("sess-a", ModeAlways)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ports) != 0 {
		t.Fatalf("expected 0 ports for dead-pid file, got %d: %v", len(ports), ports)
	}
}

func TestReadLivePorts_FreshOnlyModeUsesTighterWindow(t *testing.T) {
	tmpDir := t.TempDir()
	prev := SetStatusDir(tmpDir)
	defer SetStatusDir(prev)

	// 40s old: within the 45s ModeAlways window but outside 30s ModeFreshOnly.
	writeFileFor(t, tmpDir, os.Getpid(), []PortState{{Port: "/dev/ttyUSB0", Running: true, PID: os.Getpid(), SessionID: "sess-a"}}, time.Now().Add(-40*time.Second))

	always, err := ReadLivePorts("sess-a", ModeAlways)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(always) != 1 {
		t.Fatalf("expected 1 port under ModeAlways, got %d: %v", len(always), always)
	}

	fresh, err := ReadLivePorts("sess-a", ModeFreshOnly)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(fresh) != 0 {
		t.Fatalf("expected 0 ports under ModeFreshOnly, got %d: %v", len(fresh), fresh)
	}
}

func TestParseMode(t *testing.T) {
	cases := map[string]Mode{
		"always":     ModeAlways,
		"ports-only": ModePortsOnly,
		"fresh-only": ModeFreshOnly,
		"":           ModeAlways,
		"bogus":      ModeAlways,
	}
	for in, want := range cases {
		if got := ParseMode(in); got != want {
			t.Errorf("ParseMode(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestEffectiveStatusDir_HonorsEnvOverride(t *testing.T) {
	prev := statusDir
	statusDir = ""
	defer func() { statusDir = prev }()

	t.Setenv("POGOPIN_STATUS_DIR", "/tmp/env-override-status-dir")

	if got := effectiveStatusDir(); got != "/tmp/env-override-status-dir" {
		t.Errorf("effectiveStatusDir() = %s, want env override", got)
	}
}

func TestEffectiveStatusDir_FallsBackToDefaultDir(t *testing.T) {
	prev := statusDir
	statusDir = ""
	defer func() { statusDir = prev }()

	t.Setenv("POGOPIN_STATUS_DIR", "")

	if got := effectiveStatusDir(); got != DefaultDir() {
		t.Errorf("effectiveStatusDir() = %s, want DefaultDir() = %s", got, DefaultDir())
	}
}

func TestEffectiveStatusDir_SetStatusDirWinsOverEnv(t *testing.T) {
	prev := SetStatusDir("/tmp/explicit-override")
	defer SetStatusDir(prev)

	t.Setenv("POGOPIN_STATUS_DIR", "/tmp/env-override-status-dir")

	if got := effectiveStatusDir(); got != "/tmp/explicit-override" {
		t.Errorf("effectiveStatusDir() = %s, want explicit SetStatusDir override", got)
	}
}

func TestSweepStale_RemovesDeadOrStale_KeepsFresh(t *testing.T) {
	tmpDir := t.TempDir()
	prev := SetStatusDir(tmpDir)
	defer SetStatusDir(prev)

	freshPath := filepath.Join(tmpDir, strconv.Itoa(os.Getpid())+".json")
	writeFileFor(t, tmpDir, os.Getpid(), []PortState{{Port: "/dev/ttyUSB0", Running: true, PID: os.Getpid()}}, time.Now())

	deadPID := 999999
	deadPath := filepath.Join(tmpDir, strconv.Itoa(deadPID)+".json")
	writeFileFor(t, tmpDir, deadPID, []PortState{{Port: "/dev/ttyUSB1", Running: true, PID: deadPID}}, time.Now())

	stalePID := os.Getpid() + 1
	stalePath := filepath.Join(tmpDir, strconv.Itoa(stalePID)+".json")
	writeFileFor(t, tmpDir, stalePID, []PortState{{Port: "/dev/ttyUSB2", Running: true, PID: os.Getpid()}}, time.Now().Add(-time.Hour))

	SweepStale()

	if _, err := os.Stat(freshPath); err != nil {
		t.Errorf("fresh file should survive SweepStale, stat err = %v", err)
	}
	if _, err := os.Stat(deadPath); !os.IsNotExist(err) {
		t.Errorf("dead-pid file should be removed by SweepStale, stat err = %v", err)
	}
	if _, err := os.Stat(stalePath); !os.IsNotExist(err) {
		t.Errorf("stale file should be removed by SweepStale, stat err = %v", err)
	}
}

func TestSweepStale_SilentOnMissingDir(t *testing.T) {
	tmpDir := t.TempDir()
	prev := SetStatusDir(filepath.Join(tmpDir, "does-not-exist"))
	defer SetStatusDir(prev)

	// Should not panic.
	SweepStale()
}

func TestPidAlive(t *testing.T) {
	if !pidAlive(os.Getpid()) {
		t.Error("pidAlive(own pid) should be true")
	}
	if pidAlive(0) {
		t.Error("pidAlive(0) should be false")
	}
	if pidAlive(-1) {
		t.Error("pidAlive(-1) should be false")
	}
	if pidAlive(999999) {
		t.Error("pidAlive(999999) should be false (near-certainly dead)")
	}
}

// writeFileFor writes a status file for the given pid directly (bypassing
// Write, which always targets the current process's own pid).
func writeFileFor(t *testing.T, dir string, pid int, ports []PortState, updatedAt time.Time) {
	t.Helper()
	sf := StatusFile{Ports: ports, UpdatedAt: updatedAt.Unix()}
	data, err := json.Marshal(sf)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	path := filepath.Join(dir, strconv.Itoa(pid)+".json")
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// longRunningChildPID starts a short-lived child process and returns its
// PID for liveness testing. The caller must call killChild to clean up.
func longRunningChildPID(t *testing.T) int {
	t.Helper()
	cmd := exec.Command("sleep", "5")
	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start child process: %v", err)
	}
	return cmd.Process.Pid
}

func killChild(pid int) {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return
	}
	_ = proc.Kill()
	_, _ = proc.Wait()
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
