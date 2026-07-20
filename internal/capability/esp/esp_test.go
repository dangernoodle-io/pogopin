package esp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"

	"github.com/dangernoodle-io/shesha/testkit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	espflasher "tinygo.org/x/espflasher/pkg/espflasher"
	"tinygo.org/x/espflasher/pkg/nvs"

	"dangernoodle.io/pogopin/internal/esp"
	"dangernoodle.io/pogopin/internal/serial"
	"dangernoodle.io/pogopin/internal/session"
	"dangernoodle.io/pogopin/internal/testutil"
)

// setFlasher swaps in mock as the flasher factory for the duration of the
// test.
func setFlasher(t *testing.T, mock esp.Flasher) {
	t.Helper()
	orig := session.SetFlasherFactory(func(port string, opts *espflasher.FlasherOptions) (esp.Flasher, error) {
		return mock, nil
	})
	t.Cleanup(func() { session.SetFlasherFactory(orig) })
}

// setFlasherErr swaps in a factory that always fails with err.
func setFlasherErr(t *testing.T, err error) {
	t.Helper()
	orig := session.SetFlasherFactory(func(port string, opts *espflasher.FlasherOptions) (esp.Flasher, error) {
		return nil, err
	})
	t.Cleanup(func() { session.SetFlasherFactory(orig) })
}

func TestHandleFlashSuccess(t *testing.T) {
	testutil.SetupTestPorts(t)
	testutil.SetupTestFlasherFactory(t)

	tmpDir := t.TempDir()
	fw := tmpDir + "/firmware.bin"
	require.NoError(t, os.WriteFile(fw, []byte("firmware data"), 0o644))

	mock := &testutil.MockFlasher{}
	setFlasher(t, mock)

	h := newHarness(t)
	result, err := h.CallTool(context.Background(), "esp_flash", map[string]any{
		"port": "/dev/ttyUSB0",
		"images": []any{
			map[string]any{"path": fw, "offset": float64(0x1000)},
		},
		"baud": float64(115200),
	})
	require.NoError(t, err)
	require.False(t, result.IsError)
	assert.Contains(t, testkit.ResultText(result), "bytes_written")
	assert.True(t, mock.FlashImagesCalled)
}

func TestHandleFlashPortManaged(t *testing.T) {
	testutil.SetupTestPorts(t)
	testutil.SetupTestFlasherFactory(t)
	testutil.SetupTestListPorts(t)
	testutil.SetupFastWaitForPort(t)

	testMgr := serial.NewManager()
	testMgr.SetTestState(true, "/dev/ttyUSB0", 115200, nil)
	session.InsertPort("/dev/ttyUSB0", session.NewPortSession(testMgr, "/dev/ttyUSB0", 115200, session.ModeReader))

	h := newHarness(t)
	result, err := h.CallTool(context.Background(), "esp_flash", map[string]any{
		"port": "/dev/ttyUSB0",
		"images": []any{
			map[string]any{"path": "/tmp/fw.bin", "offset": float64(0)},
		},
	})
	require.NoError(t, err)
	if result.IsError {
		assert.NotContains(t, testkit.ResultText(result), "managed by serial_start")
	}
}

func TestHandleEraseSuccess(t *testing.T) {
	testutil.SetupTestPorts(t)
	testutil.SetupTestFlasherFactory(t)

	mock := &testutil.MockFlasher{}
	setFlasher(t, mock)

	h := newHarness(t)
	result, err := h.CallTool(context.Background(), "esp_erase", map[string]any{
		"port": "/dev/ttyUSB0", "baud": float64(115200),
	})
	require.NoError(t, err)
	require.False(t, result.IsError)
	assert.Contains(t, testkit.ResultText(result), "success")
	assert.True(t, mock.EraseFlashCalled)
}

