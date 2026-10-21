package spell

import (
	"path/filepath"
	"testing"

	"github.com/egladman/magus/internal/symbols"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// describe runs the semantic checks over a Contract as Resolve would have decoded it.
func describe(c Contract) (spells.Descriptor, error) {
	return Resolved{Contract: c}.Descriptor()
}

func targets(ops map[string]spells.Command) Contract {
	return Contract{Name: "myspell", Targets: ops}
}

func TestDescriptor_NoName(t *testing.T) {
	_, err := describe(Contract{})
	require.Error(t, err, "a Contract with no name: want error, got nil")
	assert.Contains(t, err.Error(), "name is required")
}

func TestDescriptor_NameOnly(t *testing.T) {
	m, err := describe(Contract{Name: "myspell"})
	require.NoError(t, err)
	assert.Equal(t, "myspell", m.Name)
	assert.Nil(t, m.Ops)
}

func TestDescriptor_CommandOp(t *testing.T) {
	m, err := describe(targets(map[string]spells.Command{"build": {Bin: "go", Args: []string{"build", "./..."}}}))
	require.NoError(t, err)
	tgt, ok := m.Ops["build"]
	require.True(t, ok, `Targets["build"] missing`)
	assert.Equal(t, spells.Op{Command: spells.Command{Bin: "go", Args: []string{"build", "./..."}}}, tgt)
}

// A start service's Start, Readiness and Stop land on the Service, and the op's embedded
// Command mirrors Start, which is what a direct run executes.
func TestDescriptor_StartService(t *testing.T) {
	svc := spells.Service{
		Start:     spells.Command{Bin: "podman", Args: []string{"machine", "start"}},
		Readiness: spells.Command{Bin: "podman", Args: []string{"info"}},
		Stop:      spells.Command{Bin: "podman", Args: []string{"machine", "stop"}},
		Idle:      "30m",
	}
	m, err := describe(Contract{Name: "vm", Services: map[string]spells.Service{"machine": svc}})
	require.NoError(t, err)
	assert.Equal(t, spells.Op{Kind: spells.OpKindService, Command: svc.Start, Service: &svc}, m.Ops["machine"])
}

// TestDescriptor_PodmanMachine pins the built-in podman spell's machine op, which
// hack/remote/on-linux.buzz acquires through magus\service.
func TestDescriptor_PodmanMachine(t *testing.T) {
	op, ok := Builtins()["podman"].Ops["machine"]
	require.True(t, ok, "podman declares a machine op")
	assert.Equal(t, spells.OpKindService, op.Kind)
	require.NotNil(t, op.Service)
	argv := func(c spells.Command) []string { return append([]string{c.Bin}, c.Args...) }
	svc := *op.Service
	assert.Equal(t, map[string]any{
		"command":   []string{""},
		"start":     []string{"podman", "machine", "start"},
		"readiness": []string{"podman", "info"},
		"stop":      []string{"podman", "machine", "stop"},
		"idle":      "30m",
		"runs":      []string{"podman", "machine", "start"},
	}, map[string]any{
		"command":   argv(svc.Command),
		"start":     argv(svc.Start),
		"readiness": argv(svc.Readiness),
		"stop":      argv(svc.Stop),
		"idle":      svc.Idle,
		"runs":      argv(op.Command),
	})
}

// A service the supervisor cannot run is a load error naming the op, not a failure at
// the first acquire.
func TestDescriptor_ServiceShapeRefused(t *testing.T) {
	start := spells.Command{Bin: "podman", Args: []string{"machine", "start"}}
	tests := []struct {
		name    string
		svc     spells.Service
		wantErr string
	}{
		{"neither", spells.Service{}, "service declares neither command nor start"},
		{"both", spells.Service{Command: spells.Command{Bin: "qemu"}, Start: start, Readiness: start, Stop: start}, "service declares both command and start"},
		{"start without readiness", spells.Service{Start: start, Stop: start}, "a start service needs readiness"},
		{"start without stop", spells.Service{Start: start, Readiness: start}, "a start service needs stop"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := describe(Contract{Name: "vm", Services: map[string]spells.Service{"machine": tt.svc}})
			require.ErrorContains(t, err, `spell "vm" op "machine": `+tt.wantErr)
		})
	}
}

