package types

// DiffHunk is one hunk of a changed file, as the unified-diff reader numbered it. Index and
// Digest are the address the console, the read marks and Diff.Order share, so they are copied
// from the parsed patch and never recomputed.
type DiffHunk struct {
	// Index is the hunk's position within its file, from 0.
	Index int `json:"index" yaml:"index"`
	// Digest keys the hunk by content: its path and body lines, context included. A read mark
	// made on one patch matches another only when both carry the same context lines.
	Digest   string `json:"digest" yaml:"digest"`
	OldStart int    `json:"old_start" yaml:"old_start"`
	OldCount int    `json:"old_count" yaml:"old_count"`
	NewStart int    `json:"new_start" yaml:"new_start"`
	NewCount int    `json:"new_count" yaml:"new_count"`
	// Declaration is the enclosing declaration git named in the hunk header, empty when none.
	Declaration string `json:"declaration,omitempty" yaml:"declaration,omitempty"`
	// Symbols are IDs from the same file's DiffFile.Symbols whose changed lines fall in this
	// hunk, innermost first. Empty when no symbol index covers the file.
	Symbols []string `json:"symbols,omitempty" yaml:"symbols,omitempty"`
}

// DiffHunkRef addresses one hunk across files.
type DiffHunkRef struct {
	Path   string `json:"path" yaml:"path"`
	Index  int    `json:"index" yaml:"index"`
	Digest string `json:"digest" yaml:"digest"`
}

// DiffOrder is the order to read a changeset's hunks in: groups of steps, each hunk placed by
// a relationship between changed symbols. The same diff and index always give the same order.
type DiffOrder struct {
	Groups []DiffGroup `json:"groups" yaml:"groups"`
	// Count proves every hunk appears exactly once.
	Count DiffOrderCount `json:"count" yaml:"count"`
}

// DiffGroupKind says why hunks share a group.
type DiffGroupKind string

const (
	// DiffGroupConnected hunks are linked by definition, use or implementation.
	DiffGroupConnected DiffGroupKind = "connected"
	// DiffGroupGenerated hunks are declared generated output, read after the source that
	// produced them.
	DiffGroupGenerated DiffGroupKind = "generated"
	// DiffGroupUnranked hunks are ones magus could not place; each step's Why says why. It is
	// always the last group.
	DiffGroupUnranked DiffGroupKind = "unranked"
)

// DiffGroup is one set of connected hunks.
type DiffGroup struct {
	Kind DiffGroupKind `json:"kind" yaml:"kind"`
	// Label is the qualified name of the group's first defined symbol, empty for the generated
	// and unranked groups.
	Label string `json:"label,omitempty" yaml:"label,omitempty"`
	// Hunks is the number of hunks across Steps.
	Hunks int `json:"hunks" yaml:"hunks"`
	// Reach is the widest file reach among the group's symbols, the second ranking key after
	// size.
	Reach int        `json:"reach" yaml:"reach"`
	Steps []DiffStep `json:"steps" yaml:"steps"`
}

// DiffStep is one screen. Consecutive placements from one file are merged into a single step,
// so a step spans more than one file only when its hunks form a cycle.
type DiffStep struct {
	// Number counts steps across the whole order, from 1.
	Number int            `json:"number" yaml:"number"`
	Hunks  []DiffStepHunk `json:"hunks" yaml:"hunks"`
}

// DiffStepHunk is one placed hunk and the relationship that placed it.
type DiffStepHunk struct {
	Hunk DiffHunkRef `json:"hunk" yaml:"hunk"`
	// Label is the hunk's innermost changed symbol, or its declaration when it has none.
	Label string  `json:"label,omitempty" yaml:"label,omitempty"`
	Why   DiffWhy `json:"why" yaml:"why"`
}

// DiffWhyRelation is the relationship that placed a hunk.
type DiffWhyRelation string

const (
	// DiffWhyStarts opens a group: nothing else in it is placed before this hunk.
	DiffWhyStarts DiffWhyRelation = "starts"
	// DiffWhyUses follows Step because this hunk uses Symbol, which Step defines.
	DiffWhyUses DiffWhyRelation = "uses"
	// DiffWhyUsedBy precedes Step because Step uses Symbol, which this hunk defines.
	DiffWhyUsedBy DiffWhyRelation = "used_by"
	// DiffWhyImplements follows Step because this hunk implements the interface Symbol.
	DiffWhyImplements DiffWhyRelation = "implements"
	// DiffWhyImplementedBy precedes Step because Step implements this hunk's Symbol.
	DiffWhyImplementedBy DiffWhyRelation = "implemented_by"
	// DiffWhyContinues follows the previous hunk of the same symbol or file.
	DiffWhyContinues DiffWhyRelation = "continues"
	// DiffWhySameStep shares a step with the hunks in Cycle, which use each other.
	DiffWhySameStep DiffWhyRelation = "same_step"
	// DiffWhyTests follows Step because this test hunk exercises Symbol.
	DiffWhyTests DiffWhyRelation = "tests"
	// DiffWhyGenerated is declared generated output.
	DiffWhyGenerated DiffWhyRelation = "generated"
	// DiffWhyUnranked could not be placed; Text names why (binary, deleted, moved, no index,
	// no symbols). A hunk with symbols that nothing links to stands alone in its own
	// connected group instead.
	DiffWhyUnranked DiffWhyRelation = "unranked"
)

// DiffWhy names the relationship that placed a hunk. Text is the rendered sentence, so the
// terminal and the console never derive it themselves.
type DiffWhy struct {
	Relation DiffWhyRelation `json:"relation" yaml:"relation"`
	// Step is the number of the step this hunk is placed against, 0 for none.
	Step   int      `json:"step,omitempty" yaml:"step,omitempty"`
	Symbol string   `json:"symbol,omitempty" yaml:"symbol,omitempty"`
	Cycle  []string `json:"cycle,omitempty" yaml:"cycle,omitempty"`
	Text   string   `json:"text" yaml:"text"`
}

// DiffOrderCount is the completeness line. Complete is true only when Placed equals Hunks and
// Repeated and Missing are empty.
type DiffOrderCount struct {
	Hunks    int           `json:"hunks" yaml:"hunks"`
	Placed   int           `json:"placed" yaml:"placed"`
	Complete bool          `json:"complete" yaml:"complete"`
	Repeated []DiffHunkRef `json:"repeated,omitempty" yaml:"repeated,omitempty"`
	Missing  []DiffHunkRef `json:"missing,omitempty" yaml:"missing,omitempty"`
	// Bare lists changed files with no hunk (binary, rename only, mode only), so a file the
	// order cannot show is still named.
	Bare []string `json:"bare,omitempty" yaml:"bare,omitempty"`
}
