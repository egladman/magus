package job

import (
	"fmt"
	"path"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/egladman/magus/libs/codec"
)

// Declaration is the typed INPUT for one lease: the fields a caller DECLARES,
// and nothing the store computes.
//
// It is a DECLARATION and not a merge: an omitted field is cleared rather than
// kept. A [Put] does the opposite: it merges.
//
// JSON only, so no yaml tags. The members it does not declare are kept in its
// [Schema] by its own MarshalJSON and UnmarshalJSON, which plain encoding/json
// calls; libs/codec's UnmarshalStrict refuses them instead.
type Declaration struct {
	// Schema is the job shape this record is written in. The version is
	// required, and a reader refuses one it does not know.
	Schema
	// ID is the lease's identity within the plan, the key a second register replaces on,
	// and the only required field besides the version.
	ID string `json:"id" schema:"leaseid"`
	// Parent is the lease this one was spawned under, empty for a lease the root declared.
	Parent string `json:"parent,omitempty" schema:"leaseid"`
	// Agent is the agent section; nil is ordinary work. See [Job.Agent].
	Agent *Agent `json:"agent,omitzero"`
	// Checkpoint is the working state this lease starts from, as the caller's
	// checkpoint token. See [Job.Checkpoint].
	Checkpoint string `json:"checkpoint,omitempty"`
	// Paths are what this lease may write and read. See [Paths].
	Paths Paths `json:"paths,omitzero"`
	// DependsOn names the lease ids that must land before this one.
	DependsOn []string `json:"depends_on,omitempty" schema:"leaseid"`
	// Integrates names the branches this job merges; see [Job.Integrates].
	Integrates []string `json:"integrates,omitempty"`
	// Breaks are the flags, JSON keys, exported symbols or stored formats the root approved
	// this job breaking; empty means it breaks nothing on its own authority. See [Job.Breaks].
	Breaks []string `json:"breaks,omitempty"`
	// Weight is the job's lean on the machine budget, in (0,1]: omitted means 0.5, and an
	// explicit 0 is out of range. See [Job.Weight].
	Weight *float64 `json:"weight,omitzero"`
	// BriefFormat is the format a brief to this job's worker takes; empty defers to
	// the workspace's own default. See [Job.BriefFormat].
	BriefFormat BriefFormat `json:"brief_format,omitempty"`
	// Delivery and ParentHead: see [Job.Delivery].
	Delivery   string `json:"delivery,omitempty"`
	ParentHead string `json:"parent_head,omitempty"`
	// Follows names the job this one continues. See [Job.Follows].
	Follows string `json:"follows,omitempty" schema:"leaseid"`
	// Checkout is an absolute directory, outside the workspace, where the fork makes a new
	// checkout of the repository for this job at its checkpoint, or at the forking
	// checkout's revision when it names none.
	// The job owns it: see [Worktree]. Empty works wherever the job is taken.
	Checkout string `json:"checkout,omitempty" schema:"abspath"`
	// Check is the one check this lease runs, which the store reads through
	// [WithCheckSpec], and acceptance binds a worker's evidence to it. The zero Check
	// declares none.
	Check Check `json:"check,omitzero"`
	// State is the job's lifecycle position, empty for a job that has not said
	// where it stands. no_return is a lease that never reported, which is not
	// a failure.
	State State `json:"state,omitempty"`
	// ReadOnly marks a lease that gathers evidence and writes nothing, so empty write
	// paths are correct rather than missing.
	ReadOnly bool `json:"read_only,omitzero"`
	// Goals are what done means, graded by [Store.Grade]. See [Job.Goals].
	Goals []Goal `json:"goals,omitempty"`
	// Timeout bounds the lease, as a Go duration. The store stamps Job.Deadline from it when
	// it writes the job, so a declaration carries a length and never an
	// instant. Empty is no bound; there is no default here (a workspace may
	// set jobs.default_timeout).
	Timeout string `json:"timeout,omitempty" schema:"duration"`
	// MaxAttempts caps how many times the job may be forked, its first fork included; zero
	// is no cap. See [Job.MaxAttempts].
	MaxAttempts int `json:"max_attempts,omitzero"`
	// MaxClaims caps how many proved claims one attempt takes; zero takes the default. See
	// [Job.MaxClaims].
	MaxClaims int `json:"max_claims,omitzero"`
	// Deviations accept permissible problems on the record's own authority.
	Deviations []Deviation `json:"deviations,omitempty"`
	// Enter turns the record into an entry rather than a declaration: it acknowledges one
	// write into this path, inside the write paths of the live job ID names, by somebody
	// other than its holder. A record carrying it declares nothing else.
	Enter string `json:"enter,omitempty"`
	// Claim turns the record into a proved claim of the job ID names: each entry adds
	// one write path with the proof the store re-proves before it writes. A record carrying
	// it declares nothing else.
	Claim []ClaimRequest `json:"claim,omitempty"`
	// Deferred and LeftUnmerged turn the record into a settlement of the
	// job ID names, ended or not. It declares nothing else.
	Deferred     []Deferred `json:"deferred,omitempty"`
	LeftUnmerged string     `json:"left_unmerged,omitempty"`
}

