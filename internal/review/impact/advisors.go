package impact

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/egladman/magus/libs/text/plural"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/vcs"
)

// AdvisorBase is the revision the advisors compared against, and how current this
// clone's copy of it is.
//
// It is stated ONCE for the whole set rather than per section: a local run never fetches, so
// the caveat is identical under every advisor, and repeating it ten times is how a reader
// learns to skip it.
type AdvisorBase struct {
	// Ref is the revision as the advisors spell it, so a reader can run the same comparison
	// by hand.
	Ref string `json:"ref" yaml:"ref"`
	// Tip is when Ref's commit was authored, RFC3339, and empty when Ref names nothing in
	// this clone. The AGE a reader sees is derived from this at render rather than stored:
	// a report teed to a file and read tomorrow must not still claim today's number.
	Tip string `json:"tip,omitempty" yaml:"tip,omitempty"`
}

// AdviceSection is one advisor's finding: the section it owns in the pull-request
// comment, rendered for a local reader instead. An EMPTY Body is a retraction (the
// advisor ran and found nothing) and is a section like any other, not an absence.
type AdviceSection struct {
	Name  string `json:"name"  yaml:"name"`
	Title string `json:"title" yaml:"title"`
	Body  string `json:"body"  yaml:"body"`
}

// resolveAdvisorBase dates this clone's copy of the ref the advisors compared against.
//
// nil whenever the answer would be a guess: no VCS, or a backend that cannot date a
// revision. A ref the clone does not HAVE is not that case; it resolves to a
// AdvisorBase with no Tip, because "you have never fetched this" is the single most
// useful thing the report can say about why nine advisors went quiet.
func resolveAdvisorBase(ctx context.Context, root string, opts types.VCSOptions, base string) *AdvisorBase {
	res, err := vcs.Resolve(ctx, root, "", opts)
	if err != nil || res.VCS == nil {
		return nil
	}
	// "origin/" + base is the spelling the advisors themselves use, not a normalization of
	// it: the line exists so a reader can run the same comparison by hand, and a ref that
	// reads differently here than in advice.buzz would send them at the wrong one.
	ref := "origin/" + base
	tip, found, err := res.VCS.RevTime(ctx, root, ref)
	if err != nil {
		return nil
	}
	if !found {
		return &AdvisorBase{Ref: ref}
	}
	return &AdvisorBase{Ref: ref, Tip: tip.Format(time.RFC3339)}
}

// advisorBaseLine states what the advisors measured against, and how old it is.
//
// The age is computed here rather than carried, so it describes when the report is READ.
// An unparsable Tip degrades to naming the ref: the ref alone is still true, and a
// malformed date is not worth losing the rest of the line over.
func advisorBaseLine(b *AdvisorBase) string {
	if b == nil {
		return ""
	}
	refresh := fmt.Sprintf("`git fetch origin %s` brings it forward", strings.TrimPrefix(b.Ref, "origin/"))
	if b.Tip == "" {
		return fmt.Sprintf("BASE: %s is not in this clone, so the advisors below had nothing to "+
			"compare against; %s", b.Ref, refresh)
	}
	tip, err := time.Parse(time.RFC3339, b.Tip)
	if err != nil {
		return fmt.Sprintf("BASE: %s, as this clone has it - a local run stays off the network; %s", b.Ref, refresh)
	}
	return fmt.Sprintf("BASE: %s, tip %s old - a local run stays off the network, so anything "+
		"merged since is outside what the advisors saw; %s",
		b.Ref, ageText(time.Since(tip)), refresh)
}

// ageText renders a duration at one unit of precision. A reader deciding whether to
// fetch needs the order of magnitude ("3 days"), and "3 days 4 hours 11 minutes" buries it.
func ageText(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "seconds"
	case d < time.Hour:
		return fmt.Sprintf("%d minute%s", int(d.Minutes()), plural.Word(int(d.Minutes()), "", "s"))
	case d < 24*time.Hour:
		return fmt.Sprintf("%d hour%s", int(d.Hours()), plural.Word(int(d.Hours()), "", "s"))
	default:
		days := int(d.Hours() / 24)
		return fmt.Sprintf("%d day%s", days, plural.Word(days, "", "s"))
	}
}

func advisorLines(sections []AdviceSection, failed []string, base *AdvisorBase) []string {
	// Prepended to whichever headline follows, including "nothing to report": a clean set
	// measured against a ref from last week is the case where the caveat matters MOST, and
	// hanging it off a finding count would drop it exactly there.
	var out []string
	if line := advisorBaseLine(base); line != "" {
		out = append(out, line)
	}
	if len(sections) == 0 && len(failed) == 0 {
		return append(out, "ADVISORS: nothing to report")
	}
	// An empty Body is a RETRACTION (that advisor ran and found nothing), so it is not a
	// finding, and counting sections rather than findings reported ten of them where there
	// was one, each rendered as a title with nothing underneath. A reader who learns the
	// headline overstates by an order of magnitude stops reading the section.
	var found []AdviceSection
	var clear []string
	for _, s := range sections {
		if strings.TrimRight(s.Body, "\n") == "" {
			clear = append(clear, s.Name)
			continue
		}
		found = append(found, s)
	}
	out = append(out, fmt.Sprintf("ADVISORS: %d finding%s", len(found), plural.Word(len(found), "", "s")))
	for _, s := range found {
		out = append(out, "      "+s.Title)
		for _, line := range strings.Split(strings.TrimRight(s.Body, "\n"), "\n") {
			out = append(out, "        "+line)
		}
	}
	// Named, not just counted, and kept apart from the notes below: "ran and found nothing"
	// and "could not run" are the two ways a section goes quiet, and only one of them is
	// news about the change.
	if len(clear) > 0 {
		out = append(out, fmt.Sprintf("      %d ran and found nothing: %s",
			len(clear), strings.Join(clear, ", ")))
	}
	// Named rather than counted: which advisor went quiet is what a reader needs to decide
	// whether the silence above it means anything. Printed verbatim: each note already
	// says whether it is a warning or a "could not run" failure, and only collectAdvice
	// can tell the two apart.
	for _, f := range failed {
		out = append(out, "      "+f)
	}
	return out
}
