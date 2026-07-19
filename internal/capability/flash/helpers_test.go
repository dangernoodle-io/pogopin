package flash

import (
	"testing"

	"github.com/dangernoodle-io/shesha/testkit"

	"dangernoodle.io/pogopin/internal/testutil"
)

// newHarness composes a minimal shesha App around Capability{} alone,
// mirroring internal/capability/serial's package-local helper of the same
// name.
func newHarness(t *testing.T) *testkit.Harness {
	t.Helper()
	return testutil.NewHarness(t, Capability{})
}
