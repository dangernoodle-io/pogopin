//go:build !mock

package mcpapp

// maybeEnableMock is a no-op in every build except the mock-tagged one; see
// mock_enable_mock.go.
func maybeEnableMock() {}
