package config

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/egladman/magus/internal/stamp"
	"github.com/egladman/magus/types"
)

// Note: ApplyEnv tests moved to internal/config/gen/env_test.go.

func TestDefaults_VolatilityEnabled(t *testing.T) {
	t.Parallel()
	cfg := Defaults()
	assert.True(t, cfg.Volatility.Enabled, "Defaults().Volatility.Enabled should be true")
}

func TestJobsClaimBounds(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		yaml         string
		files, projs int
		wantErr      string
	}{
		"absent takes the defaults":    {files: 50, projs: 1},
		"written numbers are taken":    {yaml: "jobs:\n  max_claim_files: 80\n  max_claim_projects: 3\n", files: 80, projs: 3},
		"one written leaves the other": {yaml: "jobs:\n  max_claim_files: 80\n", files: 80, projs: 1},
		"a written zero is an error":   {yaml: "jobs:\n  max_claim_files: 0\n", wantErr: "max_claim_files"},
		"a negative is an error":       {yaml: "jobs:\n  max_claim_projects: -1\n", wantErr: "max_claim_projects"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			if tc.yaml != "" {
				require.NoError(t, os.WriteFile(filepath.Join(root, Filename), []byte(tc.yaml), 0o644))
			}
			cfg, err := LoadWorkspaceOnly(root)
			require.NoError(t, err)
			if tc.wantErr != "" {
				assert.ErrorContains(t, Validate(cfg), tc.wantErr)
				return
			}
			require.NoError(t, Validate(cfg))
			want := Defaults().Jobs
			want.MaxClaimFiles, want.MaxClaimProjects = tc.files, tc.projs
			assert.Equal(t, want, cfg.Jobs)
		})
	}
}

// TestCacheIncludeDefaultsOff pins the OFF default deliberately, against the pull to
// "fix" it toward the safe-looking direction. Keying the host platform is not what makes
// a replay safe (Manifest.Platform refuses a cross-platform hit whatever these say), and
// a key free of host facts is what lets an output ref name the same run on every machine
// (see internal/cache's TestCacheKeyUnaffectedByPlatform). Turning these on by default
// would break that and rekey every existing entry.
func TestCacheIncludeDefaultsOff(t *testing.T) {
	t.Parallel()
	cfg := Defaults()
	assert.False(t, cfg.Cache.IncludeOS(), "cache.include.os.enabled must default off")
	assert.False(t, cfg.Cache.IncludeArch(), "cache.include.arch.enabled must default off")
}

func TestCacheIncludeExplicit(t *testing.T) {
	t.Parallel()
	on := Cache{Include: CacheInclude{
		OS:   CacheIncludeFlag{Enabled: new(true)},
		Arch: CacheIncludeFlag{Enabled: new(true)},
	}}
	assert.True(t, on.IncludeOS())
	assert.True(t, on.IncludeArch())

	off := Cache{Include: CacheInclude{
		OS:   CacheIncludeFlag{Enabled: new(false)},
		Arch: CacheIncludeFlag{Enabled: new(false)},
	}}
	assert.False(t, off.IncludeOS())
	assert.False(t, off.IncludeArch())
}

// TestSave_Concurrent verifies that 10 goroutines concurrently calling Save
// on the same path do not panic and leave a valid YAML file behind.
func TestSave_Concurrent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "magus.yaml")

	const goroutines = 10
	var wg sync.WaitGroup
	wg.Add(goroutines)
	errs := make([]error, goroutines)
	for i := 0; i < goroutines; i++ {
		i := i
		go func() {
			defer wg.Done()
			errs[i] = Save(stamp.Judge{}, path, "concurrency", fmt.Sprintf("%d", i+1))
		}()
	}
	wg.Wait()

	// At least one write must have succeeded.
	anyOK := false
	for _, err := range errs {
		if err == nil {
			anyOK = true
			break
		}
	}
	require.True(t, anyOK, "all concurrent Save calls failed")

	// Final file must be valid YAML.
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	var m map[string]interface{}
	require.NoError(t, yaml.Unmarshal(data, &m), "final file is not valid YAML:\n%s", data)
}

