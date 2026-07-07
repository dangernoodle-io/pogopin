package session

import (
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"dangernoodle.io/pogopin/internal/esp"
	"dangernoodle.io/pogopin/internal/serial"
	"dangernoodle.io/pogopin/internal/status"
	goSerial "go.bug.st/serial"
	espflasher "tinygo.org/x/espflasher/pkg/espflasher"
)

var (
	newManagerFunc = func(bufSize int) *serial.Manager {
		return serial.NewManagerWithBufferSize(bufSize)
	}
	newFlasherFactory esp.FlasherFactory = esp.DefaultFlasherFactory
	listPortsFn                          = serial.ListPorts
	isUSBPortFn                          = serial.IsLikelyUSBSerial
	serialOpen                           = goSerial.Open
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

	// portsAtAcquire snapshots the set of system port names that existed at
	// AcquireForFlasher time, before any reset-triggered re-enumeration. It
	// lets FindSimilarPort tell a genuinely re-enumerated port (newly
	// appeared) apart from an unrelated board's port that already existed
	// and merely shares a USB-serial name prefix. Nil for sessions not
	// created via AcquireForFlasher.
	portsAtAcquire map[string]bool
}

// snapshotPortNames returns the current set of system port names, best-effort
// (empty map on listPortsFn error).
func snapshotPortNames() map[string]bool {
	out := map[string]bool{}
	if ports, err := listPortsFn(); err == nil {
		for _, p := range ports {
			out[p.Name] = true
		}
	}
	return out
}

var (
	ports                  = map[string]*PortSession{}
	portsMu                sync.Mutex
	waitForPortInterval    = 50 * time.Millisecond
	deferredRestartTimeout = 5 * time.Second
	syncRetryDelay         = 1 * time.Second
)

// snapshotPorts builds a status.PortState slice from the current ports map.
// Caller MUST hold portsMu.
func snapshotPorts() []status.PortState {
	out := make([]status.PortState, 0, len(ports))
	for p, s := range ports {
		out = append(out, portStateFor(p, s))
	}
	return out
}

func portStateFor(p string, s *PortSession) status.PortState {
	var lastErr *string
	if e := s.mgr.LastError(); e != nil {
		errStr := e.Error()
		lastErr = &errStr
	}
	return status.PortState{
		Port:         p,
		Baud:         s.mgr.Baud(),
		Mode:         modeString(s.mode),
		BufferLines:  s.mgr.BufferCount(),
		Running:      s.mgr.IsRunning(),
		Reconnecting: s.mgr.IsReconnecting(),
		LastError:    lastErr,
		SessionID:    os.Getenv("CLAUDE_CODE_SESSION_ID"),
		PID:          os.Getpid(),
	}
}

func modeString(m PortMode) string {
	switch m {
	case ModeReader:
		return "reader"
	case ModeFlasher:
		return "flasher"
	case ModeExternal:
		return "external"
	case ModePending:
		return "pending"
	}
	return "unknown"
}