func TestHandleEraseRegionSuccess(t *testing.T) {
	testutil.SetupTestPorts(t)
	testutil.SetupTestFlasherFactory(t)

	mock := &testutil.MockFlasher{}
	setFlasher(t, mock)

	h := newHarness(t)
	result, err := h.CallTool(context.Background(), "esp_erase", map[string]any{
		"port": "/dev/ttyUSB0", "offset": float64(0x1000), "size": float64(0x1000),
	})
	require.NoError(t, err)
	require.False(t, result.IsError)
	assert.True(t, mock.EraseRegionCalled)
	assert.Equal(t, uint32(0x1000), mock.EraseRegionOffset)
	assert.Equal(t, uint32(0x1000), mock.EraseRegionSize)
}

// TestHandleEraseRegionMissingSizeIsSemanticError pins the SEMANTIC error
// (not a schema rejection: offset/size are both optional in the schema, but
// providing offset without size is a handler-level validation the schema
// can't express) when offset is given without size.
func TestHandleEraseRegionMissingSizeIsSemanticError(t *testing.T) {
	testutil.SetupTestPorts(t)
	testutil.SetupTestFlasherFactory(t)

	h := newHarness(t)
	result, err := h.CallTool(context.Background(), "esp_erase", map[string]any{
		"port": "/dev/ttyUSB0", "offset": float64(0x1000),
	})
	require.NoError(t, err)
	require.True(t, result.IsError)
	assert.Contains(t, testkit.ResultText(result), "size")
}

func TestHandleESPInfoChipSuccess(t *testing.T) {
	testutil.SetupTestPorts(t)
	testutil.SetupTestFlasherFactory(t)

	mock := &testutil.MockFlasher{
		ChipNameVal: "ESP32-S3",
		FlashIDMfg:  0x20,
		FlashIDDev:  0x0060,
	}
	setFlasher(t, mock)

	h := newHarness(t)
	result, err := h.CallTool(context.Background(), "esp_info", map[string]any{
		"port": "/dev/ttyUSB0", "baud": float64(115200),
	})
	require.NoError(t, err)
	require.False(t, result.IsError)

	var info map[string]any
	require.NoError(t, json.Unmarshal([]byte(testkit.ResultText(result)), &info))
	chipData, ok := info["chip"].(map[string]any)
	require.True(t, ok, "chip section not found in response")
	assert.Equal(t, "ESP32-S3", chipData["chip_name"])
	assert.Equal(t, float64(0x20), chipData["manufacturer_id"])
	assert.Equal(t, float64(0x0060), chipData["device_id"])
}

func TestHandleESPInfoSecuritySuccess(t *testing.T) {
	testutil.SetupTestPorts(t)
	testutil.SetupTestFlasherFactory(t)

	mock := &testutil.MockFlasher{ChipTypeVal: espflasher.ChipESP32S3, GetSecurityInfoVal: &espflasher.SecurityInfo{}}
	setFlasher(t, mock)

	h := newHarness(t)
	result, err := h.CallTool(context.Background(), "esp_info", map[string]any{
		"port": "/dev/ttyUSB0", "include": "security",
	})
	require.NoError(t, err)
	require.False(t, result.IsError)

	var info map[string]any
	require.NoError(t, json.Unmarshal([]byte(testkit.ResultText(result)), &info))
	_, ok := info["security"]
	assert.True(t, ok, "security section not found in response")
	_, ok = info["chip"]
	assert.False(t, ok, "chip section must be absent when include=security only")
}

// TestHandleESPInfoSecurityClassicESP32Unsupported verifies the esp_info
// security path surfaces GetSecurityInfo's clear classic-ESP32 error
// end-to-end rather than the flasher's generic command-failure text.
func TestHandleESPInfoSecurityClassicESP32Unsupported(t *testing.T) {
	testutil.SetupTestPorts(t)
	testutil.SetupTestFlasherFactory(t)

	mock := &testutil.MockFlasher{
		ChipTypeVal: espflasher.ChipESP32,
		ChipNameVal: "ESP32",
	}
	setFlasher(t, mock)

	h := newHarness(t)
	result, err := h.CallTool(context.Background(), "esp_info", map[string]any{
		"port": "/dev/ttyUSB0", "include": "security",
	})
	require.NoError(t, err)
	require.True(t, result.IsError)
	assert.Contains(t, testkit.ResultText(result), "not supported on ESP32")
}

