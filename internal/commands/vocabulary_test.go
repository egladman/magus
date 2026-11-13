package commands

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/libs/clikit/command"
	"github.com/egladman/magus/libs/clikit/generate"
)

// registryFlags is every flag name the tree declares as its own spelling: an alias
// and a one-character short are second ways to type a flag, not concepts.
func registryFlags() []string {
	seen := map[string]bool{}
	collect := func(flags []command.Flag) {
		for _, f := range flags {
			if f.AliasOf == "" && len(f.Name) > 1 {
				seen[f.Name] = true
			}
		}
	}
	collect(Root.Flags)
	command.Walk(Root, func(_ string, c command.Command) { collect(c.Flags) })
	names := make([]string, 0, len(seen))
	for n := range seen {
		names = append(names, n)
	}
	slices.Sort(names)
	return names
}

func TestRegistryFlagsPassTheGeneratorRefusals(t *testing.T) {
	if err := command.Check(Root); err != nil {
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
		key := fmt.Sprintf("%s:%s", column, conceptKey(spelling))
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

// flagUnitSuffixes are quantity units that belong on the VALUE of a command.Duration
// flag (5m, 420ms), never as a suffix of the flag's name. An Int counted in that
// unit (--tool-ms 420) is the anti-pattern this refuses.
var flagUnitSuffixes = []string{
	"-ms", "-ns", "-us", "-µs",
	"-sec", "-secs", "-second", "-seconds",
	"-min", "-mins", "-minute", "-minutes",
	"-hr", "-hrs", "-hour", "-hours",
}

func TestNoFlagNameEncodesAUnit(t *testing.T) {
	for _, name := range registryFlags() {
		for _, unit := range flagUnitSuffixes {
			if strings.HasSuffix(name, unit) {
				t.Errorf("flag --%s encodes unit %q in its name: use command.Duration and a unit-free name (e.g. --timeout 5m), not an Int counted in that unit", name, unit)
			}
		}
	}
}

type effectSuite struct{ suite.Suite }

func TestEffect(t *testing.T) { suite.Run(t, new(effectSuite)) }

// A new leaf cannot reach a reader of the tree unclassified.
func (s *effectSuite) TestEveryLeafDeclaresAnEffect() {
	command.Walk(Root, func(path string, c command.Command) {
		if len(c.Children) == 0 {
			s.NotEmpty(c.Effect, "magus %s declares no Effect: set one of %v", path, command.Effects)
		}
	})
}

func camel(flag string) string {
	words := strings.Split(flag, "-")
	for i := 1; i < len(words); i++ {
		words[i] = strings.ToUpper(words[i][:1]) + words[i][1:]
	}
	return strings.Join(words, "")
}
