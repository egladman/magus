package guard

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"time"

	"github.com/egladman/magus/internal/file"
	"github.com/egladman/magus/internal/guard/builtin"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/internal/json"
)

// lapsedRules is what the built-in rules that read only the call say of it, for a judgment
// that ran out of time: every rule Evaluate holds (busy-wait, sleep-poll, process-poll,
// the destructive VCS verbs, raw tools), the waiter rules a watcher or a backgrounded call
// meets, and spawn-waiter. None of them loads the workspace, reads the job store or opens
// a run log, so a lapse skips only the rules that do; whatever these refuse stays refused.
//
// They are graded by the settings the judgment loaded when it got that far, else by the
// ones the last judgment in this cache dir applied, else by the defaults.
func lapsedRules(ctx context.Context, base Dependencies, log *phaseLog, req Request, env hookRequest, who hookAttribution, input string, isPath bool, cacheDir string) ShellVerdict {
	deps := Dependencies{CacheDir: base.CacheDir, Spells: base.Spells, Builtins: recallSettings(cacheDir)}
	if loaded, ok := log.settled(); ok {
		deps.Builtins, deps.ShellRules, deps.ShellDialect = loaded.Builtins, loaded.ShellRules, loaded.ShellDialect
		if loaded.Spells != nil {
			deps.Spells = loaded.Spells
		}
	}
	switch {
	case env.IsSpawn:
		if !spawnWaitsFires(env.Spawn.Description, env.Value) {
			return ShellVerdict{}
		}
		d := denySpawnWaiter()
		return deps.grade(ShellVerdict{Deny: d.Say, Why: d.Why, Rule: denyRule{Name: denyRuleSpawnWaiter}})
	case isPath || env.IsContinue || input == "":
		return ShellVerdict{}
	}
	if v := Evaluate(deps, input); v.Deny != "" {
		return v
	}
	// The job store is what the judgment stalled on, so the waiter rules read none.
	ctx = job.WithSnapshot(ctx, job.Snapshot{Err: context.DeadlineExceeded})
	facts := hint.NewGate(cacheDir, who.factsKey())
	now := time.Now()
	d := effectiveDialect(deps.ShellDialect)
	if waiter, ok := waiterCall(ctx, deps, facts, env, req.CallBackground || env.CallBackground, req.Lease, who.Agent, input, d, now); ok {
		if w := gradeWaiter(deps, facts, who.Agent, waiter, now); w.Deny != "" {
			return w
		}
	}
	return ShellVerdict{}
}

// settingsFile holds the compiled-rule settings the last judgment in a cache dir applied,
// for one that lapses before it loads its own.
const settingsFile = "guard-settings.json"

// rememberSettings records settings as the cache dir's last applied ones, writing only when
// they changed. A failure costs a lapse its settings, never a verdict.
//
// A cache dir that does not exist yet is left uncreated: the job store keys a checkout's
// binding on the dir's path, which resolves through a symlink only once the dir exists, so
// creating it mid-judgment moves the key the binding was read under.
func rememberSettings(cacheDir string, settings map[string]builtin.Setting) {
	if cacheDir == "" {
		return
	}
	if info, err := os.Stat(cacheDir); err != nil || !info.IsDir() {
		return
	}
	raw, err := json.Marshal(settings)
	if err != nil {
		return
	}
	path := filepath.Join(cacheDir, settingsFile)
	if old, err := os.ReadFile(path); err == nil && bytes.Equal(old, raw) {
		return
	}
	_ = file.WriteFileAtomic(path, raw, 0o644)
}

// recallSettings is what rememberSettings last recorded in cacheDir, nil for the defaults
// when nothing readable was.
func recallSettings(cacheDir string) map[string]builtin.Setting {
	if cacheDir == "" {
		return nil
	}
	raw, err := os.ReadFile(filepath.Join(cacheDir, settingsFile))
	if err != nil {
		return nil
	}
	var settings map[string]builtin.Setting
	if json.Unmarshal(raw, &settings) != nil || len(settings) == 0 {
		return nil
	}
	return settings
}