// TestHandleESPInfoSecurityESP8266Unsupported verifies the esp_info security
// path surfaces GetSecurityInfo's clear ESP8266 error end-to-end — ESP8266
// predates GET_SECURITY_INFO even more than the original ESP32, and
// espflasher has no special handling for it either.
func TestHandleESPInfoSecurityESP8266Unsupported(t *testing.T) {
	testutil.SetupTestPorts(t)
	testutil.SetupTestFlasherFactory(t)

	mock := &testutil.MockFlasher{
		ChipTypeVal: espflasher.ChipESP8266,
		ChipNameVal: "ESP8266",
	}
	setFlasher(t, mock)

	h := newHarness(t)
	result, err := h.CallTool(context.Background(), "esp_info", map[string]any{
		"port": "/dev/ttyUSB0", "include": "security",
	})
	require.NoError(t, err)
	require.True(t, result.IsError)
	assert.Contains(t, testkit.ResultText(result), "not supported on ESP8266")
}

func TestHandleESPInfoError(t *testing.T) {
	testutil.SetupTestPorts(t)
	testutil.SetupTestFlasherFactory(t)
	setFlasherErr(t, fmt.Errorf("connection failed"))

	h := newHarness(t)
	result, err := h.CallTool(context.Background(), "esp_info", map[string]any{"port": "/dev/ttyUSB0"})
	require.NoError(t, err)
	require.True(t, result.IsError)
	assert.Contains(t, testkit.ResultText(result), "connection failed")
}

func TestHandleRegisterReadSuccess(t *testing.T) {
	testutil.SetupTestPorts(t)
	testutil.SetupTestFlasherFactory(t)

	mock := &testutil.MockFlasher{ReadRegisterVal: 0xDEADBEEF}
	setFlasher(t, mock)

	h := newHarness(t)
	result, err := h.CallTool(context.Background(), "esp_register", map[string]any{
		"port": "/dev/ttyUSB0", "address": float64(0x3FF00000),
	})
	require.NoError(t, err)
	require.False(t, result.IsError)
	assert.Contains(t, testkit.ResultText(result), "0xDEADBEEF")
}

func TestHandleRegisterWriteSuccess(t *testing.T) {
	testutil.SetupTestPorts(t)
	testutil.SetupTestFlasherFactory(t)

	mock := &testutil.MockFlasher{}
	setFlasher(t, mock)

	h := newHarness(t)
	result, err := h.CallTool(context.Background(), "esp_register", map[string]any{
		"port": "/dev/ttyUSB0", "address": float64(0x3FF00000), "value": float64(0xABCD1234),
	})
	require.NoError(t, err)
	require.False(t, result.IsError)
	assert.Equal(t, uint32(0x3FF00000), mock.WriteRegisterAddr)
	assert.Equal(t, uint32(0xABCD1234), mock.WriteRegisterVal)
	assert.Contains(t, testkit.ResultText(result), "0xABCD1234")
}

// TestHandleRegisterExplicitNullValueIsWriteMode restores parity with the
// pre-migration mcp-go handler (MC-12 review): an explicit `"value": null`
// must take the write branch (and error, since null isn't a number) rather
// than being indistinguishable from an omitted "value" (read mode).
func TestHandleRegisterExplicitNullValueIsWriteMode(t *testing.T) {
	testutil.SetupTestPorts(t)
	testutil.SetupTestFlasherFactory(t)
	mock := &testutil.MockFlasher{ReadRegisterVal: 0xDEADBEEF}
	setFlasher(t, mock)

	h := newHarness(t)
	result, err := h.CallTool(context.Background(), "esp_register", map[string]any{
		"port": "/dev/ttyUSB0", "address": float64(0x3FF00000), "value": nil,
	})
	require.NoError(t, err)
	require.True(t, result.IsError)
	assert.Contains(t, testkit.ResultText(result), "value must be a number")
	assert.NotContains(t, testkit.ResultText(result), "0xDEADBEEF", "must not silently fall through to read mode")
}

