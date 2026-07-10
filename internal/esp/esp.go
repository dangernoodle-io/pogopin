package esp

import (
	"bytes"
	"fmt"
	"os"
	"strings"

	espflasher "tinygo.org/x/espflasher/pkg/espflasher"
	"tinygo.org/x/espflasher/pkg/nvs"
)

// StatusFunc reports a transport-neutral, phase-labeled status tick for a
// long-running esp operation. phase is a short human-readable step label
// ("reading partition", "parsing", "writing", ...); current/total carry
// real byte progress when the step has a byte denominator, or (0, 0) for a
// step that's just a discrete phase transition with no bar to fill. This
// type carries no MCP (or any transport) dependency so it's directly
// reusable by a future standalone CLI adapter — callers pass nil to opt out
// entirely, which every function accepting a StatusFunc treats identically
// to today's silent no-op behavior.
type StatusFunc func(phase string, current, total int)

// Status phase names emitted through StatusFunc. These are the single
// source of truth for phase labels — callers that classify or order phases
// (e.g. internal/mcpserver's nvsPhaseOrdinal) must key off these constants,
// not duplicate string literals, so a new phase added here can't silently
// fall through an unmatched classification elsewhere.
const (
	StatusPhaseReadingPartition      = "reading partition"
	StatusPhaseParsing               = "parsing"
	StatusPhaseVerifyingCompleteness = "verifying completeness"
	StatusPhaseWriting               = "writing"
	StatusPhaseReadingBack           = "reading back"
	StatusPhaseVerifying             = "verifying"
	// StatusPhaseComputingHash is emitted around GetFlashMD5's opaque hash
	// computation. GetFlashMD5 itself has no byte-progress hook yet (a later
	// phase adds one upstream) so this is a coarse before/after marker, not a
	// bar.
	StatusPhaseComputingHash = "computing hash"
	// StatusPhaseResetting is emitted by ResetESP immediately before the
	// actual chip reset.
	StatusPhaseResetting = "resetting"
	// StatusPhaseCapturingBoot is emitted by callers (e.g. handleReset,
	// flash_external) around their post-op boot-output capture step. Lives
	// here, not just in mcpserver, so it's part of the same reusable phase
	// vocabulary a future CLI adapter can render.
	StatusPhaseCapturingBoot = "capturing boot"
	// StatusPhaseComplete is a terminal tick emitted at the end of an
	// orchestration that has no other natural final phase.
	StatusPhaseComplete = "complete"
)

// emitStatus is a nil-safe StatusFunc invocation helper for a discrete
// phase-transition tick with no byte denominator (current=0, total=0).
// Byte-denominated phases instead wire statusProgress directly onto the
// operation's real progress callback.
func emitStatus(status StatusFunc, phase string) {
	if status == nil {
		return
	}
	status(phase, 0, 0)
}

// statusProgress adapts a StatusFunc into a byte-progress callback
// (compatible with both espflasher.ProgressFunc and FlashESP's own
// progress parameter, which share the same underlying func(int, int)
// signature) that forwards current/total under a fixed phase label.
// Returns nil when status is nil, so it can be passed straight through
// nil-safe progress parameters unchanged.
func statusProgress(status StatusFunc, phase string) func(current, total int) {
	if status == nil {
		return nil
	}
	return func(current, total int) {
		status(phase, current, total)
	}
}

// FlasherFactory creates an espflasher instance. Injected for testing.
type FlasherFactory func(port string, opts *espflasher.FlasherOptions) (Flasher, error)

// Flasher interface wraps espflasher methods for testability.
type Flasher interface {
	FlashImages(images []espflasher.ImagePart, progress espflasher.ProgressFunc) error
	EraseFlash(progress espflasher.ProgressFunc) error
	EraseRegion(offset, size uint32, progress espflasher.ProgressFunc) error
	FlashID() (uint8, uint16, error)
	ChipType() espflasher.ChipType
	ChipName() string
	BootloaderFlashOffset() (uint32, bool)
	Reset()
	Close() error
	ReadRegister(address uint32) (uint32, error)
	WriteRegister(address, value uint32) error
	GetSecurityInfo() (*espflasher.SecurityInfo, error)
	GetFlashMD5(offset, size uint32) (string, error)
	ReadFlash(offset, size uint32, progress espflasher.ProgressFunc) ([]byte, error)
	FlushInput()
}

