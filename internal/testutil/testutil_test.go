package testutil

import (
	"errors"
	"net"
	"testing"

	"github.com/stretchr/testify/assert"
	espflasher "tinygo.org/x/espflasher/pkg/espflasher"
)

// TestMockFlasher_ChipIdentity exercises MockFlasher's MAC/ChipRevision/
// ChipFeatures fields (E1-19/BR-96) directly -- this is a plain, non-_test.go
// file (see the package doc comment), so its own statements are only ever
// covered by tests within this package, never by the capability-layer tests
// that construct a MockFlasher and exercise it through esp.Flasher.
func TestMockFlasher_ChipIdentity(t *testing.T) {
	mac := net.HardwareAddr{0xde, 0xad, 0xbe, 0xef, 0x00, 0x01}
	rev := espflasher.ChipRevision{Major: 1, Minor: 2}
	feats := []string{"WiFi", "BLE"}

	m := &MockFlasher{
		MACVal:          mac,
		ChipRevisionVal: rev,
		ChipFeaturesVal: feats,
	}

	gotMAC, err := m.MAC()
	assert.NoError(t, err)
	assert.Equal(t, mac, gotMAC)

	gotRev, err := m.ChipRevision()
	assert.NoError(t, err)
	assert.Equal(t, rev, gotRev)

	gotFeats, err := m.ChipFeatures()
	assert.NoError(t, err)
	assert.Equal(t, feats, gotFeats)
}

// TestMockFlasher_ChipIdentityErrors exercises the *Err fields' fail-open
// return path.
func TestMockFlasher_ChipIdentityErrors(t *testing.T) {
	macErr := errors.New("mac unsupported")
	revErr := errors.New("chip revision unsupported")
	featsErr := errors.New("chip features unsupported")

	m := &MockFlasher{
		MACErr:          macErr,
		ChipRevisionErr: revErr,
		ChipFeaturesErr: featsErr,
	}

	_, err := m.MAC()
	assert.Equal(t, macErr, err)

	_, err = m.ChipRevision()
	assert.Equal(t, revErr, err)

	_, err = m.ChipFeatures()
	assert.Equal(t, featsErr, err)
}
