package config

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/types"
)

func TestValidate_ValidMinimal(t *testing.T) {
	// ci.max_shards must be -1 (unlimited) or in [1,256]; use -1 for a minimal valid config.
	// The duplication and conform thresholds have no valid zero, so they carry their defaults.
	cfg := Config{CI: CI{MaxShards: -1}, Knowledge: Defaults().Knowledge}
	require.NoError(t, Validate(cfg), "Validate(minimal valid Config)")
}

func TestValidate_InvalidConcurrency(t *testing.T) {
	cfg := Config{Concurrency: -1}
	assert.Error(t, Validate(cfg), "Validate(Concurrency=-1): expected error")
}

// jsonl withholds every target's output for a report stream only -o jsonl opens, so a
// magus.yaml asking for it would drop per-target results with nowhere to send them.
func TestValidate_LogFormatRefusesJSONL(t *testing.T) {
	cfg := Config{CI: CI{MaxShards: -1}, Knowledge: Defaults().Knowledge}
	cfg.Log.Format = "jsonl"
	require.ErrorContains(t, Validate(cfg), "log.format")
	cfg.Log.Format = "json"
	require.NoError(t, Validate(cfg))
}

func TestValidationError_Error(t *testing.T) {
	cfg := Config{Concurrency: -5}
	err := Validate(cfg)
	if err == nil {
		t.Skip("need a validation error to test ValidationError type")
	}
	var ve *ValidationError
	require.ErrorAs(t, err, &ve)
	assert.NotEmpty(t, ve.Error(), "ValidationError.Error() is empty")
}

// A registry entry that no reference could match, or that names no credential, fails
// at load: a silently unused credential is a push that later reads as a permissions bug.
func TestValidate_SpellRegistries(t *testing.T) {
	valid := Config{CI: CI{MaxShards: -1}, Knowledge: Defaults().Knowledge}
	valid.Spells.Registries = []SpellRegistry{
		{Host: "ghcr.io", Username: "ci", Password: "GITHUB_TOKEN"},
		{Host: "localhost:5000", Username: "me", Password: "op://dev/registry/token"},
	}
	require.NoError(t, Validate(valid))

	for name, tc := range map[string]struct {
		regs []SpellRegistry
		want FieldFailure
	}{
		"scheme": {
			regs: []SpellRegistry{{Host: "https://ghcr.io", Username: "u", Password: "P"}},
			want: FieldFailure{Field: "spells.registries[0].host", Tag: "registry_host", Value: "https://ghcr.io"},
		},
		"uppercase": {
			regs: []SpellRegistry{{Host: "GHCR.io", Username: "u", Password: "P"}},
			want: FieldFailure{Field: "spells.registries[0].host", Tag: "registry_host", Value: "GHCR.io"},
		},
		"no password ref": {
			regs: []SpellRegistry{{Host: "ghcr.io", Username: "u"}},
			want: FieldFailure{Field: "spells.registries[0].password", Tag: "required"},
		},
		"duplicate host": {
			regs: []SpellRegistry{{Host: "ghcr.io", Username: "a", Password: "A"}, {Host: "ghcr.io", Username: "b", Password: "B"}},
			want: FieldFailure{Field: "spells.registries", Tag: "unique", Param: "Host"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			cfg := valid
			cfg.Spells.Registries = tc.regs
			var ve *ValidationError
			require.ErrorAs(t, Validate(cfg), &ve)
			require.Len(t, ve.Failures, 1)
			got := ve.Failures[0]
			if tc.want.Tag == "unique" {
				got.Value = ""
			}
			assert.Equal(t, tc.want, got)
		})
	}
}

