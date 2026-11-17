package gcpolicy

import (
	"fmt"
	"os"
	"os/exec"
	"runtime/debug"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const childEnv = "MAGUS_GCPOLICY_TEST_CHILD"

func TestMain(m *testing.M) {
	if os.Getenv(childEnv) == "1" {
		percent := debug.SetGCPercent(Percent)
		prior, raised := Prior()
		fmt.Printf("%d %d %t", percent, prior, raised)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestInitRaisesGCUnlessGOGCIsSet(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		gogc []string
		want string
	}{
		{name: "unset", want: fmt.Sprintf("%d 100 true", Percent)},
		{name: "empty", gogc: []string{"GOGC="}, want: fmt.Sprintf("%d 100 true", Percent)},
		{name: "explicit", gogc: []string{"GOGC=50"}, want: "50 0 false"},
		{name: "off", gogc: []string{"GOGC=off"}, want: "-1 0 false"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			env := slices.DeleteFunc(os.Environ(), func(kv string) bool { return strings.HasPrefix(kv, "GOGC=") })
			cmd := exec.Command(os.Args[0])
			cmd.Env = append(append(env, childEnv+"=1"), tc.gogc...)
			out, err := cmd.Output()
			require.NoError(t, err)
			assert.Equal(t, tc.want, string(out))
		})
	}
}

// TestRelaxConcurrentCallersShareOneRestore reproduces the race a plain save/restore
// pair had between loadMagus and inspectWorkspace: both can first-load in parallel
// goroutines, and each capturing its own "prior" GOGC lets one's restore stomp the
// other's still-active relaxation, or leave GOGC raised forever. Refcounting must keep
// GOGC raised for as long as any holder is out, and restore it exactly once, only when
// the last one releases, regardless of acquire order.
func TestRelaxConcurrentCallersShareOneRestore(t *testing.T) {
	if orig, ok := os.LookupEnv("GOGC"); ok {
		require.NoError(t, os.Unsetenv("GOGC"))
		t.Cleanup(func() { _ = os.Setenv("GOGC", orig) })
	}

	const before = 111 // distinctive, so a restore-to-the-wrong-value cannot pass by coincidence
	debug.SetGCPercent(before)
	t.Cleanup(func() { debug.SetGCPercent(before) })

	const holders = 8
	var wg sync.WaitGroup
	releases := make([]func(), holders)
	for i := range holders {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			releases[i] = Relax()
		}(i)
	}
	wg.Wait()

	// Peeking is itself a SetGCPercent call, so set back to what every holder expects
	// raised: a no-op if the fix holds, a real (and revealing) change if it does not.
	got := debug.SetGCPercent(Percent)
	assert.Equal(t, Percent, got, "GOGC must stay raised while any holder has not released")

	for _, release := range releases[:holders-1] {
		release()
	}
	got = debug.SetGCPercent(Percent)
	assert.Equal(t, Percent, got, "GOGC must stay raised until the LAST holder releases")

	releases[holders-1]()
	got = debug.SetGCPercent(before)
	assert.Equal(t, before, got, "GOGC must be restored to its pre-relax value once every holder released")
}

func TestRelaxStartupRestoresAfterTheWindow(t *testing.T) {
	if orig, ok := os.LookupEnv("GOGC"); ok {
		require.NoError(t, os.Unsetenv("GOGC"))
		t.Cleanup(func() { _ = os.Setenv("GOGC", orig) })
	}
	initPrior, raised := Prior()
	afterInit := Percent
	if raised {
		afterInit = initPrior
	}
	cases := []struct {
		name        string
		before      int
		wantRestore int
	}{
		{name: "caller set GOGC", before: 111, wantRestore: 111},
		{name: "gcpolicy init raised GOGC", before: Percent, wantRestore: afterInit},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			debug.SetGCPercent(tc.before)
			t.Cleanup(func() { debug.SetGCPercent(tc.before) })

			// Held under relaxMu so the timer's restore cannot land between the two calls.
			peek := func() (percent, holders int) {
				relaxMu.Lock()
				defer relaxMu.Unlock()
				percent = debug.SetGCPercent(tc.before)
				debug.SetGCPercent(percent)
				return percent, relaxCount
			}

			RelaxStartup(50 * time.Millisecond)
			percent, holders := peek()
			assert.Equal(t, Percent, percent)
			assert.Equal(t, 1, holders)

			require.Eventually(t, func() bool {
				_, holders := peek()
				return holders == 0
			}, 5*time.Second, 5*time.Millisecond)
			percent, _ = peek()
			assert.Equal(t, tc.wantRestore, percent)
		})
	}
	assert.Equal(t, time.Second, StartupWindow)
}
