// Shared status-dir reader for pogopin's per-session status files.
//
// Each pogo MCP server process writes its OWN file, <statusDir>/<pid>.json
// (see internal/status/status.go), instead of all servers overwriting one
// shared status.json (which caused last-writer-wins clobbering across
// concurrent Claude Code sessions). readLivePorts() globs that directory,
// drops entries whose owning process is dead or whose data is stale, and
// merges the survivors — giving callers a single flat ports[] view.
//
// Node builtins only — no external deps.

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

module.exports = { defaultCacheDir, statusDir, pidAlive, readLivePorts, STALE_SECONDS };
