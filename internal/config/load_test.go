package config

import (
	"bytes"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	queuetypes "github.com/egladman/magus/internal/queue/types"
	"github.com/egladman/magus/internal/ward"
	"github.com/egladman/magus/types"
)

// The spell declarations sit inline beside the reserved keys, so the document reads
// like the imports it answers, and a strict decode still takes it.
func TestLoadFileReadsInlineSpellImports(t *testing.T) {
	path := filepath.Join(t.TempDir(), Filename)
	require.NoError(t, os.WriteFile(path, []byte(`spells:
  registries:
    - host: ghcr.io
      username: ci
      password: GITHUB_TOKEN
  ghcr.io/team/spells/lint:
    tag: "1.4"
  magus/spell/go:
    path: spells/go
`), 0o644))
	cfg, err := LoadFile(path, true)
	require.NoError(t, err)
	assert.Equal(t, SpellsConfig{
		Registries: []SpellRegistry{{Host: "ghcr.io", Username: "ci", Password: "GITHUB_TOKEN"}},
		Imports: map[string]SpellImport{
			"ghcr.io/team/spells/lint": {Tag: "1.4"},
			"magus/spell/go":           {Path: "spells/go"},
		},
	}, cfg.Spells)

	// An unknown field inside one declaration is still refused by name.
	require.NoError(t, os.WriteFile(path, []byte("spells:\n  ghcr.io/team/spells/lint:\n    digest: sha256:abc\n"), 0o644))
	_, err = LoadFile(path, false)
	require.ErrorIs(t, err, types.UnknownConfigKey)
}

func TestMergeConfig(t *testing.T) {
	t.Parallel()
	base := Defaults()
	base.Concurrency = 4

	overlay := Config{}
	writeOff := false
	overlay.Cache.Write.Enabled = &writeOff
	overlay.Cache.Dir = "/tmp/cache"

	got := mergeConfig(base, overlay)
	assert.False(t, got.Cache.WriteEnabled())
	assert.Equal(t, "/tmp/cache", got.Cache.Dir)
	// base value preserved when overlay is zero
	assert.Equal(t, 4, got.Concurrency)
}

func TestLoadDirInto(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	content := "cache:\n  write:\n    enabled: false\nconcurrency: 12\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "magus.yaml"), []byte(content), 0o644))

	cfg, err := loadDirInto(Defaults(), dir)
	require.NoError(t, err)
	assert.False(t, cfg.Cache.WriteEnabled())
	assert.Equal(t, 12, cfg.Concurrency)
}

func TestLoadDirIntoDotted(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	content := "cache:\n  write:\n    enabled: false\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".magus.yaml"), []byte(content), 0o644))

	cfg, err := loadDirInto(Defaults(), dir)
	require.NoError(t, err)
	assert.False(t, cfg.Cache.WriteEnabled())
}

func TestLoadDirIntoCoexistenceError(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "magus.yaml"), []byte(""), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".magus.yaml"), []byte(""), 0o644))

	_, err := loadDirInto(Defaults(), dir)
	assert.Error(t, err, "expected error for coexisting magus.yaml and .magus.yaml")
}

func TestLoadDirIntoMissing(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	base := Defaults()
	cfg, err := loadDirInto(base, dir)
	require.NoError(t, err)
	// No file → cfg is unchanged from base
	assert.Equal(t, base.Cache.WriteEnabled(), cfg.Cache.WriteEnabled(), "Cache.Write.Enabled changed unexpectedly")
}

