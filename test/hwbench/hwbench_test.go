//go:build hwtest

// Package hwbench drives the pogopin MCP server over its real stdio wire
// protocol against a physical ESP board. It is the committed form of the
// scratchpad JS driver that HW-validated the esp_gpio_* tools (10/10 on an
// ESP32-S2). Skipped entirely unless POGOPIN_HW_PORT is set, so `go test
// -tags hwtest ./...` without hardware attached is a clean skip and
// `go test ./...` (no tag) never compiles this file at all.
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

	"dangernoodle.io/pogopin/internal/session"
)

// boardProfile describes the fixed hardware characteristics of one bench
// board: which GPIO drives a simple visual LED (if any), whether the chip
// enumerates over native USB (which forces reset_mode=no_reset — a
// reset-based connect hangs/desyncs the ROM on these chips), and what kind
// of LED sits on LEDPin (a plain GPIO LED can be driven with esp_gpio_set;
// an APA102 needs a real SPI/bit-bang driver that esp_gpio_set can't
// provide, so LED-visual scenarios skip on those boards).
type boardProfile struct {
	Name      string
	LEDPin    int
	NativeUSB bool
	LEDType   string // "gpio", "apa102", "rgb"
}

// boardProfiles is keyed by the POGOPIN_HW_BOARD value used to select the
// active profile.
var boardProfiles = []struct {
	Key     string
	Profile boardProfile
}{
	{"s2", boardProfile{Name: "S2 Mini", LEDPin: 15, NativeUSB: true, LEDType: "gpio"}},
	// C3 Mini's onboard LED pin varies by vendor board revision; 15 is a
	// commonly-safe drivable GPIO but is NOT verified against a specific
	// C3 Mini SKU. Override with POGOPIN_HW_LED_PIN if it doesn't drive an
	// LED on your board.
	{"c3", boardProfile{Name: "C3 Mini", LEDPin: 15, NativeUSB: true, LEDType: "gpio"}},
	// S3 T-Dongle's status LED is an APA102 (clock+data, bit-banged SPI
	// protocol) — not a plain GPIO the ROM bootloader can toggle with a
	// single register write, so LEDPin is not drivable via esp_gpio_set.
	// LED-visual scenarios skip on this profile.
	{"s3dongle", boardProfile{Name: "S3 T-Dongle", LEDPin: 0, NativeUSB: true, LEDType: "apa102"}},
	// CYD (ESP32/CH340) has a 3-pin RGB LED (R=22, G=16, B=17, per the
	// prompt's pin assignment); LEDPin defaults to the red channel (22).
	// It's still a plain GPIO from the ROM's perspective, so esp_gpio_set
	// drives it fine — only the visual result (one LED channel, not the
	// vendor's usual "RGB" impression) differs from a single-color LED.
	{"cyd", boardProfile{Name: "CYD", LEDPin: 22, NativeUSB: false, LEDType: "rgb"}},
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

func newHarness(t *testing.T, port string, profile boardProfile) *harness {
	bin := resolveBinary(t)

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

// resolveBinary honors POGOPIN_HW_BIN if set, else go builds the server
// into a temp path from the repo root (two levels up from this test file:
// test/hwbench -> test -> repo root).
func resolveBinary(t *testing.T) string {
	if bin := os.Getenv("POGOPIN_HW_BIN"); bin != "" {
		return bin
	}

	_, thisFile, _, ok := runtime.Caller(0)
	require.True(t, ok, "resolve repo root: runtime.Caller failed")
	repoRoot := filepath.Dir(filepath.Dir(filepath.Dir(thisFile)))

	bin := filepath.Join(t.TempDir(), "pogo-hwbench")
	cmd := exec.Command("go", "build", "-o", bin, ".")
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

func TestHWBench(t *testing.T) {
	port := os.Getenv("POGOPIN_HW_PORT")
	if port == "" {
		t.Skip("POGOPIN_HW_PORT not set — skipping hardware-integration bench")
	}

	boardKey := os.Getenv("POGOPIN_HW_BOARD")
	if boardKey == "" {
		boardKey = "s2"
	}
	profile, ok := lookupProfile(boardKey)
	require.True(t, ok, "unknown POGOPIN_HW_BOARD %q", boardKey)
	if pinOverride := os.Getenv("POGOPIN_HW_LED_PIN"); pinOverride != "" {
		v, err := strconv.Atoi(pinOverride)
		require.NoError(t, err, "invalid POGOPIN_HW_LED_PIN %q", pinOverride)
		profile.LEDPin = v
	}

	h := newHarness(t, port, profile)

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

		// GPIO0 is a strapping pin on every supported chip family and is
		// refused by default without include_reserved.
		res, _, err := h.callTool(ctx, "esp_gpio_set", gpioArgs(h.port, map[string]any{
			"pin":   0,
			"level": true,
		}))
		require.NoError(t, err, "esp_gpio_set(pin=0)")
		require.True(t, res.IsError, "esp_gpio_set(pin=0) expected a reserved-pin refusal, got success: %s", resultText(res))
		assert.Contains(t, strings.ToLower(resultText(res)), "reserved",
			"esp_gpio_set(pin=0) error does not mention 'reserved': %s", resultText(res))
	})

	t.Run("gpio_sweep_skips_reserved_and_emits_progress", func(t *testing.T) {
		if h.profile.LEDType == "apa102" {
			t.Skip("no plain GPIO LED to probe on this board (apa102)")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		pins := fmt.Sprintf("%d,0", h.profile.LEDPin) // one valid pin + GPIO0 (reserved)
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
			case 0:
				sawReserved = true
				assert.True(t, p.Skipped, "reserved pin 0 was not skipped")
			}
		}
		assert.True(t, sawLED, "sweep result missing LED pin %d", h.profile.LEDPin)
		assert.True(t, sawReserved, "sweep result missing reserved pin 0")

		assert.GreaterOrEqual(t, len(h.progressFor(tok)), 1, "esp_gpio_sweep emitted no notifications/progress")
	})
}
