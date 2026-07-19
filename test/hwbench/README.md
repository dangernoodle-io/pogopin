# hwbench

Committed Go hardware-integration harness that drives the pogopin MCP server
over its real stdio wire protocol (JSON-RPC 2.0, newline-delimited) against a
physical ESP board. This is the committed form of the scratchpad JS driver
that HW-validated the `esp_gpio_*` tools (10/10 on an ESP32-S2).

Uses the `github.com/mark3labs/mcp-go` stdio client (already a pogopin
dependency) rather than hand-rolled JSON-RPC framing — it speaks
`initialize` → `notifications/initialized` → `tools/list` /`tools/call`
against the real `pogo server` subprocess, and surfaces
`notifications/progress` via `Client.OnNotification`.

## Running

Requires a physical ESP board connected over USB serial. Build-tagged
`hwtest` and skipped by default:

```bash
# no hardware: clean skip, never compiles without the tag
go test ./...

# with hardware attached
POGOPIN_HW_PORT=/dev/cu.usbmodem1234561 \
POGOPIN_HW_BOARD=s2 \
go test -tags hwtest -run HWBench -v ./test/hwbench/...
```

### CI compile check (no hardware)

`make hwbench-check` runs `go build`/`go vet`/`golangci-lint run` against
the `hwtest`-tagged sources with no hardware attached — catches a compile
break or `mcp-go` API drift on every PR (via the `hwbench-check` CI job)
instead of only surfacing on a scarce one-shot hardware run:

```bash
make hwbench-check
```

### Environment variables

| Var | Required | Default | Purpose |
|-----|----------|---------|---------|
| `POGOPIN_HW_PORT` | yes | — | Serial port of the attached board. Unset = the whole test skips. |
| `POGOPIN_HW_BOARD` | no | `s2` | Selects the board profile (see table below). |
| `POGOPIN_HW_BIN` | no | — | Path to a pre-built `pogo` binary. Unset = `go build` a temp binary from the repo root. |
| `POGOPIN_HW_LED_PIN` | no | — | Overrides the selected profile's `LEDPin` (useful for unverified boards, e.g. C3 Mini). |

### Board profiles

| `POGOPIN_HW_BOARD` | Name | LED pin | Native USB | LED type |
|---------------------|------|---------|------------|----------|
| `s2` | S2 Mini | 15 | yes | gpio |
| `c3` | C3 Mini | 4 (non-reserved on every C3, but unverified as the actual LED pin — override with `POGOPIN_HW_LED_PIN`) | yes | gpio |
| `s3dongle` | S3 T-Dongle | n/a | yes | apa102 (LED-visual scenarios skip) |
| `cyd` | CYD (ESP32/CH340) | 22 (red channel; RGB LED is R=22/G=16/B=17) | no | rgb |

### Native-USB download-mode prerequisite

Boards with `NativeUSB: true` (S2 Mini, C3 Mini, S3 T-Dongle) enumerate
their USB-CDC/JTAG peripheral from firmware, not silicon — a cold plug can
leave the ROM bootloader unreachable. Before running: hold BOOT, tap/hold
RST, then release BOOT (standard ESP32-S2/S3/C3 download-mode entry). Every
`esp_gpio_*` call this harness makes passes `reset_mode: "no_reset"` —
learned the hard way on hardware that a reset-based connect hangs/desyncs
the ROM on these chips.

## Scenarios

Each is a `t.Run` subtest under `TestHWBench`:

1. `serial_list` unlocks the hardware tier; result contains the configured port.
2. `tools/list` includes `esp_gpio_read`/`esp_gpio_set`/`esp_gpio_sweep`; esp_ tool count >= 13.
3. magic-0x9 regression: two back-to-back `esp_gpio_read` calls both succeed, neither result mentions `magic`/`0x9`.
4. `esp_gpio_set` high then low on the LED pin (skipped on apa102 boards).
5. **>5s-expiry reattach** (load-bearing): wait ~6s past the session's deferred-release idle window, then `esp_gpio_read` — must still succeed with no resync failure. Validates the no-reset-on-expire fix.
6. Reserved-pin gate: `esp_gpio_set` on GPIO0 is refused by default with a "reserved" message.
7. `esp_gpio_sweep` over the LED pin + GPIO0 drives the valid pin, skips the reserved one, and emits >=1 `notifications/progress`.

