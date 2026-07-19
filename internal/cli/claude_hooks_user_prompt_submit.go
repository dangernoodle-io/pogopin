package cli

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/dangernoodle-io/shesha/host/claudecode/hooks"
)

// espIDFContextMessage is injected as plain text on UserPromptSubmit when the
// session's cwd is detected as an ESP-IDF project. Faithful port of
// context.sh's echo string, including the em-dash separator.
const espIDFContextMessage = "[pogopin] ESP-IDF project detected — use esp_info to identify chip before flashing"

// idfComponentRegisterMarker is the CMakeLists.txt substring context.sh
// grepped for.
const idfComponentRegisterMarker = "idf_component_register"

// hookHandleUserPromptSubmit is the UserPromptSubmit-hook ESP-IDF project
// detector, a native port of plugin/scripts/context.sh. Detection is purely
// filesystem-based on the session's cwd (p.Cwd): a project is detected iff
// $cwd/sdkconfig exists, or $cwd/CMakeLists.txt is readable and contains
// "idf_component_register". Fully fail-open: any read error (missing
// directory, permission denied, unreadable cwd) is treated as "not
// detected" — this hook never panics or blocks the session.
func hookHandleUserPromptSubmit(_ context.Context, _ io.Reader, p hooks.UserPromptSubmitPayload) hooks.Response {
	if !isESPIDFProject(p.Cwd) {
		return hooks.Response{}
	}
	return hooks.Response{PlainText: espIDFContextMessage}
}

// isESPIDFProject reports whether cwd looks like an ESP-IDF project:
// sdkconfig present, or CMakeLists.txt present and mentioning
// idf_component_register. Fail-open: a stat/read error on either path is
// treated as "absent", never propagated.
func isESPIDFProject(cwd string) bool {
	if cwd == "" {
		return false
	}

	if _, err := os.Stat(filepath.Join(cwd, "sdkconfig")); err == nil {
		return true
	}

	data, err := os.ReadFile(filepath.Join(cwd, "CMakeLists.txt"))
	if err != nil {
		return false
	}
	return strings.Contains(string(data), idfComponentRegisterMarker)
}
