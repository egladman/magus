package job

import "github.com/egladman/magus/types"

// ResolveBriefFormat is the format a brief to row's worker takes, and where it came from,
// as a verdict names it. The first that answers decides: the row's own brief_format, then
// the workspace's agents.brief_format, then markdown. A zero row is a brief bound to no
// row, which only the workspace and the default can decide.
func ResolveBriefFormat(row types.Job, workspace types.BriefFormat) (types.BriefFormat, string) {
	switch {
	case row.BriefFormat != "":
		return row.BriefFormat, "from row " + row.ID
	case workspace != "":
		return workspace, "from magus.yaml"
	default:
		return types.BriefFormatMarkdown, BriefFormatUnset
	}
}

// BriefFormatUnset is the source of a format neither the row nor magus.yaml chose.
const BriefFormatUnset = "agents.brief_format unset"