// A service op refuses what the supervisor cannot honor on any of its four commands.
func TestDescriptor_ServiceRefusesSecretsAndHints(t *testing.T) {
	hinted := spells.Command{Bin: "x", Hints: []spells.Hint{{Contains: "a", Advise: "b"}}}
	secret := spells.Command{Bin: "x", Secrets: map[string]string{"TOKEN": "TOKEN"}}
	start := spells.Command{Bin: "podman", Args: []string{"info"}}
	tests := []struct {
		name    string
		svc     spells.Service
		wantErr string
	}{
		{"secrets on command", spells.Service{Command: secret}, "secrets are not supported on a service op"},
		{"hints on command", spells.Service{Command: hinted}, "hints are not supported on a service op"},
		{"secrets on start", spells.Service{Start: secret, Readiness: start, Stop: start}, "secrets are not supported on a service start command"},
		{"hints on readiness", spells.Service{Start: start, Readiness: hinted, Stop: start}, "hints are not supported on a service readiness command"},
		{"hints on stop", spells.Service{Start: start, Readiness: start, Stop: hinted}, "hints are not supported on a service stop command"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := describe(Contract{Name: "vm", Services: map[string]spells.Service{"machine": tt.svc}})
			require.ErrorContains(t, err, tt.wantErr)
		})
	}
}

// mgs_getSymbolIndexer decodes onto the descriptor AND synthesizes the op the run path
// dispatches, tagged with the kind the runner matches on rather than its name.
func TestDescriptor_SymbolIndexer(t *testing.T) {
	m, err := describe(Contract{Name: "myspell", SymbolIndexer: &spells.SymbolIndexer{
		Format:  spells.SymbolFormatSCIP,
		Command: spells.Command{Bin: "scip-go", Args: []string{"--output", "$MAGUS_SYMBOL_INDEX"}},
	}})
	require.NoError(t, err)
	require.NotNil(t, m.SymbolIndexer)
	assert.Equal(t, spells.SymbolFormatSCIP, m.SymbolIndexer.Format)
	assert.Equal(t, "scip-go", m.SymbolIndexer.Command.Bin)

	op, ok := m.Ops[spells.DefaultSymbolIndexOp]
	require.True(t, ok, "an indexer that declares no op runs under the default, whatever the spell is named")
	assert.Equal(t, spells.Op{
		Kind:    spells.OpKindSymbolIndex,
		Command: spells.Command{Bin: "scip-go", Args: []string{"--output", "$MAGUS_SYMBOL_INDEX"}},
	}, op)
	assert.Len(t, m.Ops, 1)
}

// An indexer that declares its op runs under that op alone, so an index riding beside
// scip-go on one project neither shares its cache entry nor fails with it. The declared
// name is normalized like an authored op, and an authored op of the same name collides.
func TestDescriptor_SymbolIndexerDeclaresItsOp(t *testing.T) {
	indexer := func(op string) *spells.SymbolIndexer {
		return &spells.SymbolIndexer{Format: spells.SymbolFormatSCIP, Op: op, Command: spells.Command{Bin: "scip-mine"}}
	}
	m, err := describe(Contract{Name: "myspell", SymbolIndexer: indexer("scip_mine")})
	require.NoError(t, err)
	assert.Equal(t, "scip-mine", m.SymbolIndexer.OpName())
	assert.Equal(t, []string{"scip-mine"}, m.OpNames(), "registered once, and not under the default")
	assert.Equal(t, spells.OpKindSymbolIndex, m.Ops["scip-mine"].Kind)

	_, err = describe(Contract{
		Name:          "myspell",
		SymbolIndexer: indexer("scip-mine"),
		Targets:       map[string]spells.Command{"scip-mine": {Bin: "scip-mine"}},
	})
	require.ErrorContains(t, err, `declares an op named "scip-mine"`)

	_, err = describe(Contract{Name: "myspell", SymbolIndexer: indexer("scip mine")})
	require.ErrorContains(t, err, `symbol indexer: op "scip mine"`)
}

