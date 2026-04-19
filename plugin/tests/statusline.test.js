const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('fs');
const path = require('path');
const os = require('os');
const { spawnSync } = require('child_process');

const scriptPath = path.resolve(__dirname, '..', 'scripts', 'statusline.js');

test('missing file returns "serial: idle"', () => {
  const tmpDir = fs.mkdtempSync(path.join(os.tmpdir(), 'pogopin-'));
  const statusPath = path.join(tmpDir, 'nonexistent', 'status.json');

  try {
    const result = spawnSync('node', [scriptPath], {
      env: { ...process.env, POGOPIN_STATUS_PATH: statusPath }
    });

    assert.equal(result.status, 0);
    assert.equal(result.stdout.toString().trim(), 'serial: idle');
  } finally {
    try {
      fs.rmSync(tmpDir, { recursive: true });
    } catch (e) {
      // ignore
    }
  }
});

test('empty ports returns "serial: idle"', () => {
  const tmpDir = fs.mkdtempSync(path.join(os.tmpdir(), 'pogopin-'));
  const statusPath = path.join(tmpDir, 'status.json');

  try {
    fs.writeFileSync(statusPath, JSON.stringify({ ports: [] }));

    const result = spawnSync('node', [scriptPath], {
      env: { ...process.env, POGOPIN_STATUS_PATH: statusPath }
    });

    assert.equal(result.status, 0);
    assert.equal(result.stdout.toString().trim(), 'serial: idle');
  } finally {
    try {
      fs.rmSync(tmpDir, { recursive: true });
    } catch (e) {
      // ignore
    }
  }
});

test('single port renders correctly', () => {
  const tmpDir = fs.mkdtempSync(path.join(os.tmpdir(), 'pogopin-'));
  const statusPath = path.join(tmpDir, 'status.json');

  try {
    fs.writeFileSync(statusPath, JSON.stringify({
      ports: [{ port: '/dev/ttyUSB0', baud: 115200, mode: 'monitor', buffer_lines: 100 }]
    }));

    const result = spawnSync('node', [scriptPath], {
      env: { ...process.env, POGOPIN_STATUS_PATH: statusPath }
    });

    assert.equal(result.status, 0);
    assert.equal(result.stdout.toString().trim(), 'serial: ttyUSB0@115200 monitor 100L');
  } finally {
    try {
      fs.rmSync(tmpDir, { recursive: true });
    } catch (e) {
      // ignore
    }
  }
});

test('multiple ports joined by " | "', () => {
  const tmpDir = fs.mkdtempSync(path.join(os.tmpdir(), 'pogopin-'));
  const statusPath = path.join(tmpDir, 'status.json');

  try {
    fs.writeFileSync(statusPath, JSON.stringify({
      ports: [
        { port: '/dev/ttyUSB0', baud: 115200, mode: 'monitor', buffer_lines: 100 },
        { port: '/dev/ttyUSB1', baud: 9600, mode: 'read', buffer_lines: 50 }
      ]
    }));

    const result = spawnSync('node', [scriptPath], {
      env: { ...process.env, POGOPIN_STATUS_PATH: statusPath }
    });

    assert.equal(result.status, 0);
    const output = result.stdout.toString().trim();
    assert.ok(output.includes(' | '), `expected output to contain " | ", got ${output}`);
    assert.ok(output.includes('ttyUSB0@115200 monitor 100L'));
    assert.ok(output.includes('ttyUSB1@9600 read 50L'));
  } finally {
    try {
      fs.rmSync(tmpDir, { recursive: true });
    } catch (e) {
      // ignore
    }
  }
});

test('malformed JSON returns "serial: idle"', () => {
  const tmpDir = fs.mkdtempSync(path.join(os.tmpdir(), 'pogopin-'));
  const statusPath = path.join(tmpDir, 'status.json');

  try {
    fs.writeFileSync(statusPath, 'not-json');

    const result = spawnSync('node', [scriptPath], {
      env: { ...process.env, POGOPIN_STATUS_PATH: statusPath }
    });

    assert.equal(result.status, 0);
    assert.equal(result.stdout.toString().trim(), 'serial: idle');
  } finally {
    try {
      fs.rmSync(tmpDir, { recursive: true });
    } catch (e) {
      // ignore
    }
  }
});
