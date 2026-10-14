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
		OS:   CacheIncludeFlag{Enabled: boolPtr(true)},
		Arch: CacheIncludeFlag{Enabled: boolPtr(true)},
	}}
	assert.True(t, on.IncludeOS())
	assert.True(t, on.IncludeArch())

	off := Cache{Include: CacheInclude{
		OS:   CacheIncludeFlag{Enabled: boolPtr(false)},
		Arch: CacheIncludeFlag{Enabled: boolPtr(false)},
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

func TestDefaults_ConformIsValidAndFlagless(t *testing.T) {
	t.Parallel()
	cfg := Defaults()
	require.NoError(t, Validate(cfg))
	require.NoError(t, cfg.Knowledge.Conform.Options().Validate())

	field, ok := reflect.TypeOf(cfg.Knowledge).FieldByName("Conform")
	require.True(t, ok)
	assert.Equal(t, "-", field.Tag.Get("cli"), "a ratchet is committed policy: no flag, no env var")
}

func TestConformDefaults(t *testing.T) {
	t.Parallel()
	c := Defaults().Knowledge.Conform
	assert.Equal(t, ConformGatesConfig{MinCohort: 5, MinShare: 0.8, Bar: 0.5, MaxFindings: 5}, c.Gates)
	assert.Equal(t, 30, c.MinSample)
	assert.InDelta(t, 0.05, c.Step, 0)
	assert.Empty(t, c.Ratchet, "the ratchet is opt-in: a workspace names its own record")
	assert.Equal(t, []string{"libs/gopherbuzz/**"}, c.Exclude)

	assert.Equal(t, map[types.ConformPopulation]ConformLimitConfig{
		types.ConformPopSource: {Percentile: 95, Anchor: 2200},
		types.ConformPopMain:   {Percentile: 95, Anchor: 3000},
		types.ConformPopTest:   {Percentile: 99, Anchor: 1900},
	}, c.Checks[types.ConformFileLines])
	assert.Equal(t, 92, c.Checks[types.ConformFuncLines][types.ConformPopSource].Anchor)
	assert.Equal(t, 140, c.Checks[types.ConformFuncLines][types.ConformPopMain].Anchor)
	assert.Equal(t, 73, c.Checks[types.ConformFuncLines][types.ConformPopTest].Anchor)
	assert.Equal(t, 49, c.Checks[types.ConformTypeMethods][types.ConformPopSource].Anchor)
	assert.Equal(t, 22, c.Checks[types.ConformTypeMethods][types.ConformPopMain].Anchor)
	assert.Equal(t, 32, c.Checks[types.ConformTypeMethods][types.ConformPopTest].Anchor)
	assert.Equal(t, 82, c.Checks[types.ConformFileSymbols][types.ConformPopSource].Anchor)
	assert.Equal(t, ConformLimitConfig{Percentile: 50, Anchor: 80}, c.Checks[types.ConformCloneTokens][types.ConformPopSource])
	assert.Equal(t, ConformLimitConfig{Percentile: 95, Anchor: 62, Scopes: map[string]int{"cmd/magus": 89}}, c.Checks[types.ConformDepFanout][types.ConformPopSource])
}

func TestConformDefaultsAreNotShared(t *testing.T) {
	t.Parallel()
	a, b := Defaults(), Defaults()
	a.Knowledge.Conform.Checks[types.ConformFileLines][types.ConformPopSource] = ConformLimitConfig{Percentile: 1, Anchor: 1}
	a.Knowledge.Conform.Exclude[0] = "changed"
	assert.Equal(t, 2200, b.Knowledge.Conform.Checks[types.ConformFileLines][types.ConformPopSource].Anchor)
	assert.Equal(t, "libs/gopherbuzz/**", b.Knowledge.Conform.Exclude[0])
}

func TestConformOptions(t *testing.T) {
	t.Parallel()
	c := ConformConfig{
		Ratchet:   "hack/policy/conform.json",
		MinSample: 12,
		Step:      0.1,
		Exclude:   []string{"vendor/**"},
		Gates:     ConformGatesConfig{MinCohort: 3, MinShare: 0.7, Bar: 0.4, MaxFindings: 9},
		Checks: map[types.ConformMetric]map[types.ConformPopulation]ConformLimitConfig{
			types.ConformFileLines: {types.ConformPopTest: {Percentile: 99, Anchor: 1900}},
			types.ConformDepFanout: {types.ConformPopSource: {Percentile: 95, Anchor: 62, Scopes: map[string]int{"cmd/magus": 89}}},
		},
	}
	fileLines := types.ConformKey{Metric: types.ConformFileLines, Population: types.ConformPopTest}
	fanout := types.ConformKey{Metric: types.ConformDepFanout, Population: types.ConformPopSource}
	scoped := types.ConformKey{Metric: types.ConformDepFanout, Population: types.ConformPopSource, Scope: "cmd/magus"}
	want := types.ConformOptions{
		Limits: map[types.ConformKey]types.ConformLimit{
			fileLines: {Percentile: 99, Anchor: 1900},
			fanout:    {Percentile: 95, Anchor: 62},
			scoped:    {Percentile: 95, Anchor: 89},
		},
		RatchetPath: "hack/policy/conform.json",
		MinSample:   12,
		Step:        0.1,
		Exclude:     []string{"vendor/**"},
		Gates:       types.ConformGates{MinCohort: 3, MinShare: 0.7, Bar: 0.4, MaxFindings: 9},
	}
	got := c.Options()
	assert.Equal(t, want, got)

	got.Exclude[0] = "changed"
	assert.Equal(t, "vendor/**", c.Exclude[0], "the options own their exclusions")
}

// The cmd/magus pin lives above the population's anchor as its own scoped value, so the
// anchor every other package answers to stays tight.
func TestConformScopedLimitKeepsTheAnchorTight(t *testing.T) {
	t.Parallel()
	scoped := types.ConformKey{Metric: types.ConformDepFanout, Population: types.ConformPopSource, Scope: "cmd/magus"}
	other := types.ConformKey{Metric: types.ConformDepFanout, Population: types.ConformPopSource, Scope: "internal/server"}

	o := Defaults().Knowledge.Conform.Options()
	o.RatchetPath = "hack/policy/conform.json"
	o.Ratchet = types.ConformRatchet{Schema: 1, Thresholds: []types.ConformThreshold{
		{ConformKey: scoped, Value: 89, N: 1, Why: "pin", Line: 4},
	}}
	require.NoError(t, o.Validate())

	v, src := o.Effective(scoped)
	assert.Equal(t, 89, v)
	assert.Equal(t, types.ConformFromRatchet, src)

	v, src = o.Effective(other)
	assert.Equal(t, 62, v)
	assert.Equal(t, types.ConformFromAnchor, src)

	o.Ratchet.Thresholds[0].Value = 90
	assert.ErrorContains(t, o.Validate(), "looser than its anchor 89")
}

func TestConformLoadsFromYAML(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), Filename)
	require.NoError(t, os.WriteFile(path, []byte(`knowledge:
  conform:
    ratchet: hack/policy/conform.json
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
	c := cfg.Knowledge.Conform
	assert.Equal(t, "hack/policy/conform.json", c.Ratchet)
	assert.Equal(t, 10, c.MinSample)
	assert.InDelta(t, 0.05, c.Step, 0, "keys left out keep their defaults")
	assert.Equal(t, map[types.ConformMetric]map[types.ConformPopulation]ConformLimitConfig{
		types.ConformDepFanout: {types.ConformPopSource: {Percentile: 90, Anchor: 20, Scopes: map[string]int{"cmd/tool": 31}}},
	}, c.Checks, "written checks replace the defaults")

	scoped := types.ConformKey{Metric: types.ConformDepFanout, Population: types.ConformPopSource, Scope: "cmd/tool"}
	assert.Equal(t, types.ConformLimit{Percentile: 90, Anchor: 31}, c.Options().Limits[scoped])
}

func TestConformLoadRefusesAnUnknownMetric(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), Filename)
	require.NoError(t, os.WriteFile(path, []byte(`knowledge:
  conform:
    checks:
      file-line:
        source: {percentile: 95, anchor: 100}
`), 0o644))
	_, err := LoadFile(path, true)
	require.ErrorContains(t, err, "knowledge.conform.checks.file-line")
	assert.ErrorContains(t, err, "file-lines")
}
