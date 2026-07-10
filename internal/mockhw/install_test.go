package mockhw

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"dangernoodle.io/pogopin/internal/serial"
	"dangernoodle.io/pogopin/internal/session"
	goSerial "go.bug.st/serial"
)

var errSentinel = errors.New("sentinel: seam not wired")

// TestInstallWiresSeamsAndRestore drives mockhw.Install directly: it pins
// each of the five seams (session.serialOpen, serial.listPortsFn,
// session.isUSBPortFn, session.listPortsFn, session.newManagerFunc) to a
// distinguishable sentinel, calls Install, asserts each seam now exhibits
// the mock-chip behavior Install documents, then calls the returned
// restore closure and asserts every seam is back to the sentinel that was
// in place before Install ran. t.Cleanup restores the true pre-test seam
// values regardless of outcome.
func TestInstallWiresSeamsAndRestore(t *testing.T) {
	sentinelOpen := func(string, *goSerial.Mode) (goSerial.Port, error) { return nil, errSentinel }
	origOpen := session.SetSerialOpenFn(sentinelOpen)
	t.Cleanup(func() { session.SetSerialOpenFn(origOpen) })

	sentinelListPorts := func() ([]serial.PortInfo, error) { return nil, errSentinel }
	origServerListPorts := serial.SetListPortsFn(sentinelListPorts)
	t.Cleanup(func() { serial.SetListPortsFn(origServerListPorts) })

	sentinelIsUSBPort := func(string) bool { return true }
	origIsUSBPort := session.SetIsUSBPortFn(sentinelIsUSBPort)
	t.Cleanup(func() { session.SetIsUSBPortFn(origIsUSBPort) })

	sentinelSessionListPorts := func() ([]serial.PortInfo, error) { return nil, errSentinel }
	origSessionListPorts := session.SetListPortsFn(sentinelSessionListPorts)
	t.Cleanup(func() { session.SetListPortsFn(origSessionListPorts) })

	sentinelNewManagerFunc := func(int) *serial.Manager {
		m := serial.NewManagerWithBufferSize(1)
		m.OpenFunc = func(string, *goSerial.Mode) (goSerial.Port, error) { return nil, errSentinel }
		return m
	}
	origNewManagerFunc := session.SetNewManagerFunc(sentinelNewManagerFunc)
	t.Cleanup(func() { session.SetNewManagerFunc(origNewManagerFunc) })

	restore := Install()

	installedOpen := session.SetSerialOpenFn(sentinelOpen)
	port, err := installedOpen(MockPortNameS2, &goSerial.Mode{})
	require.NoError(t, err)
	vp, ok := port.(*virtualPort)
	assert.True(t, ok, "Install must wire session.serialOpen to newVirtualPort")
	assert.Same(t, profileESP32S2, vp.profile, "known port name must resolve to its chipProfile")

	_, err = installedOpen("/dev/unknown", &goSerial.Mode{})
	assert.Error(t, err, "an unrecognized mock port name must fail loud, not fall back to a default profile")

	installedServerListPorts := serial.SetListPortsFn(sentinelListPorts)
	ports, err := installedServerListPorts()
	require.NoError(t, err)
	assert.Equal(t, mockPorts, ports)

	installedIsUSBPort := session.SetIsUSBPortFn(sentinelIsUSBPort)
	assert.False(t, installedIsUSBPort("/dev/anything"), "Install must wire session.isUSBPortFn to always report false")

	installedSessionListPorts := session.SetListPortsFn(sentinelSessionListPorts)
	ports, err = installedSessionListPorts()
	require.NoError(t, err)
	assert.Equal(t, mockPorts, ports)

	installedNewManagerFunc := session.SetNewManagerFunc(sentinelNewManagerFunc)
	mgr := installedNewManagerFunc(1)
	monitorPort, err := mgr.OpenFunc("/dev/anything", &goSerial.Mode{})
	require.NoError(t, err)
	_, ok = monitorPort.(*virtualMonitorPort)
	assert.True(t, ok, "Install must wire session.newManagerFunc's OpenFunc to newVirtualMonitorPort")

	restore()

	restoredOpen := session.SetSerialOpenFn(sentinelOpen)
	_, err = restoredOpen("", nil)
	assert.ErrorIs(t, err, errSentinel, "restore must put back the pre-Install seam")

	restoredServerListPorts := serial.SetListPortsFn(sentinelListPorts)
	_, err = restoredServerListPorts()
	assert.ErrorIs(t, err, errSentinel, "restore must put back the pre-Install seam")

	restoredIsUSBPort := session.SetIsUSBPortFn(sentinelIsUSBPort)
	assert.True(t, restoredIsUSBPort(""), "restore must put back the pre-Install seam")

	restoredSessionListPorts := session.SetListPortsFn(sentinelSessionListPorts)
	_, err = restoredSessionListPorts()
	assert.ErrorIs(t, err, errSentinel, "restore must put back the pre-Install seam")

	restoredNewManagerFunc := session.SetNewManagerFunc(sentinelNewManagerFunc)
	restoredMgr := restoredNewManagerFunc(1)
	_, err = restoredMgr.OpenFunc("", nil)
	assert.ErrorIs(t, err, errSentinel, "restore must put back the pre-Install seam")
}

// TestProfileByPort table-tests the port-name -> chipProfile map directly:
// every known mock port name resolves to its documented profile, and an
// unknown port name is absent (SerialOpenFn's error path relies on this).
func TestProfileByPort(t *testing.T) {
	cases := []struct {
		port    string
		profile *chipProfile
	}{
		{MockPortNameS2, profileESP32S2},
		{MockPortNameC3, profileESP32C3},
		{MockPortNameS3, profileESP32S3},
		{MockPortNameESP32, profileESP32},
	}

	for _, c := range cases {
		t.Run(c.port, func(t *testing.T) {
			got, ok := profileByPort[c.port]
			require.True(t, ok, "known mock port must be present in profileByPort")
			assert.Same(t, c.profile, got)
		})
	}

	_, ok := profileByPort["/dev/unknown"]
	assert.False(t, ok, "unrecognized port name must not resolve to any profile")

	assert.Len(t, mockPorts, len(profileByPort), "mockPorts must list every profileByPort entry exactly once")
	for _, p := range mockPorts {
		_, ok := profileByPort[p.Name]
		assert.True(t, ok, "every mockPorts entry must have a matching profileByPort entry: %s", p.Name)
	}
}
