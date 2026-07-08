package mcpserver

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"dangernoodle.io/pogopin/internal/esp"
	"dangernoodle.io/pogopin/internal/flash"
	"dangernoodle.io/pogopin/internal/serial"
	"dangernoodle.io/pogopin/internal/session"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	goSerial "go.bug.st/serial"
	espflasher "tinygo.org/x/espflasher/pkg/espflasher"
)

func TestReadAfterDisconnect(t *testing.T) {
	setupTestPorts(t)

	testMgr := serial.NewManager()
	testMgr.AddToBuffer("line before disconnect")
	testMgr.AddToBuffer("second line")
	testMgr.SetTestState(false, "test-port", 115200, fmt.Errorf("device removed"))
	session.InsertPort("test-port", session.NewPortSession(testMgr, "test-port", 115200, session.ModeReader))

	req := mcp.CallToolRequest{}
	result, err := handleSerialRead(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, result)

	require.Len(t, result.Content, 1)
	tc, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	text := tc.Text
	assert.Contains(t, text, "line before disconnect")
	assert.Contains(t, text, "second line")
	assert.Contains(t, text, "[serial reader stopped: device removed]")
}

func TestSerialReadPatternFilter(t *testing.T) {
	setupTestPorts(t)

	testMgr := serial.NewManager()
	testMgr.AddToBuffer("INFO: starting up")
	testMgr.AddToBuffer("DEBUG: verbose stuff")
	testMgr.AddToBuffer("INFO: ready")
	testMgr.AddToBuffer("ERROR: something broke")
	testMgr.SetTestState(true, "test-port", 115200, nil)
	session.InsertPort("test-port", session.NewPortSession(testMgr, "test-port", testMgr.Baud(), session.ModeReader))

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{
		"pattern": "^INFO:",
	}
	result, err := handleSerialRead(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, result)

	tc, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, tc.Text, "INFO: starting up")
	assert.Contains(t, tc.Text, "INFO: ready")
	assert.NotContains(t, tc.Text, "DEBUG:")
	assert.NotContains(t, tc.Text, "ERROR:")
}

func TestSerialReadInvalidPattern(t *testing.T) {
	setupTestPorts(t)

	testMgr := serial.NewManager()
	testMgr.AddToBuffer("some line")
	testMgr.SetTestState(true, "test-port", 115200, nil)
	session.InsertPort("test-port", session.NewPortSession(testMgr, "test-port", testMgr.Baud(), session.ModeReader))

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{
		"pattern": "[invalid",
	}
	result, err := handleSerialRead(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, result)

	tc, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, tc.Text, "invalid pattern")
}

func TestHandleSerialStop(t *testing.T) {
	setupTestPorts(t)

	testMgr := serial.NewManager()
	testMgr.SetTestState(true, "test-port", 115200, nil)
	session.InsertPort("test-port", session.NewPortSession(testMgr, "test-port", testMgr.Baud(), session.ModeReader))

	req := mcp.CallToolRequest{}
	result, err := handleSerialStop(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, result)

	tc, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, tc.Text, "Stopped reading from test-port")
	assert.Equal(t, 0, session.PortCount())
}

func TestHandleSerialStatusSinglePort(t *testing.T) {
	setupTestPorts(t)

	testMgr := serial.NewManager()
	testMgr.SetTestState(true, "test-port", 115200, nil)
	testMgr.AddToBuffer("line 1")
	testMgr.AddToBuffer("line 2")
	testMgr.AddToBuffer("line 3")
	session.InsertPort("test-port", session.NewPortSession(testMgr, "test-port", testMgr.Baud(), session.ModeReader))

	req := mcp.CallToolRequest{}
	result, err := handleSerialStatus(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, result)

	tc, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, tc.Text, "\"running\": true")
	assert.Contains(t, tc.Text, "\"port\": \"test-port\"")
	assert.Contains(t, tc.Text, "\"baud\": 115200")
	assert.Contains(t, tc.Text, "\"buffer_lines\": 3")
	assert.Contains(t, tc.Text, "\"reconnecting\":")
}

