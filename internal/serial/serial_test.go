package serial

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.bug.st/serial"
)

// mockPort implements the serial.Port interface for testing.
// Use the readFn field to control what Read() returns.
type mockPort struct {
	mu             sync.Mutex
	readFn         func(p []byte) (n int, err error)
	closeCalled    bool
	setModeErr     error
	setReadTimeout bool
	closeErr       error
}

func (m *mockPort) SetMode(mode *serial.Mode) error {
	if m.setModeErr != nil {
		return m.setModeErr
	}
	return nil
}

func (m *mockPort) SetReadTimeout(t time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.setReadTimeout = true
	return nil
}

func (m *mockPort) Read(p []byte) (n int, err error) {
	m.mu.Lock()
	fn := m.readFn
	m.mu.Unlock()

	if fn == nil {
		return 0, fmt.Errorf("readFn not set")
	}
	return fn(p)
}

func (m *mockPort) Write(p []byte) (n int, err error) {
	return len(p), nil
}

func (m *mockPort) ResetInputBuffer() error {
	return nil
}

func (m *mockPort) ResetOutputBuffer() error {
	return nil
}

func (m *mockPort) SetDTR(dtr bool) error {
	return nil
}

func (m *mockPort) SetRTS(rts bool) error {
	return nil
}

func (m *mockPort) GetModemStatusBits() (*serial.ModemStatusBits, error) {
	return nil, nil
}

func (m *mockPort) SetReadTimeoutEx(t, i time.Duration) error {
	return nil
}

func (m *mockPort) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closeCalled = true
	if m.closeErr != nil {
		return m.closeErr
	}
	return nil
}

func (m *mockPort) Break(t time.Duration) error {
	return nil
}

func (m *mockPort) Drain() error {
	return nil
}

// TestReadLoopContextCancel verifies that when the context is cancelled,
// the readLoop exits gracefully, IsRunning() becomes false, and LastError() is nil.
func TestReadLoopContextCancel(t *testing.T) {
	port := &mockPort{
		readFn: func(p []byte) (n int, err error) {
			time.Sleep(50 * time.Millisecond) // simulate timeout
			return 0, nil
		},
	}

	sm := NewManager()
	sm.port = port
	sm.running = true
	sm.portName = "test-port"
	sm.baud = 115200

	ctx, cancel := context.WithCancel(context.Background())
	go sm.readLoop(ctx, sm.gen)

	// Give the loop time to start
	time.Sleep(50 * time.Millisecond)

	// Cancel context
	cancel()

	// Wait for loop to exit
	time.Sleep(200 * time.Millisecond)

	assert.False(t, sm.IsRunning(), "IsRunning() should be false after context cancel")
	assert.Nil(t, sm.LastError(), "LastError() should be nil after normal context cancel")
}

// TestReadLoopReadError verifies that when the port returns a read error,
// readLoop exits, IsRunning() becomes false, and LastError() contains the error.
func TestReadLoopReadError(t *testing.T) {
	expectedErr := fmt.Errorf("device disconnected")
	port := &mockPort{
		readFn: func(p []byte) (n int, err error) {
			return 0, expectedErr
		},
	}

	sm := NewManager()
	sm.port = port
	sm.running = true
	sm.portName = "test-port"
	sm.baud = 115200

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go sm.readLoop(ctx, sm.gen)

	// Wait for loop to process the error
	time.Sleep(200 * time.Millisecond)

	assert.False(t, sm.IsRunning(), "IsRunning() should be false after read error")
	require.NotNil(t, sm.LastError(), "LastError() should not be nil")
	assert.Contains(t, sm.LastError().Error(), "device disconnected")
}

// TestReadLoopLineAssembly verifies that the readLoop correctly assembles
// lines from chunked input data containing newlines.
func TestReadLoopLineAssembly(t *testing.T) {
	// Sequence: first read returns "hello wo", second returns "rld\nfoo\n", third returns error to exit
	callCount := 0
	port := &mockPort{
		readFn: func(p []byte) (n int, err error) {
			callCount++
			switch callCount {
			case 1:
				data := []byte("hello wo")
				copy(p, data)
				return len(data), nil
			case 2:
				data := []byte("rld\nfoo\n")
				copy(p, data)
				return len(data), nil
			default:
				return 0, fmt.Errorf("exit error")
			}
		},
	}

	sm := NewManager()
	sm.port = port
	sm.running = true
	sm.portName = "test-port"
	sm.baud = 115200

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go sm.readLoop(ctx, sm.gen)

	// Wait for loop to process both chunks
	time.Sleep(200 * time.Millisecond)

	// Cancel to let the defer complete
	cancel()
	time.Sleep(100 * time.Millisecond)

	lines := sm.Read(10)
	require.Equal(t, 2, len(lines), "should have 2 complete lines")
	assert.Equal(t, "hello world", lines[0])
	assert.Equal(t, "foo", lines[1])
}

