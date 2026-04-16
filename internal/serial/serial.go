package serial

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"go.bug.st/serial"
)

var reconnectDelays = []time.Duration{
	100 * time.Millisecond,
	200 * time.Millisecond,
	400 * time.Millisecond,
	800 * time.Millisecond,
	1600 * time.Millisecond,
}

type PortInfo struct {
	Name         string `json:"name"`
	IsUSB        bool   `json:"is_usb"`
	VID          string `json:"vid,omitempty"`
	PID          string `json:"pid,omitempty"`
	SerialNumber string `json:"serial_number,omitempty"`
	Product      string `json:"product,omitempty"`
}

type RingBuffer struct {
	lines []string
	size  int
	head  int
	count int
	mu    sync.Mutex
}

func NewRingBuffer(size int) *RingBuffer {
	if size <= 0 {
		size = 1000
	}
	return &RingBuffer{
		lines: make([]string, size),
		size:  size,
		head:  0,
		count: 0,
	}
}

func (rb *RingBuffer) Add(line string) {
	rb.mu.Lock()
	defer rb.mu.Unlock()

	rb.lines[rb.head] = line
	rb.head = (rb.head + 1) % rb.size

	if rb.count < rb.size {
		rb.count++
	}
}

func (rb *RingBuffer) Last(n int) []string {
	rb.mu.Lock()
	defer rb.mu.Unlock()

	if n > rb.count {
		n = rb.count
	}
	if n == 0 {
		return []string{}
	}

	result := make([]string, n)
	for i := 0; i < n; i++ {
		idx := (rb.head - n + i + rb.size) % rb.size
		result[i] = rb.lines[idx]
	}
	return result
}

func (rb *RingBuffer) Clear() {
	rb.mu.Lock()
	defer rb.mu.Unlock()

	rb.lines = make([]string, rb.size)
	rb.head = 0
	rb.count = 0
}

func (rb *RingBuffer) Count() int {
	rb.mu.Lock()
	defer rb.mu.Unlock()
	return rb.count
}

type Manager struct {
	mu           sync.Mutex
	port         serial.Port
	portName     string
	baud         int
	buf          *RingBuffer
	cancel       context.CancelFunc
	done         chan struct{}
	running      bool
	reconnecting bool
	gen          uint64
	err          error
	OpenFunc     func(string, *serial.Mode) (serial.Port, error)
}

func NewManager() *Manager {
	return NewManagerWithBufferSize(1000)
}

func NewManagerWithBufferSize(size int) *Manager {
	return &Manager{
		buf:      NewRingBuffer(size),
		OpenFunc: serial.Open,
	}
}

func (sm *Manager) Start(portName string, baud int) error {
	sm.mu.Lock()
	if sm.running {
		sm.mu.Unlock()
		_ = sm.Stop()
		sm.mu.Lock()
	}
	defer sm.mu.Unlock()

	mode := &serial.Mode{
		BaudRate: baud,
	}

	p, err := sm.OpenFunc(portName, mode)
	if err != nil {
		return fmt.Errorf("failed to open port %s: %w", portName, err)
	}

	if err := p.SetReadTimeout(100 * time.Millisecond); err != nil {
		_ = p.Close()
		return fmt.Errorf("failed to set read timeout: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	sm.port = p
	sm.portName = portName
	sm.baud = baud
	sm.cancel = cancel
	sm.running = true
	sm.gen++
	sm.err = nil

	sm.done = make(chan struct{})
	go sm.readLoop(ctx, sm.gen)

	return nil
}

func (sm *Manager) Stop() error {
	sm.mu.Lock()

	if !sm.running {
		sm.mu.Unlock()
		return nil
	}

	if sm.cancel != nil {
		sm.cancel()
	}

	var err error
	if sm.port != nil {
		err = sm.port.Close()
	}

	sm.gen++
	sm.reconnecting = false
	sm.running = false
	sm.port = nil
	sm.cancel = nil
	done := sm.done
	sm.mu.Unlock()

	if done != nil {
		select {
		case <-done:
		case <-time.After(5 * time.Second):
		}
	}

	return err
}

func (sm *Manager) reconnect(ctx context.Context, myGen uint64) bool {
	sm.mu.Lock()
	if sm.gen != myGen {
		sm.mu.Unlock()
		return false
	}
	if sm.port != nil {
		_ = sm.port.Close()
		sm.port = nil
	}
	sm.reconnecting = true
	sm.mu.Unlock()

	success := false
	defer func() {
		if !success {
			sm.mu.Lock()
			if sm.gen == myGen {
				sm.reconnecting = false
			}
			sm.mu.Unlock()
		}
	}()

	for _, delay := range reconnectDelays {
		select {
		case <-ctx.Done():
			return false
		default:
		}
		time.Sleep(delay)
		select {
		case <-ctx.Done():
			return false
		default:
		}

		sm.mu.Lock()
		if sm.gen != myGen {
			sm.mu.Unlock()
			return false
		}
		portName := sm.portName
		baud := sm.baud
		sm.mu.Unlock()

		mode := &serial.Mode{BaudRate: baud}
		p, err := sm.OpenFunc(portName, mode)
		if err != nil {
			continue
		}
		if err := p.SetReadTimeout(100 * time.Millisecond); err != nil {
			_ = p.Close()
			continue
		}

		sm.mu.Lock()
		if sm.gen != myGen {
			sm.mu.Unlock()
			_ = p.Close()
			return false
		}
		sm.port = p
		sm.err = nil
		sm.reconnecting = false
		success = true
		sm.mu.Unlock()
		sm.buf.Clear()
		return true
	}
	return false
}

func (sm *Manager) Read(lines int) []string {
	return sm.buf.Last(lines)
}

func (sm *Manager) ReadAndClear(lines int) []string {
	result := sm.buf.Last(lines)
	sm.buf.Clear()
	return result
}

func (sm *Manager) IsRunning() bool {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	return sm.running
}

func (sm *Manager) IsReconnecting() bool {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	return sm.reconnecting
}

func (sm *Manager) PortName() string {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	return sm.portName
}

func (sm *Manager) Baud() int {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	return sm.baud
}

func (sm *Manager) LastError() error {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	return sm.err
}

func (sm *Manager) BufferCount() int {
	return sm.buf.Count()
}

func (sm *Manager) ClearBuffer() {
	sm.buf.Clear()
}

func (sm *Manager) Write(data []byte) (int, error) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if !sm.running || sm.port == nil {
		return 0, fmt.Errorf("serial port is not running")
	}

	return sm.port.Write(data)
}

func (sm *Manager) AddToBuffer(line string) {
	sm.buf.Add(line)
}

// SetTestState is for testing only - sets internal state for testing handleSerialRead.
func (sm *Manager) SetTestState(running bool, portName string, baud int, err error) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.running = running
	sm.portName = portName
	sm.baud = baud
	sm.err = err
}

