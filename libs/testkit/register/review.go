package register

import (
	"fmt"
	"strings"

	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
)

// ReviewSpell registers, through [Spell], a Buzz review provider whose find_review answers
// at and whose review_threads answers threads, and returns its name for
// bindings.SetReviewProvider. reply_review and publish_review accept everything.
//
// It answers the way a real provider does, as typed Buzz the provider ops decode strictly,
// so a test using it covers that decode rather than standing in for it.
func ReviewSpell(tb testkit.TB, at types.ReviewTarget, threads []types.ReviewComment) string {
	tb.Helper()
	var src strings.Builder
	src.WriteString("import \"magus/spell\";\n\n")
	fmt.Fprintf(&src, "export fun find_review(target: Target, req: FindReviewRequest) > ReviewTarget {\n    return ReviewTarget{ id = %s, repo = %s, reason = %s, state = %s, author = %s, viewer = %s };\n}\n\n",
		buzzString(at.ID), buzzString(at.Repo), buzzString(at.Reason), buzzString(at.State), buzzString(at.Author), buzzString(at.Viewer))
	src.WriteString("export fun review_threads(target: Target, req: ReviewRef) > [ReviewComment] {\n    final threads = mut [<ReviewComment>];\n")
	for _, c := range threads {
		fmt.Fprintf(&src, "    threads.append(ReviewComment{ id = %s, path = %s, line = %d, author = %s, body = %s, root = %s, outdated = %t, diffHunk = %s });\n",
			buzzString(c.ID), buzzString(c.Path), c.Line, buzzString(c.Author), buzzString(c.Body), buzzString(c.Root), c.Outdated, buzzString(c.DiffHunk))
	}
	src.WriteString("    return threads;\n}\n\n")
	src.WriteString("export fun reply_review(target: Target, req: ReplyReviewRequest) > Ack { return Ack{ ok = true }; }\n\n")
	src.WriteString("export fun publish_review(target: Target, req: PublishReviewRequest) > Ack { return Ack{ ok = true }; }\n")
	return Spell(tb, "review", spells.WithFuncSource(src.String()))
}

// buzzString renders s as a Buzz string literal. A brace is escaped too, since an unescaped
// one would open an interpolation.
func buzzString(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`, "{", `\{`).Replace(s) + `"`
}
