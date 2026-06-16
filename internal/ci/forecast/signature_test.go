package forecast

import (
	"reflect"
	"testing"
)

func TestTags(t *testing.T) {
	tests := []struct {
		name           string
		projectPath    string
		filesInProject []string
		want           []string
	}{
		{
			name:           "transitive: no files changed inside project",
			projectPath:    "services/api",
			filesInProject: nil,
			want:           []string{"transitive"},
		},
		{
			name:           "transitive: empty slice",
			projectPath:    "services/api",
			filesInProject: []string{},
			want:           []string{"transitive"},
		},
		{
			name:        "direct: file at project root (no subdir)",
			projectPath: "services/api",
			filesInProject: []string{
				"services/api/magusfile",
			},
			want: []string{"direct"},
		},
		{
			name:        "direct with single subdir",
			projectPath: "services/api",
			filesInProject: []string{
				"services/api/src/handler.go",
				"services/api/src/handler_test.go",
			},
			want: []string{"direct", "direct.src"},
		},
		{
			name:        "direct with multiple subdirs, sorted",
			projectPath: "services/api",
			filesInProject: []string{
				"services/api/src/handler.go",
				"services/api/tests/handler_test.go",
				"services/api/docs/openapi.yaml",
			},
			want: []string{"direct", "direct.docs", "direct.src", "direct.tests"},
		},
		{
			name:        "deep nested paths: only first subdir component used",
			projectPath: "libs/shared",
			filesInProject: []string{
				"libs/shared/src/utils/string.go",
				"libs/shared/src/utils/deep/nest.go",
			},
			want: []string{"direct", "direct.src"},
		},
		{
			name:        "project path with trailing slash normalised",
			projectPath: "libs/shared/",
			filesInProject: []string{
				"libs/shared/src/foo.go",
			},
			want: []string{"direct", "direct.src"},
		},
		{
			name:        "mix of root files and subdir files",
			projectPath: "cmd/tool",
			filesInProject: []string{
				"cmd/tool/main.go",
				"cmd/tool/internal/runner.go",
			},
			want: []string{"direct", "direct.internal"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Tags(tt.projectPath, tt.filesInProject)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Tags(%q, %v) = %v, want %v", tt.projectPath, tt.filesInProject, got, tt.want)
			}
		})
	}
}
