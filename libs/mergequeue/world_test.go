package mergequeue

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"maps"
	"slices"
	"testing"

	"github.com/stretchr/testify/require"
)

// world is one repository on the model: main, the provider, and the queue's steps run
// against them the way the CLI runs them.
type world struct {
	t    *testing.T
	m    *model
	p    *modelProvider
	base string // main's first commit
}

// newWorld starts main with files; ".generated" in files marks generated paths.
func newWorld(t *testing.T, files map[string]string) *world {
	t.Helper()
	m := newModel()
	initial := map[string]*string{}
	for p, body := range files {
		initial[p] = str(body)
	}
	w := &world{t: t, m: m, p: newModelProvider(m)}
	w.base = m.commit("", "initial", initial)
	m.set("refs/heads/main", w.base)
	return w
}

func (w *world) main() string { return w.m.ref("refs/heads/main") }

// commit adds a commit on from changing files (nil deletes).
func (w *world) commit(from, subject string, files map[string]*string) string {
	return w.m.commit(from, subject, files)
}

// open lists a change whose head is head, on branch "pr<id>", squashed, affecting "a".
func (w *world) open(id, h string, mods ...func(*Change)) Change {
	c := Change{ID: id, Head: h, Ref: "refs/pull/" + id + "/head", Branch: "pr" + id, Base: "main",
		Title: "change " + id, Author: "author" + id, Method: MethodSquash, Affected: []string{"a"}}
	for _, mod := range mods {
		mod(&c)
	}
	return w.p.list(c)
}

// push lands commit directly on main, the way a person with bypass rights would.
func (w *world) push(commit string) { w.m.set("refs/heads/main", commit) }

func (w *world) changes() Changes {
	w.t.Helper()
	cs, err := w.p.ListChanges(context.Background(), ListQuery{Base: "main"})
	require.NoError(w.t, err)
	return cs
}

func (w *world) plan(depth int) Plan {
	w.t.Helper()
	p, err := w.planner(depth).Run(context.Background(), w.changes())
	require.NoError(w.t, err)
	return p
}

func (w *world) planner(depth int) *Planner {
	pl := NewPlanner(w.m)
	pl.Provider, pl.Depth, pl.Parallel = w.p, depth, 4
	return pl
}

// validate runs a full validation of p and returns its verdicts.
func (w *world) validate(p Plan, gate Gate, configure ...func(*Validator)) *collected {
	w.t.Helper()
	got := &collected{}
	v := NewValidator(w.m, gate, got)
	v.Scratch = w.t.TempDir()
	for _, c := range configure {
		c(v)
	}
	require.NoError(w.t, v.Run(context.Background(), p))
	require.Empty(w.t, w.m.checkouts, "every checkout is removed")
	return got
}

// apply merges p from verdicts, and returns its events.
func (w *world) apply(p Plan, verdicts VerdictSource) ([]Event, error) {
	w.t.Helper()
	var buf bytes.Buffer
	a := NewApplier(w.p, w.m, verdicts)
	a.Interval, a.Events = 1, NewEvents(&buf)
	err := a.Run(context.Background(), p)
	return readEvents(w.t, &buf), err
}

func readEvents(t *testing.T, buf *bytes.Buffer) []Event {
	t.Helper()
	var out []Event
	sc := bufio.NewScanner(buf)
	for sc.Scan() {
		var e Event
		require.NoError(t, json.Unmarshal(sc.Bytes(), &e))
		out = append(out, e)
	}
	return out
}

// outcomes is each change's last event kind.
func outcomes(evs []Event) map[string]EventKind {
	out := map[string]EventKind{}
	for _, e := range evs {
		if e.Change != "" && e.Kind != EventNotice {
			out[e.Change] = e.Kind
		}
	}
	return out
}

// last is id's last non-notice event.
func last(evs []Event, id string) Event {
	for _, e := range slices.Backward(evs) {
		if e.Change == id && e.Kind != EventNotice {
			return e
		}
	}
	return Event{}
}

func verdictsByID(p Plan) map[string]Verdict {
	out := map[string]Verdict{}
	for _, v := range p.Verdicts {
		out[v.Change.ID] = v
	}
	return out
}

func idsOf(vs []Verdict) []string {
	var out []string
	for _, v := range vs {
		out = append(out, v.Change.ID)
	}
	return out
}

// regenerateIndex is a regeneration hook over the model: INDEX lists every other
// file's name and content, the way a root routing index is a function of the sources.
func (w *world) regenerateIndex(_ context.Context, dir, _ string, _ Change, _ []string) error {
	w.m.mu.Lock()
	defer w.m.mu.Unlock()
	work := w.m.checkouts[dir].work
	var b bytes.Buffer
	for _, p := range slices.Sorted(maps.Keys(work)) {
		if p != "INDEX" && p != generatedFile {
			b.WriteString(p + "=" + work[p] + ";")
		}
	}
	work["INDEX"] = b.String()
	return nil
}

// index is what regenerateIndex writes for files.
func index(files tree) string {
	var b bytes.Buffer
	for _, p := range slices.Sorted(maps.Keys(files)) {
		if p != "INDEX" && p != generatedFile {
			b.WriteString(p + "=" + files[p] + ";")
		}
	}
	return b.String()
}
