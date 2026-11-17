package interp

import (
	"context"
	"fmt"
	"sync"
)

const (
	declRemoteBackend  = `magus\cache.remote`
	declSecretProvider = `magus\secret.provider`
)

// Choices are the workspace-wide selections one magusfile evaluation made: the spell
// magus\secret.provider named and the spell magus\cache.remote named. An empty field
// means the evaluation made no such choice. The code that opens the workspace applies
// them; an evaluation only reports them.
type Choices struct {
	SecretProvider string
	RemoteBackend  string
}

// ChoiceConflictError is two different spells named for one workspace-wide choice.
// FirstProject and SecondProject are empty when both calls ran in one evaluation, and
// name the two magusfiles' projects when separate evaluations disagree.
type ChoiceConflictError struct {
	Declaration   string
	First, Second string

	FirstProject, SecondProject string
}

func (e *ChoiceConflictError) Error() string {
	if e.FirstProject == "" && e.SecondProject == "" {
		return fmt.Sprintf("%s: this magusfile chose %q and then %q, and a workspace has one, so keep one",
			e.Declaration, e.First, e.Second)
	}
	return fmt.Sprintf("%s: project %q chose %q and project %q chose %q, and a workspace has one, so keep one",
		e.Declaration, e.FirstProject, e.First, e.SecondProject, e.Second)
}

// outsideMagusfileError is a workspace-wide declaration called where no magusfile
// evaluation is collecting choices, such as a `magus buzz` script.
type outsideMagusfileError struct {
	Declaration string
}

func (e *outsideMagusfileError) Error() string {
	return fmt.Sprintf("%s is a magusfile declaration, and this call runs outside a magusfile evaluation, so move it to a magusfile's top level", e.Declaration)
}

// choiceRecord collects one evaluation's [Choices]. Only [RecordSecretProvider] and
// [RecordRemoteBackend] fill it. Safe for concurrent use.
type choiceRecord struct {
	mu      sync.Mutex
	choices Choices
}

type choiceRecordKey struct{}

// withChoiceRecord installs a fresh record on ctx for one evaluation and returns it.
func withChoiceRecord(ctx context.Context) (context.Context, *choiceRecord) {
	rec := &choiceRecord{}
	return context.WithValue(ctx, choiceRecordKey{}, rec), rec
}

// Choices returns what the evaluation has chosen so far.
func (r *choiceRecord) Choices() Choices {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.choices
}

// RecordSecretProvider records spell as the evaluation's secret provider. It returns an
// [*outsideMagusfileError] when ctx carries no record, and a [*ChoiceConflictError] when
// the evaluation already chose a different spell. Naming the same spell again passes.
func RecordSecretProvider(ctx context.Context, spell string) error {
	return record(ctx, declSecretProvider, spell, func(c *Choices) *string { return &c.SecretProvider })
}

// RecordRemoteBackend records spell as the evaluation's remote cache backend, with the
// errors of [RecordSecretProvider].
func RecordRemoteBackend(ctx context.Context, spell string) error {
	return record(ctx, declRemoteBackend, spell, func(c *Choices) *string { return &c.RemoteBackend })
}

func record(ctx context.Context, decl, spell string, field func(*Choices) *string) error {
	rec, _ := ctx.Value(choiceRecordKey{}).(*choiceRecord)
	if rec == nil {
		return &outsideMagusfileError{Declaration: decl}
	}
	rec.mu.Lock()
	defer rec.mu.Unlock()
	slot := field(&rec.choices)
	if *slot != "" && *slot != spell {
		return &ChoiceConflictError{Declaration: decl, First: *slot, Second: spell}
	}
	*slot = spell
	return nil
}

// ChoiceMerge folds the [Choices] of every magusfile in a workspace into the one set the
// workspace applies. The zero value is ready to use; it is not safe for concurrent use.
type ChoiceMerge struct {
	choices                       Choices
	secretProject, backendProject string
}

// Add folds project's choices in. Two projects naming the same spell pass; different
// spells are a [*ChoiceConflictError] naming both projects, and c is not folded.
func (m *ChoiceMerge) Add(project string, c Choices) error {
	if err := conflict(declSecretProvider, m.choices.SecretProvider, m.secretProject, c.SecretProvider, project); err != nil {
		return err
	}
	if err := conflict(declRemoteBackend, m.choices.RemoteBackend, m.backendProject, c.RemoteBackend, project); err != nil {
		return err
	}
	if c.SecretProvider != "" && m.choices.SecretProvider == "" {
		m.choices.SecretProvider, m.secretProject = c.SecretProvider, project
	}
	if c.RemoteBackend != "" && m.choices.RemoteBackend == "" {
		m.choices.RemoteBackend, m.backendProject = c.RemoteBackend, project
	}
	return nil
}

// Choices returns the merged choices.
func (m *ChoiceMerge) Choices() Choices { return m.choices }

func conflict(decl, held, heldProject, next, nextProject string) error {
	if held == "" || next == "" || held == next {
		return nil
	}
	return &ChoiceConflictError{Declaration: decl, First: held, Second: next, FirstProject: heldProject, SecondProject: nextProject}
}