// DefaultFlasherFactory creates a real espflasher.
func DefaultFlasherFactory(port string, opts *espflasher.FlasherOptions) (Flasher, error) {
	return espflasher.New(port, opts)
}

// ImageSpec represents a firmware image to be flashed.
type ImageSpec struct {
	Path   string `json:"path"`
	Offset uint32 `json:"offset"`
}

// FlashOptions configures ESP flashing.
type FlashOptions struct {
	BaudRate      int    `json:"baud_rate"`
	FlashBaudRate int    `json:"flash_baud_rate"`
	ResetMode     string `json:"reset_mode"`    // "auto" (default), "default", "usb_jtag", "no_reset"
	FlashMode     string `json:"flash_mode"`    // "dio", "dout", "qio", "qout"
	FlashSize     string `json:"flash_size"`    // "1MB", "2MB", "4MB", etc.
	ChipType      string `json:"chip_type"`     // "" = auto-detect
	ForceOffsets  bool   `json:"force_offsets"` // skip partition-table offset validation (factory-flash, recovery)
}

// FlashResult reports the outcome of a flash operation.
type FlashResult struct {
	BytesWritten int    `json:"bytes_written"`
	Log          string `json:"log,omitempty"`
}

// ChipInfoResult contains chip identification.
type ChipInfoResult struct {
	ChipName       string `json:"chip_name"`
	ManufacturerID uint8  `json:"manufacturer_id"`
	DeviceID       uint16 `json:"device_id"`
}

// RegisterResult contains a register read result with hex formatting.
type RegisterResult struct {
	Address uint32 `json:"address"`
	Value   uint32 `json:"value"`
	Hex     string `json:"hex"` // "0x..." formatted value for readability
}

// SecurityInfoResult contains security info from the ESP chip.
type SecurityInfoResult struct {
	Flags         uint32   `json:"flags"`
	FlashCryptCnt uint8    `json:"flash_crypt_cnt"`
	KeyPurposes   [7]uint8 `json:"key_purposes"`
	ChipID        *uint32  `json:"chip_id"`
	APIVersion    *uint32  `json:"api_version"`
}

// FlashMD5Result contains the MD5 hash of a flash region.
type FlashMD5Result struct {
	Offset uint32 `json:"offset"`
	Size   uint32 `json:"size"`
	MD5    string `json:"md5"`
}

// ReadFlashResult contains raw bytes read from flash.
type ReadFlashResult struct {
	Offset uint32 `json:"offset"`
	Size   uint32 `json:"size"`
	Data   []byte `json:"data"`
}

// EraseOptions configures ESP erase operations.
type EraseOptions struct {
	BaudRate  int     `json:"baud_rate"`
	Offset    *uint32 `json:"offset"`     // nil = erase entire chip
	Size      *uint32 `json:"size"`       // required if Offset is set
	ResetMode string  `json:"reset_mode"` // "auto" (default), "default", "usb_jtag", "no_reset"
}

// parseResetMode converts a string to espflasher.ResetMode enum.
func parseResetMode(s string) espflasher.ResetMode {
	switch strings.ToLower(s) {
	case "usb_jtag", "usb-jtag":
		return espflasher.ResetUSBJTAG
	case "no_reset", "no-reset":
		return espflasher.ResetNoReset
	case "default":
		return espflasher.ResetDefault
	default:
		return espflasher.ResetAuto
	}
}

// parseChipType converts a string to espflasher.ChipType enum.
func parseChipType(s string) espflasher.ChipType {
	switch strings.ToLower(s) {
	case "esp32":
		return espflasher.ChipESP32
	case "esp32s2":
		return espflasher.ChipESP32S2
	case "esp32s3":
		return espflasher.ChipESP32S3
	case "esp32c2":
		return espflasher.ChipESP32C2
	case "esp32c3":
		return espflasher.ChipESP32C3
	case "esp32c5":
		return espflasher.ChipESP32C5
	case "esp32c6":
		return espflasher.ChipESP32C6
	case "esp32h2":
		return espflasher.ChipESP32H2
	case "esp32p4-rev1":
		return espflasher.ChipESP32P4Rev1
	case "esp8266":
		return espflasher.ChipESP8266
	default:
		return espflasher.ChipAuto
	}
}

