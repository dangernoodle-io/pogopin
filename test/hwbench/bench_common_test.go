// Package hwbench drives the pogopin MCP server over its real stdio wire
// protocol against a target chip. This file (untagged, compiled into every
// `go test ./test/hwbench/...` invocation) holds the scaffolding shared by
// both lanes: the hardware-gated TestHWBench (hwbench_test.go, `hwtest`
// build tag) and the hardware-free TestMockBench (mock_test.go, gated on
// ACC_POGOPIN) against internal/mockhw's virtual chip. Neither lane runs
// its scenarios directly from here — runGPIOScenarios, runSecurityInfoScenario,
// and runChipIdentityScenario hold the shared subtests, called by both
// TestHWBench and TestMockBench.
package hwbench

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"dangernoodle.io/pogopin/internal/mockhw"
	"dangernoodle.io/pogopin/internal/session"
)

// boardProfile describes the fixed hardware characteristics of one bench
// board: which GPIO drives a simple visual LED (if any), whether the chip
// enumerates over native USB (which forces reset_mode=no_reset — a
// reset-based connect hangs/desyncs the ROM on these chips), what kind of
// LED sits on LEDPin (a plain GPIO LED can be driven with esp_gpio_set; an
// APA102 needs a real SPI/bit-bang driver that esp_gpio_set can't provide,
// so LED-visual scenarios skip on those boards), the espflasher chip
// family name this board's chip detects as (asserted against esp_info's
// chip.chip_name by runChipIdentityScenario, and used for security chip_id
// by runSecurityInfoScenario), and — for TestMockBench only — which
// internal/mockhw virtual-chip port this board key maps to (real-hardware
// TestHWBench ignores MockPort; the port comes from POGOPIN_HW_PORT there).
type boardProfile struct {
	Name      string
	LEDPin    int
	NativeUSB bool
	LEDType   string // "gpio", "apa102", "rgb"

	// ChipFamily is the espflasher ChipName() this board's chip detects as
	// (e.g. "ESP32-S2") — asserted against esp_info's chip.chip_name by
	// runChipIdentityScenario, proving the port->profile->detection chain
	// serves the right chip for this board key.
	ChipFamily string
	MockPort   string // internal/mockhw port name for this chip family

	// SecurityChipID is the espflasher ImageChipID this board's chip is
	// detected by via GET_SECURITY_INFO (0 = chip is magic-detected
	// instead and has no ChipID; runSecurityInfoScenario skips it).
	SecurityChipID uint32

	// ReservedPin is a GPIO number espflasher's GPIOReserved reports
	// reserved on this chip family, used by the reserved-pin-refusal
	// scenarios. GPIO0 is a strapping pin on ESP32/S2/S3 but NOT on
	// ESP32-C3 (espflasher's defESP32C3GPIO reserved map has no entry for
	// pin 0 — its strap pins are 2/8/9), so this can't be a single
	// hardcoded constant across chip families.
	ReservedPin int
}

