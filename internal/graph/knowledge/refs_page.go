package knowledge

import (
	"fmt"
	"strings"

	"github.com/egladman/magus/internal/textindex"
	"github.com/egladman/magus/libs/clikit/exit"
	"github.com/egladman/magus/types"
)

// RefsPage is an occurrence page and the command that asked for it, less
// --limit and --offset, for the sentence that says what it cut.
type RefsPage struct {
	OccurrencePage
	Again string
}

// RefsPageOptions is what `magus refs` knows of its paging flags and trailing paths.
type RefsPageOptions struct {
	Text, Occurrences bool
	OffsetGiven       bool
	Limit, Offset     int
	// Root and ScopeArgs are the workspace root and the paths typed after the symbol.
	Root      string
	ScopeArgs []string
	// Again is the command that asked, less --limit and --offset.
	Again string
}

// NewRefsPage validates the paging flags and the trailing paths. A path that does
// not exist is an error, since a mistyped second symbol reaches here reading as one.
func NewRefsPage(o RefsPageOptions) (RefsPage, error) {
	if o.Text {
		return RefsPage{}, nil
	}
	if !o.Occurrences && o.OffsetGiven {
		return RefsPage{}, exit.Usagef("magus refs: --offset narrows --occurrences sites; add --occurrences")
	}
	if o.Occurrences && (o.Limit < 0 || o.Offset < 0) {
		return RefsPage{}, exit.Usagef("magus refs: --limit and --offset take 0 or more sites")
	}
	abs, err := textindex.ResolveScopes(o.Root, o.ScopeArgs)
	if err != nil {
		return RefsPage{}, exit.Usagef("magus refs: a trailing word scopes the answer to a path: %v", err)
	}
	page, err := NewOccurrencePage(o.Root, abs, o.Limit, o.Offset)
	if err != nil {
		return RefsPage{}, exit.Usagef("magus refs: %v", err)
	}
	return RefsPage{OccurrencePage: page, Again: o.Again}, nil
}

// scopeNote counts what the paths left out, so a narrowed count reads as one.
func (p RefsPage) scopeNote(files []types.SymbolOccurrenceFile, kept int) string {
	if len(p.Scopes) == 0 {
		return ""
	}
	total := 0
	for _, f := range files {
		total += len(f.Occurrences)
	}
	return fmt.Sprintf("%s keeps %d of %d sites", strings.Join(p.Scopes, ", "), kept, total)
}

// RefsNote is scopeNote for the plain reference list.
func (p RefsPage) RefsNote(kept, total int) string {
	return fmt.Sprintf("%s keeps %d of %d references", strings.Join(p.Scopes, ", "), kept, total)
}

// note says what the page cut, and is empty when it cut nothing.
func (p RefsPage) note(shown, total int) string {
	switch {
	case shown == total:
		return ""
	case p.Offset >= total:
		return fmt.Sprintf("0 of %d sites; --offset %d is past the last site", total, p.Offset)
	case p.Offset+shown < total:
		return fmt.Sprintf("%d of %d sites; next: %s --limit %d --offset %d", shown, total, p.Again, p.Limit, p.Offset+shown)
	}
	return fmt.Sprintf("%d of %d sites", shown, total)
}
