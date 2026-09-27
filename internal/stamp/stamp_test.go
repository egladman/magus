package stamp

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/types"
)

// history is a repository holding two commits, aaaaaaa1 before bbbbbbb2, and no other.
func history(ancestor, descendant string) (bool, error) {
	order := map[string]int{"aaaaaaa1": 1, "bbbbbbb2": 2}
	a, aok := order[ancestor]
	d, dok := order[descendant]
	if !aok || !dok {
		return false, errors.New("unknown revision")
	}
	return a <= d, nil
}

func TestNewer(t *testing.T) {
	a := Writer{Version: "v0.4.3-10-gaaaaaaa", Commit: "aaaaaaa1", Date: "2026-09-25T00:00:00Z"}
	b := Writer{Version: "v0.4.3-12-gbbbbbbb", Commit: "bbbbbbb2", Date: "2026-09-20T00:00:00Z"}
	branch := Writer{Version: "v0.4.3-15-gccccccc-dirty", Commit: "ccccccc3", Date: "2026-09-22T00:00:00Z"}
	release := Writer{Version: "v0.5.0", Commit: "ddddddd4", Date: "2026-09-01T00:00:00Z"}

	for _, tc := range []struct {
		name           string
		recorded, self Writer
		ancestry       Ancestry
		newer          bool
	}{
		{"ancestry beats a later date", b, a, history, true},
		{"a descendant may replace its ancestor", a, b, history, false},
		{"the same commit is equal", b, Writer{Version: b.Version + "-dirty", Commit: "bbbbbbb"}, history, false},
		{"no repository falls back to the date", a, b, nil, true},
		{"a commit the repository lacks falls back to the date", branch, b, history, true},
		{"a later release wins over an earlier date", release, branch, history, true},
		{"an earlier release loses to a dev build past a later tag", branch, release, history, false},
		{"an unstamped record is never newer", Writer{}, a, history, false},
		{"an unknown writer is never refused", release, Writer{}, history, false},
		{"unparseable versions and dates order nothing", Writer{Version: "dev", Commit: "x"}, Writer{Version: "dev2", Commit: "y"}, nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.newer, Judge{Self: tc.self, Ancestry: tc.ancestry}.Newer(tc.recorded))
		})
	}
}

func TestParseRoundTrip(t *testing.T) {
	w := Writer{Version: "v0.4.3-164-g3cd94ed9b-dirty", Commit: "3cd94ed9b", Date: "2026-09-26T17:46:59-04:00"}
	got, ok := Parse("# BEGIN magus-regenerate - do not edit this section manually; written by " + w.String())
	require.True(t, ok)
	assert.Equal(t, w, got)

	got, ok = Parse(Writer{Commit: "abc1234"}.String())
	require.True(t, ok)
	assert.Equal(t, Writer{Commit: "abc1234"}, got, "unknown fields read back empty")

	_, ok = Parse("# BEGIN magus-regenerate - do not edit this section manually")
	assert.False(t, ok)
}

func TestSelfReadsTheBuildOnContext(t *testing.T) {
	assert.Equal(t, Writer{}, Self(t.Context()))
	ctx := types.WithMagusBuild(t.Context(), types.MagusBuild{Version: "unknown", Commit: "abc1234", Date: "unknown"})
	assert.Equal(t, Writer{Commit: "abc1234"}, Self(ctx))
}

func TestCheckNamesBothBuildsAndTheFix(t *testing.T) {
	older := Writer{Version: "v0.4.3", Commit: "aaaaaaa1", Date: "2026-09-01T00:00:00Z"}
	newer := Writer{Version: "v0.5.0", Commit: "bbbbbbb2", Date: "2026-09-20T00:00:00Z"}
	err := Judge{Self: older}.Check("/home/me/.config/magus/magus.yaml", newer)
	var down *DowngradeError
	require.ErrorAs(t, err, &down)
	assert.Equal(t, "/home/me/.config/magus/magus.yaml was written by magus v0.5.0 (commit bbbbbbb2, 2026-09-20T00:00:00Z), "+
		"newer than this binary, magus v0.4.3 (commit aaaaaaa1, 2026-09-01T00:00:00Z), and replacing it would undo what the newer one wrote. "+
		"Update this binary to at least v0.5.0 (`magus self update` for a release, or bring a checkout of magus up to commit bbbbbbb2 and rebuild it), "+
		"or rerun this command with a magus at least that new", err.Error())
	assert.NoError(t, Judge{Self: newer}.Check("x", older))
}