func TestDefaults_ConformanceIsValidAndFlagless(t *testing.T) {
	t.Parallel()
	cfg := Defaults()
	require.NoError(t, Validate(cfg))
	require.NoError(t, cfg.Knowledge.Conformance.Options().Validate())

	field, ok := reflect.TypeOf(cfg.Knowledge).FieldByName("Conformance")
	require.True(t, ok)
	assert.Equal(t, "-", field.Tag.Get("cli"), "a ratchet is committed policy: no flag, no env var")
}

func TestConformanceDefaults(t *testing.T) {
	t.Parallel()
	c := Defaults().Knowledge.Conformance
	// The ratchet is opt-in and an exclusion names one workspace's layout, so neither ships.
	assert.Equal(t, ConformanceConfig{
		MinSample: 30,
		Step:      0.05,
		Gates:     ConformanceGatesConfig{MinCohort: 5, MinShare: 0.8, Bar: 0.5, MaxFindings: 5},
		Checks:    c.Checks, // pinned below
	}, c)

	assert.Equal(t, map[types.ConformancePopulation]ConformanceLimitConfig{
		types.ConformancePopSource: {Percentile: 95, Anchor: 2200},
		types.ConformancePopMain:   {Percentile: 95, Anchor: 3000},
		types.ConformancePopTest:   {Percentile: 99, Anchor: 1900},
	}, c.Checks[types.ConformanceFileLines])
	assert.Equal(t, 92, c.Checks[types.ConformanceFuncLines][types.ConformancePopSource].Anchor)
	assert.Equal(t, 140, c.Checks[types.ConformanceFuncLines][types.ConformancePopMain].Anchor)
	assert.Equal(t, 73, c.Checks[types.ConformanceFuncLines][types.ConformancePopTest].Anchor)
	assert.Equal(t, 49, c.Checks[types.ConformanceTypeMethods][types.ConformancePopSource].Anchor)
	assert.Equal(t, 22, c.Checks[types.ConformanceTypeMethods][types.ConformancePopMain].Anchor)
	assert.Equal(t, 32, c.Checks[types.ConformanceTypeMethods][types.ConformancePopTest].Anchor)
	assert.Equal(t, 82, c.Checks[types.ConformanceFileSymbols][types.ConformancePopSource].Anchor)
	assert.Equal(t, ConformanceLimitConfig{Percentile: 50, Anchor: 80}, c.Checks[types.ConformanceCloneTokens][types.ConformancePopSource])
	assert.Equal(t, ConformanceLimitConfig{Percentile: 95, Anchor: 62}, c.Checks[types.ConformanceDepFanout][types.ConformancePopSource],
		"a scope names one workspace's directory, so it ships in none")
	assert.Equal(t, map[types.ConformancePopulation]ConformanceLimitConfig{
		types.ConformancePopSource: {Percentile: 99, Anchor: 7},
		types.ConformancePopMain:   {Percentile: 99, Anchor: 7},
	}, c.Checks[types.ConformanceCallSequence], "7 sits below the run of 8 that caught B1, B7 and B15; tests are never measured")
	assert.Equal(t, map[types.ConformanceMetric]map[types.ConformancePopulation]ConformanceLimitConfig{
		types.ConformanceSharedBody:   {types.ConformancePopSource: {Percentile: 90, Anchor: 94}},
		types.ConformanceSharedReach:  {types.ConformancePopSource: {Percentile: 95, Anchor: 7}},
		types.ConformanceCallerSpread: {types.ConformancePopSource: {Percentile: 95, Anchor: 3}},
		types.ConformanceModeKnobs:    {types.ConformancePopSource: {Percentile: 0, Anchor: 0}},
	}, map[types.ConformanceMetric]map[types.ConformancePopulation]ConformanceLimitConfig{
		types.ConformanceSharedBody:   c.Checks[types.ConformanceSharedBody],
		types.ConformanceSharedReach:  c.Checks[types.ConformanceSharedReach],
		types.ConformanceCallerSpread: c.Checks[types.ConformanceCallerSpread],
		types.ConformanceModeKnobs:    c.Checks[types.ConformanceModeKnobs],
	}, "the coupling keys pool source and main; a knob past the rule is a list item")
	assert.Equal(t, map[types.ConformancePopulation]ConformanceLimitConfig{
		types.ConformancePopSource: {Percentile: 95, Anchor: 100, MinSubject: 5},
	}, c.Checks[types.ConformanceAliasShare])
	assert.Equal(t, map[types.ConformancePopulation]ConformanceLimitConfig{
		types.ConformancePopSource: {Percentile: 99, Anchor: 62, MinSubject: 5},
	}, c.Checks[types.ConformanceNameClash], "62 is internal/queue/types, the one clash measured")
}

