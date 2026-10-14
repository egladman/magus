package knowledge

import (
	"cmp"
	"fmt"
	"maps"
	"path"
	"slices"
	"strings"

	"github.com/egladman/magus/types"
)

// sprawlMinFamily is the family size a subject must join: three members of one owner, or two
// carried by three owners. Three is also how many must share parameters, and how many owners
// must carry the family, for either to raise the score.
const sprawlMinFamily = 3

// sprawlMinPeers is how many members the subject's family must already hold, outside the
// change: a change joining a family is advice, one inventing a family is not.
const sprawlMinPeers = 2

// sprawlLexicon are the sets of complementary words a family varies in: outcomes of one
// event, ends of one lifecycle, modes of one access. Each word sits in one set. A name-only
// family is rarely sprawl (a parser's parse<X> per grammar rule), and on this tree the lexicon
// cut 840 name-only families to 47.
var sprawlLexicon = [][]string{
	{"hit", "miss", "error"},
	{"reuse", "eviction"},
	{"acquire", "release"},
	{"read", "write", "exec"},
	{"accepted", "declined"},
}

// sprawlWord maps each lexicon word to its set's index.
var sprawlWord = func() map[string]int {
	out := map[string]int{}
	for i, set := range sprawlLexicon {
		for _, w := range set {
			out[w] = i
		}
	}
	return out
}()

// Scores of sprawlCheck's finding: a subject joining a family starts below the default bar,
// and each of a shared parameter list, three carrying owners and an owner past its type-methods
// value adds a step.
const (
	sprawlBaseScore = 0.4
	sprawlStep      = 0.1
)

// sprawlCheck reports a method or function the change adds, renames or re-signs that joins a
// family of siblings: declarations of one owner (or the top-level functions of one scope)
// whose names differ in one word, that word drawn from one set of sprawlLexicon
// (RecordCacheHit, RecordCacheMiss, RecordCacheError). One member taking the word as a value
// usually replaces the family. A name of one word is never in a family: Read and Write are an
// interface's vocabulary, not a split.
//
// The family must hold sprawlMinFamily members, or two that sprawlMinFamily owners in the
// namespace carry, and sprawlMinPeers of its members across those owners must predate the
// change. A subject added to several carriers at once is reported once, on the first by ID.
// The score starts at 0.4 and gains 0.1 for each of: sprawlMinFamily members sharing one
// parameter list, sprawlMinFamily carrying owners, and an owner with more methods than the
// type-methods source value [types.ConformOptions.Effective] enforces. The default bar of 0.5
// therefore needs one of the three.
type sprawlCheck struct{}

func (sprawlCheck) name() string { return types.CheckSiblingSprawl }

func (sprawlCheck) run(x *namingIndex) []finding {
	subjects := map[*namingDecl]bool{}
	for _, s := range x.subjects {
		if s.callable() {
			subjects[s] = true
		}
	}
	if len(subjects) == 0 {
		return nil
	}
	wide, src := x.change.Conform.Effective(types.ConformKey{Metric: types.ConformTypeMethods, Population: types.ConformPopSource})
	reported := map[string]finding{}
	for _, f := range x.siblingFamilies(x.callables) {
		kin := x.kin(f)
		carriers := x.carriers(f)
		if len(f.Members) < sprawlMinFamily && carriers < sprawlMinFamily {
			continue
		}
		shared := sharedKey(f)
		methods := 0
		if f.Owner != "" {
			for _, d := range x.callables {
				if d.scope == f.Members[0].scope && d.owner == f.Owner && d.shape.Kind == declMethod {
					methods++
				}
			}
		}
		for _, s := range f.Members {
			if !subjects[s] {
				continue
			}
			peers := 0
			for _, d := range kin {
				if d != s && !x.change.Introduced[d.id] {
					peers++
				}
			}
			if peers < sprawlMinPeers {
				continue
			}
			score := sprawlBaseScore
			details := []string{"members: " + strings.Join(labels(f.Members), ", ")}
			if len(shared) >= sprawlMinFamily {
				score += sprawlStep
				details = append(details, fmt.Sprintf("%d share the parameters %s: %s",
					len(shared), shared[0].shape.Key, strings.Join(labels(shared), ", ")))
			}
			if carriers >= sprawlMinFamily {
				score += sprawlStep
				details = append(details, fmt.Sprintf("%d types carry it: %s", carriers, strings.Join(kinOwners(kin), ", ")))
			}
			if src != "" && methods > wide {
				score += sprawlStep
				details = append(details, fmt.Sprintf("`%s` has %d methods, past the %s of %d for type-methods source",
					f.Owner, methods, thresholdSource(src), wide))
			}
			key := f.Members[0].language + "\x00" + f.Members[0].scope + "\x00" + f.Stem + "\x00" + s.folded[f.Slot]
			if prev, ok := reported[key]; ok && prev.subject <= s.id {
				continue
			}
			reported[key] = finding{
				subject: s.id,
				score:   score,
				message: fmt.Sprintf("`%s` joins `%s` %s: %d members differ in that one word (%s)",
					s.label, f.Stem, familyPlace(f), len(f.Members), strings.Join(f.Words, ", ")),
				details: details,
			}
		}
	}
	return slices.SortedFunc(maps.Values(reported), func(a, b finding) int {
		return cmp.Or(cmp.Compare(a.subject, b.subject), cmp.Compare(a.message, b.message))
	})
}

