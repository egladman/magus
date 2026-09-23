package mergequeue

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

// Schema names. Each document carries its own under "schema"; a reader refuses any
// other, so a format change is a new name rather than a silent misread.
const (
	SchemaChanges = "mergequeue.changes/v1"
	SchemaPlan    = "mergequeue.plan/v1"
	SchemaVerdict = "mergequeue.verdict/v1"
	SchemaEvent   = "mergequeue.event/v1"
)

// MaxStackDepth bounds how many unlanded changes one change may be stacked on.
const MaxStackDepth = 16

// Changes is the queue's input: the changes carrying merge intent, in queue order.
type Changes struct {
	Schema  string   `json:"schema"`
	Base    string   `json:"base"`             // branch the queue merges into
	Remote  string   `json:"remote,omitempty"` // remote the provider was asked about
	Changes []Change `json:"changes"`
	// Landed are recently merged changes an open one may be stacked on. Planning
	// checks each one's commit is on the base before relying on it.
	Landed []Landed `json:"landed,omitempty"`
}

// Decision is what the queue decided for one change.
type Decision string

const (
	// DecisionMerge: validated green; merge once every change beneath it has.
	DecisionMerge Decision = "merge"
	// DecisionKick: a real conflict, a red gate, or a refusal; kick back with the report.
	DecisionKick Decision = "kick"
	// DecisionWait: not approved, or held behind a conflicting change; retried next run.
	DecisionWait Decision = "wait"
)

// Code says why a change waits or was kicked back, from a closed set a provider and a
// workflow can act on. A WAIT_ code goes with [DecisionWait], a KICK_ code with
// [DecisionKick].
type Code string

const (
	CodeNotApproved   Code = "WAIT_NOT_APPROVED"   // no approval at the commit a review of its head covers
	CodeHeadMoved     Code = "WAIT_HEAD_MOVED"     // its head moved since it was listed or validated
	CodeBehind        Code = "WAIT_BEHIND"         // a change beneath it did not merge, or it was not validated this run
	CodeConflictAhead Code = "WAIT_CONFLICT_AHEAD" // it conflicts with a change ahead of it, which merges first
	CodeRevalidate    Code = "WAIT_REVALIDATE"     // what it was validated on is no longer what it would merge onto
	CodeBranchMoved   Code = "WAIT_BRANCH_MOVED"   // its branch moved or was deleted before an update commit could land
	CodeHostRefused   Code = "WAIT_HOST_REFUSED"   // the provider refused the merge
	CodeMerged        Code = "WAIT_MERGED"         // its head is already on the base
	CodeParent        Code = "WAIT_PARENT"         // the change it is stacked on has not landed
	CodeParentKicked  Code = "WAIT_PARENT_KICKED"  // the change it is stacked on was kicked back
	CodeRestack       Code = "WAIT_RESTACK"        // it is not built on the head of the change it says it is stacked on
	CodeRetarget      Code = "WAIT_RETARGET"       // it targets another branch than the queue's base
	CodeMethodChanged Code = "WAIT_METHOD_CHANGED" // its merge method changed since validation

	CodeConflict Code = "KICK_CONFLICT" // a real conflict with the base in files that are not generated
	CodeRed      Code = "KICK_RED"      // the gate was red on its candidate
	CodeRefused  Code = "KICK_REFUSED"  // something the author has to fix that is neither
)

// Decision is the decision c goes with, or "" for a code outside the set.
func (c Code) Decision() Decision {
	switch c {
	case CodeNotApproved, CodeHeadMoved, CodeBehind, CodeConflictAhead, CodeRevalidate, CodeBranchMoved,
		CodeHostRefused, CodeMerged, CodeParent, CodeParentKicked, CodeRestack, CodeRetarget, CodeMethodChanged:
		return DecisionWait
	case CodeConflict, CodeRed, CodeRefused:
		return DecisionKick
	}
	return ""
}

// Plan is what validation builds candidates for and an [Applier] merges against.
type Plan struct {
	Schema     string `json:"schema"`
	Base       string `json:"base"`
	Remote     string `json:"remote,omitempty"` // remote the provider names its repository by
	BaseCommit string `json:"base_commit"`      // tip of Base every candidate is built on
	// Depth is how many candidates of one partition validate at once.
	Depth int `json:"depth"`
	// Partitions hold the admitted changes in queue order, every change after the one
	// it is stacked on. Changes in one partition stack; separate partitions share no
	// affected unit and never wait on each other.
	Partitions [][]Change `json:"partitions"`
	// Verdicts are the changes planning settled without validating them.
	Verdicts []Verdict `json:"verdicts,omitempty"`
	// Landed are the landed changes planning checked against the base, which an
	// Applier reads again when a rebased head's approval has to be carried over.
	Landed []Landed `json:"landed,omitempty"`
}