func TestConformanceDefaultsAreNotShared(t *testing.T) {
	t.Parallel()
	a, b := Defaults(), Defaults()
	a.Knowledge.Conformance.Checks[types.ConformanceFileLines][types.ConformancePopSource] = ConformanceLimitConfig{Percentile: 1, Anchor: 1}
	assert.Equal(t, 2200, b.Knowledge.Conformance.Checks[types.ConformanceFileLines][types.ConformancePopSource].Anchor)
}

func TestConformanceOptions(t *testing.T) {
	t.Parallel()
	c := ConformanceConfig{
		Ratchet:   "hack/policy/conformance.json",
		MinSample: 12,
		Step:      0.1,
		Exclude:   []string{"vendor/**"},
		Gates:     ConformanceGatesConfig{MinCohort: 3, MinShare: 0.7, Bar: 0.4, MaxFindings: 9},
		Checks: map[types.ConformanceMetric]map[types.ConformancePopulation]ConformanceLimitConfig{
			types.ConformanceFileLines:  {types.ConformancePopTest: {Percentile: 99, Anchor: 1900}},
			types.ConformanceDepFanout:  {types.ConformancePopSource: {Percentile: 95, Anchor: 62, Scopes: map[string]int{"cmd/magus": 89}}},
			types.ConformanceAliasShare: {types.ConformancePopSource: {Percentile: 95, Anchor: 100, MinSubject: 5}},
		},
	}
	fileLines := types.ConformanceKey{Metric: types.ConformanceFileLines, Population: types.ConformancePopTest}
	fanout := types.ConformanceKey{Metric: types.ConformanceDepFanout, Population: types.ConformancePopSource}
	scoped := types.ConformanceKey{Metric: types.ConformanceDepFanout, Population: types.ConformancePopSource, Scope: "cmd/magus"}
	alias := types.ConformanceKey{Metric: types.ConformanceAliasShare, Population: types.ConformancePopSource}
	want := types.ConformanceOptions{
		Limits: map[types.ConformanceKey]types.ConformanceLimit{
			fileLines: {Percentile: 99, Anchor: 1900},
			fanout:    {Percentile: 95, Anchor: 62},
			scoped:    {Percentile: 95, Anchor: 89},
			alias:     {Percentile: 95, Anchor: 100, MinSubject: 5},
		},
		RatchetPath: "hack/policy/conformance.json",
		MinSample:   12,
		Step:        0.1,
		Exclude:     []string{"vendor/**"},
		Gates:       types.ConformanceGates{MinCohort: 3, MinShare: 0.7, Bar: 0.4, MaxFindings: 9},
	}
	got := c.Options()
	assert.Equal(t, want, got)

	got.Exclude[0] = "changed"
	assert.Equal(t, "vendor/**", c.Exclude[0], "the options own their exclusions")
}