func TestHandleSerialStatusMultiplePorts(t *testing.T) {
	setupTestPorts(t)

	testMgrA := serial.NewManager()
	testMgrA.SetTestState(true, "port-a", 9600, nil)
	session.InsertPort("port-a", session.NewPortSession(testMgrA, "port-a", testMgrA.Baud(), session.ModeReader))

	testMgrB := serial.NewManager()
	testMgrB.SetTestState(true, "port-b", 115200, nil)
	session.InsertPort("port-b", session.NewPortSession(testMgrB, "port-b", testMgrB.Baud(), session.ModeReader))

	req := mcp.CallToolRequest{}
	result, err := handleSerialStatus(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, result)

	tc, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, tc.Text, "\"ports\"")
	assert.Contains(t, tc.Text, "\"port-a\"")
	assert.Contains(t, tc.Text, "\"port-b\"")
	assert.Contains(t, tc.Text, "\"reconnecting\":")
}

func TestHandleSerialReadNotRunningNoBuffer(t *testing.T) {
	setupTestPorts(t)

	testMgr := serial.NewManager()
	testMgr.SetTestState(false, "test-port", 115200, nil)
	session.InsertPort("test-port", session.NewPortSession(testMgr, "test-port", testMgr.Baud(), session.ModeReader))

	req := mcp.CallToolRequest{}
	result, err := handleSerialRead(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, result)

	tc, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	// Dead sessions without buffered data are evicted with this error.
	assert.Contains(t, tc.Text, "has stopped")
}

func TestHandleSerialReadNotRunningWithError(t *testing.T) {
	setupTestPorts(t)

	testMgr := serial.NewManager()
	testMgr.SetTestState(false, "test-port", 115200, fmt.Errorf("connection lost"))
	session.InsertPort("test-port", session.NewPortSession(testMgr, "test-port", testMgr.Baud(), session.ModeReader))

	req := mcp.CallToolRequest{}
	result, err := handleSerialRead(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, result)

	tc, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	// Dead sessions without buffered data are evicted with this error.
	assert.Contains(t, tc.Text, "has stopped")
}

func TestHandleSerialWriteNotRunning(t *testing.T) {
	setupTestPorts(t)

	testMgr := serial.NewManager()
	testMgr.SetTestState(false, "test-port", 115200, nil)
	session.InsertPort("test-port", session.NewPortSession(testMgr, "test-port", testMgr.Baud(), session.ModeReader))

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{
		"data": "hello",
	}
	result, err := handleSerialWrite(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, result)

	tc, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	// Dead sessions without buffered data are evicted with this error.
	assert.Contains(t, tc.Text, "has stopped")
}

func TestHandleSerialList(t *testing.T) {
	req := mcp.CallToolRequest{}
	result, err := handleSerialList(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Len(t, result.Content, 1)
}

func TestWithRecover(t *testing.T) {
	panicHandler := func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		panic("test panic")
	}

	wrappedHandler := withRecover(panicHandler)
	result, err := wrappedHandler(context.Background(), mcp.CallToolRequest{})

	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, result.IsError)

	tc, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, tc.Text, "internal error: test panic")
}

func TestHandleSerialStartMissingPort(t *testing.T) {
	setupTestPorts(t)

	req := mcp.CallToolRequest{}
	result, err := handleSerialStart(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, result.IsError)

	tc, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, tc.Text, "port")
}

func TestHandleSerialFlashNoPort(t *testing.T) {
	setupTestPorts(t)

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{
		"command": "echo",
		"args":    []interface{}{"hi"},
	}
	result, err := handleSerialFlash(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, result.IsError)

	tc, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, tc.Text, "no serial port open")
}

func TestHandleSerialFlashMissingCommand(t *testing.T) {
	setupTestPorts(t)

	testMgr := serial.NewManager()
	testMgr.SetTestState(true, "test-port", 115200, nil)
	session.InsertPort("test-port", session.NewPortSession(testMgr, "test-port", testMgr.Baud(), session.ModeReader))

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{}
	result, err := handleSerialFlash(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, result.IsError)

	tc, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, tc.Text, "command")
}

func TestHandleSerialReadWithClear(t *testing.T) {
	setupTestPorts(t)

	testMgr := serial.NewManager()
	testMgr.AddToBuffer("line 1")
	testMgr.AddToBuffer("line 2")
	testMgr.AddToBuffer("line 3")
	testMgr.SetTestState(true, "test-port", 115200, nil)
	session.InsertPort("test-port", session.NewPortSession(testMgr, "test-port", testMgr.Baud(), session.ModeReader))

	// First read with clear=true.
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{
		"clear": true,
	}
	result, err := handleSerialRead(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, result)

	tc, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, tc.Text, "line 1")
	assert.Contains(t, tc.Text, "line 2")
	assert.Contains(t, tc.Text, "line 3")

	// Second read should be empty after clear.
	req2 := mcp.CallToolRequest{}
	result2, err2 := handleSerialRead(context.Background(), req2)
	require.NoError(t, err2)
	require.NotNil(t, result2)

	tc2, ok := result2.Content[0].(mcp.TextContent)
	require.True(t, ok)
	assert.Equal(t, "", tc2.Text)
}

