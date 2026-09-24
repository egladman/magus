package types

import (
	"math/rand"
	"reflect"
	"testing"
	"testing/quick"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	json "github.com/egladman/magus/internal/json"
)

// validGrants is every grant Valid accepts: console none/read/write crossed with tokens and
// mcp each none/write. Eighteen, few enough to check every pair and triple outright.
func validGrants() []Grant {
	var out []Grant
	for _, tok := range []Level{LevelNone, LevelWrite} {
		for _, mcp := range []Level{LevelNone, LevelWrite} {
			for _, con := range []Level{LevelNone, LevelRead, LevelWrite} {
				out = append(out, Grant{Tokens: tok, MCP: mcp, Console: con})
			}
		}
	}
	return out
}

// Generate makes quick draw only valid grants, so the laws below are about the lattice magus
// uses rather than about levels Valid refuses.
func (Grant) Generate(r *rand.Rand, _ int) reflect.Value {
	all := validGrants()
	return reflect.ValueOf(all[r.Intn(len(all))])
}

// Generate draws any need over the three surfaces and the three levels.
func (Need) Generate(r *rand.Rand, _ int) reflect.Value {
	surfaces := []Surface{SurfaceTokens, SurfaceMCP, SurfaceConsole}
	return reflect.ValueOf(Need{Surface: surfaces[r.Intn(3)], Level: Level(r.Intn(3))})
}

func implies(p, q bool) bool { return !p || q }

func TestGrantLatticeLaws(t *testing.T) {
	t.Parallel()
	cfg := &quick.Config{MaxCount: 2000}
	laws := map[string]any{
		"reflexive": func(a Grant) bool { return a.Within(a) },
		"antisymmetric": func(a, b Grant) bool {
			return implies(a.Within(b) && b.Within(a), a == b)
		},
		"transitive": func(a, b, c Grant) bool {
			return implies(a.Within(b) && b.Within(c), a.Within(c))
		},
		// Anything a narrower grant reaches, a wider one reaches too.
		"allows is monotone": func(a, b Grant, n Need) bool {
			return implies(a.Within(b) && a.Allows(n), b.Allows(n))
		},
		"operator is the top":   func(a Grant) bool { return a.Within(GrantOperator) },
		"nothing is the bottom": func(a Grant) bool { return Grant{}.Within(a) },
		"json round-trips": func(a Grant) bool {
			b, err := json.Marshal(a)
			if err != nil {
				return false
			}
			var back Grant
			return json.Unmarshal(b, &back) == nil && back == a
		},
	}
	for name, law := range laws {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			require.NoError(t, quick.Check(law, cfg))
		})
	}
}

// The laws above are checked on a sample; the lattice is small enough to check whole, and the
// exhaustive walk is what makes a wrong Within fail every time rather than usually.
func TestGrantWithinIsPointwiseOnEveryPair(t *testing.T) {
	t.Parallel()
	for _, a := range validGrants() {
		for _, b := range validGrants() {
			want := a.Tokens <= b.Tokens && a.MCP <= b.MCP && a.Console <= b.Console
			assert.Equal(t, want, a.Within(b), "%s within %s", a, b)
		}
	}
}

func TestGrantValidateRefusesLevelsASurfaceHasNoMeaningFor(t *testing.T) {
	t.Parallel()
	for _, g := range validGrants() {
		assert.NoError(t, g.Validate(), g.String())
	}
	for _, g := range []Grant{{Tokens: LevelRead}, {MCP: LevelRead}, {Console: 3}, {Tokens: 9}} {
		assert.Error(t, g.Validate(), "%+v", g)
	}
}

// A guard built on a zero or invalid need would admit every credential, so Validate refuses
// each: none is not a need, and a surface or level magus does not know is not one either.
func TestNeedValidateRefusesZeroAndInvalid(t *testing.T) {
	t.Parallel()
	for _, n := range []Need{
		{Surface: SurfaceTokens, Level: LevelWrite},
		{Surface: SurfaceMCP, Level: LevelWrite},
		{Surface: SurfaceConsole, Level: LevelRead},
		{Surface: SurfaceConsole, Level: LevelWrite},
	} {
		assert.NoError(t, n.Validate(), "%+v", n)
	}
	for _, n := range []Need{
		{},
		{Surface: SurfaceConsole},
		{Surface: "files", Level: LevelWrite},
		{Surface: SurfaceMCP, Level: LevelRead},
		{Surface: SurfaceConsole, Level: 7},
	} {
		assert.Error(t, n.Validate(), "%+v", n)
	}
}

// The presets are hand-pinned: a change to any of them is a change to what a minted token can
// do, and must show in a diff. A share link holds GrantViewer, so it and a viewer see the same
// routes.
func TestGrantPresets(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "tokens=write,mcp=write,console=write", GrantOperator.String())
	assert.Equal(t, "mcp=write", GrantConnector.String())
	assert.Equal(t, "console=write", GrantConsole.String())
	assert.Equal(t, "console=read", GrantViewer.String())
	assert.Equal(t, "", Grant{}.String())
}

// A stored grant reads as names, not numbers, and a record naming an unknown level fails to
// decode rather than reading as none.
func TestGrantJSONUsesLevelNames(t *testing.T) {
	t.Parallel()
	b, err := json.Marshal(GrantConnector)
	require.NoError(t, err)
	assert.JSONEq(t, `{"mcp":"write"}`, string(b))
	var g Grant
	require.NoError(t, json.Unmarshal([]byte(`{"console":"read"}`), &g))
	assert.Equal(t, GrantViewer, g)
	assert.Error(t, json.Unmarshal([]byte(`{"console":"admin"}`), &g))
}

func TestCredentialPhrase(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "", Credential{}.Phrase())
	assert.Equal(t, "the operator token", Credential{Class: ClassOperator, ID: "0badf00d"}.Phrase())
	assert.Equal(t, "token laptop (3fa9c1d2)", Credential{Class: ClassStored, ID: "3fa9c1d2", Name: "laptop"}.Phrase())
	assert.Equal(t, "share link 9b2e04aa", Credential{Class: ClassShare, ID: "9b2e04aa"}.Phrase())
	assert.Equal(t, "link code 51c0de00", Credential{Class: ClassExchange, ID: "51c0de00", Name: "console-1"}.Phrase())
	assert.Equal(t, "stdio", CredentialStdio.Phrase())
	assert.Equal(t, "the MCP socket's owner", CredentialSocketPeer.Phrase())
}
