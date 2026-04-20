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

## Hooks

- **SessionStart** — install binary from GitHub release, local Homebrew, or dev path
- **UserPromptSubmit** — detect ESP-IDF project and inject context reminder

## Configuration

- `POGOPIN_DEV_BINARY` — path to local dev binary (bypasses GitHub download)

## Server

Source and detailed docs at [github.com/dangernoodle-io/pogopin](https://github.com/dangernoodle-io/pogopin).

## License

MIT