// normalizeFlashMode normalizes a flash mode string.
func normalizeFlashMode(s string) string {
	s = strings.ToLower(s)
	switch s {
	case "qio", "qout", "dio", "dout":
		return s
	default:
		return "" // Empty string means keep existing
	}
}

// FlashESP flashes firmware images to an ESP chip. progress, if non-nil, is
// invoked with cumulative bytes-written / total-bytes as flashing proceeds.
func FlashESP(factory FlasherFactory, port string, images []ImageSpec, opts FlashOptions, progress func(current, total int)) (FlashResult, error) {
	if opts.BaudRate == 0 {
		opts.BaudRate = 115200
	}
	if opts.FlashBaudRate == 0 {
		opts.FlashBaudRate = 460800
	}

	flashOpts := espflasher.DefaultOptions()
	flashOpts.BaudRate = opts.BaudRate
	flashOpts.FlashBaudRate = opts.FlashBaudRate
	flashOpts.ResetMode = parseResetMode(opts.ResetMode)
	flashOpts.FlashMode = normalizeFlashMode(opts.FlashMode)
	flashOpts.ChipType = parseChipType(opts.ChipType)
	flashOpts.FlashSize = opts.FlashSize

	var logBuf bytes.Buffer
	flashOpts.Logger = &loggerAdapter{w: &logBuf}

	f, err := factory(port, flashOpts)
	if err != nil {
		return FlashResult{}, err
	}
	defer func() { _ = f.Close() }()

	// Validate image offsets against device partition table
	if !opts.ForceOffsets {
		var ptData []byte
		var ptErr error
		// prefer in-flight partition table
		for _, img := range images {
			if img.Offset == partitionTableOffset {
				ptData, ptErr = os.ReadFile(img.Path)
				break
			}
		}
		if ptData == nil {
			ptData, ptErr = f.ReadFlash(partitionTableOffset, partitionTableSize, nil)
		}
		if ptErr == nil {
			partitions := ParsePartitionTable(ptData)
			if len(partitions) > 0 {
				bootOffset, bootOK := f.BootloaderFlashOffset()
				if err := ValidateFlashOffsets(partitions, images, bootOffset, bootOK); err != nil {
					return FlashResult{}, err
				}
			}
		}
	}
	// If ReadFlash fails or ForceOffsets is true, skip validation

	imageParts := make([]espflasher.ImagePart, len(images))
	totalBytes := 0
	for i, spec := range images {
		data, err := os.ReadFile(spec.Path)
		if err != nil {
			return FlashResult{}, fmt.Errorf("failed to read image %s: %w", spec.Path, err)
		}
		imageParts[i] = espflasher.ImagePart{
			Data:   data,
			Offset: spec.Offset,
		}
		totalBytes += len(data)
	}

	cb := progress
	if cb == nil {
		cb = func(int, int) {}
	}

	err = f.FlashImages(imageParts, cb)
	if err != nil {
		return FlashResult{}, err
	}

	f.Reset()

	return FlashResult{
		BytesWritten: totalBytes,
		Log:          logBuf.String(),
	}, nil
}

// loggerAdapter adapts bytes.Buffer to espflasher.Logger interface.
type loggerAdapter struct {
	w *bytes.Buffer
}

func (la *loggerAdapter) Logf(format string, args ...interface{}) {
	_, _ = fmt.Fprintf(la.w, format+"\n", args...)
}

// EraseESP erases flash memory on an ESP chip. progress, if non-nil, is
// forwarded to the fork's opt-in ETA callback during the erase operation.
func EraseESP(factory FlasherFactory, port string, opts EraseOptions, progress espflasher.ProgressFunc) error {
	if opts.BaudRate == 0 {
		opts.BaudRate = 115200
	}

	flashOpts := espflasher.DefaultOptions()
	flashOpts.BaudRate = opts.BaudRate
	flashOpts.ResetMode = parseResetMode(opts.ResetMode)

	f, err := factory(port, flashOpts)
	if err != nil {
		return err
	}
	defer func() {
		f.Reset()
		_ = f.Close()
	}()

	if opts.Offset == nil {
		err = f.EraseFlash(progress)
	} else if opts.Size != nil {
		err = f.EraseRegion(*opts.Offset, *opts.Size, progress)
	} else {
		return fmt.Errorf("EraseRegion requires both offset and size")
	}

	return err
}

