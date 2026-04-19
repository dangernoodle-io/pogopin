package session

import (
	"time"

	"dangernoodle.io/pogopin/internal/esp"
	"dangernoodle.io/pogopin/internal/serial"
)

// NewPortSession creates a PortSession with the given fields. Exported for cross-package testing.
func NewPortSession(mgr *serial.Manager, port string, baud int, mode PortMode) *PortSession {
	return &PortSession{mgr: mgr, port: port, baud: baud, mode: mode}
}

// SetNewManagerFunc sets the manager factory and returns the previous value.
func SetNewManagerFunc(fn func(int) *serial.Manager) func(int) *serial.Manager {
	prev := newManagerFunc
	newManagerFunc = fn
	return prev
}

// SetFlasherFactory sets the flasher factory and returns the previous value.
func SetFlasherFactory(f esp.FlasherFactory) esp.FlasherFactory {
	prev := newFlasherFactory
	newFlasherFactory = f
	return prev
}

// SetListPortsFn sets the port listing function and returns the previous value.
func SetListPortsFn(fn func(bool) ([]serial.PortInfo, error)) func(bool) ([]serial.PortInfo, error) {
	prev := listPortsFn
	listPortsFn = fn
	return prev
}

// SetIsUSBPortFn sets the USB port detection function and returns the previous value.
func SetIsUSBPortFn(fn func(string) bool) func(string) bool {
	prev := isUSBPortFn
	isUSBPortFn = fn
	return prev
}

// SetWaitForPortInterval sets the wait interval and returns the previous value.
func SetWaitForPortInterval(d time.Duration) time.Duration {
	prev := waitForPortInterval
	waitForPortInterval = d
	return prev
}

// SetDeferredRestartTimeout sets the deferred restart timeout and returns the previous value.
func SetDeferredRestartTimeout(d time.Duration) time.Duration {
	prev := deferredRestartTimeout
	deferredRestartTimeout = d
	return prev
}

// SetSyncRetryDelay sets the sync retry delay and returns the previous value.
func SetSyncRetryDelay(d time.Duration) time.Duration {
	prev := syncRetryDelay
	syncRetryDelay = d
	return prev
}

// SetPorts replaces the ports map and returns the previous value. NOT thread-safe — call only from test setup.
func SetPorts(m map[string]*PortSession) map[string]*PortSession {
	prev := ports
	ports = m
	return prev
}

// InsertPort inserts a session into the ports map under the lock.
func InsertPort(key string, sess *PortSession) {
	portsMu.Lock()
	defer portsMu.Unlock()
	ports[key] = sess
}

// IsUSBPort delegates to the injectable isUSBPortFn.
func IsUSBPort(port string) bool {
	return isUSBPortFn(port)
}
