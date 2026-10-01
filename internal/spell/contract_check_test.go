package spell

import (
	"context"
	"io/fs"
	"path"
	"testing"

	"github.com/egladman/magus/libs/gopherbuzz"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCheckContract(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string // "" when the source conforms
	}{
		{
			name: "every annotation matches",
			src: `export fun mgs_getName() > str { return "x"; }
export fun mgs_getLanguage() > Language { return Language{name = "x"}; }
export fun mgs_getTools() > {str:Tool} { return {}; }
export fun mgs_listTargets() > {str: fun(Target) Command} { return {}; }`,
		},
		{
			name: "an unexported or non-mgs function is not the contract's business",
			src: `export fun mgs_getName() > str { return "x"; }
fun mgs_helper() > any { return null; }
export fun build() > any { return null; }`,
		},
		{
			name: "any is a mismatch",
			src: `export fun mgs_getName() > str { return "x"; }
export fun mgs_listTargets() > any { return {}; }`,
			want: "line 2: mgs_listTargets declares > any; the contract wants > {str: fun(Target) Command}",
		},
		{
			name: "an optional where the contract wants a value",
			src:  `export fun mgs_getLanguage() > Language? { return null; }`,
			want: "line 1: mgs_getLanguage declares > Language?; the contract wants > Language",
		},
		{
			name: "no annotation",
			src:  `export fun mgs_isOpaque() { return true; }`,
			want: "line 1: mgs_isOpaque declares no return type; the contract wants > bool",
		},
		{
			name: "parameters",
			src:  `export fun mgs_getName(dir: str) > str { return dir; }`,
			want: "line 1: mgs_getName takes parameters; a contract function takes none",
		},
		{
			name: "an mgs_ name the contract does not list",
			src:  `export fun mgs_getVersionProbe() > [str] { return ["node", "--version"]; }`,
			want: "line 1: mgs_getVersionProbe is not a spell contract function",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := checkContract(tc.src)
			if tc.want == "" {
				require.NoError(t, err)
				return
			}
			require.ErrorIs(t, err, types.SpellContractViolated)
			assert.Contains(t, err.Error(), tc.want)
		})
	}

	t.Run("every violation is reported at once", func(t *testing.T) {
		err := checkContract(`export fun mgs_getTools() > any { return {}; }
export fun mgs_listClaimedGlobs() > [Path] { return []; }`)
		require.ErrorIs(t, err, types.SpellContractViolated)
		assert.Contains(t, err.Error(), "line 1: mgs_getTools declares > any")
		assert.Contains(t, err.Error(), "line 2: mgs_listClaimedGlobs is not a spell contract function")
	})
}

// The check is off unless a caller passes WithContractCheck: the same source resolves
// without it and is refused with it.
func TestResolveRunsTheContractCheckOnlyWhenAsked(t *testing.T) {
	const src = `export fun mgs_getName() > str { return "probe"; }
export fun mgs_getVersionProbe() > [str] { return ["node", "--version"]; }`
	ctx := context.Background()
	sess := buzz.NewSession(ctx, buzz.WithEmbedded())
	defer sess.Close()
	sess.SetModuleDecls(SpellModulePath, SpellModuleSource)
	require.NoError(t, sess.Exec(ctx, src))

	spec, err := Resolve(ctx, sess)
	require.NoError(t, err)
	assert.Equal(t, "probe", spec.Name)

	_, err = Resolve(ctx, sess, WithContractCheck(src))
	require.ErrorIs(t, err, types.SpellContractViolated)
	assert.Contains(t, err.Error(), "mgs_getVersionProbe is not a spell contract function")
}

// shippedContractViolations are the shipped spells that do not conform yet, each with
// why. An entry here fails once its spell conforms, so the list only shrinks; it is empty
// when WithContractCheck can be passed on every load.
var shippedContractViolations = map[string]string{
	"typescript":      "mgs_listTargets returns > any: its map mixes commands and services until mgs_listServices splits them",
	"podman":          "mgs_listTargets returns > any: its map mixes commands and services until mgs_listServices splits them",
	"experimental/nx": "mgs_listTargets returns > any: its map mixes commands and services until mgs_listServices splits them",
}

func TestShippedSpellsMatchTheContract(t *testing.T) {
	checked := 0
	err := fs.WalkDir(spells.Shipped(), ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || d.Name() != "spell.buzz" {
			return err
		}
		dir := path.Dir(p)
		src, err := fs.ReadFile(spells.Shipped(), p)
		require.NoError(t, err)
		checked++
		cerr := checkContract(string(src))
		if why, known := shippedContractViolations[dir]; known {
			assert.ErrorIs(t, cerr, types.SpellContractViolated,
				"spells/%s conforms now; drop it from shippedContractViolations (%s)", dir, why)
			return nil
		}
		assert.NoError(t, cerr, "spells/%s", dir)
		return nil
	})
	require.NoError(t, err)
	require.Positive(t, checked)
}
