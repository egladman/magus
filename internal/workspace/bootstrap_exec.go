package workspace

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/egladman/magus/internal/ward"
)

// BootstrapExecSentinelVar marks a process that already replaced itself with
// a workspace-local ./magus, so a second hop can never chain.
//
// It is not MAGUS_LEVEL: that counts a magus spawning another as a child.
// The bootstrap exec replaces this process's image with the same argv, stdio
// and exit code, so reusing it would read one invocation as recursion.
const BootstrapExecSentinelVar = "MAGUS_BOOTSTRAP_EXEC_DONE"

// BootstrapExecOptOutVar disables the bootstrap exec entirely. Documented in
// internal/config/config.go's envOnly with the rest of the MAGUS_* variables.
const BootstrapExecOptOutVar = "MAGUS_NO_BOOTSTRAP_EXEC"

// PinnedBinaryVar is the hook glue's own pin of the binary it starts.
const PinnedBinaryVar = "__MAGUS_BIN"

// BootstrapExecTarget is the workspace-local ./magus that should replace
// self, found from start, or from the working directory when start is "".
// It reads only the tree and the environment, so no config need load.
//
// The opt-out, the sentinel and the pin each turn it off. So does a `go run`
// build of self, which is fresher than the placed ./magus.
func BootstrapExecTarget(start, self string) (target string, ok bool) {
	if truthyEnv(os.Getenv(BootstrapExecOptOutVar)) || ward.GoRunBinary(self) {
		return "", false
	}
	if os.Getenv(BootstrapExecSentinelVar) != "" || os.Getenv(PinnedBinaryVar) != "" {
		return "", false
	}
	if start == "" {
		wd, err := os.Getwd()
		if err != nil {
			return "", false
		}
		start = wd
	}
	start, err := filepath.Abs(start)
	if err != nil {
		return "", false
	}
	return resolveBootstrapExecTarget(start, self)
}

// resolveBootstrapExecTarget resolves the ./magus beside FindRoot(start),
// unless it is missing, not executable, or self. FindRoot stops at the
// nearest magus.yaml, so a nested worktree never takes its parent's binary.
func resolveBootstrapExecTarget(start, self string) (target string, ok bool) {
	dir, err := FindRoot(start)
	if err != nil {
		return "", false
	}

	name := "magus"
	if runtime.GOOS == "windows" {
		name = "magus.exe"
	}
	candidate := filepath.Join(dir, name)
	info, err := os.Stat(candidate)
	if err != nil || info.IsDir() {
		return "", false
	}
	// Windows has no execute-permission bit; the name already carries the
	// platform's executable extension.
	if runtime.GOOS != "windows" && info.Mode()&0o111 == 0 {
		return "", false
	}
	resolved, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		return "", false
	}
	if resolved == self {
		return "", false
	}
	return resolved, true
}

// truthyEnv matches the 1/true/yes convention of the boolean MAGUS_* env vars.
func truthyEnv(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes":
		return true
	default:
		return false
	}
}
