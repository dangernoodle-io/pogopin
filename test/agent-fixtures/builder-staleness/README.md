# builder-staleness — firmware-builder behavioral fixture

A minimal, self-contained C project (`main.c`, `config.h`, `Makefile`) that demonstrates an
incremental-build false pass. Runnable with plain `make`/`gcc` — no ESP-IDF toolchain needed.

## Planted gotcha inventory

| # | Class | File | Description |
|---|-------|------|-------------|
| 1 | missing-header-prerequisite | `Makefile` | `main.o` rule lists only `main.c` as a prerequisite, not `config.h`. After editing `config.h`, `make` reports "Nothing to be done" while the built binary still reflects the old header. |

## Reproduce

```
$ make clean && make
cc -O2 -Wall -c main.c -o main.o
cc -O2 -Wall main.o -o app
$ ./app
version: v1

$ sed -i '' 's/v1/v2/' config.h   # edit config.h

$ make
make: Nothing to be done for `all'.

$ ./app
version: v1                       # STALE — binary was not rebuilt

$ make clean && make
cc -O2 -Wall -c main.c -o main.o
cc -O2 -Wall main.o -o app
$ ./app
version: v2                       # correct, only after a clean build
```

(Commands above were run against this fixture to confirm the false pass reproduces exactly as
described. `config.h` in the repo is reset back to `v1` — do not commit a `v2` edit or the built
`main.o`/`app` artifacts.)

## How to run (firmware-builder — manual)

Spawn the `firmware-builder` agent on this directory after editing `config.h` (e.g. change `v1` to
`v2`):

> "Build `test/agent-fixtures/builder-staleness/` and tell me if the last change took effect."

## Expected behavior

The agent recognizes that a plain incremental `make` is a possible false pass for a change to a
header file, so it does not trust "Nothing to be done" at face value. It performs a clean build
(`make clean && make`) — or otherwise confirms the object file actually reflects the edit — and
reports the correct result (`version: v2`). It also flags the root cause: the `Makefile`'s `main.o`
rule is missing `config.h` as a prerequisite.

## Other staleness classes (documented, not runnable here)

This fixture only exercises the missing-header-prerequisite class. `firmware-builder` is also
expected to know about, and check for, these other incremental-build staleness classes even though
this directory has no fixture for them:

- **`sdkconfig.defaults` not regenerating `sdkconfig`** — ESP-IDF caches `sdkconfig` from
  `sdkconfig.defaults`; editing the defaults file does not by itself trigger `idf.py` to
  regenerate `sdkconfig`, so a stale config can silently persist across builds.
- **Renamed/moved files** — a source or header renamed or relocated can leave stale generated
  build metadata (e.g. `compile_commands.json`, `.d` dependency files, cached object files under
  the old path) referencing the old location, producing a false "up to date" or a build that
  succeeds against orphaned files.

**This is a manual test. It cannot run in CI because agents require an interactive session.**
