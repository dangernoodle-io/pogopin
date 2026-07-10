//go:build !mock

package mcpserver

// maybeEnableMock is a no-op in every build except the mock-tagged one; see
// mock_enable_mock.go.
func maybeEnableMock() {}