func TestCapLine(t *testing.T) {
	tests := map[string]struct {
		line      string
		wantExact string // if set, exact expected output
		wantMark  bool   // if true, expect a "…[+N bytes]" marker
	}{
		"short line unchanged": {
			line:      "hello world",
			wantExact: "hello world",
		},
		"invalid utf-8 replaced": {
			line:      "before\xffafter",
			wantExact: "before�after",
		},
		"oversized line truncated with marker": {
			line:     strings.Repeat("a", maxLineBytes+100),
			wantMark: true,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			got := capLine(tt.line)
			if tt.wantExact != "" {
				assert.Equal(t, tt.wantExact, got)
				return
			}
			if tt.wantMark {
				assert.Contains(t, got, "…[+")
				assert.Contains(t, got, "bytes]")
				assert.LessOrEqual(t, len(got), maxLineBytes+32)
			}
		})
	}
}

func TestBoundOutputWithinLimit(t *testing.T) {
	lines := []string{"line 1", "line 2", "line 3"}
	got := boundOutput(lines, false)
	assert.Equal(t, "line 1\nline 2\nline 3", got)
}

func TestBoundOutputTotalTruncation(t *testing.T) {
	// Each line is 100 bytes; force well over maxTotalBytes so only the
	// most recent lines survive, with an omitted-lines marker prepended.
	line := strings.Repeat("x", 100)
	var lines []string
	for i := 0; i < 400; i++ {
		lines = append(lines, fmt.Sprintf("%s-%d", line, i))
	}

	// raw:true exercises boundOutput's total-cap trimming directly; each
	// line here is a repeated byte, which sanitizeLine would collapse to a
	// short marker (see TestSanitizeLine), never reaching maxTotalBytes.
	got := boundOutput(lines, true)
	assert.Contains(t, got, "[output truncated:")
	assert.Contains(t, got, "earlier lines omitted]")
	assert.Contains(t, got, "-399") // most recent line retained
	assert.LessOrEqual(t, len(got), maxTotalBytes+128)
}

