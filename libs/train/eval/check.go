package eval

import (
	"bytes"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
)

// CheckRow is one Buzz file's `magus buzz --check` result as it stands. Dialect is
// "strict" when the upstream-strict check passed and "embedded" when only --embedded did.
// Reason is the first diagnostic line of a failure.
type CheckRow struct {
	Path   string `json:"path"`
	OK     bool   `json:"ok"`
	Absent bool   `json:"absent,omitempty"`
	// OKAtRev is the same check on the file in the checkout at Rev, the version the set
	// holds; ReasonAtRev is its first diagnostic.
	OKAtRev     bool   `json:"ok_at_rev"`
	ReasonAtRev string `json:"reason_at_rev,omitempty"`
	Dialect     string `json:"dialect,omitempty"`
	Reason      string `json:"reason,omitempty"`
}

// CheckBatch is how many files one `magus buzz --check` process takes (plan 6.1).
const CheckBatch = 200

// CheckBuzz type-checks files in root with the magus binary, batch files per process:
// strictly first, then the failures again with --embedded, the dialect magusfiles are
// written in. A file that fails both stays failed with its strict diagnostic. Only a
// run that names no file at all (the binary missing, a path absent) is an error.
func CheckBuzz(magus, root string, paths []string, batch int) ([]CheckRow, error) {
	strict, err := runCheck(magus, root, paths, batch, false)
	if err != nil {
		return nil, err
	}
	var failed []string
	for _, p := range paths {
		if strict[p] != "" {
			failed = append(failed, p)
		}
	}
	embedded, err := runCheck(magus, root, failed, batch, true)
	if err != nil {
		return nil, err
	}
	rows := make([]CheckRow, 0, len(paths))
	for _, p := range paths {
		switch {
		case strict[p] == "":
			rows = append(rows, CheckRow{Path: p, OK: true, Dialect: "strict"})
		case embedded[p] == "":
			rows = append(rows, CheckRow{Path: p, OK: true, Dialect: "embedded"})
		default:
			rows = append(rows, CheckRow{Path: p, Reason: strict[p]})
		}
	}
	return rows, nil
}

// runCheck answers each failing path's first diagnostic line.
func runCheck(magus, root string, paths []string, batch int, embedded bool) (map[string]string, error) {
	failed := map[string]string{}
	for i := 0; i < len(paths); i += batch {
		chunk := paths[i:min(i+batch, len(paths))]
		args := []string{"buzz", "--check"}
		if embedded {
			args = append(args, "--embedded")
		}
		cmd := exec.Command(magus, append(args, chunk...)...)
		cmd.Dir = root
		var out bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &out
		err := cmd.Run()
		var exit *exec.ExitError
		if err != nil && !errors.As(err, &exit) {
			return nil, fmt.Errorf("magus buzz --check: %w", err)
		}
		lines := strings.Split(out.String(), "\n")
		for _, l := range lines {
			if strings.HasPrefix(l, "[error]") {
				return nil, fmt.Errorf("magus buzz --check: %s", l)
			}
		}
		for _, p := range chunk {
			re := regexp.MustCompile(`(?:^|\s)` + regexp.QuoteMeta(p) + `:\d+:\d+`)
			for _, l := range lines {
				if re.MatchString(l) {
					failed[p] = strings.TrimSpace(l)
					break
				}
			}
		}
		if err != nil && len(failed) == 0 {
			return nil, fmt.Errorf("magus buzz --check exited %d naming no file: %s", exit.ExitCode(), strings.TrimSpace(out.String()))
		}
	}
	return failed, nil
}
