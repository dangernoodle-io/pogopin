const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('fs');
const path = require('path');

const agentPath = path.join(__dirname, '..', 'agents', 'board-medic.md');
const skillPath = path.join(__dirname, '..', 'skills', 'board-medic', 'SKILL.md');
const espToolsPath = path.join(__dirname, '..', '..', 'internal', 'mcpserver', 'esp_tools.go');
const serialToolsPath = path.join(__dirname, '..', '..', 'internal', 'mcpserver', 'serial_tools.go');
const decodeToolsPath = path.join(__dirname, '..', '..', 'internal', 'mcpserver', 'decode_tools.go');

function parseFrontmatter(md) {
  const match = md.match(/^---\n([\s\S]*?)\n---/);
  if (!match) return null;
  const fields = {};
  for (const line of match[1].split('\n')) {
    const i = line.indexOf(':');
    if (i < 0) continue;
    fields[line.slice(0, i).trim()] = line.slice(i + 1).trim();
  }
  return fields;
}

test('board-medic agent has valid frontmatter', () => {
  const md = fs.readFileSync(agentPath, 'utf8');
  const fm = parseFrontmatter(md);
  assert.ok(fm, 'frontmatter missing');
  assert.equal(fm.name, 'board-medic');
  assert.ok(fm.description && fm.description.length > 0, 'description empty');
  assert.equal(fm.model, 'sonnet');
  assert.ok(fm.tools && fm.tools.startsWith('['), 'tools must be a JSON array');
  const tools = JSON.parse(fm.tools);
  assert.ok(Array.isArray(tools) && tools.length > 0, 'tools list empty');
});

test('board-medic agent allowlist references only registered pogopin tools', () => {
  const md = fs.readFileSync(agentPath, 'utf8');
  const fm = parseFrontmatter(md);
  const tools = JSON.parse(fm.tools);
  const pogoPrefix = 'mcp__plugin_pogopin-mcp_pogopin__';
  const referenced = tools.filter(t => t.startsWith(pogoPrefix)).map(t => t.slice(pogoPrefix.length));

  const sources = [espToolsPath, serialToolsPath, decodeToolsPath]
    .map(p => fs.readFileSync(p, 'utf8'))
    .join('\n');

  for (const name of referenced) {
    assert.ok(
      sources.includes(`mcp.NewTool("${name}"`),
      `agent references tool "${name}" but no mcp.NewTool("${name}",...) found in *_tools.go`
    );
  }
});

test('board-medic agent does not grant destructive tools', () => {
  const md = fs.readFileSync(agentPath, 'utf8');
  const fm = parseFrontmatter(md);
  const tools = JSON.parse(fm.tools);
  const forbidden = ['esp_erase', 'esp_flash', 'esp_write_nvs', 'esp_nvs_set', 'esp_nvs_delete', 'flash_external', 'esp_reset'];
  for (const bad of forbidden) {
    const fqn = `mcp__plugin_pogopin-mcp_pogopin__${bad}`;
    assert.ok(!tools.includes(fqn), `destructive tool ${bad} must not be on the allowlist`);
  }
});

test('board-medic skill has valid frontmatter', () => {
  const md = fs.readFileSync(skillPath, 'utf8');
  const fm = parseFrontmatter(md);
  assert.ok(fm, 'frontmatter missing');
  assert.equal(fm.name, 'board-medic');
  assert.ok(fm.description && fm.description.length > 0, 'description empty');
});
