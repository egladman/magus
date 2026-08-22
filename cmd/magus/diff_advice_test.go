package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// stubAdviceDir builds an advisor directory holding the REAL advice.buzz plus the given
// stubs, so a test exercises the shipped collect sink rather than a re-implementation of
// it. The stubs stand in for the advisors themselves, whose findings depend on a git
// history and a loaded workspace.
func stubAdviceDir(t *testing.T, stubs map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	src, err := os.ReadFile(filepath.Join("..", "..", adviceDirRel, "advice.buzz"))
	if err != nil {
		t.Fatalf("read advice.buzz: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "advice.buzz"), src, 0o600); err != nil {
		t.Fatalf("write advice.buzz: %v", err)
	}
	for name, body := range stubs {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	return dir
}

// echoAdvisor publishes the local env contract back out, so a test can assert what
// reached the advisor rather than what the driver believes it sent.
const echoAdvisor = `import "advice";
import "std";

fun main() > void !> any {
    std\print("echo: looked at the tree");
    advice\publish(advice\env("REPO"), pr: advice\env("PR_NUMBER"), name: "echo",
        title: advice\env("PR_HEAD_SHA"), body: advice\env("PR_BASE"));
}
`

const retractingAdvisor = `import "advice";

fun main() > void !> any {
    advice\publish("", pr: "", name: "quiet", title: "Nothing to report", body: "");
}
`

const brokenAdvisor = `fun main() > void !> any {
    throw "broken on purpose";
}
`

func TestCollectAdviceEmitsSectionsAndSkipsTheForge(t *testing.T) {
	dir := stubAdviceDir(t, map[string]string{"echo.buzz": echoAdvisor})

	sections, notes, err := collectAdvice(context.Background(), dir, []string{"echo.buzz"}, "main")
	if err != nil {
		t.Fatalf("collectAdvice: %v", err)
	}
	if len(notes) != 0 {
		t.Fatalf("notes = %v, want none", notes)
	}
	if len(sections) != 1 {
		t.Fatalf("sections = %+v, want exactly one", sections)
	}
	got := sections[0]
	if got.Name != "echo" {
		t.Errorf("Name = %q, want %q", got.Name, "echo")
	}
	// The driver's base reaches the advisor as PR_BASE, which is the whole point of the
	// shared env helper's local mode.
	if got.Body != "main" {
		t.Errorf("Body = %q, want the base %q", got.Body, "main")
	}
	// PR_HEAD_SHA is supplied rather than left empty; an empty one makes every advisor
	// read the run as "not a pull request" and say nothing at all.
	if got.Title == "" {
		t.Error("Title is empty: PR_HEAD_SHA was not supplied to the advisor")
	}
}

func TestCollectAdviceKeepsARetraction(t *testing.T) {
	dir := stubAdviceDir(t, map[string]string{"quiet.buzz": retractingAdvisor})

	sections, _, err := collectAdvice(context.Background(), dir, []string{"quiet.buzz"}, "main")
	if err != nil {
		t.Fatalf("collectAdvice: %v", err)
	}
	if len(sections) != 1 || sections[0].Name != "quiet" || sections[0].Body != "" {
		t.Fatalf("sections = %+v, want one empty-bodied \"quiet\" section", sections)
	}
}

func TestCollectAdviceSurvivesABrokenAdvisor(t *testing.T) {
	dir := stubAdviceDir(t, map[string]string{
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
	if len(sections) != 2 {
		t.Fatalf("sections = %+v, want the two working advisors", sections)
	}
	if sections[0].Name != "echo" || sections[1].Name != "quiet" {
		t.Errorf("sections = %+v, want them in the order they were listed", sections)
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
	t.Setenv(adviceSinkEnv, "")
	if err := os.Unsetenv(adviceSinkEnv); err != nil {
		t.Fatalf("unsetenv: %v", err)
	}

	restore, err := setAdviceEnv("main")
	if err != nil {
		t.Fatalf("setAdviceEnv: %v", err)
	}
	if got := os.Getenv(adviceSinkEnv); got != adviceSinkCollect {
		t.Fatalf("%s = %q, want %q", adviceSinkEnv, got, adviceSinkCollect)
	}
	restore()
	// Absent, not empty: advice.buzz tells the two apart, so restoring to "" would leave
	// a later reader looking at a variable this call invented.
	if _, ok := os.LookupEnv(adviceSinkEnv); ok {
		t.Errorf("%s is still set after restore", adviceSinkEnv)
	}
}
