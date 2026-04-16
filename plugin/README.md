# breadboard-mcp

Embedded development MCP server — serial monitoring, ESP-IDF flashing, ESP chip utilities, and backtrace decoding. 18 tools for firmware development workflows.

## Install

```bash
/plugin install breadboard-mcp@dangernoodle-marketplace
```

## What it does

- **MCP server with 18 tools** for serial I/O, ESP flash operations, NVS management, and crash decoding. See the [server README](https://github.com/dangernoodle-io/breadboard) for full tool reference.
- **2 hooks auto-inject context** into prompts. SessionStart installs the binary; UserPromptSubmit detects ESP-IDF projects and reminds you to check chip info before flashing.

## Hooks

- **SessionStart** — install binary from GitHub release, local Homebrew, or dev path
- **UserPromptSubmit** — detect ESP-IDF project and inject context reminder

## Configuration

- `BREADBOARD_DEV_BINARY` — path to local dev binary (bypasses GitHub download)

## Features not yet in plugin

- Skills: dedicated commands for common tasks (flash, decode backtrace, read NVS)
- Subagents: assistant agents for firmware analysis and project configuration

These will land in v0.2.0.

## Server

Source and detailed docs at [github.com/dangernoodle-io/breadboard](https://github.com/dangernoodle-io/breadboard).

## License

MIT
