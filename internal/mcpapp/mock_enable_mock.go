//go:build mock

package mcpapp

import "dangernoodle.io/pogopin/internal/mockhw"

// maybeEnableMock activates the virtual ESP chip (internal/mockhw) in place
// of real hardware. Only compiled into a binary built with `-tags mock` —
// production/goreleaser builds never carry this tag, so the shipped binary
// has no mock code and no runtime env backdoor. The server reads no env var
// to decide; the build tag alone is the switch. The restore closure
// mockhw.Install returns is discarded: this build's mock wiring lives for
// the process's entire lifetime. MC-12 port of
// internal/mcpserver/mock_enable_mock.go (called from Serve() there; called
// from BuildApp() here, the new composition root).
func maybeEnableMock() {
	mockhw.Install()
}
