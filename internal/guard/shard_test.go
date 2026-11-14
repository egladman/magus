package guard

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/egladman/magus/libs/testkit"
)

// shardChildEnv marks a process the sharding parent started.
const shardChildEnv = "MAGUS_GUARD_TEST_SHARD"

// Most tests here take testkit.Isolate or t.Setenv, which forbid t.Parallel.
// So the top-level tests run as several processes at once, each serial inside.
func TestMain(m *testing.M) {
	if os.Getenv(shardChildEnv) == "" && shardable(os.Args[1:]) {
		os.Exit(runShards(os.Args[1:]))
	}
	testkit.Main(m, shardChildEnv)
}

// Any other flag, such as -run or a coverage profile, keeps the run in one
// process: a split would change what it selects or measures.
var shardableFlags = []string{
	"-test.v", "-test.timeout", "-test.paniconexit0", "-test.testlogfile",
	"-test.count=1", "-test.short", "-test.failfast",
}

func shardable(args []string) bool {
	for _, a := range args {
		ok := false
		for _, f := range shardableFlags {
			if a == f || strings.HasPrefix(a, f+"=") {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	return true
}

type shardResult struct {
	output []byte
	err    error
}

// runShards deals the top-level tests round-robin into more chunks than
// workers, so slow tests spread and the tail stays short.
func runShards(args []string) int {
	exe, err := os.Executable()
	if err != nil {
		fmt.Fprintln(os.Stderr, "guard shard:", err)
		return 1
	}
	list := exec.Command(exe, "-test.list=^Test")
	list.Env = append(os.Environ(), shardChildEnv+"=list")
	listed, err := list.Output()
	if err != nil {
		fmt.Fprintln(os.Stderr, "guard shard: listing tests:", err)
		return 1
	}
	var names []string
	for _, line := range strings.Split(string(listed), "\n") {
		if strings.HasPrefix(line, "Test") {
			names = append(names, line)
		}
	}

	workers := min(runtime.NumCPU(), 8)
	chunks := make([][]string, min(workers*4, max(len(names), 1)))
	for i, name := range names {
		k := i % len(chunks)
		chunks[k] = append(chunks[k], regexp.QuoteMeta(name))
	}

	verbose, logPath, forwarded := false, "", []string(nil)
	for _, a := range args {
		switch {
		case a == "-test.v" || a == "-test.v=true":
			verbose = true
			forwarded = append(forwarded, a)
		case strings.HasPrefix(a, "-test.testlogfile="):
			logPath = strings.TrimPrefix(a, "-test.testlogfile=")
		default:
			forwarded = append(forwarded, a)
		}
	}

	var (
		mu     sync.Mutex
		failed bool
		wg     sync.WaitGroup
	)
	jobs := make(chan int)
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				res := runChunk(exe, forwarded, logPath, i, chunks[i])
				mu.Lock()
				if res.err != nil {
					failed = true
					os.Stdout.Write(res.output)
					fmt.Printf("guard shard %d: %v\n", i, res.err)
				} else if verbose {
					os.Stdout.Write(bytes.TrimSuffix(res.output, []byte("PASS\n")))
				}
				mu.Unlock()
			}
		}()
	}
	for i := range chunks {
		if len(chunks[i]) > 0 {
			jobs <- i
		}
	}
	close(jobs)
	wg.Wait()

	if logPath != "" {
		mergeTestLogs(logPath, len(chunks))
	}
	if failed {
		fmt.Println("FAIL")
		return 1
	}
	fmt.Println("PASS")
	return 0
}

func runChunk(exe string, forwarded []string, logPath string, i int, chunk []string) shardResult {
	args := append([]string{"-test.run=^(" + strings.Join(chunk, "|") + ")$"}, forwarded...)
	if logPath != "" {
		args = append(args, fmt.Sprintf("-test.testlogfile=%s.shard%d", logPath, i))
	}
	cmd := exec.Command(exe, args...)
	cmd.Env = append(os.Environ(), shardChildEnv+"=run")
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	return shardResult{output: out.Bytes(), err: err}
}

// go test keys its result cache on this log, so it must name what every
// child opened.
func mergeTestLogs(logPath string, chunks int) {
	merged := []byte("# test log\n")
	for i := range chunks {
		part := fmt.Sprintf("%s.shard%d", logPath, i)
		b, err := os.ReadFile(part)
		if err != nil {
			continue
		}
		merged = append(merged, bytes.TrimPrefix(b, []byte("# test log\n"))...)
		os.Remove(part)
	}
	if err := os.WriteFile(logPath, merged, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "guard shard: writing test log:", err)
	}
}
