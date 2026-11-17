package describe

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/types"
)

func TestListTargetsPrintsOnlyPaths(t *testing.T) {
	out := testkit.CaptureStdout(t, func() {
		ListTargets(t.Context(), "affected", []types.Target{{Path: "web"}, {Path: "api"}}, "vcs")
	})
	assert.Equal(t, "web\napi\n", out, "the summary is a log line; stdout carries only the paths")

	assert.Empty(t, testkit.CaptureStdout(t, func() { ListTargets(t.Context(), "all", nil, "") }))

	out = testkit.CaptureStdout(t, func() {
		ListTargets(t.Context(), "cwd", []types.Target{{Path: "web"}}, "")
	})
	assert.Equal(t, "web\n", out)
}
