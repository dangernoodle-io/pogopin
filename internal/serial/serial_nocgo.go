//go:build !cgo
// +build !cgo

package serial

import (
	"fmt"

	"go.bug.st/serial"
)

// IsUSBPort detects if a serial port name corresponds to a USB device using
// the shared platform heuristic (see usbPortNameHeuristic in serial.go).
func IsUSBPort(name string) bool {
	return usbPortNameHeuristic(name)
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