// heads is every head a planned change may be stacked on.
func (p Plan) heads() []string {
	var out []string
	for _, l := range p.Landed {
		out = append(out, l.Head)
	}
	for _, g := range p.Partitions {
		for _, c := range g {
			out = append(out, c.Head)
		}
	}
	return out
}

// Find returns the partition holding id and its position there.
func (p Plan) Find(id string) (partition, pos int, ok bool) {
	for gi, g := range p.Partitions {
		for i, c := range g {
			if c.ID == id {
				return gi, i, true
			}
		}
	}
	return 0, 0, false
}

// Verdict is what the queue decided for one change. Validation writes one per admitted
// change as a document of its own; planning's ride in [Plan.Verdicts].
type Verdict struct {
	Schema     string   `json:"schema,omitempty"`
	BaseCommit string   `json:"base_commit,omitempty"`
	Change     Change   `json:"change"`
	Decision   Decision `json:"decision"`
	Code       Code     `json:"code,omitempty"` // set on every wait and kick
	Reason     string   `json:"reason,omitempty"`
	Report     string   `json:"report,omitempty"` // kick-back body
	Paths      []string `json:"paths,omitempty"`  // the files a kick-back is about
	With       []string `json:"with,omitempty"`   // base-branch commits touching Paths
	// After is the change validated beneath this one, empty at the bottom of its
	// partition. An Applier holds a change whose After did not merge.
	After string `json:"after,omitempty"`
	// Onto is the commit the candidate was built onto: BaseCommit at the bottom of a
	// partition, else After's candidate.
	Onto string `json:"onto,omitempty"`
	// Candidate is the validated merge commit: base plus every change beneath this one
	// plus this one, generated files regenerated.
	Candidate string      `json:"candidate,omitempty"`
	Method    MergeMethod `json:"method,omitempty"`  // the merge method it was validated under
	Message   string      `json:"message,omitempty"` // squash body: the change's own commits
	// Reviewed is the commit a review of the head covers, when that took proving that
	// the head's generated files are what regeneration produces from reviewed sources.
	Reviewed string `json:"reviewed,omitempty"`
	// Depth is the candidate's speculation depth when its gate started: 1 ran on
	// validated commits alone, 2 on top of one unvalidated candidate, and so on.
	Depth      int   `json:"depth,omitempty"`
	DurationMS int64 `json:"duration_ms,omitempty"` // gate wall time

	// CandidateFile is the file holding Candidate's commits, set by whoever read the
	// verdict.
	CandidateFile string `json:"-"`
}

func (v Verdict) kick() Kick {
	return Kick{Code: v.Code, Report: v.Report, Paths: v.Paths, With: v.With, Candidate: v.Candidate}
}

func (v Verdict) check() error {
	if err := v.Change.Check(); err != nil {
		return err
	}
	switch v.Decision {
	case DecisionMerge:
		if v.Code != "" {
			return fmt.Errorf("a merge verdict on %s carries code %q", v.Change.Label(), v.Code)
		}
		return nil
	case DecisionKick, DecisionWait:
		if got := v.Code.Decision(); got != v.Decision {
			return fmt.Errorf("a %s verdict on %s carries code %q", v.Decision, v.Change.Label(), v.Code)
		}
		return nil
	}
	return fmt.Errorf("unknown decision %q", v.Decision)
}

// ReadChanges decodes and checks a [Changes] document.
func ReadChanges(r io.Reader) (Changes, error) {
	var c Changes
	if err := decode(r, SchemaChanges, &c, &c.Schema); err != nil {
		return Changes{}, err
	}
	if err := c.check(); err != nil {
		return Changes{}, fmt.Errorf("%s: %w", SchemaChanges, err)
	}
	return c, nil
}