// Not t.Parallel, and neither are the other two tests in this file that capture the
// logger. slog.SetDefault is a PROCESS global: the comment below guarded these subtests
// against each other and missed that a parallel sibling doing the same thing restores its
// own `prev` while this one is mid-assertion. Failed under gate load and passed when run
// alone, which is the signature.
func TestWarnIfConcurrencyHigh(t *testing.T) {
	run := func(t *testing.T, concurrency, numCPU int, wantWarn bool) {
		// slog.SetDefault mutates global state — subtests cannot run in parallel.
		var buf bytes.Buffer
		prev := slog.Default()
		slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})))
		t.Cleanup(func() { slog.SetDefault(prev) })

		warnIfConcurrencyHigh(concurrency, numCPU)

		got := strings.Contains(buf.String(), "config.concurrency_high")
		assert.Equal(t, wantWarn, got, "warn emitted mismatch (log=%q)", buf.String())
	}

	t.Run("default unset", func(t *testing.T) { run(t, 0, 8, false) })
	t.Run("at limit", func(t *testing.T) { run(t, 16, 8, false) })
	t.Run("just over", func(t *testing.T) { run(t, 17, 8, true) })
	t.Run("way over", func(t *testing.T) { run(t, 200, 8, true) })
	t.Run("unknown cpu", func(t *testing.T) { run(t, 16, 0, false) })
}

// TestExtractFlag pins every spelling ExtractFlag must recognize for -config/--config,
// including the short -c form main.go advertises in its help text but never actually wired
// through (cfgPath had zero readers after fs.Parse). -C is a DIFFERENT flag (short for
// --root, bound only in cmd/magus/main.go) and flag matching is case-sensitive, so -C must
// never be read as config.
func TestExtractFlag(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		args []string
		want string
	}{
		{"long form space", []string{"-config", "a.yaml"}, "a.yaml"},
		{"long form dashdash space", []string{"--config", "a.yaml"}, "a.yaml"},
		{"long form equals", []string{"-config=a.yaml"}, "a.yaml"},
		{"long form dashdash equals", []string{"--config=a.yaml"}, "a.yaml"},
		{"short form space", []string{"-c", "a.yaml"}, "a.yaml"},
		{"short form dashdash space", []string{"--c", "a.yaml"}, "a.yaml"},
		{"short form equals", []string{"-c=a.yaml"}, "a.yaml"},
		{"short form dashdash equals", []string{"--c=a.yaml"}, "a.yaml"},
		{"short form among other args", []string{"run", "-c", "a.yaml", "build"}, "a.yaml"},
		{"missing value", []string{"-c"}, ""},
		{"no flag at all", []string{"run", "build"}, ""},
		{"-C is root, not config", []string{"-C", "a.yaml"}, ""},
		{"--root is not config either", []string{"--root", "a.yaml"}, ""},
		{"stops at -- separator", []string{"run", "build", "--", "-c", "a.yaml"}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, ExtractFlag(tc.args))
		})
	}
}

// The four documented keys that default true. mergeConfig's non-zero-wins rule
// cannot tell an absent key from an explicit `false`, so until mergeOverlay
// consulted the document's own key set every one of these decoded to false and
// was then discarded as "inherit", unrepresentable from magus.yaml.
func TestLoadDirIntoBoolFalseTurnsOffADefaultOnKey(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	content := "server:\n  enabled: false\nci:\n  record_runs: false\nvolatility:\n  enabled: false\n  annotate_gha: false\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "magus.yaml"), []byte(content), 0o644))

	require.True(t, Defaults().Server.Enabled, "precondition: server.enabled defaults true")

	cfg, err := loadDirInto(Defaults(), dir)
	require.NoError(t, err)
	assert.False(t, cfg.Server.Enabled, "server.enabled: false should stop commands handing themselves to the server")
	assert.False(t, cfg.CI.RecordRuns, "ci.record_runs: false should stop recording runs")
	assert.False(t, cfg.Volatility.Enabled, "volatility.enabled: false should disable volatility detection")
	assert.False(t, cfg.Volatility.AnnotateGHA, "volatility.annotate_gha: false should stop GHA annotations")
}

// An absent key still means "inherit": the key set settles only keys that were
// WRITTEN, so a partial overlay must not drag every unmentioned bool to false.
func TestLoadDirIntoAbsentBoolInherits(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "magus.yaml"), []byte("concurrency: 3\n"), 0o644))

	cfg, err := loadDirInto(Defaults(), dir)
	require.NoError(t, err)
	assert.Equal(t, 3, cfg.Concurrency)
	assert.True(t, cfg.Server.Enabled, "server.enabled was never written; it must keep the default")
	assert.True(t, cfg.CI.RecordRuns, "ci.record_runs was never written; it must keep the default")
	assert.True(t, cfg.Volatility.Enabled, "volatility.enabled was never written; it must keep the default")
}