// SetPortName updates the manager's stored port name (used after port re-enumeration).
func (sm *Manager) SetPortName(name string) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.portName = name
}

// portNamePrefix strips trailing digits from a port name to get the common prefix.
// e.g. "/dev/cu.usbmodem1101" -> "/dev/cu.usbmodem".
func portNamePrefix(name string) string {
	i := len(name) - 1
	for i >= 0 && name[i] >= '0' && name[i] <= '9' {
		i--
	}
	if i == len(name)-1 {
		return "" // no trailing digits
	}
	return name[:i+1]
}

// FindSimilarPort scans available ports for one matching the same name prefix
// as portName but with a different numeric suffix. Returns the new port name,
// or empty string if none found.
func FindSimilarPort(portName string, listFn func(usbOnly bool) ([]PortInfo, error)) string {
	prefix := portNamePrefix(portName)
	if prefix == "" {
		return ""
	}
	ports, err := listFn(false)
	if err != nil {
		return ""
	}
	for _, p := range ports {
		if p.Name != portName && strings.HasPrefix(p.Name, prefix) {
			return p.Name
		}
	}
	return ""
}

func isTransientSerialError(err error) bool {
	s := err.Error()
	return strings.Contains(s, "device not configured") ||
		strings.Contains(s, "bad file descriptor")
}

func (sm *Manager) readLoop(ctx context.Context, myGen uint64) {
	sm.mu.Lock()
	myDone := sm.done
	sm.mu.Unlock()

	buf := make([]byte, 4096)
	var lineBuf []byte

	defer func() {
		sm.mu.Lock()
		if sm.gen == myGen {
			sm.running = false
		}
		sm.mu.Unlock()
		if myDone != nil {
			close(myDone)
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		sm.mu.Lock()
		p := sm.port
		sm.mu.Unlock()
		if p == nil {
			return
		}
		n, err := p.Read(buf)
		if err != nil {
			if isTransientSerialError(err) {
				if sm.reconnect(ctx, myGen) {
					lineBuf = lineBuf[:0]
					continue
				}
			}
			sm.mu.Lock()
			sm.err = fmt.Errorf("serial read error: %w", err)
			if sm.port != nil {
				_ = sm.port.Close()
				sm.port = nil
			}
			sm.mu.Unlock()
			return
		}

		if n == 0 {
			continue // timeout, no data — normal
		}

		// Append to line buffer and emit complete lines
		lineBuf = append(lineBuf, buf[:n]...)
		for {
			idx := bytes.IndexByte(lineBuf, '\n')
			if idx < 0 {
				break
			}
			line := string(lineBuf[:idx])
			line = strings.TrimRight(line, "\r")
			sm.buf.Add(line)
			lineBuf = lineBuf[idx+1:]
		}

		// Prevent unbounded growth from lines without newlines
		if len(lineBuf) > 4096 {
			sm.buf.Add(string(lineBuf))
			lineBuf = lineBuf[:0]
		}
	}
}
