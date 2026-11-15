package impact

import (
	"cmp"
	"context"
	"fmt"
	"strings"

	"github.com/egladman/magus/internal/changeset"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/marker"
	"github.com/egladman/magus/internal/review"
	"github.com/egladman/magus/libs/text/plural"
	"github.com/egladman/magus/types"
	difftypes "github.com/egladman/magus/types/diff"
	"github.com/egladman/magus/vcs"
)

// LeftBehind is what a change keeps alive or leaves unused: the markers it added
// (compat(until:) and Deprecated:), and the symbols that lost their last use in it and are
// still defined. Facts, not reasons: a marker says how long code stays, never whether it
// should.
type LeftBehind struct {
	Markers []Marker `json:"markers,omitempty" yaml:"markers,omitempty"`
	// Unreferenced is the symbol delta's Left, known only when UnreferencedKnown is;
	// UnreferencedReason says why it is not.
	Unreferenced       []string `json:"unreferenced,omitempty" yaml:"unreferenced,omitempty"`
	UnreferencedKnown  bool     `json:"unreferenced_known" yaml:"unreferenced_known"`
	UnreferencedReason string   `json:"unreferenced_reason,omitempty" yaml:"unreferenced_reason,omitempty"`
}

// Marker is one marker an added line carries.
type Marker struct {
	Path string `json:"path" yaml:"path"`
	Line int    `json:"line" yaml:"line"`
	// Marker and Text are marker.Found's.
	Marker string `json:"marker" yaml:"marker"`
	Text   string `json:"text" yaml:"text"`
}

// collectLeftBehind reads the markers from the patch's added lines, in any file the change
// did not generate, and the symbols it left unused from the graph. Only a working-tree diff
// reads symbols: the graph describes the tree on disk, which is the head of no other diff.
func collectLeftBehind(ctx context.Context, ws Workspace, src Sources, rev difftypes.Diff, patch string, working bool) *LeftBehind {
	generated := map[string]bool{}
	for _, f := range rev.Files {
		generated[f.Path] = f.Generated()
	}
	l := &LeftBehind{Markers: addedMarkers(patch, generated)}
	if !working {
		l.UnreferencedReason = "read only for the working tree, the tree the symbol graph describes"
		return l
	}
	res, err := vcs.Resolve(ctx, ws.Root(), "", ws.VCSOptions())
	if err != nil || res.Source == types.VCSSourceDisabled || res.VCS == nil {
		l.UnreferencedReason = "no version control answered here"
		return l
	}
	head, err := res.VCS.FindCommit(ctx, ws.Root(), "")
	if err != nil || head.ID == "" {
		l.UnreferencedReason = "the checkout has no HEAD to compare with"
		return l
	}
	g, err := src.LoadGraph(ctx)
	if err != nil {
		l.UnreferencedReason = fmt.Sprintf("the symbol graph could not be read: %v", err)
		return l
	}
	left, ok := src.UnusedSymbols(ctx, g, res.VCS, head.ID, review.ChangedPaths(rev))
	if !ok {
		l.UnreferencedReason = fmt.Sprintf("the symbol index does not cover the changed files; `%s` builds it", hint.GraphBuild)
		return l
	}
	l.Unreferenced, l.UnreferencedKnown = left, true
	return l
}

// addedMarkers are the markers on the added lines of patch, in a comment, outside the paths
// generated marks.
func addedMarkers(patch string, generated map[string]bool) []Marker {
	var out []Marker
	for _, f := range changeset.ParseHunks(patch) {
		if generated[f.Path] {
			continue
		}
		for _, h := range f.Hunks {
			line := h.NewStart
			for _, raw := range h.Lines {
				switch {
				case strings.HasPrefix(raw, "+"):
					if mk, ok := marker.Parse(raw[1:]); ok {
						out = append(out, Marker{Path: f.Path, Line: line, Marker: mk.Marker, Text: mk.Text})
					}
					line++
				case strings.HasPrefix(raw, "-"), strings.HasPrefix(raw, `\`):
				default:
					line++
				}
			}
		}
	}
	return out
}

// leftBehindLines renders the section, including its empty forms.
func leftBehindLines(l *LeftBehind) []string {
	if l == nil {
		return []string{"LEFT BEHIND: not measured"}
	}
	var out []string
	if len(l.Markers) == 0 {
		out = append(out, "LEFT BEHIND: no compat(until:) or Deprecated: marker added")
	} else {
		out = append(out, fmt.Sprintf("LEFT BEHIND: %d compat(until:) or Deprecated: marker%s added",
			len(l.Markers), plural.Word(len(l.Markers), "", "s")))
		for _, mk := range capList(l.Markers) {
			out = append(out, fmt.Sprintf("      %s:%d %s %s", mk.Path, mk.Line, mk.Marker, mk.Text))
		}
		out = append(out, moreLine(len(l.Markers))...)
	}
	switch {
	case !l.UnreferencedKnown:
		out = append(out, "      symbols left unused: not measured, "+cmp.Or(l.UnreferencedReason, "nothing read them"))
	case len(l.Unreferenced) == 0:
		out = append(out, "      no symbol it stopped using is still defined")
	default:
		out = append(out, fmt.Sprintf("      %d symbol%s lost the last use here and %s still defined",
			len(l.Unreferenced), plural.Word(len(l.Unreferenced), "", "s"), plural.Word(len(l.Unreferenced), "is", "are")))
		for _, name := range capList(l.Unreferenced) {
			out = append(out, "        "+name)
		}
		out = append(out, moreLine(len(l.Unreferenced))...)
	}
	return out
}