// A spell declaration magus could not honor fails at load: an entry the resolver
// would skip is a remote import that later reads as undeclared.
func TestValidate_SpellImports(t *testing.T) {
	valid := Config{CI: CI{MaxShards: -1}, Knowledge: Defaults().Knowledge}
	valid.Spells.Imports = map[string]SpellImport{
		"ghcr.io/egladman/magus/spells/cursor": {Tag: "1.4"},
		"localhost:5000/team/spells/lint":      {Path: "vendor/lint"},
		"magus/spell/go":                       {Path: "spells/go"},
	}
	require.NoError(t, Validate(valid))

	for name, tc := range map[string]struct {
		imports map[string]SpellImport
		want    []FieldFailure
	}{
		"misspelled reserved key": {
			imports: map[string]SpellImport{"registires": {}},
			want:    []FieldFailure{{Field: "spells.registires", Tag: "spell_import_path", Param: "registries", Value: "registires"}},
		},
		"workspace path": {
			imports: map[string]SpellImport{"spells/lint": {Path: "spells/lint"}},
			want:    []FieldFailure{{Field: "spells.spells/lint", Tag: "spell_import_path", Value: "spells/lint"}},
		},
		"uppercase registry path": {
			imports: map[string]SpellImport{"ghcr.io/Team/lint": {Tag: "1"}},
			want:    []FieldFailure{{Field: "spells.ghcr.io/Team/lint", Tag: "spell_import_path", Value: "ghcr.io/Team/lint"}},
		},
		"tag and path": {
			imports: map[string]SpellImport{"ghcr.io/team/lint": {Tag: "1", Path: "vendor/lint"}},
			want:    []FieldFailure{{Field: "spells.ghcr.io/team/lint", Tag: "spell_tag_or_path"}},
		},
		"neither tag nor path": {
			imports: map[string]SpellImport{"ghcr.io/team/lint": {}},
			want:    []FieldFailure{{Field: "spells.ghcr.io/team/lint", Tag: "spell_tag_or_path"}},
		},
		"bad tag": {
			imports: map[string]SpellImport{"ghcr.io/team/lint": {Tag: "-1.4"}},
			want:    []FieldFailure{{Field: "spells.ghcr.io/team/lint.tag", Tag: "spell_tag", Value: "-1.4"}},
		},
		"embedded with a tag": {
			imports: map[string]SpellImport{"magus/spell/go": {Tag: "1.4"}},
			want: []FieldFailure{
				{Field: "spells.magus/spell/go.path", Tag: "required"},
				{Field: "spells.magus/spell/go.tag", Tag: "spell_embedded_tag", Value: "1.4"},
			},
		},
		"override escapes the workspace": {
			imports: map[string]SpellImport{"magus/spell/go": {Path: "../go"}},
			want:    []FieldFailure{{Field: "spells.magus/spell/go.path", Tag: "spell_override_path", Value: "../go"}},
		},
		"nested remote paths": {
			imports: map[string]SpellImport{"ghcr.io/team/lint": {Tag: "1"}, "ghcr.io/team/lint/extra": {Tag: "1"}},
			want:    []FieldFailure{{Field: "spells.ghcr.io/team/lint/extra", Tag: "spell_path_nested", Param: "ghcr.io/team/lint", Value: "ghcr.io/team/lint/extra"}},
		},
	} {
		t.Run(name, func(t *testing.T) {
			cfg := valid
			cfg.Spells.Imports = tc.imports
			var ve *ValidationError
			require.ErrorAs(t, Validate(cfg), &ve)
			assert.Equal(t, tc.want, ve.Failures)
		})
	}
}

// Each cache tier is written independently. The local tier off with the remote tier
// explicitly on is the one incoherent pair, refused rather than quietly writing nothing.
func TestValidate_CacheTierWrites(t *testing.T) {
	on, off := boolPtr(true), boolPtr(false)
	for name, tc := range map[string]struct {
		local, remote         *bool
		wantLocal, wantRemote bool
		wantFailures          []FieldFailure
	}{
		"defaults":                 {wantLocal: true, wantRemote: true},
		"remote follows local off": {local: off},
		"local tier only":          {remote: off, wantLocal: true},
		"both on":                  {local: on, remote: on, wantLocal: true, wantRemote: true},
		"both off":                 {local: off, remote: off},
		"remote on without local": {
			local: off, remote: on,
			wantFailures: []FieldFailure{{Field: "cache.remote.write.enabled", Tag: "cache_write_required", Value: "true"}},
		},
	} {
		t.Run(name, func(t *testing.T) {
			cfg := Config{CI: CI{MaxShards: -1}, Knowledge: Defaults().Knowledge}
			cfg.Cache.Write.Enabled = tc.local
			cfg.Cache.Remote.Write.Enabled = tc.remote
			assert.Equal(t, tc.wantLocal, cfg.Cache.WriteEnabled(), "WriteEnabled")
			assert.Equal(t, tc.wantRemote, cfg.Cache.RemoteWriteEnabled(), "RemoteWriteEnabled")

			err := Validate(cfg)
			if tc.wantFailures == nil {
				require.NoError(t, err)
				return
			}
			var ve *ValidationError
			require.ErrorAs(t, err, &ve)
			assert.Equal(t, tc.wantFailures, ve.Failures)
			assert.Contains(t, ve.Error(), "cannot be true while cache.write.enabled is false")
		})
	}
}

