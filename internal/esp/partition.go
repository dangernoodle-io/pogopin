package esp

import (
	"encoding/binary"
	"fmt"
	"strings"
)

const (
	partitionTableOffset = 0x8000
	partitionTableSize   = 0xC00
	partitionEntrySize   = 32
	partitionMagic       = 0x50AA // little-endian for bytes [0xAA, 0x50]
)

// PartitionEntry represents a single entry in the ESP32 partition table.
type PartitionEntry struct {
	Type    uint8
	Subtype uint8
	Offset  uint32
	Size    uint32
	Label   string
	Flags   uint32
}

// ParsePartitionTable parses raw partition table bytes into entries.
// Returns entries found before the first invalid magic or MD5 marker.
func ParsePartitionTable(data []byte) []PartitionEntry {
	var entries []PartitionEntry

	for i := 0; i+partitionEntrySize <= len(data); i += partitionEntrySize {
		entry := data[i : i+partitionEntrySize]

		magic := binary.LittleEndian.Uint16(entry[0:2])
		if magic != partitionMagic {
			break
		}

		typ := entry[2]
		// MD5 checksum marker — end of real entries
		if typ == 0xFF {
			break
		}

		label := strings.TrimRight(string(entry[12:28]), "\x00")

		entries = append(entries, PartitionEntry{
			Type:    typ,
			Subtype: entry[3],
			Offset:  binary.LittleEndian.Uint32(entry[4:8]),
			Size:    binary.LittleEndian.Uint32(entry[8:12]),
			Label:   label,
			Flags:   binary.LittleEndian.Uint32(entry[28:32]),
		})
	}

	return entries
}

// ValidateFlashOffsets checks that each image offset matches a known partition.
// Returns an error listing valid partitions if any offset doesn't match.
func ValidateFlashOffsets(partitions []PartitionEntry, images []ImageSpec) error {
	for _, img := range images {
		matched := false
		for _, p := range partitions {
			if img.Offset == p.Offset {
				matched = true
				break
			}
		}
		if !matched {
			return fmt.Errorf("offset 0x%X does not match any partition; valid partitions: %s",
				img.Offset, formatPartitions(partitions))
		}
	}
	return nil
}

func formatPartitions(partitions []PartitionEntry) string {
	var parts []string
	for _, p := range partitions {
		typeName := "data"
		if p.Type == 0 {
			typeName = "app"
		}
		parts = append(parts, fmt.Sprintf("%s(%s) @ 0x%X", p.Label, typeName, p.Offset))
	}
	return strings.Join(parts, ", ")
}
