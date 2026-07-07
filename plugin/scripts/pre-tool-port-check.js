#!/usr/bin/env node
// PreToolUse hook (BR-31): warns (never blocks) when a pogopin port-using tool
// targets a port that another Claude Code session (per CLAUDE_CODE_SESSION_ID)
// already holds/is running. Silent on the normal same-session flow, and fails
// open (no warning) when session identity is unavailable (older server,
// CLAUDE_CODE_SESSION_ID unset) or the recorded owner process is dead.

const fs = require('fs');
const path = require('path');
const os = require('os');

const PORT_TOOL_PREFIX = 'mcp__plugin_pogopin-mcp_pogopin__';
const STALE_MS = 30 * 1000;

// Mirror Go's os.UserCacheDir per-platform so this finds files written by pogopin.
function defaultCacheDir() {
  if (process.platform === 'darwin') return path.join(os.homedir(), 'Library', 'Caches');
  if (process.platform === 'win32') return process.env.LocalAppData || path.join(os.homedir(), 'AppData', 'Local');
  return process.env.XDG_CACHE_HOME || path.join(os.homedir(), '.cache');
}

function statusPath() {
  return process.env.POGOPIN_STATUS_PATH || path.join(defaultCacheDir(), 'pogopin', 'status.json');
}

// isPortTool returns true if tool_name is one of the pogopin port-using tools.
function isPortTool(toolName) {
  return typeof toolName === 'string' && toolName.startsWith(PORT_TOOL_PREFIX);
}

// readStatus returns a parsed, non-stale StatusFile, or null if missing/unreadable/
// malformed/stale. Never throws.
function readStatus(filePath, nowMs) {
  let raw;
  let stat;
  try {
    raw = fs.readFileSync(filePath, 'utf8');
    stat = fs.statSync(filePath);
  } catch (err) {
    return null;
  }

  let parsed;
  try {
    parsed = JSON.parse(raw);
  } catch (err) {
    return null;
  }

  if (!parsed || typeof parsed !== 'object' || !Array.isArray(parsed.ports)) return null;

  const updatedAtMs = typeof parsed.updated_at === 'number' ? parsed.updated_at * 1000 : stat.mtimeMs;
  if (typeof updatedAtMs !== 'number' || nowMs - updatedAtMs > STALE_MS) return null;

  return parsed;
}

// pidAlive returns true if pid names a live process (or one we can't signal
// due to permissions, which still means it's alive). Returns false for
// missing/non-integer pids and confirmed-dead (ESRCH) pids. Never throws.
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

  const status = readStatus(statusPath(), Date.now());
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

module.exports = { isPortTool, readStatus, findConflict, buildWarning, pidAlive, main, PORT_TOOL_PREFIX, STALE_MS };
