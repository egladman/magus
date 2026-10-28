package commands

import (
	"slices"
	"strings"
	"testing"

	"github.com/egladman/magus/libs/cmdline"
	"github.com/egladman/magus/libs/cmdline/generate"
)

// registryFlags is every flag name the tree declares as its own spelling: an alias
// and a one-character short are second ways to type a flag, not concepts.
func registryFlags() []string {
	seen := map[string]bool{}
	collect := func(flags []cmdline.Flag) {
		for _, f := range flags {
			if f.AliasOf == "" && len(f.Name) > 1 {
				seen[f.Name] = true
			}
		}
	}
	collect(Root.Flags)
	cmdline.Walk(Root, func(_ string, c cmdline.Command) { collect(c.Flags) })
	names := make([]string, 0, len(seen))
	for n := range seen {
		names = append(names, n)
	}
	slices.Sort(names)
	return names
}

func TestRegistryFlagsPassTheGeneratorRefusals(t *testing.T) {
	if err := cmdline.Check(Root); err != nil {
		t.Fatal(err)
	}
}

func TestEveryFlagRegistersItsConcept(t *testing.T) {
	known := map[string]bool{}
	for _, c := range vocabulary {
		known[c.Flag] = true
	}
	for _, name := range registryFlags() {
		if !known[name] {
			t.Errorf("flag --%s has no row in Vocabulary (internal/commands/vocabulary.go): register its concept, or reuse the existing spelling", name)
		}
	}
}

func TestNoVocabularyRowOutlivesItsFlag(t *testing.T) {
	declared := registryFlags()
	for _, c := range vocabulary {
		if !slices.Contains(declared, c.Flag) {
			t.Errorf("Vocabulary row %q names no flag the registry declares: delete the row", c.Flag)
		}
	}
}

func TestNoConceptHasTwoRows(t *testing.T) {
	byKey := map[string]string{}
	claim := func(column, spelling, flag string) {
		if spelling == "" {
			return
		}
		key := column + ":" + conceptKey(spelling)
		if prev, dup := byKey[key]; dup && prev != flag {
			t.Errorf("--%s and --%s are one concept under two names (%s %q): keep one", prev, flag, column, spelling)
		}
		byKey[key] = flag
	}
	for _, c := range vocabulary {
		claim("flag", c.Flag, c.Flag)
		claim("config", c.Config, c.Flag)
		claim("go", c.GoField, c.Flag)
		claim("buzz", c.BuzzField, c.Flag)
	}
}

// conceptKey folds case, separators and a leading no-, so --symbols and --no-symbols,
// or --dry-run and --dryRun, compare equal.
func conceptKey(s string) string {
	s = strings.ToLower(strings.NewReplacer("-", "", "_", "").Replace(s))
	return strings.TrimPrefix(s, "no")
}

func TestEveryColumnSpellsTheFlag(t *testing.T) {
	for _, c := range vocabulary {
		if want := strings.ReplaceAll(c.Flag, "-", "_"); c.Config != "" && c.Config != want {
			t.Errorf("--%s: magus.yaml key is %q, want %q", c.Flag, c.Config, want)
		}
		if want := generate.FieldName(c.Flag); c.GoField != "" && c.GoField != want {
			t.Errorf("--%s: Go option field is %q, want %q", c.Flag, c.GoField, want)
		}
		if want := camel(c.Flag); c.BuzzField != "" && c.BuzzField != want {
			t.Errorf("--%s: Buzz option field is %q, want %q", c.Flag, c.BuzzField, want)
		}
	}
}

func camel(flag string) string {
	words := strings.Split(flag, "-")
	for i := 1; i < len(words); i++ {
		words[i] = strings.ToUpper(words[i][:1]) + words[i][1:]
	}
	return strings.Join(words, "")
}
