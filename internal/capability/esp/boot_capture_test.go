package esp

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/dangernoodle-io/shesha/testkit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"dangernoodle.io/pogopin/internal/serial"
	"dangernoodle.io/pogopin/internal/session"
	"dangernoodle.io/pogopin/internal/testutil"
)

func TestCaptureBootOutputNilSession(t *testing.T) {
	setupFastBootCapture(t)
	assert.Nil(t, captureBootOutput(nil, 2.0))
}

func TestCaptureBootOutputZeroDwell(t *testing.T) {
	setupFastBootCapture(t)
	mgr := serial.NewManager()
	mgr.AddToBuffer("should not be read")
	sess := session.NewPortSession(mgr, "", 0, session.ModeReader)
	assert.Nil(t, captureBootOutput(sess, 0))
}

func TestCaptureBootOutputReadsLines(t *testing.T) {
	mgr := serial.NewManager()
	sess := session.NewPortSession(mgr, "", 0, session.ModeReader)
	orig := bootCaptureWait
	bootCaptureWait = func(d time.Duration) {
		mgr.AddToBuffer("boot: init")
		mgr.AddToBuffer("boot: ready")
	}
	t.Cleanup(func() { bootCaptureWait = orig })

	result := captureBootOutput(sess, 1.0)
	require.Len(t, result, 2)
	assert.Equal(t, "boot: init", result[0])
	assert.Equal(t, "boot: ready", result[1])
}

// TestCaptureBootOutputDropsStaleLines pins the BR-14 fix: lines that
// accumulated in the ring buffer before the ESP operation must not leak
// into boot_output.
func TestCaptureBootOutputDropsStaleLines(t *testing.T) {
	mgr := serial.NewManager()
	mgr.AddToBuffer("pre: stale line 1")
	mgr.AddToBuffer("pre: stale line 2")
	sess := session.NewPortSession(mgr, "", 0, session.ModeReader)
	orig := bootCaptureWait
	bootCaptureWait = func(d time.Duration) { mgr.AddToBuffer("boot: fresh") }
	t.Cleanup(func() { bootCaptureWait = orig })

	result := captureBootOutput(sess, 1.0)
	require.Len(t, result, 1)
	assert.Equal(t, "boot: fresh", result[0])
}

func TestHandleFlashBootCapture(t *testing.T) {
	testutil.SetupTestPorts(t)
	testutil.SetupTestFlasherFactory(t)
	testutil.SetupFastWaitForPort(t)
	testutil.SetupTestListPorts(t)

	testMgr := serial.NewManager()
	testMgr.SetTestState(true, "/dev/ttyUSB0", 115200, nil)
	session.InsertPort("/dev/ttyUSB0", session.NewPortSession(testMgr, "/dev/ttyUSB0", 115200, session.ModeReader))

	orig := bootCaptureWait
	bootCaptureWait = func(d time.Duration) {
		testMgr.AddToBuffer("ESP-IDF v5.1")
		testMgr.AddToBuffer("boot: ready")
	}
	t.Cleanup(func() { bootCaptureWait = orig })

	setFlasher(t, &testutil.MockFlasher{})

	origList := session.SetListPortsFn(func() ([]serial.PortInfo, error) {
		return []serial.PortInfo{{Name: "/dev/ttyUSB0"}}, nil
	})
	t.Cleanup(func() { session.SetListPortsFn(origList) })

	tmpDir := t.TempDir()
	fw := tmpDir + "/firmware.bin"
	require.NoError(t, os.WriteFile(fw, []byte("firmware"), 0o644))

	h := newHarness(t)
	result, err := h.CallTool(context.Background(), "esp_flash", map[string]any{
		"port": "/dev/ttyUSB0",
		"images": []any{
			map[string]any{"path": fw, "offset": float64(0)},
		},
		"boot_wait": float64(0.001),
	})
	require.NoError(t, err)
	text := testkit.ResultText(result)
	assert.Contains(t, text, "boot_output")
	assert.Contains(t, text, "ESP-IDF v5.1")
}

