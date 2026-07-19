package mcpapp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"dangernoodle.io/pogopin/internal/status"
)

// TestRunHeartbeat_WritesStatusFileAndStopsOnCancel ports the retired
// internal/mcpserver/heartbeat_test.go's test of the same name (MC-12
// review: RunHeartbeat's prior test was deleted, not ported, regressing its
// coverage to 0%). Asserts the status file is written and the goroutine
// returns on context cancellation.
func TestRunHeartbeat_WritesStatusFileAndStopsOnCancel(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, strconv.Itoa(os.Getpid())+".json")
	prev := status.SetStatusDir(tmp)
	defer status.SetStatusDir(prev)

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

// TestRunHeartbeatReturnsOnCancel covers the exported RunHeartbeat's thin
// delegation to runHeartbeat(ctx, heartbeatInterval): with an
// already-cancelled context, it must return immediately rather than
// waiting out the real 15s interval.
func TestRunHeartbeatReturnsOnCancel(t *testing.T) {
	prev := status.SetStatusDir(t.TempDir())
	defer status.SetStatusDir(prev)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	done := make(chan struct{})
	go func() {
		RunHeartbeat(ctx)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("RunHeartbeat did not return promptly on an already-cancelled context")
	}
}