// TestReadLoopPartialLines verifies that when a line without a newline reaches
// the 4096-byte threshold, it is flushed to the ring buffer.
func TestReadLoopPartialLines(t *testing.T) {
	// First two reads accumulate 4100+ bytes without newline, triggering flush.
	// Then error to exit.
	callCount := 0
	port := &mockPort{
		readFn: func(p []byte) (n int, err error) {
			callCount++
			switch callCount {
			case 1:
				// Return 2048 bytes without newline (first chunk)
				data := make([]byte, 2048)
				for i := range data {
					data[i] = 'x'
				}
				n := copy(p, data)
				return n, nil
			case 2:
				// Return another 2100 bytes without newline (will exceed 4096 threshold)
				data := make([]byte, 2100)
				for i := range data {
					data[i] = 'y'
				}
				n := copy(p, data)
				return n, nil
			default:
				return 0, fmt.Errorf("exit error")
			}
		},
	}

	sm := NewManager()
	sm.port = port
	sm.running = true
	sm.portName = "test-port"
	sm.baud = 115200

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go sm.readLoop(ctx, sm.gen)

	// Wait for loop to process and flush
	time.Sleep(300 * time.Millisecond)

	// Cancel to let the defer complete
	cancel()
	time.Sleep(100 * time.Millisecond)

	lines := sm.Read(10)
	require.Greater(t, len(lines), 0, "should have flushed partial line")
	// The accumulated line should be ~4148 bytes (2048 + 2100)
	assert.Greater(t, len(lines[0]), 4096, "flushed line should exceed 4096 threshold")
}

// TestReadLoopCarriageReturnTrim verifies that carriage returns are trimmed
// from the right side of complete lines.
func TestReadLoopCarriageReturnTrim(t *testing.T) {
	callCount := 0
	port := &mockPort{
		readFn: func(p []byte) (n int, err error) {
			callCount++
			switch callCount {
			case 1:
				// Return line with carriage return before newline
				data := []byte("test line\r\n")
				copy(p, data)
				return len(data), nil
			default:
				return 0, fmt.Errorf("exit error")
			}
		},
	}

	sm := NewManager()
	sm.port = port
	sm.running = true
	sm.portName = "test-port"
	sm.baud = 115200

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go sm.readLoop(ctx, sm.gen)

	// Wait for processing
	time.Sleep(200 * time.Millisecond)
	cancel()
	time.Sleep(100 * time.Millisecond)

	lines := sm.Read(10)
	require.Greater(t, len(lines), 0, "should have captured line")
	assert.Equal(t, "test line", lines[0], "carriage return should be trimmed")
}

// TestNewManagerWithBufferSize verifies that NewManagerWithBufferSize creates
// a Manager with the specified buffer size and tests buffer overflow behavior.
func TestNewManagerWithBufferSize(t *testing.T) {
	sm := NewManagerWithBufferSize(3)
	sm.AddToBuffer("a")
	sm.AddToBuffer("b")
	sm.AddToBuffer("c")
	sm.AddToBuffer("d")
	lines := sm.Read(10)
	require.Len(t, lines, 3)
	assert.Equal(t, []string{"b", "c", "d"}, lines)
}

// TestStartClearsError verifies that calling the actual Start() method
// clears any previous error state.
func TestStartClearsError(t *testing.T) {
	sm := NewManager()

	// Set a previous error
	sm.mu.Lock()
	sm.err = fmt.Errorf("previous error")
	sm.mu.Unlock()

	require.NotNil(t, sm.LastError(), "should have error before start")

	// Inject mock port opener
	sm.OpenFunc = func(name string, mode *serial.Mode) (serial.Port, error) {
		return &mockPort{
			readFn: func(p []byte) (n int, err error) {
				time.Sleep(50 * time.Millisecond)
				return 0, nil
			},
		}, nil
	}

	err := sm.Start("test-port", 115200)
	require.NoError(t, err, "Start() should succeed")
	defer func() { _ = sm.Stop() }()

	assert.True(t, sm.IsRunning(), "should be running after Start()")
	assert.Nil(t, sm.LastError(), "error should be cleared after Start()")
}

// TestRingBufferBasicOperations verifies the RingBuffer Add and Last methods.
func TestRingBufferBasicOperations(t *testing.T) {
	rb := NewRingBuffer(3)

	rb.Add("line1")
	rb.Add("line2")
	rb.Add("line3")

	lines := rb.Last(3)
	assert.Equal(t, 3, len(lines))
	assert.Equal(t, "line1", lines[0])
	assert.Equal(t, "line2", lines[1])
	assert.Equal(t, "line3", lines[2])
}