func TestSanitizeLine(t *testing.T) {
	tests := map[string]struct {
		line  string
		want  string // exact expected output, if set
		check func(string) bool
	}{
		"legit line untouched": {
			line: "INFO: heap free 176000 bytes",
			want: "INFO: heap free 176000 bytes",
		},
		"ansi stripped": {
			line: "\x1b[31mERROR\x1b[0m: something broke",
			want: "ERROR: something broke",
		},
		"ansi osc stripped": {
			line: "\x1b]0;window title\x07plain text",
			want: "plain text",
		},
		"majority control line elided": {
			line: "\x01\x02\x03\x04\x05\x06\x07\x08 ok",
			check: func(got string) bool {
				return strings.Contains(got, "bytes of framing noise elided")
			},
		},
		"repeated run collapsed not elided": {
			// A single repeated printable byte, well past maxRepeatRun;
			// collapses to a short marker rather than being elided,
			// since the marker text itself contains no control bytes.
			line: strings.Repeat("a", 200),
			check: func(got string) bool {
				return strings.Contains(got, "[0x61×200]") && !strings.Contains(got, "elided")
			},
		},
		"boundary just under threshold kept": {
			// 3 control bytes in 10 runes = 30% < 35% threshold.
			line: "\x01\x02\x03abcdefg",
			check: func(got string) bool {
				return !strings.Contains(got, "elided")
			},
		},
		"boundary just over threshold elided": {
			// 4 control bytes in 10 runes = 40% > 35% threshold.
			line: "\x01\x02\x03\x04abcdef",
			check: func(got string) bool {
				return strings.Contains(got, "bytes of framing noise elided")
			},
		},
		"tab newline cr excluded from ratio": {
			line: "\tfield1\tfield2\r\n",
			want: "\tfield1\tfield2\r\n",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			got := sanitizeLine(tt.line)
			if tt.check != nil {
				assert.True(t, tt.check(got), "got: %q", got)
				return
			}
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestSanitizeLineElidedMarkerReportsOriginalLength(t *testing.T) {
	line := "\x01\x02\x03\x04\x05\x06\x07\x08"
	got := sanitizeLine(line)
	assert.Equal(t, fmt.Sprintf("[%d bytes of framing noise elided]", len(line)), got)
}

func TestBoundOutputNonRawComposesWithMaxTotalBytes(t *testing.T) {
	// Legit (non-repeated) lines that individually pass sanitizeLine
	// untouched should still trip boundOutput's total-cap trimming.
	line := "field-a=1 field-b=2 field-c=3 field-d=4 field-e=5"
	var lines []string
	for i := 0; i < 800; i++ {
		lines = append(lines, fmt.Sprintf("%s-%d", line, i))
	}

	got := boundOutput(lines, false)
	assert.Contains(t, got, "[output truncated:")
	assert.Contains(t, got, "earlier lines omitted]")
	assert.Contains(t, got, "-799") // most recent line retained
	assert.LessOrEqual(t, len(got), maxTotalBytes+128)
}

func TestSanitizeLineComposesWithMaxLineBytes(t *testing.T) {
	// An oversized-but-legit line should still be truncated by capLine
	// after sanitizeLine's noise filtering passes it through untouched.
	line := strings.Repeat("legit-word ", maxLineBytes)
	got := sanitizeLine(line)
	assert.Contains(t, got, "…[+")
	assert.Contains(t, got, "bytes]")
	assert.LessOrEqual(t, len(got), maxLineBytes+32)
}

func TestHandleSerialReadFiltersNoiseByDefault(t *testing.T) {
	setupTestPorts(t)

	testMgr := serial.NewManager()
	testMgr.AddToBuffer("\x01\x02\x03\x04\x05\x06\x07\x08")
	testMgr.AddToBuffer("INFO: normal line")
	testMgr.SetTestState(true, "test-port", 115200, nil)
	session.InsertPort("test-port", session.NewPortSession(testMgr, "test-port", testMgr.Baud(), session.ModeReader))

	req := mcp.CallToolRequest{}
	result, err := handleSerialRead(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, result)

	tc, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, tc.Text, "bytes of framing noise elided")
	assert.Contains(t, tc.Text, "INFO: normal line")
}

func TestHandleSerialReadRawBypassesFiltering(t *testing.T) {
	setupTestPorts(t)

	noisy := "\x01\x02\x03\x04\x05\x06\x07\x08"
	testMgr := serial.NewManager()
	testMgr.AddToBuffer(noisy)
	testMgr.SetTestState(true, "test-port", 115200, nil)
	session.InsertPort("test-port", session.NewPortSession(testMgr, "test-port", testMgr.Baud(), session.ModeReader))

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{
		"raw": true,
	}
	result, err := handleSerialRead(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, result)

	tc, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	assert.NotContains(t, tc.Text, "elided")
	assert.Equal(t, noisy, tc.Text)
}

func TestHandleSerialReadCapsOversizedOutput(t *testing.T) {
	setupTestPorts(t)

	testMgr := serial.NewManager()
	testMgr.AddToBuffer(strings.Repeat("a", maxLineBytes+50))
	testMgr.AddToBuffer("before\xffafter")
	testMgr.SetTestState(true, "test-port", 115200, nil)
	session.InsertPort("test-port", session.NewPortSession(testMgr, "test-port", testMgr.Baud(), session.ModeReader))

	// raw:true exercises capLine's truncation directly; the default
	// (non-raw) path collapses this repeated-byte line to a short marker
	// well under maxLineBytes (see TestSanitizeLine), so it never truncates.
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{
		"raw": true,
	}
	result, err := handleSerialRead(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, result)

	tc, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, tc.Text, "…[+")
	assert.Contains(t, tc.Text, "before�after")
}

func TestHandleSerialWriteRaw(t *testing.T) {
	setupTestPorts(t)

	testMgr := serial.NewManager()
	testMgr.SetTestState(true, "test-port", 115200, nil)
	session.InsertPort("test-port", session.NewPortSession(testMgr, "test-port", testMgr.Baud(), session.ModeReader))

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{
		"data": "hello",
		"raw":  true,
	}
	result, err := handleSerialWrite(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, result.IsError)

	tc, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, tc.Text, "serial port is not running")
}

func TestHandleSerialStatusWithError(t *testing.T) {
	setupTestPorts(t)

	testMgr := serial.NewManager()
	testMgr.SetTestState(true, "test-port", 115200, fmt.Errorf("read timeout"))
	session.InsertPort("test-port", session.NewPortSession(testMgr, "test-port", testMgr.Baud(), session.ModeReader))

	req := mcp.CallToolRequest{}
	result, err := handleSerialStatus(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, result)

	tc, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, tc.Text, "\"last_error\"")
	assert.Contains(t, tc.Text, "read timeout")
	assert.Contains(t, tc.Text, "\"reconnecting\":")
}