// retryFlasherCreate retries flasher creation on sync failure.
// USB ports get up to 3 retries with 1s delays (device may still be re-enumerating).
// All ports try FindSimilarPort as a last resort.
func retryFlasherCreate(port string, opts *espflasher.FlasherOptions, sess *PortSession) (esp.Flasher, error) {
	// Wire the serial opener before creating the flasher
	if opts.SerialOpener == nil {
		opts.SerialOpener = OpenForFlasher(port)
	}

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

	// Try finding a re-enumerated port. Exclude ports that already existed at
	// acquire time so we don't hijack an unrelated board's port that merely
	// shares a USB-serial prefix (BR-58). portsAtAcquire is written under
	// portsMu (AcquireForFlasher/AcquireForExternal) so read it under the
	// same lock; the map itself is never mutated after being set, so the
	// local copy is safe to use unlocked afterward.
	portsMu.Lock()
	knownPorts := sess.portsAtAcquire
	portsMu.Unlock()
	newPort := serial.FindSimilarPort(port, listPortsFn, knownPorts)
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
// knownPorts, when non-nil, excludes ports that already existed before the
// wait began from the re-enumeration match (see FindSimilarPort / BR-58).
// Returns the port name if found, or "" on timeout.
func WaitForPort(port string, timeout time.Duration, knownPorts map[string]bool) string {
	deadline := time.Now().Add(timeout)
	for {
		// Check if port exists
		if _, err := os.Stat(port); err == nil {
			return port
		}

		// Check for re-enumerated port
		if p := serial.FindSimilarPort(port, listPortsFn, knownPorts); p != "" {
			return p
		}

		// Check timeout
		if time.Now().Add(waitForPortInterval).After(deadline) {
			return ""
		}

		time.Sleep(waitForPortInterval)
	}
}

// OpenForFlasher returns a serial opener suitable for espflasher's FlasherOptions.SerialOpener.
// It asserts the named port is currently in ModeFlasher (caller must have gone through
// AcquireForFlasher) and delegates to the configured serialOpen hook (goSerial.Open by default).
func OpenForFlasher(portName string) func(name string, mode *goSerial.Mode) (goSerial.Port, error) {
	return func(name string, mode *goSerial.Mode) (goSerial.Port, error) {
		portsMu.Lock()
		sess, ok := ports[portName]
		modeOK := ok && sess.mode == ModeFlasher
		portsMu.Unlock()
		if !modeOK {
			return nil, fmt.Errorf("port %s not in ModeFlasher; OpenForFlasher requires prior AcquireForFlasher", portName)
		}
		return serialOpen(name, mode)
	}
}

// AcquireForFlasher prepares a port for ESP flashing. Returns the session and a flasher factory.
// The factory handles caching: if a flasher was deferred, it wraps it as borrowed; otherwise,
// it returns a real flasher from newFlasherFactory.
//
// connectStatus, if non-nil, is wired onto FlasherOptions.ConnectStatus immediately before the
// real construction call (retryFlasherCreate / newFlasherFactory) so it observes the connect
// sequence (reset/sync/detect_chip/load_stub). The cached/borrowed-flasher path never calls
// New, so it never fires connectStatus — no connect happens there, which is correct.
func AcquireForFlasher(port string, connectStatus espflasher.ConnectStatusFunc) (*PortSession, esp.FlasherFactory) {
	// Snapshot the ports that exist right now, before any reset-triggered
	// re-enumeration happens under this acquire. Used by FindSimilarPort /
	// WaitForPort to avoid matching an unrelated board's pre-existing port
	// that merely shares a USB-serial name prefix (BR-58). Done before
	// taking portsMu — snapshotPortNames performs serial enumeration
	// (listPortsFn), which must never happen while holding the lock.
	portNames := snapshotPortNames()

	portsMu.Lock()
	defer func() {
		snap := snapshotPorts()
		portsMu.Unlock()
		status.Write(snap)
	}()

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

	sess.portsAtAcquire = portNames

	factory := func(portArg string, opts *espflasher.FlasherOptions) (esp.Flasher, error) {
		// Read the cached flasher pointer under the lock, then probe/close it
		// (serial I/O) without holding portsMu — other goroutines mutate
		// sess.flasher under the same lock (e.g. BorrowedFlasher.onReturn).
		portsMu.Lock()
		cached := sess.flasher
		portsMu.Unlock()

		var probeErr error
		if cached != nil {
			// Probe the cached flasher's connection before reusing it. If the
			// board reset/re-enumerated since it was cached, the handle is
			// dead and Reset()/register ops would silently no-op (BR-57).
			// FlashID() is a cheap, side-effect-free round trip (SPI flash ID
			// read) that requires live communication with the bootloader stub
			// without resetting the device.
			_, _, probeErr = cached.FlashID()
		}

		if cached != nil {
			// Claim the cached flasher for this call (whether we're about to
			// reuse or discard it). Re-verify sess.flasher is still the same
			// pointer — another goroutine may have swapped it in while we
			// were probing unlocked.
			portsMu.Lock()
			if sess.flasher == cached {
				sess.flasher = nil
			} else {
				cached = nil
			}
			portsMu.Unlock()

			if cached != nil && probeErr != nil {
				_ = cached.Close()
				cached = nil
			}
		}

		if cached != nil {
			// Flush stale data from serial buffer and SLIP reader before reuse.
			// ReadFlash's raw block protocol can leave leftover bytes that
			// corrupt subsequent command responses.
			cached.FlushInput()
			// Return borrowed flasher wrapping the cached one
			return &BorrowedFlasher{
				Flasher: cached,
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
		// Wire the connect-status callback before New actually connects.
		// retryFlasherCreate may retry this call several times (USB
		// re-enumeration); opts is a single shared pointer so this only
		// needs setting once here.
		opts.ConnectStatus = connectStatus
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

	// Reset and close cached flasher. The device is in bootloader/stub mode
	// (BorrowedFlasher always caches via onReturn). Reset() returns it to
	// user code. On USB CDC this triggers re-enumeration (1-3s), handled
	// by the WaitForPort below.
	if sess.flasher != nil {
		sess.flasher.Reset()
		_ = sess.flasher.Close()
		sess.flasher = nil
	}
	knownPorts := sess.portsAtAcquire

	// Wait for port availability (USB CDC may need time after close)
	portsMu.Unlock()
	foundPort := WaitForPort(port, 3*time.Second, knownPorts)
	portsMu.Lock()

	// Another goroutine may have acquired the session while we waited
	// (e.g. AcquireForFlasher set ModeFlasher). Don't interfere.
	if sess.mode != ModePending {
		snap := snapshotPorts()
		portsMu.Unlock()
		status.Write(snap)
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
			snap := snapshotPorts()
			portsMu.Unlock()
			status.Write(snap)
			return
		}
	}

	// Could not restart, delete session
	delete(ports, port)
	snap := snapshotPorts()
	portsMu.Unlock()
	status.Write(snap)
}

// ReleaseFlasherDeferred schedules a deferred restart via timer. Used by async handlers.
func ReleaseFlasherDeferred(sess *PortSession, port string) {
	portsMu.Lock()
	defer func() {
		snap := snapshotPorts()
		portsMu.Unlock()
		status.Write(snap)
	}()

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
	knownPorts := sess.portsAtAcquire
	portsMu.Unlock()

	// Wait for port (outside lock)
	foundPort := WaitForPort(port, 3*time.Second, knownPorts)
	if foundPort == "" {
		return ""
	}

	portsMu.Lock()
	defer func() {
		snap := snapshotPorts()
		portsMu.Unlock()
		status.Write(snap)
	}()

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
	// Snapshot ports before taking portsMu — see AcquireForFlasher. Used by
	// ReleaseExternal's re-enum matching to exclude pre-existing ports
	// (BR-58), same protection AcquireForFlasher already has.
	portNames := snapshotPortNames()

	portsMu.Lock()
	defer func() {
		snap := snapshotPorts()
		portsMu.Unlock()
		status.Write(snap)
	}()

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
	sess.portsAtAcquire = portNames
	return sess
}

// ReleaseExternal restarts the reader after an external command. Returns the new port name if re-enumerated.
func ReleaseExternal(sess *PortSession, port string) string {
	portsMu.Lock()
	knownPorts := sess.portsAtAcquire
	portsMu.Unlock()

	foundPort := WaitForPort(port, 3*time.Second, knownPorts)
	if foundPort == "" {
		return ""
	}

	portsMu.Lock()
	defer func() {
		snap := snapshotPorts()
		portsMu.Unlock()
		status.Write(snap)
	}()

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

	port, _ := args["port"].(string)

	// Look up session by port or fall back to single entry
	var sess *PortSession
	var resolvedPort string

	if port != "" {
		var ok bool
		sess, ok = ports[port]
		if !ok {
			snap := snapshotPorts()
			portsMu.Unlock()
			status.Write(snap)
			return nil, port, fmt.Errorf("no serial port open for %s; call serial_start first", port)
		}
		resolvedPort = port
	} else {
		if len(ports) == 0 {
			snap := snapshotPorts()
			portsMu.Unlock()
			status.Write(snap)
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
			snap := snapshotPorts()
			portsMu.Unlock()
			status.Write(snap)
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
		knownPorts := sess.portsAtAcquire
		portsMu.Unlock()
		foundPort := WaitForPort(resolvedPort, 0, knownPorts)
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
		snap := snapshotPorts()
		portsMu.Unlock()
		status.Write(snap)
		return nil, resolvedPort, fmt.Errorf("serial reader for %s has stopped; call serial_start to reconnect", resolvedPort)
	}

	snap := snapshotPorts()
	portsMu.Unlock()
	status.Write(snap)
	return sess.mgr, resolvedPort, nil
}

// StartSession opens a port and begins reading. Takes lock internally.
func StartSession(port string, baud int, bufSize int) error {
	portsMu.Lock()
	defer func() {
		snap := snapshotPorts()
		portsMu.Unlock()
		status.Write(snap)
	}()

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

// teardownSessionLocked cancels sess's pending timer, closes any cached
// flasher, stops its manager, and removes it from the ports map. Caller must
// hold portsMu. Shared by StopSession and RestartSession.
func teardownSessionLocked(sess *PortSession, port string) {
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
}

// StopSession closes a port and removes it from management. Takes lock internally.
func StopSession(port string) error {
	portsMu.Lock()
	defer func() {
		snap := snapshotPorts()
		portsMu.Unlock()
		status.Write(snap)
	}()

	sess, exists := ports[port]
	if !exists {
		return fmt.Errorf("no serial port open for %s", port)
	}

	teardownSessionLocked(sess, port)
	return nil
}

// RestartSession atomically stops (if open) and starts a fresh manager for
// port under a single portsMu acquisition, so a concurrent serial_start/
// serial_stop/serial_restart call on the same port can never interleave in
// an unlocked gap (BR-21 HIGH: the old handleSerialRestart called
// StopSession then StartSession as two separately-locked steps). baud
// defaults to the port's current baud if open, else 115200; baudOverride,
// when non-nil, wins. Always creates a fresh manager + ring buffer (never
// reuses the existing manager, unlike StartSession's same-manager restart
// branch for an already-open port) so a stuck/dead manager can't survive a
// restart. A missing/unknown port behaves like a plain start with no
// partial state left behind. Returns the baud actually used.
func RestartSession(port string, baudOverride *int, bufSize int) (int, error) {
	portsMu.Lock()
	defer func() {
		snap := snapshotPorts()
		portsMu.Unlock()
		status.Write(snap)
	}()

	baud := 115200
	if sess, exists := ports[port]; exists {
		baud = sess.baud
		teardownSessionLocked(sess, port)
	}
	if baudOverride != nil {
		baud = *baudOverride
	}

	sess := &PortSession{
		mgr:  newManagerFunc(bufSize),
		port: port,
		baud: baud,
		mode: ModeReader,
	}
	ports[port] = sess

	return baud, sess.mgr.Start(port, baud)
}

// CleanupAllSessions stops all managed ports. Used by signal handler.
func CleanupAllSessions() {
	portsMu.Lock()
	defer func() {
		snap := snapshotPorts()
		portsMu.Unlock()
		status.Write(snap)
	}()

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

// AllPortStates returns the current state of all open ports for status/MCP consumers.
func AllPortStates() []status.PortState {
	portsMu.Lock()
	defer portsMu.Unlock()
	return snapshotPorts()
}
