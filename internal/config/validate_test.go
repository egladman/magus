package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidate_ValidMinimal(t *testing.T) {
	// ci.max_shards must be -1 (unlimited) or in [1,256]; use -1 for a minimal valid config.
	// The duplication thresholds have no valid zero, so they carry their defaults.
	cfg := Config{CI: CI{MaxShards: -1}, Knowledge: Knowledge{Duplication: Defaults().Knowledge.Duplication}}
	assert.NoError(t, Validate(cfg), "Validate(minimal valid Config)")
}

func TestValidate_InvalidConcurrency(t *testing.T) {
	cfg := Config{Concurrency: -1}
	assert.Error(t, Validate(cfg), "Validate(Concurrency=-1): expected error")
}

// jsonl withholds every target's output for a report stream only -o jsonl opens, so a
// magus.yaml asking for it would drop per-target results with nowhere to send them.
func TestValidate_LogFormatRefusesJSONL(t *testing.T) {
	cfg := Config{CI: CI{MaxShards: -1}, Knowledge: Knowledge{Duplication: Defaults().Knowledge.Duplication}}
	cfg.Log.Format = "jsonl"
	require.ErrorContains(t, Validate(cfg), "log.format")
	cfg.Log.Format = "json"
	assert.NoError(t, Validate(cfg))
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
	valid := Config{CI: CI{MaxShards: -1}, Knowledge: Knowledge{Duplication: Defaults().Knowledge.Duplication}}
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
	valid := Config{CI: CI{MaxShards: -1}, Knowledge: Knowledge{Duplication: Defaults().Knowledge.Duplication}}
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

func TestSpellsConfigRegistry(t *testing.T) {
	s := SpellsConfig{Registries: []SpellRegistry{{Host: "ghcr.io", Username: "ci", Password: "GITHUB_TOKEN"}}}
	got, ok := s.Registry("GHCR.IO")
	assert.True(t, ok, "a reference's host matches case-insensitively")
	assert.Equal(t, SpellRegistry{Host: "ghcr.io", Username: "ci", Password: "GITHUB_TOKEN"}, got)
	_, ok = s.Registry("docker.io")
	assert.False(t, ok)
}