// boardProfiles is keyed by the POGOPIN_HW_BOARD/ACC_POGOPIN_BOARD value
// used to select the active profile.
var boardProfiles = []struct {
	Key     string
	Profile boardProfile
}{
	{"s2", boardProfile{Name: "S2 Mini", LEDPin: 15, NativeUSB: true, LEDType: "gpio", ChipFamily: "ESP32-S2", MockPort: mockhw.MockPortNameS2}},
	// C3 Mini's onboard LED pin varies by vendor board revision; 4 is a
	// non-reserved GPIO on every ESP32-C3 (unlike the previous default of
	// 15, which is a "flash"-reserved pin on every C3 — esp_gpio_set would
	// refuse it on real hardware; caught by BR-66 PR3's mock-lane fix that
	// finally routes the `c3` board key to a real C3 mock profile instead
	// of always talking to S2). Still NOT verified to actually drive an LED
	// on a specific C3 Mini SKU. Override with POGOPIN_HW_LED_PIN if it
	// doesn't drive an LED on your board.
	{"c3", boardProfile{Name: "C3 Mini", LEDPin: 4, NativeUSB: true, LEDType: "gpio", ChipFamily: "ESP32-C3", MockPort: mockhw.MockPortNameC3, SecurityChipID: 5, ReservedPin: 2}},
	// S3 T-Dongle's status LED is an APA102 (clock+data, bit-banged SPI
	// protocol) — not a plain GPIO the ROM bootloader can toggle with a
	// single register write, so LEDPin is not drivable via esp_gpio_set.
	// LED-visual scenarios skip on this profile.
	{"s3dongle", boardProfile{Name: "S3 T-Dongle", LEDPin: 0, NativeUSB: true, LEDType: "apa102", ChipFamily: "ESP32-S3", MockPort: mockhw.MockPortNameS3, SecurityChipID: 9}},
	// CYD (ESP32/CH340) has a 3-pin RGB LED (R=22, G=16, B=17, per the
	// prompt's pin assignment); LEDPin defaults to the red channel (22).
	// It's still a plain GPIO from the ROM's perspective, so esp_gpio_set
	// drives it fine — only the visual result (one LED channel, not the
	// vendor's usual "RGB" impression) differs from a single-color LED.
	{"cyd", boardProfile{Name: "CYD", LEDPin: 22, NativeUSB: false, LEDType: "rgb", ChipFamily: "ESP32", MockPort: mockhw.MockPortNameESP32}},
}

func lookupProfile(key string) (boardProfile, bool) {
	for _, e := range boardProfiles {
		if e.Key == key {
			return e.Profile, true
		}
	}
	return boardProfile{}, false
}

// expectedESPToolMin is the minimum number of esp_ tools the hardware tier
// must register (13 today: esp_flash, esp_erase, esp_info, esp_register,
// esp_reset, esp_read_flash, esp_read_nvs, esp_write_nvs, esp_nvs_set,
// esp_nvs_delete, esp_gpio_read, esp_gpio_set, esp_gpio_sweep). Asserted as
// a floor (>=), not an exact match, so adding a new esp_ tool doesn't break
// this harness.
const expectedESPToolMin = 13

// harness bundles the live MCP client plus test bookkeeping (progress
// notifications keyed by token) needed across scenarios.
type harness struct {
	t       *testing.T
	client  *client.Client
	port    string
	profile boardProfile

	mu       sync.Mutex
	progress map[string][]mcp.ProgressNotificationParams
	nextTok  int
}

