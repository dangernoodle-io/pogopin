package serial

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
)

// TestLineIsNoise covers lineIsNoise directly, including its empty-string
// short-circuit — only exercised indirectly via sanitizeLine elsewhere.
func TestLineIsNoise(t *testing.T) {
	assert.False(t, lineIsNoise(""))
	assert.False(t, lineIsNoise("INFO: normal log line"))
	assert.False(t, lineIsNoise("line\twith\ntabs\rand newlines"))
	assert.True(t, lineIsNoise("\x01\x02\x03\x04\x05\x06\x07\x08"))
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

// TestCapLineTruncatesOnRuneBoundary covers capLine's rune-boundary backoff
// loop: an oversized line where a multi-byte UTF-8 rune straddles the exact
// maxLineBytes cut point must back off to a valid rune start rather than
// splitting mid-rune, unlike TestCapLine's all-ASCII case where the cut
// already lands on a boundary.
func TestCapLineTruncatesOnRuneBoundary(t *testing.T) {
	// "é" is 2 bytes (0xC3 0xA9); pad so the 2-byte rune straddles
	// maxLineBytes exactly, forcing the backoff loop to execute.
	line := strings.Repeat("a", maxLineBytes-1) + "é" + strings.Repeat("b", 50)

	got := capLine(line)
	assert.True(t, utf8.ValidString(got))
	assert.Contains(t, got, "…[+")
	assert.Contains(t, got, "bytes]")
}

func TestBoundOutputWithinLimit(t *testing.T) {
	lines := []string{"line 1", "line 2", "line 3"}
	got := boundOutput(lines, false)
	assert.Equal(t, "line 1\nline 2\nline 3", got)
}

func TestBoundOutputTotalTruncation(t *testing.T) {
	line := strings.Repeat("x", 100)
	var lines []string
	for i := 0; i < 400; i++ {
		lines = append(lines, fmt.Sprintf("%s-%d", line, i))
	}

	got := boundOutput(lines, true)
	assert.Contains(t, got, "[output truncated:")
	assert.Contains(t, got, "earlier lines omitted]")
	assert.Contains(t, got, "-399")
	assert.LessOrEqual(t, len(got), maxTotalBytes+128)
}

func TestSanitizeLine(t *testing.T) {
	tests := map[string]struct {
		line  string
		want  string
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
			line: strings.Repeat("a", 200),
			check: func(got string) bool {
				return strings.Contains(got, "[0x61×200]") && !strings.Contains(got, "elided")
			},
		},
		"boundary just under threshold kept": {
			line: "\x01\x02\x03abcdefg",
			check: func(got string) bool {
				return !strings.Contains(got, "elided")
			},
		},
		"boundary just over threshold elided": {
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
	line := "field-a=1 field-b=2 field-c=3 field-d=4 field-e=5"
	var lines []string
	for i := 0; i < 800; i++ {
		lines = append(lines, fmt.Sprintf("%s-%d", line, i))
	}

	got := boundOutput(lines, false)
	assert.Contains(t, got, "[output truncated:")
	assert.Contains(t, got, "earlier lines omitted]")
	assert.Contains(t, got, "-799")
	assert.LessOrEqual(t, len(got), maxTotalBytes+128)
}

func TestSanitizeLineComposesWithMaxLineBytes(t *testing.T) {
	line := strings.Repeat("legit-word ", maxLineBytes)
	got := sanitizeLine(line)
	assert.Contains(t, got, "…[+")
	assert.Contains(t, got, "bytes]")
	assert.LessOrEqual(t, len(got), maxLineBytes+32)
}