// ClaimRequest is one write path a claim record asks for, and Because the proof:
// `refs:<symbol>`, `test:<file>`, `output:<project>:<target>` or `released`. See
// [ParseClaimEvidence].
//
// Callers, given instead of Path and Because, names a symbol: the caller expands
// the request into one per declaration holding an occurrence of it, path
// `<file>#<declaration>` because `refs:<symbol>`, and the store proves each of those as
// it would one written by hand.
type ClaimRequest struct {
	Path    string `json:"path,omitempty" schema:"writepath"`
	Because string `json:"because,omitempty" schema:"nonblank"`
	Callers string `json:"callers,omitempty" schema:"nonblank"`
}

// ClaimKind is what a proved claim rests on.
type ClaimKind string

const (
	// ClaimRefs is a path that references a declaration the job's diff changed inside
	// its write paths.
	ClaimRefs ClaimKind = "refs"
	// ClaimTest is a test beside a file the job changed that references what it defines.
	ClaimTest ClaimKind = "test"
	// ClaimOutput is a declared output of a target whose sources the job changed.
	ClaimOutput ClaimKind = "output"
	// ClaimReleased is a path the job's own holder released in this attempt.
	ClaimReleased ClaimKind = "released"
	// ClaimDeadHolder is a write path of the live job it names whose holder is dead: its
	// session ended, or it made no judged call past the silence bound with no background
	// command running.
	ClaimDeadHolder ClaimKind = "dead"
)

// ClaimEvidence is a parsed `because`: the kind and what it names, empty for released.
type ClaimEvidence struct {
	Kind    ClaimKind `json:"kind"              yaml:"kind"`
	Subject string    `json:"subject,omitempty" yaml:"subject,omitempty"`
}

// String is the evidence as a `because` spells it.
func (e ClaimEvidence) String() string {
	if e.Subject == "" {
		return string(e.Kind)
	}
	return fmt.Sprintf("%s:%s", string(e.Kind), e.Subject)
}

// ParseClaimEvidence reads a `because`. An output subject is `<project>:<target>`.
func ParseClaimEvidence(because string) (ClaimEvidence, error) {
	kind, subject, _ := strings.Cut(strings.TrimSpace(because), ":")
	e := ClaimEvidence{Kind: ClaimKind(kind), Subject: strings.TrimSpace(subject)}
	refuse := func(reason string) (ClaimEvidence, error) {
		return e, &ClaimEvidenceError{Because: because, Reason: reason}
	}
	switch {
	case !e.Kind.Valid():
		return refuse("names no proof, so use one of refs:<symbol>, test:<file>, output:<project>:<target>, dead:<job> or released")
	case e.Kind == ClaimReleased && e.Subject != "":
		return refuse("names a subject, and released names nothing after it")
	case e.Kind != ClaimReleased && e.Subject == "":
		return refuse(fmt.Sprintf("names no %s subject", e.Kind))
	case e.Kind == ClaimOutput && !strings.Contains(e.Subject, ":"):
		return refuse("names no target, so spell it output:<project>:<target>")
	}
	return e, nil
}

// ClaimEvidenceError is a `because` [ParseClaimEvidence] refuses, and why.
type ClaimEvidenceError struct {
	Because string
	Reason  string
}

