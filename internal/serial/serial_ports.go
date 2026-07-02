package serial

import (
	"fmt"
	"path"
	"runtime"
	"strings"

	"go.bug.st/serial"
)

// noiseSubstrings are case-insensitive basename fragments for ports that are
// never a target board (host virtual/debug endpoints). Kept intentionally tiny.
var noiseSubstrings = []string{"bluetooth", "debug-console"}

// isNoisePort reports whether a port name is host noise (Bluetooth virtual port
// or a debug console) that should never appear in the serial_list surface.
func isNoisePort(name string) bool {
	base := strings.ToLower(path.Base(name))
	for _, frag := range noiseSubstrings {
		if strings.Contains(base, frag) {
			return true
		}
	}
	return false
}

// filterNoisePorts drops host-noise ports from a raw port-name list.
func filterNoisePorts(names []string) []PortInfo {
	result := make([]PortInfo, 0, len(names))
	for _, name := range names {
		if isNoisePort(name) {
			continue
		}
		result = append(result, PortInfo{Name: name})
	}
	return result
}

// ListPorts returns every available serial port minus a minimal host-noise
// denylist (Bluetooth / debug-console). Pure Go, no USB classification.
func ListPorts() ([]PortInfo, error) {
	ports, err := serial.GetPortsList()
	if err != nil {
		return nil, fmt.Errorf("failed to get ports: %w", err)
	}
	return filterNoisePorts(ports), nil
}

// IsLikelyUSBSerial guesses whether a port name looks like a USB serial device
// using platform naming conventions. It is a best-effort HINT only — used
// internally to pick a safer flasher reset mode and retry policy, never exposed
// as truth in the serial_list surface.
//   - macOS: any /dev/cu.* path containing "usb" (case-insensitive) — covers
//     usbmodem, usbserial, CH340's wchusbserial, and CP210x's SLAB_USBtoUART.
//   - Linux: /dev/ttyUSB* or /dev/ttyACM*.
//   - Windows: COM* (all COM ports are assumed USB).
//   - Other platforms: assumed USB by default.
func IsLikelyUSBSerial(name string) bool {
	switch runtime.GOOS {
	case "darwin":
		return strings.HasPrefix(name, "/dev/cu.") && strings.Contains(strings.ToLower(name), "usb")
	case "linux":
		return strings.HasPrefix(name, "/dev/ttyUSB") || strings.HasPrefix(name, "/dev/ttyACM")
	case "windows":
		return strings.HasPrefix(name, "COM")
	default:
		return true
	}
}