func TestHandleRegisterReadError(t *testing.T) {
	testutil.SetupTestPorts(t)
	testutil.SetupTestFlasherFactory(t)
	setFlasher(t, &testutil.MockFlasher{ReadRegisterErr: fmt.Errorf("read timeout")})

	h := newHarness(t)
	result, err := h.CallTool(context.Background(), "esp_register", map[string]any{
		"port": "/dev/ttyUSB0", "address": float64(0x3FF00000),
	})
	require.NoError(t, err)
	require.True(t, result.IsError)
	assert.Contains(t, testkit.ResultText(result), "read timeout")
}

func TestHandleResetSuccess(t *testing.T) {
	testutil.SetupTestPorts(t)
	testutil.SetupTestFlasherFactory(t)
	setFlasher(t, &testutil.MockFlasher{})

	h := newHarness(t)
	result, err := h.CallTool(context.Background(), "esp_reset", map[string]any{"port": "/dev/ttyUSB0"})
	require.NoError(t, err)
	require.False(t, result.IsError)
	text := testkit.ResultText(result)
	assert.Contains(t, text, "success")
	assert.Contains(t, text, "device reset")
}

func TestHandleESPReadFlashSuccess(t *testing.T) {
	testutil.SetupTestPorts(t)
	testutil.SetupTestFlasherFactory(t)
	setFlasher(t, &testutil.MockFlasher{ReadFlashVal: []byte("hello flash")})

	h := newHarness(t)
	result, err := h.CallTool(context.Background(), "esp_read_flash", map[string]any{
		"port": "/dev/ttyUSB0", "offset": float64(0x1000), "size": float64(11),
	})
	require.NoError(t, err)
	require.False(t, result.IsError)
	assert.Contains(t, testkit.ResultText(result), "\"offset\"")
}

func TestHandleESPReadFlashMD5Success(t *testing.T) {
	testutil.SetupTestPorts(t)
	testutil.SetupTestFlasherFactory(t)
	setFlasher(t, &testutil.MockFlasher{FlashMD5Val: "5d41402abc4b2a76b9719d911017c592"})

	h := newHarness(t)
	result, err := h.CallTool(context.Background(), "esp_read_flash", map[string]any{
		"port": "/dev/ttyUSB0", "offset": float64(0), "size": float64(0x1000), "md5": true,
	})
	require.NoError(t, err)
	require.False(t, result.IsError)
	assert.Contains(t, testkit.ResultText(result), "5d41402abc4b2a76b9719d911017c592")
}

func TestHandleReadNVSSuccess(t *testing.T) {
	testutil.SetupTestPorts(t)
	testutil.SetupTestFlasherFactory(t)

	data, err := nvs.GenerateNVS([]nvs.Entry{
		{Namespace: "test", Key: "k1", Type: "u32", Value: uint32(42)},
	}, nvs.DefaultPartSize)
	require.NoError(t, err)
	setFlasher(t, &testutil.MockFlasher{ReadFlashVal: data})

	h := newHarness(t)
	result, callErr := h.CallTool(context.Background(), "esp_read_nvs", map[string]any{"port": "/dev/ttyUSB0"})
	require.NoError(t, callErr)
	require.False(t, result.IsError)
	assert.Contains(t, testkit.ResultText(result), "k1")
}

