---
name: board-medic
description: Diagnose why an embedded board isn't booting, panicking, bootlooping, or stuck in download mode. Spawns the board-medic subagent for observation-first diagnostics.
model: sonnet
---

Spawn the `board-medic` subagent with the user's args (or a prompt asking them to describe the symptoms) as its initial message. Return its recommended actions to the user for confirmation before executing anything destructive.