// A `true` under a key defaulting false must still win, so the key set is not a
// one-way channel that only ever writes false.
func TestLoadDirIntoBoolTrueOverridesADefaultOffKey(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "magus.yaml"), []byte("dry_run: true\n"), 0o644))

	require.False(t, Defaults().DryRun, "precondition: dry_run defaults false")

	cfg, err := loadDirInto(Defaults(), dir)
	require.NoError(t, err)
	assert.True(t, cfg.DryRun)
}

// A key magus will not honor must fail the load, naming the key and the file it
// is in. The empty-log assert is the load-bearing half: a warning leaves the
// command at exit 0, and -s/--silent drops it entirely, so a change back to
// warning has to break a test rather than pass one.
// Not t.Parallel: it captures the slog default. See TestWarnIfConcurrencyHigh.
func TestUnknownKeyIsALoadError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "magus.yaml")
	require.NoError(t, os.WriteFile(path, []byte("concurrencyy: 4\n"), 0o644))

	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	_, err := loadDirInto(Defaults(), dir)
	require.Error(t, err, "an unknown key must fail the load, not warn and continue")
	assert.Contains(t, err.Error(), "concurrencyy", "the error must name the offending key")
	assert.Contains(t, err.Error(), path, "the error must name the file the key is in")
	assert.Empty(t, buf.String(), "the failure is the error, not a log line")

	_, err = LoadFile(path, false)
	assert.Error(t, err, "LoadFile rejects an unknown key whether or not it validates")
}

// yaml.Decoder reads the first document and stops, so a second one would apply
// nothing and say nothing.
func TestSecondDocumentIsALoadError(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "magus.yaml"), []byte("concurrency: 3\n---\nconcurrency: 9\n"), 0o644))

	_, err := loadDirInto(Defaults(), dir)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "single document")
}

// An empty or comment-only magus.yaml declares nothing, which is valid. yaml's
// decoder reports io.EOF for it, and reading that as a decode failure would turn
// every empty file into an unknown-key error.
//
// Not t.Parallel: its subtests capture the slog default. See TestWarnIfConcurrencyHigh.
func TestEmptyDocumentIsNotAnError(t *testing.T) {
	for name, content := range map[string]string{
		"empty":        "",
		"comment only": "# nothing configured here yet\n",
		"whitespace":   "\n\n",
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "magus.yaml")
			require.NoError(t, os.WriteFile(path, []byte(content), 0o644))

			var buf bytes.Buffer
			prev := slog.Default()
			slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})))
			t.Cleanup(func() { slog.SetDefault(prev) })

			cfg, err := loadDirInto(Defaults(), dir)
			require.NoError(t, err)
			assert.Equal(t, Defaults().Concurrency, cfg.Concurrency)
			assert.NotContains(t, buf.String(), "EOF", "an empty document must not warn about unexpected keys")

			strictCfg, err := LoadFile(path, true)
			require.NoError(t, err, "a strict load must accept an empty magus.yaml")
			assert.True(t, strictCfg.Server.Enabled, "an empty document leaves every default in place")
		})
	}
}

