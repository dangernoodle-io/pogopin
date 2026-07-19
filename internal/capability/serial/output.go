package serial

import (
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

// Bounds on serial_read output to keep it well under the tool token cap.
const (
	maxLineBytes  = 512
	maxTotalBytes = 32768
)

// Noise-filtering thresholds for serial_read's default (non-raw) emit path.
// See BR-54: garbled/framing-noise bytes JSON-encode as \u00XX escapes
// (~6x bloat) with no signal, so we elide them before capLine truncation.
const (
	// maxRepeatRun collapses runs of the same byte longer than this into a
	// short "[0xXX×N]" marker before the noise ratio is computed, so a
	// stuck line (e.g. all 0xFF) doesn't dominate the ratio accounting.
	maxRepeatRun = 16
	// noiseRatioThreshold is the max fraction of a line's runes that may be
	// C0 control bytes (excluding \t \n \r) or U+FFFD substitutions before
	// the whole line is elided as framing noise rather than emitted.
	noiseRatioThreshold = 0.35
)

// ansiEscapeRE matches ANSI CSI ("ESC [ ... final-byte") and OSC
// ("ESC ] ... BEL-or-ST") escape sequences so they can be stripped before
// noise-ratio accounting; they carry no log signal for an LLM reader.
var ansiEscapeRE = regexp.MustCompile("\x1b(?:\\[[0-9;?]*[ -/]*[@-~]|\\][^\x07\x1b]*(?:\x07|\x1b\\\\))")

// stripANSI removes ANSI CSI/OSC escape sequences from line unconditionally;
// they carry no log signal for an LLM reader and JSON-encode expensively.
func stripANSI(line string) string {
	return ansiEscapeRE.ReplaceAllString(line, "")
}

// capLine sanitizes invalid UTF-8 and truncates a line that exceeds
// maxLineBytes on a valid rune boundary, appending a byte-count marker.
// This is also the entire raw:true fallback path — no noise filtering.
func capLine(line string) string {
	line = strings.ToValidUTF8(line, "�")
	if len(line) <= maxLineBytes {
		return line
	}
	cut := maxLineBytes
	for cut > 0 && !utf8.RuneStart(line[cut]) {
		cut--
	}
	dropped := len(line) - cut
	return fmt.Sprintf("%s …[+%d bytes]", line[:cut], dropped)
}

// collapseRepeats collapses runs of more than maxRepeatRun identical bytes
// into a short "[0xXX×N]" marker so long runs of stuck/framing bytes don't
// dominate the noise-ratio computation or the emitted line length.
func collapseRepeats(line string) string {
	data := []byte(line)
	if len(data) == 0 {
		return line
	}

	var b strings.Builder
	for i := 0; i < len(data); {
		j := i + 1
		for j < len(data) && data[j] == data[i] {
			j++
		}
		run := j - i
		if run > maxRepeatRun {
			fmt.Fprintf(&b, "[0x%02x×%d]", data[i], run)
		} else {
			b.Write(data[i:j])
		}
		i = j
	}
	return b.String()
}

// lineIsNoise reports whether more than noiseRatioThreshold of line's runes
// are C0 control bytes (excluding \t \n \r) or UTF-8 replacement runes,
// i.e. the line looks like garbled framing bytes rather than real log text.
func lineIsNoise(line string) bool {
	if line == "" {
		return false
	}
	var noisy, total int
	for _, r := range line {
		total++
		switch {
		case r == utf8.RuneError:
			noisy++
		case r < 0x20 && r != '\t' && r != '\n' && r != '\r':
			noisy++
		}
	}
	return total > 0 && float64(noisy)/float64(total) > noiseRatioThreshold
}

// sanitizeLine filters ANSI escapes and framing noise from a raw serial
// line before capLine's length truncation runs, per BR-54: strip ANSI
// unconditionally, collapse long repeated-byte runs, then elide the whole
// line if what remains is majority non-printable garbage. This is the
// default (non-raw) serial_read emit path.
func sanitizeLine(line string) string {
	origLen := len(line)

	filtered := collapseRepeats(stripANSI(line))
	valid := strings.ToValidUTF8(filtered, "�")

	if lineIsNoise(valid) {
		return fmt.Sprintf("[%d bytes of framing noise elided]", origLen)
	}

	return capLine(valid)
}

// boundOutput sanitizes and caps each line, then caps the total joined size
// by keeping the most recent lines that fit within maxTotalBytes. When raw
// is true, framing-noise filtering (sanitizeLine) is skipped and each line
// only gets capLine's UTF-8 validation + length cap.
func boundOutput(lines []string, raw bool) string {
	capped := make([]string, len(lines))
	for i, l := range lines {
		if raw {
			capped[i] = capLine(l)
		} else {
			capped[i] = sanitizeLine(l)
		}
	}

	joined := strings.Join(capped, "\n")
	if len(joined) <= maxTotalBytes {
		return joined
	}

	var kept []string
	size := 0
	omitted := len(capped)
	for i := len(capped) - 1; i >= 0; i-- {
		lineSize := len(capped[i])
		if len(kept) > 0 {
			lineSize++ // account for the joining newline
		}
		if size+lineSize > maxTotalBytes {
			omitted = i + 1
			break
		}
		kept = append(kept, capped[i])
		size += lineSize
		omitted = i
	}
	for i, j := 0, len(kept)-1; i < j; i, j = i+1, j-1 {
		kept[i], kept[j] = kept[j], kept[i]
	}

	marker := fmt.Sprintf("[output truncated: %d earlier lines omitted]", omitted)
	if len(kept) == 0 {
		return marker
	}
	return marker + "\n" + strings.Join(kept, "\n")
}
