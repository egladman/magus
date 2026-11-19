package api

import "slices"

var names []string

type Store struct {
	items []string
	index map[string]int
	inner struct{ rows []int }
}

type hidden struct{ items []string }

func (h *hidden) Items() []string { return h.items }

func (s *Store) Items() []string { return s.items } // want `Store.Items returns its own slice s.items`

func (s Store) Index() map[string]int { return s.index } // want `Store.Index returns its own map s.index`

func (s *Store) Tail() ([]string, error) {
	return s.items[1:], nil // want `Store.Tail returns its own slice s.items`
}

func (s *Store) Rows() []int { return (s.inner.rows) } // want `Store.Rows returns its own slice s.inner.rows`

func Names() []string { return names } // want `Names returns its own slice names`

func (s *Store) Copy() []string { return slices.Clone(s.items) }

func (s *Store) Count() int { return len(s.items) }

func (s *Store) Late() func() []string { return func() []string { return s.items } }

func (s *Store) Param(other *Store) []string { return other.items }

func (s *Store) Listed() []string { return s.items }

func (s *Store) Fixed() []string { return slices.Clone(s.items) }

func lower(s *Store) []string { return s.items }
