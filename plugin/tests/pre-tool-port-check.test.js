const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('fs');
const path = require('path');
const os = require('os');
const { spawnSync } = require('child_process');

const scriptPath = path.resolve(__dirname, '..', 'scripts', 'pre-tool-port-check.js');
const TOOL = 'mcp__plugin_pogopin-mcp_pogopin__serial_start';

// A pid guaranteed dead: pick a very high pid and hope it's unused would be
// flaky, so instead spawn+wait a short-lived child and use its pid after it
// exits — reliable ESRCH on all platforms this test runs on.
function deadPid() {
  const res = spawnSync('node', ['-e', 'process.exit(0)']);
  return res.pid;
}

function run(input, statusDir) {
  const env = { ...process.env };
  if (statusDir !== undefined) env.POGOPIN_STATUS_DIR = statusDir;
  else delete env.POGOPIN_STATUS_DIR;
  return spawnSync('node', [scriptPath], { input: JSON.stringify(input), env, encoding: 'utf8' });
}

function withTmpStatusDir(fn) {
  const tmpDir = fs.mkdtempSync(path.join(os.tmpdir(), 'pogopin-hook-'));
  try {
    fn(tmpDir);
  } finally {
    try {
      fs.rmSync(tmpDir, { recursive: true });
    } catch (e) {
      // ignore
    }
  }
}

// writeStatus writes a single per-session status file <dir>/<pid>.json,
// mirroring what a live pogo server process would write for itself.
function writeStatus(statusDir, pid, ports, updatedAt = Math.floor(Date.now() / 1000)) {
  fs.writeFileSync(path.join(statusDir, `${pid}.json`), JSON.stringify({ ports, updated_at: updatedAt }));
}

test('no port arg in tool_input -> no warning, exit 0', () => {
  withTmpStatusDir(statusDir => {
    writeStatus(statusDir, process.pid, [{ port: '/dev/ttyUSB0', mode: 'reader', running: true, pid: process.pid }]);
    const result = run({ tool_name: TOOL, tool_input: {} }, statusDir);
    assert.equal(result.status, 0);
    assert.equal(result.stdout.trim(), '');
  });
});

test('missing status dir -> no warning, exit 0', () => {
  withTmpStatusDir(statusDir => {
    const missingDir = path.join(statusDir, 'nonexistent');
    const result = run({ tool_name: TOOL, tool_input: { port: '/dev/ttyUSB0' } }, missingDir);
    assert.equal(result.status, 0);
    assert.equal(result.stdout.trim(), '');
  });
});

test('stale session file -> no warning, exit 0', () => {
  withTmpStatusDir(statusDir => {
    const staleTs = Math.floor(Date.now() / 1000) - 120;
    writeStatus(
      statusDir,
      process.pid,
      [{ port: '/dev/ttyUSB0', mode: 'reader', running: true, pid: process.pid }],
      staleTs
    );
    const result = run({ tool_name: TOOL, tool_input: { port: '/dev/ttyUSB0' } }, statusDir);
    assert.equal(result.status, 0);
    assert.equal(result.stdout.trim(), '');
  });
});

test('port free/not conflicting -> no warning', () => {
  withTmpStatusDir(statusDir => {
    writeStatus(statusDir, process.pid, [{ port: '/dev/ttyUSB0', mode: 'reader', running: false, pid: process.pid }]);
    const result = run({ tool_name: TOOL, tool_input: { port: '/dev/ttyUSB0' } }, statusDir);
    assert.equal(result.status, 0);
    assert.equal(result.stdout.trim(), '');
  });
});

test('cross-session port busy/held with live owner pid -> warning emitted via additionalContext/systemMessage, no deny/ask', () => {
  withTmpStatusDir(statusDir => {
    writeStatus(statusDir, process.pid, [
      { port: '/dev/ttyUSB0', mode: 'reader', running: true, session_id: 'sess-owner', pid: process.pid },
    ]);
    const result = run(
      { session_id: 'sess-caller', tool_name: TOOL, tool_input: { port: '/dev/ttyUSB0' } },
      statusDir
    );
    assert.equal(result.status, 0);
    const out = JSON.parse(result.stdout.trim());
    assert.ok(out.systemMessage && out.systemMessage.length > 0);
    assert.ok(out.hookSpecificOutput.additionalContext.includes('/dev/ttyUSB0'));
    assert.ok(out.hookSpecificOutput.additionalContext.includes('another Claude Code session'));
    assert.equal(out.hookSpecificOutput.hookEventName, 'PreToolUse');
    assert.notEqual(out.hookSpecificOutput.permissionDecision, 'deny');
    assert.notEqual(out.hookSpecificOutput.permissionDecision, 'ask');
    assert.equal(out.hookSpecificOutput.permissionDecision, undefined);
  });
});