// GetChipInfo retrieves chip information from an ESP device.
func GetChipInfo(factory FlasherFactory, port string, baudRate int, resetMode string) (ChipInfoResult, error) {
	if baudRate == 0 {
		baudRate = 115200
	}

	flashOpts := espflasher.DefaultOptions()
	flashOpts.BaudRate = baudRate
	flashOpts.ChipType = espflasher.ChipAuto
	flashOpts.ResetMode = parseResetMode(resetMode)

	f, err := factory(port, flashOpts)
	if err != nil {
		return ChipInfoResult{}, err
	}
	defer func() {
		f.Reset()
		_ = f.Close()
	}()

	mfgID, devID, err := f.FlashID()
	if err != nil {
		return ChipInfoResult{}, err
	}

	return ChipInfoResult{
		ChipName:       f.ChipName(),
		ManufacturerID: mfgID,
		DeviceID:       devID,
	}, nil
}

// ReadRegister reads a 32-bit register from an ESP chip.
func ReadRegister(factory FlasherFactory, port string, address uint32, baudRate int, resetMode string) (RegisterResult, error) {
	if baudRate == 0 {
		baudRate = 115200
	}

	flashOpts := espflasher.DefaultOptions()
	flashOpts.BaudRate = baudRate
	flashOpts.ChipType = espflasher.ChipAuto
	flashOpts.ResetMode = parseResetMode(resetMode)

	f, err := factory(port, flashOpts)
	if err != nil {
		return RegisterResult{}, err
	}
	defer func() {
		f.Reset()
		_ = f.Close()
	}()

	value, err := f.ReadRegister(address)
	if err != nil {
		return RegisterResult{}, err
	}

	return RegisterResult{
		Address: address,
		Value:   value,
		Hex:     fmt.Sprintf("0x%08X", value),
	}, nil
}

// WriteRegister writes a 32-bit value to a register on an ESP chip.
func WriteRegister(factory FlasherFactory, port string, address, value uint32, baudRate int, resetMode string) error {
	if baudRate == 0 {
		baudRate = 115200
	}

	flashOpts := espflasher.DefaultOptions()
	flashOpts.BaudRate = baudRate
	flashOpts.ChipType = espflasher.ChipAuto
	flashOpts.ResetMode = parseResetMode(resetMode)

	f, err := factory(port, flashOpts)
	if err != nil {
		return err
	}
	defer func() {
		f.Reset()
		_ = f.Close()
	}()

	return f.WriteRegister(address, value)
}

// ResetESP resets an ESP device. status, if non-nil, receives a
// "resetting" tick immediately before the reset is issued. Callers that
// orchestrate further steps after reset (e.g. boot-output capture) reuse the
// same status func directly for their own StatusPhaseCapturingBoot/
// StatusPhaseComplete ticks — ResetESP's job ends at the reset itself.
func ResetESP(factory FlasherFactory, port string, resetMode string, status StatusFunc) error {
	flashOpts := espflasher.DefaultOptions()
	flashOpts.BaudRate = 115200
	flashOpts.ResetMode = parseResetMode(resetMode)

	f, err := factory(port, flashOpts)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	emitStatus(status, StatusPhaseResetting)
	f.Reset()
	return nil
}

// GetSecurityInfo retrieves security info from an ESP device.
func GetSecurityInfo(factory FlasherFactory, port string, baudRate int, resetMode string) (SecurityInfoResult, error) {
	if baudRate == 0 {
		baudRate = 115200
	}
	flashOpts := espflasher.DefaultOptions()
	flashOpts.BaudRate = baudRate
	flashOpts.ResetMode = parseResetMode(resetMode)

	f, err := factory(port, flashOpts)
	if err != nil {
		return SecurityInfoResult{}, err
	}
	defer func() {
		f.Reset()
		_ = f.Close()
	}()

	info, err := f.GetSecurityInfo()
	if err != nil {
		return SecurityInfoResult{}, err
	}

	return SecurityInfoResult{
		Flags:         info.Flags,
		FlashCryptCnt: info.FlashCryptCnt,
		KeyPurposes:   info.KeyPurposes,
		ChipID:        info.ChipID,
		APIVersion:    info.APIVersion,
	}, nil
}

