package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"dangernoodle.io/pogopin/internal/status"
)

// writeStatusFileFor writes a status file for an arbitrary pid (bypassing
// status.Write's current-process targeting) so tests can simulate multiple
// server processes without spawning real ones.
func writeStatusFileFor(t *testing.T, dir string, pid int, ports []status.PortState) {
	t.Helper()
	sf := struct {
		Ports     []status.PortState `json:"ports"`
		UpdatedAt int64              `json:"updated_at"`
	}{Ports: ports, UpdatedAt: time.Now().Unix()}
	data, err := json.Marshal(sf)
	require.NoError(t, err)
	path := filepath.Join(dir, strconv.Itoa(pid)+".json")
	require.NoError(t, os.WriteFile(path, data, 0644))
}

func TestResolveConsumerSessionID_Precedence(t *testing.T) {
	cases := []struct {
		name       string
		envPogopin string
		stdinSID   string
		envClaude  string
		want       string
	}{
		{"pogopin env wins over everything", "sess-env", "sess-stdin", "sess-claude", "sess-env"},
		{"stdin wins over claude env", "", "sess-stdin", "sess-claude", "sess-stdin"},
		{"claude env used when nothing else set", "", "", "sess-claude", "sess-claude"},
		{"empty when nothing set", "", "", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("POGOPIN_SESSION_ID", tc.envPogopin)
			t.Setenv("CLAUDE_CODE_SESSION_ID", tc.envClaude)
			got := resolveConsumerSessionID(statuslineInput{SessionID: tc.stdinSID})
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestParseStatuslineStdin(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  statuslineInput
	}{
		{"valid json", `{"session_id":"s1","cwd":"/x"}`, statuslineInput{SessionID: "s1", Cwd: "/x"}},
		{"empty stdin", "", statuslineInput{}},
		{"garbage stdin", "not json at all", statuslineInput{}},
		{"empty object", "{}", statuslineInput{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := parseStatuslineStdin(strings.NewReader(tc.input))
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestRenderStatusline_NoPortsAlwaysMode(t *testing.T) {
	var buf bytes.Buffer
	renderStatusline(&buf, nil, status.ModeAlways)
	assert.Equal(t, "serial: idle\n", buf.String())
}

func TestRenderStatusline_NoPortsPortsOnlyMode(t *testing.T) {
	var buf bytes.Buffer
	renderStatusline(&buf, nil, status.ModePortsOnly)
	assert.Empty(t, buf.String())
}

func TestRenderStatusline_NoPortsFreshOnlyMode(t *testing.T) {
	var buf bytes.Buffer
	renderStatusline(&buf, nil, status.ModeFreshOnly)
	assert.Empty(t, buf.String())
}

func TestRenderStatusline_SinglePortFormat(t *testing.T) {
	var buf bytes.Buffer
	ports := []status.PortState{
		{Port: "/dev/ttyUSB0", Baud: 115200, Mode: "reader", BufferLines: 42},
	}
	renderStatusline(&buf, ports, status.ModeAlways)
	assert.Equal(t, "serial: ttyUSB0@115200 reader 42L\n", buf.String())
}

func TestRenderStatusline_MultiplePortsJoined(t *testing.T) {
	var buf bytes.Buffer
	ports := []status.PortState{
		{Port: "/dev/ttyUSB0", Baud: 115200, Mode: "reader", BufferLines: 5},
		{Port: "/dev/ttyACM0", Baud: 921600, Mode: "flasher", BufferLines: 0},
	}
	renderStatusline(&buf, ports, status.ModeAlways)
	assert.Equal(t, "serial: ttyUSB0@115200 reader 5L | serial: ttyACM0@921600 flasher 0L\n", buf.String())
}

func TestRunStatusline_EmptyStdinNoError(t *testing.T) {
	tmpDir := t.TempDir()
	prev := status.SetStatusDir(tmpDir)
	defer status.SetStatusDir(prev)
	t.Setenv("POGOPIN_SESSION_ID", "")
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	t.Setenv("POGOPIN_STATUSLINE_MODE", "")

	cmd := statuslineCmd
	var out bytes.Buffer
	cmd.SetIn(strings.NewReader(""))
	cmd.SetOut(&out)

	err := runStatusline(cmd, nil)
	require.NoError(t, err)
	assert.Equal(t, "serial: idle\n", out.String())
}

func TestRunStatusline_GarbageStdinNoError(t *testing.T) {
	tmpDir := t.TempDir()
	prev := status.SetStatusDir(tmpDir)
	defer status.SetStatusDir(prev)
	t.Setenv("POGOPIN_SESSION_ID", "")
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	t.Setenv("POGOPIN_STATUSLINE_MODE", "")

	cmd := statuslineCmd
	var out bytes.Buffer
	cmd.SetIn(strings.NewReader("{not valid json"))
	cmd.SetOut(&out)

	err := runStatusline(cmd, nil)
	require.NoError(t, err)
	assert.Equal(t, "serial: idle\n", out.String())
}

func TestRunStatusline_RendersMatchingSessionPorts(t *testing.T) {
	tmpDir := t.TempDir()
	prev := status.SetStatusDir(tmpDir)
	defer status.SetStatusDir(prev)
	t.Setenv("POGOPIN_SESSION_ID", "")
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	t.Setenv("POGOPIN_STATUSLINE_MODE", "")

	writeStatusFileFor(t, tmpDir, os.Getpid(), []status.PortState{
		{Port: "/dev/ttyUSB0", Baud: 115200, Mode: "reader", BufferLines: 3, PID: os.Getpid(), SessionID: "sess-x"},
	})

	cmd := statuslineCmd
	var out bytes.Buffer
	cmd.SetIn(strings.NewReader(`{"session_id":"sess-x"}`))
	cmd.SetOut(&out)

	err := runStatusline(cmd, nil)
	require.NoError(t, err)
	assert.Equal(t, "serial: ttyUSB0@115200 reader 3L\n", out.String())
}

func TestRunStatusline_NoSessionRendersIdleNotOtherSessionPorts(t *testing.T) {
	tmpDir := t.TempDir()
	prev := status.SetStatusDir(tmpDir)
	defer status.SetStatusDir(prev)
	t.Setenv("POGOPIN_SESSION_ID", "")
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	t.Setenv("POGOPIN_STATUSLINE_MODE", "")

	writeStatusFileFor(t, tmpDir, os.Getpid(), []status.PortState{
		{Port: "/dev/ttyUSB0", Baud: 115200, Mode: "reader", BufferLines: 3, PID: os.Getpid(), SessionID: "sess-x"},
	})

	cmd := statuslineCmd
	var out bytes.Buffer
	cmd.SetIn(strings.NewReader(""))
	cmd.SetOut(&out)

	err := runStatusline(cmd, nil)
	require.NoError(t, err)
	// No session resolved -> ReadLivePorts renders nothing -> idle text.
	assert.Equal(t, "serial: idle\n", out.String())
}

func TestRunStatusline_PortsOnlyModeSilentWhenEmpty(t *testing.T) {
	tmpDir := t.TempDir()
	prev := status.SetStatusDir(tmpDir)
	defer status.SetStatusDir(prev)
	t.Setenv("POGOPIN_SESSION_ID", "sess-x")
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	t.Setenv("POGOPIN_STATUSLINE_MODE", "ports-only")

	cmd := statuslineCmd
	var out bytes.Buffer
	cmd.SetIn(strings.NewReader(""))
	cmd.SetOut(&out)

	err := runStatusline(cmd, nil)
	require.NoError(t, err)
	assert.Empty(t, out.String())
}

func TestStatuslineCmd_RegisteredOnRoot(t *testing.T) {
	found := false
	for _, c := range rootCmd.Commands() {
		if c.Use == "statusline" {
			found = true
		}
	}
	assert.True(t, found, "statusline command should be registered on root")
}
