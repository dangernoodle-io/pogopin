package session

import (
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"dangernoodle.io/breadboard/internal/esp"
	"dangernoodle.io/breadboard/internal/serial"
	espflasher "tinygo.org/x/espflasher/pkg/espflasher"
)

var (
	newManagerFunc = func(bufSize int) *serial.Manager {
		return serial.NewManagerWithBufferSize(bufSize)
	}
	newFlasherFactory esp.FlasherFactory = esp.DefaultFlasherFactory
	listPortsFn                          = serial.ListPorts
	isUSBPortFn                          = serial.IsUSBPort
)

// PortMode indicates the current usage mode of a port.
type PortMode int

const (
	ModeReader   PortMode = iota // serial reader active
	ModeFlasher                  // ESP flasher owns port
	ModeExternal                 // external command owns port
	ModePending                  // deferred restart pending
)

// PortSession represents the state of a managed serial port.
type PortSession struct {
	mgr     *serial.Manager
	port    string
	baud    int
	mode    PortMode
	flasher esp.Flasher // cached flasher (only in ModeFlasher/ModePending)
	timer   *time.Timer // deferred restart timer (only in ModePending)
}

var (
	ports                  = map[string]*PortSession{}
	portsMu                sync.Mutex
	waitForPortInterval    = 50 * time.Millisecond
	deferredRestartTimeout = 5 * time.Second
	syncRetryDelay         = 1 * time.Second
)

// retryFlasherCreate retries flasher creation on sync failure.
// USB ports get up to 3 retries with 1s delays (device may still be re-enumerating).
// All ports try FindSimilarPort as a last resort.
func retryFlasherCreate(port string, opts *espflasher.FlasherOptions, sess *PortSession) (esp.Flasher, error) {
	f, err := newFlasherFactory(port, opts)
	if err == nil {
		return f, nil
	}

	var syncErr *espflasher.SyncError
	if !errors.As(err, &syncErr) {
		return nil, err
	}

	// USB ports may need time after re-enumeration
	if isUSBPortFn(port) {
		for i := 0; i < 3; i++ {
			time.Sleep(syncRetryDelay)
			f, err = newFlasherFactory(port, opts)
			if err == nil {
				return f, nil
			}
		}
	}

	// Try finding a re-enumerated port
	newPort := serial.FindSimilarPort(port, listPortsFn)
	if newPort == "" || newPort == port {
		return nil, err
	}

	f, err = newFlasherFactory(newPort, opts)
	if err != nil {
		return nil, err
	}

	// Update session port mapping
	portsMu.Lock()
	delete(ports, port)
	sess.port = newPort
	ports[newPort] = sess
	portsMu.Unlock()

	return f, nil
}

// BorrowedFlasher wraps an esp.Flasher and overrides Reset/Close to manage ownership.
type BorrowedFlasher struct {
	esp.Flasher
	onReturn func(esp.Flasher)
}

// Reset is a no-op to keep the device in bootloader mode.
func (b *BorrowedFlasher) Reset() {
	// no-op
}

// Close calls the onReturn callback with the flasher and returns nil.
func (b *BorrowedFlasher) Close() error {
	b.onReturn(b.Flasher)
	return nil
}

// WaitForPort polls for port availability by file existence or re-enumeration.
// Returns the port name if found, or "" on timeout.
func WaitForPort(port string, timeout time.Duration) string {
	deadline := time.Now().Add(timeout)
	for {
		// Check if port exists
		if _, err := os.Stat(port); err == nil {
			return port
		}

		// Check for re-enumerated port
		if p := serial.FindSimilarPort(port, listPortsFn); p != "" {
			return p
		}

		// Check timeout
		if time.Now().Add(waitForPortInterval).After(deadline) {
			return ""
		}

		time.Sleep(waitForPortInterval)
	}
}

