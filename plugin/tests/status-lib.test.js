const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('fs');
const path = require('path');
const os = require('os');
const { spawnSync } = require('child_process');

function withTmpDir(fn) {
  const tmpDir = fs.mkdtempSync(path.join(os.tmpdir(), 'pogopin-status-lib-'));
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

function withStatusDir(dir, fn) {
  const prev = process.env.POGOPIN_STATUS_DIR;
  process.env.POGOPIN_STATUS_DIR = dir;
  // Reload the module so statusDir() re-reads the env var each time.
  delete require.cache[require.resolve('../scripts/status-lib.js')];
  const lib = require('../scripts/status-lib.js');
  try {
    fn(lib);
  } finally {
    if (prev === undefined) delete process.env.POGOPIN_STATUS_DIR;
    else process.env.POGOPIN_STATUS_DIR = prev;
    delete require.cache[require.resolve('../scripts/status-lib.js')];
  }
}

test('readLivePorts: missing dir returns []', () => {
  withTmpDir(tmpDir => {
    withStatusDir(path.join(tmpDir, 'nonexistent'), lib => {
      assert.deepEqual(lib.readLivePorts(), []);
    });
  });
});

test('readLivePorts: merges ports across multiple live files', () => {
  withTmpDir(tmpDir => {
    writeStatusFile(tmpDir, process.pid, [{ port: '/dev/ttyUSB0', pid: process.pid }]);
    writeStatusFile(tmpDir, process.pid + 1, [{ port: '/dev/ttyUSB1', pid: process.pid }]);
    withStatusDir(tmpDir, lib => {
      const ports = lib.readLivePorts();
      assert.equal(ports.length, 2);
      const names = ports.map(p => p.port).sort();
      assert.deepEqual(names, ['/dev/ttyUSB0', '/dev/ttyUSB1']);
    });
  });
});

test('readLivePorts: drops dead-pid file', () => {
  withTmpDir(tmpDir => {
    const dead = spawnSync('node', ['-e', 'process.exit(0)']);
    writeStatusFile(tmpDir, dead.pid, [{ port: '/dev/ttyUSB0', pid: dead.pid }]);
    writeStatusFile(tmpDir, process.pid, [{ port: '/dev/ttyUSB1', pid: process.pid }]);
    withStatusDir(tmpDir, lib => {
      const ports = lib.readLivePorts();
      assert.equal(ports.length, 1);
      assert.equal(ports[0].port, '/dev/ttyUSB1');
    });
  });
});

test('readLivePorts: drops stale file even with live pid', () => {
  withTmpDir(tmpDir => {
    const staleTs = Math.floor(Date.now() / 1000) - 120;
    writeStatusFile(tmpDir, process.pid, [{ port: '/dev/ttyUSB0', pid: process.pid }], staleTs);
    withStatusDir(tmpDir, lib => {
      assert.deepEqual(lib.readLivePorts(), []);
    });
  });
});

test('readLivePorts: skips unparseable file, keeps live ones', () => {
  withTmpDir(tmpDir => {
    fs.writeFileSync(path.join(tmpDir, 'garbage.json'), 'not-json');
    writeStatusFile(tmpDir, process.pid, [{ port: '/dev/ttyUSB0', pid: process.pid }]);
    withStatusDir(tmpDir, lib => {
      const ports = lib.readLivePorts();
      assert.equal(ports.length, 1);
      assert.equal(ports[0].port, '/dev/ttyUSB0');
    });
  });
});

test('pidAlive: own pid true, 0/negative/dead false', () => {
  withTmpDir(tmpDir => {
    withStatusDir(tmpDir, lib => {
      assert.equal(lib.pidAlive(process.pid), true);
      assert.equal(lib.pidAlive(0), false);
      assert.equal(lib.pidAlive(-1), false);
      const dead = spawnSync('node', ['-e', 'process.exit(0)']);
      assert.equal(lib.pidAlive(dead.pid), false);
    });
  });
});