// An indexer's envs decode in order, one overlay per run. An empty overlay or one naming
// a variable magus sets for the run is refused at load, since either would build an
// index nobody reads.
func TestDescriptor_SymbolIndexerEnvs(t *testing.T) {
	src := func(envs ...map[string]string) Contract {
		return Contract{Name: "myspell", SymbolIndexer: &spells.SymbolIndexer{
			Format:  spells.SymbolFormatSCIP,
			Command: spells.Command{Bin: "scip-go"},
			Envs:    envs,
		}}
	}
	m, err := describe(src(map[string]string{"GOOS": "linux"}, map[string]string{"GOOS": "windows", "CGO_ENABLED": "0"}))
	require.NoError(t, err)
	assert.Equal(t, []map[string]string{{"GOOS": "linux"}, {"GOOS": "windows", "CGO_ENABLED": "0"}}, m.SymbolIndexer.Envs)

	_, err = describe(src(map[string]string{}))
	require.EqualError(t, err, `spell "myspell": symbol indexer: envs[0] is empty`)
	_, err = describe(src(map[string]string{"GOOS": "linux"}, map[string]string{symbols.IndexEnvVar: "/tmp/x"}))
	require.EqualError(t, err, `spell "myspell": symbol indexer: envs[1] sets MAGUS_SYMBOL_INDEX, which magus sets for each run`)
}

// The go spell indexes every GOOS a file suffix can name, so a host indexes the files
// only another platform compiles.
func TestBuiltinGoSymbolIndexerCoversEveryOS(t *testing.T) {
	si := Builtins()["go"].SymbolIndexer
	require.NotNil(t, si)
	assert.Equal(t, []map[string]string{{"GOOS": "linux"}, {"GOOS": "darwin"}, {"GOOS": "windows"}}, si.Envs)
}

// The shipped indexers that declare no op keep the op and the index file every cached
// run already recorded, so binding the buzz spell beside them re-keys nothing; buzz
// declares an op of its own.
func TestBuiltinIndexersKeepTheirOps(t *testing.T) {
	ops := map[string]string{}
	for name, d := range Builtins() {
		if d.SymbolIndexer != nil {
			ops[name] = d.SymbolIndexer.OpName()
			assert.Equalf(t, spells.OpKindSymbolIndex, d.Ops[ops[name]].Kind, "%s registers its indexer under the op it names", name)
		}
	}
	assert.Equal(t, map[string]string{
		"buzz": "scip-buzz", "go": "scip", "python": "scip", "rust": "scip", "typescript": "scip",
	}, ops)
	assert.Equal(t, "index.scip", filepath.Base(symbols.IndexPath(t.TempDir(), t.TempDir(), ops["go"])))
}

// The tools an indexer runs besides its binary decode onto the indexer, and each must be a
// tool the spell probes for a version: a used tool with no probe would key the index on
// nothing, so the load fails naming the spell rather than leaving the index unstaleable.
func TestDescriptor_SymbolIndexerUses(t *testing.T) {
	src := func(uses []string, tools map[string]spells.Tool) Contract {
		return Contract{
			Name:  "myspell",
			Tools: tools,
			SymbolIndexer: &spells.SymbolIndexer{
				Format:  spells.SymbolFormatSCIP,
				Command: spells.Command{Bin: "scip-go"},
				Uses:    uses,
			},
		}
	}
	probed := map[string]spells.Tool{"go": {Probe: spells.Command{Bin: "go", Args: []string{"version"}}}}

	m, err := describe(src([]string{"go"}, probed))
	require.NoError(t, err)
	assert.Equal(t, []string{"go"}, m.SymbolIndexer.Uses)

	_, err = describe(src([]string{"gopls"}, probed))
	require.EqualError(t, err, `spell "myspell": symbol indexer uses "gopls", which mgs_getTools does not declare with a version probe`)

	observed := map[string]spells.Tool{"go": {Observe: spells.Command{Bin: "go", Args: []string{"version"}}}}
	_, err = describe(src([]string{"go"}, observed))
	require.Error(t, err, "an observe probe is not a version probe")
}

// The format is required rather than defaulted to SCIP: the whole point of naming it is
// that ingestion no longer assumes.
func TestDescriptor_SymbolIndexerRequiresAFormat(t *testing.T) {
	_, err := describe(Contract{Name: "myspell", SymbolIndexer: &spells.SymbolIndexer{Command: spells.Command{Bin: "scip-go"}}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "format")
}

// The pre-declaration spelling (a `scip` entry in mgs_listTargets) fails at load naming
// the export to move to, rather than decoding as an ordinary op that would run with an
// unresolved destination and silently drop the project out of the symbol graph.
func TestDescriptor_ReservedSymbolIndexOpIsRefused(t *testing.T) {
	_, err := describe(targets(map[string]spells.Command{spells.DefaultSymbolIndexOp: {Bin: "scip-go"}}))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "mgs_getSymbolIndexer")
}

