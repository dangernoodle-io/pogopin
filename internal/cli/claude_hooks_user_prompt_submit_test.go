package cli

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/dangernoodle-io/shesha/host/claudecode/hooks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHookHandleUserPromptSubmit(t *testing.T) {
	tests := []struct {
		name     string
		setup    func(t *testing.T) string // returns cwd
		wantText string
	}{
		{
			name: "sdkconfig present",
			setup: func(t *testing.T) string {
				dir := t.TempDir()
				require.NoError(t, os.WriteFile(filepath.Join(dir, "sdkconfig"), []byte("CONFIG_X=y\n"), 0644))
				return dir
			},
			wantText: espIDFContextMessage,
		},
		{
			name: "CMakeLists.txt with idf_component_register",
			setup: func(t *testing.T) string {
				dir := t.TempDir()
				require.NoError(t, os.WriteFile(filepath.Join(dir, "CMakeLists.txt"), []byte("idf_component_register(SRCS \"main.c\")\n"), 0644))
				return dir
			},
			wantText: espIDFContextMessage,
		},
		{
			name: "CMakeLists.txt without idf_component_register",
			setup: func(t *testing.T) string {
				dir := t.TempDir()
				require.NoError(t, os.WriteFile(filepath.Join(dir, "CMakeLists.txt"), []byte("project(hello)\n"), 0644))
				return dir
			},
			wantText: "",
		},
		{
			name: "neither file present",
			setup: func(t *testing.T) string {
				return t.TempDir()
			},
			wantText: "",
		},
		{
			name: "unreadable cwd fails open",
			setup: func(t *testing.T) string {
				return filepath.Join(t.TempDir(), "does-not-exist")
			},
			wantText: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cwd := tt.setup(t)
			p := hooks.UserPromptSubmitPayload{Common: hooks.Common{Cwd: cwd}, Prompt: "flash the board"}
			resp := hookHandleUserPromptSubmit(context.Background(), nil, p)
			assert.Equal(t, tt.wantText, resp.PlainText)
		})
	}
}

func TestIsESPIDFProject_EmptyCwd(t *testing.T) {
	assert.False(t, isESPIDFProject(""))
}