// newHarnessWithBinary spawns the given pogo server binary and connects the
// MCP client to it. Called from both TestHWBench (hwbench_test.go, hwtest
// build tag) with a plain-built binary, and TestMockBench (mock_test.go)
// with a `-tags mock` binary — resolveBinary handles the build in each
// case.
func newHarnessWithBinary(t *testing.T, bin string, port string, profile boardProfile) *harness {
	c, err := client.NewStdioMCPClientWithOptions(bin, nil, []string{"server"})
	require.NoError(t, err, "spawn pogo server")

	// Drain the subprocess's stderr pipe to prevent deadlock on large stderr
	// output. The Scanner is sized to accept lines up to 1MB; if a line exceeds
	// this, a fallback io.Copy keeps draining even after Scan stops early, so
	// the pipe can never fill and block the test. This goroutine only ever
	// reads, so it can't block the test. It exits on EOF once the client (and
	// its underlying process) closes. t.Log is only safe to call while the test
	// is still running, so stderr lines are buffered here and flushed to t.Log
	// from the Cleanup itself (which runs on the test goroutine before the test
	// is marked complete), after waiting for the drain goroutine to observe EOF.
	var stderrWG sync.WaitGroup
	var stderrMu sync.Mutex
	var stderrLines []string
	if stderr, ok := client.GetStderr(c); ok {
		stderrWG.Add(1)
		go func() {
			defer stderrWG.Done()
			scanner := bufio.NewScanner(stderr)
			scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
			for scanner.Scan() {
				line := scanner.Text()
				stderrMu.Lock()
				stderrLines = append(stderrLines, line)
				stderrMu.Unlock()
			}
			// Keep draining even if Scan stopped early (e.g. a line exceeding the cap)
			// so the subprocess can never block on a full stderr pipe.
			_, _ = io.Copy(io.Discard, stderr)
		}()
	}
	t.Cleanup(func() {
		_ = c.Close()
		stderrWG.Wait()
		stderrMu.Lock()
		defer stderrMu.Unlock()
		for _, line := range stderrLines {
			t.Log("pogo server stderr: " + line)
		}
	})

	h := &harness{
		t:        t,
		client:   c,
		port:     port,
		profile:  profile,
		progress: make(map[string][]mcp.ProgressNotificationParams),
	}

	c.OnNotification(func(n mcp.JSONRPCNotification) {
		if n.Method != "notifications/progress" {
			return
		}
		raw, err := json.Marshal(n.Params.AdditionalFields)
		if err != nil {
			return
		}
		var p mcp.ProgressNotificationParams
		if err := json.Unmarshal(raw, &p); err != nil {
			return
		}
		tok := fmt.Sprintf("%v", p.ProgressToken)
		h.mu.Lock()
		h.progress[tok] = append(h.progress[tok], p)
		h.mu.Unlock()
	})

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// client.NewStdioMCPClientWithOptions only starts the underlying
	// transport; it does NOT call (*client.Client).Start, which is what
	// wires the transport's onNotification dispatcher onto the handlers
	// registered via OnNotification above (see stdio.go's
	// SetNotificationHandler, only invoked from Client.Start). Without this
	// call the server-sent notifications/progress frames are read off the
	// wire and silently dropped at the transport layer (onNotification is
	// nil), even though a progressToken was supplied on the call and the
	// server genuinely emitted -- this is what produced the "0 >= 1"
	// gpio_sweep progress-notification failure on real hardware.
	require.NoError(t, c.Start(ctx), "start mcp client transport")

	_, err = c.Initialize(ctx, mcp.InitializeRequest{
		Params: mcp.InitializeParams{
			ProtocolVersion: mcp.LATEST_PROTOCOL_VERSION,
			ClientInfo:      mcp.Implementation{Name: "pogopin-hwbench", Version: "0.1.0"},
		},
	})
	require.NoError(t, err, "initialize")

	return h
}

// resolveBinary honors the named override env var if set (empty string
// disables the override check), else `go build`s the server binary — with
// the given extra build tags, if any (the mock lane passes []string{"mock"})
// — into a temp path from the repo root (two levels up from this test file:
// test/hwbench -> test -> repo root).
func resolveBinary(t *testing.T, overrideEnv string, extraTags []string) string {
	if overrideEnv != "" {
		if bin := os.Getenv(overrideEnv); bin != "" {
			return bin
		}
	}

	_, thisFile, _, ok := runtime.Caller(0)
	require.True(t, ok, "resolve repo root: runtime.Caller failed")
	repoRoot := filepath.Dir(filepath.Dir(filepath.Dir(thisFile)))

	bin := filepath.Join(t.TempDir(), "pogo-hwbench")
	args := []string{"build"}
	if len(extraTags) > 0 {
		args = append(args, "-tags", strings.Join(extraTags, ","))
	}
	args = append(args, "-o", bin, ".")
	cmd := exec.Command("go", args...)
	cmd.Dir = repoRoot
	// CGO_ENABLED=0 for parity with the flashed binary — the Makefile's
	// canonical `make build` sets it and a CGO-enabled build here could mask
	// a CGO-only regression that would break the real release artifact.
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "go build pogo server:\n%s", out)
	return bin
}

