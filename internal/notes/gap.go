package notes

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
)

// SourceHarnessGap is a gap in the harness a person recorded: a place the guard or the
// tooling let work go wrong, captured with the refs it was seen in.
const SourceHarnessGap SourceKind = "harness-gap"

// Gap tags. A gap is captured open; ruled, refused and upstream are a person's edit of the
// tag, never a field magus sets.
const (
	TagGap     = "gap"
	TagGapOpen = "gap/open"
)

// GapResolver is what a gap capture needs from the graph: the resolution every anchor gets,
// the node id an anchor names, and the symbol a file#decl reference points at.
type GapResolver interface {
	Resolver
	NodeID(a Anchor) string
	Declaration(ctx context.Context, file, name string) (Anchor, error)
}

// Gap is a harness gap as a person captures it with `magus notes capture --gap`.
type Gap struct {
	Title string
	// Anchors are references as typed: kind:target, or file:<path>#<decl> for a declaration
	// in a file, which is stored as the symbol it names.
	Anchors []string
	// Refs are the guard, run and invocation refs the gap was seen in, recorded as cited.
	Refs     []string
	Tags     []string
	AsOf     string
	Captured time.Time
}

// Note resolves every anchor against res and renders the gap as a note under name, or the
// default name when name is empty.
//
// A gap is refused when any anchor resolves to nothing, naming that anchor, and when none of
// them is a symbol or diagnostic code: a gap found again only by its file or project points
// at too much to find it in.
func (g Gap) Note(ctx context.Context, name string, res GapResolver) (Note, error) {
	if strings.TrimSpace(g.Title) == "" {
		return Note{}, errors.New("notes: a gap needs a title")
	}
	if g.Captured.IsZero() {
		return Note{}, errors.New("notes: a gap needs the time it was captured")
	}
	if len(g.Anchors) == 0 {
		return Note{}, errors.New("notes: a gap needs at least one --anchor (symbol:<key>, file:<path>#<decl> or diagnostic:<code>), or nobody finds it again")
	}
	anchors := make([]Anchor, 0, len(g.Anchors))
	precise := false
	for _, raw := range g.Anchors {
		a, err := resolveGapAnchor(ctx, res, raw)
		if err != nil {
			return Note{}, err
		}
		if !slices.Contains(anchors, a) {
			anchors = append(anchors, a)
		}
		precise = precise || a.Kind == AnchorSymbol || a.Kind == AnchorDiagnostic
	}
	if !precise {
		return Note{}, errors.New("notes: a gap needs a symbol, file#declaration or diagnostic anchor, a file or project alone points at too much to find the gap in")
	}
	tags, err := gapTags(g.Tags)
	if err != nil {
		return Note{}, err
	}
	src := Source{
		Kind:     SourceHarnessGap,
		Ref:      strings.Join(g.Refs, " "),
		AsOf:     g.AsOf,
		Captured: g.Captured.UTC().Truncate(time.Second),
	}
	if name == "" {
		name = "gap-" + Digest(append([]string{g.Title}, g.Anchors...))[:12]
	}
	n := Note{
		Name:    name,
		ID:      name,
		Title:   g.Title,
		Tags:    tags,
		Anchors: anchors,
		Source:  &src,
		Find:    FindLines(anchors, res),
		Body:    gapBody(src),
	}
	if err := Validate(n); err != nil {
		return Note{}, err
	}
	return n, nil
}

func resolveGapAnchor(ctx context.Context, res GapResolver, raw string) (Anchor, error) {
	if rest, ok := strings.CutPrefix(raw, string(AnchorFile)+":"); ok {
		if file, decl, ok := strings.Cut(rest, "#"); ok {
			a, err := res.Declaration(ctx, strings.TrimSpace(file), strings.TrimSpace(decl))
			if err != nil {
				return Anchor{}, fmt.Errorf("notes: gap anchor %q resolves to nothing: %w", raw, err)
			}
			return a, nil
		}
	}
	a, err := ParseAnchor(raw)
	if err != nil {
		return Anchor{}, err
	}
	if !res.Resolves(ctx, a) {
		return Anchor{}, fmt.Errorf("notes: gap anchor %q resolves to nothing in the graph, so the gap could not be found again: `magus graph build` refreshes the graph when the code is new", raw)
	}
	return a, nil
}

// gapTags puts gap and gap/open first and keeps the caller's tags after them. A disposition
// tag is refused: it records what a person decided after reading the gap, which cannot have
// happened before it was captured.
func gapTags(extra []string) ([]string, error) {
	tags := []string{TagGap, TagGapOpen}
	for _, t := range extra {
		if strings.HasPrefix(t, TagGap+"/") && t != TagGapOpen {
			return nil, fmt.Errorf("notes: a gap is captured %s, tag it %s later with `magus notes edit`", TagGapOpen, t)
		}
		if !slices.Contains(tags, t) {
			tags = append(tags, t)
		}
	}
	return tags, nil
}

func gapBody(src Source) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Captured from a %s on %s.\n", src.Kind, src.Captured.Format(time.RFC3339))
	if src.Ref != "" {
		fmt.Fprintf(&b, "Seen in %s.\n", src.Ref)
	}
	if src.AsOf != "" {
		fmt.Fprintf(&b, "Found at %s.\n", src.AsOf)
	}
	b.WriteString("\nThe find commands in this note's frontmatter look the anchored code up as it is now, and `magus notes verify` says whether it moved since.\n")
	return b.String()
}

// FindLines derives the commands that look each anchor up again, in anchor order with
// duplicates dropped. ids names the node each anchor resolves to.
func FindLines(anchors []Anchor, ids interface{ NodeID(a Anchor) string }) []string {
	var out []string
	for _, a := range anchors {
		var line string
		switch a.Kind {
		case AnchorSymbol:
			line = "magus refs " + shellQuote(ids.NodeID(a))
		case AnchorDiagnostic:
			line = "magus query " + shellQuote("kind=diagnostic id="+a.Target)
		case AnchorNote:
			line = "magus notes get " + shellQuote(a.Target)
		default:
			line = "magus explain " + shellQuote(ids.NodeID(a))
		}
		if !slices.Contains(out, line) {
			out = append(out, line)
		}
	}
	return out
}

// FindBlock renders a note's find lines as the "find it:" block, followed by one stale line
// per anchor that no longer grades clean. A nil res checks nothing and says so, so an
// unchecked block never reads as a current one. A note with no find lines renders nothing.
func FindBlock(ctx context.Context, res Resolver, n Note) []string {
	if len(n.Find) == 0 {
		return nil
	}
	out := []string{"find it:"}
	for _, line := range n.Find {
		out = append(out, "  "+line)
	}
	if res == nil {
		return append(out, "  not checked: the knowledge graph did not load, so these may point at code that moved")
	}
	for _, a := range n.Anchors {
		if f := resolveAnchor(ctx, res, n.Name, a); f.code != "" {
			out = append(out, fmt.Sprintf("  stale (%s): %s", f.code, f.message))
		}
	}
	return out
}

// shellQuote leaves a word of plain characters bare and single-quotes anything else, so a
// SCIP key with spaces and backticks pastes into a shell as one argument.
func shellQuote(s string) string {
	if s != "" && strings.IndexFunc(s, func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && !strings.ContainsRune("-_./:=@", r)
	}) < 0 {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
