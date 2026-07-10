package mockhw

import (
	"fmt"

	"dangernoodle.io/pogopin/internal/serial"
	"dangernoodle.io/pogopin/internal/session"
	goSerial "go.bug.st/serial"
)

// Synthetic port names the virtual chips are addressed as once wired into
// the session/serial seams — one per emulated chip family. MockPortName is
// kept as the pre-multichip S2 alias for existing callers/tests.
const (
	MockPortNameS2    = "/dev/mock-esp32s2"
	MockPortNameC3    = "/dev/mock-esp32c3"
	MockPortNameS3    = "/dev/mock-esp32s3"
	MockPortNameESP32 = "/dev/mock-esp32"

	// MockPortName is the original single-chip mock port name, kept as an
	// alias for MockPortNameS2.
	MockPortName = MockPortNameS2
)

// profileByPort maps each synthetic mock port name to the chipProfile the
// virtual chip on that port emulates. SerialOpenFn below dispatches on this
// map; an unrecognized port name is a fail-loud error, not an S2 fallback.
var profileByPort = map[string]*chipProfile{
	MockPortNameS2:    profileESP32S2,
	MockPortNameC3:    profileESP32C3,
	MockPortNameS3:    profileESP32S3,
	MockPortNameESP32: profileESP32,
}

// mockPorts is the serial.PortInfo list every mock-wired seam reports: one
// entry per virtual chip in profileByPort, in a fixed order (map iteration
// order is randomized in Go, which would make ListPorts results flaky).
var mockPorts = []serial.PortInfo{
	{Name: MockPortNameS2},
	{Name: MockPortNameC3},
	{Name: MockPortNameS3},
	{Name: MockPortNameESP32},
}

// Install wires the virtual chips into the session and serial package
// seams so the mock-tagged server opens any port name in profileByPort
// against a virtualPort for that chip's profile instead of a real device.
// It captures each setter's previous value and returns a restore closure
// that puts all five seams back, so callers that need isolation (tests via
// t.Cleanup(mockhw.Install())) can undo the wiring; mcpserver.maybeEnableMock
// (mock-tagged build only) discards the restore since the mock-tagged
// binary runs mock wiring for its whole process lifetime.
//
// The fifth seam, session.SetNewManagerFunc, routes the serial-monitor path
// (serial_start/read/write/stop, via session.Manager.readLoop) to a
// virtualMonitorPort instead of a real device — separate from the
// flasher-path virtualPort above, which speaks the ESP ROM bootloader's
// SLIP protocol and has no monitor-log model. The monitor port is
// chip-agnostic (no chipProfile), so it is unaffected by the port-name
// argument.
func Install() (restore func()) {
	prevSerialOpen := session.SetSerialOpenFn(func(name string, _ *goSerial.Mode) (goSerial.Port, error) {
		profile, ok := profileByPort[name]
		if !ok {
			return nil, fmt.Errorf("mockhw: unknown mock port %q", name)
		}
		return newVirtualPort(profile), nil
	})
	prevListPorts := serial.SetListPortsFn(func() ([]serial.PortInfo, error) {
		return mockPorts, nil
	})
	prevIsUSBPort := session.SetIsUSBPortFn(func(string) bool { return false })
	prevSessionListPorts := session.SetListPortsFn(func() ([]serial.PortInfo, error) {
		return mockPorts, nil
	})
	prevNewManagerFunc := session.SetNewManagerFunc(func(bufSize int) *serial.Manager {
		mgr := serial.NewManagerWithBufferSize(bufSize)
		mgr.OpenFunc = func(string, *goSerial.Mode) (goSerial.Port, error) {
			return newVirtualMonitorPort(), nil
		}
		return mgr
	})

	return func() {
		session.SetSerialOpenFn(prevSerialOpen)
		serial.SetListPortsFn(prevListPorts)
		session.SetIsUSBPortFn(prevIsUSBPort)
		session.SetListPortsFn(prevSessionListPorts)
		session.SetNewManagerFunc(prevNewManagerFunc)
	}
}
