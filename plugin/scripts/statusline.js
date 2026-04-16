#!/usr/bin/env node

const fs = require('fs');
const path = require('path');
const os = require('os');

try {
  // Resolve status path from env var or default
  const statusPath = process.env.BREADBOARD_STATUS_PATH ||
    path.join(os.homedir(), '.cache/breadboard/status.json');

  // Try to read and stat the file
  let statusFile;
  try {
    const data = fs.readFileSync(statusPath, 'utf8');
    statusFile = JSON.parse(data);
  } catch (err) {
    // File missing or read fails
    process.exit(0);
  }

  // Check staleness: (now - updated_at) > 30 seconds
  const now = Date.now() / 1000;
  if (!statusFile.updated_at || (now - statusFile.updated_at > 30)) {
    process.exit(0);
  }

  // Check if ports is missing or empty
  if (!statusFile.ports || statusFile.ports.length === 0) {
    process.exit(0);
  }

  // Format each port entry
  const segments = statusFile.ports.map(portEntry => {
    const shortPort = path.basename(portEntry.port);
    return `serial: ${shortPort}@${portEntry.baud} ${portEntry.mode} ${portEntry.buffer_lines}L`;
  });

  // Join with " | " and print
  const result = segments.join(' | ');
  console.log(result);
  process.exit(0);
} catch (err) {
  // Any unexpected throw: silent exit 0
  process.exit(0);
}