// nextToken returns a fresh progress token string, used as the MCP
// progressToken for a call so this scenario's notifications don't collide
// with another's in the shared h.progress map.
func (h *harness) nextToken() string {
	h.mu.Lock()
	h.nextTok++
	tok := strconv.Itoa(h.nextTok)
	h.mu.Unlock()
	return tok
}

func (h *harness) progressFor(tok string) []mcp.ProgressNotificationParams {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.progress[tok]
}

// callTool issues tools/call for name with args, always attaching a fresh
// progress token so callers can inspect h.progressFor(tok) afterward.
func (h *harness) callTool(ctx context.Context, name string, args map[string]any) (*mcp.CallToolResult, string, error) {
	tok := h.nextToken()
	req := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name:      name,
			Arguments: args,
			Meta:      &mcp.Meta{ProgressToken: tok},
		},
	}
	res, err := h.client.CallTool(ctx, req)
	return res, tok, err
}

// gpioArgs builds the argument map for an esp_gpio_* call, always pinning
// reset_mode to no_reset — on native-USB chips a reset-based connect
// hangs/desyncs the ROM, so every gpio call this harness makes must use it.
func gpioArgs(port string, extra map[string]any) map[string]any {
	args := map[string]any{
		"port":       port,
		"reset_mode": "no_reset",
	}
	for k, v := range extra {
		args[k] = v
	}
	return args
}

func resultText(res *mcp.CallToolResult) string {
	if res == nil {
		return ""
	}
	var sb strings.Builder
	for _, c := range res.Content {
		if tc, ok := mcp.AsTextContent(c); ok {
			sb.WriteString(tc.Text)
		}
	}
	return sb.String()
}

