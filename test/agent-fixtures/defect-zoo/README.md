# defect-zoo — firmware-reviewer behavioral fixture

Two source files with eight deliberately planted defects, one per audit class from the
`firmware-reviewer` checklist. Use this directory to confirm the agent's detection coverage.

## Defect inventory

| # | Class | File | Line | Description |
|---|-------|------|-------------|-------------|
| 1 | include-guard | `buggy_sensor.h` | 5 | `#ifndef` guard instead of `#pragma once` |
| 2 | platform-type-leak | `buggy_sensor.h` | 12–13, 22–24 | `esp_http_server.h` / `esp_err.h` in public header; `esp_err_t` and `httpd_handle_t` in public API |
| 3 | inert-knob | `buggy_sensor.c` | 16–17 | `#ifndef SENSOR_BUF_LEN` shadows `CONFIG_SENSOR_BUF_LEN`; Kconfig symbol is silently dead |
| 4 | timestamp-wrap | `buggy_sensor.c` | 63–64 | `uint32_t ms = esp_timer_get_time() / 1000` truncates µs→ms, then stored in `uint64_t last_ms`; 49.7-day wrap |
| 5 | missing-lock | `buggy_sensor.c` | 54 | `s_sample_count++` without taking `s_mutex` |
| 6 | bare-%d | `buggy_sensor.c` | 58 | `ESP_LOGI` uses `%d` for `uint32_t` — must be `PRIu32` |
| 7 | untimed-block | `buggy_sensor.c` | 46 | `xQueueReceive` with `portMAX_DELAY` and no justifying comment |
| 8 | runtime-ESP_ERROR_CHECK | `buggy_sensor.c` | 50 | `ESP_ERROR_CHECK` on a non-init runtime path — must be `ESP_RETURN_ON_ERROR` |

## How to run (firmware-reviewer — manual)

Spawn the `firmware-reviewer` agent on this directory:

> "Audit `test/agent-fixtures/defect-zoo/` for defects. Review both `buggy_sensor.h` and
> `buggy_sensor.c` against your full checklist."

Expected output: the agent reports all eight defect classes. Each finding should match a row in
the table above. If the agent misses a class or misidentifies a severity, the agent definition
needs updating — not this fixture.

**This is a manual test. It cannot run in CI because agents require an interactive session.**

## Manual smoke tests for the other firmware agents

### firmware-architect

Give a one-line design brief such as: "Design a FreeRTOS task that polls a BME280 over I2C every
500 ms and publishes temperature/humidity to an MQTT topic." Confirm the response is a structured
design brief covering all seven sections (Scope, Public API, Config knobs, Memory model,
Concurrency model, Test seam, Risks/alternatives). The agent must make no file edits.

### firmware-implementer

Give a tiny spec derived from a firmware-architect brief. Confirm the agent: writes code to a
file, builds it (`idf.py build` or `pio run`), runs host tests if a test seam is present, flashes
hardware, reads serial output, and uses honest language ("looks ok so far" rather than "fixed")
until a clean reboot is observed. Premature "done" declarations without on-device evidence are a
signal the agent's verification loop is broken.
