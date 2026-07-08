#!/usr/bin/env node
// SessionStart self-heal + installer (BR-4). Single pure-Node script: no
// install.sh, no npm deps, no unzip. Installs the pogo binary (dev path,
// local Homebrew, or GitHub release) and validates the plugin's own files
// are on disk, repairing the binary when it's missing/broken.
//
// Fully fail-open: every check and every install step is advisory. Any
// error (missing env var, unreadable file, network failure) is logged to
// stderr and the process still exits 0 — this hook must never block
// session start.
//
// Path resolution (all via env, no hardcoded ~/.claude):
//   - CLAUDE_CONFIG_DIR (default ~/.claude)         -> settings.json (read-only)
//   - CLAUDE_PLUGIN_ROOT                            -> hooks/*.json + scripts/*
//   - CLAUDE_PLUGIN_DATA                             -> bin/pogo, .version
//   - POGOPIN_DEV_BINARY                             -> local dev binary override
//
// Release archives are tar.gz on every platform (darwin included — see
// .goreleaser.yml), so extraction only needs zlib.gunzipSync + an in-house
// minimal ustar reader. No unzip, no third-party tar/https-follow-redirects
// packages.
//
// Deliberately does NOT write settings.json (rejected as an anti-pattern,
// BR-33) — hook registration stays static in hooks.json.

const fs = require('fs');
const path = require('path');
const os = require('os');
const https = require('https');
const zlib = require('zlib');
const crypto = require('crypto');
const { spawnSync } = require('child_process');

const REPO = 'dangernoodle-io/pogopin';
const USER_AGENT = 'pogopin-self-heal';

function configDir() {
  return process.env.CLAUDE_CONFIG_DIR || path.join(os.homedir(), '.claude');
}

function log(msg) {
  try {
    console.error(`pogopin: ${msg}`);
  } catch (err) {
    // stderr unavailable: nothing more we can do, stay fail-open.
  }
}

function isExecutable(p) {
  try {
    fs.accessSync(p, fs.constants.X_OK);
    return true;
  } catch (err) {
    return false;
  }
}

// statSig returns a "<epoch-seconds> <size>" signature for change detection,
// mirroring install.sh's `stat -f '%m %z'` / `stat -c '%Y %s'` comparison.
function statSig(p) {
  try {
    const st = fs.statSync(p);
    return `${Math.floor(st.mtimeMs / 1000)} ${st.size}`;
  } catch (err) {
    return null;
  }
}

function sigEqual(a, b) {
  return a !== null && a !== undefined && a === b;
}

// atomicInstallBinary writes `content` to `destPath` via a same-directory
// temp file (so the rename is on one filesystem), chmods it executable,
// best-effort mac-codesigns it, then atomically renames it into place.
function atomicInstallBinary(destPath, content) {
  const dir = path.dirname(destPath);
  fs.mkdirSync(dir, { recursive: true });
  const tmpPath = path.join(dir, `${path.basename(destPath)}.tmp.${process.pid}`);
  fs.writeFileSync(tmpPath, content);
  fs.chmodSync(tmpPath, 0o755);
  if (process.platform === 'darwin') {
    try {
      spawnSync('codesign', ['-s', '-', tmpPath], { timeout: 10000 });
    } catch (err) {
      // best-effort only
    }
  }
  fs.renameSync(tmpPath, destPath);
}

// --- validation ---------------------------------------------------------

// checkBinary reports whether <CLAUDE_PLUGIN_DATA>/bin/pogo exists and is
// executable. install() always installs to this final path regardless of
// how it sourced the binary (GitHub release, local Homebrew, or
// POGOPIN_DEV_BINARY), so this single path check mirrors its outcome.
function checkBinary(pluginData) {
  if (!pluginData) return { ok: false, reason: 'CLAUDE_PLUGIN_DATA not set' };
  const binary = path.join(pluginData, 'bin', 'pogo');
  if (isExecutable(binary)) return { ok: true, binary };
  return { ok: false, reason: `binary missing or not executable: ${binary}`, binary };
}

// collectHookCommands walks an arbitrary hooks.json shape and returns every
// { type: "command", command } string found, regardless of event name —
// robust to new SessionStart/etc. entries being added later.
function collectHookCommands(hooksJson) {
  const commands = [];
  function walk(node) {
    if (Array.isArray(node)) {
      node.forEach(walk);
      return;
    }
    if (node && typeof node === 'object') {
      if (node.type === 'command' && typeof node.command === 'string') {
        commands.push(node.command);
      }
      Object.values(node).forEach(walk);
    }
  }
  walk(hooksJson);
  return commands;
}

