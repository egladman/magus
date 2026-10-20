package main

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"runtime/debug"
	"strings"
)

func writeJSONFile(p string, v any) error {
	data, err := json.MarshalIndent(v, "", " ")
	if err != nil {
		return err
	}

	return os.WriteFile(p, append(data, '\n'), 0o600)
}

// ruleFilesChanged lists the rule files that differ from rev in the work
// tree, untracked ones included.
func ruleFilesChanged(rev string) ([]string, error) {
	diff, err := gitOutput(append([]string{"diff", "--name-only", rev, "--"}, rulePathspecs...)...)
	if err != nil {
		return nil, err
	}

	untracked, err := gitOutput(append([]string{"ls-files", "--others", "--exclude-standard", "--"}, rulePathspecs...)...)
	if err != nil {
		return nil, err
	}

	return append(strings.Fields(diff), strings.Fields(untracked)...), nil
}

func gitOutput(args ...string) (string, error) {
	cmd := exec.Command("git", args...)

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git %s (%s): %w", strings.Join(args, " "), strings.TrimSpace(stderr.String()), err)
	}

	return strings.TrimSpace(string(out)), nil
}

// proofreadVersion names the build judging: its module version and the
// commit it was built from, when the build recorded them.
func proofreadVersion() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "unknown"
	}

	parts := []string{info.Main.Version}

	for _, s := range info.Settings {
		switch {
		case s.Key == "vcs.revision":
			parts = append(parts, s.Value)
		case s.Key == "vcs.modified" && s.Value == "true":
			parts = append(parts, "modified")
		}
	}

	return strings.Join(parts, " ")
}

// executableSHA256 is the digest of the running binary. Build info alone does
// not name the sampler, since a cached build output restores a binary another
// checkout built.
func executableSHA256() (string, error) {
	path, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("find the running proofread-train binary: %w", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("hash the running proofread-train binary: %w", err)
	}

	return sha256Hex(data), nil
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)

	return hex.EncodeToString(sum[:])
}

func readJSONFile(p string, v any) error {
	data, err := os.ReadFile(p)
	if err != nil {
		return err
	}

	if err := json.Unmarshal(data, v); err != nil {
		return fmt.Errorf("%s: %w", p, err)
	}

	return nil
}

func readJSONLines[T any](p string) ([]T, error) {
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var out []T

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<20), 64<<20)

	for n := 1; sc.Scan(); n++ {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}

		var v T
		if err := json.Unmarshal(line, &v); err != nil {
			return nil, fmt.Errorf("%s:%d: %w", p, n, err)
		}

		out = append(out, v)
	}

	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("read %s: %w", p, err)
	}

	return out, nil
}