// DefaultArgs lands on Op.DefaultArgs, distinct from Args, and an absent one stays nil.
func TestDescriptor_CommandDefaultArgs(t *testing.T) {
	m, err := describe(targets(map[string]spells.Command{
		"test": {Bin: "go", Args: []string{"test"}, DefaultArgs: []string{"./..."}},
	}))
	require.NoError(t, err)
	assert.Equal(t, spells.Op{Command: spells.Command{Bin: "go", Args: []string{"test"}, DefaultArgs: []string{"./..."}}}, m.Ops["test"])

	m, err = describe(targets(map[string]spells.Command{"test": {Bin: "go", Args: []string{"test"}}}))
	require.NoError(t, err)
	assert.Nil(t, m.Ops["test"].DefaultArgs)
}

func TestDescriptor_CommandNeedsArgs(t *testing.T) {
	m, err := describe(targets(map[string]spells.Command{
		"serve": {Bin: "pnpm", Args: []string{"exec"}, NeedsArgs: "name a command"},
		"build": {Bin: "go", Args: []string{"build"}},
	}))
	require.NoError(t, err)
	assert.Equal(t, "name a command", m.Ops["serve"].NeedsArgs)
	assert.Empty(t, m.Ops["build"].NeedsArgs)
}

func TestDescriptor_ModeArgs(t *testing.T) {
	spell := func(modes map[string][]string) Contract {
		c := targets(map[string]spells.Command{
			"node_test": {Bin: "node", Args: []string{"--enable-source-maps", "--test"}},
			"build":     {Bin: "go", Args: []string{"build"}},
		})
		c.ModeArgs = modes
		return c
	}
	m, err := describe(spell(map[string][]string{"node_test": {"--test"}}))
	require.NoError(t, err)
	assert.Equal(t, []string{"--test"}, m.Ops["node-test"].ModeArgs)
	assert.Nil(t, m.Ops["build"].ModeArgs)

	for _, tc := range []struct {
		modes map[string][]string
		want  string
	}{
		{modes: map[string][]string{"lint": {"--test"}}, want: `names op "lint", which the spell does not declare`},
		{modes: map[string][]string{"node-test": {"--watch"}}, want: `"--watch" is not in the op's args`},
		{modes: map[string][]string{"node-test": {}}, want: "names no args"},
	} {
		_, err := describe(spell(tc.modes))
		require.ErrorContains(t, err, tc.want)
	}
}

// Secrets land on Op.Secrets untouched: no resolution happens at decode time, only at spawn.
func TestDescriptor_CommandSecrets(t *testing.T) {
	m, err := describe(targets(map[string]spells.Command{
		"publish": {Bin: "npm", Args: []string{"publish"}, Secrets: map[string]string{"NPM_TOKEN": "NPM_TOKEN"}},
	}))
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"NPM_TOKEN": "NPM_TOKEN"}, m.Ops["publish"].Secrets)
}

// A charm carrying a replace patch op reaches the op as the canonical spells.PatchOp.
func TestDescriptor_CharmReplaceOp(t *testing.T) {
	m, err := describe(targets(map[string]spells.Command{
		"fmt": {Bin: "gofmt", Args: []string{"-l", "."}, Charms: map[string]spells.Charm{
			"write": {Ops: []spells.PatchOp{{Op: "replace", Path: "/0", Value: "-w"}}},
		}},
	}))
	require.NoError(t, err)
	charm, ok := m.Ops["fmt"].Charms["write"]
	require.True(t, ok, `Charms["write"] missing`)
	assert.Equal(t, []spells.PatchOp{{Op: "replace", Path: "/0", Value: "-w"}}, charm.Ops)
}

// An append patch op (add /-) is accepted.
func TestDescriptor_CharmAddOp(t *testing.T) {
	_, err := describe(targets(map[string]spells.Command{
		"test": {Bin: "go", Args: []string{"test", "./..."}, Charms: map[string]spells.Charm{
			"debug": {Ops: []spells.PatchOp{{Op: "add", Path: "/-", Value: "-v"}}},
		}},
	}))
	require.NoError(t, err)
}

// A root-path op (whole-argv replace) is rejected: charms are element-level.
func TestDescriptor_CharmRootRejected(t *testing.T) {
	_, err := describe(targets(map[string]spells.Command{
		"fmt": {Bin: "gofmt", Charms: map[string]spells.Charm{
			"write": {Ops: []spells.PatchOp{{Op: "replace", Path: "", Value: "x"}}},
		}},
	}))
	require.Error(t, err)
	assert.ErrorContains(t, err, `charm "write"`)
}

