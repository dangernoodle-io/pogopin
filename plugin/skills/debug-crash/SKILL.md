---
name: debug-crash
description: Decode an ESP32 panic backtrace and explain the crash. Point it at a backtrace (or a panicking board) plus the matching ELF. Spawns the board-medic subagent focused on crash decode.
model: sonnet
---

Spawn the `board-medic` subagent with the user's args (the panic backtrace and/or the affected port, plus the ELF path — or a prompt asking for them) as its initial message, scoped to decoding the crash and naming the cause. Destructive recovery goes to `board-operator` via you.
