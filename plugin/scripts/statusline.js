#!/usr/bin/env node

const fs = require('fs');
const path = require('path');
const os = require('os');

// Mirror Go's os.UserCacheDir per-platform so the widget finds files written by pogopin.
function defaultCacheDir() {
  if (process.platform === 'darwin') return path.join(os.homedir(), 'Library', 'Caches');
  if (process.platform === 'win32') return process.env.LocalAppData || path.join(os.homedir(), 'AppData', 'Local');
  return process.env.XDG_CACHE_HOME || path.join(os.homedir(), '.cache');
}

try {
  const statusPath = process.env.POGOPIN_STATUS_PATH ||
    path.join(defaultCacheDir(), 'pogopin', 'status.json');

  let statusFile;
  try {
    statusFile = JSON.parse(fs.readFileSync(statusPath, 'utf8'));
  } catch (err) {
    // No file yet — pogopin hasn't run this session
    console.log('serial: idle');
    process.exit(0);
  }

  const ports = statusFile.ports || [];
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
