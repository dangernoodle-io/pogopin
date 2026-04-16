package decode

import (
	"fmt"
	"regexp"
	"strings"
)

var (
	reXtensaToken = regexp.MustCompile(`^0x[0-9a-fA-F]+:0x[0-9a-fA-F]+$`)
	reRiscvToken  = regexp.MustCompile(`^0x[0-9a-fA-F]+$`)
)

const (
	sentinelFeefeffe = "0xfeefeffe"
	sentinelZero     = "0x00000000"
)

// ParseBacktrace extracts PC addresses from a panic backtrace block.
// arch hints which format to expect; parsing is permissive and will accept
// either format regardless but returns an error if no backtrace line is found.
func ParseBacktrace(panicText string, _ Arch) ([]string, error) {
	// Normalize line endings.
	normalized := strings.ReplaceAll(panicText, "\r\n", "\n")
	normalized = strings.ReplaceAll(normalized, "\r", "\n")

	for _, line := range strings.Split(normalized, "\n") {
		trimmed := strings.TrimSpace(line)
		idx := strings.Index(strings.ToLower(trimmed), "backtrace:")
		if idx < 0 {
			continue
		}
		rest := trimmed[idx+len("backtrace:"):]
		tokens := strings.Fields(rest)

		var pcs []string
		for _, tok := range tokens {
			var pc string
			switch {
			case reXtensaToken.MatchString(tok):
				pc = strings.ToLower(strings.SplitN(tok, ":", 2)[0])
			case reRiscvToken.MatchString(tok):
				pc = strings.ToLower(tok)
			default:
				continue
			}
			if pc == sentinelFeefeffe || pc == sentinelZero {
				continue
			}
			pcs = append(pcs, pc)
		}
		return pcs, nil
	}

	return nil, fmt.Errorf("no backtrace line found in panic text")
}
