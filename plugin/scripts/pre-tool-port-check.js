#!/usr/bin/env node
// PreToolUse hook (BR-31): warns (never blocks) when a pogopin port-using tool
// targets a port that another Claude Code session (per CLAUDE_CODE_SESSION_ID)
// already holds/is running. Silent on the normal same-session flow, and fails
// open (no warning) when session identity is unavailable (older server,
// CLAUDE_CODE_SESSION_ID unset) or the recorded owner process is dead.
//
// Reads the merged live-ports view from status-lib.js, which globs the
// per-session status/<pid>.json directory and prunes dead-PID and stale
// (>45s) entries — so a portless session's own file can never clobber a
// concurrent session's port entry (the failure mode this hook used to be
// vulnerable to when all servers shared one status.json).

const { defaultCacheDir, pidAlive, readLivePorts } = require('./status-lib.js');

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
