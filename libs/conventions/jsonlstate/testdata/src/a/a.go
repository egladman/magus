package a

import (
	"encoding/json"
	"os"
	"path/filepath"
)

type store struct{ path string }

func stateDir() (string, error) { return "", nil }

func writeAtomic(path string, data []byte, perm os.FileMode) error { return nil }

func mustMarshal(v any) []byte { b, _ := json.Marshal(v); return b }

func rewriteUnderRoot(v any) error {
	dir, _ := stateDir()
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "x.json"), b, 0o600) // want `rewrites a whole JSON document`
}

func rewriteUnderSegment(v any) error {
	b, _ := json.MarshalIndent(v, "", " ")
	return os.WriteFile(filepath.Join("repo", ".magus", "x.json"), b, 0o600) // want `rewrites a whole JSON document`
}

func rewriteConfiguredWriter(v any) error {
	root, _ := stateDir()
	p := filepath.Join(root, "x")
	return writeAtomic(p, mustMarshal(v), 0o600) // want `rewrites a whole JSON document`
}

func (s *store) rewriteField(v any) error {
	b, _ := json.Marshal(v)
	return writeAtomic(s.path, b, 0o600) // want `rewrites a whole JSON document`
}

func renameIntoPlace(v any) error {
	dir, _ := stateDir()
	b, _ := json.Marshal(v)
	tmp := filepath.Join(dir, "x.tmp")
	if err := os.WriteFile(tmp, b, 0o600); err != nil { // want `rewrites a whole JSON document`
		return err
	}
	return os.Rename(tmp, filepath.Join(dir, "x.json")) // want `rewrites a whole JSON document`
}

func appendLine(v any) error {
	dir, _ := stateDir()
	b, _ := json.Marshal(v)
	f, err := os.OpenFile(filepath.Join(dir, "x.jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(b, '\n'))
	return err
}

func notState(v any, dir string) error {
	b, _ := json.Marshal(v)
	return os.WriteFile(filepath.Join(dir, "out.json"), b, 0o600)
}

func similarSegment(v any) error {
	b, _ := json.Marshal(v)
	return os.WriteFile(filepath.Join("repo", ".magus-base-cache", "x.json"), b, 0o600)
}

func notJSON() error {
	root, _ := stateDir()
	return os.WriteFile(filepath.Join(root, "x.txt"), []byte("hi"), 0o600)
}