// GetFlashMD5 computes the MD5 hash of a flash region. status, if non-nil,
// receives a coarse "computing hash" tick before the (currently
// progress-less) hash computation and a "complete" tick after it succeeds.
// f.GetFlashMD5 has no byte-progress hook yet — a later phase adds one
// upstream and threads it through here — so these are before/after markers,
// not a bar.
func GetFlashMD5(factory FlasherFactory, port string, offset, size uint32, baudRate int, resetMode string, status StatusFunc) (FlashMD5Result, error) {
	if baudRate == 0 {
		baudRate = 115200
	}
	flashOpts := espflasher.DefaultOptions()
	flashOpts.BaudRate = baudRate
	flashOpts.ResetMode = parseResetMode(resetMode)

	f, err := factory(port, flashOpts)
	if err != nil {
		return FlashMD5Result{}, err
	}
	defer func() {
		f.Reset()
		_ = f.Close()
	}()

	emitStatus(status, StatusPhaseComputingHash)
	md5, err := f.GetFlashMD5(offset, size)
	if err != nil {
		return FlashMD5Result{}, err
	}
	emitStatus(status, StatusPhaseComplete)

	return FlashMD5Result{
		Offset: offset,
		Size:   size,
		MD5:    md5,
	}, nil
}

// ReadFlashData reads raw bytes from ESP flash. progress, if non-nil, is
// forwarded to the fork's byte-accurate read progress callback.
func ReadFlashData(factory FlasherFactory, port string, offset, size uint32, baudRate int, resetMode string, progress espflasher.ProgressFunc) (ReadFlashResult, error) {
	if baudRate == 0 {
		baudRate = 115200
	}
	flashOpts := espflasher.DefaultOptions()
	flashOpts.BaudRate = baudRate
	flashOpts.ResetMode = parseResetMode(resetMode)

	f, err := factory(port, flashOpts)
	if err != nil {
		return ReadFlashResult{}, err
	}
	defer func() {
		f.Reset()
		_ = f.Close()
	}()

	data, err := f.ReadFlash(offset, size, progress)
	if err != nil {
		return ReadFlashResult{}, err
	}

	return ReadFlashResult{
		Offset: offset,
		Size:   size,
		Data:   data,
	}, nil
}

// ReadNVS reads and parses NVS entries from an ESP device. status, if
// non-nil, receives "reading partition" (with real byte progress) then
// "parsing" phase ticks.
func ReadNVS(factory FlasherFactory, port string, offset, size uint32, baudRate int, namespace string, resetMode string, status StatusFunc) ([]nvs.Entry, error) {
	emitStatus(status, StatusPhaseReadingPartition)
	result, err := ReadFlashData(factory, port, offset, size, baudRate, resetMode, statusProgress(status, StatusPhaseReadingPartition))
	if err != nil {
		return nil, fmt.Errorf("read flash: %w", err)
	}

	emitStatus(status, StatusPhaseParsing)
	entries, err := nvs.ParseNVS(result.Data)
	if err != nil {
		return nil, fmt.Errorf("parse NVS: %w", err)
	}

	// Filter by namespace if specified
	if namespace != "" {
		var filtered []nvs.Entry
		for _, e := range entries {
			if e.Namespace == namespace {
				filtered = append(filtered, e)
			}
		}
		return filtered, nil
	}

	return entries, nil
}

// WriteNVS generates an NVS binary from entries and flashes it to the
// device. status, if non-nil, receives "writing" phase ticks with real byte
// progress as the partition is flashed.
func WriteNVS(factory FlasherFactory, port string, entries []nvs.Entry, offset, size uint32, baudRate int, resetMode string, status StatusFunc) error {
	data, err := nvs.GenerateNVS(entries, int(size))
	if err != nil {
		return fmt.Errorf("generate NVS: %w", err)
	}

	// Flash the NVS partition using FlashESP with a temp file
	tmpFile, err := os.CreateTemp("", "nvs-*.bin")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	defer func() { _ = os.Remove(tmpFile.Name()) }()

	if _, err := tmpFile.Write(data); err != nil {
		_ = tmpFile.Close()
		return fmt.Errorf("write temp file: %w", err)
	}
	_ = tmpFile.Close()

	emitStatus(status, StatusPhaseWriting)
	_, err = FlashESP(factory, port, []ImageSpec{
		{Path: tmpFile.Name(), Offset: offset},
	}, FlashOptions{BaudRate: baudRate, ResetMode: resetMode}, statusProgress(status, StatusPhaseWriting))
	return err
}