func TestDescriptor_InvalidTargetName(t *testing.T) {
	_, err := describe(targets(map[string]spells.Command{"has space": {Bin: "echo"}}))
	assert.Error(t, err, "an op name with a space: want error, got nil")
}

func TestDescriptor_NeedsAndProvidesAreLexicalValues(t *testing.T) {
	m, err := describe(Contract{
		Name:          "myspell",
		RequiredGlobs: []types.Path{{Value: "**/*.go", Base: "ignored"}, {Value: "go.mod"}},
		ProvidedGlobs: []types.Path{{Value: "gen/**"}},
	})
	require.NoError(t, err)
	assert.Equal(t, spells.Descriptor{Name: "myspell", Needs: []string{"**/*.go", "go.mod"}, Provides: []string{"gen/**"}}, m)
}

// Ignore dirs reduce to their values, and an absent declaration is nil.
func TestDescriptor_IgnoreDirs(t *testing.T) {
	m, err := describe(Contract{Name: "myspell", IgnoreDirs: []types.Path{{Value: "vendor", IsDir: true}, {Value: "target", IsDir: true}}})
	require.NoError(t, err)
	assert.Equal(t, []string{"vendor", "target"}, m.IgnoreDirs)

	m, err = describe(Contract{Name: "bare"})
	require.NoError(t, err)
	assert.Nil(t, m.IgnoreDirs, "a spell with no ignore dirs must decode to nil")
}

// ValidateTargetName admits '_' and uppercase, but every request reaching dispatchOp has
// already been kebab-normalized by ParseTarget and dispatch is a plain map hit. An op
// stored as authored would be declared and reachable by nothing.
func TestDescriptor_OpNameIsNormalized(t *testing.T) {
	d, err := describe(targets(map[string]spells.Command{
		"go_build": {Bin: "go", Args: []string{"build"}},
		"goVet":    {Bin: "go", Args: []string{"vet"}},
	}))
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"go-build", "go-vet"}, d.OpNames())

	for _, authored := range []string{"go_build", "goVet"} {
		requested, perr := types.ParseTarget(authored)
		require.NoErrorf(t, perr, "ParseTarget(%q)", authored)
		_, ok := d.Ops[requested.Name]
		assert.Truef(t, ok, "op authored %q must be reachable as %q", authored, requested.Name)
	}
}

// Collapsing keys to canonical form means two spellings can land on one, and a plain
// map assignment would resolve that by last-write-wins: a load error naming both.
func TestDescriptor_OpNameCollisionIsAnError(t *testing.T) {
	_, err := describe(targets(map[string]spells.Command{
		"go-build": {Bin: "go", Args: []string{"FIRST"}},
		"goBuild":  {Bin: "go", Args: []string{"SECOND"}},
	}))
	require.Error(t, err, "two spellings of one canonical op must not decode silently")
	assert.Contains(t, err.Error(), "go-build", "the error names the canonical op")
	assert.Contains(t, err.Error(), "goBuild", "and both authored spellings")

	d, err := describe(targets(map[string]spells.Command{
		"go-build": {Bin: "go", Args: []string{"build"}},
		"goVet":    {Bin: "go", Args: []string{"vet"}},
	}))
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"go-build", "go-vet"}, d.OpNames())
}

// A spell declaring no tools keeps the conservative default.
func TestDescriptorToolsAbsent(t *testing.T) {
	m, err := describe(Contract{Name: "go"})
	require.NoError(t, err)
	assert.Empty(t, m.Tools)
}

// Everything magus knows about one binary arrives in one entry, keyed by the bin an op
// already names, which is what replaced five parallel declarations.
func TestDescriptorToolsCarriesProbeKeyAndReady(t *testing.T) {
	m, err := describe(Contract{Name: "docker", Tools: map[string]spells.Tool{
		"docker": {
			Probe: spells.Command{Bin: "docker", Args: []string{"--version"}},
			Key:   spells.VersionKey{UpTo: spells.VersionPatch},
			Ready: spells.Command{Bin: "docker", Args: []string{"info"}},
		},
		"hadolint": {Probe: spells.Command{Bin: "hadolint", Args: []string{"--version"}}},
	}})
	require.NoError(t, err)

	d := m.Tools["docker"]
	assert.Equal(t, "docker", d.Probe.Bin)
	assert.Equal(t, spells.VersionPatch, d.Key.UpTo)
	assert.Equal(t, "info", d.Ready.Args[0])

	h := m.Tools["hadolint"]
	assert.Equal(t, "hadolint", h.Probe.Bin)
	assert.Empty(t, h.Ready.Bin, "linting a Dockerfile must not wait on the docker daemon")
}