// TestUnknownKeyMessage pins the code and the whole rendered message, not a substring: what
// the rewrite buys is the shape a reader meets, and yaml.v3's own text already
// satisfied every Contains assert around it.
//
// A go.mod in the temp dir makes it a workspace root, so the path renders the
// way it does in a real workspace instead of as an absolute temp path.
//
// Not t.Parallel: each case chdirs into its workspace.
func TestUnknownKeyMessage(t *testing.T) {
	// Three list items each carrying two unknown keys, placed on the lines of the report
	// this layout was written against: env at 16, 21, 26 and base at 17, 22, 27.
	sample := "required_version: \">= 0.4.4\"\n" + strings.Repeat("#\n", 12) +
		"spells:\n  allow_shadow:\n" +
		strings.Repeat("    - env: X\n      base: y\n      name: spells/a\n      reason: r\n    #\n", 3)
	const devBuild, release = "v0.4.3-122-g1a2b3c4", "v0.4.3"
	cases := map[string]struct {
		doc        string
		sourceTree bool
		running    string
		want       string
	}{
		"the reported sample": {
			doc:        sample,
			sourceTree: true,
			running:    devBuild,
			want: "magus.yaml has keys this magus does not know:\n" +
				"  - spells.allow_shadow.env   (lines 16, 21, 26)\n" +
				"  - spells.allow_shadow.base  (lines 17, 22, 27; did you mean \"name\"?)\n" +
				"\n" +
				"This magus (v0.4.3-122-g1a2b3c4) is older than the workspace needs (>= 0.4.4).\n" +
				"Fix it:\n" +
				"  - released binary: magus self update\n" +
				"  - built from this checkout: ./magus run go-build .\n" +
				"  - if that cannot load the tree either: mv magus magus.old, then GOEXPERIMENT=jsonv2 go run -trimpath ./cmd/magus run go-build --no-cache .",
		},
		// A release build cannot be the one a checkout outgrew, so it is never told to
		// bootstrap; the checkout's own build is still a fix.
		"a release build in a source tree": {
			doc:        "required_version: \">= 0.4.4\"\nzzzzzzzz: 1\n",
			sourceTree: true,
			running:    release,
			want: "magus.yaml has a key this magus does not know:\n" +
				"  - zzzzzzzz  (line 2)\n" +
				"\n" +
				"This magus (v0.4.3) is older than the workspace needs (>= 0.4.4).\n" +
				"Fix it:\n" +
				"  - released binary: magus self update\n" +
				"  - built from this checkout: ./magus run go-build .",
		},
		"a dev build in a source tree": {
			doc:        "required_version: \">= 0.4.4\"\nzzzzzzzz: 1\n",
			sourceTree: true,
			running:    devBuild,
			want: "magus.yaml has a key this magus does not know:\n" +
				"  - zzzzzzzz  (line 2)\n" +
				"\n" +
				"This magus (v0.4.3-122-g1a2b3c4) is older than the workspace needs (>= 0.4.4).\n" +
				"Fix it:\n" +
				"  - released binary: magus self update\n" +
				"  - built from this checkout: ./magus run go-build .\n" +
				"  - if that cannot load the tree either: mv magus magus.old, then GOEXPERIMENT=jsonv2 go run -trimpath ./cmd/magus run go-build --no-cache .",
		},
		// The loader is never told the version, and neither side is invented.
		"a single unknown key with nothing known": {
			doc: "concurrency: 2\nzzzzzzzz: 1\n",
			want: "magus.yaml has a key this magus does not know:\n" +
				"  - zzzzzzzz  (line 2)\n" +
				"\n" +
				"This magus's version is unknown here, and the workspace declares no required_version floor.\n" +
				"Fix it:\n" +
				"  - released binary: magus self update",
		},
		// A near miss is a typo: no version gap, however old the build is.
		"a typo only": {
			doc:        "required_version: \">= 0.4.4\"\nconcurrencyy: 4\n",
			sourceTree: true,
			running:    devBuild,
			want: "magus.yaml has a key this magus does not know:\n" +
				"  - concurrencyy  (line 2; did you mean \"concurrency\"?)",
		},
		"two typos at different levels": {
			doc: "concurrencyy: 4\nsandbox:\n  modee: required\n",
			want: "magus.yaml has keys this magus does not know:\n" +
				"  - concurrencyy   (line 1; did you mean \"concurrency\"?)\n" +
				"  - sandbox.modee  (line 3; did you mean \"mode\"?)",
		},
		// One name at two levels is two keys, each named by its path.
		"one name at two levels": {
			doc: "zzzzzzzz: 1\nwatch:\n  zzzzzzzz: 2\n",
			want: "magus.yaml has keys this magus does not know:\n" +
				"  - zzzzzzzz        (line 1)\n" +
				"  - watch.zzzzzzzz  (line 3)\n" +
				"\n" +
				"This magus's version is unknown here, and the workspace declares no required_version floor.\n" +
				"Fix it:\n" +
				"  - released binary: magus self update",
		},
		// A retired key is misconfiguration, and the error names the key that took its
		// settings rather than guessing at a typo or blaming the binary.
		"retired key": {
			doc: "daemon:\n  idle_ttl: 1h\n",
			want: "magus.yaml has a key this magus does not know:\n" +
				"  - daemon  (line 1; renamed to \"server\")",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			dir := unknownKeyWorkspace(t, tc.doc, tc.sourceTree)

			_, err := loadDirInto(Defaults(), dir)
			if tc.running != "" {
				err = WithRunningVersion(err, tc.running)
			}
			require.ErrorIs(t, err, types.UnknownConfigKey)
			assert.Equal(t, types.DiagnosticErrorf(types.UnknownConfigKey, "%s", tc.want).Error(), err.Error())
		})
	}
}

