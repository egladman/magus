package job

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/egladman/magus/types"
)

func TestResolveBriefFormatPrefersTheRowThenMagusYaml(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name      string
		row       types.Job
		workspace types.BriefFormat
		want      types.BriefFormat
		source    string
	}{
		{"the row wins", types.Job{ID: "api", BriefFormat: types.BriefFormatBuzz}, types.BriefFormatMarkdown, types.BriefFormatBuzz, "from row api"},
		{"magus.yaml when the row names none", types.Job{ID: "api"}, types.BriefFormatBuzz, types.BriefFormatBuzz, "from magus.yaml"},
		{"markdown when neither does", types.Job{ID: "api"}, "", types.BriefFormatMarkdown, "agents.brief_format unset"},
		{"no row", types.Job{}, types.BriefFormatMarkdown, types.BriefFormatMarkdown, "from magus.yaml"},
	} {
		got, source := ResolveBriefFormat(tt.row, tt.workspace)
		assert.Equal(t, tt.want, got, tt.name)
		assert.Equal(t, tt.source, source, tt.name)
	}
}

// An apply that changes a live row's brief_format is a spec change, so the generation
// records which format a trial ran under.
func TestBriefFormatChangeIsASpecChange(t *testing.T) {
	t.Parallel()
	prev := types.Job{ID: "api", Generation: 2}
	next := prev
	next.BriefFormat = types.BriefFormatBuzz
	assert.Equal(t, []string{"brief_format"}, specChanges(prev, next))
}
