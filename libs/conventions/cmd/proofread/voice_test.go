package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egladman/magus/libs/conventions/proofread"
)

// synthReplies are n short replies an invented author wrote.
func synthReplies(n int) []string {
	extra := []string{"", " It's close.", " We'll see.", " I'd keep it."}
	out := make([]string, n)

	for i := range out {
		out[i] = fmt.Sprintf("I think case %d works. Let's merge it after the build.%s", i, extra[i%len(extra)])
	}

	return out
}

func runVoiceArgs(t *testing.T, stdin string, args ...string) (code int, stdout, stderr string) {
	t.Helper()

	var out, errOut bytes.Buffer

	code = run(args, strings.NewReader(stdin), &out, &errOut)

	return code, out.String(), errOut.String()
}

func readVoice(t *testing.T, path string) *proofread.Voice {
	t.Helper()

	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	v, err := proofread.ReadVoice(f)
	if err != nil {
		t.Fatal(err)
	}

	return v
}

func TestVoiceBuildMeasuresFilesIntoAPrivateFile(t *testing.T) {
	dir := t.TempDir()
	args := []string{"voice", "build", "-kind", "review-reply", "-o", filepath.Join(dir, "out", "voice.json")}

	for i, text := range synthReplies(proofread.MinVoiceTexts) {
		args = append(args, writeFile(t, dir, fmt.Sprintf("r%d.md", i), text))
	}

	code, stdout, stderr := runVoiceArgs(t, "", args...)
	if code != exitOK || stdout != "" {
		t.Fatalf("voice build: code %d stdout %q stderr %q", code, stdout, stderr)
	}

	path := filepath.Join(dir, "out", "voice.json")
	if !strings.HasPrefix(stderr, "review-reply: 30 texts, ") || !strings.HasSuffix(stderr, "wrote "+path+"\n") {
		t.Errorf("stderr = %q", stderr)
	}

	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("voice file: %v, %v, want mode 0600", info, err)
	}

	if v := readVoice(t, path); v.Kinds[proofread.KindReviewReply].Ranges == nil {
		t.Errorf("voice = %+v, want ranges for review-reply", v)
	}
}

func TestVoiceBuildReadsJSONLinesAndNULSeparatedStdin(t *testing.T) {
	dir := t.TempDir()

	var lines strings.Builder

	for i, text := range synthReplies(4) {
		kind := map[bool]string{true: "issue", false: ""}[i%2 == 0]
		data, err := json.Marshal(map[string]string{"body": text, "type": kind})
		if err != nil {
			t.Fatal(err)
		}

		lines.Write(append(data, '\n'))
	}

	jsonl := filepath.Join(dir, "jsonl.json")
	if code, _, stderr := runVoiceArgs(t, lines.String(), "voice", "build", "-kind", "review-reply",
		"-field", "body", "-kind-field", "type", "-o", jsonl); code != exitOK {
		t.Fatalf("voice build from JSON lines: %s", stderr)
	}

	if v := readVoice(t, jsonl); v.Kinds[proofread.KindIssue].Texts != 2 || v.Kinds[proofread.KindReviewReply].Texts != 2 {
		t.Errorf("voice from JSON lines = %+v, want two issues and two replies", v.Kinds)
	}

	nul := filepath.Join(dir, "nul.json")
	if code, _, stderr := runVoiceArgs(t, "fix the build\n\nIt failed.\x00add a flag\x00\n", "voice", "build",
		"-kind", "commit-message", "-o", nul); code != exitOK || !strings.Contains(stderr, "under 30") {
		t.Fatalf("voice build from stdin: code %d stderr %q", code, stderr)
	}

	if v := readVoice(t, nul); v.Kinds[proofread.KindCommitMessage].Texts != 2 {
		t.Errorf("voice from stdin = %+v, want two commit messages", v.Kinds)
	}
}