// TestAllHardwareToolsSurfaceSyncError pins handleSyncError's propagation
// through every esp_* handler that acquires a flasher via
// session.AcquireForFlasher: a *espflasher.SyncError from the flasher
// factory must surface as the "not in download mode" formatted message
// rather than the raw error, for every tool -- not just esp_info (already
// covered by internal/mcpapp's smoke test).
func TestAllHardwareToolsSurfaceSyncError(t *testing.T) {
	cases := []struct {
		tool string
		args map[string]any
	}{
		{"esp_flash", map[string]any{"port": "/dev/ttyUSB0", "images": []any{map[string]any{"path": "/tmp/x", "offset": float64(0)}}}},
		{"esp_erase", map[string]any{"port": "/dev/ttyUSB0"}},
		{"esp_info", map[string]any{"port": "/dev/ttyUSB0"}},
		{"esp_register", map[string]any{"port": "/dev/ttyUSB0", "address": float64(0)}},
		{"esp_reset", map[string]any{"port": "/dev/ttyUSB0"}},
		{"esp_read_flash", map[string]any{"port": "/dev/ttyUSB0", "offset": float64(0), "size": float64(16)}},
		{"esp_read_flash", map[string]any{"port": "/dev/ttyUSB0", "offset": float64(0), "size": float64(16), "md5": true}},
		// esp_read_nvs is deliberately excluded: esp.ReadNVS wraps
		// ReadFlashData's error via fmt.Errorf("read flash: %w", err)
		// (internal/esp/esp.go), so handleSyncError's plain type assertion
		// never matches there -- pre-existing behavior carried over
		// unchanged from the mark3labs-based handler (which had the same
		// gap and no test for it either).
		{"esp_gpio_read", map[string]any{"port": "/dev/ttyUSB0", "pin": float64(4)}},
		{"esp_gpio_set", map[string]any{"port": "/dev/ttyUSB0", "pin": float64(4), "level": true}},
		{"esp_gpio_sweep", map[string]any{"port": "/dev/ttyUSB0", "pins": "4"}},
	}

	for _, tc := range cases {
		t.Run(tc.tool, func(t *testing.T) {
			testutil.SetupTestPorts(t)
			testutil.SetupTestFlasherFactory(t)
			testutil.SetupTestListPorts(t)
			testutil.SetupFastWaitForPort(t)
			setFlasherErr(t, &espflasher.SyncError{Attempts: 7})

			h := newHarness(t)
			result, err := h.CallTool(context.Background(), tc.tool, tc.args)
			require.NoError(t, err)
			require.True(t, result.IsError)
			assert.Contains(t, testkit.ResultText(result), "not in download mode")
			assert.Contains(t, testkit.ResultText(result), "7 attempts")
		})
	}
}

// NVS RMW handler tests (BR-53): handleNVSSet / handleNVSDelete are
// read-modify-write and must never report success without a verified
// post-write re-read. These cover the success path plus the two abort
// paths (pre-write lossy-parse guard, post-write verify failure).

func TestHandleNVSSetSuccess(t *testing.T) {
	testutil.SetupTestPorts(t)
	testutil.SetupTestFlasherFactory(t)

	existingData, err := nvs.GenerateNVS(nil, nvs.DefaultPartSize)
	require.NoError(t, err)
	mock := &testutil.MockFlasher{ReadFlashVal: existingData}
	setFlasher(t, mock)

	h := newHarness(t)
	result, callErr := h.CallTool(context.Background(), "esp_nvs_set", map[string]any{
		"port": "/dev/ttyUSB0",
		"entries": []any{
			map[string]any{"namespace": "test", "key": "k", "type": "u8", "value": "42"},
		},
	})
	require.NoError(t, callErr)
	require.False(t, result.IsError)

	var resp map[string]any
	require.NoError(t, json.Unmarshal([]byte(testkit.ResultText(result)), &resp))
	assert.Equal(t, "success", resp["status"])
	assert.Equal(t, float64(1), resp["updated"])
	assert.True(t, mock.FlashImagesCalled)
}

func TestHandleNVSSetAbortsOnLossyParse(t *testing.T) {
	testutil.SetupTestPorts(t)
	testutil.SetupTestFlasherFactory(t)

	entries := []nvs.Entry{{Namespace: "test", Key: "k1", Type: "u32", Value: uint32(42)}}
	data, err := nvs.GenerateNVS(entries, nvs.DefaultPartSize)
	require.NoError(t, err)

	// Flip an otherwise-empty slot's bitmap state to Written without a real
	// entry there, so nvs.ParseNVS silently drops it as orphaned -- the
	// pre-write completeness guard must catch this and refuse to write.
	tampered := append([]byte(nil), data...)
	page := tampered[0:nvs.PageSize]
	const slot = 100
	bitIndex := uint(slot) * 2
	byteIdx := nvs.HeaderSize + int(bitIndex/8)
	bitOffset := bitIndex % 8
	page[byteIdx] &^= 1 << bitOffset

	mock := &testutil.MockFlasher{ReadFlashVal: tampered}
	setFlasher(t, mock)

	h := newHarness(t)
	result, callErr := h.CallTool(context.Background(), "esp_nvs_set", map[string]any{
		"port": "/dev/ttyUSB0",
		"entries": []any{
			map[string]any{"namespace": "test", "key": "k2", "type": "u8", "value": "1"},
		},
	})
	require.NoError(t, callErr)
	require.True(t, result.IsError)
	assert.Contains(t, testkit.ResultText(result), "lossy")
	assert.False(t, mock.FlashImagesCalled, "must not flash when the pre-write parse is lossy")
}