// A malformed probe or ready command must be a load error, not a silently dropped
// declaration that leaves a tool never probed with nothing to tell the author why.
func TestDescriptorToolsPropagatesMalformedCommandErrors(t *testing.T) {
	malformed := spells.Command{Bin: "docker", Quiet: true}
	for field, tool := range map[string]spells.Tool{
		"probe":   {Probe: malformed},
		"ready":   {Ready: malformed},
		"observe": {Observe: malformed},
	} {
		_, err := describe(Contract{Name: "docker", Tools: map[string]spells.Tool{"docker": tool}})
		require.Error(t, err, field)
		assert.Contains(t, err.Error(), `spell "docker"`)
		assert.Contains(t, err.Error(), `tools["docker"].`+field)
	}
}

// A constant stands in for a tool that cannot report a version, and needs no probe.
func TestDescriptorToolsConstNeedsNoProbe(t *testing.T) {
	m, err := describe(Contract{Name: "x", Tools: map[string]spells.Tool{
		"protoc-gen-go": {Key: spells.VersionKey{Const: "protoc-gen-go-1"}},
	}})
	require.NoError(t, err)
	tool := m.Tools["protoc-gen-go"]
	assert.Equal(t, "protoc-gen-go-1", tool.Key.Const)
	assert.True(t, tool.HasProbe(), "a constant is a version magus can key on")
}

// A typo'd component would otherwise leave a spell claiming "major" while the cache
// silently kept keying on the whole output.
func TestDescriptorToolsRejectsUnknownComponent(t *testing.T) {
	_, err := describe(Contract{Name: "go", Tools: map[string]spells.Tool{
		"go": {Key: spells.VersionKey{UpTo: "mayor"}},
	}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), `tools["go"].key.upTo`)
	assert.Contains(t, err.Error(), "mayor")
}

// A bound is validated with the parser that later COMPARES it. The two used to differ:
// a leading zero passed validation and failed the comparison, so the spell loaded and
// every Check against it degraded to VerdictUnknown: a window that constrained nothing.
func TestDescriptorToolsRejectsABoundTheComparisonCannotRead(t *testing.T) {
	const bound = "01.2"
	require.False(t, spells.ValidBound(bound), "the bound this pins must be one Check cannot read")
	require.Equal(t, spells.VerdictUnknown,
		spells.VersionBounds{Min: bound}.Check("v1.5.0"),
		"and it must be the degradation, not a comparison")

	for field, supported := range map[string]spells.VersionBounds{
		"min":   {Min: bound},
		"below": {Below: bound},
	} {
		t.Run(field, func(t *testing.T) {
			_, err := describe(Contract{Name: "go", Tools: map[string]spells.Tool{"go": {Supported: supported}}})
			require.Error(t, err)
			assert.Contains(t, err.Error(), "tools[\"go\"].supported."+field)
			assert.Contains(t, err.Error(), bound)
		})
	}
}

// The forms authors actually write (a partial version, a full one, either with or
// without the v Go spells) keep loading.
func TestDescriptorToolsAcceptsAuthoredBoundForms(t *testing.T) {
	for _, bound := range []string{"1.21", "25", "2.0.0", "v1.21.0", "1.0.0-rc1"} {
		m, err := describe(Contract{Name: "go", Tools: map[string]spells.Tool{
			"go": {Supported: spells.VersionBounds{Min: bound}},
		}})
		require.NoErrorf(t, err, "bound %q", bound)
		assert.Equal(t, bound, m.Tools["go"].Supported.Min)
	}
}

// An entry declaring nothing at all is dropped rather than kept as a tool magus knows
// nothing about.
func TestDescriptorToolsDropsEmptyEntries(t *testing.T) {
	m, err := describe(Contract{Name: "go", Tools: map[string]spells.Tool{"gofmt": {}}})
	require.NoError(t, err)
	assert.Empty(t, m.Tools)
}

// A tool declaring only a diagnostics format is kept: the entry says something magus
// acts on, so the drop-empty-entries rule must not treat it as saying nothing.
func TestDescriptorToolsCarriesDiagnosticsFormat(t *testing.T) {
	m, err := describe(Contract{Name: "docker", Tools: map[string]spells.Tool{
		"hadolint": {Diagnostics: spells.DiagnosticGNU},
	}})
	require.NoError(t, err)
	assert.Equal(t, spells.DiagnosticGNU, m.Tools["hadolint"].Diagnostics)
}

