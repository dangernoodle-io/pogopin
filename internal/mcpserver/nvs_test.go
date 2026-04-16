package mcpserver

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"tinygo.org/x/espflasher/pkg/nvs"
)

// NVS parsing tests

func TestParseNVSParams(t *testing.T) {
	// Test defaults
	offset, size, baud := parseNVSParams(map[string]interface{}{})
	assert.Equal(t, uint32(0x9000), offset)
	assert.Equal(t, uint32(nvs.DefaultPartSize), size)
	assert.Equal(t, 0, baud) // baud defaults to 0, not 115200

	// Test custom offset
	offset, size, baud = parseNVSParams(map[string]interface{}{"offset": float64(0x5000)})
	assert.Equal(t, uint32(0x5000), offset)
	assert.Equal(t, uint32(nvs.DefaultPartSize), size)
	assert.Equal(t, 0, baud)

	// Test custom size
	offset, size, baud = parseNVSParams(map[string]interface{}{"size": float64(0x7000)})
	assert.Equal(t, uint32(0x9000), offset)
	assert.Equal(t, uint32(0x7000), size)
	assert.Equal(t, 0, baud)

	// Test custom baud
	offset, size, baud = parseNVSParams(map[string]interface{}{"baud": float64(460800)})
	assert.Equal(t, uint32(0x9000), offset)
	assert.Equal(t, uint32(nvs.DefaultPartSize), size)
	assert.Equal(t, 460800, baud)

	// Test all custom
	offset, size, baud = parseNVSParams(map[string]interface{}{
		"offset": float64(0x1000),
		"size":   float64(0x5000),
		"baud":   float64(921600),
	})
	assert.Equal(t, uint32(0x1000), offset)
	assert.Equal(t, uint32(0x5000), size)
	assert.Equal(t, 921600, baud)

	// Test wrong types (should use defaults)
	offset, size, baud = parseNVSParams(map[string]interface{}{
		"offset": "invalid",
		"size":   []int{1, 2},
		"baud":   true,
	})
	assert.Equal(t, uint32(0x9000), offset)
	assert.Equal(t, uint32(nvs.DefaultPartSize), size)
	assert.Equal(t, 0, baud)
}

func TestParseNVSValue(t *testing.T) {
	// Test u8
	val, err := parseNVSValue("u8", float64(255))
	require.NoError(t, err)
	assert.Equal(t, uint8(255), val)

	// Test u16
	val, err = parseNVSValue("u16", float64(65535))
	require.NoError(t, err)
	assert.Equal(t, uint16(65535), val)

	// Test u32
	val, err = parseNVSValue("u32", float64(0xDEADBEEF))
	require.NoError(t, err)
	assert.Equal(t, uint32(0xDEADBEEF), val)

	// Test i8
	val, err = parseNVSValue("i8", float64(-128))
	require.NoError(t, err)
	assert.Equal(t, int8(-128), val)

	// Test i16
	val, err = parseNVSValue("i16", float64(-32768))
	require.NoError(t, err)
	assert.Equal(t, int16(-32768), val)

	// Test i32
	val, err = parseNVSValue("i32", float64(-2147483648))
	require.NoError(t, err)
	assert.Equal(t, int32(-2147483648), val)

	// Test string
	val, err = parseNVSValue("string", "test-value")
	require.NoError(t, err)
	assert.Equal(t, "test-value", val)

	// Test string from float64 (should fail - must be a string)
	_, err = parseNVSValue("string", float64(42))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "must be a string")

	// Test invalid type
	_, err = parseNVSValue("invalid", float64(42))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported NVS type")

	// Test wrong value type for u8
	_, err = parseNVSValue("u8", "not-a-number")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "must be a number")
}

func TestParseNVSValueFromString(t *testing.T) {
	// Test u8
	val, err := parseNVSValueFromString("u8", "255")
	require.NoError(t, err)
	assert.Equal(t, uint8(255), val)

	// Test u16
	val, err = parseNVSValueFromString("u16", "65535")
	require.NoError(t, err)
	assert.Equal(t, uint16(65535), val)

	// Test u32
	val, err = parseNVSValueFromString("u32", "4294967295")
	require.NoError(t, err)
	assert.Equal(t, uint32(4294967295), val)

	// Test i8
	val, err = parseNVSValueFromString("i8", "-128")
	require.NoError(t, err)
	assert.Equal(t, int8(-128), val)

	// Test i16
	val, err = parseNVSValueFromString("i16", "-32768")
	require.NoError(t, err)
	assert.Equal(t, int16(-32768), val)

	// Test i32
	val, err = parseNVSValueFromString("i32", "-2147483648")
	require.NoError(t, err)
	assert.Equal(t, int32(-2147483648), val)

	// Test string
	val, err = parseNVSValueFromString("string", "test-value")
	require.NoError(t, err)
	assert.Equal(t, "test-value", val)

	// Test invalid type
	_, err = parseNVSValueFromString("invalid", "42")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported NVS type")

	// Test invalid u8 value
	_, err = parseNVSValueFromString("u8", "256")
	require.Error(t, err)

	// Test invalid i32 value
	_, err = parseNVSValueFromString("i32", "invalid")
	require.Error(t, err)
}
