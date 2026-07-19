package esp

import (
	"testing"
	"time"

	"github.com/dangernoodle-io/shesha/mcpx"
	"github.com/dangernoodle-io/shesha/testkit"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"dangernoodle.io/pogopin/internal/testutil"
)

// newHarness composes a minimal shesha App around Capability{} alone,
// mirroring internal/capability/serial's package-local helper of the same
// name.
func newHarness(t *testing.T) *testkit.Harness {
	t.Helper()
	return testutil.NewHarness(t, Capability{})
}

// setupFastBootCapture sets up a no-op boot capture wait function, mirroring
// internal/mcpserver/helpers_test.go's fixture of the same name (this
// package's bootCaptureWait var is unexported, so this helper must live
// here rather than in internal/testutil).
func setupFastBootCapture(t *testing.T) {
	t.Helper()
	orig := bootCaptureWait
	bootCaptureWait = func(d time.Duration) {}
	t.Cleanup(func() { bootCaptureWait = orig })
}

// TestParseNVSValue ports internal/mcpserver/nvs_test.go's coverage of the
// JSON-typed NVS value parser (esp_write_nvs's Value any field).
func TestParseNVSValue(t *testing.T) {
	val, err := parseNVSValue("u8", float64(255))
	require.NoError(t, err)
	assert.Equal(t, uint8(255), val)

	val, err = parseNVSValue("u16", float64(65535))
	require.NoError(t, err)
	assert.Equal(t, uint16(65535), val)

	val, err = parseNVSValue("u32", float64(0xDEADBEEF))
	require.NoError(t, err)
	assert.Equal(t, uint32(0xDEADBEEF), val)

	val, err = parseNVSValue("i8", float64(-128))
	require.NoError(t, err)
	assert.Equal(t, int8(-128), val)

	val, err = parseNVSValue("i16", float64(-32768))
	require.NoError(t, err)
	assert.Equal(t, int16(-32768), val)

	val, err = parseNVSValue("i32", float64(-2147483648))
	require.NoError(t, err)
	assert.Equal(t, int32(-2147483648), val)

	val, err = parseNVSValue("string", "test-value")
	require.NoError(t, err)
	assert.Equal(t, "test-value", val)

	_, err = parseNVSValue("string", float64(42))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "must be a string")

	_, err = parseNVSValue("invalid", float64(42))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported NVS type")

	_, err = parseNVSValue("u8", "not-a-number")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "must be a number")
}

// TestParseNVSValueFromString ports internal/mcpserver/nvs_test.go's
// coverage of the string-typed NVS value parser (esp_nvs_set's Value string
// field).
func TestParseNVSValueFromString(t *testing.T) {
	val, err := parseNVSValueFromString("u8", "255")
	require.NoError(t, err)
	assert.Equal(t, uint8(255), val)

	val, err = parseNVSValueFromString("u16", "65535")
	require.NoError(t, err)
	assert.Equal(t, uint16(65535), val)

	val, err = parseNVSValueFromString("u32", "4294967295")
	require.NoError(t, err)
	assert.Equal(t, uint32(4294967295), val)

	val, err = parseNVSValueFromString("i8", "-128")
	require.NoError(t, err)
	assert.Equal(t, int8(-128), val)

	val, err = parseNVSValueFromString("i16", "-32768")
	require.NoError(t, err)
	assert.Equal(t, int16(-32768), val)

	val, err = parseNVSValueFromString("i32", "-2147483648")
	require.NoError(t, err)
	assert.Equal(t, int32(-2147483648), val)

	val, err = parseNVSValueFromString("string", "test-value")
	require.NoError(t, err)
	assert.Equal(t, "test-value", val)

	_, err = parseNVSValueFromString("invalid", "42")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported NVS type")

	_, err = parseNVSValueFromString("u8", "256")
	require.Error(t, err)

	_, err = parseNVSValueFromString("i32", "invalid")
	require.Error(t, err)
}

// TestHasArgKey covers hasArgKey's fail-closed edge cases (nil request, nil
// Params, empty Arguments, malformed JSON) alongside its present/absent
// happy paths — only the "value present" branch is exercised indirectly via
// esp_register's handler tests.
func TestHasArgKey(t *testing.T) {
	assert.False(t, hasArgKey(nil, "value"))
	assert.False(t, hasArgKey(&mcpx.CallToolRequest{}, "value"))
	assert.False(t, hasArgKey(&mcpx.CallToolRequest{Params: &mcp.CallToolParamsRaw{}}, "value"))
	assert.False(t, hasArgKey(&mcpx.CallToolRequest{Params: &mcp.CallToolParamsRaw{Arguments: []byte("not-json")}}, "value"))
	assert.True(t, hasArgKey(&mcpx.CallToolRequest{Params: &mcp.CallToolParamsRaw{Arguments: []byte(`{"value":null}`)}}, "value"))
	assert.False(t, hasArgKey(&mcpx.CallToolRequest{Params: &mcp.CallToolParamsRaw{Arguments: []byte(`{"address":1}`)}}, "value"))
}

// TestNVSOffsetSize covers nvsOffsetSize's default-when-omitted and
// honor-explicit-zero semantics.
func TestNVSOffsetSize(t *testing.T) {
	offset, size := nvsOffsetSize(nil, nil)
	assert.Equal(t, uint32(0x9000), offset)
	assert.Equal(t, uint32(0x6000), size)

	explicitZero := uint32(0)
	offset, size = nvsOffsetSize(&explicitZero, &explicitZero)
	assert.Equal(t, uint32(0), offset)
	assert.Equal(t, uint32(0), size)

	custom := uint32(0x5000)
	offset, size = nvsOffsetSize(&custom, nil)
	assert.Equal(t, uint32(0x5000), offset)
	assert.Equal(t, uint32(0x6000), size)
}