func TestHandleSerialStatusExplicitPort(t *testing.T) {
	setupTestPorts(t)

	testMgrA := serial.NewManager()
	testMgrA.SetTestState(true, "port-a", 9600, nil)
	session.InsertPort("port-a", session.NewPortSession(testMgrA, "port-a", testMgrA.Baud(), session.ModeReader))

	testMgrB := serial.NewManager()
	testMgrB.SetTestState(true, "port-b", 115200, nil)
	session.InsertPort("port-b", session.NewPortSession(testMgrB, "port-b", testMgrB.Baud(), session.ModeReader))

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{
		"port": "port-a",
	}
	result, err := handleSerialStatus(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, result)

	tc, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, tc.Text, "\"port\": \"port-a\"")
	assert.NotContains(t, tc.Text, "\"ports\"")
	assert.NotContains(t, tc.Text, "port-b")
	assert.Contains(t, tc.Text, "\"reconnecting\":")
}

func TestHandleSerialStartSuccess(t *testing.T) {
	setupTestPorts(t)
	setupTestManagersFunc(t)

	session.SetNewManagerFunc(func(bufSize int) *serial.Manager {
		m := serial.NewManagerWithBufferSize(bufSize)
		m.OpenFunc = func(name string, mode *goSerial.Mode) (goSerial.Port, error) {
			return &noopPort{}, nil
		}
		return m
	})

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{
		"port":        "test-port",
		"baud":        float64(9600),
		"buffer_size": float64(500),
	}
	result, err := handleSerialStart(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.False(t, result.IsError)

	tc, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, tc.Text, "Started reading from test-port at 9600 baud")
}

func TestHandleSerialStartOpenError(t *testing.T) {
	setupTestPorts(t)
	setupTestManagersFunc(t)

	session.SetNewManagerFunc(func(bufSize int) *serial.Manager {
		m := serial.NewManagerWithBufferSize(bufSize)
		m.OpenFunc = func(name string, mode *goSerial.Mode) (goSerial.Port, error) {
			return nil, fmt.Errorf("device busy")
		}
		return m
	})

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{
		"port": "test-port",
	}
	result, err := handleSerialStart(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, result.IsError)

	tc, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, tc.Text, "device busy")
}

func TestHandleSerialFlashSuccess(t *testing.T) {
	setupTestPorts(t)
	setupTestManagersFunc(t)

	m := serial.NewManagerWithBufferSize(1000)
	m.OpenFunc = func(name string, mode *goSerial.Mode) (goSerial.Port, error) {
		return &noopPort{}, nil
	}
	session.InsertPort("test-port", session.NewPortSession(m, "test-port", m.Baud(), session.ModeReader))

	err := m.Start("test-port", 115200)
	require.NoError(t, err)
	assert.True(t, m.IsRunning())

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{
		"port":    "test-port",
		"command": "echo",
		"args":    []interface{}{"hello"},
	}
	result, err := handleSerialFlash(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.False(t, result.IsError)

	tc, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, tc.Text, "\"success\": true")
	assert.Contains(t, tc.Text, "\"command_output\"")
	assert.Contains(t, tc.Text, "hello")

	portCount := session.PortCount()
	if portCount > 0 {
		m, _, _ := session.ResolveSession(map[string]interface{}{})
		if m != nil && m.IsRunning() {
			_ = m.Stop()
		}
	}
}

// TestFlashExternalStepsTotalMatchesPhases guards the coupling the review
// flagged: flashExternalStepsTotal (fed to newSequentialStatusEmitter in
// handleSerialFlash) must cover every flash.StatusPhase* constant, or a tick
// added/removed from flash.Flash()'s body or the handler's own two ticks
// would silently under/over-count instead of failing a test. Enumerates the
// full phase set explicitly (not by reflection, since Go constants aren't
// reflectable) so a forgotten update to flashExternalPhases is exactly the
// failure this test catches.
func TestFlashExternalStepsTotalMatchesPhases(t *testing.T) {
	allFlashPhases := []string{
		flash.StatusPhaseStoppingPort,
		flash.StatusPhaseRunningCmd,
		flash.StatusPhaseRestarting,
		flash.StatusPhaseCapturingBoot,
		flash.StatusPhaseComplete,
	}

	assert.Equal(t, allFlashPhases, flashExternalPhases[:],
		"flashExternalPhases must list every flash.StatusPhase* constant in emission order")
	assert.Equal(t, len(allFlashPhases), flashExternalStepsTotal,
		"flashExternalStepsTotal must match the full flash.StatusPhase* set")
}

