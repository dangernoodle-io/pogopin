package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/dangernoodle-io/shesha/host/claudecode/statusline"
	"github.com/dangernoodle-io/shesha/style"
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

func runStatuslineCmd(t *testing.T, opts ...statusline.Option) (*bytes.Buffer, func(stdin string)) {
	t.Helper()
	cmd := statusline.Command(statuslineProvider{}, opts...)
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetContext(context.Background())

	run := func(stdin string) {
		cmd.SetIn(strings.NewReader(stdin))
		cmd.SetArgs(nil)
		require.NoError(t, cmd.Execute())
	}

	return &out, run
}

func TestStatuslineParity_IdleAlwaysMode(t *testing.T) {
	tmpDir := t.TempDir()
	prev := status.SetStatusDir(tmpDir)
	defer status.SetStatusDir(prev)
	t.Setenv("POGOPIN_SESSION_ID", "sess-x")
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	t.Setenv("POGOPIN_STATUSLINE_MODE", "")

	out, run := runStatuslineCmd(t, statusline.WithAppPrefix("POGOPIN"), statusline.WithForceLevel(style.LevelNone))
	run("")

	assert.Equal(t, "serial: idle\n", out.String())
}

func TestStatuslineParity_IdleQuietMode(t *testing.T) {
	tmpDir := t.TempDir()
	prev := status.SetStatusDir(tmpDir)
	defer status.SetStatusDir(prev)
	t.Setenv("POGOPIN_SESSION_ID", "sess-x")
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	t.Setenv("POGOPIN_STATUSLINE_MODE", "ports-only")

	out, run := runStatuslineCmd(t, statusline.WithAppPrefix("POGOPIN"), statusline.WithForceLevel(style.LevelNone))
	run("")

	assert.Empty(t, out.String())
}

func TestStatuslineParity_SinglePort(t *testing.T) {
	tmpDir := t.TempDir()
	prev := status.SetStatusDir(tmpDir)
	defer status.SetStatusDir(prev)
	t.Setenv("POGOPIN_SESSION_ID", "")
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	t.Setenv("POGOPIN_STATUSLINE_MODE", "")

	writeStatusFileFor(t, tmpDir, os.Getpid(), []status.PortState{
		{Port: "/dev/ttyUSB0", Baud: 115200, Mode: "reader", BufferLines: 42, PID: os.Getpid(), SessionID: "sess-x"},
	})

	out, run := runStatuslineCmd(t, statusline.WithAppPrefix("POGOPIN"), statusline.WithForceLevel(style.LevelNone))
	run(`{"session_id":"sess-x"}`)

	assert.Equal(t, "serial: ttyUSB0@115200 reader 42L\n", out.String())
}

func TestStatuslineParity_MultiplePortsJoined(t *testing.T) {
	tmpDir := t.TempDir()
	prev := status.SetStatusDir(tmpDir)
	defer status.SetStatusDir(prev)
	t.Setenv("POGOPIN_SESSION_ID", "")
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	t.Setenv("POGOPIN_STATUSLINE_MODE", "")

	writeStatusFileFor(t, tmpDir, os.Getpid(), []status.PortState{
		{Port: "/dev/ttyUSB0", Baud: 115200, Mode: "reader", BufferLines: 5, PID: os.Getpid(), SessionID: "sess-x"},
		{Port: "/dev/ttyACM0", Baud: 921600, Mode: "flasher", BufferLines: 0, PID: os.Getpid(), SessionID: "sess-x"},
	})

	out, run := runStatuslineCmd(t, statusline.WithAppPrefix("POGOPIN"), statusline.WithForceLevel(style.LevelNone))
	run(`{"session_id":"sess-x"}`)

	assert.Equal(t, "serial: ttyUSB0@115200 reader 5L | serial: ttyACM0@921600 flasher 0L\n", out.String())
}

func TestStatuslineColor_SinglePortHasEscapes(t *testing.T) {
	tmpDir := t.TempDir()
	prev := status.SetStatusDir(tmpDir)
	defer status.SetStatusDir(prev)
	t.Setenv("POGOPIN_SESSION_ID", "")
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	t.Setenv("POGOPIN_STATUSLINE_MODE", "")

	writeStatusFileFor(t, tmpDir, os.Getpid(), []status.PortState{
		{Port: "/dev/ttyUSB0", Baud: 115200, Mode: "reader", BufferLines: 42, PID: os.Getpid(), SessionID: "sess-x"},
	})

	plain, runPlain := runStatuslineCmd(t, statusline.WithAppPrefix("POGOPIN"), statusline.WithForceLevel(style.LevelNone))
	runPlain(`{"session_id":"sess-x"}`)

	colored, runColored := runStatuslineCmd(t, statusline.WithAppPrefix("POGOPIN"), statusline.WithForceLevel(style.LevelBasic))
	runColored(`{"session_id":"sess-x"}`)

	assert.Contains(t, colored.String(), "\x1b[")
	assert.NotContains(t, plain.String(), "\x1b[")
	assert.NotEqual(t, plain.String(), colored.String())
}

