//go:build mock

package mcpserver

import "dangernoodle.io/pogopin/internal/mockhw"

// maybeEnableMock activates the virtual ESP chip (internal/mockhw) in place
// of real hardware. Only compiled into a binary built with `-tags mock` —
// production/goreleaser builds never carry this tag, so the shipped binary
// has no mock code and no runtime env backdoor. The server reads no env var
// to decide; the build tag alone is the switch. The restore closure
// mockhw.Install returns is discarded: this build's mock wiring lives for
// the process's entire lifetime.
func maybeEnableMock() {
	mockhw.Install()
}
