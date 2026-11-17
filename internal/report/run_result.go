package report

import (
	"context"
	"log/slog"
	"slices"
	"strings"
	"sync"

	"github.com/egladman/magus/internal/journal"
)

// ResultRefs collects the output ref of every target result the invocation's journal
// records, in the order the results arrive.
type ResultRefs struct {
	mu   sync.Mutex
	refs []string
}

func (c *ResultRefs) Enabled(context.Context, slog.Level) bool { return true }

func (c *ResultRefs) Handle(_ context.Context, r slog.Record) error {
	e, ok := journal.EventFromRecord(r)
	if !ok || e.Kind != journal.KindResult || e.Ref == "" {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if !slices.Contains(c.refs, e.Ref) {
		c.refs = append(c.refs, e.Ref)
	}
	return nil
}

func (c *ResultRefs) WithAttrs([]slog.Attr) slog.Handler { return c }
func (c *ResultRefs) WithGroup(string) slog.Handler      { return c }

// List is the refs collected so far, in arrival order.
func (c *ResultRefs) List() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.refs)
}

// ResultStatuses collects the journal status of the asked target's result,
// by project. A dependency's result is left out.
type ResultStatuses struct {
	target string
	mu     sync.Mutex
	status map[string]string
}

// NewResultStatuses collects the results of target.
func NewResultStatuses(target string) *ResultStatuses {
	return &ResultStatuses{target: target, status: map[string]string{}}
}

func (c *ResultStatuses) Enabled(context.Context, slog.Level) bool { return true }

func (c *ResultStatuses) Handle(_ context.Context, r slog.Record) error {
	e, ok := journal.EventFromRecord(r)
	if !ok || e.Kind != journal.KindResult || e.Status == "" {
		return nil
	}
	// The journal names a charmed step "target:charm,...".
	if name, _, _ := strings.Cut(e.Target, ":"); name != c.target {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.status[e.Project] = e.Status
	return nil
}

func (c *ResultStatuses) WithAttrs([]slog.Attr) slog.Handler { return c }
func (c *ResultStatuses) WithGroup(string) slog.Handler      { return c }

// Of is the project's result status, empty when none arrived. A nil collector
// reports nothing.
func (c *ResultStatuses) Of(project string) string {
	if c == nil {
		return ""
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.status[project]
}

// RefLine is the one line a successful `-s` run prints: every other line of a pass is
// dropped, and without the ref a caller reruns without -s just to learn it. It mirrors the
// "ref  <id>" line a default run prints under each target; "" when no target left a ref.
func RefLine(refs []string) string {
	switch len(refs) {
	case 0:
		return ""
	case 1:
		return "ref  " + refs[0]
	default:
		return "refs  " + strings.Join(refs, " ")
	}
}

// RunArtifact is one file the run produced, with the classification `magus describe
// file` would give it. Role rides along because "where did the build put it" and "is
// this thing generated" are the two questions an agent asks in sequence, and
// answering only the first leaves it to guess the second.
type RunArtifact struct {
	Path string `json:"path"           yaml:"path"`
	Glob string `json:"glob"           yaml:"glob"`
	Role string `json:"role,omitempty" yaml:"role,omitempty"`
}

// RunProject is one project's slice of the run. Artifacts and Value are per
// project because a run fans out: a single flat list could not say which project
// produced which file, and a single value would be meaningless for a multi-project
// run.
type RunProject struct {
	Path string `json:"path" yaml:"path"`
	// Status is the journal's result: pass, cached or permitted.
	// Permitted passed only by a deviation ledger. A dry run has none.
	Status string `json:"status,omitempty" yaml:"status,omitempty"`
	// Value is what the target returned (str or [str]), absent for a `> void`
	// target. Replayed from the cache entry on a hit, so it is present whether or
	// not this invocation actually executed the target.
	Value     any           `json:"value,omitempty"     yaml:"value,omitempty"`
	Artifacts []RunArtifact `json:"artifacts,omitempty" yaml:"artifacts,omitempty"`
}

// RunOutput is the structured result of `magus run <target>`.
type RunOutput struct {
	Target   string       `json:"target"           yaml:"target"`
	Charms   []string     `json:"charms,omitempty" yaml:"charms,omitempty"`
	DryRun   bool         `json:"dry_run"          yaml:"dry_run"`
	Count    int          `json:"count"            yaml:"count"`
	Projects []RunProject `json:"projects"         yaml:"projects"`
	// Permitted counts targets that passed only by a deviation ledger.
	Permitted int `json:"permitted,omitempty" yaml:"permitted,omitempty"`
	// UndeclaredSeeds names the projects an affected run selected ONLY through changed
	// files no project declares (MGS1028): targets whose answer could not have moved.
	// Absent rather than empty when there are none, so a consumer counting the debt can
	// tell "none" from "not reported".
	UndeclaredSeeds []string `json:"undeclared_seeds,omitempty" yaml:"undeclared_seeds,omitempty"`
}
