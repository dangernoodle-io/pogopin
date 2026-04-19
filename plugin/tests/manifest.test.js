const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('fs');
const path = require('path');

const pluginJsonPath = path.join(__dirname, '..', '.claude-plugin', 'plugin.json');
const hooksJsonPath = path.join(__dirname, '..', 'hooks', 'hooks.json');

test('plugin.json parses as JSON', () => {
  const content = fs.readFileSync(pluginJsonPath, 'utf8');
  assert.doesNotThrow(() => JSON.parse(content));
});

test('plugin.json has name === "pogopin-mcp"', () => {
  const content = fs.readFileSync(pluginJsonPath, 'utf8');
  const parsed = JSON.parse(content);
  assert.equal(parsed.name, 'pogopin-mcp');
});

test('plugin.json mcpServers.pogopin.command ends with /bin/pogo', () => {
  const content = fs.readFileSync(pluginJsonPath, 'utf8');
  const parsed = JSON.parse(content);
  const command = parsed.mcpServers.pogopin.command;
  assert.ok(command.endsWith('/bin/pogo'), `expected command to end with /bin/pogo, got ${command}`);
});

test('plugin.json mcpServers.pogopin.args deep-equals ["server"]', () => {
  const content = fs.readFileSync(pluginJsonPath, 'utf8');
  const parsed = JSON.parse(content);
  const args = parsed.mcpServers.pogopin.args;
  assert.deepEqual(args, ['server']);
});

test('plugin.json version key is absent', () => {
  const content = fs.readFileSync(pluginJsonPath, 'utf8');
  const parsed = JSON.parse(content);
  assert.equal(parsed.version, undefined);
});

test('hooks.json parses as JSON', () => {
  const content = fs.readFileSync(hooksJsonPath, 'utf8');
  assert.doesNotThrow(() => JSON.parse(content));
});

test('hooks.json has top-level hooks key', () => {
  const content = fs.readFileSync(hooksJsonPath, 'utf8');
  const parsed = JSON.parse(content);
  assert.ok('hooks' in parsed);
});

test('hooks.json SessionStart[0].hooks[0].type === "command"', () => {
  const content = fs.readFileSync(hooksJsonPath, 'utf8');
  const parsed = JSON.parse(content);
  const type = parsed.hooks.SessionStart[0].hooks[0].type;
  assert.equal(type, 'command');
});

test('hooks.json SessionStart[0].hooks[0].command contains install.sh', () => {
  const content = fs.readFileSync(hooksJsonPath, 'utf8');
  const parsed = JSON.parse(content);
  const command = parsed.hooks.SessionStart[0].hooks[0].command;
  assert.ok(command.includes('install.sh'), `expected command to contain install.sh, got ${command}`);
});

test('hooks.json UserPromptSubmit[0].hooks[0].command contains context.sh', () => {
  const content = fs.readFileSync(hooksJsonPath, 'utf8');
  const parsed = JSON.parse(content);
  const command = parsed.hooks.UserPromptSubmit[0].hooks[0].command;
  assert.ok(command.includes('context.sh'), `expected command to contain context.sh, got ${command}`);
});