// unknownKeyWorkspace writes doc as the magus.yaml of a fresh workspace root, with
// cmd/magus in it when sourceTree. The cwd stays elsewhere: the file's own workspace
// decides how its path renders and whether it is a source tree.
func unknownKeyWorkspace(t *testing.T, doc string, sourceTree bool) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module tmp\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "magus.yaml"), []byte(doc), 0o644))
	if sourceTree {
		require.NoError(t, os.MkdirAll(filepath.Join(dir, "cmd", "magus"), 0o755))
	}
	return dir
}

// TestUnknownKeysDropsNothing pins that grouping removes only repeated prose: twenty
// keys, each on three lines, all reach both the message and the structured error.
func TestUnknownKeysDropsNothing(t *testing.T) {
	t.Parallel()
	var item strings.Builder
	item.WriteString("    - name: spells/a\n      reason: r\n")
	for i := range 20 {
		fmt.Fprintf(&item, "      zq%02dxv: 1\n", i)
	}
	doc := "spells:\n  allow_shadow:\n" + strings.Repeat(item.String(), 3)
	dir := unknownKeyWorkspace(t, doc, false)

	_, err := loadDirInto(Defaults(), dir)
	require.ErrorIs(t, err, types.UnknownConfigKey)

	// Each item spans 22 lines from line 3: name, reason, then the twenty keys.
	want := UnknownKeysError{File: "magus.yaml"}
	lines := []string{"magus.yaml has keys this magus does not know:"}
	for i := range 20 {
		key := fmt.Sprintf("spells.allow_shadow.zq%02dxv", i)
		at := []int{5 + i, 27 + i, 49 + i}
		want.Keys = append(want.Keys, UnknownKey{Key: key, Lines: at})
		lines = append(lines, fmt.Sprintf("  - %s  (lines %d, %d, %d)", key, at[0], at[1], at[2]))
	}
	lines = append(lines, "", ward.StaleBuild{}.Advice())

	var ue *UnknownKeysError
	require.ErrorAs(t, err, &ue)
	assert.Equal(t, want, *ue)
	assert.Equal(t, types.DiagnosticErrorf(types.UnknownConfigKey, "%s", strings.Join(lines, "\n")).Error(), err.Error())
}

func TestWithRunningVersionLeavesOtherErrorsAlone(t *testing.T) {
	t.Parallel()
	other := types.DiagnosticErrorf(types.WorkspaceNeedsNewerMagus, "too old")
	assert.Equal(t, error(other), WithRunningVersion(other, "v0.4.3"))
	assert.NoError(t, WithRunningVersion(nil, "v0.4.3"))
}

// A type mismatch is not an unknown key, and rewriting half of yaml's report
// would drop the half that says what is wrong.
func TestTypeMismatchKeepsYamlsOwnReport(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "magus.yaml")
	require.NoError(t, os.WriteFile(path, []byte("concurrency: nope\n"), 0o644))

	_, err := loadDirInto(Defaults(), dir)
	require.Error(t, err)
	assert.Contains(t, err.Error(), path)
	assert.Contains(t, err.Error(), "cannot unmarshal")
}