func (e *ClaimEvidenceError) Error() string { return strconv.Quote(e.Because) + " " + e.Reason }

// Claim is one write path a holder added with a proof. Proof is the sentence the
// store found it true by, At unix seconds and By who wrote it.
type Claim struct {
	Path     string        `json:"path"     yaml:"path"`
	Evidence ClaimEvidence `json:"evidence" yaml:"evidence"`
	Proof    string        `json:"proof"    yaml:"proof"`
	At       int64         `json:"at"       yaml:"at"`
	By       Origin        `json:"by"       yaml:"by"`
}

// pathOutsideWorkspace says why a declared path can name no workspace-relative path, or
// "": the guard matches declarations against paths relative to the workspace root, never
// expanding a ~ or reading an absolute path in one.
func pathOutsideWorkspace(p string) string {
	file, _ := SplitClaim(p)
	switch clean := path.Clean(file); {
	case strings.HasPrefix(file, "~"):
		return "begins with ~"
	case path.IsAbs(clean):
		return "is absolute"
	case clean == ".." || strings.HasPrefix(clean, "../"):
		return "climbs out of the workspace with .."
	}
	return ""
}

// Faults is every fault the record shows on its own, without a workspace: each field's
// shape, the deviations and the goals. What a check says is the store's [CheckSpec]'s
// to refuse, at write. It never stops at the first. A
// deviation may permit only a code in permissible, the caller's own.
func (d Declaration) Faults(permissible ...Code) []Fault {
	var ps []Fault
	invalid := func(field, message, fix string) {
		ps = append(ps, Fault{Kind: FaultRecord, Field: field, Message: message, Fix: fix})
	}
	if rule := idRule(strings.TrimSpace(d.ID)); rule != "" {
		invalid("id", fmt.Sprintf("%q is not a lease id: %s", d.ID, rule), "name a branch-shaped id such as fix-parser or area/fix-parser")
	}
	if d.Enter != "" {
		rest := d
		rest.Schema, rest.ID, rest.Enter = Schema{}, "", ""
		if !reflect.ValueOf(rest).IsZero() {
			invalid("enter", "a record carrying enter enters a job and declares nothing, so it carries only api_version, id and enter",
				"drop every other field, or drop enter")
		}
		return ps
	}
	if len(d.Claim) > 0 {
		return append(ps, d.claimFaults()...)
	}
	if d.Settles() {
		return append(ps, d.settlementFaults()...)
	}
	if p := strings.TrimSpace(d.Parent); p != "" && !ValidID(p) {
		invalid("parent", fmt.Sprintf("parent %q is not a lease id", d.Parent), "name the id of the job this one was forked under")
	}
	if d.Agent != nil {
		if err := d.Agent.Validate(); err != nil {
			invalid("agent", err.Error(), `write worker {"type": "resumed"}, or {"type": "new", "reason": "<why>"}`)
		}
	}
	for i, dep := range d.DependsOn {
		if d := strings.TrimSpace(dep); d != "" && !ValidID(d) {
			invalid(fmt.Sprintf("depends_on[%d]", i), fmt.Sprintf("depends_on[%d] %q is not a lease id", i, dep),
				"name the id of a job that must land first")
		}
	}
	for i, branch := range d.Integrates {
		at := fmt.Sprintf("integrates[%d]", i)
		switch b := strings.TrimSpace(branch); {
		case !validIntegratedBranch(b):
			invalid(at, fmt.Sprintf("%s %q is not a branch name a merge can take", at, branch),
				"name a local branch, such as the one a finished job committed on")
		case slices.Contains(d.Integrates[:i], branch):
			invalid(at, fmt.Sprintf("%s %q is named twice", at, branch), "drop the repeat")
		}
	}
	if len(d.Integrates) > 0 && d.ReadOnly {
		invalid("read_only", "an integration job merges branches, so it writes", "drop read_only, or drop integrates")
	}
	if d.State != "" && !ValidState(d.State) {
		invalid("state", fmt.Sprintf("state %q is not a job state", d.State), "use one of "+StateVocabulary())
	}
	if _, err := ParseTimeout(d.Timeout); err != nil {
		invalid("timeout", fmt.Sprintf("timeout %q is not a positive duration", d.Timeout), "name one such as 30m or 2h, or leave it out")
	}
	if d.BriefFormat != "" && !d.BriefFormat.Valid() {
		invalid("brief_format", fmt.Sprintf("brief_format %q is not a brief format", d.BriefFormat),
			fmt.Sprintf("use one of %s, or leave it out for the workspace's default", strings.Join(d.BriefFormat.Values(), ", ")))
	}
	if d.Delivery != "" && !slices.Contains(Deliveries(), d.Delivery) {
		invalid("delivery", fmt.Sprintf("delivery %q is not a delivery", d.Delivery),
			"use one of "+strings.Join(Deliveries(), ", "))
	}
	if d.ParentHead != "" && d.Delivery != DeliveryStacked {
		invalid("parent_head", "parent_head is the base of a stacked job's pull request, and this job is not stacked",
			"drop parent_head, or set delivery to "+DeliveryStacked)
	}
	if d.Weight != nil && !ValidWeight(*d.Weight) {
		invalid("weight", fmt.Sprintf("weight %v is outside (0,1]", *d.Weight),
			fmt.Sprintf("use a weight above 0 and at most 1, or leave it out for %v", DefaultWeight))
	}
	if d.MaxAttempts < 0 {
		invalid("max_attempts", fmt.Sprintf("max_attempts is %d, and it is a count of forks", d.MaxAttempts), "use a positive count, or 0 for no cap")
	}
	if d.MaxClaims < 0 {
		invalid("max_claims", fmt.Sprintf("max_claims is %d, and it is a count of claims", d.MaxClaims), "use a positive count, or 0 for the default")
	}
	if d.Checkout != "" && !filepath.IsAbs(d.Checkout) {
		invalid("checkout", fmt.Sprintf("checkout %q is relative, and a fork names the checkout it makes by an absolute path", d.Checkout),
			"name an absolute directory outside the workspace")
	}
	for _, field := range []struct {
		name  string
		paths []string
	}{{"paths.write", d.Paths.Write}, {"paths.deny", d.Paths.Deny}, {"paths.read", d.Paths.Read}} {
		for i, p := range field.paths {
			if why := pathOutsideWorkspace(p); why != "" {
				at := fmt.Sprintf("%s[%d]", field.name, i)
				invalid(at, fmt.Sprintf("%s %q %s, so it matches no workspace path and bounds nothing", at, p, why),
					"name it relative to the workspace root")
			}
		}
	}
	for i, p := range d.Paths.Soft {
		if !slices.Contains(d.Paths.Write, p) {
			at := fmt.Sprintf("paths.soft[%d]", i)
			invalid(at, fmt.Sprintf("%s %q is not in paths.write, and a soft path is a write path that overlaps nothing", at, p),
				"name an entry of paths.write, or drop it")
		}
	}
	for i, b := range d.Breaks {
		if strings.TrimSpace(b) == "" {
			at := fmt.Sprintf("breaks[%d]", i)
			invalid(at, at+" is blank", "name the flag, key, symbol or format the job may break, or drop the entry")
		}
	}
	for i, w := range d.Deviations {
		at := fmt.Sprintf("deviations[%d]", i)
		if !slices.Contains(permissible, w.Code) {
			invalid(at+".code", fmt.Sprintf("%s permits %q, which is not a permissible code", at, w.Code),
				fmt.Sprintf("permit only %s, and fix the record for anything else", orList(permissible)))
		}
		if strings.TrimSpace(w.Subject) == "" {
			invalid(at+".subject", at+" names no subject", "name the write path, `target project` or output path the problem names")
		}
		if strings.TrimSpace(w.Reason) == "" {
			invalid(at+".reason", at+" gives no reason", "say why the problem is accepted, such as \"the root regenerates it\"")
		}
	}
	return append(ps, goalFaults(d.Goals, !d.Check.IsZero())...)
}

