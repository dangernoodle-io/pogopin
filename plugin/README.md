# pogopin-mcp

Embedded development MCP server — serial monitoring, ESP-IDF flashing, ESP chip utilities, and backtrace decoding. 18 tools for firmware development workflows.

## Install

```bash
/plugin install pogopin-mcp@dangernoodle-marketplace
```

## What it does

- **MCP server with 18 tools** for serial I/O, ESP flash operations, NVS management, and crash decoding. See the [server README](https://github.com/dangernoodle-io/pogopin) for full tool reference.
- **2 hooks auto-inject context** into prompts. SessionStart installs the binary; UserPromptSubmit detects ESP-IDF projects and reminds you to check chip info before flashing.
- **`board-medic` subagent + `/diagnose` skill** — read-mostly hardware diagnostician for embedded boards. Observes state, names a hypothesis, and recommends recovery actions for the main agent to execute. Spawned automatically when you describe a hardware symptom ("board doesn't boot after flash", "guru meditation on every reset") or on demand via `/diagnose`. ESP32 family covered today; other platforms added as tooling lands.
- **`firmware-architect`, `firmware-reviewer`, `firmware-implementer` subagents** — ESP32/Arduino build-lifecycle specialists: read-only design briefs, defect-class code audits, and spec-driven implementation with on-device verification. Spawned on demand or when the main agent delegates design/review/implementation work.
- **`board-operator`, `firmware-builder`, `board-conductor` subagents** — executor, builder, and test-conductor counterparts. `board-operator` runs surgical hardware ops (minimum flash, OTA-preferred, chip-aware reset, confirm-gate on destructive ops) reaching the board over serial or its own remote interface. `firmware-builder` builds with whatever build system the project uses (Makefile/idf.py/pio/Arduino/CMake) and reports artifacts/sizes/warnings — no hardware, no source edits. `board-conductor` drives a user's test workflow against one/many/no devices, tool- and spec-agnostic, triaging failures and remediating OTA-first before escalating to `board-operator`.
- **`firmware-explorer` subagent** — read-only comprehension agent for an existing firmware codebase. Maps boot/init flow, the task and concurrency model, partition/NVS/memory layout, config surface, and peripheral/connectivity usage, so you know where things live before changing or debugging them. Complements `firmware-architect`/`firmware-reviewer`/`firmware-implementer` without designing, auditing, or editing.

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
  "Agent(board-conductor)",
  "Agent(firmware-explorer)"
] } }
```

Disabling the whole plugin also removes the agents, but takes the MCP tools with it — `permissions.deny` drops only the agents and keeps the tools.

## Hooks

- **SessionStart** — single hook entry (`scripts/self-heal.js`, pure Node, no npm deps): installs the binary from GitHub release, local Homebrew, or dev path (release archives are `tar.gz` on every platform, verified by SHA256 and extracted with a built-in tar/gzip reader — no `unzip`), then validates the binary and every hook script actually exist on disk, re-running the installer if the binary is missing/broken. Best-effort and fail-open: never blocks session start, and never writes `settings.json` (hook registration stays static in `hooks.json`).
- **UserPromptSubmit** — detect ESP-IDF project and inject context reminder

## Configuration

- `POGOPIN_DEV_BINARY` — path to local dev binary (bypasses GitHub download)
- `POGOPIN_STATUS_DIR` — override the status directory `scripts/statusline.js` and `scripts/pre-tool-port-check.js` read from (default: `~/.cache/pogopin/status` per-platform)
- `POGOPIN_STATUSLINE_MODE` — controls `scripts/statusline.js` visibility. `always` (default, unchanged behavior): render live ports, print `serial: idle` when none. `ports-only`: render live ports, exit silently (no output) when none. `fresh-only`: render only ports updated within the last 30s, exit silently when none qualify. Unknown/empty values fall back to `always`.

## Server

Source and detailed docs at [github.com/dangernoodle-io/pogopin](https://github.com/dangernoodle-io/pogopin).

## License

MIT
