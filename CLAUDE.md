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

`make hwbench-check` compile-checks the MCP hardware-integration harness; `make mock-bench`/`make mcp-mock`/`make acc` run the hardware-free virtual-chip acceptance suite; see `test/hwbench/README.md` for both lanes and run instructions.

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

Risk is sourced from the `toolRiskClass` registry (`internal/mcpserver/risk.go`, BR-71) — the single source of truth; `internal/mcpserver/tool_risk_doc_test.go` enforces this table stays aligned with it.

<!-- tool-risk-table:start -->
| Tool | Domain | Risk | Description |
|------|--------|------|-------------|
| serial_list | serial | read | List available serial ports |
| serial_start | serial | write | Open port, start buffered monitoring |
| serial_read | serial | read | Read buffered serial output |
| serial_write | serial | write | Write data to port |
| serial_stop | serial | write | Close port |
| serial_restart | serial | write | Atomic stop+start on a port (re-trigger DTR/RTS reset) |
| serial_status | serial | read | Port status (JSON) |
| flash_external | flash | destructive | Stop port → run external flash command → restart → capture |
| esp_flash | ESP | destructive | Flash firmware (native Go flasher) |
| esp_erase | ESP | destructive | Erase flash (whole chip or region) |
| esp_info | ESP | read | Chip info (default) or security info (include=security) |
| esp_register | ESP | write | Read/write 32-bit register (omit value=read, provide value=write) |
| esp_reset | ESP | write | Reset via bootloader |
| esp_read_flash | ESP | read | Read flash bytes or MD5 hash (md5=true) |
| esp_read_nvs | ESP | read | Read NVS entries |
| esp_write_nvs | ESP | destructive | Full NVS partition replace (DESTRUCTIVE — intentional, unguarded) |
| esp_nvs_set | ESP | write | Set NVS keys (read-modify-write, batch entries[]) |
| esp_nvs_delete | ESP | write | Delete NVS key or namespace (read-modify-write) |
| esp_gpio_read | ESP | read | Read a GPIO pin level via the bootloader (no firmware) |
| esp_gpio_set | ESP | destructive | Drive a GPIO pin high/low; refuses reserved/input-only pins unless include_reserved |
| esp_gpio_sweep | ESP | destructive | Sweep/dwell across candidate GPIOs to find which drives an LED; no-reset hold |
| decode_backtrace | decode | read | Symbolize xtensa/riscv32 panic frames |
<!-- tool-risk-table:end -->

`esp_nvs_set`/`esp_nvs_delete` (BR-53) are RMW with defense-in-depth independent of the codec: a **pre-write completeness guard** (`internal/esp/nvs_guard.go`) reads the raw partition's per-page entry-state bitmap and counts Written slots directly (ground truth, no structural interpretation), then compares against the slot span accounted for by `nvs.ParseNVS`'s result plus independently-counted namespace-declaration slots — if the parse left any Written slot unaccounted for, the write aborts before anything is flashed rather than silently dropping data. After a successful flash, the partition is re-read and re-parsed; `esp_nvs_set` confirms every requested key landed with its new value and every untouched pre-existing key survived, `esp_nvs_delete` confirms the deleted key(s) are gone and everything else survived. Only a verified outcome is reported as success — `updated`/`deleted` counts reflect confirmed changes, not the request. `esp_write_nvs` remains the intentional destructive full-partition replace with no such guard.

