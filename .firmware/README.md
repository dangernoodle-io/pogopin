# Test firmware for pogopin

Minimal ESP32-S3 / ESP32-P4 firmware for hardware testing of pogopin tools. Provides:

- Serial output (1 Hz heartbeat with tick counter and heap info)
- NVS entries seeded on first boot (namespace `test_ns`: counter, name, flag, temperature, initialized)
- NVS counter persistence every 10 ticks
- Serial echo (stdin lines echoed back via `ESP_LOGI`)
- USB CDC console (not UART)

## Prerequisites

- [ESP-IDF](https://docs.espressif.com/projects/esp-idf/en/stable/esp32s3/get-started/) v5.5+
- Python 3.11-3.13 (ESP-IDF requirement)
- cmake (`brew install cmake`)

### ESP-IDF location

If ESP-IDF was installed via PlatformIO, it lives at `~/.platformio/packages/framework-espidf`. The standalone install lives at `~/esp/esp-idf`. Adjust the `IDF_PATH` and `source` commands below accordingly.

### Python 3.14

ESP-IDF v5.5 does not support Python 3.14. If `python3 --version` reports 3.14, create a shim directory that maps `python3` to `python3.13`:

```bash
brew install python@3.13
mkdir -p /tmp/py313-shim
ln -sf $(brew --prefix python@3.13)/libexec/bin/python3 /tmp/py313-shim/python3
export PATH=/tmp/py313-shim:$PATH
```

The shim in `/tmp` is ephemeral. Recreate after reboot.

### First-time setup

After installing ESP-IDF, run `idf_tools.py install` to download required toolchains (compilers, esp-rom-elfs, openocd, etc.):

```bash
export IDF_PATH=~/.platformio/packages/framework-espidf  # or ~/esp/esp-idf
python3 $IDF_PATH/tools/idf_tools.py install
```

## Build

```bash
cd .firmware
export IDF_PATH=~/.platformio/packages/framework-espidf  # or ~/esp/esp-idf
source $IDF_PATH/export.sh

# Build for ESP32-S3 (T-Dongle-S3)
python3 $IDF_PATH/tools/idf.py set-target esp32s3
python3 $IDF_PATH/tools/idf.py build

# Or build for ESP32-P4 (Elecrow CrowPanel HMI 7.0)
# python3 $IDF_PATH/tools/idf.py set-target esp32p4
# python3 $IDF_PATH/tools/idf.py build
```

Note: use `python3 $IDF_PATH/tools/idf.py` instead of bare `idf.py` — the PlatformIO install doesn't add `idf.py` to PATH.

Per-target configuration overlays (console driver, flash size) are defined in `sdkconfig.defaults.esp32s3` and `sdkconfig.defaults.esp32p4`.

Output binaries in `build/`:

| Binary | Flash offset |
|--------|-------------|
| `build/bootloader/bootloader.bin` | `0x0` |
| `build/partition_table/partition-table.bin` | `0x8000` |
| `build/serial-io-test-firmware.bin` | `0x10000` |

## Flash

Using pogopin tools (absolute paths required):

```
esp_flash(port="/dev/cu.usbmodemXXXX", images=[
  {"path": "/absolute/path/to/.firmware/build/bootloader/bootloader.bin", "offset": 0},
  {"path": "/absolute/path/to/.firmware/build/partition_table/partition-table.bin", "offset": 32768},
  {"path": "/absolute/path/to/.firmware/build/serial-io-test-firmware.bin", "offset": 65536}
])
```

If the device has different firmware with a different partition layout, erase first:
1. `esp_erase(port="/dev/cu.usbmodemXXXX")` — wipe flash
2. `esp_flash(port, images=[...])` — flash all three images in one call

The `esp_flash` tool validates offsets against the existing partition table (see `pogopin/internal/esp/partition.go` ValidateFlashOffsets). If validation fails with offset conflicts, erase first.

Or via idf.py:

```bash
cd .firmware
python3 $IDF_PATH/tools/idf.py -p /dev/cu.usbmodemXXXX flash
```

## Partition layout

| Name | Type | Offset | Size |
|------|------|--------|------|
| nvs | data | 0x9000 | 0x6000 (24 KB) |
| factory | app | 0x10000 | 0x300000 (3 MB) |

## NVS test entries

Seeded on first boot in namespace `test_ns`:

| Key | Type | Initial value |
|-----|------|---------------|
| initialized | u8 | 1 |
| counter | u32 | 0 (incremented every 10 ticks) |
| name | string | "serial-io-test" |
| flag | u8 | 1 |
| temperature | i16 | 2500 |

## Manual test plan

Discover the device port with `serial_list(usb_only=true)` — the port path varies across connections. Use absolute paths for firmware images.

### Operational notes

- Start of each run: if the previous run left the chip in a weird state (null NVS, no boot_output, "device not in download mode"), unplug/replug before step 0. Stale USB-Serial-JTAG state is the most common cause of mysterious failures.
- Step 0 must always be `esp_erase` before `esp_flash` when the partition layout might not match what's on the chip — ValidateFlashOffsets rejects bootloader/partition-table offsets otherwise.
- Echo test (`serial_write` → `ESP_LOGI` echo line) works out-of-box under `CONFIG_ESP_CONSOLE_USB_CDC=y` (stdin auto-wired by TinyUSB). Under `CONFIG_ESP_CONSOLE_USB_SERIAL_JTAG=y`, firmware must `usb_serial_jtag_driver_install()` and `usb_serial_jtag_vfs_use_driver()` to get stdin.
- Boot output from `esp_reset` / `esp_erase` may include ticks from prior runs because the monitor buffer accumulates across operations (BR-14); the authoritative signal is `esp_read_flash` MD5 and `esp_read_nvs` for state verification.

### Target reference

| Target           | chip_name       | flash_size | sample register address |
|------------------|-----------------|------------|-------------------------|
| T-Dongle-S3      | ESP32-S3        | 8MB        | 0x60000000 (GPIO base)  |
| Elecrow P4 HMI 7 | ESP32-P4-Rev1   | 16MB       | 0x40000000 (ROM base)   |

### Prerequisites

- Device plugged in via USB
- `serial-io-mcp` built and registered as MCP server
- No other process holding the serial port
- Test firmware built (see Build above)

### 0. Flash test firmware

1. `esp_erase(port="/dev/cu.usbmodemXXXX")` — wipe flash (expect `invalid header: 0xffffffff` in boot output)
2. `esp_flash(port="/dev/cu.usbmodemXXXX", images=[...])` — flash bootloader (0x0), partition table (0x8000), app (0x10000)
3. Verify boot output contains `test-fw:` heartbeat lines

**Note**: If the board has a different partition layout than the test firmware, erase first (step 1), then flash all three images in one `esp_flash` call (step 2). The `esp_flash` tool validates offsets against the existing partition table.

### 1. Serial lifecycle

| Step | Tool | Expected |
|------|------|----------|
| List ports | `serial_list(usb_only=true)` | Device appears with VID/PID |
| Start | `serial_start(port, baud=115200)` | "Started reading" |
| Status | `serial_status(port)` | running=true, reconnecting=false |
| Reset | `esp_reset(port, boot_wait=3)` | Heartbeat in boot_output |
| Read | `serial_read(lines=5)` | Heartbeat lines |
| Write | `serial_write(data="hello")` | Bytes written |
| Read echo | `serial_read(pattern="echo")` | `echo: hello` |
| Stop | `serial_stop(port)` | "Stopped reading" |
| Status after stop | `serial_status(port)` | Error: no serial port open |
| fd check | `lsof <port>` | 0 fds |

**USB CDC note**: `serial_start` automatically resets USB CDC devices (`auto_reset=true` by default) so output flows immediately. Use `auto_reset=false` to skip the reset.

### 2. ESP tools — success path

Start port first, then run each tool. After each, verify `lsof` shows exactly 1 fd.

| Tool | Call | Expected (S3) | Expected (P4) |
|------|------|-------|-------|
| chip_info | `esp_info(port)` | chip_name=ESP32-S3 | chip_name=ESP32-P4-Rev1 |
| flash_md5 | `esp_read_flash(port, offset=0x9000, size=4096, md5=true)` | md5 hash | md5 hash |
| read_nvs | `esp_read_nvs(port, namespace="test_ns")` | 5 entries: name, flag, temperature, initialized, counter | 5 entries (if FW booted once) |
| read_register | `esp_register(port, address=0x60000000)` | (S3: GPIO base) | `esp_register(port, address=0x40000000)` (P4: ROM base) |
| security_info | `esp_info(port, include="security")` | secure_boot, flash_encryption fields | secure_boot, flash_encryption fields |
| reset_esp | `esp_reset(port, boot_wait=3)` | status=success, heartbeat in boot_output | status=success, heartbeat in boot_output |

**NVS note (P4 only)**: The P4 test firmware seeds NVS entries on first boot. If entries do not appear in `esp_read_nvs`, the firmware has not booted once yet. Flash and wait for boot output, then retry.

### 3. Out-of-order execution

Run ESP tools in non-standard order to verify no tool depends on a specific prior call. Start port first.

| Sequence | Tools | Expected |
|----------|-------|----------|
| security_info first | `esp_info(port, include="security")` → `esp_info(port)` | Both succeed |
| read before chip_info | `esp_read_nvs(port, namespace="test_ns")` → `esp_info(port)` | Both succeed |
| register before chip_info | `esp_register(port, address=0x60000000)` → `esp_info(port)` | Both succeed |
| rapid fire | `esp_info(port)` → `esp_register(port, address=0x60000000)` (immediately) | No "Serial port busy" |
| md5 after reset | `esp_reset(port)` → `esp_read_flash(port, offset=0x9000, size=4096, md5=true)` | Both succeed |
| deferred timer recovery | `esp_info(port)` → wait 3s → `esp_info(port)` | Both succeed (deferred timer fires between calls) |

Each sequence should start clean (stop + start port between sequences).

### 4. Recovery

After any ESP tool:
1. `serial_stop(port)` — should succeed or report already stopped
2. `serial_start(port, baud=115200)` — should succeed
3. `serial_status(port)` — running=true, reconnecting=false

### 5. fd leak regression check

Run this sequence and verify fd count after each step:

```bash
# After start (before any ESP tool)
lsof <port> 2>/dev/null | grep pogo | wc -l  # expect: 1

# After esp_info
lsof <port> 2>/dev/null | grep pogo | wc -l  # expect: 1

# After esp_read_nvs
lsof <port> 2>/dev/null | grep pogo | wc -l  # expect: 1

# After stop
lsof <port> 2>/dev/null | grep pogo | wc -l  # expect: 0
```

If any step shows >1 fd or 0 during operation, there is an fd leak or premature close.

### 6. Cross-section out-of-order

Mix serial lifecycle and ESP tools in non-standard sequences. Start from a clean state (no port open).

| Sequence | Steps | Expected |
|----------|-------|----------|
| ESP before read | `serial_start` → `esp_info` → `serial_read(lines=5)` | esp_info succeeds, read returns heartbeat lines |
| write between ESP | `serial_start` → `esp_info` → `serial_write("hello")` → `serial_read(pattern="echo")` | write+echo work after ESP tool |
| double stop | `serial_start` → `esp_info` → `serial_stop` → `serial_stop` | first stop succeeds, second reports already stopped |
| start-stop-start | `serial_start` → `serial_stop` → `serial_start` → `esp_info` | esp_info succeeds on second session |
| ESP after flash | `esp_flash(images)` → `esp_info` | both succeed without manual start |
| status mid-ESP | `serial_start` → `esp_info` → `serial_status` | status shows running=true |
| reset then read | `serial_start` → `esp_reset(boot_wait=3)` → `serial_read(lines=5)` | read returns heartbeat lines from reset |
| auto_reset false | `serial_start(auto_reset=false)` → `serial_read(lines=3)` | no output (CDC not attached); `esp_reset` → read works |

Each sequence starts from a clean state (stop port if open).
