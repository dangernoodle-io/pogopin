package esp

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"tinygo.org/x/espflasher/pkg/nvs"
)

func TestVerifySetAppliedSuccess(t *testing.T) {
	pre := []nvs.Entry{
		{Namespace: "wifi", Key: "ssid", Type: "string", Value: "old"},
		{Namespace: "wifi", Key: "untouched", Type: "u8", Value: uint8(1)},
	}
	post := []nvs.Entry{
		{Namespace: "wifi", Key: "ssid", Type: "string", Value: "new"},
		{Namespace: "wifi", Key: "untouched", Type: "u8", Value: uint8(1)},
	}
	updates := []NVSUpdate{{Namespace: "wifi", Key: "ssid", Type: "string", Value: "new"}}

	applied, err := verifySetApplied(pre, post, updates)
	require.NoError(t, err)
	assert.Equal(t, 1, applied)
}

func TestVerifySetAppliedMultipleUpdatesPreservesUntouched(t *testing.T) {
	pre := []nvs.Entry{
		{Namespace: "wifi", Key: "a", Type: "u8", Value: uint8(1)},
		{Namespace: "wifi", Key: "b", Type: "u8", Value: uint8(2)},
		{Namespace: "wifi", Key: "c", Type: "u8", Value: uint8(3)},
	}
	post := []nvs.Entry{
		{Namespace: "wifi", Key: "a", Type: "u8", Value: uint8(9)},
		{Namespace: "wifi", Key: "b", Type: "u8", Value: uint8(8)},
		{Namespace: "wifi", Key: "c", Type: "u8", Value: uint8(3)},
	}
	updates := []NVSUpdate{
		{Namespace: "wifi", Key: "a", Type: "u8", Value: uint8(9)},
		{Namespace: "wifi", Key: "b", Type: "u8", Value: uint8(8)},
	}

	applied, err := verifySetApplied(pre, post, updates)
	require.NoError(t, err)
	assert.Equal(t, 2, applied)
}

func TestVerifySetAppliedMissingAfterWrite(t *testing.T) {
	pre := []nvs.Entry{}
	post := []nvs.Entry{}
	updates := []NVSUpdate{{Namespace: "wifi", Key: "ssid", Type: "string", Value: "new"}}

	_, err := verifySetApplied(pre, post, updates)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not present after write")
}

func TestVerifySetAppliedValueMismatch(t *testing.T) {
	pre := []nvs.Entry{}
	post := []nvs.Entry{
		{Namespace: "wifi", Key: "ssid", Type: "string", Value: "stale"},
	}
	updates := []NVSUpdate{{Namespace: "wifi", Key: "ssid", Type: "string", Value: "new"}}

	_, err := verifySetApplied(pre, post, updates)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "value mismatch")
}

func TestVerifySetAppliedLostPreExistingKey(t *testing.T) {
	pre := []nvs.Entry{
		{Namespace: "wifi", Key: "ssid", Type: "string", Value: "old"},
		{Namespace: "wifi", Key: "dropped", Type: "u8", Value: uint8(1)},
	}
	post := []nvs.Entry{
		{Namespace: "wifi", Key: "ssid", Type: "string", Value: "new"},
		// "dropped" is gone from post despite not being in updates.
	}
	updates := []NVSUpdate{{Namespace: "wifi", Key: "ssid", Type: "string", Value: "new"}}

	_, err := verifySetApplied(pre, post, updates)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "was lost during write")
}

func TestVerifyDeleteAppliedSingleKeySuccess(t *testing.T) {
	pre := []nvs.Entry{
		{Namespace: "wifi", Key: "ssid", Type: "string", Value: "x"},
		{Namespace: "wifi", Key: "other", Type: "u8", Value: uint8(1)},
	}
	post := []nvs.Entry{
		{Namespace: "wifi", Key: "other", Type: "u8", Value: uint8(1)},
	}

	deleted, err := verifyDeleteApplied(pre, post, "wifi", "ssid")
	require.NoError(t, err)
	assert.Equal(t, 1, deleted)
}

func TestVerifyDeleteAppliedNamespaceSuccess(t *testing.T) {
	pre := []nvs.Entry{
		{Namespace: "wifi", Key: "a", Type: "u8", Value: uint8(1)},
		{Namespace: "wifi", Key: "b", Type: "u8", Value: uint8(2)},
		{Namespace: "other", Key: "c", Type: "u8", Value: uint8(3)},
	}
	post := []nvs.Entry{
		{Namespace: "other", Key: "c", Type: "u8", Value: uint8(3)},
	}

	deleted, err := verifyDeleteApplied(pre, post, "wifi", "")
	require.NoError(t, err)
	assert.Equal(t, 2, deleted)
}

func TestVerifyDeleteAppliedStillPresentAfterDelete(t *testing.T) {
	pre := []nvs.Entry{
		{Namespace: "wifi", Key: "ssid", Type: "string", Value: "x"},
	}
	post := []nvs.Entry{
		{Namespace: "wifi", Key: "ssid", Type: "string", Value: "x"},
	}

	_, err := verifyDeleteApplied(pre, post, "wifi", "ssid")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "still present after delete")
}

func TestVerifyDeleteAppliedLostUnrelatedKey(t *testing.T) {
	pre := []nvs.Entry{
		{Namespace: "wifi", Key: "ssid", Type: "string", Value: "x"},
		{Namespace: "wifi", Key: "other", Type: "u8", Value: uint8(1)},
	}
	post := []nvs.Entry{
		// both "ssid" (deleted, expected) and "other" (unexpectedly lost) are gone.
	}

	_, err := verifyDeleteApplied(pre, post, "wifi", "ssid")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "was lost during delete")
}