// AcquireForFlasher prepares a port for ESP flashing. Returns the session and a flasher factory.
// The factory handles caching: if a flasher was deferred, it wraps it as borrowed; otherwise,
// it returns a real flasher from newFlasherFactory.
func AcquireForFlasher(port string) (*PortSession, esp.FlasherFactory) {
	portsMu.Lock()
	defer portsMu.Unlock()

	sess, exists := ports[port]
	if exists {
		switch sess.mode {
		case ModeReader:
			_ = sess.mgr.Stop()
			sess.mode = ModeFlasher
		case ModePending:
			if sess.timer != nil {
				sess.timer.Stop()
				sess.timer = nil
			}
			// Preserve cached flasher if any - it will be reused
			sess.mode = ModeFlasher
		}
	} else {
		sess = &PortSession{
			mgr:  newManagerFunc(1000),
			port: port,
			baud: 0,
			mode: ModeFlasher,
		}
		ports[port] = sess
	}

	factory := func(portArg string, opts *espflasher.FlasherOptions) (esp.Flasher, error) {
		if sess.flasher != nil {
			// Flush stale data from serial buffer and SLIP reader before reuse.
			// ReadFlash's raw block protocol can leave leftover bytes that
			// corrupt subsequent command responses.
			sess.flasher.FlushInput()
			// Return borrowed flasher wrapping the cached one
			f := sess.flasher
			sess.flasher = nil
			return &BorrowedFlasher{
				Flasher: f,
				onReturn: func(flasher esp.Flasher) {
					portsMu.Lock()
					sess.flasher = flasher
					portsMu.Unlock()
				},
			}, nil
		}
		// USB CDC ports must use usb_jtag reset to avoid corrupting
		// the USB-JTAG/Serial peripheral's DTR state machine.
		if isUSBPortFn(portArg) && opts.ResetMode == espflasher.ResetAuto {
			opts.ResetMode = espflasher.ResetUSBJTAG
		}
		// Create real flasher and wrap as borrowed so Reset() is no-op
		// and Close() caches it for the next tool call.
		f, err := retryFlasherCreate(portArg, opts, sess)
		if err != nil {
			return nil, err
		}
		return &BorrowedFlasher{
			Flasher: f,
			onReturn: func(flasher esp.Flasher) {
				portsMu.Lock()
				sess.flasher = flasher
				portsMu.Unlock()
			},
		}, nil
	}

	return sess, factory
}

// expireSession is called when a deferred session timer expires. It restarts the reader or cleans up.
func expireSession(sess *PortSession, port string) {
	portsMu.Lock()
	defer portsMu.Unlock()

	// Reset and close cached flasher. The device is in bootloader/stub mode
	// (BorrowedFlasher always caches via onReturn). Reset() returns it to
	// user code. On USB CDC this triggers re-enumeration (1-3s), handled
	// by the WaitForPort below.
	if sess.flasher != nil {
		sess.flasher.Reset()
		_ = sess.flasher.Close()
		sess.flasher = nil
	}

	// Wait for port availability (USB CDC may need time after close)
	portsMu.Unlock()
	foundPort := WaitForPort(port, 3*time.Second)
	portsMu.Lock()

	// Another goroutine may have acquired the session while we waited
	// (e.g. AcquireForFlasher set ModeFlasher). Don't interfere.
	if sess.mode != ModePending {
		return
	}

	if foundPort != "" {
		err := sess.mgr.Start(foundPort, sess.baud)
		if err == nil {
			sess.port = foundPort
			sess.mode = ModeReader
			// If port changed, update map
			if foundPort != port {
				delete(ports, port)
				ports[foundPort] = sess
			}
			return
		}
	}

	// Could not restart, delete session
	delete(ports, port)
}

// ReleaseFlasherDeferred schedules a deferred restart via timer. Used by async handlers.
func ReleaseFlasherDeferred(sess *PortSession, port string) {
	portsMu.Lock()
	defer portsMu.Unlock()

	sess.mode = ModePending
	sess.timer = time.AfterFunc(deferredRestartTimeout, func() {
		expireSession(sess, port)
	})
}

