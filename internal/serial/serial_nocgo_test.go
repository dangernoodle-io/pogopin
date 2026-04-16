//go:build !cgo
// +build !cgo

package serial

import (
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestIsUSBPort(t *testing.T) {
	switch runtime.GOOS {
	case "darwin":
		testIsUSBPortDarwin(t)
	case "linux":
		testIsUSBPortLinux(t)
	case "windows":
		testIsUSBPortWindows(t)
	default:
		testIsUSBPortUnknown(t)
	}
}

func testIsUSBPortDarwin(t *testing.T) {
	tests := map[string]struct {
		portName string
		want     bool
	}{
		"usbmodem": {
			portName: "/dev/cu.usbmodem101",
			want:     true,
		},
		"usbserial": {
			portName: "/dev/cu.usbserial-1420",
			want:     true,
		},
		"Bluetooth non-USB": {
			portName: "/dev/cu.Bluetooth-PDA-Sync",
			want:     false,
		},
		"ttyS non-USB": {
			portName: "/dev/ttyS0",
			want:     false,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			got := IsUSBPort(tt.portName)
			assert.Equal(t, tt.want, got, "IsUSBPort(%q) on darwin", tt.portName)
		})
	}
}

func testIsUSBPortLinux(t *testing.T) {
	tests := map[string]struct {
		portName string
		want     bool
	}{
		"ttyUSB": {
			portName: "/dev/ttyUSB0",
			want:     true,
		},
		"ttyACM": {
			portName: "/dev/ttyACM0",
			want:     true,
		},
		"ttyS non-USB": {
			portName: "/dev/ttyS0",
			want:     false,
		},
		"usbmodem not on Linux": {
			portName: "/dev/cu.usbmodem101",
			want:     false,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			got := IsUSBPort(tt.portName)
			assert.Equal(t, tt.want, got, "IsUSBPort(%q) on linux", tt.portName)
		})
	}
}

func testIsUSBPortWindows(t *testing.T) {
	tests := map[string]struct {
		portName string
		want     bool
	}{
		"COM3": {
			portName: "COM3",
			want:     true,
		},
		"COM1": {
			portName: "COM1",
			want:     true,
		},
		"non-COM port": {
			portName: "/dev/ttyUSB0",
			want:     false,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			got := IsUSBPort(tt.portName)
			assert.Equal(t, tt.want, got, "IsUSBPort(%q) on windows", tt.portName)
		})
	}
}

func testIsUSBPortUnknown(t *testing.T) {
	tests := map[string]struct {
		portName string
		want     bool
	}{
		"unknown port defaults to true": {
			portName: "/unknown/port",
			want:     true,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			got := IsUSBPort(tt.portName)
			assert.Equal(t, tt.want, got, "IsUSBPort(%q) on unknown platform", tt.portName)
		})
	}
}

func TestListPortsUSBFiltering(t *testing.T) {
	// Note: This is a basic test that verifies the filtering logic.
	// Actual port enumeration depends on the system, so we can't test
	// the full ListPorts function without real hardware or mocking.
	// The test verifies that ListPorts at least returns a result without error.

	ports, err := ListPorts(false)
	assert.NoError(t, err, "ListPorts(false) should not error")
	assert.NotNil(t, ports, "ListPorts(false) should return a slice")

	// When usbOnly is true, all returned ports should have IsUSB: true
	usbPorts, err := ListPorts(true)
	assert.NoError(t, err, "ListPorts(true) should not error")
	for _, port := range usbPorts {
		assert.True(t, port.IsUSB, "ListPorts(true) should only return USB ports, got: %s", port.Name)
	}
}
