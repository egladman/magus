package config

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/egladman/magus/types"
)

// UseEntry is one magus.yaml `use` entry: another workspace this one reads
// rules from. ward.ResolveUses resolves it at load.
type UseEntry struct {
	// Name is the handle use references carry, [a-z0-9-]+ and unique.
	Name string `json:"name" yaml:"name"`
	// Path is the used workspace's root, absolute or relative to the
	// directory holding magus.yaml.
	Path string `json:"path" yaml:"path"`
	// Share names the kinds this entry lends. Empty takes every kind magus
	// implements (types.ShareKind.Values).
	Share []types.ShareKind `json:"share" yaml:"share"`
}

// Kinds is the entry's share list, every implemented kind when it names none.
func (e UseEntry) Kinds() []types.ShareKind {
	if len(e.Share) > 0 {
		return e.Share
	}
	var all []types.ShareKind
	for _, k := range types.ShareKind("").Values() {
		all = append(all, types.ShareKind(k))
	}
	return all
}

var useName = regexp.MustCompile(`^[a-z0-9-]+$`)

// useFailures checks each name, path and share kind. One kind shared by
// two entries is refused: two owners would need a merge, and core merges
// no tool config.
func useFailures(entries []UseEntry) []FieldFailure {
	var out []FieldFailure
	names := map[string]bool{}
	owner := map[types.ShareKind]string{}
	for i, e := range entries {
		field := fmt.Sprintf("use[%d]", i)
		switch {
		case !useName.MatchString(e.Name):
			out = append(out, FieldFailure{Field: field + ".name", Tag: "use_name", Value: e.Name})
		case names[e.Name]:
			out = append(out, FieldFailure{Field: field + ".name", Tag: "unique", Param: "name", Value: e.Name})
		}
		names[e.Name] = true
		if e.Path == "" {
			out = append(out, FieldFailure{Field: field + ".path", Tag: "required"})
		}
		for j, k := range e.Kinds() {
			kind := fmt.Sprintf("%s.share[%d]", field, j)
			if !k.Valid() {
				out = append(out, FieldFailure{Field: kind, Tag: "use_share_kind",
					Param: strings.Join(k.Values(), " "), Value: string(k)})
				continue
			}
			if prior, taken := owner[k]; taken {
				out = append(out, FieldFailure{Field: kind, Tag: "use_share_owner", Param: prior, Value: string(k)})
				continue
			}
			owner[k] = e.Name
		}
	}
	return out
}
