package mcpserver

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"dangernoodle.io/breadboard/internal/status"
)

func TestRunHeartbeat_WritesStatusFileAndStopsOnCancel(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "status.json")
	prev := status.SetStatusFilePath(path)
	defer status.SetStatusFilePath(prev)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		runHeartbeat(ctx, 5*time.Millisecond)
		close(done)
	}()

	require.Eventually(t, func() bool {
		_, err := os.Stat(path)
		return err == nil
	}, time.Second, 5*time.Millisecond, "status file not created by heartbeat")

	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("runHeartbeat did not return after context cancel")
	}

	b, err := os.ReadFile(path)
	require.NoError(t, err)
	var sf status.StatusFile
	require.NoError(t, json.Unmarshal(b, &sf))
	require.NotZero(t, sf.UpdatedAt)
}