// mcp.http is honored only while the server serves MCP at all; an explicit true beside
// mcp.enabled false is refused rather than ignored.
func TestValidate_MCPHTTP(t *testing.T) {
	on, off := boolPtr(true), boolPtr(false)
	for name, tc := range map[string]struct {
		enabled, http *bool
		wantHTTP      bool
		wantFailure   bool
	}{
		"defaults":                   {wantHTTP: true},
		"http off":                   {http: off},
		"http on":                    {http: on, wantHTTP: true},
		"mcp off, http unset":        {enabled: off, wantHTTP: true},
		"mcp off, http off":          {enabled: off, http: off},
		"http on without mcp":        {enabled: off, http: on, wantHTTP: true, wantFailure: true},
		"mcp on explicitly, http on": {enabled: on, http: on, wantHTTP: true},
	} {
		t.Run(name, func(t *testing.T) {
			cfg := Config{CI: CI{MaxShards: -1}, Knowledge: Defaults().Knowledge}
			cfg.MCP.Enabled, cfg.MCP.HTTP = tc.enabled, tc.http
			assert.Equal(t, tc.wantHTTP, cfg.MCP.HTTPEnabled())

			err := Validate(cfg)
			if !tc.wantFailure {
				require.NoError(t, err)
				return
			}
			var ve *ValidationError
			require.ErrorAs(t, err, &ve)
			assert.Equal(t, []FieldFailure{{Field: "mcp.http", Tag: "mcp_enabled_required", Value: "true"}}, ve.Failures)
			assert.Contains(t, ve.Error(), "cannot be true while mcp.enabled is false")
		})
	}
}

func TestSpellsConfigRegistry(t *testing.T) {
	s := SpellsConfig{Registries: []SpellRegistry{{Host: "ghcr.io", Username: "ci", Password: "GITHUB_TOKEN"}}}
	got, ok := s.Registry("GHCR.IO")
	assert.True(t, ok, "a reference's host matches case-insensitively")
	assert.Equal(t, SpellRegistry{Host: "ghcr.io", Username: "ci", Password: "GITHUB_TOKEN"}, got)
	_, ok = s.Registry("docker.io")
	assert.False(t, ok)
}

