package main

import "github.com/egladman/magus/libs/proofread"

// withUserKinds adds the user's kinds to t; from is t's file.
func withUserKinds(t table, from string) (table, error) {
	kinds, err := proofread.WithUserKinds(t.kinds, from)
	if err != nil {
		return table{}, err
	}

	t.kinds = kinds

	return t, nil
}
