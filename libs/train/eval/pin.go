package eval

import (
	"bufio"
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
)

// Digest is one line of gen/split.digest: a dataset written under the cache root and the
// sha256 of its file. The datasets stay out of git; this line is what is committed.
type Digest struct {
	Name   string `json:"name"`
	SHA256 string `json:"sha256"`
	Rows   int    `json:"rows"`
	Rev    string `json:"rev"`
}

// FileDigest is the sha256 of the file at path, in hex.
func FileDigest(path string) (string, error) {
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

// ReadDigests reads a digest file; a missing file has no digests.
func ReadDigests(path string) ([]Digest, error) {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []Digest
	sc := bufio.NewScanner(f)
	for n := 1; sc.Scan(); n++ {
		var d Digest
		if err := json.Unmarshal(sc.Bytes(), &d); err != nil {
			return nil, fmt.Errorf("%s:%d: %w", path, n, err)
		}
		out = append(out, d)
	}
	return out, sc.Err()
}

// Pin records d in the digest file, replacing the line of the same name, and rewrites the
// file sorted by name through a rename.
func Pin(path string, d Digest) error {
	all, err := ReadDigests(path)
	if err != nil {
		return err
	}
	all = slices.DeleteFunc(all, func(o Digest) bool { return o.Name == d.Name })
	all = append(all, d)
	slices.SortFunc(all, func(a, b Digest) int { return cmp.Compare(a.Name, b.Name) })
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	w := bufio.NewWriter(f)
	for _, o := range all {
		b, err := json.Marshal(o)
		if err != nil {
			f.Close()
			return err
		}
		w.Write(b)
		w.WriteByte('\n')
	}
	if err := w.Flush(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
