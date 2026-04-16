# Test firmware for serial-io-mcp

Minimal ESP32-S3 firmware for hardware testing of serial-io-mcp tools. Provides:

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
python3 $IDF_PATH/tools/idf.py build
```

Note: use `python3 $IDF_PATH/tools/idf.py` instead of bare `idf.py` — the PlatformIO install doesn't add `idf.py` to PATH.

Output binaries in `build/`:

| Binary | Flash offset |
|--------|-------------|
| `build/bootloader/bootloader.bin` | `0x0` |
| `build/partition_table/partition-table.bin` | `0x8000` |
| `build/serial-io-test-firmware.bin` | `0x10000` |

## Flash

Using serial-io-mcp tools (absolute paths required):

```
serial_flash_esp port=/dev/cu.usbmodemXXXX images=[
  {"path": "/absolute/path/to/.firmware/build/bootloader/bootloader.bin", "offset": 0},
  {"path": "/absolute/path/to/.firmware/build/partition_table/partition-table.bin", "offset": 32768},
  {"path": "/absolute/path/to/.firmware/build/serial-io-test-firmware.bin", "offset": 65536}
]
```

If the device has different firmware with a different partition layout, erase first: `serial_erase_esp(port)`. The flasher validates offsets against the existing partition table.

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

### Prerequisites

- Device plugged in via USB
- `serial-io-mcp` built and registered as MCP server
- No other process holding the serial port
- Test firmware built (see Build above)

### 0. Flash test firmware

If the device has other firmware, erase first — `serial_flash_esp` validates offsets against the existing partition table:

1. `serial_erase_esp(port)` — wipe flash (expect `invalid header: 0xffffffff` in boot output)
2. `serial_flash_esp(port, images=[...])` — flash bootloader (0x0), partition table (0x8000), app (0x10000)
3. Verify boot output contains `test-fw:` heartbeat lines

### 1. Serial lifecycle

| Step | Tool | Expected |
|------|------|----------|
| List ports | `serial_list(usb_only=true)` | Device appears with VID/PID |
| Start | `serial_start(port, baud=115200)` | "Started reading" |
| Status | `serial_status(port)` | running=true, reconnecting=false |
| Reset | `serial_reset_esp(port, boot_wait=3)` | Heartbeat in boot_output |
| Read | `serial_read(lines=5)` | Heartbeat lines |
| Write | `serial_write(data="hello")` | Bytes written |
| Read echo | `serial_read(pattern="echo")` | `echo: hello` |
| Stop | `serial_stop(port)` | "Stopped reading" |
| Status after stop | `serial_status(port)` | Error: no serial port open |
| fd check | `lsof <port>` | 0 fds |

**USB CDC note**: `serial_start` automatically resets USB CDC devices (`auto_reset=true` by default) so output flows immediately. Use `auto_reset=false` to skip the reset.

### 2. ESP tools — success path

Start port first, then run each tool. After each, verify `lsof` shows exactly 1 fd.

| Tool | Call | Expected |
|------|------|----------|
| chip_info | `serial_chip_info(port)` | chip_name=ESP32-S3, manufacturer_id, device_id |
| flash_md5 | `serial_flash_md5(port, offset=0x9000, size=4096)` | md5 hash |
| read_nvs | `serial_read_nvs(port, namespace="test_ns")` | 5 entries: name, flag, temperature, initialized, counter |
| read_register | `serial_read_register(port, address=0x60000000)` | address + value |
| security_info | `serial_security_info(port)` | secure_boot, flash_encryption fields |
| reset_esp | `serial_reset_esp(port, boot_wait=3)` | status=success, heartbeat in boot_output |

### 3. Out-of-order execution

Run ESP tools in non-standard order to verify no tool depends on a specific prior call. Start port first.

| Sequence | Tools | Expected |
|----------|-------|----------|
| security_info first | `serial_security_info(port)` → `serial_chip_info(port)` | Both succeed |
| read before chip_info | `serial_read_nvs(port, namespace="test_ns")` → `serial_chip_info(port)` | Both succeed |
| register before chip_info | `serial_read_register(port, address=0x60000000)` → `serial_chip_info(port)` | Both succeed |
| rapid fire | `serial_chip_info(port)` → `serial_read_register(port, address=0x60000000)` (immediately) | No "Serial port busy" |
| md5 after reset | `serial_reset_esp(port)` → `serial_flash_md5(port, offset=0x9000, size=4096)` | Both succeed |
| deferred timer recovery | `serial_chip_info(port)` → wait 3s → `serial_chip_info(port)` | Both succeed (deferred timer fires between calls) |

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
lsof <port> 2>/dev/null | grep serial-io | wc -l  # expect: 1

# After chip_info
lsof <port> 2>/dev/null | grep serial-io | wc -l  # expect: 1

# After read_nvs
lsof <port> 2>/dev/null | grep serial-io | wc -l  # expect: 1

# After stop
lsof <port> 2>/dev/null | grep serial-io | wc -l  # expect: 0
```

If any step shows >1 fd or 0 during operation, there is an fd leak or premature close.

### 6. Cross-section out-of-order

Mix serial lifecycle and ESP tools in non-standard sequences. Start from a clean state (no port open).

| Sequence | Steps | Expected |
|----------|-------|----------|
| ESP before read | `serial_start` → `serial_chip_info` → `serial_read(lines=5)` | chip_info succeeds, read returns heartbeat lines |
| write between ESP | `serial_start` → `serial_chip_info` → `serial_write("hello")` → `serial_read(pattern="echo")` | write+echo work after ESP tool |
| double stop | `serial_start` → `serial_chip_info` → `serial_stop` → `serial_stop` | first stop succeeds, second reports already stopped |
| start-stop-start | `serial_start` → `serial_stop` → `serial_start` → `serial_chip_info` | chip_info succeeds on second session |
| ESP after flash | `serial_flash_esp(images)` → `serial_chip_info` | both succeed without manual start |
| status mid-ESP | `serial_start` → `serial_chip_info` → `serial_status` | status shows running=true |
| reset then read | `serial_start` → `serial_reset_esp(boot_wait=3)` → `serial_read(lines=5)` | read returns heartbeat lines from reset |
| auto_reset false | `serial_start(auto_reset=false)` → `serial_read(lines=3)` | no output (CDC not attached); `serial_reset_esp` → read works |

Each sequence starts from a clean state (stop port if open).
