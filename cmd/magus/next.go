package main

import (
	"fmt"
	"io"

	"github.com/egladman/magus"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/types"
)

// The rendering half of hint.Next: the structured results carry the field, and text
// mode prints the same commands under a `next:` label so a person sees the step an
// agent's harness would.

// queryWithNext, explainWithNext and filesWithNext add `next` to a structured result
// without changing the record around it.
//
// Wrappers rather than fields on the types, because types/ deliberately imports
// almost nothing and hint is where the command paths live. json and yaml flatten the
// embedded result, so the wire shape is the result plus one key.
type queryWithNext struct {
	types.KnowledgeQueryOutput `yaml:",inline"`
	Next                       []hint.Next `json:"next,omitempty" yaml:"next,omitempty"`
}

type explainWithNext struct {
	types.KnowledgeExplainOutput `yaml:",inline"`
	Next                         []hint.Next `json:"next,omitempty" yaml:"next,omitempty"`
}

type filesWithNext struct {
	types.FileReport `yaml:",inline"`
	Next             []hint.Next `json:"next,omitempty" yaml:"next,omitempty"`
}

// nextGate holds each breadcrumb's Why to one firing per session, reusing the marker
// store the guard advisories already keep.
//
// A CLI run carries no host session id (only a hook envelope reports one), so these
// markers land in the anonymous bucket and expire on advisoryAnonWindow: one firing
// per checkout for a working session's length, which is the suppression the
// measurement asked for. A workspace magus cannot locate suppresses nothing, which
// is the right direction to fail.
func nextGate(root string) advisoryGate {
	dir, err := magus.ResolveCacheDir(resolveRootOrEmpty(root), magus.WithLoadedConfig(globalCfg))
	if err != nil {
		return advisoryGate{}
	}
	return newAdvisoryGate(dir, "")
}

// printNext writes a result's breadcrumbs, one line each.
//
// The Run line prints every time: it is navigation, and a reader who has seen it
// before still needs the ids filled in. The Why is advice, and advice says nothing
// the second time, so it goes through the gate. -s drops it outright without
// spending the firing, so the next full run still explains itself.
func printNext(w io.Writer, gate advisoryGate, next []hint.Next) {
	if len(next) == 0 {
		return
	}
	fmt.Fprintf(w, "\nnext:\n")
	for _, n := range next {
		why := ""
		if !global.silent {
			why = gate.once(advisoryKind("next-"+n.ID), n.Why)
		}
		if why == "" {
			fmt.Fprintf(w, "  %s\n", n.Run)
			continue
		}
		fmt.Fprintf(w, "  %s  (%s)\n", n.Run, why)
	}
}
