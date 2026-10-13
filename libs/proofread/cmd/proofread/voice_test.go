package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/egladman/magus/libs/proofread"
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

	for i, text := range synthReplies(100) {
		args = append(args, writeFile(t, dir, fmt.Sprintf("r%d.md", i), text))
	}

	code, stdout, stderr := runVoiceArgs(t, "", args...)
	if code != exitOK || stdout != "" {
		t.Fatalf("voice build: code %d stdout %q stderr %q", code, stdout, stderr)
	}

	path := filepath.Join(dir, "out", "voice.json")
	if !strings.HasPrefix(stderr, "review-reply: 100 texts, ") || !strings.Contains(stderr, " windows, cut ") ||
		!strings.HasSuffix(stderr, "wrote "+path+"\n") {
		t.Errorf("stderr = %q", stderr)
	}

	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("voice file: %v, %v, want mode 0600", info, err)
	}

	if v := readVoice(t, path); v.Kinds[proofread.KindReviewReply].Windows == nil {
		t.Errorf("voice = %+v, want windows for review-reply", v)
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
		"-kind", "commit-message", "-o", nul); code != exitOK ||
		!strings.Contains(stderr, "commit-message: 2 texts, 8 words, under 1000, so voice-drift skips it\n") {
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
		{"no subcommand", "", []string{"voice"}, "proofread: voice takes a subcommand, build or eval\n"},
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

// jsonLines is a JSON-lines file in dir holding each text under "text" and,
// when kind is not empty, its kind under "kind".
func jsonLines(t *testing.T, dir, name, kind string, texts []string) string {
	t.Helper()

	var b strings.Builder

	for _, text := range texts {
		record := map[string]string{"text": text}
		if kind != "" {
			record["kind"] = kind
		}

		line, err := json.Marshal(record)
		if err != nil {
			t.Fatal(err)
		}

		b.Write(append(line, '\n'))
	}

	return writeFile(t, dir, name, b.String())
}

func evalFiles(t *testing.T) (owner, other string) {
	t.Helper()

	dir := t.TempDir()
	semis := make([]string, 5)
	commas := make([]string, 5)

	// The two sets differ only in the mark joining the clauses.
	for i := range semis {
		semis[i] = fmt.Sprintf("Case %d builds; the cache holds it.", i)
		commas[i] = fmt.Sprintf("Case %d builds, the cache holds it.", i)
	}

	return jsonLines(t, dir, "owner.jsonl", "review-reply", semis), jsonLines(t, dir, "other.jsonl", "", commas)
}

func TestVoiceEvalPrintsEachFeatureBySeparation(t *testing.T) {
	owner, other := evalFiles(t)

	code, stdout, stderr := runVoiceArgs(t, "", "voice", "eval", "-owner", owner, "-other", other, "-kind", "review-reply")
	if code != exitOK || stderr != "" {
		t.Fatalf("voice eval: code %d stderr %q", code, stderr)
	}

	lines := strings.Split(stdout, "\n")
	if lines[0] != "review-reply: 5 owner texts, 5 other texts" {
		t.Errorf("header = %q", lines[0])
	}

	rows := map[string][]string{}

	for _, line := range lines[1:] {
		if fields := strings.Fields(line); len(fields) > 0 {
			rows[fields[0]] = fields[1:]
		}
	}

	want := map[string][]string{
		"punctuation.comma":     {"0.000", "5", "5", "owner", "lower"},
		"punctuation.semicolon": {"1.000", "5", "5", "owner", "higher"},
		"layout.bold":           {"0.500", "5", "5", "no", "difference"},
	}
	for feature, w := range want {
		if !slices.Equal(rows[feature], w) {
			t.Errorf("%s = %q, want %q", feature, rows[feature], w)
		}
	}
}

func TestVoiceEvalWritesJSONPerKind(t *testing.T) {
	owner, other := evalFiles(t)

	code, stdout, stderr := runVoiceArgs(t, "", "voice", "eval", "-owner", owner, "-other", other, "-kind", "review-reply",
		"-format", "json")
	if code != exitOK || stderr != "" {
		t.Fatalf("voice eval: code %d stderr %q", code, stderr)
	}

	var got map[proofread.Kind][]proofread.Separation
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("decode %q: %v", stdout, err)
	}

	semicolon := proofread.Separation{Feature: "punctuation.semicolon", AUC: 1, Owner: 5, Other: 5}
	if len(got) != 1 || !slices.Contains(got[proofread.KindReviewReply], semicolon) {
		t.Errorf("separations = %+v, want review-reply alone, holding %+v", got, semicolon)
	}
}