test('same-session port busy/held -> silent (normal same-session flow)', () => {
  withTmpStatusDir(statusDir => {
    writeStatus(statusDir, process.pid, [
      { port: '/dev/ttyUSB0', mode: 'reader', running: true, session_id: 'sess-same', pid: process.pid },
    ]);
    const result = run(
      { session_id: 'sess-same', tool_name: TOOL, tool_input: { port: '/dev/ttyUSB0' } },
      statusDir
    );
    assert.equal(result.status, 0);
    assert.equal(result.stdout.trim(), '');
  });
});

test('cross-session port busy/held but owner pid is dead -> silent (stale entry)', () => {
  withTmpStatusDir(statusDir => {
    const stalePid = deadPid();
    writeStatus(statusDir, stalePid, [
      { port: '/dev/ttyUSB0', mode: 'reader', running: true, session_id: 'sess-owner', pid: stalePid },
    ]);
    const result = run(
      { session_id: 'sess-caller', tool_name: TOOL, tool_input: { port: '/dev/ttyUSB0' } },
      statusDir
    );
    assert.equal(result.status, 0);
    assert.equal(result.stdout.trim(), '');
  });
});

test('entry without session_id (older server / env unset) -> silent (graceful degrade)', () => {
  withTmpStatusDir(statusDir => {
    writeStatus(statusDir, process.pid, [{ port: '/dev/ttyUSB0', mode: 'reader', running: true, pid: process.pid }]);
    const result = run(
      { session_id: 'sess-caller', tool_name: TOOL, tool_input: { port: '/dev/ttyUSB0' } },
      statusDir
    );
    assert.equal(result.status, 0);
    assert.equal(result.stdout.trim(), '');
  });
});

test('entry session_id and caller session_id both empty string -> silent (absent-guard precedes equality check)', () => {
  withTmpStatusDir(statusDir => {
    writeStatus(statusDir, process.pid, [
      { port: '/dev/ttyUSB0', mode: 'reader', running: true, session_id: '', pid: process.pid },
    ]);
    const result = run(
      { session_id: '', tool_name: TOOL, tool_input: { port: '/dev/ttyUSB0' } },
      statusDir
    );
    assert.equal(result.status, 0);
    assert.equal(result.stdout.trim(), '');
  });
});

test('malformed session file -> fail-open: skipped, no crash', () => {
  withTmpStatusDir(statusDir => {
    fs.writeFileSync(path.join(statusDir, `${process.pid}.json`), 'not-json{{{');
    const result = run({ tool_name: TOOL, tool_input: { port: '/dev/ttyUSB0' } }, statusDir);
    assert.equal(result.status, 0);
    assert.equal(result.stdout.trim(), '');
    assert.equal(result.stderr.trim(), '');
  });
});

test('non-pogopin tool_name -> no warning', () => {
  withTmpStatusDir(statusDir => {
    writeStatus(statusDir, process.pid, [{ port: '/dev/ttyUSB0', mode: 'reader', running: true, pid: process.pid }]);
    const result = run({ tool_name: 'Bash', tool_input: { port: '/dev/ttyUSB0' } }, statusDir);
    assert.equal(result.status, 0);
    assert.equal(result.stdout.trim(), '');
  });
});

test('malformed stdin JSON -> fail-open: no warning, exit 0', () => {
  withTmpStatusDir(statusDir => {
    writeStatus(statusDir, process.pid, [{ port: '/dev/ttyUSB0', mode: 'reader', running: true, pid: process.pid }]);
    const env = { ...process.env, POGOPIN_STATUS_DIR: statusDir };
    const result = spawnSync('node', [scriptPath], { input: 'not-json', env, encoding: 'utf8' });
    assert.equal(result.status, 0);
    assert.equal(result.stdout.trim(), '');
  });
});

test('two-session merge: cross-session conflict on one port, own port on another -> warning only for the conflicting port', () => {
  withTmpStatusDir(statusDir => {
    writeStatus(statusDir, process.pid, [
      { port: '/dev/ttyUSB0', mode: 'reader', running: true, session_id: 'sess-caller', pid: process.pid },
    ]);
    writeStatus(statusDir, process.pid + 1, [
      { port: '/dev/ttyUSB1', mode: 'reader', running: true, session_id: 'sess-owner', pid: process.pid },
    ]);
    const result = run(
      { session_id: 'sess-caller', tool_name: TOOL, tool_input: { port: '/dev/ttyUSB1' } },
      statusDir
    );
    assert.equal(result.status, 0);
    const out = JSON.parse(result.stdout.trim());
    assert.ok(out.hookSpecificOutput.additionalContext.includes('/dev/ttyUSB1'));
  });
});