func TestHandleNVSSetAbortsOnPostWriteVerifyFailure(t *testing.T) {
	testutil.SetupTestPorts(t)
	testutil.SetupTestFlasherFactory(t)

	existingData, err := nvs.GenerateNVS([]nvs.Entry{
		{Namespace: "test", Key: "existing", Type: "u8", Value: uint8(1)},
	}, nvs.DefaultPartSize)
	require.NoError(t, err)

	// Device comes back after the write missing the pre-existing key.
	postWriteData, err := nvs.GenerateNVS([]nvs.Entry{
		{Namespace: "test", Key: "new_key", Type: "u8", Value: uint8(1)},
	}, nvs.DefaultPartSize)
	require.NoError(t, err)

	mock := &testutil.MockFlasher{ReadFlashVal: existingData, ReadFlashPostWriteOverride: postWriteData}
	setFlasher(t, mock)

	h := newHarness(t)
	result, callErr := h.CallTool(context.Background(), "esp_nvs_set", map[string]any{
		"port": "/dev/ttyUSB0",
		"entries": []any{
			map[string]any{"namespace": "test", "key": "new_key", "type": "u8", "value": "1"},
		},
	})
	require.NoError(t, callErr)
	require.True(t, result.IsError)
	text := testkit.ResultText(result)
	assert.Contains(t, text, "post-write verify")
	assert.Contains(t, text, "existing")
	assert.True(t, mock.FlashImagesCalled)
}

func TestHandleNVSDeleteSuccess(t *testing.T) {
	testutil.SetupTestPorts(t)
	testutil.SetupTestFlasherFactory(t)

	existingData, err := nvs.GenerateNVS([]nvs.Entry{
		{Namespace: "test", Key: "key1", Type: "u32", Value: uint32(42)},
		{Namespace: "test", Key: "key2", Type: "string", Value: "hello"},
	}, nvs.DefaultPartSize)
	require.NoError(t, err)
	mock := &testutil.MockFlasher{ReadFlashVal: existingData}
	setFlasher(t, mock)

	h := newHarness(t)
	result, callErr := h.CallTool(context.Background(), "esp_nvs_delete", map[string]any{
		"port": "/dev/ttyUSB0", "namespace": "test", "key": "key1",
	})
	require.NoError(t, callErr)
	require.False(t, result.IsError)

	var resp map[string]any
	require.NoError(t, json.Unmarshal([]byte(testkit.ResultText(result)), &resp))
	assert.Equal(t, "success", resp["status"])
	assert.Equal(t, float64(1), resp["deleted"])
	assert.True(t, mock.FlashImagesCalled)
}

func TestHandleNVSDeleteAbortsOnPostWriteVerifyFailure(t *testing.T) {
	testutil.SetupTestPorts(t)
	testutil.SetupTestFlasherFactory(t)

	existingData, err := nvs.GenerateNVS([]nvs.Entry{
		{Namespace: "test", Key: "key1", Type: "u32", Value: uint32(42)},
		{Namespace: "test", Key: "key2", Type: "string", Value: "hello"},
	}, nvs.DefaultPartSize)
	require.NoError(t, err)

	// Device comes back empty after deleting key1 -- key2 was also lost.
	postWriteData, err := nvs.GenerateNVS(nil, nvs.DefaultPartSize)
	require.NoError(t, err)

	mock := &testutil.MockFlasher{ReadFlashVal: existingData, ReadFlashPostWriteOverride: postWriteData}
	setFlasher(t, mock)

	h := newHarness(t)
	result, callErr := h.CallTool(context.Background(), "esp_nvs_delete", map[string]any{
		"port": "/dev/ttyUSB0", "namespace": "test", "key": "key1",
	})
	require.NoError(t, callErr)
	require.True(t, result.IsError)
	text := testkit.ResultText(result)
	assert.Contains(t, text, "post-write verify")
	assert.Contains(t, text, "key2")
	assert.True(t, mock.FlashImagesCalled)
}