func TestHandleSerialStartReusesExistingManager(t *testing.T) {
	setupTestPorts(t)
	setupTestManagersFunc(t)

	m := serial.NewManagerWithBufferSize(1000)
	m.OpenFunc = func(name string, mode *goSerial.Mode) (goSerial.Port, error) {
		return &noopPort{}, nil
	}
	session.InsertPort("test-port", session.NewPortSession(m, "test-port", m.Baud(), session.ModeReader))

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{
		"port": "test-port",
		"baud": float64(9600),
	}
	result, err := handleSerialStart(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.False(t, result.IsError)

	assert.Equal(t, 1, session.PortCount())

	_ = m.Stop()
}

func TestHandleSerialFlashShellMode(t *testing.T) {
	setupTestPorts(t)
	setupTestManagersFunc(t)

	m := serial.NewManagerWithBufferSize(1000)
	m.OpenFunc = func(name string, mode *goSerial.Mode) (goSerial.Port, error) {
		return &noopPort{}, nil
	}
	session.InsertPort("test-port", session.NewPortSession(m, "test-port", m.Baud(), session.ModeReader))

	err := m.Start("test-port", 115200)
	require.NoError(t, err)
	assert.True(t, m.IsRunning())

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{
		"port":    "test-port",
		"command": "echo hello && echo world",
		"shell":   true,
	}
	result, err := handleSerialFlash(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.False(t, result.IsError)

	tc, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, tc.Text, "hello")
	assert.Contains(t, tc.Text, "world")

	portCount := session.PortCount()
	if portCount > 0 {
		m, _, _ := session.ResolveSession(map[string]interface{}{})
		if m != nil && m.IsRunning() {
			_ = m.Stop()
		}
	}
}

func TestHandleSerialFlashCwd(t *testing.T) {
	setupTestPorts(t)
	setupTestManagersFunc(t)

	m := serial.NewManagerWithBufferSize(1000)
	m.OpenFunc = func(name string, mode *goSerial.Mode) (goSerial.Port, error) {
		return &noopPort{}, nil
	}
	session.InsertPort("test-port", session.NewPortSession(m, "test-port", m.Baud(), session.ModeReader))

	err := m.Start("test-port", 115200)
	require.NoError(t, err)
	assert.True(t, m.IsRunning())

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{
		"port":    "test-port",
		"command": "pwd",
		"cwd":     "/tmp",
	}
	result, err := handleSerialFlash(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.False(t, result.IsError)

	tc, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	// macOS /tmp is symlinked to /private/tmp.
	assert.True(t, strings.Contains(tc.Text, "/tmp") || strings.Contains(tc.Text, "/private/tmp"))

	portCount := session.PortCount()
	if portCount > 0 {
		m, _, _ := session.ResolveSession(map[string]interface{}{})
		if m != nil && m.IsRunning() {
			_ = m.Stop()
		}
	}
}

func TestRegisterTools(t *testing.T) {
	s := server.NewMCPServer("pogopin", "test",
		server.WithToolCapabilities(true),
	)
	require.NotNil(t, s)
	registerTools(s)
	// If we get here without panicking, registration succeeded.
}

func TestHandleSerialStartAutoResetUSB(t *testing.T) {
	setupTestPorts(t)
	setupTestManagersFunc(t)
	setupTestFlasherFactory(t)
	setupTestIsUSBPort(t)
	setupFastWaitForPort(t)
	setupFastBootCapture(t)

	session.SetNewManagerFunc(func(bufSize int) *serial.Manager {
		m := serial.NewManagerWithBufferSize(bufSize)
		m.OpenFunc = func(name string, mode *goSerial.Mode) (goSerial.Port, error) {
			return &noopPort{}, nil
		}
		return m
	})

	mockFlasher := &mockESPFlasher{resetCalled: false}
	session.SetFlasherFactory(func(port string, opts *espflasher.FlasherOptions) (esp.Flasher, error) {
		return mockFlasher, nil
	})

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{
		"port": "/dev/cu.usbmodem1101",
	}
	result, err := handleSerialStart(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.False(t, result.IsError)

	tc, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, tc.Text, "Started reading from /dev/cu.usbmodem1101")
	assert.Contains(t, tc.Text, "auto-reset")
}

func TestHandleSerialStartAutoResetFalse(t *testing.T) {
	setupTestPorts(t)
	setupTestManagersFunc(t)
	setupTestFlasherFactory(t)
	setupFastWaitForPort(t)
	setupFastBootCapture(t)

	session.SetNewManagerFunc(func(bufSize int) *serial.Manager {
		m := serial.NewManagerWithBufferSize(bufSize)
		m.OpenFunc = func(name string, mode *goSerial.Mode) (goSerial.Port, error) {
			return &noopPort{}, nil
		}
		return m
	})

	mockFlasher := &mockESPFlasher{resetCalled: false}
	session.SetFlasherFactory(func(port string, opts *espflasher.FlasherOptions) (esp.Flasher, error) {
		return mockFlasher, nil
	})

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{
		"port":       "/dev/cu.usbmodem1101",
		"auto_reset": false,
	}
	result, err := handleSerialStart(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.False(t, result.IsError)

	tc, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, tc.Text, "Started reading from /dev/cu.usbmodem1101")
	assert.NotContains(t, tc.Text, "auto-reset")
	assert.False(t, mockFlasher.resetCalled)
}

func TestHandleSerialStartAutoResetNonUSB(t *testing.T) {
	setupTestPorts(t)
	setupTestManagersFunc(t)
	setupTestFlasherFactory(t)
	setupFastWaitForPort(t)
	setupFastBootCapture(t)

	session.SetNewManagerFunc(func(bufSize int) *serial.Manager {
		m := serial.NewManagerWithBufferSize(bufSize)
		m.OpenFunc = func(name string, mode *goSerial.Mode) (goSerial.Port, error) {
			return &noopPort{}, nil
		}
		return m
	})

	mockFlasher := &mockESPFlasher{resetCalled: false}
	session.SetFlasherFactory(func(port string, opts *espflasher.FlasherOptions) (esp.Flasher, error) {
		return mockFlasher, nil
	})

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{
		"port": "/dev/ttyS0",
	}
	result, err := handleSerialStart(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.False(t, result.IsError)

	tc, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, tc.Text, "Started reading from /dev/ttyS0")
	assert.NotContains(t, tc.Text, "auto-reset")
	assert.False(t, mockFlasher.resetCalled)
}

func TestHandleSerialStartAutoResetFailure(t *testing.T) {
	setupTestPorts(t)
	setupTestManagersFunc(t)
	setupTestFlasherFactory(t)
	setupFastWaitForPort(t)
	setupFastBootCapture(t)

	session.SetNewManagerFunc(func(bufSize int) *serial.Manager {
		m := serial.NewManagerWithBufferSize(bufSize)
		m.OpenFunc = func(name string, mode *goSerial.Mode) (goSerial.Port, error) {
			return &noopPort{}, nil
		}
		return m
	})

	session.SetFlasherFactory(func(port string, opts *espflasher.FlasherOptions) (esp.Flasher, error) {
		return nil, fmt.Errorf("device not found")
	})

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{
		"port": "/dev/cu.usbmodem1101",
	}
	result, err := handleSerialStart(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.False(t, result.IsError)

	tc, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, tc.Text, "Started reading from /dev/cu.usbmodem1101")
	assert.NotContains(t, tc.Text, "auto-reset")
}

func TestHandleSerialStopExplicitPort(t *testing.T) {
	setupTestPorts(t)
	testMgr := serial.NewManager()
	testMgr.SetTestState(true, "port-a", 115200, nil)
	session.InsertPort("port-a", session.NewPortSession(testMgr, "port-a", testMgr.Baud(), session.ModeReader))
	session.InsertPort("port-b", session.NewPortSession(serial.NewManager(), "port-b", 115200, session.ModeReader))

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{"port": "port-a"}
	result, err := handleSerialStop(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.False(t, result.IsError)
}

func TestHandleSerialStopNoPort(t *testing.T) {
	setupTestPorts(t)
	req := mcp.CallToolRequest{}
	result, err := handleSerialStop(context.Background(), req)
	require.NoError(t, err)
	require.True(t, result.IsError)
}

// TestHandleSerialRestartOpenPortPreservesBaud verifies restart on an
// already-open port stops the existing session (freeing it for a fresh
// StartSession) and starts a new one preserving the prior baud.
func TestHandleSerialRestartOpenPortPreservesBaud(t *testing.T) {
	setupTestPorts(t)
	setupTestManagersFunc(t)

	session.SetNewManagerFunc(func(bufSize int) *serial.Manager {
		m := serial.NewManagerWithBufferSize(bufSize)
		m.OpenFunc = func(name string, mode *goSerial.Mode) (goSerial.Port, error) {
			return &noopPort{}, nil
		}
		return m
	})

	m := serial.NewManagerWithBufferSize(1000)
	m.OpenFunc = func(name string, mode *goSerial.Mode) (goSerial.Port, error) {
		return &noopPort{}, nil
	}
	session.InsertPort("test-port", session.NewPortSession(m, "test-port", 57600, session.ModeReader))
	require.NoError(t, m.Start("test-port", 57600))
	require.True(t, m.IsRunning())

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{
		"port": "test-port",
	}
	result, err := handleSerialRestart(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.False(t, result.IsError)

	tc, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, tc.Text, "Restarted reading from test-port at 57600 baud")
	assert.Equal(t, 1, session.PortCount())

	newMgr, resolvedPort, err := session.ResolveSession(map[string]interface{}{"port": "test-port"})
	require.NoError(t, err)
	assert.Equal(t, "test-port", resolvedPort)
	assert.True(t, newMgr.IsRunning())
	assert.Equal(t, 57600, newMgr.Baud())
	_ = newMgr.Stop()
}

// TestHandleSerialRestartClosedPortBehavesLikeStart verifies restart on a
// port with no open session behaves like a plain serial_start (falls back
// to the 115200 default baud, no stop attempted).
func TestHandleSerialRestartClosedPortBehavesLikeStart(t *testing.T) {
	setupTestPorts(t)
	setupTestManagersFunc(t)

	session.SetNewManagerFunc(func(bufSize int) *serial.Manager {
		m := serial.NewManagerWithBufferSize(bufSize)
		m.OpenFunc = func(name string, mode *goSerial.Mode) (goSerial.Port, error) {
			return &noopPort{}, nil
		}
		return m
	})

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{
		"port": "test-port",
	}
	result, err := handleSerialRestart(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.False(t, result.IsError)

	tc, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, tc.Text, "Restarted reading from test-port at 115200 baud")
	assert.Equal(t, 1, session.PortCount())

	m, _, err := session.ResolveSession(map[string]interface{}{"port": "test-port"})
	require.NoError(t, err)
	_ = m.Stop()
}

// TestHandleSerialRestartArgsOverridePreservedBaud verifies an explicit baud
// in the restart request wins over the previously-open port's baud.
func TestHandleSerialRestartArgsOverridePreservedBaud(t *testing.T) {
	setupTestPorts(t)
	setupTestManagersFunc(t)

	session.SetNewManagerFunc(func(bufSize int) *serial.Manager {
		m := serial.NewManagerWithBufferSize(bufSize)
		m.OpenFunc = func(name string, mode *goSerial.Mode) (goSerial.Port, error) {
			return &noopPort{}, nil
		}
		return m
	})

	m := serial.NewManagerWithBufferSize(1000)
	m.OpenFunc = func(name string, mode *goSerial.Mode) (goSerial.Port, error) {
		return &noopPort{}, nil
	}
	session.InsertPort("test-port", session.NewPortSession(m, "test-port", 9600, session.ModeReader))
	require.NoError(t, m.Start("test-port", 9600))

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{
		"port": "test-port",
		"baud": float64(230400),
	}
	result, err := handleSerialRestart(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.False(t, result.IsError)

	tc, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, tc.Text, "Restarted reading from test-port at 230400 baud")

	newMgr, _, err := session.ResolveSession(map[string]interface{}{"port": "test-port"})
	require.NoError(t, err)
	assert.Equal(t, 230400, newMgr.Baud())
	_ = newMgr.Stop()
}

func TestHandleSerialRestartMissingPort(t *testing.T) {
	setupTestPorts(t)
	req := mcp.CallToolRequest{}
	result, err := handleSerialRestart(context.Background(), req)
	require.NoError(t, err)
	require.True(t, result.IsError)
}

// TestHandleSerialRestartOpenError verifies session.RestartSession's error
// (e.g. Start failing on the fresh manager) propagates as a tool error.
func TestHandleSerialRestartOpenError(t *testing.T) {
	setupTestPorts(t)
	setupTestManagersFunc(t)

	session.SetNewManagerFunc(func(bufSize int) *serial.Manager {
		m := serial.NewManagerWithBufferSize(bufSize)
		m.OpenFunc = func(name string, mode *goSerial.Mode) (goSerial.Port, error) {
			return nil, fmt.Errorf("device busy")
		}
		return m
	})

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{
		"port":        "test-port",
		"buffer_size": float64(2000),
	}
	result, err := handleSerialRestart(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, result.IsError)

	tc, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, tc.Text, "device busy")
}
