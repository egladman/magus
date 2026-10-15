package job

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/egladman/magus/internal/file"
)

// jsonLines is what [readLines] found in a JSON Lines file.
type jsonLines struct {
	// lines are the complete lines, empty ones dropped, each with its 1-based line number.
	lines []numberedLine
	// partial says the file ends in bytes no newline closes: an append caught mid-write, or
	// the remains of a writer that died in one. A writer appending after them would glue its
	// first line onto them, so it rewrites the file instead.
	partial bool
}

type numberedLine struct {
	n    int
	text []byte
}

// readLines reads the JSON Lines file at path. An absent file has no lines.
func readLines(path string) (jsonLines, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return jsonLines{}, nil
	}
	if err != nil {
		return jsonLines{}, err
	}
	end := bytes.LastIndexByte(raw, '\n') + 1
	out := jsonLines{partial: len(bytes.TrimSpace(raw[end:])) > 0}
	n := 0
	for line := range bytes.SplitSeq(raw[:end], []byte{'\n'}) {
		n++
		if len(bytes.TrimSpace(line)) > 0 {
			out.lines = append(out.lines, numberedLine{n: n, text: line})
		}
	}
	return out, nil
}

// appendLines adds lines to the end of the file at path in ONE write, so a reader sees
// each of them whole or not at all, and syncs it.
func appendLines(path string, lines [][]byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o644)
	if err != nil {
		return err
	}
	_, err = f.Write(joinLines(lines))
	if err == nil {
		err = f.Sync()
	}
	return errors.Join(err, f.Close())
}

// rewriteLines replaces the file at path with lines through a rename, so a reader sees the
// old file or the new one.
func rewriteLines(path string, lines [][]byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return file.WriteFileAtomic(path, joinLines(lines), 0o644)
}

func joinLines(lines [][]byte) []byte {
	var b bytes.Buffer
	for _, l := range lines {
		b.Write(l)
		b.WriteByte('\n')
	}
	return b.Bytes()
}
