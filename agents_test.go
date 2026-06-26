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
