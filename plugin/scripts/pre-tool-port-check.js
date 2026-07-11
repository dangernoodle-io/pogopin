#!/usr/bin/env node
// PreToolUse hook (BR-31): warns (never blocks) when a pogopin port-using tool
// targets a port that another Claude Code session (per CLAUDE_CODE_SESSION_ID)
// already holds/is running. Silent on the normal same-session flow, and fails
// open (no warning) when session identity is unavailable (older server,
// CLAUDE_CODE_SESSION_ID unset) or the recorded owner process is dead.
//
// Reads the merged live-ports view via a self-contained status-dir reader
// (formerly the shared status-lib.js, retired alongside statusline.js in
// favor of the native `pogo statusline` command — BR-76). This hook is now
// the sole JS consumer of that reader, so it's inlined here rather than kept
// as a separate shared module. Globs the per-session status/<pid>.json
// directory and prunes dead-PID and stale (>45s) entries — so a portless
// session's own file can never clobber a concurrent session's port entry
// (the failure mode this hook used to be vulnerable to when all servers
// shared one status.json).

const fs = require('fs');
const path = require('path');
const os = require('os');

// Guards against PID reuse: a status file older than this is treated as
// dead even if its recorded PID happens to match a live process. 3x the
// 15s server heartbeat interval.
const STALE_SECONDS = 45;

// Mirror Go's os.UserCacheDir per-platform so this finds files written by pogopin.
function defaultCacheDir() {
  if (process.platform === 'darwin') return path.join(os.homedir(), 'Library', 'Caches');
  if (process.platform === 'win32') return process.env.LocalAppData || path.join(os.homedir(), 'AppData', 'Local');
  return process.env.XDG_CACHE_HOME || path.join(os.homedir(), '.cache');
}

function statusDir() {
  return process.env.POGOPIN_STATUS_DIR || path.join(defaultCacheDir(), 'pogopin', 'status');
}

// pidAlive returns true if pid names a live process (or one we can't signal
// due to permissions, which still means it's alive). Returns false for
// missing/non-integer/non-positive pids and confirmed-dead (ESRCH) pids.
// Never throws.
function pidAlive(pid) {
  if (typeof pid !== 'number' || !Number.isInteger(pid) || pid <= 0) return false;
  try {
    process.kill(pid, 0);
    return true;
  } catch (err) {
    if (err && err.code === 'EPERM') return true;
    return false;
  }
}

// pidFromEntry derives the owning PID for a status file: prefer a
// PortState.pid value if present on any port, otherwise fall back to the
// filename stem (<pid>.json).
function pidFromEntry(fileName, parsed) {
  const ports = Array.isArray(parsed.ports) ? parsed.ports : [];
  for (const p of ports) {
    if (p && typeof p.pid === 'number' && p.pid > 0) return p.pid;
  }
  const stem = path.basename(fileName, '.json');
  const n = Number(stem);
  return Number.isInteger(n) ? n : NaN;
}

// readLivePorts globs statusDir() for per-process status files, drops ports
// belonging to a file whose owning process is dead or whose updated_at is
// older than STALE_SECONDS, and merges the surviving ports[] from all files
// into one flat array. Fully fail-open: any error (missing dir, bad json)
// results in that file (or the whole call) contributing no ports. Never
// throws.
function readLivePorts() {
  let entries;
  try {
    entries = fs.readdirSync(statusDir());
  } catch (err) {
    return [];
  }

  const nowSec = Date.now() / 1000;
  const merged = [];

  for (const name of entries) {
    if (!name.endsWith('.json')) continue;

    let parsed;
    try {
      const raw = fs.readFileSync(path.join(statusDir(), name), 'utf8');
      parsed = JSON.parse(raw);
    } catch (err) {
      continue;
    }

    if (!parsed || typeof parsed !== 'object' || !Array.isArray(parsed.ports)) continue;

    const updatedAt = typeof parsed.updated_at === 'number' ? parsed.updated_at : 0;
    if (nowSec - updatedAt > STALE_SECONDS) continue;

    const pid = pidFromEntry(name, parsed);
    if (!pidAlive(pid)) continue;

    merged.push(...parsed.ports);
  }

  return merged;
}

const PORT_TOOL_PREFIX = 'mcp__plugin_pogopin-mcp_pogopin__';

// isPortTool returns true if tool_name is one of the pogopin port-using tools.
function isPortTool(toolName) {
  return typeof toolName === 'string' && toolName.startsWith(PORT_TOOL_PREFIX);
}

// readStatus returns the merged live ports as a { ports } shape (matching
// the previous single-file StatusFile interface), or null if there are no
// live ports. Never throws (readLivePorts is fail-open).
function readStatus() {
  const ports = readLivePorts();
  if (!Array.isArray(ports) || ports.length === 0) return null;
  return { ports };
}

// findConflict returns the busy PortState entry for `port` iff it represents
// a genuine cross-session conflict, or null otherwise (free/absent/same-
// session/stale/no-session-identity — all fail open, no warning).
function findConflict(status, port, eventSessionId) {
  if (!status || !port) return null;
  const entry = status.ports.find(p => p && p.port === port);
  if (!entry || !entry.running) return null;

  // No session identity recorded (older server / CLAUDE_CODE_SESSION_ID
  // unset): can't tell same- from cross-session, so don't warn.
  if (!entry.session_id) return null;

  // Same session owns this port — the normal same-session flow. Silent.
  if (entry.session_id === eventSessionId) return null;

  // Different session. Only warn if the owning process is actually alive;
  // a dead owner means a stale entry, not a real conflict.
  if (!pidAlive(entry.pid)) return null;

  return entry;
}

function buildWarning(toolName, entry) {
  const text = `pogopin: port ${entry.port} is in use by another Claude Code session ` +
    `(mode=${entry.mode}, running=true) — ` +
    `${toolName.slice(PORT_TOOL_PREFIX.length)} may conflict with that session`;
  return {
    systemMessage: text,
    hookSpecificOutput: {
      hookEventName: 'PreToolUse',
      additionalContext: text,
    },
  };
}

function main(input) {
  let parsed;
  try {
    parsed = JSON.parse(input);
  } catch (err) {
    return null;
  }

  if (!parsed || typeof parsed !== 'object') return null;
  if (!isPortTool(parsed.tool_name)) return null;

  const port = parsed.tool_input && parsed.tool_input.port;
  if (!port || typeof port !== 'string') return null;

  const status = readStatus();
  if (!status) return null;

  const conflict = findConflict(status, port, parsed.session_id);
  if (!conflict) return null;

  return buildWarning(parsed.tool_name, conflict);
}

if (require.main === module) {
  try {
    const chunks = [];
    process.stdin.on('data', c => chunks.push(c));
    process.stdin.on('end', () => {
      try {
        const result = main(Buffer.concat(chunks).toString('utf8'));
        if (result) process.stdout.write(JSON.stringify(result));
      } catch (err) {
        // fail open: never block, never crash noisily
      }
      process.exit(0);
    });
  } catch (err) {
    process.exit(0);
  }
}

module.exports = { isPortTool, readStatus, findConflict, buildWarning, pidAlive, main, PORT_TOOL_PREFIX, defaultCacheDir };