// An unrecognized format is a spell-authoring bug caught at load, the same place a bad
// key.upTo lands, not a silent fall back to scraping prose.
func TestDescriptorToolsRejectsUnknownDiagnosticsFormat(t *testing.T) {
	_, err := describe(Contract{Name: "docker", Tools: map[string]spells.Tool{
		"hadolint": {Diagnostics: "sarif"},
	}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "diagnostics is sarif")
	assert.Contains(t, err.Error(), "gnu", "the message names the formats magus accepts")
}

// A half-written hint is refused rather than dropped: one with no contains would fire on
// every failure of the command, and one with no advise would consume the match and
// print nothing, reading as "magus had no idea".
func TestValidateCommandHints(t *testing.T) {
	t.Run("a whole hint passes", func(t *testing.T) {
		cmd := spells.Command{Bin: "docker", Hints: []spells.Hint{{Contains: "denied", Advise: "docker login"}}}
		require.NoError(t, validateCommand("docker", "docker-buildx", cmd))
	})
	t.Run("a hint with no contains is refused", func(t *testing.T) {
		err := validateCommand("s", "o", spells.Command{Hints: []spells.Hint{{Advise: "do the thing"}}})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "both contains and advise are required")
	})
	t.Run("a contains with no advise is refused", func(t *testing.T) {
		err := validateCommand("s", "o", spells.Command{Hints: []spells.Hint{{Contains: "denied"}}})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "both contains and advise are required")
	})
}

// An unknown External is a LOAD error rather than a lenient zero: an op that meant
// mutates-external and misspelled it would otherwise decode as "declares nothing" and
// be silently exempt from the one check it was opting into.
func TestDescriptor_CommandExternal(t *testing.T) {
	m, err := describe(targets(map[string]spells.Command{
		"scan":  {Bin: "trivy", Args: []string{"image"}, External: spells.ExternalReads},
		"push":  {Bin: "cosign", Args: []string{"sign"}, External: spells.ExternalMutates},
		"build": {Bin: "go", Args: []string{"build"}},
	}))
	require.NoError(t, err)
	assert.Equal(t, spells.ExternalReads, m.Ops["scan"].External)
	assert.Equal(t, spells.ExternalMutates, m.Ops["push"].External)
	assert.Equal(t, spells.ExternalNone, m.Ops["build"].External,
		"declaring nothing is the default and the honest answer for most ops")

	_, err = describe(targets(map[string]spells.Command{"scan": {Bin: "trivy", External: "reads-network"}}))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "reads-network")
	assert.Contains(t, err.Error(), "reads-external", "the message must name what is accepted")
}

// A lifecycle alone keeps the entry: it names the product a reader looks dates up by.
func TestDescriptor_ToolLifecycle(t *testing.T) {
	m, err := describe(Contract{Name: "ts", Tools: map[string]spells.Tool{"node": {Lifecycle: "nodejs"}}})
	require.NoError(t, err)
	assert.Equal(t, "nodejs", m.Tools["node"].Lifecycle)
}

// The observation probe decodes beside the version probe, and a tool entry declaring
// only an observe command is still a tool magus knows something about, so it must
// survive the drop that removes entries declaring nothing.
func TestDescriptor_ToolObserve(t *testing.T) {
	m, err := describe(Contract{Name: "myspell", Tools: map[string]spells.Tool{
		"trivy": {
			Probe:   spells.Command{Bin: "trivy", Args: []string{"version"}},
			Observe: spells.Command{Bin: "trivy", Args: []string{"version", "--format", "json"}},
		},
		"feed": {Observe: spells.Command{Bin: "feedctl", Args: []string{"revision"}}},
	}})
	require.NoError(t, err)

	trivy := m.Tools["trivy"]
	assert.Equal(t, []string{"version"}, trivy.Probe.Args)
	assert.Equal(t, []string{"version", "--format", "json"}, trivy.Observe.Args)
	assert.True(t, trivy.HasObservationProbe())

	feed, ok := m.Tools["feed"]
	require.True(t, ok, "a tool declaring only an observe probe must not be dropped")
	assert.True(t, feed.HasObservationProbe())
	assert.False(t, feed.HasProbe(), "an observation is not a version")
}

func stubSpell(stubs spells.StubSyntax) Contract {
	return Contract{Name: "lang", Language: &spells.Language{
		Name:       "lang",
		Extensions: []string{".l"},
		Syntax:     &spells.Syntax{Stubs: &stubs},
	}}
}