// jobs.stale_after defaults to 2h, and a written zero is never rather than the default:
// non-zero-wins would read `0` as absent.
func TestJobsStaleAfterHonorsAWrittenZero(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		yaml string
		want string
	}{
		"absent is the default":   {want: "2h0m0s"},
		"a written zero is never": {yaml: "jobs:\n  stale_after: 0s\n", want: "0s"},
		"a written window wins":   {yaml: "jobs:\n  stale_after: 30m\n", want: "30m0s"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			if tc.yaml != "" {
				require.NoError(t, os.WriteFile(filepath.Join(root, Filename), []byte(tc.yaml), 0o644))
			}
			cfg, err := LoadWorkspaceOnly(root)
			require.NoError(t, err)
			assert.Equal(t, tc.want, cfg.Jobs.StaleAfter.String())
		})
	}
}

func TestJobsStaleAfterRefusesANegativeWindow(t *testing.T) {
	t.Parallel()

	cfg := Defaults()
	cfg.Jobs.StaleAfter = -1
	assert.ErrorContains(t, Validate(cfg), "stale_after")
}

// queue.carry_approvals defaults to every tier but code when unset, an empty list is the
// strictest policy, and a name that is no tier, or is code, stops the load with MGS1050
// naming each entry's line.
func TestQueueCarryApprovals(t *testing.T) {
	for name, tc := range map[string]struct {
		yaml    string
		want    queuetypes.CarryPolicy
		wantErr string
	}{
		"unset":  {yaml: "queue: {}\n", want: queuetypes.DefaultCarryPolicy()},
		"empty":  {yaml: "queue:\n  carry_approvals: []\n", want: queuetypes.CarryPolicy{}},
		"listed": {yaml: "queue:\n  carry_approvals: [rebase, prose]\n", want: queuetypes.CarryPolicy{queuetypes.CarryRebase, queuetypes.CarryProse}},
		"unknown and code": {yaml: "queue:\n  carry_approvals:\n    - prose\n    - docs\n    - code\n",
			wantErr: "line 4: queue.carry_approvals: \"docs\" is not an approval carry tier (want some of rebase, generated, prose, comment-only)\n" +
				"line 5: queue.carry_approvals: \"code\" never carries an approval: a reviewer has to see a code change (want some of rebase, generated, prose, comment-only)"},
		"not a list": {yaml: "queue:\n  carry_approvals: prose\n", wantErr: "line 2: queue.carry_approvals must be a list of tiers"},
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			require.NoError(t, os.WriteFile(filepath.Join(root, Filename), []byte(tc.yaml), 0o644))
			cfg, err := LoadWorkspaceOnly(root)
			if tc.wantErr != "" {
				require.ErrorIs(t, err, types.CarryApprovalsInvalid)
				assert.Equal(t, "config: "+filepath.Join(root, Filename)+": "+types.DiagnosticErrorf(types.CarryApprovalsInvalid, "%s", tc.wantErr).Error(), err.Error())
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, cfg.Queue.CarryApprovals.Policy())
		})
	}
}

// FuzzLoadFile feeds arbitrary bytes through the YAML loader. The
// invariant is "no panic": malformed YAML and unknown-key strict
// failures must return errors, not crash. Strict mode also enforces
// schema validation, exercising the validate package via Validate().
func FuzzLoadFile(f *testing.F) {
	for _, seed := range [][]byte{
		[]byte(""),
		[]byte("cache:\n  mode: auto\n"),
		[]byte("cache:\n  mode: bogus\n"),
		[]byte("vcs:\n  enabled: true\n  command_name: git\n"),
		[]byte("concurrency: -1\n"),
		[]byte("log:\n  format: pretty\n"),
		[]byte("\xff\xfe\x00\x00binary garbage"),
		[]byte("a: !!binary unparsable"),
		[]byte("---\n---\nmultiple-docs"),
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		// LoadFile is path-based; persist the fuzz input and let the
		// loader read it. t.TempDir handles cleanup.
		path := filepath.Join(t.TempDir(), "magus.yaml")
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
		// Both modes; strict adds KnownFields + Validate. Either path
		// must terminate in (Config, error) and never panic.
		_, _ = LoadFile(path, false)
		_, _ = LoadFile(path, true)
	})
}