// runGPIOScenarios holds the 7 scenarios shared by TestHWBench (real
// hardware) and TestMockBench (internal/mockhw virtual chip) — identical
// assertions against either target, driven entirely through the real MCP
// stdio wire protocol.
func runGPIOScenarios(t *testing.T, h *harness) {
	t.Run("serial_list_unlocks_hardware_tier", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()

		res, _, err := h.callTool(ctx, "serial_list", nil)
		require.NoError(t, err, "serial_list")
		require.False(t, res.IsError, "serial_list returned error: %s", resultText(res))

		var ports []struct {
			Name string `json:"name"`
		}
		err = json.Unmarshal([]byte(resultText(res)), &ports)
		require.NoError(t, err, "parse serial_list result: %s", resultText(res))
		found := false
		for _, p := range ports {
			if p.Name == h.port {
				found = true
				break
			}
		}
		assert.True(t, found, "serial_list result does not contain configured port %q: %v", h.port, ports)
	})

	t.Run("tools_list_includes_gpio_tools", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()

		res, err := h.client.ListTools(ctx, mcp.ListToolsRequest{})
		require.NoError(t, err, "tools/list")

		names := make(map[string]bool, len(res.Tools))
		espCount := 0
		for _, tool := range res.Tools {
			names[tool.Name] = true
			if strings.HasPrefix(tool.Name, "esp_") {
				espCount++
			}
		}

		for _, want := range []string{"esp_gpio_read", "esp_gpio_set", "esp_gpio_sweep"} {
			assert.True(t, names[want], "tools/list missing %s", want)
		}
		assert.GreaterOrEqual(t, espCount, expectedESPToolMin, "esp_ tool count")
	})

	t.Run("gpio_read_no_magic_0x9", func(t *testing.T) {
		if h.profile.LEDType == "apa102" {
			t.Skip("no plain GPIO LED to probe on this board (apa102)")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		for i := 0; i < 2; i++ {
			res, _, err := h.callTool(ctx, "esp_gpio_read", gpioArgs(h.port, map[string]any{
				"pin": h.profile.LEDPin,
			}))
			require.NoErrorf(t, err, "esp_gpio_read call %d", i)
			require.Falsef(t, res.IsError, "esp_gpio_read call %d returned error: %s", i, resultText(res))
			text := strings.ToLower(resultText(res))
			assert.Falsef(t, strings.Contains(text, "magic") || strings.Contains(text, "0x9"),
				"esp_gpio_read call %d result mentions magic/0x9: %s", i, resultText(res))
		}
	})

	t.Run("gpio_set_high_then_low", func(t *testing.T) {
		if h.profile.LEDType == "apa102" {
			t.Skip("LED is apa102, not a plain GPIO esp_gpio_set can drive")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		for _, level := range []bool{true, false} {
			res, _, err := h.callTool(ctx, "esp_gpio_set", gpioArgs(h.port, map[string]any{
				"pin":   h.profile.LEDPin,
				"level": level,
			}))
			require.NoErrorf(t, err, "esp_gpio_set(level=%v)", level)
			require.Falsef(t, res.IsError, "esp_gpio_set(level=%v) returned error: %s", level, resultText(res))
		}
	})

	t.Run("gpio_read_survives_5s_expiry_no_reset", func(t *testing.T) {
		if h.profile.LEDType == "apa102" {
			t.Skip("no plain GPIO LED to probe on this board (apa102)")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()

		res, _, err := h.callTool(ctx, "esp_gpio_read", gpioArgs(h.port, map[string]any{
			"pin": h.profile.LEDPin,
		}))
		require.NoError(t, err, "esp_gpio_read (pre-expiry)")
		require.False(t, res.IsError, "esp_gpio_read (pre-expiry) returned error: %s", resultText(res))

		// Wait past session.DeferredRestartTimeout (the session package's
		// deferred-release idle window) before reattaching, with 2s headroom
		// on top so retuning that constant doesn't shrink the margin below
		// the boundary this scenario exists to test. This is the
		// load-bearing scenario: it validates the no-reset-on-expire fix,
		// where a naive deferred release would Reset() the chip out of the
		// bootloader on expiry and the following connect would then need a
		// full resync.
		time.Sleep(session.DeferredRestartTimeout() + 2*time.Second)

		ctx2, cancel2 := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel2()
		res2, _, err := h.callTool(ctx2, "esp_gpio_read", gpioArgs(h.port, map[string]any{
			"pin": h.profile.LEDPin,
		}))
		require.NoError(t, err, "esp_gpio_read (post-expiry)")
		require.False(t, res2.IsError, "esp_gpio_read (post-expiry) returned error (no-reset-on-expire regression?): %s", resultText(res2))
		assert.NotContains(t, strings.ToLower(resultText(res2)), "sync failed",
			"esp_gpio_read (post-expiry) reports a resync failure")
	})

	t.Run("gpio_set_reserved_pin_refused", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()

		// h.profile.ReservedPin is a strapping pin (or similar) reserved by
		// espflasher's GPIOReserved for this chip family — refused by
		// default without include_reserved. Not GPIO0 on every chip: C3's
		// strap pins are 2/8/9, not 0 (see ReservedPin's doc comment).
		res, _, err := h.callTool(ctx, "esp_gpio_set", gpioArgs(h.port, map[string]any{
			"pin":   h.profile.ReservedPin,
			"level": true,
		}))
		require.NoError(t, err, "esp_gpio_set(pin=%d)", h.profile.ReservedPin)
		require.True(t, res.IsError, "esp_gpio_set(pin=%d) expected a reserved-pin refusal, got success: %s", h.profile.ReservedPin, resultText(res))
		assert.Contains(t, strings.ToLower(resultText(res)), "reserved",
			"esp_gpio_set(pin=%d) error does not mention 'reserved': %s", h.profile.ReservedPin, resultText(res))
	})

	t.Run("gpio_sweep_skips_reserved_and_emits_progress", func(t *testing.T) {
		if h.profile.LEDType == "apa102" {
			t.Skip("no plain GPIO LED to probe on this board (apa102)")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		pins := fmt.Sprintf("%d,%d", h.profile.LEDPin, h.profile.ReservedPin) // one valid pin + one reserved pin
		res, tok, err := h.callTool(ctx, "esp_gpio_sweep", gpioArgs(h.port, map[string]any{
			"pins":  pins,
			"dwell": 1,
			"both":  false,
		}))
		require.NoError(t, err, "esp_gpio_sweep")
		require.False(t, res.IsError, "esp_gpio_sweep returned error: %s", resultText(res))

		var sweep struct {
			Pins []struct {
				Pin     int    `json:"pin"`
				Skipped bool   `json:"skipped"`
				Reason  string `json:"reason"`
			} `json:"pins"`
		}
		err = json.Unmarshal([]byte(resultText(res)), &sweep)
		require.NoError(t, err, "parse esp_gpio_sweep result: %s", resultText(res))

		var sawLED, sawReserved bool
		for _, p := range sweep.Pins {
			switch p.Pin {
			case h.profile.LEDPin:
				sawLED = true
				assert.Falsef(t, p.Skipped, "LED pin %d unexpectedly skipped: %s", p.Pin, p.Reason)
			case h.profile.ReservedPin:
				sawReserved = true
				assert.True(t, p.Skipped, "reserved pin %d was not skipped", h.profile.ReservedPin)
			}
		}
		assert.True(t, sawLED, "sweep result missing LED pin %d", h.profile.LEDPin)
		assert.True(t, sawReserved, "sweep result missing reserved pin %d", h.profile.ReservedPin)

		assert.GreaterOrEqual(t, len(h.progressFor(tok)), 1, "esp_gpio_sweep emitted no notifications/progress")
	})
}

// runSecurityInfoScenario exercises esp_info include=security for a chip
// that's detected via GET_SECURITY_INFO's ChipID field (ESP32-C3,
// ESP32-S3 — espflasher's UsesMagicValue:false path), asserting the
// returned chip_id matches h.profile.SecurityChipID with no error. Skips
// when SecurityChipID is 0 (magic-detected chips never expose a ChipID; on
// the mock target GET_SECURITY_INFO deliberately errors for them so
// espflasher's detectChip falls through to the chip-magic path, so
// esp_info include=security has nothing to assert there).
func runSecurityInfoScenario(t *testing.T, h *harness) {
	t.Run("esp_info_security_chip_id", func(t *testing.T) {
		if h.profile.SecurityChipID == 0 {
			t.Skip("board's chip is magic-detected, not ChipID-detected — no security chip_id to assert")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()

		res, _, err := h.callTool(ctx, "esp_info", map[string]any{
			"port":    h.port,
			"include": "security",
		})
		require.NoError(t, err, "esp_info include=security")
		require.False(t, res.IsError, "esp_info include=security returned error: %s", resultText(res))

		var out struct {
			Security struct {
				ChipID *uint32 `json:"chip_id"`
			} `json:"security"`
		}
		require.NoError(t, json.Unmarshal([]byte(resultText(res)), &out), "parse esp_info result: %s", resultText(res))
		require.NotNil(t, out.Security.ChipID, "esp_info include=security result missing chip_id: %s", resultText(res))
		assert.Equal(t, h.profile.SecurityChipID, *out.Security.ChipID)
	})
}

// runChipIdentityScenario exercises esp_info's default chip section
// (include="chip", no security probe needed) and asserts the reported
// chip.chip_name equals h.profile.ChipFamily — proving the
// port->profile->detection chain serves the correct chip for this board
// key (would catch, e.g., a profileByPort mismap routing the c3 board key
// to an S2 mock chip). Skips if ChipFamily is unset (defensive; every
// entry in boardProfiles sets it today).
func runChipIdentityScenario(t *testing.T, h *harness) {
	t.Run("esp_info_chip_identity", func(t *testing.T) {
		if h.profile.ChipFamily == "" {
			t.Skip("board profile has no ChipFamily to assert")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()

		res, _, err := h.callTool(ctx, "esp_info", map[string]any{
			"port": h.port,
		})
		require.NoError(t, err, "esp_info")
		require.False(t, res.IsError, "esp_info returned error: %s", resultText(res))

		var out struct {
			Chip struct {
				ChipName string `json:"chip_name"`
			} `json:"chip"`
		}
		require.NoError(t, json.Unmarshal([]byte(resultText(res)), &out), "parse esp_info result: %s", resultText(res))
		assert.Equal(t, h.profile.ChipFamily, out.Chip.ChipName, "esp_info chip.chip_name does not match board profile's ChipFamily")
	})
}

// runSerialMonitorScenarios exercises serial_start/read/write/stop against
// internal/mockhw's virtual monitor port (BR-66 PR2) — hardware-free,
// through the same real MCP stdio wire protocol runGPIOScenarios uses. Only
// called by TestMockBench: there is no serial-monitor equivalent in
// TestHWBench today, since the real-hardware bench doesn't drive a boot
// banner it can assert against.
func runSerialMonitorScenarios(t *testing.T, h *harness) {
	// serialReadTextContaining polls serial_read until want appears or the
	// timeout elapses, guarding against the inherent goroutine-scheduling
	// race between starting/writing the port and the manager's readLoop
	// goroutine draining the virtual monitor port's outbound queue.
	serialReadTextContaining := func(t *testing.T, want string) string {
		t.Helper()
		var text string
		require.Eventually(t, func() bool {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			res, _, err := h.callTool(ctx, "serial_read", map[string]any{
				"port": h.port,
			})
			if err != nil || res == nil || res.IsError {
				return false
			}
			text = resultText(res)
			return strings.Contains(text, want)
		}, 2*time.Second, 10*time.Millisecond, "serial_read never observed %q", want)
		return text
	}

	t.Run("serial_start_then_status_running", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()

		res, _, err := h.callTool(ctx, "serial_start", map[string]any{
			"port": h.port,
		})
		require.NoError(t, err, "serial_start")
		require.False(t, res.IsError, "serial_start returned error: %s", resultText(res))

		res, _, err = h.callTool(ctx, "serial_status", map[string]any{
			"port": h.port,
		})
		require.NoError(t, err, "serial_status")
		require.False(t, res.IsError, "serial_status returned error: %s", resultText(res))

		var status struct {
			Running bool `json:"running"`
		}
		require.NoError(t, json.Unmarshal([]byte(resultText(res)), &status), "parse serial_status result: %s", resultText(res))
		assert.True(t, status.Running, "serial_status reports not running after serial_start")
	})

	t.Run("serial_read_returns_boot_banner", func(t *testing.T) {
		serialReadTextContaining(t, "mock-esp32: virtual chip ready")
	})

	t.Run("serial_write_then_read_loopback", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()

		res, _, err := h.callTool(ctx, "serial_write", map[string]any{
			"port": h.port,
			"data": "PING-hwbench-test",
		})
		require.NoError(t, err, "serial_write")
		require.False(t, res.IsError, "serial_write returned error: %s", resultText(res))

		serialReadTextContaining(t, "PING-hwbench-test")
	})

	t.Run("serial_stop_then_status_reports_not_open", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()

		res, _, err := h.callTool(ctx, "serial_stop", map[string]any{
			"port": h.port,
		})
		require.NoError(t, err, "serial_stop")
		require.False(t, res.IsError, "serial_stop returned error: %s", resultText(res))

		// serial_stop tears the port session down entirely (session.StopSession
		// removes it from the ports map), so a subsequent serial_status by
		// name errors rather than reporting running=false -- there's no
		// session left to report status for.
		res, _, err = h.callTool(ctx, "serial_status", map[string]any{
			"port": h.port,
		})
		require.NoError(t, err, "serial_status")
		assert.True(t, res.IsError, "serial_status expected an error after serial_stop, got success: %s", resultText(res))
		assert.Contains(t, resultText(res), "no serial port open",
			"serial_status error does not mention the session is gone: %s", resultText(res))
	})
}