// siblingFamily is the members of one owner, or the top-level functions of one scope when
// Owner is empty, whose names differ only in the word at Slot, each such word in one
// sprawlLexicon set. Stem is the name as the first member spells it with <X> at Slot; Words
// are the folded slot words, sorted; Members are in ID order.
type siblingFamily struct {
	Owner, Stem string
	Slot        int
	Words       []string
	Members     []*namingDecl
}

// siblingFamilies groups ds into families of two or more distinct slot words, in the order of
// their first member's ID, then Slot.
func (x *namingIndex) siblingFamilies(ds []*namingDecl) []siblingFamily {
	type familyKey struct {
		language, scope, owner, masked string
		slot, set                      int
	}
	groups := map[familyKey][]*namingDecl{}
	for _, d := range ds {
		if len(d.folded) < 2 {
			continue
		}
		for slot, w := range d.folded {
			set, ok := sprawlWord[w]
			if !ok {
				continue
			}
			k := familyKey{language: d.language, scope: d.scope, owner: d.owner, masked: maskedWords(d.folded, slot), slot: slot, set: set}
			groups[k] = append(groups[k], d)
		}
	}
	var out []siblingFamily
	for k, members := range groups {
		var words []string
		for _, m := range members {
			if !slices.Contains(words, m.folded[k.slot]) {
				words = append(words, m.folded[k.slot])
			}
		}
		if len(words) < 2 {
			continue
		}
		slices.Sort(words)
		slices.SortFunc(members, func(a, b *namingDecl) int { return cmp.Compare(a.id, b.id) })
		w := members[0].words[k.slot]
		out = append(out, siblingFamily{
			Owner: k.owner, Stem: members[0].label[:w.start] + "<X>" + members[0].label[w.end:],
			Slot: k.slot, Words: words, Members: members,
		})
	}
	slices.SortFunc(out, func(a, b siblingFamily) int {
		return cmp.Or(cmp.Compare(a.Members[0].id, b.Members[0].id), cmp.Compare(a.Slot, b.Slot))
	})
	return out
}

// maskedWords joins words with the one at slot replaced by a wildcard.
func maskedWords(words []string, slot int) string {
	masked := slices.Clone(words)
	masked[slot] = "*"
	return strings.Join(masked, " ")
}

// sharedKey is the largest subset of f's members declaring one parameter shape, the lower
// shape on a tie, or nil when no member's language renders one.
func sharedKey(f siblingFamily) []*namingDecl {
	byKey := map[string][]*namingDecl{}
	for _, m := range f.Members {
		if m.shape.Key != "" {
			byKey[m.shape.Key] = append(byKey[m.shape.Key], m)
		}
	}
	var best []*namingDecl
	for _, k := range slices.Sorted(maps.Keys(byKey)) {
		if len(byKey[k]) > len(best) {
			best = byKey[k]
		}
	}
	return best
}

// kin is every callable in f's scope that carries f's stem with a word of f's set: f's own
// members and those of the same family on every other owner, in ID order.
func (x *namingIndex) kin(f siblingFamily) []*namingDecl {
	first := f.Members[0]
	masked, set := maskedWords(first.folded, f.Slot), sprawlWord[f.Words[0]]
	var out []*namingDecl
	for _, d := range x.callables {
		if d.language != first.language || d.scope != first.scope || (d.owner == "") != (f.Owner == "") ||
			len(d.folded) != len(first.folded) || maskedWords(d.folded, f.Slot) != masked {
			continue
		}
		if s, ok := sprawlWord[d.folded[f.Slot]]; ok && s == set {
			out = append(out, d)
		}
	}
	return out
}

// carriers is how many owners in f's namespace hold two or more of f's words on its stem: the
// interface and implementers that repeat one family. Top-level functions are one carrier.
func (x *namingIndex) carriers(f siblingFamily) int {
	words := map[string]map[string]bool{}
	for _, d := range x.kin(f) {
		if words[d.owner] == nil {
			words[d.owner] = map[string]bool{}
		}
		words[d.owner][d.folded[f.Slot]] = true
	}
	n := 0
	for _, ws := range words {
		if len(ws) >= 2 {
			n++
		}
	}
	return n
}

// kinOwners names the distinct owners among kin, sorted, up to conformanceExamples.
func kinOwners(kin []*namingDecl) []string {
	var out []string
	for _, d := range kin {
		if !slices.Contains(out, d.owner) {
			out = append(out, d.owner)
		}
	}
	slices.Sort(out)
	return capList(out, conformanceExamples)
}

// familyPlace says where f lives: on its owner, or in its scope's directory.
func familyPlace(f siblingFamily) string {
	if f.Owner != "" {
		return "on `" + f.Owner + "`"
	}
	return "in " + path.Dir(f.Members[0].file())
}
