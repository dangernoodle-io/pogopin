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

function run(input, statusPath) {
  const env = { ...process.env };
  if (statusPath !== undefined) env.POGOPIN_STATUS_PATH = statusPath;
  else delete env.POGOPIN_STATUS_PATH;
  return spawnSync('node', [scriptPath], { input: JSON.stringify(input), env, encoding: 'utf8' });
}

function withTmpStatus(fn) {
  const tmpDir = fs.mkdtempSync(path.join(os.tmpdir(), 'pogopin-hook-'));
  const statusPath = path.join(tmpDir, 'status.json');
  try {
    fn(statusPath);
  } finally {
    try {
      fs.rmSync(tmpDir, { recursive: true });
    } catch (e) {
      // ignore
    }
  }
}

function writeStatus(statusPath, ports, updatedAt = Math.floor(Date.now() / 1000)) {
  fs.writeFileSync(statusPath, JSON.stringify({ ports, updated_at: updatedAt }));
}

test('no port arg in tool_input -> no warning, exit 0', () => {
  withTmpStatus(statusPath => {
    writeStatus(statusPath, [{ port: '/dev/ttyUSB0', mode: 'reader', running: true }]);
    const result = run({ tool_name: TOOL, tool_input: {} }, statusPath);
    assert.equal(result.status, 0);
    assert.equal(result.stdout.trim(), '');
  });
});

test('missing status.json -> no warning, exit 0', () => {
  withTmpStatus(statusPath => {
    const result = run({ tool_name: TOOL, tool_input: { port: '/dev/ttyUSB0' } }, statusPath);
    assert.equal(result.status, 0);
    assert.equal(result.stdout.trim(), '');
  });
});

test('stale status.json -> no warning, exit 0', () => {
  withTmpStatus(statusPath => {
    const staleTs = Math.floor(Date.now() / 1000) - 120;
    writeStatus(statusPath, [{ port: '/dev/ttyUSB0', mode: 'reader', running: true }], staleTs);
    const result = run({ tool_name: TOOL, tool_input: { port: '/dev/ttyUSB0' } }, statusPath);
    assert.equal(result.status, 0);
    assert.equal(result.stdout.trim(), '');
  });
});

test('port free/not conflicting -> no warning', () => {
  withTmpStatus(statusPath => {
    writeStatus(statusPath, [{ port: '/dev/ttyUSB0', mode: 'reader', running: false }]);
    const result = run({ tool_name: TOOL, tool_input: { port: '/dev/ttyUSB0' } }, statusPath);
    assert.equal(result.status, 0);
    assert.equal(result.stdout.trim(), '');
  });
});

test('cross-session port busy/held with live owner pid -> warning emitted via additionalContext/systemMessage, no deny/ask', () => {
  withTmpStatus(statusPath => {
    writeStatus(statusPath, [
      { port: '/dev/ttyUSB0', mode: 'reader', running: true, session_id: 'sess-owner', pid: process.pid },
    ]);
    const result = run(
      { session_id: 'sess-caller', tool_name: TOOL, tool_input: { port: '/dev/ttyUSB0' } },
      statusPath
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
  withTmpStatus(statusPath => {
    writeStatus(statusPath, [
      { port: '/dev/ttyUSB0', mode: 'reader', running: true, session_id: 'sess-same', pid: process.pid },
    ]);
    const result = run(
      { session_id: 'sess-same', tool_name: TOOL, tool_input: { port: '/dev/ttyUSB0' } },
      statusPath
    );
    assert.equal(result.status, 0);
    assert.equal(result.stdout.trim(), '');
  });
});

test('cross-session port busy/held but owner pid is dead -> silent (stale entry)', () => {
  withTmpStatus(statusPath => {
    const stalePid = deadPid();
    writeStatus(statusPath, [
      { port: '/dev/ttyUSB0', mode: 'reader', running: true, session_id: 'sess-owner', pid: stalePid },
    ]);
    const result = run(
      { session_id: 'sess-caller', tool_name: TOOL, tool_input: { port: '/dev/ttyUSB0' } },
      statusPath
    );
    assert.equal(result.status, 0);
    assert.equal(result.stdout.trim(), '');
  });
});

test('entry without session_id (older server / env unset) -> silent (graceful degrade)', () => {
  withTmpStatus(statusPath => {
    writeStatus(statusPath, [{ port: '/dev/ttyUSB0', mode: 'reader', running: true, pid: process.pid }]);
    const result = run(
      { session_id: 'sess-caller', tool_name: TOOL, tool_input: { port: '/dev/ttyUSB0' } },
      statusPath
    );
    assert.equal(result.status, 0);
    assert.equal(result.stdout.trim(), '');
  });
});

test('entry session_id and caller session_id both empty string -> silent (absent-guard precedes equality check)', () => {
  withTmpStatus(statusPath => {
    writeStatus(statusPath, [
      { port: '/dev/ttyUSB0', mode: 'reader', running: true, session_id: '', pid: process.pid },
    ]);
    const result = run(
      { session_id: '', tool_name: TOOL, tool_input: { port: '/dev/ttyUSB0' } },
      statusPath
    );
    assert.equal(result.status, 0);
    assert.equal(result.stdout.trim(), '');
  });
});

test('malformed status.json -> fail-open: no warning, exit 0, no crash', () => {
  withTmpStatus(statusPath => {
    fs.writeFileSync(statusPath, 'not-json{{{');
    const result = run({ tool_name: TOOL, tool_input: { port: '/dev/ttyUSB0' } }, statusPath);
    assert.equal(result.status, 0);
    assert.equal(result.stdout.trim(), '');
    assert.equal(result.stderr.trim(), '');
  });
});

test('non-pogopin tool_name -> no warning', () => {
  withTmpStatus(statusPath => {
    writeStatus(statusPath, [{ port: '/dev/ttyUSB0', mode: 'reader', running: true }]);
    const result = run({ tool_name: 'Bash', tool_input: { port: '/dev/ttyUSB0' } }, statusPath);
    assert.equal(result.status, 0);
    assert.equal(result.stdout.trim(), '');
  });
});

test('malformed stdin JSON -> fail-open: no warning, exit 0', () => {
  withTmpStatus(statusPath => {
    writeStatus(statusPath, [{ port: '/dev/ttyUSB0', mode: 'reader', running: true }]);
    const env = { ...process.env, POGOPIN_STATUS_PATH: statusPath };
    const result = spawnSync('node', [scriptPath], { input: 'not-json', env, encoding: 'utf8' });
    assert.equal(result.status, 0);
    assert.equal(result.stdout.trim(), '');
  });
});
