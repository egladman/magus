package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"

	"github.com/egladman/magus/libs/proofread"
	"github.com/egladman/magus/libs/proofread/internal/xdg"
)

// userKindsPath is $XDG_CONFIG_HOME/proofread/kinds.json (see [xdg.ConfigDir]).
func userKindsPath() (string, error) {
	dir, err := xdg.ConfigDir()
	if err != nil {
		return "", fmt.Errorf("find the user kinds file's directory: %w", err)
	}

	return filepath.Join(dir, "proofread", "kinds.json"), nil
}

// readUserKinds returns the kinds in the user kinds file and its path. An
// absent file declares none. The file takes "kinds" alone, so it cannot loosen
// a repository's rules, paths or minimums.
func readUserKinds() (kinds []proofread.KindSpec, path string, err error) {
	path, err = userKindsPath()
	if err != nil {
		return nil, "", err
	}

	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, path, nil
	}

	if err != nil {
		return nil, path, fmt.Errorf("read user kinds %s: %w", path, err)
	}

	var raw struct {
		Kinds map[string]proofread.KindDecl `json:"kinds"`
	}

	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()

	if err := dec.Decode(&raw); err != nil {
		return nil, path, fmt.Errorf("user kinds %s: %w", path, err)
	}

	kinds, err = proofread.DeclareKinds(raw.Kinds)
	if err != nil {
		return nil, path, fmt.Errorf("user kinds %s: %w", path, err)
	}

	return kinds, path, nil
}

// withUserKinds adds the user kinds to t. A name both declare is an error
// naming both files; from is t's file.
func withUserKinds(t table, from string) (table, error) {
	user, path, err := readUserKinds()
	if err != nil {
		return table{}, err
	}

	for _, s := range user {
		if slices.ContainsFunc(t.kinds, func(r proofread.KindSpec) bool { return r.Name == s.Name }) {
			return table{}, fmt.Errorf("kind %q is declared in both %s and %s: rename one", s.Name, from, path)
		}
	}

	t.kinds = append(t.kinds, user...)

	return t, nil
}
