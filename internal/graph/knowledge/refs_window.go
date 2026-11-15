package knowledge

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/egladman/magus/libs/clikit/exit"
	"github.com/egladman/magus/types"
)

// RefsWindow is a source window and the command that asked for it, less
// --max-lines and --skip-lines, for the sentence that says what it cut.
type RefsWindow struct {
	SourceWindow
	Again string
}

// RefsWindowOptions is what `magus refs` knows of its window flags. The
// *Given fields say which were typed, since an explicit --before 0 overrides
// --context.
type RefsWindowOptions struct {
	Text, Source           bool
	Context, Before, After int
	BeforeGiven            bool
	AfterGiven             bool
	Lines                  string
	MaxLines, SkipLines    int
	// Sides, Ranged and Paged say whether any of --context/--before/--after,
	// --lines, and --max-lines/--skip-lines were typed.
	Sides, Ranged, Paged bool
}

// NewRefsWindow reads the window off the options, refusing the combinations
// that cannot go together.
func NewRefsWindow(o RefsWindowOptions) (RefsWindow, error) {
	if err := checkRefsWindow(o); err != nil {
		return RefsWindow{}, err
	}
	if o.Ranged {
		return refsLinesWindow(o)
	}
	return refsSidesWindow(o, o.Source || o.Sides || o.Paged), nil
}

// checkRefsWindow refuses the window flags that cannot go together.
func checkRefsWindow(o RefsWindowOptions) error {
	if o.Text && (o.Sides || o.Ranged || o.Paged) {
		return exit.Usagef("magus refs: --text prints no source; drop --context, --before, --after, --lines, --max-lines and --skip-lines")
	}
	if err := CheckSourcePage("refs", o.MaxLines, o.SkipLines); err != nil {
		return err
	}
	if o.Ranged && o.Sides {
		return exit.Usagef("magus refs: --lines sets both sides; use it alone, as --lines=-5:4, or use --context, --before and --after")
	}
	if !o.Ranged && min(o.Context, o.Before, o.After) < 0 {
		return exit.Usagef("magus refs: --context, --before and --after take 0 or more lines")
	}
	return nil
}

func refsLinesWindow(o RefsWindowOptions) (RefsWindow, error) {
	from, to, err := parseLineRange(o.Lines)
	return RefsWindow{SourceWindow: SourceWindow{Show: true, From: from, To: to, MaxLines: o.MaxLines, SkipLines: o.SkipLines}}, err
}

func refsSidesWindow(o RefsWindowOptions, show bool) RefsWindow {
	w := SourceWindow{Show: show, From: -o.Context, To: o.Context, MaxLines: o.MaxLines, SkipLines: o.SkipLines}
	if o.BeforeGiven {
		w.From = -o.Before
	}
	if o.AfterGiven {
		w.To = o.After
	}
	return RefsWindow{SourceWindow: w}
}

func parseLineRange(text string) (from, to int, err error) {
	a, b, found := strings.Cut(text, ":")
	from, errFrom := strconv.Atoi(a)
	to, errTo := strconv.Atoi(b)
	if !found || errFrom != nil || errTo != nil {
		return 0, 0, exit.Usagef("magus refs: --lines takes A:B counted from the match, as --lines=-5:-1; got %q", text)
	}
	if from > to {
		return 0, 0, exit.Usagef("magus refs: --lines %s runs backwards; write the smaller number first", text)
	}
	return from, to, nil
}

// CheckSourcePage refuses the --max-lines and --skip-lines no page can honor.
func CheckSourcePage(command string, maxLines, skipLines int) error {
	if maxLines < 0 || skipLines < 0 {
		return exit.Usagef("magus %s: --max-lines and --skip-lines take 0 or more lines", command)
	}
	return nil
}

// CheckSites checks each site and says what its window cut.
func (w RefsWindow) CheckSites(root, name string, sites []types.KnowledgeDefinitionSite, indexedAt func(file string) (time.Time, bool)) {
	w.CheckDefinitions(root, name, sites, indexedAt)
	if !w.Show {
		return
	}
	for i := range sites {
		site := &sites[i]
		if site.StartLine != 0 && site.Status != types.DefinitionUnreadable {
			site.SourceNote = w.note(SourceBlock{Text: site.Source, StartLine: site.SourceStartLine, Total: site.SourceTotalLines})
		}
	}
}

// note says what the window cut from block, empty when it cut nothing.
func (w RefsWindow) note(block SourceBlock) string {
	if block.StartLine == 0 {
		return w.noLinesNote(block.Total)
	}
	if block.Total == 0 {
		return ""
	}
	shown := strings.Count(block.Text, "\n") + 1
	if w.SkipLines+shown < block.Total {
		return fmt.Sprintf("%d of %d lines; next: %s --max-lines %d --skip-lines %d", shown, block.Total, w.Again, w.MaxLines, w.SkipLines+shown)
	}
	return fmt.Sprintf("%d of %d lines", shown, block.Total)
}

func (w RefsWindow) noLinesNote(total int) string {
	if total == 0 {
		return fmt.Sprintf("no lines: %d:%d falls outside the file", w.From, w.To)
	}
	return fmt.Sprintf("no lines: --skip-lines %d is past the last of %d lines", w.SkipLines, total)
}

// fillSources sets each occurrence's source and what its window cut.
func (w RefsWindow) fillSources(root string, files []types.SymbolOccurrenceFile) {
	w.FillOccurrences(root, files)
	for _, file := range files {
		for j := range file.Occurrences {
			occ := &file.Occurrences[j]
			if occ.SourceNote == "" {
				occ.SourceNote = w.note(SourceBlock{Text: occ.Source, StartLine: occ.SourceStartLine, Total: occ.SourceTotalLines})
			}
		}
	}
}
