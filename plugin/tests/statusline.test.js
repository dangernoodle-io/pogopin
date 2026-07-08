const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('fs');
const path = require('path');
const os = require('os');
const { spawnSync } = require('child_process');

const scriptPath = path.resolve(__dirname, '..', 'scripts', 'statusline.js');

function withTmpDir(fn) {
  const tmpDir = fs.mkdtempSync(path.join(os.tmpdir(), 'pogopin-statusline-'));
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

function writeStatusFile(dir, pid, ports, updatedAt = Math.floor(Date.now() / 1000)) {
  fs.writeFileSync(path.join(dir, `${pid}.json`), JSON.stringify({ ports, updated_at: updatedAt }));
}

function run(statusDir, env = {}) {
  // Explicitly clear CLAUDE_CODE_SESSION_ID: this test suite may itself run
  // inside a Claude Code session, whose ambient session id would otherwise
  // leak into the child and unintentionally trigger the own-session filter.
  const base = { ...process.env };
  delete base.CLAUDE_CODE_SESSION_ID;
  return spawnSync('node', [scriptPath], {
    env: { ...base, POGOPIN_STATUS_DIR: statusDir, ...env },
    encoding: 'utf8',
  });
}

test('missing status dir returns "serial: idle"', () => {
  withTmpDir(tmpDir => {
    const missingDir = path.join(tmpDir, 'nonexistent');
    const result = run(missingDir);
    assert.equal(result.status, 0);
    assert.equal(result.stdout.trim(), 'serial: idle');
  });
});

test('empty ports returns "serial: idle"', () => {
  withTmpDir(tmpDir => {
    writeStatusFile(tmpDir, process.pid, []);
    const result = run(tmpDir);
    assert.equal(result.status, 0);
    assert.equal(result.stdout.trim(), 'serial: idle');
  });
});

test('single port renders correctly', () => {
  withTmpDir(tmpDir => {
    writeStatusFile(tmpDir, process.pid, [
      { port: '/dev/ttyUSB0', baud: 115200, mode: 'monitor', buffer_lines: 100, pid: process.pid },
    ]);
    const result = run(tmpDir);
    assert.equal(result.status, 0);
    assert.equal(result.stdout.trim(), 'serial: ttyUSB0@115200 monitor 100L');
  });
});

test('multiple session files merged and joined by " | "', () => {
  withTmpDir(tmpDir => {
    writeStatusFile(tmpDir, process.pid, [
      { port: '/dev/ttyUSB0', baud: 115200, mode: 'monitor', buffer_lines: 100, pid: process.pid },
    ]);
    // A second, distinct live session file (own pid reused via filename since
    // we can't easily spawn another long-lived pid here; the important part
    // is exercising merge across multiple files).
    writeStatusFile(tmpDir, process.pid + 1, [
      { port: '/dev/ttyUSB1', baud: 9600, mode: 'read', buffer_lines: 50, pid: process.pid },
    ]);

    const result = run(tmpDir);
    assert.equal(result.status, 0);
    const output = result.stdout.trim();
    assert.ok(output.includes(' | '), `expected output to contain " | ", got ${output}`);
    assert.ok(output.includes('ttyUSB0@115200 monitor 100L'));
    assert.ok(output.includes('ttyUSB1@9600 read 50L'));
  });
});

test('malformed JSON file is skipped, other live files still shown', () => {
  withTmpDir(tmpDir => {
    fs.writeFileSync(path.join(tmpDir, `${process.pid + 2}.json`), 'not-json');
    writeStatusFile(tmpDir, process.pid, [
      { port: '/dev/ttyUSB0', baud: 115200, mode: 'monitor', buffer_lines: 100, pid: process.pid },
    ]);

    const result = run(tmpDir);
    assert.equal(result.status, 0);
    assert.equal(result.stdout.trim(), 'serial: ttyUSB0@115200 monitor 100L');
  });
});

test('all files malformed/missing dir returns "serial: idle"', () => {
  withTmpDir(tmpDir => {
    fs.writeFileSync(path.join(tmpDir, `${process.pid}.json`), 'not-json');
    const result = run(tmpDir);
    assert.equal(result.status, 0);
    assert.equal(result.stdout.trim(), 'serial: idle');
  });
});

test('dead-pid session file is pruned', () => {
  withTmpDir(tmpDir => {
    const dead = spawnSync('node', ['-e', 'process.exit(0)']);
    writeStatusFile(tmpDir, dead.pid, [
      { port: '/dev/ttyUSB0', baud: 115200, mode: 'monitor', buffer_lines: 100, pid: dead.pid },
    ]);
    const result = run(tmpDir);
    assert.equal(result.status, 0);
    assert.equal(result.stdout.trim(), 'serial: idle');
  });
});

test('stale session file is pruned even with live pid', () => {
  withTmpDir(tmpDir => {
    const staleTs = Math.floor(Date.now() / 1000) - 120;
    writeStatusFile(
      tmpDir,
      process.pid,
      [{ port: '/dev/ttyUSB0', baud: 115200, mode: 'monitor', buffer_lines: 100, pid: process.pid }],
      staleTs
    );
    const result = run(tmpDir);
    assert.equal(result.status, 0);
    assert.equal(result.stdout.trim(), 'serial: idle');
  });
});

test('CLAUDE_CODE_SESSION_ID set filters to own-session ports only', () => {
  withTmpDir(tmpDir => {
    writeStatusFile(tmpDir, process.pid, [
      { port: '/dev/ttyUSB0', baud: 115200, mode: 'monitor', buffer_lines: 100, pid: process.pid, session_id: 'sess-own' },
    ]);
    writeStatusFile(tmpDir, process.pid + 3, [
      { port: '/dev/ttyUSB1', baud: 9600, mode: 'read', buffer_lines: 50, pid: process.pid, session_id: 'sess-other' },
    ]);

    const result = run(tmpDir, { CLAUDE_CODE_SESSION_ID: 'sess-own' });
    assert.equal(result.status, 0);
    const output = result.stdout.trim();
    assert.equal(output, 'serial: ttyUSB0@115200 monitor 100L');
    assert.ok(!output.includes('ttyUSB1'));
  });
});
