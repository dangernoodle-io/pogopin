---
name: flash
description: Flash firmware to an embedded board and confirm it boots — minimum-footprint (app partition only) by default, OTA-preferred, chip-aware reset. Spawns the board-operator subagent.
model: sonnet
---

Spawn the `board-operator` subagent with the user's args (target board/port, firmware path, and goal — or a prompt asking for them) as its initial message. It flashes the minimum necessary, verifies by hash, and confirms the board settles on the new firmware; it asks before any destructive operation (whole-chip erase, bootloader/partition-table flash, NVS wipe).
