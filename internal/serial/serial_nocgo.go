//go:build !cgo
// +build !cgo

package serial

import (
	"fmt"
	"runtime"
	"strings"

	"go.bug.st/serial"
)

// IsUSBPort detects if a serial port name corresponds to a USB device using heuristics.
// The detection varies by platform:
// - macOS: /dev/cu.usbmodem* or /dev/cu.usbserial*.
// - Linux: /dev/ttyUSB* or /dev/ttyACM*.
// - Windows: COM* (all COM ports are assumed USB).
// - Other platforms: assumed USB by default.
func IsUSBPort(name string) bool {
	switch runtime.GOOS {
	case "darwin":
		return strings.HasPrefix(name, "/dev/cu.usbmodem") || strings.HasPrefix(name, "/dev/cu.usbserial")
	case "linux":
		return strings.HasPrefix(name, "/dev/ttyUSB") || strings.HasPrefix(name, "/dev/ttyACM")
	case "windows":
		return strings.HasPrefix(name, "COM")
	default:
		// Assume USB on unknown platforms
		return true
	}
}

// ListPorts returns available serial ports.
// Without CGO, USB information is detected using port name heuristics.
// If usbOnly is true, only ports detected as USB are returned.
func ListPorts(usbOnly bool) ([]PortInfo, error) {
	ports, err := serial.GetPortsList()
	if err != nil {
		return nil, fmt.Errorf("failed to get ports: %w", err)
	}

	var result []PortInfo
	for _, name := range ports {
		isUSB := IsUSBPort(name)

		// Filter out non-USB ports if requested
		if usbOnly && !isUSB {
			continue
		}

		result = append(result, PortInfo{
			Name:  name,
			IsUSB: isUSB,
		})
	}

	return result, nil
}
