package proofread

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"

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
func readUserKinds() (kinds []KindSpec, path string, err error) {
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
		Kinds map[string]KindDecl `json:"kinds"`
	}

	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()

	if err := dec.Decode(&raw); err != nil {
		return nil, path, fmt.Errorf("user kinds %s: %w", path, err)
	}

	kinds, err = DeclareKinds(raw.Kinds)
	if err != nil {
		return nil, path, fmt.Errorf("user kinds %s: %w", path, err)
	}

	return kinds, path, nil
}

// WithUserKinds appends the kinds of the user kinds file to kinds. A name
// both declare is an error naming both files; from is the file kinds came
// from.
func WithUserKinds(kinds []KindSpec, from string) ([]KindSpec, error) {
	user, path, err := readUserKinds()
	if err != nil {
		return nil, err
	}

	for _, s := range user {
		if slices.ContainsFunc(kinds, func(r KindSpec) bool { return r.Name == s.Name }) {
			return nil, fmt.Errorf("kind %q is declared in both %s and %s: rename one", s.Name, from, path)
		}
	}

	return append(kinds, user...), nil
}