func (c Changes) check() error {
	if err := CheckBranch(c.Base); err != nil {
		return fmt.Errorf("base: %w", err)
	}
	seen := make(map[string]bool, len(c.Changes)+len(c.Landed))
	for i, ch := range c.Changes {
		if err := ch.Check(); err != nil {
			return fmt.Errorf("changes[%d]: %w", i, err)
		}
		if seen[ch.ID] {
			return fmt.Errorf("changes[%d]: id %q appears twice", i, ch.ID)
		}
		seen[ch.ID] = true
	}
	for i, l := range c.Landed {
		if err := l.check(); err != nil {
			return fmt.Errorf("landed[%d]: %w", i, err)
		}
		if seen[l.ID] {
			return fmt.Errorf("landed[%d]: id %q appears twice", i, l.ID)
		}
		seen[l.ID] = true
	}
	return nil
}

func (l Landed) check() error {
	if err := CheckID(l.ID); err != nil {
		return err
	}
	if !isObjectID(l.Head) || !isObjectID(l.Commit) {
		return fmt.Errorf("#%s: head %q and commit %q must be full commit ids", l.ID, l.Head, l.Commit)
	}
	if !l.Method.valid() {
		return fmt.Errorf("#%s: merge method %q; want merge, squash or rebase", l.ID, l.Method)
	}
	return nil
}

// ReadPlan decodes and checks a [Plan] document.
func ReadPlan(r io.Reader) (Plan, error) {
	var p Plan
	if err := decode(r, SchemaPlan, &p, &p.Schema); err != nil {
		return Plan{}, err
	}
	if err := p.check(); err != nil {
		return Plan{}, fmt.Errorf("%s: %w", SchemaPlan, err)
	}
	return p, nil
}

func (p Plan) check() error {
	if err := CheckBranch(p.Base); err != nil {
		return fmt.Errorf("base: %w", err)
	}
	if !isObjectID(p.BaseCommit) {
		return fmt.Errorf("base_commit %q is not a commit id", p.BaseCommit)
	}
	if p.Depth < 1 {
		return fmt.Errorf("depth %d is below 1", p.Depth)
	}
	seen := map[string]bool{}
	add := func(c Change) error {
		if err := c.Check(); err != nil {
			return err
		}
		if seen[c.ID] {
			return fmt.Errorf("change %q appears twice", c.ID)
		}
		seen[c.ID] = true
		return nil
	}
	for _, g := range p.Partitions {
		ahead := make(map[string]bool, len(g))
		for _, c := range g {
			// A change merges after the one it is stacked on, so that one is earlier in
			// the same partition.
			if c.Below != "" && !ahead[c.Below] {
				return fmt.Errorf("%s is stacked on #%s, which is not ahead of it in its partition", c.Label(), c.Below)
			}
			if err := add(c); err != nil {
				return err
			}
			ahead[c.ID] = true
		}
	}
	for _, v := range p.Verdicts {
		if err := add(v.Change); err != nil {
			return err
		}
		if v.Decision != DecisionKick && v.Decision != DecisionWait {
			return fmt.Errorf("planning decides %q for %s; it only kicks back or waits", v.Decision, v.Change.Label())
		}
		if err := v.check(); err != nil {
			return err
		}
	}
	for _, l := range p.Landed {
		if err := l.check(); err != nil {
			return fmt.Errorf("landed: %w", err)
		}
	}
	return nil
}

// ReadVerdict decodes and checks a [Verdict] document.
func ReadVerdict(r io.Reader) (Verdict, error) {
	var v Verdict
	if err := decode(r, SchemaVerdict, &v, &v.Schema); err != nil {
		return Verdict{}, err
	}
	if err := v.check(); err != nil {
		return Verdict{}, fmt.Errorf("%s: %w", SchemaVerdict, err)
	}
	return v, nil
}

// WriteChanges encodes c on one line, stamping its schema.
func WriteChanges(w io.Writer, c Changes) error {
	c.Schema = SchemaChanges
	return json.NewEncoder(w).Encode(c)
}

// WritePlan encodes p, stamping its schema.
func WritePlan(w io.Writer, p Plan) error {
	p.Schema = SchemaPlan
	if p.Partitions == nil {
		p.Partitions = [][]Change{} // "partitions": [] rather than null for a reader iterating it
	}
	return encodeIndented(w, p)
}

// WriteVerdict encodes v, stamping its schema.
func WriteVerdict(w io.Writer, v Verdict) error {
	v.Schema = SchemaVerdict
	return encodeIndented(w, v)
}