// NVSWriteResult reports the actually-verified outcome of a read-modify-
// write NVS operation. Applied is only ever set after a post-write re-read
// of the device confirmed every requested change landed and nothing
// pre-existing was lost — success is never reported on trust alone.
type NVSWriteResult struct {
	Applied int `json:"applied"`
}

// NVSSet reads the current NVS, sets/updates a single key, and writes back.
func NVSSet(factory FlasherFactory, port string, namespace, key, typ string, value interface{}, offset, size uint32, baudRate int, resetMode string, status StatusFunc) (NVSWriteResult, error) {
	return NVSSetBatch(factory, port, []NVSUpdate{
		{Namespace: namespace, Key: key, Type: typ, Value: value},
	}, offset, size, baudRate, resetMode, status)
}

// NVSDelete reads the current NVS, removes a key or namespace, and writes back
// in a single flasher session (one device reset instead of two).
// If key is empty, deletes all entries in the namespace.
//
// Before generating a replacement partition image, verifyLosslessParse
// (BR-53) confirms nvs.ParseNVS accounted for every entry slot the device's
// raw NVS bitmap reports as Written; a lossy parse aborts before anything is
// flashed. After the write, the partition is re-read and re-parsed so the
// reported result reflects what the device actually holds, not what was
// merely requested.
//
// status, if non-nil, receives a phase tick per orchestration step:
// "reading partition" (bytes) -> "parsing" -> "verifying completeness" ->
// "writing" (bytes) -> "reading back" (bytes) -> "verifying".
func NVSDelete(factory FlasherFactory, port string, namespace, key string, offset, size uint32, baudRate int, resetMode string, status StatusFunc) (NVSWriteResult, error) {
	if baudRate == 0 {
		baudRate = 115200
	}

	flashOpts := espflasher.DefaultOptions()
	flashOpts.BaudRate = baudRate
	flashOpts.ResetMode = parseResetMode(resetMode)

	var logBuf bytes.Buffer
	flashOpts.Logger = &loggerAdapter{w: &logBuf}

	f, err := factory(port, flashOpts)
	if err != nil {
		return NVSWriteResult{}, fmt.Errorf("connect: %w", err)
	}
	defer func() {
		f.Reset()
		_ = f.Close()
	}()

	// Read current NVS
	emitStatus(status, StatusPhaseReadingPartition)
	data, err := f.ReadFlash(offset, size, statusProgress(status, StatusPhaseReadingPartition))
	if err != nil {
		return NVSWriteResult{}, fmt.Errorf("read NVS: %w", err)
	}

	emitStatus(status, StatusPhaseParsing)
	entries, err := nvs.ParseNVS(data)
	if err != nil {
		return NVSWriteResult{}, fmt.Errorf("parse NVS: %w", err)
	}

	emitStatus(status, StatusPhaseVerifyingCompleteness)
	if err := verifyLosslessParse(data, entries); err != nil {
		return NVSWriteResult{}, err
	}

	// Filter out deleted entries
	var filtered []nvs.Entry
	for _, e := range entries {
		if key == "" {
			// Delete entire namespace
			if e.Namespace != namespace {
				filtered = append(filtered, e)
			}
		} else {
			// Delete specific key
			if e.Namespace != namespace || e.Key != key {
				filtered = append(filtered, e)
			}
		}
	}

	// Generate and write back
	newData, err := nvs.GenerateNVS(filtered, int(size))
	if err != nil {
		return NVSWriteResult{}, fmt.Errorf("generate NVS: %w", err)
	}

	emitStatus(status, StatusPhaseWriting)
	err = f.FlashImages([]espflasher.ImagePart{
		{Data: newData, Offset: offset},
	}, statusProgress(status, StatusPhaseWriting))
	if err != nil {
		return NVSWriteResult{}, fmt.Errorf("write NVS: %w", err)
	}

	emitStatus(status, StatusPhaseReadingBack)
	verifyData, err := f.ReadFlash(offset, size, statusProgress(status, StatusPhaseReadingBack))
	if err != nil {
		return NVSWriteResult{}, fmt.Errorf("post-write verify: read back NVS: %w", err)
	}
	postEntries, err := nvs.ParseNVS(verifyData)
	if err != nil {
		return NVSWriteResult{}, fmt.Errorf("post-write verify: parse NVS: %w", err)
	}

	emitStatus(status, StatusPhaseVerifying)
	deleted, err := verifyDeleteApplied(entries, postEntries, namespace, key)
	if err != nil {
		return NVSWriteResult{}, err
	}

	return NVSWriteResult{Applied: deleted}, nil
}