// claimFaults is every fault of a claim record: it carries only schema_version, id
// and claim, and each entry names one literal workspace path and a proof that parses.
func (d Declaration) claimFaults() []Fault {
	var ps []Fault
	invalid := func(field, message, fix string) {
		ps = append(ps, Fault{Kind: FaultRecord, Field: field, Message: message, Fix: fix})
	}
	rest := d
	rest.Schema, rest.ID, rest.Claim = Schema{}, "", nil
	if !reflect.ValueOf(rest).IsZero() {
		invalid("claim", "a record carrying claim claims a job and declares nothing, so it carries only api_version, id and claim",
			"drop every other field, or drop claim")
	}
	for i, w := range d.Claim {
		at := fmt.Sprintf("claim[%d]", i)
		if strings.TrimSpace(w.Callers) != "" {
			if w.Path != "" || w.Because != "" {
				invalid(at+".callers", fmt.Sprintf("%s names callers of %s and also a path or a proof, and callers expands into both", at, w.Callers),
					"drop path and because, or drop callers")
			}
			continue
		}
		switch p := strings.TrimSpace(w.Path); {
		case p == "":
			invalid(at+".path", at+" names no path", "name the file to add, relative to the workspace root")
		case pathOutsideWorkspace(p) != "":
			invalid(at+".path", fmt.Sprintf("%s %q %s", at, w.Path, pathOutsideWorkspace(p)), "name it relative to the workspace root")
		case isGlobMeta(p) || strings.Contains(p, "#"):
			invalid(at+".path", fmt.Sprintf("%s %q is a pattern or a claim, and a proof covers one file", at, w.Path), "name one file")
		}
		if _, err := ParseClaimEvidence(w.Because); err != nil {
			invalid(at+".because", fmt.Sprintf("%s: %s", at, err), "name the proof the path rests on")
		}
	}
	return ps
}

