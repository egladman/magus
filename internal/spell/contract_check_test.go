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

type contractCase struct {
	name string
	src  string
	want string // "" when the source conforms
}

func runContractCases(t *testing.T, cases []contractCase) {
	t.Helper()
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
}

func TestCheckContract(t *testing.T) {
	runContractCases(t, []contractCase{
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
	})
}

func TestCheckContractProviderOps(t *testing.T) {
	runContractCases(t, []contractCase{
		{
			name: "a provider op takes its Target and its request",
			src: `export fun mgs_getName() > str { return "x"; }
export fun resolve_secret(t: Target, ask: SecretRef) > Secret { return Secret{value = ask.ref}; }
export fun list_lifecycles(target: Target, req: LifecycleQuery) > [Lifecycle] { return []; }`,
		},
		{
			name: "a provider op still on the input callback",
			src:  `export fun get_artifact(target: Target, cb: fun(any)) > bool { return false; }`,
			want: "line 1: get_artifact takes (Target, fun(any)); the contract wants (target: Target, req: ArtifactRequest)",
		},
		{
			name: "a provider op returning the bare value",
			src:  `export fun resolve_secret(target: Target, req: SecretRef) > str { return ""; }`,
			want: "line 1: resolve_secret declares > str; the contract wants > Secret",
		},
	})
}

func TestCheckContractHarnessFunctions(t *testing.T) {
	runContractCases(t, []contractCase{
		{
			name: "a harness function takes the arguments the harness contract names",
			src: `export fun mgs_getName() > str { return "x"; }
export fun mgs_getHarnessConfig() > HarnessConfig { return HarnessConfig{path = ".x/hooks.json"}; }
export fun mgs_renderAgents(shipped: [ShippedAgent]) > [AgentFile] { return []; }
export fun mgs_renderPrompts(humanOnly: {str: str}) > [Prompt] { return []; }`,
		},
		{
			name: "a harness omits the prompts function",
			src:  `export fun mgs_getHarnessConfig() > HarnessConfig { return HarnessConfig{}; }`,
			want: "mgs_renderPrompts is required on a harness spell",
		},
		{
			name: "a harness function with the wrong argument",
			src:  `export fun mgs_renderPrompts(humanOnly: [str]) > [Prompt] { return []; }`,
			want: "line 1: mgs_renderPrompts takes ([str]); the contract wants (humanOnly: {str: str})",
		},
		{
			name: "a harness function still on the input callback",
			src:  `export fun mgs_getMcp(target: Target, cb: fun(any)) > McpServer { return McpServer{}; }`,
			want: "line 1: mgs_getMcp takes parameters; a contract function takes none",
		},
	})
}

func TestCheckContractReportsEveryViolationAtOnce(t *testing.T) {
	err := checkContract(`export fun mgs_getTools() > any { return {}; }
export fun mgs_listClaimedGlobs() > [Path] { return []; }`)
	require.ErrorIs(t, err, types.SpellContractViolated)
	assert.Contains(t, err.Error(), "line 1: mgs_getTools declares > any")
	assert.Contains(t, err.Error(), "line 2: mgs_listClaimedGlobs is not a spell contract function")
}

func TestResolveRefusesASpellThatBreaksTheContract(t *testing.T) {
	const src = `export fun mgs_getName() > str { return "probe"; }
export fun mgs_getVersionProbe() > [str] { return ["node", "--version"]; }`
	ctx := context.Background()
	sess := buzz.NewSession(ctx, buzz.WithEmbedded())
	defer sess.Close()
	sess.SetModuleDecls(SpellModulePath, SpellModuleSource)
	require.NoError(t, sess.Exec(ctx, src))

	_, err := Resolve(ctx, sess, src)
	require.ErrorIs(t, err, types.SpellContractViolated)
	assert.Contains(t, err.Error(), "mgs_getVersionProbe is not a spell contract function")
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
		require.NoError(t, checkContract(string(src)), "spells/%s", dir)
		return nil
	})
	require.NoError(t, err)
	require.Positive(t, checked)
}