// TestRingBufferWraparound verifies the RingBuffer wraps around correctly
// when more items are added than the buffer size.
func TestRingBufferWraparound(t *testing.T) {
	rb := NewRingBuffer(3)

	rb.Add("line1")
	rb.Add("line2")
	rb.Add("line3")
	rb.Add("line4") // wraps around

	// Should only have the last 3 items
	lines := rb.Last(3)
	assert.Equal(t, 3, len(lines))
	assert.Equal(t, "line2", lines[0])
	assert.Equal(t, "line3", lines[1])
	assert.Equal(t, "line4", lines[2])
}

// TestRingBufferPartialRead verifies we can request fewer lines than available.
func TestRingBufferPartialRead(t *testing.T) {
	rb := NewRingBuffer(5)

	rb.Add("line1")
	rb.Add("line2")
	rb.Add("line3")

	lines := rb.Last(2)
	assert.Equal(t, 2, len(lines))
	assert.Equal(t, "line2", lines[0])
	assert.Equal(t, "line3", lines[1])
}

// TestRingBufferClear verifies the Clear method resets the buffer.
func TestRingBufferClear(t *testing.T) {
	rb := NewRingBuffer(3)

	rb.Add("line1")
	rb.Add("line2")

	rb.Clear()

	lines := rb.Last(10)
	assert.Equal(t, 0, len(lines))
}

// TestRingBufferEmptyRead verifies reading from empty buffer returns empty slice.
func TestRingBufferEmptyRead(t *testing.T) {
	rb := NewRingBuffer(3)

	lines := rb.Last(10)
	assert.Equal(t, 0, len(lines))
}

// TestSerialManagerIsRunning verifies the IsRunning() method returns correct state.
func TestSerialManagerIsRunning(t *testing.T) {
	sm := NewManager()

	assert.False(t, sm.IsRunning(), "should not be running initially")

	sm.mu.Lock()
	sm.running = true
	sm.mu.Unlock()

	assert.True(t, sm.IsRunning(), "should be running after setting running=true")
}

// TestSerialManagerPortName verifies the PortName() method returns correct value.
func TestSerialManagerPortName(t *testing.T) {
	sm := NewManager()

	portName := sm.PortName()
	assert.Equal(t, "", portName, "should be empty initially")

	sm.mu.Lock()
	sm.portName = "COM3"
	sm.mu.Unlock()

	portName = sm.PortName()
	assert.Equal(t, "COM3", portName)
}

// TestSerialManagerBaud verifies the Baud() method returns correct value.
func TestSerialManagerBaud(t *testing.T) {
	sm := NewManager()

	baud := sm.Baud()
	assert.Equal(t, 0, baud, "should be 0 initially")

	sm.mu.Lock()
	sm.baud = 115200
	sm.mu.Unlock()

	baud = sm.Baud()
	assert.Equal(t, 115200, baud)
}

// TestReadLoopTimeoutHandling verifies that timeouts (0, nil returns)
// are handled gracefully without storing errors.
func TestReadLoopTimeoutHandling(t *testing.T) {
	port := &mockPort{
		readFn: func(p []byte) (n int, err error) {
			return 0, nil // timeout, no data
		},
	}

	sm := NewManager()
	sm.port = port
	sm.running = true
	sm.portName = "test-port"
	sm.baud = 115200

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	go sm.readLoop(ctx, sm.gen)

	// Wait for timeout
	<-ctx.Done()
	time.Sleep(100 * time.Millisecond)

	assert.False(t, sm.IsRunning(), "should be stopped after context timeout")
	assert.Nil(t, sm.LastError(), "timeout should not produce an error")
}

// TestBufferCount verifies the Count() method of RingBuffer.
func TestBufferCount(t *testing.T) {
	rb := NewRingBuffer(5)
	assert.Equal(t, 0, rb.Count())

	rb.Add("line1")
	rb.Add("line2")
	assert.Equal(t, 2, rb.Count())

	rb.Add("line3")
	rb.Add("line4")
	rb.Add("line5")
	rb.Add("line6") // wraps
	assert.Equal(t, 5, rb.Count())
}