// Settles reports whether the record answers a job's deferred items or states
// why its work stays unmerged, rather than declaring a spec.
func (d Declaration) Settles() bool {
	return len(d.Deferred) > 0 || d.LeftUnmerged != ""
}

func (d Declaration) settlementFaults() []Fault {
	var ps []Fault
	invalid := func(field, message, fix string) {
		ps = append(ps, Fault{Kind: FaultRecord, Field: field, Message: message, Fix: fix})
	}
	rest := d
	rest.Schema, rest.ID, rest.Deferred, rest.LeftUnmerged = Schema{}, "", nil, ""
	if !reflect.ValueOf(rest).IsZero() {
		invalid("deferred", "a record carrying deferred or left_unmerged settles a job and declares nothing, so it carries only api_version, id, deferred and left_unmerged",
			"drop every other field, or drop deferred and left_unmerged")
	}
	if d.LeftUnmerged != "" && strings.TrimSpace(d.LeftUnmerged) == "" {
		invalid("left_unmerged", "left_unmerged is blank", "state why the work stays off the merge branch")
	}
	for i, d := range d.Deferred {
		at := fmt.Sprintf("deferred[%d]", i)
		row, reason := strings.TrimSpace(d.Job), strings.TrimSpace(d.Reason)
		switch {
		case strings.TrimSpace(d.Item) == "":
			invalid(at+".item", at+" names no item", "copy the item's text from the job's result")
		case (row == "") == (reason == ""):
			invalid(at, at+" answers with neither or both of job and reason", "name the declared job that carries it, or the reason it is dismissed")
		case row != "" && !ValidID(row):
			invalid(at+".job", fmt.Sprintf("%s job %q is not a lease id", at, d.Job), "name the declared job that carries it")
		}
	}
	return ps
}

// ParseTimeout reads a declared timeout: empty is no bound and parses to zero, anything
// else must be a positive Go duration.
func ParseTimeout(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 {
		return 0, &TimeoutError{Timeout: s}
	}
	return d, nil
}

// TimeoutError is a declared timeout [ParseTimeout] refuses.
type TimeoutError struct{ Timeout string }

func (e *TimeoutError) Error() string {
	return fmt.Sprintf("timeout %q is not a positive duration such as 30m or 2h", e.Timeout)
}

// The deliveries a person picks for a job's work.
const (
	// DeliveryFold folds the work into the parent's pull request.
	DeliveryFold = "fold"
	// DeliverySeparate opens its own pull request against the base branch.
	DeliverySeparate = "separate"
	// DeliveryStacked opens a pull request based on the parent's head.
	DeliveryStacked = "stacked"
)

