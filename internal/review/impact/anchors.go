package impact

import (
	"fmt"

	"github.com/egladman/magus/internal/review"
	"github.com/egladman/magus/libs/text/plural"
)

func anchorLines(hits []review.AnchorHit) []string {
	if len(hits) == 0 {
		return []string{"ANCHORS: no note anchors a changed file or symbol"}
	}
	out := []string{fmt.Sprintf("ANCHORS: %d note%s anchored to what you changed",
		len(hits), plural.Word(len(hits), "", "s"))}
	for _, h := range capList(hits) {
		out = append(out, "      "+h.Line())
	}
	return append(out, moreLine(len(hits))...)
}