// NVSUpdate describes a single key update for batch NVS operations.
type NVSUpdate struct {
	Namespace string
	Key       string
	Type      string
	Value     interface{}
}

// NVSSetBatch reads the current NVS, applies multiple updates, and writes back
// in a single flasher session (one device reset instead of 2N).
//
// Before generating a replacement partition image, verifyLosslessParse
// (BR-53) confirms nvs.ParseNVS accounted for every entry slot the device's
// raw NVS bitmap reports as Written; a lossy parse aborts before anything is
// flashed. After the write, the partition is re-read and re-parsed so the
// reported result reflects what the device actually holds, not what was
// merely requested.
//
// status, if non-nil, receives a phase tick per orchestration step:
// "reading partition" (bytes) -> "parsing" -> "verifying completeness" ->
// "writing" (bytes) -> "reading back" (bytes) -> "verifying".
func NVSSetBatch(factory FlasherFactory, port string, updates []NVSUpdate, offset, size uint32, baudRate int, resetMode string, status StatusFunc) (NVSWriteResult, error) {
	if baudRate == 0 {
		baudRate = 115200
	}

	flashOpts := espflasher.DefaultOptions()
	flashOpts.BaudRate = baudRate
	flashOpts.ResetMode = parseResetMode(resetMode)

	var logBuf bytes.Buffer
	flashOpts.Logger = &loggerAdapter{w: &logBuf}

	f, err := factory(port, flashOpts)
	if err != nil {
		return NVSWriteResult{}, fmt.Errorf("connect: %w", err)
	}
	defer func() {
		f.Reset()
		_ = f.Close()
	}()

	// Read current NVS
	emitStatus(status, StatusPhaseReadingPartition)
	data, err := f.ReadFlash(offset, size, statusProgress(status, StatusPhaseReadingPartition))
	if err != nil {
		return NVSWriteResult{}, fmt.Errorf("read NVS: %w", err)
	}

	emitStatus(status, StatusPhaseParsing)
	entries, err := nvs.ParseNVS(data)
	if err != nil {
		return NVSWriteResult{}, fmt.Errorf("parse NVS: %w", err)
	}

	emitStatus(status, StatusPhaseVerifyingCompleteness)
	if err := verifyLosslessParse(data, entries); err != nil {
		return NVSWriteResult{}, err
	}

	preEntries := append([]nvs.Entry(nil), entries...)

	// Apply all updates (upsert)
	for _, u := range updates {
		found := false
		for i, e := range entries {
			if e.Namespace == u.Namespace && e.Key == u.Key {
				entries[i].Type = u.Type
				entries[i].Value = u.Value
				found = true
				break
			}
		}
		if !found {
			entries = append(entries, nvs.Entry{
				Namespace: u.Namespace,
				Key:       u.Key,
				Type:      u.Type,
				Value:     u.Value,
			})
		}
	}

	// Generate new NVS binary
	newData, err := nvs.GenerateNVS(entries, int(size))
	if err != nil {
		return NVSWriteResult{}, fmt.Errorf("generate NVS: %w", err)
	}

	// Write back in same session
	emitStatus(status, StatusPhaseWriting)
	err = f.FlashImages([]espflasher.ImagePart{
		{Data: newData, Offset: offset},
	}, statusProgress(status, StatusPhaseWriting))
	if err != nil {
		return NVSWriteResult{}, fmt.Errorf("write NVS: %w", err)
	}

	emitStatus(status, StatusPhaseReadingBack)
	verifyData, err := f.ReadFlash(offset, size, statusProgress(status, StatusPhaseReadingBack))
	if err != nil {
		return NVSWriteResult{}, fmt.Errorf("post-write verify: read back NVS: %w", err)
	}
	postEntries, err := nvs.ParseNVS(verifyData)
	if err != nil {
		return NVSWriteResult{}, fmt.Errorf("post-write verify: parse NVS: %w", err)
	}

	emitStatus(status, StatusPhaseVerifying)
	applied, err := verifySetApplied(preEntries, postEntries, updates)
	if err != nil {
		return NVSWriteResult{}, err
	}

	return NVSWriteResult{Applied: applied}, nil
}