// ReleaseFlasherImmediate restarts the reader immediately. Used by inline handlers.
// Returns the new port name if the port was re-enumerated, otherwise "".
func ReleaseFlasherImmediate(sess *PortSession, port string) string {
	// Close cached flasher while holding lock
	portsMu.Lock()
	if sess.flasher != nil {
		sess.flasher.Reset()
		_ = sess.flasher.Close()
		sess.flasher = nil
	}
	portsMu.Unlock()

	// Wait for port (outside lock)
	foundPort := WaitForPort(port, 3*time.Second)
	if foundPort == "" {
		return ""
	}

	portsMu.Lock()
	defer portsMu.Unlock()

	err := sess.mgr.Start(foundPort, sess.baud)
	if err != nil {
		return ""
	}

	sess.mode = ModeReader
	oldPort := sess.port
	sess.port = foundPort

	// If port changed, update map
	if foundPort != oldPort {
		delete(ports, oldPort)
		ports[foundPort] = sess
		return foundPort
	}

	return ""
}

// AcquireForExternal prepares a port for an external command. Returns the session.
func AcquireForExternal(port string) *PortSession {
	portsMu.Lock()
	defer portsMu.Unlock()

	sess, exists := ports[port]
	if exists {
		// Close cached flasher if any
		if sess.flasher != nil {
			sess.flasher.Reset()
			_ = sess.flasher.Close()
			sess.flasher = nil
		}

		switch sess.mode {
		case ModeReader:
			_ = sess.mgr.Stop()
		case ModePending:
			if sess.timer != nil {
				sess.timer.Stop()
				sess.timer = nil
			}
		}
	} else {
		sess = &PortSession{
			mgr:  newManagerFunc(1000),
			port: port,
			baud: 0,
			mode: ModeExternal,
		}
		ports[port] = sess
	}

	sess.mode = ModeExternal
	return sess
}

// ReleaseExternal restarts the reader after an external command. Returns the new port name if re-enumerated.
func ReleaseExternal(sess *PortSession, port string) string {
	foundPort := WaitForPort(port, 3*time.Second)
	if foundPort == "" {
		return ""
	}

	portsMu.Lock()
	defer portsMu.Unlock()

	err := sess.mgr.Start(foundPort, sess.baud)
	if err != nil {
		return ""
	}

	sess.mode = ModeReader
	oldPort := sess.port
	sess.port = foundPort

	// If port changed, update map
	if foundPort != oldPort {
		delete(ports, oldPort)
		ports[foundPort] = sess
		return foundPort
	}

	return ""
}

// ResolveSession finds or restores a session for read/write/status operations.
// Returns the manager, port name, and error. Takes lock internally.
func ResolveSession(args map[string]interface{}) (*serial.Manager, string, error) {
	portsMu.Lock()
	defer portsMu.Unlock()

	port, _ := args["port"].(string)

	// Look up session by port or fall back to single entry
	var sess *PortSession
	var resolvedPort string

	if port != "" {
		var ok bool
		sess, ok = ports[port]
		if !ok {
			return nil, port, fmt.Errorf("no serial port open for %s; call serial_start first", port)
		}
		resolvedPort = port
	} else {
		if len(ports) == 0 {
			return nil, "", fmt.Errorf("no serial port open; call serial_start first")
		}
		if len(ports) == 1 {
			for p, s := range ports {
				sess = s
				resolvedPort = p
				break
			}
		} else {
			var names []string
			for p := range ports {
				names = append(names, p)
			}
			return nil, "", fmt.Errorf("multiple ports open (%v); specify port parameter", names)
		}
	}

	// Handle pending/deferred restart
	if sess.mode == ModePending {
		if sess.timer != nil {
			sess.timer.Stop()
			sess.timer = nil
		}
		// Close cached flasher if any
		if sess.flasher != nil {
			sess.flasher.Reset()
			_ = sess.flasher.Close()
			sess.flasher = nil
		}
		// Restart reader
		portsMu.Unlock()
		foundPort := WaitForPort(resolvedPort, 0)
		portsMu.Lock()
		if foundPort != "" {
			_ = sess.mgr.Start(foundPort, sess.baud)
			sess.port = foundPort
			sess.mode = ModeReader
			if foundPort != resolvedPort {
				delete(ports, resolvedPort)
				ports[foundPort] = sess
				resolvedPort = foundPort
			}
		}
	}

	// Check if manager is dead
	if !sess.mgr.IsRunning() && !sess.mgr.IsReconnecting() && sess.mgr.BufferCount() == 0 {
		delete(ports, resolvedPort)
		return nil, resolvedPort, fmt.Errorf("serial reader for %s has stopped; call serial_start to reconnect", resolvedPort)
	}

	return sess.mgr, resolvedPort, nil
}

