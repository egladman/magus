package guard

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/sys/pid"
)

// unfinishedDir holds one file per hook judgment in progress, in a directory per session.
const unfinishedDir = "guard-unfinished"

// unfinishedGrace is how long past the host's hook timeout a judgment whose process still
// runs must have started before a later one counts it as never finished, so one still
// running beside this one is left alone.
const unfinishedGrace = 5 * time.Second

// advisoryUnfinished names the notice that earlier guard checks in a session never
// finished, so no rule judged their calls.
const advisoryUnfinished hint.MarkerKind = "guard-unfinished"

// hookTimeout is the host's hook timeout as its harness declares it, false when it declares
// none. Answering loads the workspace and the harness spell, so it is asked only about a
// judgment older than hookBudget: a host that gives up sooner than the guard's own budget
// is one the guard cannot answer in time anyway.
type hookTimeout func() (time.Duration, bool)

// noHookTimeout is a host whose harness declares no hook timeout, or one not asked.
func noHookTimeout() (time.Duration, bool) { return 0, false }

// boundedTimeout asks lookup once, and only while ctx has time left: the answer opens the
// workspace and loads the host's harness spell, seconds on a loaded machine, and a judgment
// still waiting on it when its host gives up is the cut-off the mark exists to report. A
// lookup that has not answered by ctx's end counts as no timeout declared.
func boundedTimeout(ctx context.Context, lookup func(context.Context) (time.Duration, bool)) hookTimeout {
	type declared struct {
		d  time.Duration
		ok bool
	}
	return sync.OnceValues(func() (time.Duration, bool) {
		if ctx.Err() != nil {
			return 0, false
		}
		got, err := awaitDeadline(ctx, func() (declared, error) {
			d, ok := lookup(ctx)
			return declared{d, ok}, nil
		})
		if err != nil {
			return 0, false
		}
		return got.d, got.ok
	})
}

// judgmentMark is the file one judgment keeps while it runs. Zero marks nothing.
type judgmentMark struct {
	dir, path string
	started   time.Time
	// parent is the process that started this one when the mark was made: the hook glue
	// the host waits on, or the host itself.
	parent int
}

type judgmentMarkKey struct{}

// beginJudgment leaves a file saying a judgment started at now, for a later judgment in the
// same session to find if this one never finishes. It marks nothing for a caller with no
// session key, which has no session to be told, or with no cache dir.
func beginJudgment(cacheDir, sessionKey string, now time.Time) judgmentMark {
	if cacheDir == "" || sessionKey == "" {
		return judgmentMark{}
	}
	sum := sha256.Sum256([]byte(sessionKey))
	dir := filepath.Join(cacheDir, unfinishedDir, hex.EncodeToString(sum[:6]))
	if os.MkdirAll(dir, 0o755) != nil {
		return judgmentMark{}
	}
	path := filepath.Join(dir, strconv.FormatInt(now.UnixNano(), 10)+"-"+strconv.Itoa(os.Getpid()))
	if os.WriteFile(path, nil, 0o644) != nil {
		return judgmentMark{}
	}
	return judgmentMark{dir: dir, path: path, started: now, parent: os.Getppid()}
}

// BeginJudgment marks the hook judgment req is about to get, before the caller loads the
// workspace's rules: a hook its host kills during that load is then still reported to the
// session's next judgment. Judge ends the mark it finds on the returned context.
//
// cacheDir resolves a workspace's cache directory, as [Dependencies.CacheDir] does. Nothing
// is marked for a request Judge would not judge: no input, an observation, a message, or a
// dry run.
func BeginJudgment(ctx context.Context, cacheDir func(root string) (string, error), req Request) context.Context {
	if req.Input == "" || req.Observe || req.Message || req.DryRun {
		return ctx
	}
	who := hookAttribution{Host: req.Host, Session: req.Session, Window: req.Window}
	deps := Dependencies{CacheDir: cacheDir}
	at := ctx
	if filepath.IsAbs(req.Dir) {
		at = hookContextAt(at, deps, req.Dir)
	}
	if env, ok := decodeHookEnvelope(req.Input); ok {
		who.Session = cmp.Or(who.Session, env.Who.Session)
		at = hookContextAt(at, deps, env.Cwd)
	}
	m := beginJudgment(hookLocation(at, deps).cacheDir, who.factsKey(), time.Now())
	if m.path == "" {
		return ctx
	}
	return context.WithValue(ctx, judgmentMarkKey{}, m)
}

// begunJudgment is the mark BeginJudgment left on ctx, zero for none.
func begunJudgment(ctx context.Context) judgmentMark {
	m, _ := ctx.Value(judgmentMarkKey{}).(judgmentMark)
	return m
}

// end removes this judgment's own file unless its host stopped waiting for it, which keeps
// the file for a later judgment to report. That is known two ways: the process that started
// this one is gone, as when a host kills its hook glue and leaves the `magus shell` child
// running, reparented; or the judgment ran past the hook timeout the host's harness
// declares. A host that declares none is not assumed to have given up.
func (m judgmentMark) end(now time.Time, timeout hookTimeout) {
	if m.path == "" || os.Getppid() != m.parent {
		return
	}
	if elapsed := now.Sub(m.started); elapsed >= hookBudget {
		if d, ok := timeout(); ok && elapsed >= d {
			return
		}
	}
	_ = os.Remove(m.path)
}

// abandoned claims and counts the session's earlier judgments that never finished: each one
// whose process ended without removing its file, and each one still running past the
// declared hook timeout. Each is claimed by removing its file, so two judgments ending at
// once never count one twice, and each is reported once.
func (m judgmentMark) abandoned(now time.Time, timeout hookTimeout) int {
	if m.path == "" {
		return 0
	}
	entries, err := os.ReadDir(m.dir)
	if err != nil {
		return 0
	}
	n := 0
	for _, e := range entries {
		if filepath.Join(m.dir, e.Name()) == m.path {
			continue
		}
		stamp, owner, _ := strings.Cut(e.Name(), "-")
		started, err := strconv.ParseInt(stamp, 10, 64)
		if err != nil {
			continue
		}
		if ownerPID, err := strconv.Atoi(owner); err == nil && pid.Alive(ownerPID) {
			age := now.Sub(time.Unix(0, started))
			if age < hookBudget+unfinishedGrace {
				continue
			}
			if d, ok := timeout(); !ok || age < d+unfinishedGrace {
				continue
			}
		}
		if os.Remove(filepath.Join(m.dir, e.Name())) == nil {
			n++
		}
	}
	return n
}

// unfinishedNote tells a session how many of its earlier guard checks its host gave up on.
func unfinishedNote(n int) string {
	lead := "1 earlier guard check in this session never finished"
	if n > 1 {
		lead = strconv.Itoa(n) + " earlier guard checks in this session never finished"
	}
	return lead + ": the host stopped waiting for a verdict, so no rule judged the call."
}