// A scope pin lives above the population's anchor as its own scoped value, so the anchor
// every other package answers to stays tight.
func TestConformanceScopedLimitKeepsTheAnchorTight(t *testing.T) {
	t.Parallel()
	scoped := types.ConformanceKey{Metric: types.ConformanceDepFanout, Population: types.ConformancePopSource, Scope: "cmd/magus"}
	other := types.ConformanceKey{Metric: types.ConformanceDepFanout, Population: types.ConformancePopSource, Scope: "internal/server"}

	c := Defaults().Knowledge.Conformance
	c.Checks[types.ConformanceDepFanout][types.ConformancePopSource] = ConformanceLimitConfig{Percentile: 95, Anchor: 62, Scopes: map[string]int{"cmd/magus": 89}}
	o := c.Options()
	o.RatchetPath = "hack/policy/conformance.json"
	o.Ratchet = types.ConformanceRatchet{Schema: 1, Thresholds: []types.ConformanceThreshold{
		{ConformanceKey: scoped, Value: 89, N: 1, Why: "pin", Line: 4},
	}}
	require.NoError(t, o.Validate())

	v, src := o.Effective(scoped)
	assert.Equal(t, 89, v)
	assert.Equal(t, types.ConformanceFromRatchet, src)

	v, src = o.Effective(other)
	assert.Equal(t, 62, v)
	assert.Equal(t, types.ConformanceFromAnchor, src)

	o.Ratchet.Thresholds[0].Value = 90
	assert.ErrorContains(t, o.Validate(), "looser than its anchor 89")
}

func TestConformanceLoadsFromYAML(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), Filename)
	require.NoError(t, os.WriteFile(path, []byte(`knowledge:
  conformance:
    ratchet: hack/policy/conformance.json
    min_sample: 10
    checks:
      dep-fanout:
        source:
          percentile: 90
          anchor: 20
          scopes:
            cmd/tool: 31
`), 0o644))
	cfg, err := LoadFile(path, true)
	require.NoError(t, err)
	c := cfg.Knowledge.Conformance
	assert.Equal(t, ConformanceConfig{
		Ratchet:   "hack/policy/conformance.json",
		MinSample: 10,
		Step:      0.05,
		Gates:     ConformanceGatesConfig{MinCohort: 5, MinShare: 0.8, Bar: 0.5, MaxFindings: 5},
		Checks: map[types.ConformanceMetric]map[types.ConformancePopulation]ConformanceLimitConfig{
			types.ConformanceDepFanout: {types.ConformancePopSource: {Percentile: 90, Anchor: 20, Scopes: map[string]int{"cmd/tool": 31}}},
		},
	}, c, "keys left out keep their defaults and written checks replace the defaults")

	scoped := types.ConformanceKey{Metric: types.ConformanceDepFanout, Population: types.ConformancePopSource, Scope: "cmd/tool"}
	assert.Equal(t, types.ConformanceLimit{Percentile: 90, Anchor: 31}, c.Options().Limits[scoped])
}

func TestConformanceLoadRefusesAnUnknownMetric(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), Filename)
	require.NoError(t, os.WriteFile(path, []byte(`knowledge:
  conformance:
    checks:
      file-line:
        source: {percentile: 95, anchor: 100}
`), 0o644))
	_, err := LoadFile(path, true)
	require.ErrorContains(t, err, "knowledge.conformance.checks.file-line")
	assert.ErrorContains(t, err, "file-lines")
}

func TestJobsWideningsCeiling(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		yaml    string
		want    int
		wantErr string
	}{
		"absent takes the default":   {want: 10},
		"a written number is taken":  {yaml: "jobs:\n  max_widenings_ceiling: 4\n", want: 4},
		"a written zero is an error": {yaml: "jobs:\n  max_widenings_ceiling: 0\n", wantErr: "max_widenings_ceiling"},
		"a negative is an error":     {yaml: "jobs:\n  max_widenings_ceiling: -2\n", wantErr: "max_widenings_ceiling"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			if tc.yaml != "" {
				require.NoError(t, os.WriteFile(filepath.Join(root, Filename), []byte(tc.yaml), 0o644))
			}
			cfg, err := LoadWorkspaceOnly(root)
			require.NoError(t, err)
			if tc.wantErr != "" {
				assert.ErrorContains(t, Validate(cfg), tc.wantErr)
				return
			}
			require.NoError(t, Validate(cfg))
			assert.Equal(t, tc.want, cfg.Jobs.MaxWideningsCeiling)
		})
	}
}