func TestVoiceEvalPoolsTextsIntoChunks(t *testing.T) {
	owner, other := evalFiles(t)

	// Seven words a text: chunks of 14 or more take two, so 5 texts make 2 chunks.
	code, stdout, stderr := runVoiceArgs(t, "", "voice", "eval", "-owner", owner, "-other", other, "-kind", "review-reply",
		"-pool", "14", "-seed", "5")
	if code != exitOK || stderr != "" {
		t.Fatalf("voice eval: code %d stderr %q", code, stderr)
	}

	if first, _, _ := strings.Cut(stdout, "\n"); first != "review-reply: 2 owner chunks, 2 other chunks, chunks of 14+ words" {
		t.Errorf("header = %q", first)
	}

	_, again, _ := runVoiceArgs(t, "", "voice", "eval", "-owner", owner, "-other", other, "-kind", "review-reply",
		"-pool", "14", "-seed", "5")
	if again != stdout {
		t.Errorf("one seed wrote\n%s\nthen\n%s", stdout, again)
	}
}

func TestVoiceEvalExitsTwoOnInputItCannotUse(t *testing.T) {
	owner, other := evalFiles(t)

	cases := []struct {
		name string
		args []string
		want string
	}{
		{"no other file", []string{"-owner", owner}, "proofread: voice eval needs -owner and -other\n"},
		{"an operand", []string{"-owner", owner, "-other", other, "x.md"}, "proofread: voice eval reads -owner and -other, not x.md\n"},
		{"an unknown format", []string{"-owner", owner, "-other", other, "-format", "sarif"},
			"proofread: unknown format \"sarif\": write text or json\n"},
		{"no kind for a line", []string{"-owner", owner, "-other", other},
			fmt.Sprintf("proofread: %s:1: no string field \"kind\" names a kind, and -kind gives none\n", other)},
		{"a negative pool", []string{"-owner", owner, "-other", other, "-pool", "-1"},
			"proofread: -pool is -1: name a word count, or 0 to pool nothing\n"},
		{"an unknown kind", []string{"-owner", owner, "-other", other, "-kind", "poem"},
			fmt.Sprintf("proofread: %s: unknown kind \"poem\"\n", other)},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, stdout, stderr := runVoiceArgs(t, "", append([]string{"voice", "eval"}, tc.args...)...)
			if code != exitError || stdout != "" || stderr != tc.want {
				t.Errorf("run: code %d stdout %q stderr %q, want code 2 stderr %q", code, stdout, stderr, tc.want)
			}
		})
	}
}

func TestRunJudgesWithAVoiceFile(t *testing.T) {
	dir := t.TempDir()

	v, err := proofread.BuildVoice(map[proofread.Kind][]string{proofread.KindReviewReply: synthReplies(120)})
	if err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(dir, "voice.json")
	if err := writeVoice(path, v); err != nil {
		t.Fatal(err)
	}

	drafted := strings.Repeat("The change updates the cache layer (the part that keys every target) so that each key is "+
		"computed from the sorted inputs, and the test suite covers the path: the handler reads all of the inputs, "+
		"sorts them, and hashes the result; the ordering no longer affects the outcome, and the docs say so. ", 4)

	code, stdout, stderr := runVoiceArgs(t, drafted, "review-reply", "-voice", path, "-only", "voice-drift")
	if code != exitOK || !strings.Contains(stdout, `"rule":"voice-drift"`) || !strings.Contains(stdout, `"decision":"advise"`) {
		t.Errorf("review-reply -voice: code %d stdout %s stderr %q, want one advise voice-drift finding", code, stdout, stderr)
	}

	if _, stdout, _ := runVoiceArgs(t, drafted, "review-reply", "-only", "voice-drift"); stdout != "[]\n" {
		t.Errorf("review-reply with no voice = %s, want no finding", stdout)
	}

	bad := writeFile(t, dir, "bad.json", `{"schema":"other"}`)
	if code, _, stderr := runVoiceArgs(t, drafted, "review-reply", "-voice", bad); code != exitError ||
		stderr != "proofread: voice "+bad+": schema \"other\" is not proofread-voice/2\n" {
		t.Errorf("a bad voice file: code %d stderr %q", code, stderr)
	}

	old := writeFile(t, dir, "old.json", `{"schema":"proofread-voice/1","kinds":{"issue":{"ranges":{}}}}`)
	if code, _, stderr := runVoiceArgs(t, drafted, "review-reply", "-voice", old); code != exitError ||
		!strings.Contains(stderr, "rerun `proofread voice build`") {
		t.Errorf("a voice file from an earlier proofread: code %d stderr %q, want the command that rebuilds it", code, stderr)
	}
}
