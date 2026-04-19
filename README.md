# pogopin

[![Go](https://img.shields.io/badge/Go-1.26.2-00ADD8?logo=go)](https://go.dev/)
[![Build](https://github.com/dangernoodle-io/pogopin/actions/workflows/build.yml/badge.svg)](https://github.com/dangernoodle-io/pogopin/actions/workflows/build.yml)
[![Release](https://github.com/dangernoodle-io/pogopin/actions/workflows/release.yml/badge.svg)](https://github.com/dangernoodle-io/pogopin/actions/workflows/release.yml)
[![Coverage Status](https://coveralls.io/repos/github/dangernoodle-io/pogopin/badge.svg?branch=main)](https://coveralls.io/github/dangernoodle-io/pogopin?branch=main)

Embedded development MCP server. Serial port monitoring, ESP-IDF chip programming, NVS management, and crash backtrace decoding — all in one binary.

> **Maintained by AI** — This project is developed and maintained by Claude (via [@dangernoodle-io](https://github.com/dangernoodle-io)).
> If you find a bug or have a feature request, please [open an issue](https://github.com/dangernoodle-io/pogopin/issues) with examples so it can be addressed.

## Tools

| Namespace | Tools | Docs |
|-----------|-------|------|
| Serial | `serial_list`, `serial_start`, `serial_read`, `serial_write`, `serial_stop`, `serial_status`, `flash_external` | [Wiki](../../wiki/Serial-Tools) |
| ESP | `esp_flash`, `esp_erase`, `esp_info`, `esp_register`, `esp_reset`, `esp_read_flash`, `esp_read_nvs`, `esp_write_nvs`, `esp_nvs_set`, `esp_nvs_delete` | [Wiki](../../wiki/ESP-Tools) |
| Decode | `decode_backtrace` | [Wiki](../../wiki/Decode) |

## Use with Claude Code

The recommended way to run pogopin is via the marketplace plugin — it handles installation and auto-registers the MCP server.

```
/plugin marketplace add dangernoodle-io/dangernoodle-marketplace
/plugin install pogopin-mcp@dangernoodle-marketplace
```

The plugin adds, beyond the raw MCP tools:

- Auto-installs the `pogo` binary on session start — no manual install step
- ESP-IDF context detection hooks for automatic project environment setup
- Serial monitoring tools pre-wired to the Claude Code environment

Source: [dangernoodle-io/dangernoodle-marketplace](https://github.com/dangernoodle-io/dangernoodle-marketplace).

## Install the binary standalone

If you're not using Claude Code, or you want pogopin as a plain MCP server without the plugin's context hooks, install the binary directly.

### Homebrew

```bash
brew install dangernoodle-io/tap/pogo
```

### From Source

```bash
go install dangernoodle.io/pogopin@latest
```

### GitHub Releases

Download pre-built binaries from [releases](https://github.com/dangernoodle-io/pogopin/releases).

### Register manually with Claude Code

```bash
claude mcp add --scope user pogopin /absolute/path/to/pogo server
```

This gives you the 18 MCP tools but none of the auto-context injection that the plugin provides.

## CLI

See [CLI](../../wiki/CLI) for subcommand reference.

## License

See workspace LICENSE.
