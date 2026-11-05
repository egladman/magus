package pulls

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/egladman/magus/internal/file"
	pullsv1 "github.com/egladman/magus/proto/gen/go/magus/pulls/v1alpha1"
)

// authorEntry is one line of authors.jsonl. An untracked line ends its
// login; the empty login marks a saved filter that tracks nobody.
type authorEntry struct {
	Login   string    `json:"login"`
	Tracked bool      `json:"tracked"`
	At      time.Time `json:"at"`
}

// authorFilter is the saved author filter. Before anything is saved it is not
// Saved and the board shows the viewer's own pulls.
type authorFilter struct {
	Saved   bool
	Authors []string
}

// Effective returns the logins the board shows, or nil for every author.
func (f authorFilter) Effective(viewer string) []string {
	switch {
	case !f.Saved && viewer == "":
		return nil
	case !f.Saved:
		return []string{viewer}
	case len(f.Authors) == 0:
		return nil
	default:
		return f.Authors
	}
}

func (f authorFilter) proto(viewer string) *pullsv1.AuthorFilter {
	return &pullsv1.AuthorFilter{
		Authors:     f.Effective(viewer),
		IsDefault:   !f.Saved,
		Viewer:      viewer,
		EveryAuthor: f.Saved && len(f.Authors) == 0,
	}
}

// Store keeps the author filter in a JSON Lines file under the state dir.
type Store struct {
	path  string
	lines file.JSONL[authorEntry]
	now   func() time.Time
}

// OpenStore returns the store at path. The file is created by the first
// Set, and its absence is what makes the filter the default.
func OpenStore(path string) Store {
	return Store{
		path: path,
		now:  time.Now,
		lines: file.JSONL[authorEntry]{
			Path: path,
			Key:  func(e authorEntry) string { return e.Login },
			Live: func(e authorEntry) bool { return e.Tracked },
		},
	}
}

// Filter reads the saved filter.
func (s Store) Filter() (authorFilter, error) {
	_, err := os.Stat(s.path)
	if errors.Is(err, fs.ErrNotExist) {
		return authorFilter{}, nil
	}
	if err != nil {
		return authorFilter{}, fmt.Errorf("pulls: stat authors: %w", err)
	}
	entries, err := s.lines.Load()
	if err != nil {
		return authorFilter{}, fmt.Errorf("pulls: read authors: %w", err)
	}
	f := authorFilter{Saved: true}
	for _, e := range entries {
		f.Authors = append(f.Authors, e.Login)
	}
	return f, nil
}

// Set replaces the tracked logins. An empty list tracks every author and
// still creates the file, so it reads as saved.
func (s Store) Set(ctx context.Context, authors []string) error {
	want := cleanLogins(authors)
	_, statErr := os.Stat(s.path)
	err := s.lines.Update(ctx, func(have []authorEntry) []authorEntry {
		at := s.now()
		var add []authorEntry
		tracked := map[string]bool{}
		for _, e := range have {
			tracked[e.Login] = true
			if !slices.Contains(want, e.Login) {
				add = append(add, authorEntry{Login: e.Login, At: at})
			}
		}
		for _, login := range want {
			if !tracked[login] {
				add = append(add, authorEntry{Login: login, Tracked: true, At: at})
			}
		}
		if len(add) == 0 && errors.Is(statErr, fs.ErrNotExist) {
			add = append(add, authorEntry{At: at})
		}
		return add
	})
	if err != nil {
		return fmt.Errorf("pulls: write authors: %w", err)
	}
	return nil
}

func cleanLogins(in []string) []string {
	var out []string
	for _, l := range in {
		l = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(l), "@"))
		if l != "" && !slices.Contains(out, l) {
			out = append(out, l)
		}
	}
	return out
}
