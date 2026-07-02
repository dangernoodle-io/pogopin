//go:build cgo
// +build cgo

package serial

import (
	"fmt"

	"go.bug.st/serial/enumerator"
)

// IsUSBPort detects if a serial port is a USB device.
// With CGO enabled, uses the detailed port enumeration from enumerator when available.
// Falls back to the shared heuristic (see usbPortNameHeuristic in serial.go)
// if enumeration fails or the port is not found.
func IsUSBPort(name string) bool {
	ports, err := enumerator.GetDetailedPortsList()
	if err == nil {
		for _, p := range ports {
			if p.Name == name {
				return p.IsUSB
			}
		}
	}

	// Fall back to heuristic matching if port not found in enumeration
	return usbPortNameHeuristic(name)
}

// ListPorts returns detailed port information from the system.
// With CGO enabled, includes USB device details (VID, PID, serial number, product name).
// The usbOnly parameter filters results to USB devices only.
func ListPorts(usbOnly bool) ([]PortInfo, error) {
	ports, err := enumerator.GetDetailedPortsList()
	if err != nil {
		return nil, fmt.Errorf("failed to list ports: %w", err)
	}

	var result []PortInfo
	for _, p := range ports {
		if usbOnly && !p.IsUSB {
			continue
		}

		result = append(result, PortInfo{
			Name:         p.Name,
			IsUSB:        p.IsUSB,
			VID:          p.VID,
			PID:          p.PID,
			SerialNumber: p.SerialNumber,
			Product:      p.Product,
		})
	}

	return result, nil
}
