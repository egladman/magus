package job

import (
	"math/rand"
	"reflect"
	"testing"
	"testing/quick"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/libs/codec"
)

// validGrants is every grant Valid accepts: console none/read/write crossed with tokens and
// mcp each none/write. Eighteen, few enough to check every pair and triple outright.
func validGrants() []Access {
	var out []Access
	for _, tok := range []Level{LevelNone, LevelWrite} {
		for _, mcp := range []Level{LevelNone, LevelWrite} {
			for _, con := range []Level{LevelNone, LevelRead, LevelWrite} {
				out = append(out, Access{Tokens: tok, MCP: mcp, Console: con})
			}
		}
	}
	return out
}

// Generate makes quick draw only valid grants, so the laws below are about the lattice magus
// uses rather than about levels Valid refuses.
func (Access) Generate(r *rand.Rand, _ int) reflect.Value {
	all := validGrants()
	return reflect.ValueOf(all[r.Intn(len(all))])
}

// Generate draws any need over the three scopes and the three levels.
func (Need) Generate(r *rand.Rand, _ int) reflect.Value {
	scopes := []Scope{ScopeTokens, ScopeMCP, ScopeConsole}
	return reflect.ValueOf(Need{Scope: scopes[r.Intn(3)], Level: Level(r.Intn(3))})
}

func implies(p, q bool) bool { return !p || q }

func TestGrantLatticeLaws(t *testing.T) {
	t.Parallel()
	cfg := &quick.Config{MaxCount: 2000}
	laws := map[string]any{
		"reflexive": func(a Access) bool { return a.Within(a) },
		"antisymmetric": func(a, b Access) bool {
			return implies(a.Within(b) && b.Within(a), a == b)
		},
		"transitive": func(a, b, c Access) bool {
			return implies(a.Within(b) && b.Within(c), a.Within(c))
		},
		// Anything a narrower grant reaches, a wider one reaches too.
		"allows is monotone": func(a, b Access, n Need) bool {
			return implies(a.Within(b) && a.Allows(n), b.Allows(n))
		},
		"operator is the top":   func(a Access) bool { return a.Within(AccessOperator) },
		"nothing is the bottom": func(a Access) bool { return Access{}.Within(a) },
		"json round-trips": func(a Access) bool {
			b, err := codec.Marshal(a)
			if err != nil {
				return false
			}
			var back Access
			return codec.Unmarshal(b, &back) == nil && back == a
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

func TestGrantValidateRefusesLevelsAScopeHasNoMeaningFor(t *testing.T) {
	t.Parallel()
	for _, g := range validGrants() {
		require.NoError(t, g.Validate(), g.String())
	}
	for _, g := range []Access{{Tokens: LevelRead}, {MCP: LevelRead}, {Console: 3}, {Tokens: 9}} {
		assert.Error(t, g.Validate(), "%+v", g)
	}
}

// A guard built on a zero or invalid need would admit every credential, so Validate refuses
// each: none is not a need, and a scope or level magus does not know is not one either.
func TestNeedValidateRefusesZeroAndInvalid(t *testing.T) {
	t.Parallel()
	for _, n := range []Need{
		{Scope: ScopeTokens, Level: LevelWrite},
		{Scope: ScopeMCP, Level: LevelWrite},
		{Scope: ScopeConsole, Level: LevelRead},
		{Scope: ScopeConsole, Level: LevelWrite},
	} {
		require.NoError(t, n.Validate(), "%+v", n)
	}
	for _, n := range []Need{
		{},
		{Scope: ScopeConsole},
		{Scope: "files", Level: LevelWrite},
		{Scope: ScopeMCP, Level: LevelRead},
		{Scope: ScopeConsole, Level: 7},
	} {
		assert.Error(t, n.Validate(), "%+v", n)
	}
}

// The presets are hand-pinned: a change to any of them is a change to what a minted token can
// do, and must show in a diff. A share link holds AccessViewer, so it and a viewer see the same
// routes.
func TestGrantPresets(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "tokens=write,mcp=write,console=write", AccessOperator.String())
	assert.Equal(t, "mcp=write", AccessConnector.String())
	assert.Equal(t, "console=write", AccessConsole.String())
	assert.Equal(t, "console=read", AccessViewer.String())
	assert.Equal(t, "", Access{}.String())
}

// A stored grant reads as names, not numbers, and a record naming an unknown level fails to
// decode rather than reading as none.
func TestGrantJSONUsesLevelNames(t *testing.T) {
	t.Parallel()
	b, err := codec.Marshal(AccessConnector)
	require.NoError(t, err)
	assert.JSONEq(t, `{"mcp":"write"}`, string(b))
	var g Access
	require.NoError(t, codec.Unmarshal([]byte(`{"console":"read"}`), &g))
	assert.Equal(t, AccessViewer, g)
	assert.Error(t, codec.Unmarshal([]byte(`{"console":"admin"}`), &g))
}

func TestCredentialPhrase(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "", Credential{}.Phrase())
	assert.Equal(t, "the operator token", Credential{Kind: KindOperator, ID: "0badf00d"}.Phrase())
	assert.Equal(t, "token laptop (3fa9c1d2)", Credential{Kind: KindStored, ID: "3fa9c1d2", Name: "laptop"}.Phrase())
	assert.Equal(t, "share link 9b2e04aa", Credential{Kind: KindShare, ID: "9b2e04aa"}.Phrase())
	assert.Equal(t, "link code 51c0de00", Credential{Kind: KindExchange, ID: "51c0de00", Name: "console-1"}.Phrase())
	assert.Equal(t, "stdio", CredentialStdio.Phrase())
	assert.Equal(t, "the socket's owner", CredentialSocketPeer.Phrase())
}