// TestHandleSyncErrorHelper pins handleSyncError's own three branches
// directly (SyncError / other error / nil), mirroring
// internal/mcpserver/esp_handlers_test.go's TestHandleSyncError.
func TestHandleSyncErrorHelper(t *testing.T) {
	result := handleSyncError(&espflasher.SyncError{Attempts: 10})
	require.NotNil(t, result)
	require.True(t, result.IsError)
	assert.Contains(t, testkit.ResultText(result), "10 attempts")

	assert.Nil(t, handleSyncError(fmt.Errorf("some other error")))
	assert.Nil(t, handleSyncError(nil))
}

// handleWriteNVS tests (esp_write_nvs): DESTRUCTIVE full-partition replace,
// unguarded by design (no pre/post verification like handleNVSSet/Delete).
// Covers the entry-parsing loop (success + per-type error), the
// esp.WriteNVS call, sync-error remap, and the success-result marshal.

func TestHandleWriteNVSSuccess(t *testing.T) {
	testutil.SetupTestPorts(t)
	testutil.SetupTestFlasherFactory(t)

	mock := &testutil.MockFlasher{}
	setFlasher(t, mock)

	h := newHarness(t)
	result, callErr := h.CallTool(context.Background(), "esp_write_nvs", map[string]any{
		"port": "/dev/ttyUSB0",
		"entries": []any{
			map[string]any{"namespace": "test", "key": "k1", "type": "u32", "value": float64(42)},
			map[string]any{"namespace": "test", "key": "k2", "type": "string", "value": "hello"},
		},
	})
	require.NoError(t, callErr)
	require.False(t, result.IsError)

	var resp map[string]any
	require.NoError(t, json.Unmarshal([]byte(testkit.ResultText(result)), &resp))
	assert.Equal(t, "success", resp["status"])
	assert.True(t, mock.FlashImagesCalled)
}

func TestHandleWriteNVSEntryParseError(t *testing.T) {
	testutil.SetupTestPorts(t)
	testutil.SetupTestFlasherFactory(t)

	mock := &testutil.MockFlasher{}
	setFlasher(t, mock)

	h := newHarness(t)
	result, callErr := h.CallTool(context.Background(), "esp_write_nvs", map[string]any{
		"port": "/dev/ttyUSB0",
		"entries": []any{
			// u32 requires a JSON number; a string value is a parse error.
			map[string]any{"namespace": "test", "key": "k1", "type": "u32", "value": "not-a-number"},
		},
	})
	require.NoError(t, callErr)
	require.True(t, result.IsError)
	assert.Contains(t, testkit.ResultText(result), "u32 value must be a number")
	assert.False(t, mock.FlashImagesCalled, "must not flash when entry parsing fails")
}

func TestHandleWriteNVSSurfacesSyncError(t *testing.T) {
	testutil.SetupTestPorts(t)
	testutil.SetupTestFlasherFactory(t)
	setFlasherErr(t, &espflasher.SyncError{Attempts: 3})

	h := newHarness(t)
	result, callErr := h.CallTool(context.Background(), "esp_write_nvs", map[string]any{
		"port": "/dev/ttyUSB0",
		"entries": []any{
			map[string]any{"namespace": "test", "key": "k1", "type": "u32", "value": float64(1)},
		},
	})
	require.NoError(t, callErr)
	require.True(t, result.IsError)
	assert.Contains(t, testkit.ResultText(result), "not in download mode")
	assert.Contains(t, testkit.ResultText(result), "3 attempts")
}