func TestHandleEraseBootCapture(t *testing.T) {
	testutil.SetupTestPorts(t)
	testutil.SetupTestFlasherFactory(t)
	testutil.SetupFastWaitForPort(t)
	testutil.SetupTestListPorts(t)

	testMgr := serial.NewManager()
	testMgr.SetTestState(true, "/dev/ttyUSB0", 115200, nil)
	session.InsertPort("/dev/ttyUSB0", session.NewPortSession(testMgr, "/dev/ttyUSB0", 115200, session.ModeReader))

	orig := bootCaptureWait
	bootCaptureWait = func(d time.Duration) { testMgr.AddToBuffer("boot: erased") }
	t.Cleanup(func() { bootCaptureWait = orig })

	setFlasher(t, &testutil.MockFlasher{})

	origList := session.SetListPortsFn(func() ([]serial.PortInfo, error) {
		return []serial.PortInfo{{Name: "/dev/ttyUSB0"}}, nil
	})
	t.Cleanup(func() { session.SetListPortsFn(origList) })

	h := newHarness(t)
	result, err := h.CallTool(context.Background(), "esp_erase", map[string]any{
		"port": "/dev/ttyUSB0", "boot_wait": float64(0.001),
	})
	require.NoError(t, err)
	text := testkit.ResultText(result)
	assert.Contains(t, text, "boot_output")
	assert.Contains(t, text, "status")
}

func TestHandleResetBootCapture(t *testing.T) {
	testutil.SetupTestPorts(t)
	testutil.SetupTestFlasherFactory(t)
	testutil.SetupFastWaitForPort(t)
	testutil.SetupTestListPorts(t)

	testMgr := serial.NewManager()
	testMgr.SetTestState(true, "/dev/ttyUSB0", 115200, nil)
	session.InsertPort("/dev/ttyUSB0", session.NewPortSession(testMgr, "/dev/ttyUSB0", 115200, session.ModeReader))

	orig := bootCaptureWait
	bootCaptureWait = func(d time.Duration) { testMgr.AddToBuffer("boot: reset") }
	t.Cleanup(func() { bootCaptureWait = orig })

	setFlasher(t, &testutil.MockFlasher{})

	origList := session.SetListPortsFn(func() ([]serial.PortInfo, error) {
		return []serial.PortInfo{{Name: "/dev/ttyUSB0"}}, nil
	})
	t.Cleanup(func() { session.SetListPortsFn(origList) })

	h := newHarness(t)
	result, err := h.CallTool(context.Background(), "esp_reset", map[string]any{
		"port": "/dev/ttyUSB0", "boot_wait": float64(0.001),
	})
	require.NoError(t, err)
	text := testkit.ResultText(result)
	assert.Contains(t, text, "boot_output")
	assert.Contains(t, text, "device reset")
}

func TestHandleFlashBootWaitZero(t *testing.T) {
	testutil.SetupTestPorts(t)
	testutil.SetupTestFlasherFactory(t)
	testutil.SetupFastWaitForPort(t)
	testutil.SetupTestListPorts(t)
	setupFastBootCapture(t)

	setFlasher(t, &testutil.MockFlasher{})

	origList := session.SetListPortsFn(func() ([]serial.PortInfo, error) {
		return []serial.PortInfo{{Name: "/dev/ttyUSB0"}}, nil
	})
	t.Cleanup(func() { session.SetListPortsFn(origList) })

	tmpDir := t.TempDir()
	fw := tmpDir + "/firmware.bin"
	require.NoError(t, os.WriteFile(fw, []byte("firmware"), 0o644))

	h := newHarness(t)
	result, err := h.CallTool(context.Background(), "esp_flash", map[string]any{
		"port": "/dev/ttyUSB0",
		"images": []any{
			map[string]any{"path": fw, "offset": float64(0)},
		},
		"boot_wait": float64(0),
	})
	require.NoError(t, err)
	require.False(t, result.IsError)
	assert.NotContains(t, testkit.ResultText(result), "boot_output")
}