// extractPluginPaths resolves ${CLAUDE_PLUGIN_ROOT} (and bare
// $CLAUDE_PLUGIN_ROOT) in `command` the way Claude Code would, then returns
// every whitespace-delimited token that starts with pluginRoot (i.e. every
// plugin-owned script path referenced).
function extractPluginPaths(command, pluginRoot) {
  if (!pluginRoot) return [];
  const resolved = command
    .split('${CLAUDE_PLUGIN_ROOT}')
    .join(pluginRoot)
    .split('$CLAUDE_PLUGIN_ROOT')
    .join(pluginRoot);
  return resolved.split(/\s+/).filter(tok => tok.startsWith(pluginRoot));
}

// checkHookScripts verifies every script referenced by hooks/hooks.json
// exists under CLAUDE_PLUGIN_ROOT. Missing scripts mean a corrupt plugin
// install — install() only manages the binary and cannot repair this, so
// the caller just logs an actionable message.
function checkHookScripts(pluginRoot) {
  if (!pluginRoot) return { ok: false, missing: [], reason: 'CLAUDE_PLUGIN_ROOT not set' };
  const hooksPath = path.join(pluginRoot, 'hooks', 'hooks.json');
  let hooksJson;
  try {
    hooksJson = JSON.parse(fs.readFileSync(hooksPath, 'utf8'));
  } catch (err) {
    return { ok: false, missing: [], reason: `cannot read/parse ${hooksPath}: ${err.message}` };
  }
  const seen = new Set();
  const missing = [];
  for (const cmd of collectHookCommands(hooksJson)) {
    for (const p of extractPluginPaths(cmd, pluginRoot)) {
      if (seen.has(p)) continue;
      seen.add(p);
      if (!fs.existsSync(p)) missing.push(p);
    }
  }
  return { ok: missing.length === 0, missing };
}

// checkStatusline is a best-effort, read-only look at the user's
// settings.json for a `statusLine.command` that references a plugin script,
// and confirms that script still exists. No settings.json / no statusLine /
// unparsable JSON all mean "nothing to validate" (skipped, not a failure).
// Never writes settings.json.
function checkStatusline(pluginRoot) {
  const settingsPath = path.join(configDir(), 'settings.json');
  let settings;
  try {
    settings = JSON.parse(fs.readFileSync(settingsPath, 'utf8'));
  } catch (err) {
    return { ok: true, skipped: true, settingsPath };
  }
  const cmd =
    settings && settings.statusLine && typeof settings.statusLine.command === 'string'
      ? settings.statusLine.command
      : null;
  if (!cmd || !pluginRoot) return { ok: true, skipped: true, settingsPath };
  const missing = extractPluginPaths(cmd, pluginRoot).filter(p => !fs.existsSync(p));
  return { ok: missing.length === 0, missing, settingsPath };
}

// --- network (injectable) -----------------------------------------------

// rawRequest performs a single HTTPS GET and resolves the full response
// (status, headers, body) without following redirects.
function rawRequest(url, headers) {
  return new Promise((resolve, reject) => {
    const req = https.get(url, { headers }, res => {
      const chunks = [];
      res.on('data', c => chunks.push(c));
      res.on('end', () => resolve({ statusCode: res.statusCode, headers: res.headers, body: Buffer.concat(chunks) }));
      res.on('error', reject);
    });
    req.on('error', reject);
    req.setTimeout(30000, () => req.destroy(new Error(`timed out fetching ${url}`)));
  });
}

// followRedirects drives `requestFn` (rawRequest, or a test stub) across up
// to `hopsLeft` 3xx hops and returns the final 200 body. GitHub release
// asset URLs 302 to objects.githubusercontent.com and https.get does not
// auto-follow redirects, so this is required for downloads.
async function followRedirects(requestFn, url, headers, hopsLeft = 5) {
  const res = await requestFn(url, headers);
  if (res.statusCode >= 300 && res.statusCode < 400 && res.headers && res.headers.location) {
    if (hopsLeft <= 0) throw new Error(`too many redirects fetching ${url}`);
    if (!res.headers.location.startsWith('https://')) {
      throw new Error(`refusing non-https redirect target: ${res.headers.location}`);
    }
    return followRedirects(requestFn, res.headers.location, headers, hopsLeft - 1);
  }
  if (res.statusCode !== 200) {
    throw new Error(`HTTP ${res.statusCode} for ${url}`);
  }
  return res.body;
}

// httpGet is the default injectable network function: GET a URL (with
// redirects followed) and resolve its body as a Buffer.
function httpGet(url, headers) {
  return followRedirects(rawRequest, url, headers);
}

// --- release archive handling --------------------------------------------

// detectPlatform maps process.platform/process.arch onto goreleaser's
// os/arch naming. Returns null for anything unsupported.
function detectPlatform() {
  const osMap = { darwin: 'darwin', linux: 'linux' };
  const archMap = { x64: 'amd64', arm64: 'arm64' };
  const goos = osMap[process.platform];
  const goarch = archMap[process.arch];
  if (!goos || !goarch) return null;
  return { os: goos, arch: goarch };
}