func TestVoiceBuildRefusesADefaultPathInAWorkTree(t *testing.T) {
	config := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", config)

	if err := os.Mkdir(filepath.Join(config, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}

	code, _, stderr := runVoiceArgs(t, "It works.", "voice", "build", "-kind", "review-reply")
	if code != exitError || !strings.Contains(stderr, "is inside the git work tree") {
		t.Errorf("voice build in a work tree: code %d stderr %q, want a refusal", code, stderr)
	}

	named := filepath.Join(config, "named.json")
	if code, _, stderr := runVoiceArgs(t, "It works.", "voice", "build", "-kind", "review-reply", "-o", named); code != exitOK {
		t.Errorf("voice build with -o in a work tree: code %d stderr %q, want it written", code, stderr)
	}
}

func TestVoiceBuildWritesTheDefaultPathOutsideAWorkTree(t *testing.T) {
	config := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", config)

	if code, _, stderr := runVoiceArgs(t, "It works.", "voice", "build", "-kind", "review-reply"); code != exitOK {
		t.Fatalf("voice build: code %d stderr %q", code, stderr)
	}

	readVoice(t, filepath.Join(config, "proofread", "voice.json"))
}

func TestVoiceBuildExitsTwoOnInputItCannotUse(t *testing.T) {
	cases := []struct {
		name, stdin string
		args        []string
		want        string
	}{
		{"no subcommand", "", []string{"voice"}, "proofread: voice takes one subcommand, build\n"},
		{"no kind", "It works.", []string{"voice", "build"},
			"proofread: name the texts' kind with -kind, or with -kind-field for JSON lines\n"},
		{"a kind field alone", "It works.", []string{"voice", "build", "-kind-field", "type"},
			"proofread: -kind-field needs -field, since only JSON lines name a kind\n"},
		{"a kind with no voice", "It works.", []string{"voice", "build", "-kind", "guide"},
			"proofread: a voice measures change-description, review-reply, commit-message and issue, not guide\n"},
		{"no text", "\x00", []string{"voice", "build", "-kind", "issue"}, "proofread: no text has a prose word to measure\n"},
		{"a line with no text", `{"text":1}`, []string{"voice", "build", "-kind", "issue", "-field", "text"},
			"proofread: stdin:1: no string field \"text\"\n"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if code, _, stderr := runVoiceArgs(t, tc.stdin, tc.args...); code != exitError || stderr != tc.want {
				t.Errorf("run: code %d stderr %q, want code 2 stderr %q", code, stderr, tc.want)
			}
		})
	}
}

func TestRunJudgesWithAVoiceFile(t *testing.T) {
	dir := t.TempDir()

	v, err := proofread.BuildVoice(map[proofread.Kind][]string{proofread.KindReviewReply: synthReplies(40)})
	if err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(dir, "voice.json")
	if err := writeVoice(path, v); err != nil {
		t.Fatal(err)
	}

	drafted := "**Summary:** The change updates the cache layer so that every key is computed with the sorted " +
		"inputs, and the test suite now covers the path. **Details:** The handler reads all of the inputs, " +
		"sorts them, and hashes the result, which ensures that one key is produced for each distinct set."

	code, stdout, stderr := runVoiceArgs(t, drafted, "review-reply", "-voice", path, "-only", "voice-drift")
	if code != exitOK || !strings.Contains(stdout, `"rule":"voice-drift"`) || !strings.Contains(stdout, `"decision":"advise"`) {
		t.Errorf("review-reply -voice: code %d stdout %s stderr %q, want one advise voice-drift finding", code, stdout, stderr)
	}

	if _, stdout, _ := runVoiceArgs(t, drafted, "review-reply", "-only", "voice-drift"); stdout != "[]\n" {
		t.Errorf("review-reply with no voice = %s, want no finding", stdout)
	}

	bad := writeFile(t, dir, "bad.json", `{"schema":"other"}`)
	if code, _, stderr := runVoiceArgs(t, drafted, "review-reply", "-voice", bad); code != exitError ||
		stderr != "proofread: voice "+bad+": schema \"other\" is not proofread-voice/1\n" {
		t.Errorf("a bad voice file: code %d stderr %q", code, stderr)
	}
}
