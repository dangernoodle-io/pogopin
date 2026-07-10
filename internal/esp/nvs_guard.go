package esp

import (
	"fmt"
	"hash/crc32"
	"reflect"

	"tinygo.org/x/espflasher/pkg/nvs"
)

// NVS v2 on-flash constants not exported by pkg/nvs (mirrored here so the
// completeness guard below can walk the raw partition independently of the
// codec it is checking). Values match ESP-IDF's NVS page/entry format.
const (
	nvsPageStateEmpty    = 0xFF // page header byte 0: page never written
	nvsPageVersion       = 0xFE // page header byte 8: NVS v2
	nvsEntryStateWritten = 0x02 // 0b10, 2-bit state in the per-page bitmap
	nvsNamespaceType     = 0x01 // entry type byte for a namespace declaration
	nvsNamespaceIdxSlot  = 0    // namespace declarations always use namespaceIdx 0
)

// nvsEntryKey identifies an NVS entry for pre/post-write comparison.
// ChunkIndex is included so distinct chunks of a chunked value (e.g.
// esp_wifi blob-index credentials) that legitimately share a namespace+key
// are not collapsed together.
type nvsEntryKey struct {
	Namespace  string
	Key        string
	ChunkIndex uint8
}

// nvsEntryMap indexes entries by their full (namespace, key, chunk) identity.
func nvsEntryMap(entries []nvs.Entry) map[nvsEntryKey]nvs.Entry {
	m := make(map[nvsEntryKey]nvs.Entry, len(entries))
	for _, e := range entries {
		m[nvsEntryKey{Namespace: e.Namespace, Key: e.Key, ChunkIndex: e.ChunkIndex}] = e
	}
	return m
}

// nvsEntriesByNSKey indexes entries by (namespace, key) only, ignoring
// ChunkIndex, preserving the order entries appear in the source slice. Used
// by the namespace+key lookups below instead of ranging a map keyed by the
// full (namespace, key, chunk) identity — map iteration order is randomized
// per Go's spec, which would make which chunk of a multi-chunk key gets
// matched (and therefore which coverage branches run) nondeterministic
// between runs. A slice-backed, insertion-ordered index keeps the match
// deterministic (first entry in source order wins, matching prior behavior
// on every partition observed in practice, where a given namespace+key pairs
// with a single chunk).
func nvsEntriesByNSKey(entries []nvs.Entry) map[string][]nvs.Entry {
	m := make(map[string][]nvs.Entry, len(entries))
	for _, e := range entries {
		k := e.Namespace + "\x00" + e.Key
		m[k] = append(m[k], e)
	}
	return m
}

// espCRC32 matches the fork's unexported nvs.espCRC32 (ESP-IDF's page/entry
// CRC): CRC-32/IEEE seeded with 0xFFFFFFFF instead of the usual 0.
func espCRC32(data []byte) uint32 {
	return crc32.Update(0xFFFFFFFF, crc32.IEEETable, data)
}

// validNVSPages returns the byte-offset of every page nvs.ParseNVS would
// itself treat as active (mirrors its page-validity check: skip pages in
// the Empty state, skip pages whose version byte doesn't match, and
// (redundantly, for defense-in-depth) verify the header CRC32). By the time
// callers reach this, nvs.ParseNVS has already succeeded against the same
// bytes, so a CRC failure here would indicate the two implementations have
// diverged rather than a genuinely corrupt partition.
func validNVSPages(data []byte) ([][]byte, error) {
	if len(data)%nvs.PageSize != 0 {
		return nil, fmt.Errorf("nvs data length (%d) is not a multiple of page size (%d)", len(data), nvs.PageSize)
	}

	totalPages := len(data) / nvs.PageSize
	pages := make([][]byte, 0, totalPages)
	for p := 0; p < totalPages; p++ {
		page := data[p*nvs.PageSize : (p+1)*nvs.PageSize]
		if page[0] == nvsPageStateEmpty {
			continue
		}
		if page[8] != nvsPageVersion {
			continue
		}
		expectedCRC := uint32(page[28]) | uint32(page[29])<<8 | uint32(page[30])<<16 | uint32(page[31])<<24
		if espCRC32(page[4:28]) != expectedCRC {
			return nil, fmt.Errorf("nvs completeness guard: page header CRC mismatch on a page nvs.ParseNVS accepted — codec/guard have diverged")
		}
		pages = append(pages, page)
	}
	return pages, nil
}

