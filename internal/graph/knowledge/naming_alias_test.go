package knowledge

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/types"
)

// aliasUser is a function the change touches in an importer file of aliasFixture.
func aliasUser(file string) types.KnowledgeSymbol {
	return couplingFunction("/internal/use", "Use"+file[len("internal/use/"):len(file)-len(".go")], file, 5, 9, "func Use()", nil)
}

func TestAliasCheckReportsAnAliasedImport(t *testing.T) {
	for _, tc := range []struct {
		name, file, message string
		past                []string
	}{
		{
			name:    "a clash blames the lesser package",
			file:    "internal/use/q00.go",
			message: "internal/queue/types shares the package name types with types, and 62% of its 13 importers import both: consider renaming internal/queue/types",
			past: []string{
				"name-clash source: 62, past the configured anchor of 25",
				"alias-share source: 38, past the configured anchor of 30",
			},
		},
		{
			name:    "one spelling names the package",
			file:    "internal/use/r00.go",
			message: "internal/spell/remote is aliased by 6 of its 6 importers, always as remotespell: consider naming the package remotespell",
			past:    []string{"alias-share source: 100, past the configured anchor of 30"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			use := aliasUser(tc.file)
			g := aliasGraph(use)

			got := g.ConformanceChecks(ConformanceChange{Subjects: []string{symID(use)}, Conformance: aliasOptions()})

			require.Len(t, got[symID(use)], 1)
			c := got[symID(use)][0]
			n := len(c.Details)
			require.Greater(t, n, len(tc.past))
			c.Details = c.Details[n-len(tc.past)-1:]
			assert.Equal(t, types.Check{
				Name:     types.CheckImportAlias,
				Status:   types.CheckAdvice,
				Evidence: types.EvidenceInferred,
				Message:  tc.message,
				Details:  append(tc.past, "rename the package, or record a known entry with the reason"),
			}, c)
		})
	}
}

func TestAliasCheckNamesMixedSpellings(t *testing.T) {
	f := types.AliasFacts{Dir: "internal/proc/run", Name: "run", Importers: 43, Aliased: 21, Needed: 21,
		Aliases: map[string]int{"procrun": 13, "runPkg": 7, "run": 1}}
	assert.Equal(t, "internal/proc/run is aliased by 21 of its 43 importers in 3 spellings (procrun, runPkg, run): pick one name", aliasMessage(f))
}

func TestAliasCheckSkipsKnownPackages(t *testing.T) {
	use := aliasUser("internal/use/q00.go")
	o := aliasOptions()
	o.Ratchet = types.ConformanceRatchet{Schema: 1, Known: []types.ConformanceKnown{
		{Metric: types.ConformanceNameClash, Items: []string{"internal/queue/types"}, Reason: "rename pending"},
		{Metric: types.ConformanceAliasShare, Items: []string{"internal/queue/types"}, Reason: "rename pending"},
	}}

	assert.Empty(t, aliasGraph(use).ConformanceChecks(ConformanceChange{Subjects: []string{symID(use)}, Conformance: o}))
}

// A new package is the cheapest one to rename: one taking an existing package's clause is
// named the moment it lands.
func TestAliasCheckReportsANewPackageTakingAName(t *testing.T) {
	fresh := couplingFunction("/internal/newq", "Enqueue", "internal/newq/newq.go", 5, 9, "func Enqueue()", nil)
	pkg := aliasPkg("/internal/newq", "types", []string{"internal/newq/newq.go"}, nil)
	g := aliasGraph(pkg, fresh)

	got := g.ConformanceChecks(ConformanceChange{Subjects: []string{symID(fresh)}, Introduced: introduced(fresh), Conformance: aliasOptions()})

	assert.Equal(t, map[string][]types.Check{symID(fresh): {advice(types.CheckImportAlias,
		"internal/newq takes the package name types, which internal/queue/types (13 importers), types (20 importers) already declares: "+
			"consider another name before anything imports both",
	)}}, got)

	assert.Empty(t, g.ConformanceChecks(ConformanceChange{Subjects: []string{symID(fresh)}, Conformance: aliasOptions()}),
		"a package that already existed is the alias-share check's to judge")
}

func TestAliasCheckIsSilentWithoutASymbolIndex(t *testing.T) {
	assert.Empty(t, NewGraph().ConformanceChecks(ConformanceChange{Subjects: []string{"symbol:x"}, Conformance: aliasOptions()}))
}
