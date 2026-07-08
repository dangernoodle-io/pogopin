#!/usr/bin/env node

const path = require('path');
const { readLivePorts } = require('./status-lib.js');

try {
  let ports = readLivePorts();

  const sessionId = process.env.CLAUDE_CODE_SESSION_ID;
  if (sessionId) {
    ports = ports.filter(p => p && p.session_id === sessionId);
  }

  if (ports.length === 0) {
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