func TestDescriptor_Stubs(t *testing.T) {
	want := spells.StubSyntax{Kinds: []string{"Function", "Method"}, BodyStyle: "brace", Body: "{ panic() }"}
	m, err := describe(stubSpell(want))
	require.NoError(t, err)
	require.NotNil(t, m.Syntax)
	assert.Equal(t, &want, m.Syntax.Stubs)
	assert.Nil(t, m.Syntax.Comments, "stubs alone declare no comment syntax")

	m, err = describe(Contract{Name: "lang", Language: &spells.Language{Name: "lang"}})
	require.NoError(t, err)
	assert.Nil(t, m.Syntax, "a language record without syntax declares none")
}

func TestDescriptor_StubsRefused(t *testing.T) {
	valid := func() spells.StubSyntax {
		return spells.StubSyntax{Kinds: []string{"Function"}, BodyStyle: "indent", Body: "raise"}
	}
	tests := []struct {
		name   string
		mutate func(*spells.StubSyntax)
		want   string
	}{
		{"unknown body style", func(s *spells.StubSyntax) { s.BodyStyle = "curly" }, `language.syntax: stubs.bodyStyle is "curly", want "brace" or "indent"`},
		{"missing body style", func(s *spells.StubSyntax) { s.BodyStyle = "" }, `stubs.bodyStyle is ""`},
		{"no kinds", func(s *spells.StubSyntax) { s.Kinds = nil }, "stubs.kinds is empty"},
		{"lowercase kind", func(s *spells.StubSyntax) { s.Kinds = []string{"function"} }, `stubs.kinds: "function" is not a SCIP symbol kind`},
		{"unspecified kind", func(s *spells.StubSyntax) { s.Kinds = []string{"UnspecifiedKind"} }, `"UnspecifiedKind" is not a SCIP symbol kind`},
		{"no body", func(s *spells.StubSyntax) { s.Body = "" }, "stubs.body is required"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stubs := valid()
			tt.mutate(&stubs)
			_, err := describe(stubSpell(stubs))
			require.Error(t, err)
			assert.Contains(t, err.Error(), `spell "lang"`, "the error names the spell")
			assert.Contains(t, err.Error(), tt.want)
		})
	}
}

// Comment syntax without the extensions that scope it covers no files.
func TestDescriptor_CommentsNeedExtensions(t *testing.T) {
	_, err := describe(Contract{Name: "lang", Language: &spells.Language{
		Name:   "lang",
		Syntax: &spells.Syntax{Comments: &spells.CommentSyntax{LineComments: []string{"//"}}},
	}})
	require.ErrorContains(t, err, "syntax.comments without extensions covers no files")
}

// TestBuiltinStubSyntax pins what each built-in declares: the splitter reads
// these through magus\describe\spell, and a body that stops type-checking
// under every signature turns every stub it writes into a build failure.
func TestBuiltinStubSyntax(t *testing.T) {
	m := Builtins()
	want := map[string]spells.StubSyntax{
		"go": {
			Kinds: []string{"Function", "Method"}, BodyStyle: "brace",
			Body: "{\n\tpanic(\"unimplemented: {{&Name}} lands in {{&Branch}}\")\n}",
		},
		"typescript": {
			Kinds: []string{"Function", "Method"}, BodyStyle: "brace",
			Body: "{\n\tthrow new Error(\"unimplemented: {{&Name}} lands in {{&Branch}}\");\n}",
		},
		"rust": {
			Kinds: []string{"Function", "Method"}, BodyStyle: "brace",
			Body: "{\n    todo!(\"{{&Name}} lands in {{&Branch}}\")\n}",
		},
		"python": {
			Kinds: []string{"Function", "Method"}, BodyStyle: "indent",
			Body: "raise NotImplementedError(\"{{&Name}} lands in {{&Branch}}\")",
		},
	}
	for name, w := range want {
		require.NotNil(t, m[name].Syntax, "%s declares syntax", name)
		require.NotNil(t, m[name].Syntax.Stubs, "%s declares stubs", name)
		assert.Equal(t, w, *m[name].Syntax.Stubs, name)
	}
	assert.Nil(t, m["bash"].Syntax, "a spell that cannot declare a body honestly declares none")
	require.NotNil(t, m["buzz"].Syntax)
	assert.Nil(t, m["buzz"].Syntax.Stubs, "buzz declares comments only")
}