func encodeIndented(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func decode(r io.Reader, schema string, v any, got *string) error {
	if err := json.NewDecoder(r).Decode(v); err != nil {
		return fmt.Errorf("decode %s: %w", schema, err)
	}
	if *got != schema {
		return fmt.Errorf("document is %q, want %q", *got, schema)
	}
	return nil
}

// Check reports whether c can be handed to a version control system and used as a
// directory name: an id [CheckID] accepts, full commit ids as head and stack base, refs
// and branches in the strict grammar [CheckBranch] names, and a merge method. Every
// field reaches a VCS command line, so a value that could read as an option or a
// refspec is refused here rather than quoted there.
func (c Change) Check() error {
	if err := CheckID(c.ID); err != nil {
		return err
	}
	if !isObjectID(c.Head) {
		return fmt.Errorf("%s: head %q is not a full commit id", c.Label(), c.Head)
	}
	if c.StackBase != "" && !isObjectID(c.StackBase) {
		return fmt.Errorf("%s: stack base %q is not a full commit id", c.Label(), c.StackBase)
	}
	if !c.Method.valid() {
		return fmt.Errorf("%s: merge method %q; want merge, squash or rebase", c.Label(), c.Method)
	}
	if c.Ref != "" {
		if !strings.HasPrefix(c.Ref, "refs/") {
			return fmt.Errorf("%s: ref %q does not start with refs/", c.Label(), c.Ref)
		}
		if err := checkRefName(c.Ref); err != nil {
			return fmt.Errorf("%s: ref: %w", c.Label(), err)
		}
	}
	for _, b := range []struct{ name, v string }{{"branch", c.Branch}, {"base", c.Base}} {
		if b.v == "" {
			continue
		}
		if err := CheckBranch(b.v); err != nil {
			return fmt.Errorf("%s: %s: %w", c.Label(), b.name, err)
		}
	}
	for _, id := range []struct{ name, v string }{{"parent", c.Parent}, {"below", c.Below}} {
		if id.v == "" {
			continue
		}
		if err := CheckID(id.v); err != nil {
			return fmt.Errorf("%s: %s: %w", c.Label(), id.name, err)
		}
	}
	return nil
}

// CheckID accepts 1 to 128 ASCII letters, digits, '.', '_' and '-', not starting with
// '.' or '-'. An id names a directory and an artifact, so "..", a separator, or a
// leading dot (which marks an entry in progress) would escape or hide it.
func CheckID(id string) error {
	if id == "" || len(id) > 128 || id[0] == '.' || id[0] == '-' {
		return fmt.Errorf("change id %q is empty, too long, or starts with '.' or '-'", id)
	}
	for i := range len(id) {
		b := id[i]
		if !('a' <= b && b <= 'z' || 'A' <= b && b <= 'Z' || '0' <= b && b <= '9' || b == '.' || b == '_' || b == '-') {
			return fmt.Errorf("change id %q holds %q; ids are letters, digits, '.', '_' and '-'", id, b)
		}
	}
	return nil
}

// CheckBranch accepts a branch name in git's check-ref-format grammar, which the queue
// holds every version control system to.
func CheckBranch(name string) error {
	if name == "" {
		return errors.New("empty branch name")
	}
	if strings.HasPrefix(name, "refs/") {
		return fmt.Errorf("branch %q is a full ref; name the branch alone", name)
	}
	return checkRefName(name)
}

// checkRefName applies git check-ref-format's rules, plus no leading '-'.
func checkRefName(name string) error {
	bad := func(why string) error { return fmt.Errorf("%q %s", name, why) }
	switch {
	case strings.HasPrefix(name, "-"):
		return bad("starts with '-'")
	case strings.HasPrefix(name, "/") || strings.HasSuffix(name, "/") || strings.Contains(name, "//"):
		return bad("has an empty component")
	case strings.HasSuffix(name, ".") || strings.HasSuffix(name, ".lock"):
		return bad("ends with '.' or '.lock'")
	case strings.Contains(name, "..") || strings.Contains(name, "@{") || strings.Contains(name, "/."):
		return bad("holds '..', '@{' or a component starting with '.'")
	case name == "@":
		return bad("is '@'")
	}
	for _, r := range name {
		if r < ' ' || r == 0x7f || strings.ContainsRune(" ~^:?*[\\", r) {
			return bad(fmt.Sprintf("holds %q", r))
		}
	}
	return nil
}

// isObjectID reports whether s is a full SHA-1 or SHA-256 object id in lowercase hex.
func isObjectID(s string) bool {
	if len(s) != 40 && len(s) != 64 {
		return false
	}
	for i := range len(s) {
		if b := s[i]; !('0' <= b && b <= '9' || 'a' <= b && b <= 'f') {
			return false
		}
	}
	return true
}
