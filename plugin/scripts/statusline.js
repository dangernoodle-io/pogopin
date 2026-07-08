#!/usr/bin/env node

const path = require('path');
const { readLivePorts } = require('./status-lib.js');

// Modes for POGOPIN_STATUSLINE_MODE, controlling widget visibility:
//   always      (default) render live ports; print "serial: idle" when none.
//   ports-only  render live ports; when none, exit 0 silently (no output).
//   fresh-only  render only ports fresher than FRESH_SECONDS; when none
//               qualify, exit 0 silently.
// Unknown/empty values fall back to "always" (safe default; preserves
// pre-BR-8 behavior).
const VALID_MODES = ['always', 'ports-only', 'fresh-only'];
const FRESH_SECONDS = 30;

function statuslineMode() {
  const mode = process.env.POGOPIN_STATUSLINE_MODE;
  return VALID_MODES.includes(mode) ? mode : 'always';
}

try {
  const mode = statuslineMode();
  let ports = readLivePorts();

  const sessionId = process.env.CLAUDE_CODE_SESSION_ID;
  if (sessionId) {
    ports = ports.filter(p => p && p.session_id === sessionId);
  }

  if (mode === 'fresh-only') {
    const nowSec = Date.now() / 1000;
    ports = ports.filter(p => {
      const updatedAt = typeof p.updated_at === 'number' ? p.updated_at : 0;
      return nowSec - updatedAt <= FRESH_SECONDS;
    });
  }

  if (ports.length === 0) {
    if (mode === 'ports-only' || mode === 'fresh-only') {
      process.exit(0);
    }
    console.log('serial: idle');
    process.exit(0);
  }

  const segments = ports.map(p =>
    `serial: ${path.basename(p.port)}@${p.baud} ${p.mode} ${p.buffer_lines}L`
  );
  console.log(segments.join(' | '));
  process.exit(0);
} catch (err) {
  // Any unexpected throw: silent exit 0
  process.exit(0);
}
