package progress

import (
	"strings"

	"github.com/egladman/magus/libs/termkit"
)

// Bar draws done of total in cols cells, filled in eighths so it moves a
// fraction of a cell at a time. The filled part is unstyled and the rest of
// the track takes the track style. A total of zero or less draws nothing, and
// done is clamped to [0, total].
func Bar(done, total, cols int, track termkit.SGR) termkit.Line {
	if total <= 0 || cols <= 0 {
		return termkit.Line{}
	}
	parts := []rune(eighths)
	filled := min(max(done, 0), total) * cols * 8 / total
	full := filled / 8
	bar := strings.Repeat("█", full)
	used := full
	if part := filled % 8; part > 0 {
		bar += string(parts[part])
		used++
	}
	var spans []termkit.Span
	if bar != "" {
		spans = append(spans, termkit.Span{Text: bar})
	}
	if used < cols {
		spans = append(spans, termkit.Span{Text: strings.Repeat("░", cols-used), Style: track})
	}
	return termkit.Line{Spans: spans}
}