func TestStatuslineColor_IdleHasEscapes(t *testing.T) {
	tmpDir := t.TempDir()
	prev := status.SetStatusDir(tmpDir)
	defer status.SetStatusDir(prev)
	t.Setenv("POGOPIN_SESSION_ID", "sess-x")
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	t.Setenv("POGOPIN_STATUSLINE_MODE", "")

	colored, runColored := runStatuslineCmd(t, statusline.WithAppPrefix("POGOPIN"), statusline.WithForceLevel(style.LevelBasic))
	runColored("")

	assert.Contains(t, colored.String(), "\x1b[")
}

func TestModeColor(t *testing.T) {
	cases := []struct {
		mode string
		want string
	}{
		{"reader", colorModeReader},
		{"flasher", colorModeFlasher},
		{"external", colorModeExternal},
		{"pending", ""},
		{"", ""},
	}
	for _, tc := range cases {
		t.Run(tc.mode, func(t *testing.T) {
			assert.Equal(t, tc.want, modeColor(tc.mode))
		})
	}
}

func TestStatuslineProvider_ModeResolution(t *testing.T) {
	tmpDir := t.TempDir()
	prev := status.SetStatusDir(tmpDir)
	defer status.SetStatusDir(prev)
	t.Setenv("POGOPIN_SESSION_ID", "sess-x")
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")

	cases := []struct {
		name     string
		envMode  string
		wantIdle bool
	}{
		{"always (default) renders idle text", "", true},
		{"ports-only renders nothing", "ports-only", false},
		{"fresh-only renders nothing", "fresh-only", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("POGOPIN_STATUSLINE_MODE", tc.envMode)
			segs, err := statuslineProvider{}.Statusline(context.Background(), statusline.Payload{}, "sess-x")
			require.NoError(t, err)
			if tc.wantIdle {
				require.Len(t, segs, 1)
				assert.Equal(t, "serial: idle", segs[0].Text)
			} else {
				assert.Empty(t, segs)
			}
		})
	}
}

