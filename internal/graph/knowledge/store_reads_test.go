package knowledge

import (
	"context"
	"strings"
	"testing"

	"github.com/egladman/magus/internal/readlog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// readsStamper is a FastStampsFunc whose domain stamp is a function of the reads it is
// handed and of env, a stand-in for the environment: the stamp a real read computes folds
// the current value of every variable the last evaluation looked up.
func readsStamper(env *map[string]string) func(context.Context, readlog.Reads, bool) Stamps {
	return func(_ context.Context, reads readlog.Reads, known bool) Stamps {
		var b strings.Builder
		if !known {
			b.WriteString("reads:unknown")
		}
		for _, name := range reads.Env {
			b.WriteString(name + "=" + (*env)[name] + ";")
		}
		return Stamps{ClassDomain: "domain:" + b.String(), ClassRuntime: "runtime"}
	}
}

// readsRun is one Ensure over the stamp fixture with the reads machinery wired, counting
// what it had to pay for.
type readsRun struct {
	stamps, gathers int
}

func ensureWithReads(t *testing.T, cacheDir string, in Inputs, reads *readlog.Reads, stamper func(context.Context, readlog.Reads, bool) Stamps, withReadsFunc bool) (*Graph, readsRun) {
	t.Helper()
	var run readsRun
	opts := BuildOptions{
		FastStampsFunc: stamper,
		StampsFunc: func(context.Context) (Stamps, error) {
			run.stamps++
			return stampsAll("v1"), nil
		},
	}
	if withReadsFunc {
		opts.ReadsFunc = func(context.Context) (readlog.Reads, error) { return *reads, nil }
	}
	g, err := Ensure(context.Background(), cacheDir, opts, DefaultClasses, func([]ShardClass) (Inputs, error) {
		run.gathers++
		in.Reads = reads
		return in, nil
	}, nil)
	require.NoError(t, err)
	return g, run
}

func TestEnsureSyncRecordsTheEvaluationReadsAndStampsOverThem(t *testing.T) {
	cacheDir, in := stampFixture(t)
	env := map[string]string{"REGION": "eu"}
	reads := &readlog.Reads{Env: []string{"REGION"}}
	stamper := readsStamper(&env)

	_, first := ensureWithReads(t, cacheDir, in, reads, stamper, true)
	require.Equal(t, 1, first.gathers, "a store with no manifest builds")

	man := readManifest(t, cacheDir)
	assert.Equal(t, *reads, man.Reads, "the sync records what the evaluation read")
	assert.True(t, man.ReadsKnown)
	assert.Equal(t, stamper(context.Background(), *reads, true)[ClassDomain], man.FastStamps[ClassDomain],
		"the recorded fast stamp is computed over the reads just recorded, not over the manifest's old ones")

	_, second := ensureWithReads(t, cacheDir, in, reads, stamper, true)
	assert.Equal(t, readsRun{}, second, "the same environment settles fast: no full stamps, no gather")

	env["REGION"] = "us"
	_, third := ensureWithReads(t, cacheDir, in, reads, stamper, true)
	assert.Equal(t, 1, third.stamps, "a moved variable the evaluation read misses the fast stamp and asks the full one")
	assert.Equal(t, 0, third.gathers, "the full stamp still matches, so nothing is rebuilt")
	assert.Equal(t, stamper(context.Background(), *reads, true)[ClassDomain], readManifest(t, cacheDir).FastStamps[ClassDomain],
		"the fast stamp is re-recorded over the new value, so the next read settles fast again")

	_, fourth := ensureWithReads(t, cacheDir, in, reads, stamper, true)
	assert.Equal(t, readsRun{}, fourth)
}

func TestEnsureRecordsReadsForAManifestThatNeverHadThem(t *testing.T) {
	cacheDir, in := stampFixture(t)
	// A store written before reads existed: plain stamps, no reads, no fast stamps.
	ensure(t, cacheDir, stampsAll("v1"), AllClasses, in)
	before := readManifest(t, cacheDir)
	require.False(t, before.ReadsKnown)

	env := map[string]string{"REGION": "eu"}
	reads := &readlog.Reads{Env: []string{"REGION"}, Files: []string{"/etc/magus/site.toml"}}
	stamper := readsStamper(&env)

	_, run := ensureWithReads(t, cacheDir, in, reads, stamper, true)
	assert.Equal(t, 1, run.stamps, "unknown reads match nothing recorded, so the full stamps are asked")
	assert.Equal(t, 0, run.gathers, "and match, so nothing is rebuilt")
	after := readManifest(t, cacheDir)
	assert.Equal(t, *reads, after.Reads, "the repair records the reads the evaluation made")
	assert.True(t, after.ReadsKnown)
	assert.Equal(t, stamper(context.Background(), *reads, true)[ClassDomain], after.FastStamps[ClassDomain],
		"and the fast stamp over them, not the one computed over unknown reads")

	_, again := ensureWithReads(t, cacheDir, in, reads, stamper, true)
	assert.Equal(t, readsRun{}, again, "from here the read settles fast")
}

func TestEnsureWithoutAReadsFuncNeverRecordsReads(t *testing.T) {
	cacheDir, in := stampFixture(t)
	ensure(t, cacheDir, stampsAll("v1"), AllClasses, in)
	env := map[string]string{}
	stamper := readsStamper(&env)

	_, run := ensureWithReads(t, cacheDir, in, &readlog.Reads{}, stamper, false)
	assert.Equal(t, 1, run.stamps)
	man := readManifest(t, cacheDir)
	assert.False(t, man.ReadsKnown, "a caller that cannot say what was read records nothing")

	_, again := ensureWithReads(t, cacheDir, in, &readlog.Reads{}, stamper, false)
	assert.Equal(t, 1, again.stamps, "and keeps paying the full stamps rather than trusting a stamp over unknown reads")
}

func TestStoreEvaluationReadsDistinguishNoneFromUnknown(t *testing.T) {
	cacheDir, in := stampFixture(t)
	store := NewStore(cacheDir, true, 0, nil, nil)
	_, known := store.EvaluationReads()
	assert.False(t, known, "no manifest")

	ensure(t, cacheDir, stampsAll("v1"), AllClasses, in)
	_, known = store.EvaluationReads()
	assert.False(t, known, "a sync that carried no reads")

	env := map[string]string{}
	ensureWithReads(t, cacheDir, in, &readlog.Reads{}, readsStamper(&env), true)
	reads, known := store.EvaluationReads()
	assert.True(t, known, "an evaluation that read nothing is known to have read nothing")
	assert.Equal(t, readlog.Reads{}, reads)
}
