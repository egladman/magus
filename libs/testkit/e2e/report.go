package e2e

import (
	"cmp"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/egladman/magus/internal/json"
)

// Outcome is how one case ended.
type Outcome string

const (
	OutcomePass Outcome = "pass"
	OutcomeSkip Outcome = "skip"
	OutcomeFail Outcome = "fail"
)

// Case is one subject x case row of a Report. Status is set when the
// test ends.
type Case struct {
	Subject string  `json:"subject"`
	Name    string  `json:"case"`
	Status  Outcome `json:"status"`
	Detail  string  `json:"detail,omitempty"`
}

// Report is the record of a run that outlives the test log: each case's
// outcome, plus whatever the suite attaches. It is safe for concurrent use.
type Report struct {
	mu       sync.Mutex
	cases    []Case
	attached map[string]any
}

// NewReport returns an empty report.
func NewReport() *Report { return &Report{attached: map[string]any{}} }

// Track adds a case to the report when t ends. The caller may set Detail
// on the returned Case until then.
func (r *Report) Track(t testing.TB, subject, name string) *Case {
	c := &Case{Subject: subject, Name: name}
	t.Cleanup(func() {
		c.Status = outcome(t)
		r.mu.Lock()
		defer r.mu.Unlock()
		r.cases = append(r.cases, *c)
	})
	return c
}

func outcome(t testing.TB) Outcome {
	switch {
	case t.Skipped():
		return OutcomeSkip
	case t.Failed():
		return OutcomeFail
	}
	return OutcomePass
}

// Attach stores value under key at the top level of the written report.
// Write owns the keys generated_at and cases, and overwrites them.
func (r *Report) Attach(key string, value any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.attached[key] = value
}

// Cases returns the finished cases, sorted by subject and then name.
func (r *Report) Cases() []Case {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := slices.Clone(r.cases)
	slices.SortFunc(out, func(a, b Case) int {
		return cmp.Or(strings.Compare(a.Subject, b.Subject), strings.Compare(a.Name, b.Name))
	})
	return out
}

// Write writes the report as JSON to path, creating its directory.
func (r *Report) Write(path string) error {
	cases := r.Cases()
	r.mu.Lock()
	doc := maps.Clone(r.attached)
	r.mu.Unlock()
	doc["generated_at"] = time.Now().UTC().Format(time.RFC3339)
	doc["cases"] = cases
	body, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return fmt.Errorf("e2e: encode report: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("e2e: write report: %w", err)
	}
	if err := os.WriteFile(path, append(body, '\n'), 0o644); err != nil {
		return fmt.Errorf("e2e: write report: %w", err)
	}
	return nil
}
