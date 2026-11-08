package diff

import (
	"fmt"
	"strconv"
)

// Hunk is one hunk of a changed file, as the unified-diff reader numbered it. Index and
// Digest are the address the console, the read marks and Diff.Order share, so they are copied
// from the parsed patch and never recomputed.
type Hunk struct {
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
	// Symbols are IDs from the same file's File.Symbols whose changed lines fall in this
	// hunk, innermost first (smallest definition extent first). Order labels a hunk by the
	// first one, so a caller building a hunk must keep that order. Empty when no symbol index
	// covers the file.
	Symbols []string `json:"symbols,omitempty" yaml:"symbols,omitempty"`
}

// HunkRef addresses one hunk across files.
type HunkRef struct {
	Path   string `json:"path" yaml:"path"`
	Index  int    `json:"index" yaml:"index"`
	Digest string `json:"digest" yaml:"digest"`
}

// Key is the "path#index" that names the hunk in a report. Digest is left out, so two refs to
// the same hunk of one patch share a Key.
func (r HunkRef) Key() string { return fmt.Sprintf("%s#%s", r.Path, strconv.Itoa(r.Index)) }

// Order is the order to read a changeset's hunks in: groups of steps, each hunk placed by
// a relationship between changed symbols. The same diff and index always give the same order.
//
// It orders hunks, where Diff.SortForReading orders whole files. The two are independent: a
// caller listing files uses the file order, and one walking hunks uses this, which can visit a
// file's hunks in separate steps.
type Order struct {
	Groups []Group `json:"groups" yaml:"groups"`
	// Count proves every hunk appears exactly once.
	Count OrderCount `json:"count" yaml:"count"`
}

// GroupKind says why hunks share a group.
type GroupKind string

const (
	// GroupConnected hunks are linked by definition, use or implementation.
	GroupConnected GroupKind = "connected"
	// GroupGenerated hunks are declared generated output, read after the source that
	// produced them.
	GroupGenerated GroupKind = "generated"
	// GroupUnranked hunks are ones magus could not place; each step's Why says why. It is
	// always the last group.
	GroupUnranked GroupKind = "unranked"
)

// Group is one set of hunks that belong together: hunks linked by a relationship between
// changed symbols, the generated output, or the hunks that could not be placed. Kind says which.
type Group struct {
	Kind GroupKind `json:"kind" yaml:"kind"`
	// Label is the qualified name of the group's first defined symbol, empty for the generated
	// and unranked groups.
	Label string `json:"label,omitempty" yaml:"label,omitempty"`
	// HunkCount is the number of hunks across Steps.
	HunkCount int `json:"hunk_count" yaml:"hunk_count"`
	// Reach is the widest file reach among the group's symbols, the second ranking key after
	// size.
	Reach int        `json:"reach" yaml:"reach"`
	Steps []Step `json:"steps" yaml:"steps"`
}

// Step is one screen. Consecutive placements from one file are merged into a single step,
// so a step spans more than one file only when its hunks form a cycle.
type Step struct {
	// Number counts steps across the whole order, from 1.
	Number int            `json:"number" yaml:"number"`
	Hunks  []StepHunk `json:"hunks" yaml:"hunks"`
}

// StepHunk is one placed hunk and the relationship that placed it.
type StepHunk struct {
	Ref HunkRef `json:"ref" yaml:"ref"`
	// Label is the hunk's innermost changed symbol, or its declaration when it has none.
	Label string  `json:"label,omitempty" yaml:"label,omitempty"`
	Why   Why `json:"why" yaml:"why"`
}

// WhyRelation is the relationship that placed a hunk.
type WhyRelation string

const (
	// WhyStarts opens a group: nothing else in it is placed before this hunk.
	WhyStarts WhyRelation = "starts"
	// WhyUses follows Step because this hunk uses Symbol, which Step defines.
	WhyUses WhyRelation = "uses"
	// WhyUsedBy precedes Step because Step uses Symbol, which this hunk defines.
	WhyUsedBy WhyRelation = "used_by"
	// WhyImplements follows Step because this hunk implements the interface Symbol.
	WhyImplements WhyRelation = "implements"
	// WhyImplementedBy precedes Step because Step implements this hunk's Symbol.
	WhyImplementedBy WhyRelation = "implemented_by"
	// WhyContinues follows the previous hunk of the same symbol or file.
	WhyContinues WhyRelation = "continues"
	// WhyContinuedBy precedes Step because Step continues the symbol this hunk begins.
	WhyContinuedBy WhyRelation = "continued_by"
	// WhySameStep shares a step with the hunks in Cycle, which use each other.
	WhySameStep WhyRelation = "same_step"
	// WhyTests follows Step because this test hunk exercises Symbol.
	WhyTests WhyRelation = "tests"
	// WhyGenerated is declared generated output.
	WhyGenerated WhyRelation = "generated"
	// WhyUnranked could not be placed; Text names why (binary, deleted, moved, no index,
	// no symbols). A hunk with symbols that nothing links to stands alone in its own
	// connected group instead.
	WhyUnranked WhyRelation = "unranked"
)

// Why names the relationship that placed a hunk. Text is the rendered sentence, so the
// terminal and the console never derive it themselves.
type Why struct {
	Relation WhyRelation `json:"relation" yaml:"relation"`
	// Step is the number of the step this hunk is placed against, 0 for none.
	Step   int      `json:"step,omitempty" yaml:"step,omitempty"`
	Symbol string   `json:"symbol,omitempty" yaml:"symbol,omitempty"`
	Cycle  []string `json:"cycle,omitempty" yaml:"cycle,omitempty"`
	Text   string   `json:"text" yaml:"text"`
}

// OrderCount is the completeness line. Complete is true only when Placed equals HunkCount
// and Repeated and Missing are empty, so a renderer trusts it instead of recounting.
type OrderCount struct {
	HunkCount int           `json:"hunk_count" yaml:"hunk_count"`
	Placed    int           `json:"placed" yaml:"placed"`
	Complete  bool          `json:"complete" yaml:"complete"`
	Repeated  []HunkRef `json:"repeated,omitempty" yaml:"repeated,omitempty"`
	Missing   []HunkRef `json:"missing,omitempty" yaml:"missing,omitempty"`
	// FilesWithoutHunks lists changed files with no hunk (binary, rename only, mode only), so
	// a file the order cannot show is still named.
	FilesWithoutHunks []string `json:"files_without_hunks,omitempty" yaml:"files_without_hunks,omitempty"`
}
