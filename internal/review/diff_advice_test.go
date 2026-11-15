package review

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// adviceDirWith builds an advisor directory holding the REAL advice.buzz plus the given
// advisor scripts, so a test exercises the shipped collect sink rather than a
// re-implementation of it. The scripts replace the real advisors, whose findings depend on a
// git history and a loaded workspace.
func adviceDirWith(t *testing.T, advisors map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	src, err := os.ReadFile(filepath.Join("..", "..", adviceDirRel, "advice.buzz"))
	if err != nil {
		t.Fatalf("read advice.buzz: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "advice.buzz"), src, 0o600); err != nil {
		t.Fatalf("write advice.buzz: %v", err)
	}
	for name, body := range advisors {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	return dir
}

// echoAdvisor publishes the local pull request back out, so a test can assert what
// reached the advisor rather than what the driver believes it sent.
const echoAdvisor = `import "advice";
import "std";

fun main() > void !> any {
    std\print("echo: looked at the tree");
    final pull = advice\pullRequest();
    advice\publish(pull.repo, pr: pull.number, name: "echo", title: pull.headSha, body: pull.base);
}
`

// warningAdvisor trips BZZ3002 (text put before a string in a loop)
// and still publishes, like a shipped advisor that lints imperfectly.
const warningAdvisor = `import "advice";

fun main() > void !> any {
    var text = "";
    foreach (part in ["standing", "still"]) {
        text = part + text;
    }
    advice\publish("", pr: "", name: "warned", title: "Ran anyway", body: text);
}
`

const retractingAdvisor = `import "advice";

fun main() > void !> any {
    advice\publish("", pr: "", name: "quiet", title: "Nothing to report", body: "");
}
`

// rangeAdvisor publishes the rev spec diffRange handed it. Only the driver can put an
// advisor into local mode (advice.buzz decides on os\env), so the local half of the range
// contract is only reachable from here.
const rangeAdvisor = `import "advice";

fun main() > void !> any {
    final pull = advice\pullRequest();
    advice\publish("", pr: "", name: "range", title: "Range",
        body: advice\diffRange(pull.base, head: pull.headSha));
}
`

const brokenAdvisor = `fun main() > void !> any {
    throw "broken on purpose";
}
`

// staleBaseAdvisor exercises the half of the local-base contract no Buzz test can reach:
// advice.buzz decides on os\env, and os\with_env does not touch this process's own
// environment, so only a driver that really sets the variable can put an advisor into
// local mode.
//
// The base is one no remote can resolve. In CI mode fetchBase runs the refspec fetch,
// git fails to find the ref, and fetchBase throws, so merely reaching publish is the
// proof that the local-mode gate returned before any git ran.
const staleBaseAdvisor = `import "advice";

fun main() > void !> any {
    advice\fetchBase("magus-advice-no-such-base", refspec: true);
    advice\publish("", pr: "", name: "stale", title: "Stale base", body: advice\pullRequest().base);
}
`

func TestCollectAdviceEmitsSectionsAndSkipsTheForge(t *testing.T) {
	dir := adviceDirWith(t, map[string]string{"echo.buzz": echoAdvisor})

	sections, notes, err := collectAdvice(context.Background(), dir, []string{"echo.buzz"}, "main")
	if err != nil {
		t.Fatalf("collectAdvice: %v", err)
	}
	if len(notes) != 0 {
		// Compile warnings are notes too, so a note here may be a BZZ diagnostic raised by
		// the shipped advice.buzz rather than anything the advisor did.
		t.Fatalf("notes = %v, want none", notes)
	}
	if len(sections) != 1 {
		t.Fatalf("sections = %+v, want exactly one", sections)
	}
	got := sections[0]
	// headSha is supplied rather than left empty; an empty one makes every advisor read
	// the run as "not a pull request" and say nothing at all.
	if got.Title == "" {
		t.Error("Title is empty: the head sha was not supplied to the advisor")
	}
	// The driver's base reaches the advisor as the pull request's base, which is the whole
	// point of advice.buzz's local mode.
	want := AdviceSection{Name: "echo", Title: got.Title, Body: "main"}
	if got != want {
		t.Errorf("section = %+v, want %+v", got, want)
	}
}

func TestCollectAdviceKeepsARetraction(t *testing.T) {
	dir := adviceDirWith(t, map[string]string{"quiet.buzz": retractingAdvisor})

	sections, _, err := collectAdvice(context.Background(), dir, []string{"quiet.buzz"}, "main")
	if err != nil {
		t.Fatalf("collectAdvice: %v", err)
	}
	if want := []AdviceSection{{Name: "quiet", Title: "Nothing to report"}}; !slices.Equal(sections, want) {
		t.Fatalf("sections = %+v, want one empty-bodied \"quiet\" section %+v", sections, want)
	}
}

func TestCollectAdviceSurvivesABrokenAdvisor(t *testing.T) {
	dir := adviceDirWith(t, map[string]string{
		"echo.buzz":   echoAdvisor,
		"broken.buzz": brokenAdvisor,
		"quiet.buzz":  retractingAdvisor,
	})
	// Broken in the MIDDLE: a failure that stops the run would take the third advisor
	// with it and look identical to one that had nothing to say.
	order := []string{"echo.buzz", "broken.buzz", "quiet.buzz"}

	sections, notes, err := collectAdvice(context.Background(), dir, order, "main")
	if err != nil {
		t.Fatalf("collectAdvice: %v", err)
	}
	if len(notes) != 1 {
		t.Fatalf("notes = %v, want exactly one", notes)
	}
	if !strings.Contains(notes[0], "broken.buzz") || !strings.Contains(notes[0], "broken on purpose") {
		t.Errorf("note = %q, want it to name the advisor and the error", notes[0])
	}
	// The renderer prints notes verbatim, so an unstamped failure would read as a
	// warning from an advisor that ran.
	if !strings.HasPrefix(notes[0], "could not run: ") {
		t.Errorf("note = %q, want the could-not-run stamp on a failure", notes[0])
	}
	if len(sections) != 2 {
		t.Fatalf("sections = %+v, want the two working advisors", sections)
	}
	if sections[0].Name != "echo" || sections[1].Name != "quiet" {
		t.Errorf("sections = %+v, want them in the order they were listed", sections)
	}
}

// A warning from an advisor that RAN is not the reader's business.
//
// These are lint diagnostics about the advisor's own source: magus's shipped scripts, not
// anything in the changeset. Printing them unconditionally is what made a one-line docs fix
// draw ~40 lines of BZZ3001/BZZ3002 about merge-conflict.buzz and doctor.buzz; four
// personas hit it independently and the drive-by contributor named it as the point they
// nearly abandoned the repo, assuming they had broken something.
func TestCollectAdviceDropsWarningsFromAnAdvisorThatRan(t *testing.T) {
	dir := adviceDirWith(t, map[string]string{"warned.buzz": warningAdvisor})

	// Without a warning to drop, the empty notes below would prove nothing.
	restore, err := setAdviceEnv("main")
	if err != nil {
		t.Fatalf("setAdviceEnv: %v", err)
	}
	_, warnings, err := runAdvisor(context.Background(), dir, "warned.buzz")
	restore()
	if err != nil {
		t.Fatalf("runAdvisor: %v", err)
	}
	if !slices.ContainsFunc(warnings, func(w string) bool { return strings.Contains(w, "[BZZ3002]") }) {
		t.Fatalf("warnings = %v, want BZZ3002 from the fixture", warnings)
	}

	sections, notes, err := collectAdvice(context.Background(), dir, []string{"warned.buzz"}, "main")
	if err != nil {
		t.Fatalf("collectAdvice: %v", err)
	}
	if len(sections) != 1 {
		t.Fatalf("sections = %+v, want the advisor's finding: it ran", sections)
	}
	if len(notes) != 0 {
		t.Fatalf("notes = %v, want none: the advisor ran, so its own lint is not the reader's business", notes)
	}
}

// ...but when the advisor CRASHED, its warnings are kept and ordered above the failure: a
// BZZ3001 over a crash is usually the explanation for it, which is the whole reason they
// were ever collected.
func TestCollectAdviceKeepsWarningsFromAnAdvisorThatFailed(t *testing.T) {
	dir := adviceDirWith(t, map[string]string{"broken.buzz": brokenAdvisor})

	_, notes, err := collectAdvice(context.Background(), dir, []string{"broken.buzz"}, "main")
	if err != nil {
		t.Fatalf("collectAdvice: %v", err)
	}
	if len(notes) == 0 {
		t.Fatal("want the failure surfaced as a note")
	}
	last := notes[len(notes)-1]
	if !strings.HasPrefix(last, "could not run: ") {
		t.Errorf("last note = %q, want the could-not-run stamp last, after any warnings", last)
	}
}

// TestLocalModeDiffsTheWorkingTree pins the half of the range contract advice.buzz cannot
// reach: only a driver that really sets the mode variable puts an advisor into local mode.
//
// The assertion is the REV the advisors are handed, not the diff it produces, because the
// behaviour being bought is git's: `git diff <commit>` with no second rev compares the
// WORKING TREE against that commit, where `base...head` stops at the last commit. Choosing
// the merge base is this code's decision and the only part worth pinning.
func TestLocalModeDiffsTheWorkingTree(t *testing.T) {
	// The advisors run git against the process working directory, which under `go test` is
	// this package inside magus's own repository, a real clone with a real history.
	out, err := exec.Command("git", "merge-base", "origin/main", "HEAD").Output()
	if err != nil {
		t.Skip("origin/main is not in this clone, which is the fallback path below, not this one")
	}
	want := strings.TrimSpace(string(out))

	dir := adviceDirWith(t, map[string]string{"range.buzz": rangeAdvisor})
	sections, notes, err := collectAdvice(context.Background(), dir, []string{"range.buzz"}, "main")
	if err != nil {
		t.Fatalf("collectAdvice: %v", err)
	}
	if len(notes) != 0 {
		t.Fatalf("notes = %v, want none", notes)
	}
	if len(sections) != 1 {
		t.Fatalf("sections = %+v, want exactly one", sections)
	}
	if got := sections[0].Body; got != want {
		t.Errorf("range = %q, want the merge base %q: a three-dot range would stop at the "+
			"last commit and report the uncommitted change as absent", got, want)
	}
}

// TestLocalModeReadsAStaleBaseWithoutFetching pins the rule that a local advice run stays
// off the network. `magus diff` is a read-only report on a working tree (it may run
// offline, and under --watch it re-fires on every save), so fetching, and writing
// refs/remotes/ while doing it, is a report mutating what it reports on.
func TestLocalModeReadsAStaleBaseWithoutFetching(t *testing.T) {
	dir := adviceDirWith(t, map[string]string{"stale.buzz": staleBaseAdvisor})

	sections, notes, err := collectAdvice(context.Background(), dir, []string{"stale.buzz"}, "main")
	if err != nil {
		t.Fatalf("collectAdvice: %v", err)
	}
	// A note here IS the defect: fetchBase reached git, git could not resolve the refspec,
	// and it threw. On the correct path no git runs at all.
	if len(notes) != 0 {
		t.Fatalf("notes = %v, want none: fetchBase went to the network during a local run", notes)
	}
	if len(sections) != 1 {
		t.Fatalf("sections = %+v, want exactly one", sections)
	}
	// The advisor saw the driver's base rather than a pull request's. Saying that the base
	// went unfetched is the DRIVER's line, once for the whole set (see
	// TestImpactBaseSeparatesOldFromAbsent), so no section carries the disclaimer.
	if body := sections[0].Body; body != "main" {
		t.Errorf("Body = %q, want the local base the driver supplied", body)
	}
}

func TestParseAdviceSectionsDropsProgressLines(t *testing.T) {
	out := "unclaimed: advised on a, b\n" +
		`{"name":"unclaimed","title":"Files no project claims (2)","body":"a\nb"}` + "\n" +
		"not json at all\n" +
		`{"title":"no name","body":"x"}` + "\n"

	got := parseAdviceSections(out)
	if len(got) != 1 {
		t.Fatalf("got %+v, want only the one named section", got)
	}
	if got[0].Body != "a\nb" {
		t.Errorf("Body = %q, want the escaped newline decoded", got[0].Body)
	}
}

func TestSetAdviceEnvRestoresAbsence(t *testing.T) {
	t.Setenv(adviceModeEnv, "")
	if err := os.Unsetenv(adviceModeEnv); err != nil {
		t.Fatalf("unsetenv: %v", err)
	}

	restore, err := setAdviceEnv("main")
	if err != nil {
		t.Fatalf("setAdviceEnv: %v", err)
	}
	if got := os.Getenv(adviceModeEnv); got != adviceModeLocal {
		t.Fatalf("%s = %q, want %q", adviceModeEnv, got, adviceModeLocal)
	}
	restore()
	// Absent, not empty: advice.buzz tells the two apart, so restoring to "" would leave
	// a later reader looking at a variable this call invented.
	if _, ok := os.LookupEnv(adviceModeEnv); ok {
		t.Errorf("%s is still set after restore", adviceModeEnv)
	}
}

// adviceLocalExclusions are read-only advisors advise.buzz runs that `magus diff`
// deliberately does not. The value is the reason, and carrying one is the point: leaving
// an advisor out is a decision, and a decision with no reason recorded is indistinguishable
// from having forgotten it.
var adviceLocalExclusions = map[string]string{
	"first-contribution.buzz": "asks the forge whether the author has merged before, and a " +
		"working tree has no author and no first-time contributor to welcome",
	"merge-queue.buzz": "reads the pull request's review state and labels through its own gh " +
		"call, and a working tree has no pull request to queue",
	"trusted-script-moved.buzz": "asks the forge for the pull request's renamed and removed " +
		"files, and a working tree has no pull request",
}

// adviseScripts returns the scripts one of advise.buzz's advisor lists names, in order.
//
// A LINE SCAN of the Buzz source rather than running it: the lists are literals of one
// `Advisor{input = "...", script = "..."},` per line, so the scan reads what a human reads.
// It is allowed to be wrong in one direction only: a line it cannot read fails the test
// naming it, never a quietly shorter list.
func adviseScripts(t *testing.T, src, list string) []string {
	t.Helper()
	_, body, ok := strings.Cut(src, fmt.Sprintf(`
final %s = [
`, list))
	if !ok {
		t.Fatalf("advise.buzz declares no %q list", list)
	}
	body, _, ok = strings.Cut(body, "\n];")
	if !ok {
		t.Fatalf("advise.buzz's %q list is not closed by a line reading \"];\"", list)
	}
	var scripts []string
	for _, line := range strings.Split(body, "\n") {
		_, rest, ok := strings.Cut(line, `script = "`)
		if !ok {
			t.Errorf("advise.buzz's %q list carries a line the scan cannot read: %q", list, line)
			continue
		}
		script, _, _ := strings.Cut(rest, `"`)
		scripts = append(scripts, script)
	}
	return scripts
}

// TestConformanceTablesCoverEveryCheck holds conformance.buzz's symbolChecks to the check
// names types declares, both ways: a check with no table would report into nothing, and a
// table with no check is a heading nobody can reach.
func TestConformanceTablesCoverEveryCheck(t *testing.T) {
	decls, err := os.ReadFile(filepath.Join("..", "..", "types", "diff", "diff.go"))
	require.NoError(t, err)
	script, err := os.ReadFile(filepath.Join("..", "..", adviceDirRel, "conformance.buzz"))
	require.NoError(t, err)

	var declared []string
	for _, m := range regexp.MustCompile(`\n\tCheck[A-Za-z]+ += "([a-z-]+)"`).FindAllStringSubmatch(string(decls), -1) {
		declared = append(declared, m[1])
	}
	var tabled []string
	for _, m := range regexp.MustCompile(`\{"check": "([a-z-]+)"`).FindAllStringSubmatch(string(script), -1) {
		tabled = append(tabled, m[1])
	}
	require.NotEmpty(t, declared, "the scan found no check constants in types/diff.go")
	assert.ElementsMatch(t, declared, tabled)
}

// TestLocalAdvisorsMatchAdviseBuzz is the gate on localAdvisors restating advise.buzz by
// hand. A read-only advisor added to CI that never reaches `magus diff` is invisible
// otherwise: both halves keep working, and the local command is simply quieter than the
// pull request for no stated reason.
func TestLocalAdvisorsMatchAdviseBuzz(t *testing.T) {
	dir := filepath.Join("..", "..", adviceDirRel)
	src, err := os.ReadFile(filepath.Join(dir, "advise.buzz"))
	require.NoError(t, err)
	readOnly := adviseScripts(t, string(src), "reading")
	if len(readOnly) == 0 {
		t.Fatalf("the scan found 0 advisors: it is measuring nothing, and every comparison " +
			"below would pass on an empty file")
	}

	// Every advisor here is read-only: none pushes to the pull request. A script that
	// passes "push" to git is a fixer, and fixers do not belong in this directory.
	for _, file := range readOnly {
		body, err := os.ReadFile(filepath.Join(dir, file))
		require.NoError(t, err)
		if strings.Contains(string(body), `"push"`) {
			t.Errorf("%s pushes, so it does not belong among the read-only advisors", file)
		}
	}

	want := make([]string, 0, len(readOnly))
	for _, file := range readOnly {
		if _, excluded := adviceLocalExclusions[file]; !excluded {
			want = append(want, file)
		}
	}
	for file := range adviceLocalExclusions {
		if !slices.Contains(readOnly, file) {
			t.Errorf("adviceLocalExclusions names %q, which advise.buzz no longer runs as a "+
				"read-only advisor: drop the entry, since the exclusion now protects nothing", file)
		}
	}
	if slices.Equal(localAdvisors, want) {
		return
	}

	mismatched := false
	for _, file := range want {
		if !slices.Contains(localAdvisors, file) {
			mismatched = true
			t.Errorf("advise.buzz runs read-only advisor %q and `magus diff` does not. Add it "+
				"to localAdvisors, or name it in adviceLocalExclusions with the reason it has "+
				"no local meaning.", file)
		}
	}
	for _, file := range localAdvisors {
		if !slices.Contains(want, file) {
			mismatched = true
			t.Errorf("localAdvisors runs %q, which advise.buzz does not run as a read-only "+
				"advisor: it was renamed, removed, or has become a writer.", file)
		}
	}
	if !mismatched {
		// Same members either way, so only the order moved. It is not cosmetic: a local
		// reader gets the findings in the order CI chose to present them.
		t.Errorf("localAdvisors = %v, want advise.buzz's order %v", localAdvisors, want)
	}
}