The plugin also ships a **`board-medic`** subagent (read-mostly hardware diagnostician) with a matching `/diagnose` skill. Platform-agnostic posture with an ESP32-family section today; extend with new platform sections as their tooling lands. See `plugin/agents/board-medic.md`. Three firmware build-lifecycle agents live alongside it: **`firmware-architect`** (read-only design brief — API, memory, concurrency, test seam); **`firmware-reviewer`** (read-only audit against a defect-class checklist, findings ranked by severity); **`firmware-implementer`** (implements from a spec, builds, runs host tests, flashes, and verifies on hardware). Three more round out the roster: **`board-operator`** (executes surgical hardware ops — flashes the minimum, app-partition only, OTA-preferred, chip-aware reset, confirm-gate on destructive ops; reaches the board over serial or its exposed remote interface; the executor counterpart to board-medic); **`firmware-builder`** (builds with whatever build system the project uses — Makefile/idf.py/pio/Arduino/CMake — reports artifacts/sizes/warnings, knows incremental-build staleness gotchas; no hardware, no source edits); **`board-conductor`** (drives a user's test workflow against one/many/no devices, tool-, spec-, and interface-agnostic; interprets verdicts, triages failures, remediates OTA-first, escalating to board-operator for serial flash). One more rounds out the roster: **`firmware-explorer`** (read-only comprehension agent — maps boot/init flow, task and concurrency model, partition/NVS/memory layout, config surface, and peripheral usage on an existing firmware codebase; complements firmware-architect/firmware-reviewer/firmware-implementer without designing, auditing, or editing). Agent definitions are validated by `agents_test.go` (frontmatter, model/tools checks, and a generic-leak guard; runs in CI via `go test ./...`); a manual reviewer fixture with planted defects lives in `test/agent-fixtures/defect-zoo/`.

Tools register in two tiers. The **core tier** (7× `serial_*` + `decode_backtrace`) registers at startup. The **hardware tier** (13× `esp_*` + `flash_external`) registers lazily on the first `serial_list` or `serial_start` call via `notifications/tools/list_changed`. Sessions that only decode crash logs never pay for the ESP tool surface.

`pogo server --diagnostic` (or `POGOPIN_DIAGNOSTIC=1`, either enables it) runs a **diagnostic profile** (BR-72): registers only READ-class tools (per `toolRiskClass` in `internal/mcpserver/risk.go`) — observe-only, no writes, flashing, erase, or session start. Enforcement is server-side (`internal/mcpserver/diagnostic.go`'s `addTool` gate) — a diagnostic client's `tools/list` never contains a non-READ tool, so it can't call one. Inert by default; the hardware-tier unlock (`serial_list`/`serial_start`) still fires since `serial_list` is READ.

Every tool emits `notifications/progress` (start + completion ticks at minimum; multi-phase ops like `esp_read_nvs`/`esp_read_flash`/`esp_reset`/`flash_external` add coarse in-between phase markers) via a transport-neutral `esp.StatusFunc`/`newSequentialStatusEmitter` — no tool is silent for the duration of a call.

## Dependencies

- `github.com/spf13/cobra` — CLI framework
- `github.com/mark3labs/mcp-go` — MCP server framework
- `go.bug.st/serial` — serial port I/O
- `tinygo.org/x/espflasher` (via jgangemi/espflasher fork) — ESP flasher, NVS library

## Plugin

`plugin/` contains the Claude Code plugin wrapper (pogopin-mcp) — same pattern as espidf-tools: SessionStart hook installs release binary, UserPromptSubmit hook injects ESP-IDF context, PreToolUse hook warns on cross-session port conflicts.

- `plugin/.claude-plugin/plugin.json` — manifest; `mcpServers.pogopin.command` points at `${CLAUDE_PLUGIN_DATA}/bin/pogo server`
- `plugin/hooks/hooks.json` — single `SessionStart` entry runs `scripts/self-heal.js` (install + validate + repair, no separate installer, no parallel-hook race); `PreToolUse` hook (matcher `mcp__plugin_pogopin-mcp_pogopin__.*`) running `scripts/pre-tool-port-check.js`
- `plugin/scripts/self-heal.js` — single pure-Node script (builtins only: `fs`, `path`, `os`, `https`, `zlib`, `crypto`, `child_process` — no npm deps) that is both the installer and the SessionStart self-heal (BR-4). **Install** precedence: `POGOPIN_DEV_BINARY` (dev binary, copied+codesigned) > local Homebrew binary (`/usr/local/bin/pogo` or `/opt/homebrew/bin/pogo`, symlink-resolved) > GitHub release (detects os/arch from `process.platform`/`process.arch`, fetches the latest tag from the GitHub API, downloads the `tar.gz` archive + `SHA256SUMS` — following HTTP redirects itself since `https.get` doesn't — verifies the SHA256, `zlib.gunzipSync`s it, and reads the `pogo` entry out with an in-house minimal POSIX ustar reader; skips the download entirely when `<CLAUDE_PLUGIN_DATA>/.version` already matches the latest tag; falls back to keeping the existing binary if the network is unreachable). Every install path lands the binary atomically (write to a `<binary>.tmp.<pid>` sibling, chmod 755, best-effort mac `codesign`, then `fs.renameSync` into place). **Validate**: `<CLAUDE_PLUGIN_DATA>/bin/pogo` exists+executable, every script referenced by `hooks/hooks.json` exists under `<CLAUDE_PLUGIN_ROOT>` (placeholders resolved the way Claude Code would), plus a best-effort read-only check that a `statusLine.command` configured in `<CLAUDE_CONFIG_DIR-or-~/.claude>/settings.json` points at an existing plugin script. Repair action: binary missing/broken → run the installer above (the one-and-only installer now — SessionStart no longer races two hook entries). A missing hook script means a corrupt plugin install the installer can't fix — logged as an actionable message only. Fully fail-open (every check and every install step advisory, always exits 0, never throws, never blocks session start) and never writes `settings.json` — hook registration stays static in `hooks.json` (BR-33). Release archives are `tar.gz` on every platform including darwin (`.goreleaser.yml` no longer overrides to `zip` on darwin) so extraction never needs `unzip`.
- Status model: each pogo server process writes its OWN file, `~/.cache/pogopin/status/<pid>.json` (not a single shared `status.json`) — avoids a portless session's 15s heartbeat clobbering a concurrent session's port entry (last-writer-wins on one shared file). `plugin/scripts/status-lib.js` is the shared reader: `readLivePorts()` globs the status dir, drops a file's ports when its owning process is dead or `updated_at` is older than 45s (3x the heartbeat, guards PID reuse), and merges survivors into one flat `ports[]`. Honors `POGOPIN_STATUS_DIR` override. Fully fail-open — bad/missing files contribute nothing, never throws.
- `plugin/scripts/pre-tool-port-check.js` — warn-only PreToolUse hook (BR-31): reads the merged live-ports view via `status-lib.js`, warns via `additionalContext`/`systemMessage` (never denies) only when the target port is `running` under a *different* `CLAUDE_CODE_SESSION_ID` than the calling hook's and that session's owning process is still alive. Silent on the normal same-session flow, and gracefully degrades to no-warning on older servers or when `CLAUDE_CODE_SESSION_ID` is unset (status entries lack `session_id`).
- `plugin/scripts/statusline.js` — Node.js widget for ccstatusline custom-command that renders serial port state from the merged live-ports view; when `CLAUDE_CODE_SESSION_ID` is set, filters to that session's own ports only. Visibility mode is configurable via `POGOPIN_STATUSLINE_MODE` (BR-8): `always` (default, unchanged) prints `serial: idle` when no ports; `ports-only` exits silently when none; `fresh-only` renders only ports updated within the last 30s (via an additive `readLivePorts({maxAgeSeconds})` opt and per-port `updated_at`, non-breaking for the BR-31 hook's default-call 45s semantics), exiting silently when none qualify. Unknown/empty mode values fall back to `always`.

**No plugin version field**: `plugin/.claude-plugin/plugin.json` intentionally omits `version`. When absent, Claude Code keys its plugin cache on the source commit sha, so changing the `marketplace.json` ref to a new tag automatically invalidates the cache — no lockstep bump required. Release automation only needs to update the marketplace ref.

**Local dev**: from a clone of `dangernoodle-marketplace`, run `.scripts/plugin-dev.sh link pogopin-mcp` to symlink the plugin cache dir to this working tree.

## Test firmware

`.firmware/` contains a minimal ESP32-S3 firmware (ESP-IDF) for hardware testing. Build on demand — binaries are gitignored. See `.firmware/README.md` for build instructions, flash offsets, NVS test entries, and manual test plan.

### Test firmware notes

- If esp_* tools behave strangely (null NVS, no boot_output, chip appears stuck in ROM bootloader, "device not in download mode"), unplug/replug the board before diagnosing code — stale USB-Serial-JTAG peripheral state is the most common cause.
- NVS read/write through pogopin uses the ROM bootloader — the app does NOT need to be running. Only heartbeat/echo tests require app boot.
- `CONFIG_ESP_CONSOLE_USB_CDC=y` works fine on S3 alongside pogopin ESP tools (TaipanMiner uses this config). It auto-wires stdin, so echo tests work with no extra VFS setup. Only switch to `USB_SERIAL_JTAG` if you need the JTAG interface — and then you must install the driver + `usb_serial_jtag_vfs_use_driver()` to get stdin.
