package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var agentBuiltinTools = map[string]bool{
	"Read": true, "Grep": true, "Glob": true, "Bash": true,
	"Edit": true, "Write": true, "WebFetch": true, "WebSearch": true,
	"Task": true, "TodoWrite": true,
}

var agentValidModels = map[string]bool{
	"opus": true, "sonnet": true, "haiku": true,
}

// leakPatterns are banned in firmware-*.md agent definitions.
var leakPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)bb_`),
	regexp.MustCompile(`(?i)config_bb`),
	regexp.MustCompile(`(?i)taipan`),
	regexp.MustCompile(`(?i)breadboard`),
	regexp.MustCompile(`(?i)\b(mining|asic)\b`),
}

// agentNamePattern matches agent-name-shaped tokens (board-* / firmware-*)
// anywhere in an agent .md file, so stale/renamed delegation targets can be
// caught by cross-referencing against the real agent name set.
var agentNamePattern = regexp.MustCompile(`\b(?:board|firmware)-[a-z]+\b`)

// agentNameFalsePositives are tokens that match agentNamePattern's shape but
// are not agent-name references — exclude them explicitly, with reason.
var agentNameFalsePositives = map[string]bool{
	// board-operator.md: "Download-mode entry is board-dependent" — an
	// adjective, not a delegation target.
	"board-dependent": true,
}

// pogopinToolShortNames are the exact short names of tools this plugin
// exposes (the substring after the last "__" in the mcp__... tool id).
var pogopinToolShortNames = map[string]bool{
	"serial_list": true, "serial_start": true, "serial_read": true,
	"serial_write": true, "serial_stop": true, "serial_status": true,
	"serial_restart": true,
	"esp_flash":      true, "esp_erase": true, "esp_info": true,
	"esp_register": true, "esp_reset": true, "esp_read_flash": true,
	"esp_read_nvs": true, "esp_write_nvs": true, "esp_nvs_set": true,
	"esp_nvs_delete": true, "flash_external": true, "decode_backtrace": true,
	"esp_gpio_read": true, "esp_gpio_set": true, "esp_gpio_sweep": true,
}

// toolHonestyAllowlist documents agents whose prose backtick-references a
// pogopin tool short-name they don't themselves hold, because the prose is
// describing delegation to another agent rather than a direct call. Empty
// today — kept so a future finding has a documented place to land instead of
// prompting a tools: grant just to satisfy the test.
var toolHonestyAllowlist = map[string]map[string]bool{}

func agentShortName(tool string) string {
	if idx := strings.LastIndex(tool, "__"); idx >= 0 {
		return tool[idx+2:]
	}
	return tool
}

func TestPluginAgentDefinitions(t *testing.T) {
	files, err := filepath.Glob("plugin/agents/*.md")
	require.NoError(t, err, "glob plugin/agents/*.md")
	require.NotEmpty(t, files, "expected at least one agent .md file in plugin/agents/")

	for _, path := range files {
		base := filepath.Base(path)
		agentName := strings.TrimSuffix(base, ".md")

		t.Run(agentName, func(t *testing.T) {
			raw, readErr := os.ReadFile(path)
			require.NoError(t, readErr, "read %s", path)
			s := string(raw)

			// a. Frontmatter: starts with ---\n and has a closing --- line.
			require.True(t, strings.HasPrefix(s, "---\n"), "file must start with ---\\n")
			rest := s[4:] // content after opening ---\n
			closingIdx := strings.Index(rest, "\n---")
			require.Greater(t, closingIdx, 0, "frontmatter must have a closing --- line with content before it")
			frontmatter := rest[:closingIdx]

			// b. Required keys present in frontmatter.
			for _, key := range []string{"name:", "description:", "tools:", "model:"} {
				assert.True(t, strings.Contains(frontmatter, key),
					"frontmatter must contain key %q", key)
			}

			// c. model: value must be opus, sonnet, or haiku.
			modelRe := regexp.MustCompile(`(?m)^model:\s*(\S+)`)
			modelMatch := modelRe.FindStringSubmatch(frontmatter)
			require.NotNil(t, modelMatch, "model: key must have a value")
			assert.True(t, agentValidModels[modelMatch[1]],
				"model %q not in {opus, sonnet, haiku}", modelMatch[1])

			// d. name: value must equal filename without .md.
			nameRe := regexp.MustCompile(`(?m)^name:\s*(\S+)`)
			nameMatch := nameRe.FindStringSubmatch(frontmatter)
			require.NotNil(t, nameMatch, "name: key must have a value")
			assert.Equal(t, agentName, nameMatch[1],
				"name value must match filename (without .md)")

			// e. tools: must parse as a non-empty JSON array of strings;
			// each entry must be a builtin tool or start with mcp__.
			bracketStart := strings.Index(s, "[")
			require.Greater(t, bracketStart, 0, "tools: value must contain [")
			depth := 0
			bracketEnd := -1
			for i := bracketStart; i < len(s); i++ {
				switch s[i] {
				case '[':
					depth++
				case ']':
					depth--
					if depth == 0 {
						bracketEnd = i
					}
				}
				if bracketEnd >= 0 {
					break
				}
			}
			require.Greater(t, bracketEnd, bracketStart, "tools: array must have matching ]")
			toolsJSON := s[bracketStart : bracketEnd+1]
			var tools []string
			require.NoError(t, json.Unmarshal([]byte(toolsJSON), &tools),
				"tools: must parse as JSON array of strings")
			assert.NotEmpty(t, tools, "tools: array must be non-empty")
			for _, tool := range tools {
				if !agentBuiltinTools[tool] {
					assert.True(t, strings.HasPrefix(tool, "mcp__"),
						"tool %q must be a builtin or start with mcp__", tool)
				}
			}

			// f. Generic-leak guard: firmware-*.md must not reference project-specific tokens.
			if strings.HasPrefix(agentName, "firmware-") {
				for _, re := range leakPatterns {
					assert.False(t, re.MatchString(s),
						"firmware agent %s must not contain banned token matching %s",
						agentName, re.String())
				}
			}
		})
	}
}

// TestPluginAgentCrossReferences catches stale/renamed delegation targets:
// every board-*/firmware-* token mentioned in an agent file must name a real
// agent (a plugin/agents/*.md basename).
func TestPluginAgentCrossReferences(t *testing.T) {
	files, err := filepath.Glob("plugin/agents/*.md")
	require.NoError(t, err, "glob plugin/agents/*.md")
	require.NotEmpty(t, files, "expected at least one agent .md file in plugin/agents/")

	validNames := make(map[string]bool, len(files))
	for _, path := range files {
		validNames[strings.TrimSuffix(filepath.Base(path), ".md")] = true
	}

	for _, path := range files {
		base := filepath.Base(path)
		agentName := strings.TrimSuffix(base, ".md")

		t.Run(agentName, func(t *testing.T) {
			raw, readErr := os.ReadFile(path)
			require.NoError(t, readErr, "read %s", path)
			s := string(raw)

			for _, ref := range agentNamePattern.FindAllString(s, -1) {
				if agentNameFalsePositives[ref] {
					continue
				}
				t.Logf("%s references %q", base, ref)
				assert.True(t, validNames[ref],
					"%s references %q, which is not a real agent name (renamed/stale delegation target?)",
					base, ref)
			}
		})
	}
}

// TestPluginAgentToolHonesty catches prose that backtick-references a
// pogopin tool short-name an agent doesn't itself hold in its tools:
// frontmatter — a sign the tool was removed/renamed but the prose wasn't
// updated, or the prose implies a direct call the agent can't make.
func TestPluginAgentToolHonesty(t *testing.T) {
	files, err := filepath.Glob("plugin/agents/*.md")
	require.NoError(t, err, "glob plugin/agents/*.md")
	require.NotEmpty(t, files, "expected at least one agent .md file in plugin/agents/")

	backtickRe := regexp.MustCompile("`([a-zA-Z0-9_]+)`")

	for _, path := range files {
		base := filepath.Base(path)
		agentName := strings.TrimSuffix(base, ".md")

		t.Run(agentName, func(t *testing.T) {
			raw, readErr := os.ReadFile(path)
			require.NoError(t, readErr, "read %s", path)
			s := string(raw)

			// Re-locate frontmatter and the tools: array (mirrors
			// TestPluginAgentDefinitions' parsing).
			require.True(t, strings.HasPrefix(s, "---\n"), "file must start with ---\\n")
			rest := s[4:]
			closingIdx := strings.Index(rest, "\n---")
			require.Greater(t, closingIdx, 0, "frontmatter must have a closing --- line")

			bracketStart := strings.Index(s, "[")
			require.Greater(t, bracketStart, 0, "tools: value must contain [")
			depth := 0
			bracketEnd := -1
			for i := bracketStart; i < len(s); i++ {
				switch s[i] {
				case '[':
					depth++
				case ']':
					depth--
					if depth == 0 {
						bracketEnd = i
					}
				}
				if bracketEnd >= 0 {
					break
				}
			}
			require.Greater(t, bracketEnd, bracketStart, "tools: array must have matching ]")
			var tools []string
			require.NoError(t, json.Unmarshal([]byte(s[bracketStart:bracketEnd+1]), &tools))

			granted := make(map[string]bool, len(tools))
			for _, tool := range tools {
				granted[agentShortName(tool)] = true
			}

			// Body = everything after the closing frontmatter delimiter.
			bodyStart := 4 + closingIdx + len("\n---")
			body := s[bodyStart:]

			allowed := toolHonestyAllowlist[agentName]

			for _, m := range backtickRe.FindAllStringSubmatch(body, -1) {
				token := m[1]
				if !pogopinToolShortNames[token] {
					continue
				}
				if granted[token] || allowed[token] {
					continue
				}
				assert.Fail(t, "tool-honesty violation",
					"%s prose references `%s` but does not hold that tool (granted: %v)",
					base, token, tools)
			}
		})
	}
}