// countWrittenSlots is the ground-truth count of entry slots marked Written
// in the per-page bitmap, summed across every page nvs.ParseNVS would scan.
// It is a raw bitmap popcount — independent of any structural/span
// interpretation — so it stays correct even if the structural walk used
// elsewhere (here or in the pinned codec) has a bug.
func countWrittenSlots(pages [][]byte) int {
	total := 0
	for _, page := range pages {
		for slot := 0; slot < nvs.EntriesPerPage; slot++ {
			bitIndex := uint(slot) * 2
			byteIdx := nvs.HeaderSize + int(bitIndex/8)
			bitOffset := bitIndex % 8
			state := (page[byteIdx] >> bitOffset) & 0x3
			if state == nvsEntryStateWritten {
				total++
			}
		}
	}
	return total
}

// countNamespaceDeclarationSlots structurally walks the same valid pages
// (bitmap + span, mirroring nvs.ParseNVS's own walk closely enough to keep
// the counts comparable) and sums the slots consumed by namespace
// declaration entries. nvs.ParseNVS resolves these internally to build its
// namespace-index map and never includes them in the []Entry it returns, so
// accounting purely from parsed entries would always under-count a real
// partition by its declared-namespace slots.
func countNamespaceDeclarationSlots(pages [][]byte) int {
	total := 0
	for _, page := range pages {
		processed := make(map[int]bool)
		for slot := 0; slot < nvs.EntriesPerPage; slot++ {
			if processed[slot] {
				continue
			}
			bitIndex := uint(slot) * 2
			byteIdx := nvs.HeaderSize + int(bitIndex/8)
			bitOffset := bitIndex % 8
			state := (page[byteIdx] >> bitOffset) & 0x3
			if state != nvsEntryStateWritten {
				continue
			}

			entryOffset := nvs.FirstEntryOffset + slot*nvs.EntrySize
			if entryOffset+nvs.EntrySize > len(page) {
				continue
			}
			entryBytes := page[entryOffset : entryOffset+nvs.EntrySize]
			namespaceIdx := entryBytes[0]
			entryType := entryBytes[1]
			span := entryBytes[2]
			if span == 0 {
				span = 1
			}

			contNeeded := int(span) - 1
			s := slot + 1
			for contNeeded > 0 && s < nvs.EntriesPerPage {
				processed[s] = true
				s++
				contNeeded--
			}
			if contNeeded > 0 {
				// Mirror nvs.ParseNVS: a span claiming more continuation
				// slots than remain on this page cannot come from a real
				// ESP-IDF partition (an item's header + continuations are
				// always fully contained on one page). Clamp to what's
				// actually present so this guard's accounting can't diverge
				// from the codec on a corrupted partition.
				span -= uint8(contNeeded)
			}

			if namespaceIdx == nvsNamespaceIdxSlot && entryType == nvsNamespaceType {
				total += int(span)
			}
		}
	}
	return total
}

// dataEntriesFor returns the number of 32-byte continuation slots needed to
// hold n bytes of payload, matching nvs.GenerateNVS's "1 header + ceil(n /
// EntrySize) data entries" layout (minimum 1 data entry, even for n==0).
func dataEntriesFor(n int) int {
	if n <= 0 {
		return 1
	}
	return (n + nvs.EntrySize - 1) / nvs.EntrySize
}

// entrySlotSpan returns the number of 32-byte slots (header included) an
// entry occupies on the wire. nvs.ParseNVS always populates Span for Raw
// entries; for the types it decodes natively (primitives, string, blob) it
// leaves Span at its zero value, so this falls back to computing the span
// the same way nvs.GenerateNVS does.
func entrySlotSpan(e nvs.Entry) int {
	if e.Span > 0 {
		return int(e.Span)
	}
	switch e.Type {
	case "string":
		s, _ := e.Value.(string)
		return 1 + dataEntriesFor(len(s)+1) // +1 for the null terminator
	case "blob":
		b, _ := e.Value.([]byte)
		return 1 + dataEntriesFor(len(b))
	default:
		// "raw" is handled by the e.Span>0 fast-path above: nvs.ParseNVS
		// always populates Span for Raw entries, so a Raw entry never
		// reaches this switch.
		return 1
	}
}

