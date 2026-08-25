//go:build linux || darwin

package mem

import (
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// childEnv makes a re-exec of this test binary hold memory instead of running
// tests. Re-exec rather than a compiled fixture so the test needs nothing built
// and nothing on PATH.
const childEnv = "MAGUS_HOSTMEM_TREE_CHILD_MB"

func TestMain(m *testing.M) {
	if mb := os.Getenv(childEnv); mb != "" {
		holdMemory(mb)
		return
	}
	os.Exit(m.Run())
}

// holdMemory faults in the requested megabytes and stays resident long enough for
// a parent to sample the tree several times.
func holdMemory(mb string) {
	n := 0
	for _, c := range mb {
		n = n*10 + int(c-'0')
	}
	buf := make([]byte, n<<20)
	for i := 0; i < len(buf); i += 4096 {
		buf[i] = 1
	}
	time.Sleep(5 * time.Second)
	// Keep buf alive across the sleep: without this the compiler is free to drop
	// the allocation before the parent ever looks.
	runtimeKeepAlive(buf)
}

func runtimeKeepAlive(b []byte) {
	if len(b) > 0 && b[0] == 2 {
		os.Exit(3) // unreachable; exists only so b is observably used
	}
}

// The whole point of this package's tree walk: a process tree is worth more than
// its largest member. The kernel will not total one (wait4 folds a subtree as a
// maximum), which is exactly the shortfall that made a parallel `go test` record
// its biggest package binary instead of the suite.
func TestTreeBytesCountsConcurrentChildren(t *testing.T) {
	const (
		perChild = 128
		children = 4
	)
	before := TreeBytes(os.Getpid())
	require.Positive(t, before, "this host must report its own resident memory")

	var procs []*exec.Cmd
	for range children {
		c := exec.Command(os.Args[0])
		c.Env = append(os.Environ(), childEnv+"=128")
		require.NoError(t, c.Start())
		procs = append(procs, c)
	}
	t.Cleanup(func() {
		for _, c := range procs {
			_ = c.Process.Kill()
			_ = c.Wait()
		}
	})

	// Poll rather than sleep a fixed time: the children fault their pages in at
	// their own pace, and a fixed wait is either flaky or slow.
	var grew int64
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		if grew = TreeBytes(os.Getpid()) - before; grew > int64(children)*perChild<<20*3/4 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	assert.Greater(t, grew, int64(perChild)<<20*3/2,
		"the tree total must exceed any single child (%dMB); got %dMB of growth",
		perChild, grew>>20)
}

// A pid with no children is its own tree, and a pid that does not exist is 0
// rather than an error: a process that exits mid-walk is the ordinary case.
func TestTreeBytesEdges(t *testing.T) {
	assert.Positive(t, TreeBytes(os.Getpid()))
	assert.Zero(t, TreeBytes(-1))
}