func TestStatuslineProvider_NoSessionRendersIdleNotOtherSessionPorts(t *testing.T) {
	tmpDir := t.TempDir()
	prev := status.SetStatusDir(tmpDir)
	defer status.SetStatusDir(prev)
	t.Setenv("POGOPIN_STATUSLINE_MODE", "")

	writeStatusFileFor(t, tmpDir, os.Getpid(), []status.PortState{
		{Port: "/dev/ttyUSB0", Baud: 115200, Mode: "reader", BufferLines: 3, PID: os.Getpid(), SessionID: "sess-x"},
	})

	// sessionID == "" -> ReadLivePorts renders nothing -> idle text (ModeAlways default).
	segs, err := statuslineProvider{}.Statusline(context.Background(), statusline.Payload{}, "")
	require.NoError(t, err)
	require.Len(t, segs, 1)
	assert.Equal(t, "serial: idle", segs[0].Text)
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

func TestStatuslineCmd_SessionIDPrecedence(t *testing.T) {
	tmpDir := t.TempDir()
	prev := status.SetStatusDir(tmpDir)
	defer status.SetStatusDir(prev)
	t.Setenv("POGOPIN_STATUSLINE_MODE", "")

	writeStatusFileFor(t, tmpDir, os.Getpid(), []status.PortState{
		{Port: "/dev/ttyUSB0", Baud: 115200, Mode: "reader", BufferLines: 3, PID: os.Getpid(), SessionID: "sess-env"},
	})

	// POGOPIN_SESSION_ID wins over stdin session_id and CLAUDE_CODE_SESSION_ID.
	t.Setenv("POGOPIN_SESSION_ID", "sess-env")
	t.Setenv("CLAUDE_CODE_SESSION_ID", "sess-claude")

	out, run := runStatuslineCmd(t, statusline.WithAppPrefix("POGOPIN"), statusline.WithForceLevel(style.LevelNone))
	run(`{"session_id":"sess-stdin"}`)

	assert.Equal(t, "serial: ttyUSB0@115200 reader 3L\n", out.String())
}

// TestStatuslineCmd_StdinSessionIDBeatsClaudeEnv isolates tier2 > tier3 of
// the Resolve precedence: with POGOPIN_SESSION_ID UNSET, the stdin payload's
// session_id must win over CLAUDE_CODE_SESSION_ID when both are set and
// differ. TestStatuslineCmd_SessionIDPrecedence only proves tier1 beats the
// lower two together; this proves tier2 beats tier3 on its own.
func TestStatuslineCmd_StdinSessionIDBeatsClaudeEnv(t *testing.T) {
	tmpDir := t.TempDir()
	prev := status.SetStatusDir(tmpDir)
	defer status.SetStatusDir(prev)
	t.Setenv("POGOPIN_STATUSLINE_MODE", "")
	t.Setenv("POGOPIN_SESSION_ID", "")
	t.Setenv("CLAUDE_CODE_SESSION_ID", "sess-claude")

	writeStatusFileFor(t, tmpDir, os.Getpid(), []status.PortState{
		{Port: "/dev/ttyUSB0", Baud: 115200, Mode: "reader", BufferLines: 3, PID: os.Getpid(), SessionID: "sess-stdin"},
	})

	out, run := runStatuslineCmd(t, statusline.WithAppPrefix("POGOPIN"), statusline.WithForceLevel(style.LevelNone))
	run(`{"session_id":"sess-stdin"}`)

	assert.Equal(t, "serial: ttyUSB0@115200 reader 3L\n", out.String())
}

// TestStatuslineCmd_ProductionConstructionRendersColor sources the
// production statuslineOpts var (rather than re-deriving its own copy of
// WithAppPrefix/WithForceLevel) so a future change to statuslineCmd's
// options is automatically reflected here too. It builds a fresh Command
// instance from those opts rather than reusing the package-level
// statuslineCmd var, since that var is already mounted as a child of
// rootCmd (root.go's init) and cobra's Execute() redirects any command
// with a parent to Root().ExecuteC(), which would run against real
// os.Args instead of the args a test sets directly on the child. It
// proves color is reachable from the actual production wiring
// (WithForceLevel(LevelBasic), no --plain) — not merely when a test
// forces LevelBasic itself. This is the regression guard for the review
// finding that color was inert because Claude Code always pipes
// statusline stdout to a non-TTY, so uncontrolled style.Detect(w) would
// silently resolve LevelNone.
func TestStatuslineCmd_ProductionConstructionRendersColor(t *testing.T) {
	tmpDir := t.TempDir()
	prev := status.SetStatusDir(tmpDir)
	defer status.SetStatusDir(prev)
	t.Setenv("POGOPIN_SESSION_ID", "")
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	t.Setenv("POGOPIN_STATUSLINE_MODE", "")

	writeStatusFileFor(t, tmpDir, os.Getpid(), []status.PortState{
		{Port: "/dev/ttyUSB0", Baud: 115200, Mode: "reader", BufferLines: 42, PID: os.Getpid(), SessionID: "sess-x"},
	})

	out, run := runStatuslineCmd(t, statuslineOpts...)
	run(`{"session_id":"sess-x"}`)

	assert.Contains(t, out.String(), "\x1b[")
}

// TestStatuslineCmd_ProductionConstructionPlainFlagStillPlain sources the
// same shared statuslineOpts var and proves --plain still overrides the
// forced LevelBasic on the production option set, collapsing to the exact
// byte-parity plain output.
func TestStatuslineCmd_ProductionConstructionPlainFlagStillPlain(t *testing.T) {
	tmpDir := t.TempDir()
	prev := status.SetStatusDir(tmpDir)
	defer status.SetStatusDir(prev)
	t.Setenv("POGOPIN_SESSION_ID", "")
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	t.Setenv("POGOPIN_STATUSLINE_MODE", "")

	writeStatusFileFor(t, tmpDir, os.Getpid(), []status.PortState{
		{Port: "/dev/ttyUSB0", Baud: 115200, Mode: "reader", BufferLines: 42, PID: os.Getpid(), SessionID: "sess-x"},
	})

	cmd := statusline.Command(statuslineProvider{}, statuslineOpts...)
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetContext(context.Background())
	cmd.SetIn(strings.NewReader(`{"session_id":"sess-x"}`))
	cmd.SetArgs([]string{"--plain"})
	require.NoError(t, cmd.Execute())

	assert.Equal(t, "serial: ttyUSB0@115200 reader 42L\n", out.String())
}

// TestExecute_StatuslineSubcommandProductionWiring drives the package-level
// Execute() entrypoint (main.go's only call) with args targeting `statusline
// --plain`, rather than building a standalone statusline.Command like the
// tests above. This is the one path that exercises the *production*
// statuslineCmd package var as actually mounted on rootCmd (root.go's
// init), through the real Execute() -> rootCmd.Execute() call chain — every
// other test in this file builds its own fresh Command and never touches
// Execute() or rootCmd's registration at all.
func TestExecute_StatuslineSubcommandProductionWiring(t *testing.T) {
	tmpDir := t.TempDir()
	prev := status.SetStatusDir(tmpDir)
	defer status.SetStatusDir(prev)
	t.Setenv("POGOPIN_SESSION_ID", "")
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	t.Setenv("POGOPIN_STATUSLINE_MODE", "")

	writeStatusFileFor(t, tmpDir, os.Getpid(), []status.PortState{
		{Port: "/dev/ttyUSB0", Baud: 115200, Mode: "reader", BufferLines: 7, PID: os.Getpid(), SessionID: "sess-x"},
	})

	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	rootCmd.SetIn(strings.NewReader(`{"session_id":"sess-x"}`))
	rootCmd.SetArgs([]string{"statusline", "--plain"})
	defer func() {
		rootCmd.SetOut(nil)
		rootCmd.SetErr(nil)
		rootCmd.SetIn(nil)
		rootCmd.SetArgs(nil)
	}()

	require.NoError(t, Execute())

	assert.Equal(t, "serial: ttyUSB0@115200 reader 7L\n", out.String())
}
