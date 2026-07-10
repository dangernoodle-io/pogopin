package session

import (
	"time"

	"dangernoodle.io/pogopin/internal/esp"
	"dangernoodle.io/pogopin/internal/serial"
	goSerial "go.bug.st/serial"
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
func SetListPortsFn(fn func() ([]serial.PortInfo, error)) func() ([]serial.PortInfo, error) {
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

// SetWaitForPortInterval atomically sets the wait interval and returns the
// previous value. Race-free against concurrent reads from an in-flight
// expireSession goroutine (BR-63) — see waitForPortIntervalNanos in
// session.go.
func SetWaitForPortInterval(d time.Duration) time.Duration {
	return time.Duration(waitForPortIntervalNanos.Swap(int64(d)))
}

// SetDeferredRestartTimeout atomically sets the deferred restart timeout and
// returns the previous value. See SetWaitForPortInterval.
func SetDeferredRestartTimeout(d time.Duration) time.Duration {
	return time.Duration(deferredRestartTimeoutNanos.Swap(int64(d)))
}

// DeferredRestartTimeout returns the current deferred-restart duration.
// Exported so callers outside this package (e.g. a downstream hardware-bench
// harness) can read the value without the side-effecting SetDeferredRestartTimeout
// round-trip; it simply wraps the internal atomic accessor.
func DeferredRestartTimeout() time.Duration {
	return deferredRestartTimeout()
}

// SetSyncRetryDelay atomically sets the sync retry delay and returns the
// previous value. See SetWaitForPortInterval.
func SetSyncRetryDelay(d time.Duration) time.Duration {
	return time.Duration(syncRetryDelayNanos.Swap(int64(d)))
}

// WaitForExpireSessions blocks until every deferred-restart timer callback
// (ReleaseFlasherDeferred's expireSession goroutines) that was outstanding
// at the moment of this call has finished. It joins via the expireTimers
// registry (session.go) rather than scanning the ports map: expireSession
// can delete(ports, port) on its failure/never-re-enumerate path before its
// callback closes its done channel, which would make that goroutine
// invisible to a ports-map scan and let it keep running past this call
// (BR-63 delete-path fix) — the registry tracks "goroutine still possibly
// running" independent of ports membership. A session whose timer was
// successfully stopped before firing (see stopSessionTimerLocked) or that
// was never scheduled via ReleaseFlasherDeferred was already unregistered
// (or never registered) and has nothing to wait for. The registry is
// snapshotted and its lock released before blocking on any channel, and
// this never holds portsMu while waiting, so it can't deadlock against a
// callback that needs portsMu to finish and close its channel. Call in test
// cleanup, ideally after best-effort stopping timers, to guarantee no
// callback outlives the test that scheduled it — without this, a callback
// that fires right at test teardown can keep running into the next test,
// racing that test's mutation of the injectable timer values above and
// mutating stale session/ports state (BR-63).
func WaitForExpireSessions() {
	expireTimersMu.Lock()
	pending := make([]chan struct{}, 0, len(expireTimers))
	for done := range expireTimers {
		pending = append(pending, done)
	}
	expireTimersMu.Unlock()
	for _, done := range pending {
		<-done
	}
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

// SetSerialOpenFn sets the serial open function and returns the previous value.
func SetSerialOpenFn(fn func(string, *goSerial.Mode) (goSerial.Port, error)) func(string, *goSerial.Mode) (goSerial.Port, error) {
	prev := serialOpen
	serialOpen = fn
	return prev
}