// A knowledge.conform value magus could not honor fails at load: a check keyed by a
// metric nothing measures would pass without anyone noticing.
func TestValidate_Conform(t *testing.T) {
	valid := Config{CI: CI{MaxShards: -1}, Knowledge: Defaults().Knowledge}
	require.NoError(t, Validate(valid))

	for name, tc := range map[string]struct {
		edit func(*ConformConfig)
		want FieldFailure
	}{
		"ratchet outside the workspace": {
			edit: func(c *ConformConfig) { c.Ratchet = "../conform.json" },
			want: FieldFailure{Field: "knowledge.conform.ratchet", Tag: "conform_ratchet_path", Value: "../conform.json"},
		},
		"absolute ratchet": {
			edit: func(c *ConformConfig) { c.Ratchet = "/etc/conform.json" },
			want: FieldFailure{Field: "knowledge.conform.ratchet", Tag: "conform_ratchet_path", Value: "/etc/conform.json"},
		},
		"empty exclude pattern": {
			edit: func(c *ConformConfig) { c.Exclude = []string{""} },
			want: FieldFailure{Field: "knowledge.conform.exclude[0]", Tag: "required"},
		},
		"malformed exclude glob": {
			edit: func(c *ConformConfig) { c.Exclude = []string{"vendor/**", "third/[a-"} },
			want: FieldFailure{Field: "knowledge.conform.exclude[1]", Tag: "conform_exclude_glob", Value: "third/[a-"},
		},
		"unknown metric": {
			edit: func(c *ConformConfig) {
				c.Checks["file-line"] = map[types.ConformPopulation]ConformLimitConfig{types.ConformPopSource: {Percentile: 95, Anchor: 1}}
			},
			want: FieldFailure{Field: "knowledge.conform.checks.file-line", Tag: "conform_metric", Param: strings.Join(types.ConformFileLines.Values(), " "), Value: "file-line"},
		},
		"unknown population": {
			edit: func(c *ConformConfig) {
				c.Checks[types.ConformFileLines]["tests"] = ConformLimitConfig{Percentile: 95, Anchor: 1}
			},
			want: FieldFailure{Field: "knowledge.conform.checks.file-lines.tests", Tag: "conform_population", Param: strings.Join(types.ConformPopSource.Values(), " "), Value: "tests"},
		},
		"zero percentile": {
			edit: func(c *ConformConfig) {
				c.Checks[types.ConformFileLines][types.ConformPopSource] = ConformLimitConfig{Anchor: 2200}
			},
			want: FieldFailure{Field: "knowledge.conform.checks.file-lines.source.percentile", Tag: "gt", Param: "0", Value: "0"},
		},
		"percentile past 100": {
			edit: func(c *ConformConfig) {
				c.Checks[types.ConformFileLines][types.ConformPopSource] = ConformLimitConfig{Percentile: 101, Anchor: 2200}
			},
			want: FieldFailure{Field: "knowledge.conform.checks.file-lines.source.percentile", Tag: "lte", Param: "100", Value: "101"},
		},
		"zero anchor": {
			edit: func(c *ConformConfig) {
				c.Checks[types.ConformFileLines][types.ConformPopSource] = ConformLimitConfig{Percentile: 95}
			},
			want: FieldFailure{Field: "knowledge.conform.checks.file-lines.source.anchor", Tag: "gte", Param: "1", Value: "0"},
		},
		"mode-knobs below a zero anchor": {
			edit: func(c *ConformConfig) {
				c.Checks[types.ConformModeKnobs][types.ConformPopSource] = ConformLimitConfig{Anchor: -1}
			},
			want: FieldFailure{Field: "knowledge.conform.checks.mode-knobs.source.anchor", Tag: "gte", Param: "0", Value: "-1"},
		},
		"export-scope below a zero anchor": {
			edit: func(c *ConformConfig) {
				c.Checks[types.ConformExportScope] = map[types.ConformPopulation]ConformLimitConfig{types.ConformPopMain: {Anchor: -1}}
			},
			want: FieldFailure{Field: "knowledge.conform.checks.export-scope.main.anchor", Tag: "gte", Param: "0", Value: "-1"},
		},
		"scope on a metric without scopes": {
			edit: func(c *ConformConfig) {
				c.Checks[types.ConformFileLines][types.ConformPopSource] = ConformLimitConfig{Percentile: 95, Anchor: 2200, Scopes: map[string]int{"cmd": 3000}}
			},
			want: FieldFailure{Field: "knowledge.conform.checks.file-lines.source.scopes.cmd", Tag: "conform_scope_metric", Param: "dep-fanout", Value: "cmd"},
		},
		"scope outside the workspace": {
			edit: func(c *ConformConfig) {
				c.Checks[types.ConformDepFanout][types.ConformPopSource] = ConformLimitConfig{Percentile: 95, Anchor: 62, Scopes: map[string]int{"../x": 89}}
			},
			want: FieldFailure{Field: "knowledge.conform.checks.dep-fanout.source.scopes.../x", Tag: "conform_scope_path", Value: "../x"},
		},
		"scope with a zero anchor": {
			edit: func(c *ConformConfig) {
				c.Checks[types.ConformDepFanout][types.ConformPopSource] = ConformLimitConfig{Percentile: 95, Anchor: 62, Scopes: map[string]int{"cmd/magus": 0}}
			},
			want: FieldFailure{Field: "knowledge.conform.checks.dep-fanout.source.scopes.cmd/magus", Tag: "gte", Param: "1", Value: "0"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			cfg := valid
			cfg.Knowledge.Conform = conformDefaults()
			tc.edit(&cfg.Knowledge.Conform)
			var ve *ValidationError
			require.ErrorAs(t, Validate(cfg), &ve)
			require.Len(t, ve.Failures, 1)
			assert.Equal(t, tc.want, ve.Failures[0])
		})
	}
}

// The scalar knobs are graded by struct tag, so the failure names the yaml path.
func TestValidate_ConformScalars(t *testing.T) {
	for name, tc := range map[string]struct {
		edit  func(*ConformConfig)
		field string
	}{
		"min_sample":       {func(c *ConformConfig) { c.MinSample = 0 }, "min_sample"},
		"step zero":        {func(c *ConformConfig) { c.Step = 0 }, "step"},
		"step one":         {func(c *ConformConfig) { c.Step = 1 }, "step"},
		"gates.min_cohort": {func(c *ConformConfig) { c.Gates.MinCohort = 0 }, "min_cohort"},
		"gates.min_share":  {func(c *ConformConfig) { c.Gates.MinShare = 1.5 }, "min_share"},
		"gates.bar":        {func(c *ConformConfig) { c.Gates.Bar = -0.1 }, "bar"},
		"gates.max":        {func(c *ConformConfig) { c.Gates.MaxFindings = 0 }, "max_findings"},
	} {
		t.Run(name, func(t *testing.T) {
			cfg := Config{CI: CI{MaxShards: -1}, Knowledge: Defaults().Knowledge}
			cfg.Knowledge.Conform = conformDefaults()
			tc.edit(&cfg.Knowledge.Conform)
			var ve *ValidationError
			require.ErrorAs(t, Validate(cfg), &ve)
			require.Len(t, ve.Failures, 1)
			assert.Contains(t, ve.Failures[0].Field, "conform")
			assert.Contains(t, ve.Failures[0].Field, tc.field)
		})
	}
}
