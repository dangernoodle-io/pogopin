package esp

import (
	"encoding/binary"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func makePartitionEntry(typ, subtype uint8, offset, size uint32, label string) []byte {
	entry := make([]byte, 32)
	binary.LittleEndian.PutUint16(entry[0:2], partitionMagic)
	entry[2] = typ
	entry[3] = subtype
	binary.LittleEndian.PutUint32(entry[4:8], offset)
	binary.LittleEndian.PutUint32(entry[8:12], size)
	copy(entry[12:28], label)
	return entry
}

func TestParsePartitionTable(t *testing.T) {
	// Build a partition table matching TaipanMiner layout
	var table []byte
	table = append(table, makePartitionEntry(1, 0x02, 0x9000, 0x6000, "nvs")...)
	table = append(table, makePartitionEntry(1, 0x00, 0xF000, 0x2000, "otadata")...)
	table = append(table, makePartitionEntry(0, 0x10, 0x20000, 0x1E0000, "ota_0")...)
	table = append(table, makePartitionEntry(0, 0x11, 0x200000, 0x1E0000, "ota_1")...)
	// Add terminator (non-magic bytes)
	table = append(table, make([]byte, 32)...)

	entries := ParsePartitionTable(table)
	require.Len(t, entries, 4)

	assert.Equal(t, "nvs", entries[0].Label)
	assert.Equal(t, uint32(0x9000), entries[0].Offset)
	assert.Equal(t, uint8(1), entries[0].Type)

	assert.Equal(t, "ota_0", entries[2].Label)
	assert.Equal(t, uint32(0x20000), entries[2].Offset)
	assert.Equal(t, uint8(0), entries[2].Type)
}

func TestParsePartitionTableEmpty(t *testing.T) {
	// All 0xFF — no valid entries
	data := make([]byte, 128)
	for i := range data {
		data[i] = 0xFF
	}
	entries := ParsePartitionTable(data)
	assert.Empty(t, entries)
}

func TestParsePartitionTableMD5Marker(t *testing.T) {
	var table []byte
	table = append(table, makePartitionEntry(0, 0x10, 0x20000, 0x1E0000, "ota_0")...)
	// MD5 marker: magic bytes + type 0xFF
	md5Entry := make([]byte, 32)
	binary.LittleEndian.PutUint16(md5Entry[0:2], partitionMagic)
	md5Entry[2] = 0xFF
	table = append(table, md5Entry...)
	table = append(table, makePartitionEntry(0, 0x11, 0x200000, 0x1E0000, "ota_1")...)

	entries := ParsePartitionTable(table)
	require.Len(t, entries, 1)
	assert.Equal(t, "ota_0", entries[0].Label)
}

func TestValidateFlashOffsetsValid(t *testing.T) {
	partitions := []PartitionEntry{
		{Type: 0, Offset: 0x20000, Label: "ota_0"},
		{Type: 0, Offset: 0x200000, Label: "ota_1"},
		{Type: 1, Offset: 0x9000, Label: "nvs"},
	}

	images := []ImageSpec{{Path: "firmware.bin", Offset: 0x20000}}
	err := ValidateFlashOffsets(partitions, images)
	assert.NoError(t, err)
}

func TestValidateFlashOffsetsInvalid(t *testing.T) {
	partitions := []PartitionEntry{
		{Type: 0, Offset: 0x20000, Label: "ota_0"},
		{Type: 0, Offset: 0x200000, Label: "ota_1"},
	}

	images := []ImageSpec{{Path: "firmware.bin", Offset: 0x10000}}
	err := ValidateFlashOffsets(partitions, images)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "0x10000")
	assert.Contains(t, err.Error(), "ota_0")
}