// accountedSlots sums the slot span of every parsed entry plus the
// independently-counted namespace-declaration slots.
func accountedSlots(entries []nvs.Entry, namespaceSlots int) int {
	total := namespaceSlots
	for _, e := range entries {
		total += entrySlotSpan(e)
	}
	return total
}

// verifyLosslessParse is the pre-write completeness guard: it confirms
// every entry slot the device's raw NVS bitmap reports as Written was
// accounted for by nvs.ParseNVS before a caller generates a replacement
// partition image from the parsed result. If the parse dropped or
// miscounted anything — whether from a codec regression or a partition
// layout nvs.ParseNVS doesn't fully understand — writing back only the
// parsed entries would silently erase the unaccounted data. This is the
// last line of defense against that regardless of *why* the parse came up
// short, and it refuses (aborts, never flashes) rather than risk it.
func verifyLosslessParse(raw []byte, entries []nvs.Entry) error {
	pages, err := validNVSPages(raw)
	if err != nil {
		return fmt.Errorf("nvs completeness guard: %w", err)
	}

	total := countWrittenSlots(pages)
	nsSlots := countNamespaceDeclarationSlots(pages)
	accounted := accountedSlots(entries, nsSlots)

	if total > accounted {
		return fmt.Errorf("refusing to write: NVS parse is lossy (accounted %d of %d written entry slots); aborting to avoid data loss", accounted, total)
	}
	return nil
}

// verifySetApplied checks post-write state after an NVSSetBatch write: every
// requested update must be present with its new value, and every
// pre-existing entry not targeted by an update must still be present.
// Returns the number of verified updates on success.
func verifySetApplied(pre, post []nvs.Entry, updates []NVSUpdate) (int, error) {
	postMap := nvsEntryMap(post)
	postByNSKey := nvsEntriesByNSKey(post)

	touched := make(map[string]bool, len(updates))
	for _, u := range updates {
		touched[u.Namespace+"\x00"+u.Key] = true
	}

	applied := 0
	for _, u := range updates {
		candidates := postByNSKey[u.Namespace+"\x00"+u.Key]
		if len(candidates) == 0 {
			return 0, fmt.Errorf("post-write verify: %s.%s not present after write", u.Namespace, u.Key)
		}
		e := candidates[0]
		if !reflect.DeepEqual(e.Value, u.Value) {
			return 0, fmt.Errorf("post-write verify: %s.%s value mismatch after write (want %#v, got %#v)", u.Namespace, u.Key, u.Value, e.Value)
		}
		applied++
	}

	for _, e := range pre {
		if touched[e.Namespace+"\x00"+e.Key] {
			continue
		}
		k := nvsEntryKey{Namespace: e.Namespace, Key: e.Key, ChunkIndex: e.ChunkIndex}
		if _, ok := postMap[k]; !ok {
			return 0, fmt.Errorf("post-write verify: pre-existing %s.%s was lost during write", e.Namespace, e.Key)
		}
	}

	return applied, nil
}

// verifyDeleteApplied checks post-write state after an NVSDelete write: the
// deleted key (or, if key is empty, every key in the deleted namespace)
// must be absent, and every pre-existing entry outside the deletion target
// must still be present. Returns the number of verified-deleted entries on
// success.
func verifyDeleteApplied(pre, post []nvs.Entry, namespace, key string) (int, error) {
	postMap := nvsEntryMap(post)

	deleted := 0
	for _, e := range pre {
		if e.Namespace != namespace {
			continue
		}
		if key != "" && e.Key != key {
			continue
		}
		k := nvsEntryKey{Namespace: e.Namespace, Key: e.Key, ChunkIndex: e.ChunkIndex}
		if _, ok := postMap[k]; ok {
			return 0, fmt.Errorf("post-write verify: %s.%s still present after delete", e.Namespace, e.Key)
		}
		deleted++
	}

	for _, e := range pre {
		if e.Namespace == namespace && (key == "" || e.Key == key) {
			continue // intended deletion, already checked above
		}
		k := nvsEntryKey{Namespace: e.Namespace, Key: e.Key, ChunkIndex: e.ChunkIndex}
		if _, ok := postMap[k]; !ok {
			return 0, fmt.Errorf("post-write verify: pre-existing %s.%s was lost during delete", e.Namespace, e.Key)
		}
	}

	return deleted, nil
}