## Hardware-free mock lane

`TestMockBench` (`mock_test.go`) runs the `runGPIOScenarios` suite
`TestHWBench` also runs (shared via `bench_common_test.go`), plus
`runSecurityInfoScenario` (BR-66 PR3 — `esp_info include=security` asserts
`chip_id` on chip-ID-detected boards) and `runSerialMonitorScenarios`
(serial_start/read/write/stop against a synthetic boot banner + write/read
loopback — BR-66 PR2), against `internal/mockhw`'s virtual chip instead of
a physical board — same stdio wire-protocol path (`mcp-go` client, real
`pogo server` subprocess), no board attached. It complements the hardware
lane above; it does not replace it. Silicon-specific quirks such as the
magic-0x9 regression (scenario 3) can only be validated on real hardware —
the mock lane exists to catch tool-surface/protocol/session-logic
regressions cheaply and deterministically in CI, not to simulate every
hardware quirk.

`internal/mockhw` emulates four chip families — ESP32, ESP32-S2, ESP32-C3,
ESP32-S3 — each on its own synthetic port (`boardProfile.MockPort`,
selected via `ACC_POGOPIN_BOARD`). ESP32/ESP32-S2 are detected via
espflasher's chip-magic register path; ESP32-C3/ESP32-S3 have no magic
value and are detected via a real 20-byte `GET_SECURITY_INFO` response
carrying the chip's `ChipID` (5 for C3, 9 for S3) — the same path
`esp_info include=security` exercises, which `runSecurityInfoScenario`
asserts for the `c3`/`s3dongle` board profiles (`boardProfile.SecurityChipID`).

Untagged (no `hwtest` build tag) — the mock server binary is built
separately with the `mock` build tag (see `internal/mockhw`'s package doc
and `mcpapp.maybeEnableMock`), independent of this test binary's own
tags. Gated on `ACC_POGOPIN` (mirrors `TF_ACC`) so it skips in a plain `go
test ./...` run.

```bash
# no hardware, no ACC_POGOPIN: clean skip
go test ./...

# hardware-free mock bench
make mock-bench

# hardware-free in-process mcpapp integration test
# (TestMockGPIOInProcess, internal/mcpapp/mock_integration_test.go)
make mcp-mock

# both, in one shot — the CI mock-bench job runs this
make acc
```

### Environment variables (mock lane)

| Var | Required | Default | Purpose |
|-----|----------|---------|---------|
| `ACC_POGOPIN` | yes | — | Gate; unset = the mock tests skip. Set automatically by `make mock-bench`/`mcp-mock`/`acc`. |
| `ACC_POGOPIN_BOARD` | no | `s2` | Selects the board profile (same table as the hardware lane above). |
| `ACC_POGOPIN_BIN` | no | — | Path to a pre-built `-tags mock` `pogo` binary. Unset = build one from the repo root with the `mock` tag. |

`TestMockGPIOInProcess` and `TestMockSerialMonitorInProcess`
(`internal/mcpapp/mock_integration_test.go`) are sibling tests in a
different package — in-process tests (no subprocess) that drive the real
MCP tool handlers directly against the virtual chip, exercising the actual
espflasher/session (GPIO) and session/serial.Manager (monitor) code paths
at a lower level than this package's wire-protocol bench.

CI runs both mock-lane suites, plus `hwbench-check` (the `hwtest`-tagged
compile check above), as separate jobs — see `mock-bench` and
`hwbench-check` in `.github/workflows/build.yml`.
