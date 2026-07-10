package mockhw

import (
	"dangernoodle.io/pogopin/internal/serial"
	"dangernoodle.io/pogopin/internal/session"
	goSerial "go.bug.st/serial"
)

// MockPortName is the synthetic port name the virtual chip is addressed as
// once wired into the session/serial seams.
const MockPortName = "/dev/mock-esp32s2"

// mockPorts is the serial.PortInfo list every mock-wired seam reports: the
// single virtual chip, addressed as MockPortName.
var mockPorts = []serial.PortInfo{{Name: MockPortName}}

// Install wires the virtual chip into the session and serial package seams
// so the mock-tagged server opens MockPortName against a virtualPort
// instead of a real device. It captures each setter's previous value and
// returns a restore closure that puts all five seams back, so callers that
// need isolation (tests via t.Cleanup(mockhw.Install())) can undo the
// wiring; mcpserver.maybeEnableMock (mock-tagged build only) discards the
// restore since the mock-tagged binary runs mock wiring for its whole
// process lifetime.
//
// The fifth seam, session.SetNewManagerFunc, routes the serial-monitor path
// (serial_start/read/write/stop, via session.Manager.readLoop) to a
// virtualMonitorPort instead of a real device — separate from the
// flasher-path virtualPort above, which speaks the ESP ROM bootloader's
// SLIP protocol and has no monitor-log model.
func Install() (restore func()) {
	prevSerialOpen := session.SetSerialOpenFn(func(string, *goSerial.Mode) (goSerial.Port, error) {
		return newVirtualPort(profileESP32S2), nil
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