// Deliveries is the closed set [Job.Delivery] takes.
func Deliveries() []string {
	return []string{DeliveryFold, DeliverySeparate, DeliveryStacked}
}

// StateVocabulary is the closed set of job states, as an error quotes it. Shared by
// Declaration.Validate and the merge a caller makes of a [Put], the two places a
// caller-supplied state is checked against the set.
func StateVocabulary() string {
	states := states()
	names := make([]string, len(states))
	for i, s := range states {
		names[i] = string(s)
	}
	return strings.Join(names, ", ")
}

// Apply writes this declaration onto a job. Store-computed fields are untouched, and so
// is Validation: a job that already carries releases or a registration keeps them.
func (d Declaration) Apply(j *Job) {
	j.Parent = strings.TrimSpace(d.Parent)
	j.Checkpoint = strings.TrimSpace(d.Checkpoint)
	j.Paths.Write = trimmedNonEmpty(d.Paths.Write)
	j.Paths.Deny = trimmedNonEmpty(d.Paths.Deny)
	j.Paths.Soft = trimmedNonEmpty(d.Paths.Soft)
	j.Paths.Read = trimmedNonEmpty(d.Paths.Read)
	j.DependsOn = trimmedNonEmpty(d.DependsOn)
	j.Integrates = trimmedNonEmpty(d.Integrates)
	j.Breaks = trimmedNonEmpty(d.Breaks)
	j.Agent = d.Agent.Trimmed()
	j.Weight = 0
	if d.Weight != nil {
		j.Weight = *d.Weight
	}
	j.BriefFormat = d.BriefFormat
	j.Delivery = d.Delivery
	j.ParentHead = strings.TrimSpace(d.ParentHead)
	j.Follows = strings.TrimSpace(d.Follows)
	// The store validates the check when it writes the row; see [WithCheckSpec].
	j.Check = d.Check
	j.Goals = cloneGoals(d.Goals)
	j.State = d.State
	j.ReadOnly = d.ReadOnly
	j.MaxAttempts = d.MaxAttempts
	j.MaxClaims = d.MaxClaims
	j.Deviations = slices.Clone(d.Deviations)
}

// ApplySpec writes this declaration's spec onto a job, re-applying a declaration to a
// row that exists. It is Apply, but the job's state is kept, and so is its checkpoint
// when the declaration names none.
func (d Declaration) ApplySpec(j *Job) {
	state, checkpoint := j.State, j.Checkpoint
	d.Apply(j)
	j.State = state
	if j.Checkpoint == "" {
		j.Checkpoint = checkpoint
	}
}

func cloneGoals(in []Goal) []Goal {
	if in == nil {
		return nil
	}
	out := make([]Goal, len(in))
	for i, goal := range in {
		// RESOLVED on the way through, which is what makes this the write boundary the
		// enums' no-empty-member rule depends on: every path that stores goals (Apply,
		// job.PutMerge) clones them through here.
		out[i] = goal.Resolve()
		out[i].Check = goal.Check
		out[i].Paths = slices.Clone(goal.Paths)
		out[i].Symbols = slices.Clone(goal.Symbols)
		out[i].DependsOn = slices.Clone(goal.DependsOn)
		out[i].Accept = slices.Clone(goal.Accept)
	}
	return out
}

// trimmedNonEmpty trims every element of in and drops the ones left empty.
func trimmedNonEmpty(in []string) []string {
	var out []string
	for _, s := range in {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// MarshalJSON writes the declaration with the members it does not declare that it kept.
func (d Declaration) MarshalJSON() ([]byte, error) {
	type plain Declaration
	return codec.MarshalWithUnknown(plain(d), d.unknown)
}

// UnmarshalJSON reads the declaration, keeping the members it does not declare.
// libs/codec's UnmarshalStrict refuses them instead.
func (d *Declaration) UnmarshalJSON(data []byte) error {
	type plain Declaration
	var p plain
	unknown, err := codec.UnmarshalWithUnknown(data, &p)
	if err != nil {
		return err
	}
	*d = Declaration(p)
	d.unknown = unknown
	return nil
}
