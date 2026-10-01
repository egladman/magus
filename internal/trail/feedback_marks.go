package trail

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/egladman/magus/internal/config"
	json "github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/vcs"
)

const marksFile = "marks.jsonl"

// maxMarkExamples bounds the commands a mark keeps; the backlog shows a few, and a mark is
// a person's verdict rather than a copy of the trail.
const maxMarkExamples = 3

// feedbackID is the shape of a row id: "fb" and twelve hex digits of the digest the
// report computes over a row's section and key.
var feedbackID = regexp.MustCompile(`^fb[0-9a-f]{12}$`)

// FeedbackMarksDir resolves the per-repository store feedback marks live in,
// <XDG state>/magus/feedback/<repo>-<hash12>. It keys on repository identity the way the
// session store does, so a mark made in one worktree is read in every other one and in
// later sessions.
func FeedbackMarksDir(root string) (string, error) {
	base, err := config.UserStateDir()
	if err != nil {
		return "", fmt.Errorf("feedback: resolve state dir: %w (set XDG_STATE_HOME to a writable absolute path)", err)
	}
	dir, err := vcs.StateDir(base, "feedback", root)
	if err != nil {
		return "", fmt.Errorf("feedback: %w", err)
	}
	return dir, nil
}

// AppendFeedbackMark validates m, stamps its time, and appends it to the store at dir.
// Marks are never rewritten: a later mark on the same id supersedes an earlier one for the
// row's current verdict, and the history is what the cross-session fold counts.
//
// Unlike the trail's producers it returns its error, because a person who asked to record
// a verdict must learn that it was not recorded.
func AppendFeedbackMark(dir string, m types.FeedbackMark, now time.Time) (types.FeedbackMark, error) {
	if !feedbackID.MatchString(m.ID) {
		return m, fmt.Errorf("feedback mark id %q is not a row id (fb and 12 hex digits)", m.ID)
	}
	if err := m.Section.Validate(); err != nil {
		return m, err
	}
	if err := m.Verdict.Validate(); err != nil {
		return m, err
	}
	if strings.TrimSpace(m.Key) == "" {
		return m, errors.New("feedback mark has no key: name the rule or shape the row groups by")
	}
	if len(m.Examples) > maxMarkExamples {
		m.Examples = m.Examples[:maxMarkExamples]
	}
	m.At = now.UnixMilli()
	line, err := json.Marshal(m)
	if err != nil {
		return m, err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return m, fmt.Errorf("feedback: %w", err)
	}
	f, err := os.OpenFile(filepath.Join(dir, marksFile), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return m, fmt.Errorf("feedback: %w", err)
	}
	// One short line per append, so concurrent writers do not interleave.
	if _, err := f.Write(append(line, '\n')); err != nil {
		_ = f.Close()
		return m, fmt.Errorf("feedback: %w", err)
	}
	return m, f.Close()
}

// ReadFeedbackMarks returns every mark in the store at dir, oldest first. A store nobody
// has written to holds no marks. A line that does not decode is an error naming it, since
// a silently dropped verdict reads as one the person never gave.
func ReadFeedbackMarks(dir string) ([]types.FeedbackMark, error) {
	path := filepath.Join(dir, marksFile)
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()
	var out []types.FeedbackMark
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for n := 1; sc.Scan(); n++ {
		if len(sc.Bytes()) == 0 {
			continue
		}
		var m types.FeedbackMark
		if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
			return nil, fmt.Errorf("feedback: %s:%d: %w", path, n, err)
		}
		out = append(out, m)
	}
	return out, sc.Err()
}
