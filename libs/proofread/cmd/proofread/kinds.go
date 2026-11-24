package main

import (
	"errors"
	"fmt"

	"github.com/egladman/magus/libs/proofread"
)

// withUserKinds adds the user's kinds to t; from is t's file.
func withUserKinds(t table, from string) (table, error) {
	kinds, err := proofread.WithUserKinds(t.kinds, from)
	if err != nil {
		return table{}, err
	}

	t.kinds = kinds

	return t, nil
}

// codeCommentOptions reads --language and --budgets, which code-comment alone
// takes. Source on stdin names its language; a file's comes from its
// extension.
func codeCommentOptions(kind proofread.Kind, o *kindOptions) ([]proofread.Option, error) {
	if kind != proofread.KindCodeComment {
		if o.language != "" || o.budgets != "" {
			return nil, fmt.Errorf("--language and --budgets judge code-comment, not %s", kind)
		}

		return nil, nil
	}

	var opts []proofread.Option

	if o.budgets != "" {
		b, err := proofread.ParseCommentBudgets([]byte(o.budgets))
		if err != nil {
			return nil, fmt.Errorf("--budgets: %w", err)
		}

		opts = append(opts, proofread.WithCommentBudgets(b))
	}

	switch {
	case o.stdin && o.language == "":
		return nil, errors.New("code-comment --stdin needs --language go, ts or buzz")
	case !o.stdin && o.language != "":
		return nil, errors.New("--language names the language of stdin, and a file's comes from its extension")
	case o.stdin:
		lang, ok := proofread.ParseLanguage(o.language)
		if !ok {
			return nil, fmt.Errorf("unknown --language %q: want go, ts or buzz", o.language)
		}

		opts = append(opts, proofread.WithLanguage(lang))
	}

	return opts, nil
}
