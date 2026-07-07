# pogopin

Embedded development MCP server — serial monitoring, ESP-IDF flashing, crash decode.

## Module

`dangernoodle.io/pogopin`, Go 1.26.x

## Build

```bash
make build    # CGO_ENABLED=0 go build -o pogo
make test     # go test -race ./...
make cover    # test + coverage summary
make lint     # golangci-lint run
make install  # go install .
```

## Project layout

- `main.go` — thin wrapper, delegates to `internal/cli.Execute`
- `internal/cli/` — cobra root + CLI subcommands (decode, server)
- `internal/mcpserver/` — MCP server setup, tool registration, handlers
- `internal/serial/` — SerialManager, RingBuffer, port I/O
- `internal/session/` — port session lifecycle (Reader, Flasher, External modes)
- `internal/esp/` — ESP chip flasher adapter, NVS utilities
- `internal/decode/` — backtrace decoder types and logic
- `internal/flash/` — external flash command orchestration

## Tools

| Tool | Domain | Description |
|------|--------|-------------|
| serial_list | serial | List available serial ports |
| serial_start | serial | Open port, start buffered monitoring |
| serial_read | serial | Read buffered serial output |
| serial_write | serial | Write data to port |
| serial_stop | serial | Close port |
| serial_restart | serial | Atomic stop+start on a port (re-trigger DTR/RTS reset) |
| serial_status | serial | Port status (JSON) |
| flash_external | flash | Stop port → run external flash command → restart → capture |
| esp_flash | ESP | Flash firmware (native Go flasher) |
| esp_erase | ESP | Erase flash (whole chip or region) |
| esp_info | ESP | Chip info (default) or security info (include=security) |
| esp_register | ESP | Read/write 32-bit register (omit value=read, provide value=write) |
| esp_reset | ESP | Reset via bootloader |
| esp_read_flash | ESP | Read flash bytes or MD5 hash (md5=true) |
| esp_read_nvs | ESP | Read NVS entries |
| esp_write_nvs | ESP | Full NVS partition replace (DESTRUCTIVE) |
| esp_nvs_set | ESP | Set NVS keys (read-modify-write, batch entries[]) |
| esp_nvs_delete | ESP | Delete NVS key or namespace (read-modify-write) |
| decode_backtrace | decode | Symbolize xtensa/riscv32 panic frames |

The plugin also ships a **`board-medic`** subagent (read-mostly hardware diagnostician) with a matching `/diagnose` skill. Platform-agnostic posture with an ESP32-family section today; extend with new platform sections as their tooling lands. See `plugin/agents/board-medic.md`. Three firmware build-lifecycle agents live alongside it: **`firmware-architect`** (read-only design brief — API, memory, concurrency, test seam); **`firmware-reviewer`** (read-only audit against a defect-class checklist, findings ranked by severity); **`firmware-implementer`** (implements from a spec, builds, runs host tests, flashes, and verifies on hardware). Three more round out the roster: **`board-operator`** (executes surgical hardware ops — flashes the minimum, app-partition only, OTA-preferred, chip-aware reset, confirm-gate on destructive ops; reaches the board over serial or its exposed remote interface; the executor counterpart to board-medic); **`firmware-builder`** (builds with whatever build system the project uses — Makefile/idf.py/pio/Arduino/CMake — reports artifacts/sizes/warnings, knows incremental-build staleness gotchas; no hardware, no source edits); **`board-conductor`** (drives a user's test workflow against one/many/no devices, tool-, spec-, and interface-agnostic; interprets verdicts, triages failures, remediates OTA-first, escalating to board-operator for serial flash). One more rounds out the roster: **`firmware-explorer`** (read-only comprehension agent — maps boot/init flow, task and concurrency model, partition/NVS/memory layout, config surface, and peripheral usage on an existing firmware codebase; complements firmware-architect/firmware-reviewer/firmware-implementer without designing, auditing, or editing). Agent definitions are validated by `agents_test.go` (frontmatter, model/tools checks, and a generic-leak guard; runs in CI via `go test ./...`); a manual reviewer fixture with planted defects lives in `test/agent-fixtures/defect-zoo/`.

Tools register in two tiers. The **core tier** (7× `serial_*` + `decode_backtrace`) registers at startup. The **hardware tier** (10× `esp_*` + `flash_external`) registers lazily on the first `serial_list` or `serial_start` call via `notifications/tools/list_changed`. Sessions that only decode crash logs never pay for the ESP tool surface.

## Dependencies

- `github.com/spf13/cobra` — CLI framework
- `github.com/mark3labs/mcp-go` — MCP server framework
- `go.bug.st/serial` — serial port I/O
- `tinygo.org/x/espflasher` (via jgangemi/espflasher fork) — ESP flasher, NVS library

## Plugin

`plugin/` contains the Claude Code plugin wrapper (pogopin-mcp) — same pattern as espidf-tools: SessionStart hook installs release binary, UserPromptSubmit hook injects ESP-IDF context.

- `plugin/.claude-plugin/plugin.json` — manifest; `mcpServers.pogopin.command` points at `${CLAUDE_PLUGIN_DATA}/bin/pogo server`
- `plugin/hooks/hooks.json` — `SessionStart` hook running `scripts/install.sh` to fetch the release binary
- `plugin/scripts/install.sh` — downloads the GitHub release archive, verifies SHA256, installs to plugin data dir
- `plugin/scripts/statusline.js` — Node.js widget for ccstatusline custom-command that renders serial port state from `~/.cache/pogopin/status.json`

**No plugin version field**: `plugin/.claude-plugin/plugin.json` intentionally omits `version`. When absent, Claude Code keys its plugin cache on the source commit sha, so changing the `marketplace.json` ref to a new tag automatically invalidates the cache — no lockstep bump required. Release automation only needs to update the marketplace ref.

**Local dev**: from a clone of `dangernoodle-marketplace`, run `.scripts/plugin-dev.sh link pogopin-mcp` to symlink the plugin cache dir to this working tree.

## Test firmware

`.firmware/` contains a minimal ESP32-S3 firmware (ESP-IDF) for hardware testing. Build on demand — binaries are gitignored. See `.firmware/README.md` for build instructions, flash offsets, NVS test entries, and manual test plan.

### Test firmware notes

- If esp_* tools behave strangely (null NVS, no boot_output, chip appears stuck in ROM bootloader, "device not in download mode"), unplug/replug the board before diagnosing code — stale USB-Serial-JTAG peripheral state is the most common cause.
- NVS read/write through pogopin uses the ROM bootloader — the app does NOT need to be running. Only heartbeat/echo tests require app boot.
- `CONFIG_ESP_CONSOLE_USB_CDC=y` works fine on S3 alongside pogopin ESP tools (TaipanMiner uses this config). It auto-wires stdin, so echo tests work with no extra VFS setup. Only switch to `USB_SERIAL_JTAG` if you need the JTAG interface — and then you must install the driver + `usb_serial_jtag_vfs_use_driver()` to get stdin.
