package cli

import (
	"fmt"

	sheshacli "github.com/dangernoodle-io/shesha/cli"
	"github.com/dangernoodle-io/shesha/host/claudecode"
	"github.com/dangernoodle-io/shesha/host/claudecode/hooks"
	"github.com/spf13/cobra"
)

// claudeProvider returns the shesha cli.CommandProvider contributing the
// `claude` host namespace ("everything Claude Code's plugin protocol invokes
// against this binary"): `claude hooks` mounts UserPromptSubmit (BR-87's
// context.sh port, ESP-IDF project detection) and PreToolUse (BR-31/BR-87's
// pre-tool-port-check.js port, cross-session port-conflict warning). No
// `extra` provider is passed — `pogo statusline` deliberately stays a
// top-level command (see internal/cli/statusline.go), not nested under
// `claude`, since the plugin's settings.json statusLine.command depends on
// the exact top-level invocation.
//
// SessionStart is intentionally NOT registered here: plugin/scripts/
// self-heal.js stays Node — it's what installs this very binary, so a
// Go-native SessionStart hook would have nothing to invoke it on a fresh
// install (the chicken-and-egg installer problem).
func claudeProvider() sheshacli.CommandProvider {
	return claudecode.NewProvider(
		hooks.NewRegistry().
			UserPromptSubmit(hookHandleUserPromptSubmit).
			PreToolUse(hookHandlePreToolUse),
	)
}

// mustMountProviders mounts providers onto root via shesha's MountProviders,
// panicking on a non-nil error (an unresolved Mount.Under path) — a mount
// failure is a startup-config bug, not a runtime condition to recover from.
// Extracted from init() so the panic path is independently testable.
func mustMountProviders(root *cobra.Command, providers ...sheshacli.CommandProvider) {
	if err := sheshacli.MountProviders(root, providers...); err != nil {
		panic(fmt.Sprintf("pogopin: mount claude provider: %v", err))
	}
}
