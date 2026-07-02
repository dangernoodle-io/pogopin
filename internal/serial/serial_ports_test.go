package serial

import (
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestIsLikelyUSBSerial(t *testing.T) {
	switch runtime.GOOS {
	case "darwin":
		testIsLikelyUSBSerialDarwin(t)
	case "linux":
		testIsLikelyUSBSerialLinux(t)
	case "windows":
		testIsLikelyUSBSerialWindows(t)
	default:
		testIsLikelyUSBSerialUnknown(t)
	}
}

func testIsLikelyUSBSerialDarwin(t *testing.T) {
	tests := map[string]struct {
		portName string
		want     bool
	}{
		"usbmodem":                {portName: "/dev/cu.usbmodem101", want: true},
		"usbserial":               {portName: "/dev/cu.usbserial-1420", want: true},
		"wchusbserial (CH340)":    {portName: "/dev/cu.wchusbserial1440", want: true},
		"SLAB_USBtoUART (CP210x)": {portName: "/dev/cu.SLAB_USBtoUART", want: true},
		"Bluetooth non-USB":       {portName: "/dev/cu.Bluetooth-PDA-Sync", want: false},
		"ttyS non-USB":            {portName: "/dev/ttyS0", want: false},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			got := IsLikelyUSBSerial(tt.portName)
			assert.Equal(t, tt.want, got, "IsLikelyUSBSerial(%q) on darwin", tt.portName)
		})
	}
}

func testIsLikelyUSBSerialLinux(t *testing.T) {
	tests := map[string]struct {
		portName string
		want     bool
	}{
		"ttyUSB":                {portName: "/dev/ttyUSB0", want: true},
		"ttyACM":                {portName: "/dev/ttyACM0", want: true},
		"ttyS non-USB":          {portName: "/dev/ttyS0", want: false},
		"usbmodem not on Linux": {portName: "/dev/cu.usbmodem101", want: false},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			got := IsLikelyUSBSerial(tt.portName)
			assert.Equal(t, tt.want, got, "IsLikelyUSBSerial(%q) on linux", tt.portName)
		})
	}
}

func testIsLikelyUSBSerialWindows(t *testing.T) {
	tests := map[string]struct {
		portName string
		want     bool
	}{
		"COM3":         {portName: "COM3", want: true},
		"COM1":         {portName: "COM1", want: true},
		"non-COM port": {portName: "/dev/ttyUSB0", want: false},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			got := IsLikelyUSBSerial(tt.portName)
			assert.Equal(t, tt.want, got, "IsLikelyUSBSerial(%q) on windows", tt.portName)
		})
	}
}

func testIsLikelyUSBSerialUnknown(t *testing.T) {
	tests := map[string]struct {
		portName string
		want     bool
	}{
		"unknown port defaults to true": {portName: "/unknown/port", want: true},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			got := IsLikelyUSBSerial(tt.portName)
			assert.Equal(t, tt.want, got, "IsLikelyUSBSerial(%q) on unknown platform", tt.portName)
		})
	}
}

// TestFilterNoisePorts verifies the ListPorts noise denylist: Bluetooth/debug
// virtual ports are dropped while ordinary board ports pass through unchanged.
func TestFilterNoisePorts(t *testing.T) {
	input := []string{
		"/dev/cu.Bluetooth-Incoming-Port",
		"/dev/cu.usbmodem101",
		"/dev/cu.wchusbserial1440",
		"/dev/cu.SomeBoard",
		"/dev/cu.debug-console",
	}

	got := filterNoisePorts(input)

	names := make([]string, len(got))
	for i, p := range got {
		names[i] = p.Name
	}

	assert.NotContains(t, names, "/dev/cu.Bluetooth-Incoming-Port", "Bluetooth port must be excluded")
	assert.NotContains(t, names, "/dev/cu.debug-console", "debug-console port must be excluded")
	assert.Contains(t, names, "/dev/cu.usbmodem101")
	assert.Contains(t, names, "/dev/cu.wchusbserial1440")
	assert.Contains(t, names, "/dev/cu.SomeBoard")
	assert.Len(t, names, 3, "only the three real board ports should remain")
}

func TestIsNoisePort(t *testing.T) {
	tests := map[string]struct {
		name string
		want bool
	}{
		"bluetooth incoming": {name: "/dev/cu.Bluetooth-Incoming-Port", want: true},
		"debug console":      {name: "/dev/cu.debug-console", want: true},
		"usbmodem board":     {name: "/dev/cu.usbmodem101", want: false},
		"wchusbserial board": {name: "/dev/cu.wchusbserial1440", want: false},
		"arbitrary board":    {name: "/dev/cu.SomeBoard", want: false},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tt.want, isNoisePort(tt.name))
		})
	}
}