// verifySha256 finds the SHA256SUMS line for `archiveName` and throws if the
// computed digest of `buf` doesn't match.
function verifySha256(buf, checksumText, archiveName) {
  const line = checksumText.split('\n').find(l => {
    const fields = l.trim().split(/\s+/);
    return fields[1] === archiveName;
  });
  if (!line) throw new Error(`no checksum entry for ${archiveName}`);
  const expected = line.trim().split(/\s+/)[0].toLowerCase();
  const actual = crypto.createHash('sha256').update(buf).digest('hex');
  if (expected !== actual) {
    throw new Error(`checksum mismatch for ${archiveName}: expected ${expected}, got ${actual}`);
  }
  return true;
}

// extractTarEntry does a minimal POSIX ustar read over `tarBuf`, returning
// the data of the first regular-file entry whose basename equals
// `entryName`, or null if not found. No third-party tar package.
function extractTarEntry(tarBuf, entryName) {
  let offset = 0;
  while (offset + 512 <= tarBuf.length) {
    const header = tarBuf.subarray(offset, offset + 512);
    if (header.every(b => b === 0)) break; // end-of-archive marker

    const name = header.subarray(0, 100).toString('utf8').replace(/\0.*$/, '');
    const sizeField = header.subarray(124, 136).toString('utf8').replace(/\0.*$/, '').trim();
    const size = sizeField ? parseInt(sizeField, 8) : 0;
    const typeflag = String.fromCharCode(header[156]);
    offset += 512;

    const dataStart = offset;
    const dataEnd = dataStart + size;
    const paddedEnd = dataStart + Math.ceil(size / 512) * 512;

    const isRegularFile = typeflag === '0' || typeflag === '\0';
    if (isRegularFile && path.basename(name) === entryName) {
      return tarBuf.subarray(dataStart, dataEnd);
    }
    offset = paddedEnd;
  }
  return null;
}

// --- install --------------------------------------------------------------

function installDevBinary(pluginData, devBinaryPath) {
  if (!isExecutable(devBinaryPath)) {
    log(`dev binary not found: ${devBinaryPath}`);
    return { ok: false, reason: `dev binary not found: ${devBinaryPath}` };
  }
  const binary = path.join(pluginData, 'bin', 'pogo');
  if (isExecutable(binary) && sigEqual(statSig(devBinaryPath), statSig(binary))) {
    return { ok: true, skipped: true };
  }
  atomicInstallBinary(binary, fs.readFileSync(devBinaryPath));
  fs.writeFileSync(path.join(pluginData, '.version'), 'dev');
  log(`installed dev binary from ${devBinaryPath}`);
  return { ok: true, version: 'dev' };
}

function findLocalBinary() {
  for (const candidate of ['/usr/local/bin/pogo', '/opt/homebrew/bin/pogo']) {
    if (isExecutable(candidate)) return candidate;
  }
  return null;
}

function installLocalBinary(pluginData, localBin) {
  let real;
  try {
    real = fs.realpathSync(localBin);
  } catch (err) {
    real = localBin;
  }
  const binary = path.join(pluginData, 'bin', 'pogo');
  if (isExecutable(binary) && sigEqual(statSig(real), statSig(binary))) {
    return { ok: true, skipped: true };
  }
  atomicInstallBinary(binary, fs.readFileSync(real));
  let version = 'local';
  try {
    const r = spawnSync(binary, ['--version'], { encoding: 'utf8', timeout: 5000 });
    if (r.status === 0 && r.stdout && r.stdout.trim()) version = r.stdout.trim();
  } catch (err) {
    // best-effort only
  }
  fs.writeFileSync(path.join(pluginData, '.version'), version);
  log(`installed ${version} from ${localBin}`);
  return { ok: true, version };
}

