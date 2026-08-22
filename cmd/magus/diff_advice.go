package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/egladman/magus"
	"github.com/egladman/magus/internal/interp/bindings"
	json "github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/libs/gopherbuzz"
	vm "github.com/egladman/magus/libs/gopherbuzz/vm"
	"github.com/egladman/magus/types"
)

// adviceSection is one advisor's finding: the section it owns in the pull-request
// comment, rendered for a local reader instead. An EMPTY Body is a retraction - the
// advisor ran and found nothing - and is a section like any other, not an absence.
type adviceSection struct {
	Name  string `json:"name"  yaml:"name"`
	Title string `json:"title" yaml:"title"`
	Body  string `json:"body"  yaml:"body"`
}

// adviceDirRel is where this repository keeps the advisors. They are checked in as a
// composite action because CI is where they run first, not because the pull request is
// the only place their answers are useful.
var adviceDirRel = filepath.Join(".github", "actions", "advice")

// localAdvisors is every advisor a local run may execute, in the order action.yml runs
// them. action.yml is the source of truth for that set; this list restates it.
//
// Restating it rather than reading the directory is deliberate, and the deciding reason
// is safety. Three of the scripts in that directory PUSH to a branch, and nothing about a
// filename separates them from the read-only ones: `fix-generated-drift.buzz` and
// `fix-merge-conflict.buzz` share a prefix that `settle-fix-labels.buzz` does not. A
// sweep of *.buzz would therefore enroll a writer into a local command the moment someone
// added one, and the failure mode of getting that wrong is a `magus diff` that pushes.
//
// Two lesser reasons: the directory also holds `advice.buzz`, which is the shared library
// and not an advisor at all, and `api-surface.buzz`, which action.yml does not run. And
// filename order is not the order action.yml chose.
//
// first-contribution.buzz is the one read-only advisor deliberately left out: it asks the
// forge who opened the pull request, through its own `gh` call rather than through
// advice.buzz, so the collect sink cannot intercept it. It also has no local meaning.
var localAdvisors = []string{
	"merge-conflict.buzz",
	"hand-edited-generated.buzz",
	"target-outputs.buzz",
	"doctor.buzz",
	"version-floor.buzz",
	"unclaimed.buzz",
	"blast-radius.buzz",
	"skip-cache.buzz",
	"conformance.buzz",
	"missing-target.buzz",
}

// runLocalAdvisors runs the read-only PR advisors against the local tree and returns
// their sections in localAdvisors order, plus a note per advisor that failed.
//
// base is a BRANCH name, not a rev: the advisors compare against `origin/<base>`, the
// same way they use PR_BASE in CI.
//
// An advisor that raises produces a note and never an error - one broken advisor must not
// take the other nine down, because the caller is showing a reader what magus knows and
// nine tenths of that is still worth showing. The error return is for a failure that
// makes the whole set meaningless.
//
// Not safe for concurrent use: the advisors read their inputs with os\env, so the two
// collect-mode variables are set process-wide for the duration of the call.
func runLocalAdvisors(ctx context.Context, m *magus.Magus, base string) ([]adviceSection, []string, error) {
	dir := filepath.Join(m.Root(), adviceDirRel)
	if _, err := os.Stat(dir); err != nil {
		return nil, []string{fmt.Sprintf("no advisors in this workspace: %s is not readable (%v)", dir, err)}, nil
	}

	// The advisors ask magus about the workspace (magus\describeFile, magus\diff,
	// magus\affectedImpact), which reads it off the context the way `magus buzz` does.
	// The caller's already-loaded workspace is attached rather than loaded again:
	// loadMagus is once-per-process and panics on a second call with a different root.
	if m != nil {
		ctx = types.WithWorkspace(ctx, m)
	}
	return collectAdvice(ctx, dir, localAdvisors, base)
}