// StartSession opens a port and begins reading. Takes lock internally.
func StartSession(port string, baud int, bufSize int) error {
	portsMu.Lock()
	defer portsMu.Unlock()

	sess, exists := ports[port]
	if exists {
		// Cancel pending timer if any
		if sess.timer != nil {
			sess.timer.Stop()
			sess.timer = nil
		}
		// Close cached flasher if any
		if sess.flasher != nil {
			sess.flasher.Reset()
			_ = sess.flasher.Close()
			sess.flasher = nil
		}
		// Restart reader with new baud
		_ = sess.mgr.Stop()
		sess.baud = baud
	} else {
		sess = &PortSession{
			mgr:  newManagerFunc(bufSize),
			port: port,
			baud: baud,
			mode: ModeReader,
		}
		ports[port] = sess
	}

	sess.mode = ModeReader
	return sess.mgr.Start(port, baud)
}

// StopSession closes a port and removes it from management. Takes lock internally.
func StopSession(port string) error {
	portsMu.Lock()
	defer portsMu.Unlock()

	sess, exists := ports[port]
	if !exists {
		return fmt.Errorf("no serial port open for %s", port)
	}

	// Cancel pending timer if any
	if sess.timer != nil {
		sess.timer.Stop()
		sess.timer = nil
	}

	// Close cached flasher if any
	if sess.flasher != nil {
		sess.flasher.Reset()
		_ = sess.flasher.Close()
		sess.flasher = nil
	}

	// Stop reader
	_ = sess.mgr.Stop()

	delete(ports, port)
	return nil
}

// CleanupAllSessions stops all managed ports. Used by signal handler.
func CleanupAllSessions() {
	portsMu.Lock()
	defer portsMu.Unlock()

	for port, sess := range ports {
		if sess.timer != nil {
			sess.timer.Stop()
			sess.timer = nil
		}
		if sess.flasher != nil {
			sess.flasher.Reset()
			_ = sess.flasher.Close()
			sess.flasher = nil
		}
		_ = sess.mgr.Stop()
		delete(ports, port)
	}
}

// GetManager returns the session's serial manager.
func (ps *PortSession) GetManager() *serial.Manager {
	return ps.mgr
}

// PortCount returns the number of active ports.
func PortCount() int {
	portsMu.Lock()
	defer portsMu.Unlock()
	return len(ports)
}

// AllPortStatus returns status for all active ports as a map.
func AllPortStatus() map[string]interface{} {
	portsMu.Lock()
	defer portsMu.Unlock()

	allStatus := map[string]interface{}{}
	for name, sess := range ports {
		m := sess.mgr
		status := map[string]interface{}{
			"running":      m.IsRunning(),
			"port":         m.PortName(),
			"baud":         m.Baud(),
			"buffer_lines": m.BufferCount(),
			"reconnecting": m.IsReconnecting(),
			"last_error":   nil,
		}
		if lastErr := m.LastError(); lastErr != nil {
			status["last_error"] = lastErr.Error()
		}
		allStatus[name] = status
	}
	return allStatus
}
