package esp

import (
	"bytes"
	"fmt"
	"os"
	"strings"

	espflasher "tinygo.org/x/espflasher/pkg/espflasher"
	"tinygo.org/x/espflasher/pkg/nvs"
)

// FlasherFactory creates an espflasher instance. Injected for testing.
type FlasherFactory func(port string, opts *espflasher.FlasherOptions) (Flasher, error)

// Flasher interface wraps espflasher methods for testability.
type Flasher interface {
	FlashImages(images []espflasher.ImagePart, progress espflasher.ProgressFunc) error
	EraseFlash() error
	EraseRegion(offset, size uint32) error
	FlashID() (uint8, uint16, error)
	ChipType() espflasher.ChipType
	ChipName() string
	Reset()
	Close() error
	ReadRegister(address uint32) (uint32, error)
	WriteRegister(address, value uint32) error
	GetSecurityInfo() (*espflasher.SecurityInfo, error)
	FlashMD5(offset, size uint32) (string, error)
	ReadFlash(offset, size uint32) ([]byte, error)
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
	ResetMode     string `json:"reset_mode"` // "auto" (default), "default", "usb_jtag", "no_reset"
	FlashMode     string `json:"flash_mode"` // "dio", "dout", "qio", "qout"
	FlashSize     string `json:"flash_size"` // "1MB", "2MB", "4MB", etc.
	ChipType      string `json:"chip_type"`  // "" = auto-detect
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

// FlashESP flashes firmware images to an ESP chip.
func FlashESP(factory FlasherFactory, port string, images []ImageSpec, opts FlashOptions) (FlashResult, error) {
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
	ptData, ptErr := f.ReadFlash(partitionTableOffset, partitionTableSize)
	if ptErr == nil {
		partitions := ParsePartitionTable(ptData)
		if len(partitions) > 0 {
			if err := ValidateFlashOffsets(partitions, images); err != nil {
				return FlashResult{}, err
			}
		}
	}
	// If ReadFlash fails, skip validation (device may not have a partition table yet)

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

	err = f.FlashImages(imageParts, func(current, total int) {})
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

// EraseESP erases flash memory on an ESP chip.
func EraseESP(factory FlasherFactory, port string, opts EraseOptions) error {
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
		err = f.EraseFlash()
	} else if opts.Size != nil {
		err = f.EraseRegion(*opts.Offset, *opts.Size)
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

// ResetESP resets an ESP device.
func ResetESP(factory FlasherFactory, port string, resetMode string) error {
	flashOpts := espflasher.DefaultOptions()
	flashOpts.BaudRate = 115200
	flashOpts.ResetMode = parseResetMode(resetMode)

	f, err := factory(port, flashOpts)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

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

// GetFlashMD5 computes the MD5 hash of a flash region.
func GetFlashMD5(factory FlasherFactory, port string, offset, size uint32, baudRate int, resetMode string) (FlashMD5Result, error) {
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

	md5, err := f.FlashMD5(offset, size)
	if err != nil {
		return FlashMD5Result{}, err
	}

	return FlashMD5Result{
		Offset: offset,
		Size:   size,
		MD5:    md5,
	}, nil
}

// ReadFlashData reads raw bytes from ESP flash.
func ReadFlashData(factory FlasherFactory, port string, offset, size uint32, baudRate int, resetMode string) (ReadFlashResult, error) {
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

	data, err := f.ReadFlash(offset, size)
	if err != nil {
		return ReadFlashResult{}, err
	}

	return ReadFlashResult{
		Offset: offset,
		Size:   size,
		Data:   data,
	}, nil
}

// ReadNVS reads and parses NVS entries from an ESP device.
func ReadNVS(factory FlasherFactory, port string, offset, size uint32, baudRate int, namespace string, resetMode string) ([]nvs.Entry, error) {
	result, err := ReadFlashData(factory, port, offset, size, baudRate, resetMode)
	if err != nil {
		return nil, fmt.Errorf("read flash: %w", err)
	}

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

// WriteNVS generates an NVS binary from entries and flashes it to the device.
func WriteNVS(factory FlasherFactory, port string, entries []nvs.Entry, offset, size uint32, baudRate int, resetMode string) error {
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

	_, err = FlashESP(factory, port, []ImageSpec{
		{Path: tmpFile.Name(), Offset: offset},
	}, FlashOptions{BaudRate: baudRate, ResetMode: resetMode})
	return err
}

// NVSSet reads the current NVS, sets/updates a single key, and writes back.
func NVSSet(factory FlasherFactory, port string, namespace, key, typ string, value interface{}, offset, size uint32, baudRate int, resetMode string) error {
	return NVSSetBatch(factory, port, []NVSUpdate{
		{Namespace: namespace, Key: key, Type: typ, Value: value},
	}, offset, size, baudRate, resetMode)
}

// NVSDelete reads the current NVS, removes a key or namespace, and writes back
// in a single flasher session (one device reset instead of two).
// If key is empty, deletes all entries in the namespace.
func NVSDelete(factory FlasherFactory, port string, namespace, key string, offset, size uint32, baudRate int, resetMode string) error {
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
		return fmt.Errorf("connect: %w", err)
	}
	defer func() {
		f.Reset()
		_ = f.Close()
	}()

	// Read current NVS
	data, err := f.ReadFlash(offset, size)
	if err != nil {
		return fmt.Errorf("read NVS: %w", err)
	}

	entries, err := nvs.ParseNVS(data)
	if err != nil {
		return fmt.Errorf("parse NVS: %w", err)
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
		return fmt.Errorf("generate NVS: %w", err)
	}

	err = f.FlashImages([]espflasher.ImagePart{
		{Data: newData, Offset: offset},
	}, func(current, total int) {})
	if err != nil {
		return fmt.Errorf("write NVS: %w", err)
	}

	return nil
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
func NVSSetBatch(factory FlasherFactory, port string, updates []NVSUpdate, offset, size uint32, baudRate int, resetMode string) error {
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
		return fmt.Errorf("connect: %w", err)
	}
	defer func() {
		f.Reset()
		_ = f.Close()
	}()

	// Read current NVS
	data, err := f.ReadFlash(offset, size)
	if err != nil {
		return fmt.Errorf("read NVS: %w", err)
	}

	entries, err := nvs.ParseNVS(data)
	if err != nil {
		return fmt.Errorf("parse NVS: %w", err)
	}

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
		return fmt.Errorf("generate NVS: %w", err)
	}

	// Write back in same session
	err = f.FlashImages([]espflasher.ImagePart{
		{Data: newData, Offset: offset},
	}, func(current, total int) {})
	if err != nil {
		return fmt.Errorf("write NVS: %w", err)
	}

	return nil
}
