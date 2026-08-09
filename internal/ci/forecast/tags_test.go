package forecast

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestTags(t *testing.T) {
	t.Run("transitive: no files changed inside project", func(t *testing.T) {
		got := Tags("services/api", nil)
		assert.Equal(t, []string{"transitive"}, got)
	})

	t.Run("transitive: empty slice", func(t *testing.T) {
		got := Tags("services/api", []string{})
		assert.Equal(t, []string{"transitive"}, got)
	})

	t.Run("direct: file at project root (no subdir)", func(t *testing.T) {
		got := Tags("services/api", []string{
			"services/api/magusfile",
		})
		assert.Equal(t, []string{"direct"}, got)
	})

	t.Run("direct with single subdir", func(t *testing.T) {
		got := Tags("services/api", []string{
			"services/api/src/handler.go",
			"services/api/src/handler_test.go",
		})
		assert.Equal(t, []string{"direct", "direct.src"}, got)
	})

	t.Run("direct with multiple subdirs, sorted", func(t *testing.T) {
		got := Tags("services/api", []string{
			"services/api/src/handler.go",
			"services/api/tests/handler_test.go",
			"services/api/docs/openapi.yaml",
		})
		assert.Equal(t, []string{"direct", "direct.docs", "direct.src", "direct.tests"}, got)
	})

	t.Run("deep nested paths: only first subdir component used", func(t *testing.T) {
		got := Tags("libs/shared", []string{
			"libs/shared/src/utils/string.go",
			"libs/shared/src/utils/deep/nest.go",
		})
		assert.Equal(t, []string{"direct", "direct.src"}, got)
	})

	t.Run("project path with trailing slash normalised", func(t *testing.T) {
		got := Tags("libs/shared/", []string{
			"libs/shared/src/foo.go",
		})
		assert.Equal(t, []string{"direct", "direct.src"}, got)
	})

	t.Run("mix of root files and subdir files", func(t *testing.T) {
		got := Tags("cmd/tool", []string{
			"cmd/tool/main.go",
			"cmd/tool/internal/runner.go",
		})
		assert.Equal(t, []string{"direct", "direct.internal"}, got)
	})
}
