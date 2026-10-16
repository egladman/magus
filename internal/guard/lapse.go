package guard

import (
	"bytes"
	"context"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/egladman/magus/internal/file"
	"github.com/egladman/magus/internal/guard/builtin"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/internal/json"
)

// lapsedRules is what the built-in rules that read only the call say of it, for a judgment
// that ran out of time: every rule Evaluate holds (busy-wait, sleep-poll, process-poll,
// the destructive VCS verbs, raw tools), the waiter rules a watcher, a timer or a
// backgrounded call meets, and spawn-waiter. None of them loads the workspace, reads the job store or opens
// a run log, so a lapse skips only the rules that do; whatever these refuse stays refused.
//
// They are graded by the settings the judgment loaded when it got that far, else by the
// ones the last judgment in this cache dir applied, else by the defaults.
func lapsedRules(ctx context.Context, base Dependencies, log *phaseLog, req Request, env hookRequest, who hookAttribution, input string, isPath bool, cacheDir string) ShellVerdict {
	deps := Dependencies{CacheDir: base.CacheDir, Spells: base.Spells, Builtins: recalledSettings(cacheDir)}
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

// settingsFile holds the compiled-rule settings the last judgment in a cache dir applied, for
// one that lapses before it loads its own. It is JSON Lines: a line per rule whose setting
// changed, the last line for a rule being its setting.
const settingsFile = "guard-settings.jsonl"

// oldSettingsFile is the JSON document an older magus kept the settings in.
const oldSettingsFile = "guard-settings.json"

// settingLine is one line of settingsFile: a rule's setting, or the rule's removal.
type settingLine struct {
	Rule    string           `json:"rule"`
	Removed bool             `json:"removed,omitempty"`
	Setting *builtin.Setting `json:"setting,omitempty"`
}

func settingsLog(cacheDir string) file.JSONL[settingLine] {
	return file.JSONL[settingLine]{
		Path: filepath.Join(cacheDir, settingsFile),
		Key:  func(l settingLine) string { return l.Rule },
		Live: func(l settingLine) bool { return !l.Removed && l.Setting != nil },
		Old:  filepath.Join(cacheDir, oldSettingsFile),
	}
}

// rememberSettings records settings as the cache dir's last applied ones, appending a line
// for each rule whose setting changed and for each rule no longer set, and nothing when none
// did. A failure costs a lapse its settings, never a verdict.
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
	_ = settingsLog(cacheDir).Update(context.Background(), func(stored []settingLine) []settingLine {
		held := make(map[string]settingLine, len(stored))
		for _, l := range stored {
			held[l.Rule] = l
		}
		var add []settingLine
		for _, rule := range slices.Sorted(maps.Keys(settings)) {
			setting := settings[rule]
			old, ok := held[rule]
			if ok && sameEncoding(*old.Setting, setting) {
				continue
			}
			add = append(add, settingLine{Rule: rule, Setting: &setting})
		}
		for _, l := range stored {
			if _, kept := settings[l.Rule]; !kept {
				add = append(add, settingLine{Rule: l.Rule, Removed: true})
			}
		}
		return add
	})
}

// sameEncoding reports whether a and b encode alike, which is the sameness a setting read back
// from the file can have: an empty list of markers reads back as none.
func sameEncoding(a, b builtin.Setting) bool {
	x, errA := json.Marshal(a)
	y, errB := json.Marshal(b)
	return errA == nil && errB == nil && bytes.Equal(x, y)
}

// recallSettings is what rememberSettings last recorded in cacheDir, nil for the defaults
// when nothing was. A file that cannot be read, or an older magus's guard-settings.json beside
// it, is an error.
func recallSettings(cacheDir string) (map[string]builtin.Setting, error) {
	var held []settingLine
	var err error
	if cacheDir != "" {
		held, err = settingsLog(cacheDir).Load()
	}
	if err != nil || len(held) == 0 {
		return nil, err
	}
	settings := make(map[string]builtin.Setting, len(held))
	for _, l := range held {
		settings[l.Rule] = *l.Setting
	}
	return settings, nil
}

// recalledSettings is recallSettings for a caller that grades by the defaults when the record
// cannot be read, and logs that it did.
func recalledSettings(cacheDir string) map[string]builtin.Setting {
	settings, err := recallSettings(cacheDir)
	if err != nil {
		reportGuardMemo(fmt.Errorf("grading by the default settings: %w", err))
	}
	return settings
}