// collectAdvice is runLocalAdvisors without the workspace and directory resolution, so a
// test can drive it against stub advisors.
func collectAdvice(ctx context.Context, dir string, files []string, base string) ([]adviceSection, []string, error) {
	restore, err := setAdviceEnv(base)
	if err != nil {
		return nil, nil, err
	}
	defer restore()

	var sections []adviceSection
	var notes []string
	for _, file := range files {
		out, err := runAdvisor(ctx, dir, file)
		// Sections printed before the failure are kept: an advisor that publishes and
		// then dies has already said something true, and dropping it would report the
		// finding as absent rather than as partial.
		sections = append(sections, parseAdviceSections(out)...)
		if err != nil {
			notes = append(notes, fmt.Sprintf("%s: %v", file, err))
		}
	}
	return sections, notes, nil
}

// adviceEnv is the contract between this driver and advice.buzz. The names are read there
// with os\env; there is no per-session environment to hand a Buzz script, so they are set
// on the process.
const (
	adviceSinkEnv     = "MAGUS_ADVICE_SINK"
	adviceSinkCollect = "collect"
	adviceBaseEnv     = "MAGUS_ADVICE_BASE"
)

// setAdviceEnv puts the collect-mode variables on the process and returns the restore.
// It restores an absent variable to absent rather than to empty, because advice.buzz
// distinguishes the two.
func setAdviceEnv(base string) (func(), error) {
	saved := map[string]*string{}
	for name, want := range map[string]string{adviceSinkEnv: adviceSinkCollect, adviceBaseEnv: base} {
		if had, ok := os.LookupEnv(name); ok {
			saved[name] = &had
		} else {
			saved[name] = nil
		}
		if err := os.Setenv(name, want); err != nil {
			return func() {}, fmt.Errorf("magus diff: set %s: %w", name, err)
		}
	}
	return func() {
		for name, was := range saved {
			if was == nil {
				_ = os.Unsetenv(name)
				continue
			}
			_ = os.Setenv(name, *was)
		}
	}, nil
}

// runAdvisor evaluates one advisor in-process and returns whatever it printed. The
// session is built exactly as `magus buzz <file>` builds one - same module surface, same
// strict parse mode - so an advisor cannot behave one way in CI and another way here.
// std.print is redirected to a buffer, which is how the sections come back.
func runAdvisor(ctx context.Context, dir, file string) (string, error) {
	src, err := os.ReadFile(filepath.Join(dir, file))
	if err != nil {
		return "", err
	}
	sess := buzz.NewSession(ctx)
	defer func() { _ = sess.Close() }()
	// `import "advice"` resolves against this list. The composite action gets the same
	// effect from BUZZ_INCLUDE_PATH; setting it directly keeps the advisor's view of the
	// filesystem out of the process environment.
	sess.SetIncludeDirs([]string{dir})

	var out bytes.Buffer
	bindings.RegisterModuleSurface(ctx, sess, bindings.WithScriptOutput(&out))
	bindings.RegisterMagusNamespace(ctx, sess)
	bindings.RegisterSpellSourceModules(sess)

	if err := sess.Exec(ctx, string(src)); err != nil {
		return out.String(), err
	}
	mainFn := sess.GetGlobal("main")
	if !mainFn.IsFun() {
		return out.String(), fmt.Errorf("no main() to run")
	}
	if _, err := sess.CallValue(ctx, mainFn, []vm.Value{vm.ListValue(nil)}); err != nil {
		return out.String(), err
	}
	return out.String(), nil
}

// parseAdviceSections picks the section objects out of one advisor's output. An advisor
// also prints a progress line for a human ("unclaimed: advised on ..."), so the two are
// told apart by decoding rather than by position: a line that is not a section object is
// the advisor talking, and is dropped.
func parseAdviceSections(out string) []adviceSection {
	var got []adviceSection
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "{") {
			continue
		}
		var s adviceSection
		if err := json.Unmarshal([]byte(line), &s); err != nil || s.Name == "" {
			continue
		}
		got = append(got, s)
	}
	return got
}
