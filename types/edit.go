package types

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// EditSetSchemaVersion is the newest [EditSet] shape this magus reads. A set carrying a
// larger one is refused by name rather than read as a set with fields it ignored.
const EditSetSchemaVersion = 1

// EditAnchor names how an [EditSite] finds its bytes in the file.
type EditAnchor string

const (
	// EditAnchorLines addresses whole lines, terminators included: [start, end], 1-based
	// and inclusive. end = start-1 is the empty span before line start, an insertion.
	EditAnchorLines EditAnchor = "lines"
	// EditAnchorText addresses the bytes equal to Old: its one occurrence, or every
	// occurrence when All is set.
	EditAnchorText EditAnchor = "text"
)

// EditSet is a multi-file edit declared as data. Every site is checked against the file
// on disk before any file is written, and either every file is written or none is. The
// caller authors every replacement; magus never invents text.
type EditSet struct {
	SchemaVersion int        `json:"schema_version" yaml:"schema_version" jsonschema:"the EditSet shape this set was written against; 1"`
	Edits         []EditSite `json:"edits"          yaml:"edits"`
}

// EditSite is one replacement. Sites in one file may not overlap.
type EditSite struct {
	Path   string     `json:"path"             yaml:"path"             jsonschema:"workspace-relative path of an existing regular file; a declared output is refused"`
	Anchor EditAnchor `json:"anchor"           yaml:"anchor"           jsonschema:"lines: whole lines by number; text: the bytes equal to old"`
	Lines  []int      `json:"lines,omitempty"  yaml:"lines,omitempty"  jsonschema:"lines anchor: [start, end], 1-based inclusive, terminators included; end = start-1 inserts before line start"`
	Old    string     `json:"old,omitempty"    yaml:"old,omitempty"    jsonschema:"the exact bytes at the anchor: required for text, checked when given for lines"`
	New    string     `json:"new"              yaml:"new"              jsonschema:"the replacement bytes; empty deletes"`
	All    bool       `json:"all,omitempty"    yaml:"all,omitempty"    jsonschema:"text anchor only: replace every occurrence; without it old must occur exactly once"`
	Digest string     `json:"digest,omitempty" yaml:"digest,omitempty" jsonschema:"sha256:<hex> of the whole file as the caller read it; a file that differs is refused"`
}

// Validate reports what makes the site unreadable before any file is opened: no path,
// an unknown anchor, a malformed line range, or a field its anchor does not take.
func (e EditSite) Validate() error {
	if e.Path == "" {
		return errors.New("no path")
	}
	switch e.Anchor {
	case EditAnchorLines:
		if len(e.Lines) != 2 {
			return fmt.Errorf("the lines anchor takes [start, end], got %d numbers", len(e.Lines))
		}
		if start, end := e.Lines[0], e.Lines[1]; start < 1 || end < start-1 {
			return fmt.Errorf("lines [%d, %d] is not a range: start is 1-based and end is at least start-1", start, end)
		}
		if e.All {
			return errors.New("all applies only to the text anchor")
		}
	case EditAnchorText:
		if e.Old == "" {
			return errors.New("the text anchor needs old, the bytes it replaces")
		}
		if len(e.Lines) > 0 {
			return errors.New("lines applies only to the lines anchor")
		}
	case "":
		return fmt.Errorf("no anchor; one of %s", strings.Join(e.Anchor.Values(), ", "))
	default:
		return fmt.Errorf("unknown anchor %q; one of %s", e.Anchor, strings.Join(e.Anchor.Values(), ", "))
	}
	return nil
}

// EditReceipt is what applying or checking an [EditSet] produced. A refused set carries
// Refused and nothing else happened; an applied one carries ID, the per-file digests,
// and Undo, the set that reverses it.
type EditReceipt struct {
	SchemaVersion int `json:"schema_version" yaml:"schema_version"`
	// ID names the stored receipt `magus edit --undo` reads. Empty unless Applied.
	ID               string        `json:"id,omitempty"                yaml:"id,omitempty"`
	Applied          bool          `json:"applied"                     yaml:"applied"`
	AppliedAt        time.Time     `json:"applied_at,omitzero"         yaml:"applied_at,omitempty"`
	CheckpointBefore string        `json:"checkpoint_before,omitempty" yaml:"checkpoint_before,omitempty"`
	CheckpointAfter  string        `json:"checkpoint_after,omitempty"  yaml:"checkpoint_after,omitempty"`
	Files            []EditedFile  `json:"files,omitempty"             yaml:"files,omitempty"`
	Spans            []EditSpan    `json:"spans,omitempty"             yaml:"spans,omitempty"`
	Undo             *EditSet      `json:"undo,omitempty"              yaml:"undo,omitempty"`
	Refused          []EditRefusal `json:"refused,omitempty"           yaml:"refused,omitempty"`
}

// EditedFile is one file a set rewrites, with its digest before and after.
type EditedFile struct {
	Path         string `json:"path"          yaml:"path"`
	DigestBefore string `json:"digest_before" yaml:"digest_before"`
	DigestAfter  string `json:"digest_after"  yaml:"digest_after"`
}

// EditSpan is where one site resolved in the file as it was read. A text anchor with All
// resolves to one span per occurrence.
type EditSpan struct {
	// Edit indexes the site in [EditSet.Edits].
	Edit  int          `json:"edit"  yaml:"edit"`
	Path  string       `json:"path"  yaml:"path"`
	Start EditPosition `json:"start" yaml:"start"`
	// End is exclusive.
	End EditPosition `json:"end" yaml:"end"`
}

// EditPosition is a 1-based line and a 1-based byte column.
type EditPosition struct {
	Line int `json:"line" yaml:"line"`
	Col  int `json:"col"  yaml:"col"`
}

// EditRefusal is one reason a set was not applied. Edit indexes the site, or is -1 for
// a reason that belongs to a file rather than to one site.
type EditRefusal struct {
	Edit   int    `json:"edit"   yaml:"edit"`
	Path   string `json:"path"   yaml:"path"`
	Reason string `json:"reason" yaml:"reason"`
}
