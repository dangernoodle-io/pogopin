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
// each of the four seams (session.serialOpen, serial.listPortsFn,
// session.isUSBPortFn, session.listPortsFn) to a distinguishable sentinel,
// calls Install, asserts each seam now exhibits the mock-chip behavior
// Install documents, then calls the returned restore closure and asserts
// every seam is back to the sentinel that was in place before Install ran.
// t.Cleanup restores the true pre-test seam values regardless of outcome.
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

	restore := Install()

	installedOpen := session.SetSerialOpenFn(sentinelOpen)
	port, err := installedOpen("/dev/anything", &goSerial.Mode{})
	require.NoError(t, err)
	_, ok := port.(*virtualPort)
	assert.True(t, ok, "Install must wire session.serialOpen to newVirtualPort")

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
}