async function installFromRelease(pluginData, get) {
  const platform = detectPlatform();
  if (!platform) {
    const reason = `unsupported platform: ${process.platform}/${process.arch}`;
    log(reason);
    return { ok: false, unsupported: true, reason };
  }

  const binary = path.join(pluginData, 'bin', 'pogo');
  const versionFile = path.join(pluginData, '.version');
  const headers = { 'User-Agent': USER_AGENT };

  let tag;
  try {
    const body = await get(`https://api.github.com/repos/${REPO}/releases/latest`, headers);
    const parsed = JSON.parse(body.toString('utf8'));
    tag = parsed && parsed.tag_name;
    if (!tag) throw new Error('no tag_name in latest release response');
  } catch (err) {
    log(`failed to fetch latest release tag: ${err.message}`);
    if (isExecutable(binary)) {
      log('offline fallback: keeping existing binary');
      return { ok: true, offline: true };
    }
    return { ok: false, reason: err.message };
  }

  const version = tag.replace(/^v/, '');
  let installed = '';
  try {
    installed = fs.readFileSync(versionFile, 'utf8').trim();
  } catch (err) {
    // no .version yet
  }
  if (installed === version && isExecutable(binary)) {
    return { ok: true, skipped: true, version };
  }

  log(`installing ${version} (${platform.os}/${platform.arch})...`);
  const archiveName = `pogopin_${version}_${platform.os}_${platform.arch}.tar.gz`;
  const checksumName = `pogopin_${version}_SHA256SUMS`;
  const base = `https://github.com/${REPO}/releases/download/${tag}`;

  let archiveBuf;
  let checksumBuf;
  try {
    archiveBuf = await get(`${base}/${archiveName}`, headers);
    checksumBuf = await get(`${base}/${checksumName}`, headers);
  } catch (err) {
    log(`download failed: ${err.message}`);
    if (isExecutable(binary)) {
      log('offline fallback: keeping existing binary');
      return { ok: true, offline: true };
    }
    return { ok: false, reason: err.message };
  }

  try {
    verifySha256(archiveBuf, checksumBuf.toString('utf8'), archiveName);
  } catch (err) {
    log(`checksum verification failed: ${err.message}`);
    return { ok: false, reason: err.message };
  }

  let entry;
  try {
    const tarBuf = zlib.gunzipSync(archiveBuf);
    entry = extractTarEntry(tarBuf, 'pogo');
    if (!entry) throw new Error(`'pogo' entry not found in ${archiveName}`);
  } catch (err) {
    log(`extraction failed: ${err.message}`);
    return { ok: false, reason: err.message };
  }

  atomicInstallBinary(binary, entry);
  fs.writeFileSync(versionFile, version);
  log(`installed ${version}`);
  return { ok: true, version };
}

// install is the single installer: dev binary > local Homebrew binary >
// GitHub release, in that precedence, matching install.sh's old order.
async function install(env, opts) {
  env = env || process.env;
  opts = opts || {};
  const get = opts.get || httpGet;
  const pluginData = env.CLAUDE_PLUGIN_DATA;
  if (!pluginData) {
    log('cannot install: CLAUDE_PLUGIN_DATA not set');
    return { ok: false, reason: 'CLAUDE_PLUGIN_DATA not set' };
  }
  fs.mkdirSync(path.join(pluginData, 'bin'), { recursive: true });

  if (env.POGOPIN_DEV_BINARY) {
    return installDevBinary(pluginData, env.POGOPIN_DEV_BINARY);
  }

  const localBin = findLocalBinary();
  if (localBin) {
    return installLocalBinary(pluginData, localBin);
  }

  return installFromRelease(pluginData, get);
}

// --- orchestration ---------------------------------------------------------

// selfHeal runs validation and, if the binary is missing/broken, installs
// it. A missing hook script is logged only — install() can't repair a
// corrupt plugin directory. Never throws; returns a summary for tests.
async function selfHeal(env, opts) {
  env = env || process.env;

  const pluginRoot = env.CLAUDE_PLUGIN_ROOT;
  const pluginData = env.CLAUDE_PLUGIN_DATA;

  const binary = checkBinary(pluginData);
  let repaired = false;
  let installResult = null;
  if (!binary.ok) {
    log(binary.reason);
    installResult = await install(env, opts);
    repaired = true;
  }

  const scripts = checkHookScripts(pluginRoot);
  if (!scripts.ok) {
    if (scripts.reason) {
      log(scripts.reason);
    } else {
      log(
        `corrupt plugin install: missing hook script(s): ${scripts.missing.join(', ')} — ` +
          'reinstall/update the plugin'
      );
    }
  }

  const statusline = checkStatusline(pluginRoot);
  if (!statusline.skipped && !statusline.ok) {
    log(`configured statusLine command references missing script(s): ${statusline.missing.join(', ')}`);
  }

  return { binary, scripts, statusline, repaired, installResult };
}

if (require.main === module) {
  selfHeal(process.env)
    .catch(err => log(`unexpected error: ${err && err.message}`))
    .then(() => process.exit(0));
}

module.exports = {
  configDir,
  isExecutable,
  statSig,
  atomicInstallBinary,
  checkBinary,
  collectHookCommands,
  extractPluginPaths,
  checkHookScripts,
  checkStatusline,
  rawRequest,
  followRedirects,
  httpGet,
  detectPlatform,
  verifySha256,
  extractTarEntry,
  installDevBinary,
  findLocalBinary,
  installLocalBinary,
  installFromRelease,
  install,
  selfHeal,
  REPO,
};
