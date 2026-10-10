package spells

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateScriptRunners(t *testing.T) {
	tests := []struct {
		name    string
		runners []Command
		wantErr string
	}{
		{name: "none", runners: nil},
		{name: "bare bin and prefix", runners: []Command{{Bin: "poe"}, {Bin: "pnpm", Args: []string{"run"}}}},
		{name: "blank bin", runners: []Command{{Args: []string{"run"}}},
			wantErr: `mgs_listScriptRunners[0]: bin "" must name one executable`},
		{name: "bin with a space", runners: []Command{{Bin: "npm run"}},
			wantErr: `mgs_listScriptRunners[0]: bin "npm run" must name one executable`},
		{name: "blank arg", runners: []Command{{Bin: "npm"}, {Bin: "pnpm", Args: []string{""}}},
			wantErr: `mgs_listScriptRunners[1] (pnpm): args [""] holds a blank token`},
		{name: "a field a prefix cannot honor", runners: []Command{{Bin: "npm", Args: []string{"run"}, DefaultArgs: []string{"build"}}},
			wantErr: "mgs_listScriptRunners[0] (npm): a runner is an argv prefix, set only bin and args"},
		{name: "capture", runners: []Command{{Bin: "npm", Capture: true}},
			wantErr: "mgs_listScriptRunners[0] (npm): a runner is an argv prefix, set only bin and args"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateScriptRunners(tt.runners)
			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}
			assert.EqualError(t, err, tt.wantErr)
		})
	}
}

func TestMatchScriptRunner(t *testing.T) {
	runners := []Command{
		{Bin: "pnpm", Args: []string{"run"}},
		{Bin: "npm", Args: []string{"test"}},
		{Bin: "poe"},
	}
	type match struct {
		Runner Command
		Script string
		OK     bool
	}
	tests := []struct {
		name string
		argv []string
		want match
	}{
		{name: "empty argv", argv: nil, want: match{}},
		{name: "prefix and script", argv: []string{"pnpm", "run", "build"},
			want: match{Runner: runners[0], Script: "build", OK: true}},
		{name: "flag before the script", argv: []string{"pnpm", "run", "--silent", "build"},
			want: match{Runner: runners[0], Script: "build", OK: true}},
		{name: "absolute bin", argv: []string{"/usr/local/bin/pnpm", "run", "lint"},
			want: match{Runner: runners[0], Script: "lint", OK: true}},
		{name: "lifecycle prefix names no script", argv: []string{"npm", "test"},
			want: match{Runner: runners[1], OK: true}},
		{name: "bare-bin runner", argv: []string{"poe", "lint"},
			want: match{Runner: runners[2], Script: "lint", OK: true}},
		{name: "same bin, another subcommand", argv: []string{"pnpm", "exec", "tsc"}, want: match{}},
		{name: "argv shorter than the prefix", argv: []string{"pnpm"}, want: match{}},
		{name: "bin only shares a prefix", argv: []string{"pnpmx", "run", "build"}, want: match{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, script, ok := MatchScriptRunner(runners, tt.argv)
			assert.Equal(t, tt.want, match{Runner: r, Script: script, OK: ok})
		})
	}
}
