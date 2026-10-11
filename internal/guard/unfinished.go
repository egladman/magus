package guard

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/egladman/magus/internal/hint"
)

// unfinishedDir holds one file per hook judgment in progress, in a directory per session.
const unfinishedDir = "guard-unfinished"

// unfinishedAfter is how long ago a judgment must have started before a later one counts it
// as never finished. The shipped wiring kills a hook at ten seconds and runs the call
// unjudged, so a file older than that belongs to a hook that was killed, not to one still
// running beside this one.
const unfinishedAfter = 15 * time.Second

// judgmentMark is the file one judgment keeps while it runs. Zero marks nothing.
type judgmentMark struct {
	dir, path string
}

// beginJudgment leaves a file saying a judgment started at now, for a later judgment in the
// same session to find if this one never finishes. It marks nothing for a caller with no
// key, which has no session to be told, or with no cache dir.
func beginJudgment(cacheDir, key string, now time.Time) judgmentMark {
	if cacheDir == "" || key == "" {
		return judgmentMark{}
	}
	sum := sha256.Sum256([]byte(key))
	dir := filepath.Join(cacheDir, unfinishedDir, hex.EncodeToString(sum[:6]))
	if os.MkdirAll(dir, 0o755) != nil {
		return judgmentMark{}
	}
	path := filepath.Join(dir, strconv.FormatInt(now.UnixNano(), 10)+"-"+strconv.Itoa(os.Getpid()))
	if os.WriteFile(path, nil, 0o644) != nil {
		return judgmentMark{}
	}
	return judgmentMark{dir: dir, path: path}
}

// finish removes this judgment's file and reports how many earlier judgments of the session
// never finished. Each is claimed by removing its file, so two judgments finishing at once
// never count one twice, and each is reported once.
func (m judgmentMark) finish(now time.Time) int {
	if m.path == "" {
		return 0
	}
	_ = os.Remove(m.path)
	entries, err := os.ReadDir(m.dir)
	if err != nil {
		return 0
	}
	n := 0
	for _, e := range entries {
		stamp, _, _ := strings.Cut(e.Name(), "-")
		started, err := strconv.ParseInt(stamp, 10, 64)
		if err != nil || now.Sub(time.Unix(0, started)) < unfinishedAfter {
			continue
		}
		if os.Remove(filepath.Join(m.dir, e.Name())) == nil {
			n++
		}
	}
	return n
}

// unfinishedNote tells a session how many of its earlier guard checks its host gave up on.
func unfinishedNote(n int) string {
	lead := "1 earlier guard check in this session never finished and was"
	if n > 1 {
		lead = strconv.Itoa(n) + " earlier guard checks in this session never finished and were"
	}
	return lead + " allowed: the host stopped waiting for a verdict and ran the call with no rule applied."
}

// unfinishedKind is the notice an unfinished-check note rides on when nothing else spoke:
// the one that says a rule judged nothing, for the tool this call is.
func unfinishedKind(tool string) hint.MarkerKind {
	if tool == hookToolWrite {
		return advisoryWriteRuleFailed
	}
	return advisoryCommandRuleFailed
}
