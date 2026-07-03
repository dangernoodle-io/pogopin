# pogopin-mcp

Embedded development MCP server — serial monitoring, ESP-IDF flashing, ESP chip utilities, and backtrace decoding. 18 tools for firmware development workflows.

## Install

```bash
/plugin install pogopin-mcp@dangernoodle-marketplace
```

## What it does

- **MCP server with 18 tools** for serial I/O, ESP flash operations, NVS management, and crash decoding. See the [server README](https://github.com/dangernoodle-io/pogopin) for full tool reference.
- **2 hooks auto-inject context** into prompts. SessionStart installs the binary; UserPromptSubmit detects ESP-IDF projects and reminds you to check chip info before flashing.
- **`board-medic` subagent + `/board-medic` skill** — read-mostly hardware diagnostician for embedded boards. Observes state, names a hypothesis, and recommends recovery actions for the main agent to execute. Spawned automatically when you describe a hardware symptom ("board doesn't boot after flash", "guru meditation on every reset") or on demand via `/board-medic`. ESP32 family covered today; other platforms added as tooling lands.
- **`firmware-architect`, `firmware-reviewer`, `firmware-implementer` subagents** — ESP32/Arduino build-lifecycle specialists: read-only design briefs, defect-class code audits, and spec-driven implementation with on-device verification. Spawned on demand or when the main agent delegates design/review/implementation work.
- **`board-operator`, `firmware-builder`, `board-conductor` subagents** — executor, builder, and test-conductor counterparts. `board-operator` runs surgical hardware ops (minimum flash, OTA-preferred, chip-aware reset, confirm-gate on destructive ops) reaching the board over serial or its own remote interface. `firmware-builder` builds with whatever build system the project uses (Makefile/idf.py/pio/Arduino/CMake) and reports artifacts/sizes/warnings — no hardware, no source edits. `board-conductor` drives a user's test workflow against one/many/no devices, tool- and spec-agnostic, triaging failures and remediating OTA-first before escalating to `board-operator`.

The firmware agents also tap two **optional** external MCP servers when installed — `espressif-documentation` (semantic ESP-IDF doc search) and `esp-component-registry` (component search) — to ground design and review in current Espressif docs and reusable components. They work fine without these servers; the extra tools simply go unused.

### Opting out of the agents

The agents ship enabled. To keep the MCP tools but drop any agent you don't want, add it to `permissions.deny` in your `settings.json` (project `.claude/settings.json` or user `~/.claude/settings.json`):

```json
{ "permissions": { "deny": [
  "Agent(board-medic)",
  "Agent(firmware-architect)",
  "Agent(firmware-reviewer)",
  "Agent(firmware-implementer)",
  "Agent(board-operator)",
  "Agent(firmware-builder)",
  "Agent(board-conductor)"
] } }
```

Disabling the whole plugin also removes the agents, but takes the MCP tools with it — `permissions.deny` drops only the agents and keeps the tools.

## Hooks

- **SessionStart** — install binary from GitHub release, local Homebrew, or dev path
- **UserPromptSubmit** — detect ESP-IDF project and inject context reminder

## Configuration

- `POGOPIN_DEV_BINARY` — path to local dev binary (bypasses GitHub download)

## Server

Source and detailed docs at [github.com/dangernoodle-io/pogopin](https://github.com/dangernoodle-io/pogopin).

## License

MIT