func TestWriteRequiresRunningPort(t *testing.T) {
	sm := NewManager()
	_, err := sm.Write([]byte("hello"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not running")
}

func TestWriteToPort(t *testing.T) {
	sm := NewManager()
	sm.OpenFunc = func(name string, mode *serial.Mode) (serial.Port, error) {
		return &mockPort{
			readFn: func(p []byte) (n int, err error) {
				time.Sleep(50 * time.Millisecond)
				return 0, nil
			},
		}, nil
	}

	err := sm.Start("test-port", 115200)
	require.NoError(t, err)
	defer func() { _ = sm.Stop() }()

	n, err := sm.Write([]byte("test\n"))
	require.NoError(t, err)
	assert.Equal(t, 5, n)
}

// TestReadLoopErrorClosesPort verifies that when readLoop encounters a read
// error, it closes the port and nils sm.port.
func TestReadLoopErrorClosesPort(t *testing.T) {
	port := &mockPort{
		readFn: func(p []byte) (n int, err error) {
			return 0, fmt.Errorf("device removed")
		},
	}

	sm := NewManager()
	sm.port = port
	sm.running = true
	sm.portName = "test-port"
	sm.baud = 115200

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go sm.readLoop(ctx, sm.gen)
	time.Sleep(200 * time.Millisecond)

	assert.False(t, sm.IsRunning())

	port.mu.Lock()
	assert.True(t, port.closeCalled)
	port.mu.Unlock()

	sm.mu.Lock()
	assert.Nil(t, sm.port)
	sm.mu.Unlock()
}

// TestIntegrationStartReadStop verifies the full Start→readLoop→Read→Stop cycle.
// Uses mockPort injection and sync.Once to deliver data on first read only.
func TestIntegrationStartReadStop(t *testing.T) {
	sm := NewManager()

	// Inject mock port opener with sync.Once pattern
	var once sync.Once
	sm.OpenFunc = func(name string, mode *serial.Mode) (serial.Port, error) {
		return &mockPort{
			readFn: func(p []byte) (n int, err error) {
				var dataLen int
				once.Do(func() {
					data := []byte("hello\nworld\n")
					dataLen = copy(p, data)
				})
				return dataLen, nil
			},
		}, nil
	}

	// Start the manager
	err := sm.Start("test-port", 115200)
	require.NoError(t, err, "Start() should succeed")

	// Sleep to allow readLoop to process data
	time.Sleep(200 * time.Millisecond)

	// Verify running state and properties
	assert.True(t, sm.IsRunning(), "IsRunning() should be true")
	assert.Equal(t, "test-port", sm.PortName(), "PortName() should return 'test-port'")
	assert.Equal(t, 115200, sm.Baud(), "Baud() should return 115200")

	// Read lines
	lines := sm.Read(10)
	require.Len(t, lines, 2, "should have 2 lines: 'hello' and 'world'")
	assert.Equal(t, "hello", lines[0])
	assert.Equal(t, "world", lines[1])

	// Stop the manager
	err = sm.Stop()
	require.NoError(t, err, "Stop() should succeed")

	// Verify stopped state
	assert.False(t, sm.IsRunning(), "IsRunning() should be false after Stop()")
	assert.Nil(t, sm.LastError(), "LastError() should be nil after clean shutdown")
}

// TestIntegrationReadAndClear verifies that ReadAndClear returns lines and clears the buffer.
func TestIntegrationReadAndClear(t *testing.T) {
	sm := NewManager()

	var once sync.Once
	sm.OpenFunc = func(name string, mode *serial.Mode) (serial.Port, error) {
		return &mockPort{
			readFn: func(p []byte) (n int, err error) {
				var dataLen int
				once.Do(func() {
					data := []byte("line1\nline2\nline3\n")
					dataLen = copy(p, data)
				})
				return dataLen, nil
			},
		}, nil
	}

	err := sm.Start("test-port", 115200)
	require.NoError(t, err)
	defer func() { _ = sm.Stop() }()

	time.Sleep(200 * time.Millisecond)

	// ReadAndClear should return all lines and clear the buffer
	lines := sm.ReadAndClear(10)
	require.Len(t, lines, 3, "should return 3 lines")
	assert.Equal(t, []string{"line1", "line2", "line3"}, lines)

	// Second Read should return empty since buffer was cleared
	lines = sm.Read(10)
	assert.Len(t, lines, 0, "buffer should be empty after ReadAndClear()")
}

// TestIntegrationBufferCount verifies that BufferCount returns the number of buffered lines.
func TestIntegrationBufferCount(t *testing.T) {
	sm := NewManager()

	var once sync.Once
	sm.OpenFunc = func(name string, mode *serial.Mode) (serial.Port, error) {
		return &mockPort{
			readFn: func(p []byte) (n int, err error) {
				var dataLen int
				once.Do(func() {
					data := []byte("a\nb\nc\n")
					dataLen = copy(p, data)
				})
				return dataLen, nil
			},
		}, nil
	}

	err := sm.Start("test-port", 115200)
	require.NoError(t, err)
	defer func() { _ = sm.Stop() }()

	time.Sleep(200 * time.Millisecond)

	count := sm.BufferCount()
	assert.Equal(t, 3, count, "BufferCount() should return 3")
}

// TestIntegrationStartStopsExisting verifies that calling Start() on an already-running
// manager stops the existing port and starts the new one.
func TestIntegrationStartStopsExisting(t *testing.T) {
	sm := NewManager()

	// First port
	var once1 sync.Once
	openPortA := func(name string, mode *serial.Mode) (serial.Port, error) {
		return &mockPort{
			readFn: func(p []byte) (n int, err error) {
				var dataLen int
				once1.Do(func() {
					data := []byte("port-a-data\n")
					dataLen = copy(p, data)
				})
				return dataLen, nil
			},
		}, nil
	}

	// Start on port-a
	sm.OpenFunc = openPortA
	err := sm.Start("port-a", 115200)
	require.NoError(t, err)

	time.Sleep(200 * time.Millisecond)
	assert.True(t, sm.IsRunning())
	assert.Equal(t, "port-a", sm.PortName())

	// Second port
	var once2 sync.Once
	openPortB := func(name string, mode *serial.Mode) (serial.Port, error) {
		return &mockPort{
			readFn: func(p []byte) (n int, err error) {
				var dataLen int
				once2.Do(func() {
					data := []byte("port-b-data\n")
					dataLen = copy(p, data)
				})
				return dataLen, nil
			},
		}, nil
	}

	// Start on port-b (should stop port-a first)
	sm.OpenFunc = openPortB
	err = sm.Start("port-b", 115200)
	require.NoError(t, err)

	// Wait for port-b to be established (port name changes AND running is true)
	for i := 0; i < 100; i++ {
		if sm.PortName() == "port-b" && sm.IsRunning() {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	assert.True(t, sm.IsRunning())
	assert.Equal(t, "port-b", sm.PortName())

	// Clean up
	_ = sm.Stop()
}

// TestIsTransientSerialError verifies that isTransientSerialError correctly
// identifies transient errors that should trigger a reconnect attempt.
func TestIsTransientSerialError(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		expect bool
	}{
		{"device not configured", fmt.Errorf("device not configured"), true},
		{"bad file descriptor", fmt.Errorf("bad file descriptor"), true},
		{"wrapped device not configured", fmt.Errorf("serial read error: device not configured"), true},
		{"wrapped bad file descriptor", fmt.Errorf("read: bad file descriptor"), true},
		{"device removed", fmt.Errorf("device removed"), false},
		{"connection reset", fmt.Errorf("connection reset"), false},
		{"generic error", fmt.Errorf("some other error"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expect, isTransientSerialError(tt.err))
		})
	}
}

// TestReadLoopReconnectSuccess verifies that readLoop encounters a transient error,
// reconnect succeeds, and loop continues reading.
func TestReadLoopReconnectSuccess(t *testing.T) {
	orig := reconnectDelays
	reconnectDelays = []time.Duration{time.Millisecond, time.Millisecond}
	defer func() { reconnectDelays = orig }()

	sm := NewManager()

	var openCount int32
	sm.OpenFunc = func(name string, mode *serial.Mode) (serial.Port, error) {
		count := atomic.AddInt32(&openCount, 1)
		if count == 1 {
			// First open — return port that will error with transient error
			var once sync.Once
			return &mockPort{
				readFn: func(p []byte) (n int, err error) {
					var errOut error
					once.Do(func() {
						errOut = fmt.Errorf("device not configured")
					})
					if errOut != nil {
						return 0, errOut
					}
					time.Sleep(50 * time.Millisecond)
					return 0, nil
				},
			}, nil
		}
		// Reconnect — return a working port with data
		var dataOnce sync.Once
		return &mockPort{
			readFn: func(p []byte) (n int, err error) {
				var dataLen int
				dataOnce.Do(func() {
					data := []byte("reconnected\n")
					dataLen = copy(p, data)
				})
				if dataLen > 0 {
					return dataLen, nil
				}
				time.Sleep(50 * time.Millisecond)
				return 0, nil
			},
		}, nil
	}

	err := sm.Start("test-port", 115200)
	require.NoError(t, err)
	defer func() { _ = sm.Stop() }()

	time.Sleep(500 * time.Millisecond)

	assert.True(t, sm.IsRunning())
	lines := sm.Read(10)
	assert.Contains(t, lines, "reconnected")
}

// TestReadLoopReconnectExhausted verifies that when all reconnect attempts fail,
// readLoop exits with error.
func TestReadLoopReconnectExhausted(t *testing.T) {
	orig := reconnectDelays
	reconnectDelays = []time.Duration{time.Millisecond, time.Millisecond}
	defer func() { reconnectDelays = orig }()

	port := &mockPort{
		readFn: func(p []byte) (n int, err error) {
			return 0, fmt.Errorf("device not configured")
		},
	}

	sm := NewManager()
	sm.OpenFunc = func(name string, mode *serial.Mode) (serial.Port, error) {
		return nil, fmt.Errorf("port not found")
	}
	sm.port = port
	sm.running = true
	sm.portName = "test-port"
	sm.baud = 115200

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go sm.readLoop(ctx, sm.gen)
	time.Sleep(500 * time.Millisecond)

	assert.False(t, sm.IsRunning())
	require.NotNil(t, sm.LastError())
	assert.Contains(t, sm.LastError().Error(), "device not configured")
}

// TestReadLoopReconnectContextCancel verifies that when context is cancelled
// during reconnect, loop exits cleanly.
func TestReadLoopReconnectContextCancel(t *testing.T) {
	orig := reconnectDelays
	reconnectDelays = []time.Duration{100 * time.Millisecond, 100 * time.Millisecond, 100 * time.Millisecond, 100 * time.Millisecond, 100 * time.Millisecond}
	defer func() { reconnectDelays = orig }()

	port := &mockPort{
		readFn: func(p []byte) (n int, err error) {
			return 0, fmt.Errorf("bad file descriptor")
		},
	}

	sm := NewManager()
	sm.OpenFunc = func(name string, mode *serial.Mode) (serial.Port, error) {
		return nil, fmt.Errorf("port not found")
	}
	sm.port = port
	sm.running = true
	sm.portName = "test-port"
	sm.baud = 115200

	ctx, cancel := context.WithCancel(context.Background())

	go sm.readLoop(ctx, sm.gen)
	time.Sleep(50 * time.Millisecond)
	cancel()
	time.Sleep(300 * time.Millisecond)

	assert.False(t, sm.IsRunning())
}

// TestReconnectClearsBuffer verifies that after a successful reconnect,
// the ring buffer is empty.
func TestReconnectClearsBuffer(t *testing.T) {
	orig := reconnectDelays
	reconnectDelays = []time.Duration{time.Millisecond, time.Millisecond}
	defer func() { reconnectDelays = orig }()

	sm := NewManagerWithBufferSize(10)

	// Add some initial lines to the buffer
	sm.AddToBuffer("line1")
	sm.AddToBuffer("line2")
	sm.AddToBuffer("line3")

	require.Equal(t, 3, sm.BufferCount(), "buffer should have 3 lines initially")

	// Mock OpenFunc that first fails with transient error, then succeeds
	var openCount int32
	sm.OpenFunc = func(name string, mode *serial.Mode) (serial.Port, error) {
		count := atomic.AddInt32(&openCount, 1)
		if count == 1 {
			// First open — return port that will error with transient error
			var once sync.Once
			return &mockPort{
				readFn: func(p []byte) (n int, err error) {
					var errOut error
					once.Do(func() {
						errOut = fmt.Errorf("device not configured")
					})
					if errOut != nil {
						return 0, errOut
					}
					time.Sleep(50 * time.Millisecond)
					return 0, nil
				},
			}, nil
		}
		// Reconnect — return a working port
		return &mockPort{
			readFn: func(p []byte) (n int, err error) {
				time.Sleep(50 * time.Millisecond)
				return 0, nil
			},
		}, nil
	}

	err := sm.Start("test-port", 115200)
	require.NoError(t, err)
	defer func() { _ = sm.Stop() }()

	// Wait for reconnect to occur
	time.Sleep(500 * time.Millisecond)

	// After reconnect, buffer should be cleared
	assert.Equal(t, 0, sm.BufferCount(), "buffer should be empty after reconnect")
}

// TestReconnectingStateVisibility verifies that IsReconnecting() transitions
// correctly during reconnect.
func TestReconnectingStateVisibility(t *testing.T) {
	orig := reconnectDelays
	reconnectDelays = []time.Duration{100 * time.Millisecond, 100 * time.Millisecond}
	defer func() { reconnectDelays = orig }()

	sm := NewManager()

	var openCount int32
	reconnectStarted := make(chan struct{})
	sm.OpenFunc = func(name string, mode *serial.Mode) (serial.Port, error) {
		count := atomic.AddInt32(&openCount, 1)
		if count == 1 {
			// First open — signal and return port that errors
			return &mockPort{
				readFn: func(p []byte) (n int, err error) {
					once := &sync.Once{}
					var errOut error
					once.Do(func() {
						close(reconnectStarted)
						errOut = fmt.Errorf("bad file descriptor")
					})
					if errOut != nil {
						return 0, errOut
					}
					time.Sleep(50 * time.Millisecond)
					return 0, nil
				},
			}, nil
		}
		// Reconnect succeeds
		return &mockPort{
			readFn: func(p []byte) (n int, err error) {
				time.Sleep(50 * time.Millisecond)
				return 0, nil
			},
		}, nil
	}

	err := sm.Start("test-port", 115200)
	require.NoError(t, err)
	defer func() { _ = sm.Stop() }()

	// Initially not reconnecting
	assert.False(t, sm.IsReconnecting(), "should not be reconnecting initially")

	// Wait for reconnect to start
	<-reconnectStarted
	time.Sleep(10 * time.Millisecond)

	// Should be reconnecting during the reconnect process
	assert.True(t, sm.IsReconnecting(), "should be reconnecting after transient error")

	// Wait for reconnect to complete
	time.Sleep(300 * time.Millisecond)

	// After successful reconnect, should not be reconnecting
	assert.False(t, sm.IsReconnecting(), "should not be reconnecting after successful reconnect")
}

// TestPortNamePrefix is a table-driven test for the portNamePrefix function.
func TestPortNamePrefix(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"standard usb dev port", "/dev/cu.usbmodem1101", "/dev/cu.usbmodem"},
		{"standard tty usb port", "/dev/ttyUSB0", "/dev/ttyUSB"},
		{"com port", "COM3", "COM"},
		{"port with no trailing digits", "/dev/cu.debug-console", ""},
		{"empty string", "", ""},
		{"digits only", "12345", ""},
		{"single char with digit", "A0", "A"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := portNamePrefix(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestFindSimilarPort tests finding similar ports with a mock listFn.
func TestFindSimilarPort(t *testing.T) {
	tests := []struct {
		name            string
		originalPort    string
		availablePorts  []PortInfo
		expectedSimilar string
		listFnErr       error
	}{
		{
			name:         "matching port found",
			originalPort: "/dev/cu.usbmodem1101",
			availablePorts: []PortInfo{
				{Name: "/dev/cu.usbmodem2101"},
				{Name: "/dev/cu.usbmodem1101"},
				{Name: "/dev/ttyUSB0"},
			},
			expectedSimilar: "/dev/cu.usbmodem2101",
			listFnErr:       nil,
		},
		{
			name:         "no matching port",
			originalPort: "/dev/cu.usbmodem1101",
			availablePorts: []PortInfo{
				{Name: "/dev/ttyUSB0"},
				{Name: "/dev/ttyUSB1"},
			},
			expectedSimilar: "",
			listFnErr:       nil,
		},
		{
			name:         "only same port available",
			originalPort: "/dev/cu.usbmodem1101",
			availablePorts: []PortInfo{
				{Name: "/dev/cu.usbmodem1101"},
			},
			expectedSimilar: "",
			listFnErr:       nil,
		},
		{
			name:            "port with no trailing digits",
			originalPort:    "/dev/cu.debug-console",
			availablePorts:  []PortInfo{{Name: "/dev/cu.debug-console"}},
			expectedSimilar: "",
			listFnErr:       nil,
		},
		{
			name:         "listFn error",
			originalPort: "/dev/cu.usbmodem1101",
			availablePorts: []PortInfo{
				{Name: "/dev/cu.usbmodem2101"},
			},
			expectedSimilar: "",
			listFnErr:       fmt.Errorf("enumeration failed"),
		},
		{
			name:         "multiple matching ports returns first",
			originalPort: "COM3",
			availablePorts: []PortInfo{
				{Name: "COM1"},
				{Name: "COM2"},
				{Name: "COM3"},
				{Name: "COM4"},
			},
			expectedSimilar: "COM1",
			listFnErr:       nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockListFn := func(usbOnly bool) ([]PortInfo, error) {
				return tt.availablePorts, tt.listFnErr
			}
			result := FindSimilarPort(tt.originalPort, mockListFn)
			assert.Equal(t, tt.expectedSimilar, result)
		})
	}
}

// TestSetPortName verifies that SetPortName updates the port name correctly.
func TestSetPortName(t *testing.T) {
	sm := NewManager()

	// Initially empty
	assert.Equal(t, "", sm.PortName())

	// Set new port name
	sm.SetPortName("new-port-name")
	assert.Equal(t, "new-port-name", sm.PortName())

	// Change it again
	sm.SetPortName("/dev/cu.usbmodem1101")
	assert.Equal(t, "/dev/cu.usbmodem1101", sm.PortName())

	// Set to empty string
	sm.SetPortName("")
	assert.Equal(t, "", sm.PortName())
}

// TestStopBumpsGen verifies that Stop() increments the generation counter
// to invalidate in-flight reconnect goroutines.
func TestStopBumpsGen(t *testing.T) {
	port := &mockPort{
		readFn: func(p []byte) (n int, err error) {
			time.Sleep(50 * time.Millisecond)
			return 0, nil
		},
	}

	sm := NewManager()
	sm.port = port
	sm.running = true
	sm.portName = "test"
	sm.baud = 115200

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go sm.readLoop(ctx, sm.gen)

	genBefore := sm.gen
	_ = sm.Stop()

	assert.Equal(t, genBefore+1, sm.gen, "Stop() should increment gen")
}

// TestStopClearsReconnecting verifies that Stop() clears the reconnecting flag.
func TestStopClearsReconnecting(t *testing.T) {
	sm := NewManager()
	port := &mockPort{
		readFn: func(p []byte) (n int, err error) {
			return 0, nil
		},
	}

	sm.running = true
	sm.reconnecting = true
	sm.port = port

	_, cancel := context.WithCancel(context.Background())
	sm.cancel = cancel

	_ = sm.Stop()

	assert.False(t, sm.IsReconnecting(), "Stop() should clear reconnecting flag")
}

// TestReconnectExitsOnGenMismatch verifies that reconnect() exits early
// and returns false when the generation counter has changed.
func TestReconnectExitsOnGenMismatch(t *testing.T) {
	sm := NewManager()
	sm.portName = "test"
	sm.baud = 115200

	openCounter := atomic.Int32{}
	sm.OpenFunc = func(portName string, mode *serial.Mode) (serial.Port, error) {
		openCounter.Add(1)
		return &mockPort{
			readFn: func(p []byte) (n int, err error) {
				time.Sleep(50 * time.Millisecond)
				return 0, nil
			},
		}, nil
	}

	sm.gen = 5

	result := sm.reconnect(context.Background(), 1)

	assert.False(t, result, "reconnect() should return false on gen mismatch")
	assert.Equal(t, int32(0), openCounter.Load(), "Open() should not be called when gen doesn't match at line 209")
}

// TestStopWaitsForReadLoop verifies that Stop() waits for the readLoop goroutine
// to exit. The readLoop should exit quickly after context cancellation and port close.
func TestStopWaitsForReadLoop(t *testing.T) {
	sm := NewManager()
	sm.OpenFunc = func(portName string, mode *serial.Mode) (serial.Port, error) {
		return &mockPort{
			readFn: func(p []byte) (n int, err error) {
				time.Sleep(200 * time.Millisecond)
				return 0, nil
			},
		}, nil
	}

	err := sm.Start("test", 115200)
	require.NoError(t, err, "Start() should succeed")

	time.Sleep(50 * time.Millisecond) // Let readLoop start

	startTime := time.Now()
	err = sm.Stop()
	elapsed := time.Since(startTime)

	require.NoError(t, err, "Stop() should succeed")
	assert.Less(t, elapsed, 1*time.Second, "Stop() should return quickly after readLoop exits")
	assert.False(t, sm.IsRunning(), "IsRunning() should be false after Stop()")
}

// TestStopDoneChannelClosed verifies that Stop() closes the done channel
// when it exits, allowing waiters to detect the event.
func TestStopDoneChannelClosed(t *testing.T) {
	sm := NewManager()
	sm.OpenFunc = func(portName string, mode *serial.Mode) (serial.Port, error) {
		return &mockPort{
			readFn: func(p []byte) (n int, err error) {
				time.Sleep(50 * time.Millisecond)
				return 0, nil
			},
		}, nil
	}

	err := sm.Start("test", 115200)
	require.NoError(t, err, "Start() should succeed")

	time.Sleep(50 * time.Millisecond) // Let readLoop start

	sm.mu.Lock()
	done := sm.done
	sm.mu.Unlock()

	err = sm.Stop()
	require.NoError(t, err, "Stop() should succeed")

	// Try to receive from done channel with timeout
	select {
	case <-done:
		// Channel was closed successfully
	case <-time.After(1 * time.Second):
		t.Fatal("done channel should be closed within 1 second")
	}
}

// TestReadLoopNilPortSafe verifies that readLoop exits cleanly when Stop() is called
// concurrently while the read loop is executing. This tests the nil-safety of port access.
func TestReadLoopNilPortSafe(t *testing.T) {
	blockingRead := &atomic.Bool{}
	blockingRead.Store(true)

	port := &mockPort{
		readFn: func(p []byte) (n int, err error) {
			// Block briefly to simulate slow read
			for blockingRead.Load() {
				time.Sleep(5 * time.Millisecond)
			}
			return 0, nil // timeout, no data
		},
	}

	sm := NewManager()
	sm.OpenFunc = func(name string, mode *serial.Mode) (serial.Port, error) {
		return port, nil
	}

	err := sm.Start("test", 115200)
	require.NoError(t, err, "Start() should succeed")

	// Let readLoop enter its loop and block on Read
	time.Sleep(50 * time.Millisecond)

	// Unblock the read and immediately call Stop
	blockingRead.Store(false)
	err = sm.Stop()
	require.NoError(t, err, "Stop() should succeed")

	// Verify the manager is not running
	assert.False(t, sm.IsRunning(), "manager should not be running after Stop()")

	// No panic should have occurred; if we got here, the test passes
	assert.True(t, true, "readLoop handled nil port safely")
}
