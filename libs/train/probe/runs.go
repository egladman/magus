// Package probe measures what stage S0 of the local-models plan needs before any long
// run: tokenizer fertility, short mlx-lm training pilots and llama-bench numbers. Each
// measurement is one line appended to libs/train/runs.jsonl.
package probe

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// Input is one line of libs/train/inputs.jsonl: a pinned download. Path is relative to
// the cache root.
type Input struct {
	Name        string `json:"name"`
	Source      string `json:"source"`
	Revision    string `json:"revision"`
	SHA256      string `json:"sha256"`
	Size        int64  `json:"size"`
	License     string `json:"license"`
	LicenseFile string `json:"license_file"`
	LicenseNote string `json:"license_note,omitempty"`
	Path        string `json:"path"`
}

// ReadInputs reads an inputs.jsonl. A row without a name, source, sha256 or relative
// path is an error.
func ReadInputs(path string) ([]Input, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []Input
	sc := bufio.NewScanner(f)
	for n := 1; sc.Scan(); n++ {
		var in Input
		if err := json.Unmarshal(sc.Bytes(), &in); err != nil {
			return nil, fmt.Errorf("%s:%d: %w", path, n, err)
		}
		if in.Name == "" || in.Source == "" || len(in.SHA256) != 64 || in.Path == "" || filepath.IsAbs(in.Path) {
			return nil, fmt.Errorf("%s:%d: needs name, source, sha256 and a relative path", path, n)
		}
		out = append(out, in)
	}
	return out, sc.Err()
}

// WithPrefix returns the inputs whose name starts with prefix, or an error naming the
// prefix when none does.
func WithPrefix(inputs []Input, prefix string) ([]Input, error) {
	var out []Input
	for _, in := range inputs {
		if strings.HasPrefix(in.Name, prefix) {
			out = append(out, in)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("inputs.jsonl pins nothing named %s*", prefix)
	}
	return out, nil
}

// Present checks that each input is in the cache root at its pinned size and returns
// name -> pinned sha256. It does not rehash: `magus run models libs/train` verified
// every sha256 when it fetched the file, and rehashing a 7 GB shard per pilot costs more
// than the pilot's first step.
func Present(cache string, inputs []Input) (map[string]string, error) {
	sums := make(map[string]string, len(inputs))
	for _, in := range inputs {
		st, err := os.Stat(filepath.Join(cache, in.Path))
		if err != nil {
			return nil, fmt.Errorf("input %s (magus run models libs/train fetches it): %w", in.Name, err)
		}
		if st.Size() != in.Size {
			return nil, fmt.Errorf("input %s is %d bytes, inputs.jsonl pins %d", in.Name, st.Size(), in.Size)
		}
		sums[in.Name] = in.SHA256
	}
	return sums, nil
}

// FileSHA256 returns the hex sha256 of a file's bytes.
func FileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// Append writes row as one JSON line at the end of path, creating the file if needed.
func Append(path string, row any) error {
	b, err := json.Marshal(row)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	_, err = f.Write(append(b, '\n'))
	return errors.Join(err, f.Close())
}

var loadavgRE = regexp.MustCompile(`^\{\s*([0-9.]+)\s+([0-9.]+)\s+([0-9.]+)\s*\}$`)

// ParseLoadavg reads the 1-minute load average from `sysctl -n vm.loadavg` output,
// "{ 11.67 19.64 24.66 }".
func ParseLoadavg(out string) (float64, error) {
	m := loadavgRE.FindStringSubmatch(strings.TrimSpace(out))
	if m == nil {
		return 0, fmt.Errorf("vm.loadavg %q is not three load averages in braces", strings.TrimSpace(out))
	}
	return strconv.ParseFloat(m[1], 64)
}

// Load1 returns the machine's 1-minute load average. Other sessions share the machine,
// so every run row records it beside its throughput. It needs macOS's sysctl and fails
// elsewhere.
func Load1() (float64, error) {
	out, err := exec.Command("sysctl", "-n", "vm.loadavg").Output()
	if err != nil {
		return 0, fmt.Errorf("sysctl -n vm.loadavg: %w", err)
	}
	return ParseLoadavg(string(out))
}
