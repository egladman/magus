package main

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"maps"
	"math"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/egladman/magus/hack/embed/distill"
	"github.com/egladman/magus/hack/embed/teacher"
	"github.com/egladman/magus/libs/xdg"
)

const usage = `usage: embed <command> [flags]

commands:
  fetch           download the pinned inputs named in inputs.jsonl and verify their sha256
  texts           write the training texts and forced tokens from embed-mine's source manifest
  vocab           build the vocabulary and write its token texts for a teacher run
  teacher         embed a texts file with a teacher model into resumable, content-addressed shards
  teacher-parity  check the Go teacher against the reference vectors and report texts/s
  record          append a teacher run's totals to runs.jsonl
  train           distill, train and quantize the table from finished teacher runs
  bench           write CodeRankEmbed's runs over a subset the external benchmark exported

run "embed <command> -h" for a command's flags`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "fetch":
		err = runFetch(os.Args[2:])
	case "texts":
		err = runTexts(os.Args[2:])
	case "vocab":
		err = runVocab(os.Args[2:])
	case "teacher":
		err = runTeacher(os.Args[2:])
	case "teacher-parity":
		err = runParity(os.Args[2:])
	case "record":
		err = runRecord(os.Args[2:])
	case "train":
		err = runTrain(os.Args[2:])
	case "bench":
		err = runBench(os.Args[2:])
	default:
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "embed %s: %v\n", os.Args[1], err)
		os.Exit(1)
	}
}

// textsSalt orders the training texts by hash, so each teacher shard samples the whole
// mix and the first shard's rate projects the run.
const textsSalt = "embed-texts/v1:"

// sourceRecord is one line of hack/embed/gen/sources.jsonl, as far as texts reads it.
type sourceRecord struct {
	Kind      string   `json:"kind"`
	Path      string   `json:"path"`
	Group     string   `json:"group"`
	Split     string   `json:"split"`
	Generated bool     `json:"generated"`
	SHA256    string   `json:"sha256"`
	Name      string   `json:"name"`
	Tokens    []string `json:"tokens"`
	ID        string   `json:"id"`
	Fold      int      `json:"fold"`
	Title     string   `json:"title"`
	Desc      string   `json:"desc"`
}

// excludedReason names why a train file's text stays out of the training texts, or is
// "" for a file it may enter. A generated output restates its source, which may be held
// out, and changelog entries restate commit subjects, which are eval queries.
func excludedReason(r sourceRecord) string {
	switch {
	case r.Generated:
		return "generated"
	case strings.HasPrefix(r.Path, "changes/unreleased/") && strings.HasSuffix(r.Path, ".md"):
		return "changelog-fragment"
	case path.Base(r.Path) == "CHANGELOG.md":
		return "changelog"
	}
	return ""
}

// runTexts turns the source manifest into the texts file vocab, teacher and train read.
// Every train-split file excludedReason lets in is cut into line-bounded chunks, each
// figure outside eval fold 0 becomes its title and description, and CoRNStack rows add
// a query and the head of its document, one row per in-repo text at -cornstack 1. Files
// are read at the commit that last wrote the manifest, and each must hash to its pinned
// digest, so the texts do not move when the tree does.
func runTexts(args []string) error {
	fs := flag.NewFlagSet("texts", flag.ExitOnError)
	sourcesPath := fs.String("sources", "gen/sources.jsonl", "embed-mine's source manifest")
	root := fs.String("root", "../..", "the repository the manifest's paths are relative to")
	rev := fs.String("rev", "", "the commit to read files at; default: the last commit that changed -sources")
	cache := fs.String("cache", cacheRoot(), "the cache root the manifest's input paths are relative to")
	out := fs.String("out", "", "the directory to write texts.jsonl and forced.txt to; default <cache>/training")
	maxBytes := fs.Int("chunk-bytes", 512, "a text holds at most this many bytes, so the teacher's 256-token cap rarely cuts it")
	ratio := fs.Float64("cornstack", 1, "CoRNStack rows per in-repo text; each row adds two texts")
	skip := fs.String("skip", "", "comma-separated input names left out; an input with no reader must be named here")
	_ = fs.Parse(args)
	if *out == "" {
		*out = filepath.Join(*cache, "training")
	}
	skipped := map[string]bool{}
	for name := range strings.SplitSeq(*skip, ",") {
		if name != "" {
			skipped[name] = true
		}
	}

	records, err := readSources(*sourcesPath)
	if err != nil {
		return err
	}
	if err := checkGroups(records); err != nil {
		return fmt.Errorf("%s: %w", *sourcesPath, err)
	}
	if *rev == "" {
		if *rev, err = lastCommit(*root, *sourcesPath); err != nil {
			return err
		}
	}
	var paths []string
	for _, r := range records {
		if r.Kind == "file" && r.Split == "train" && excludedReason(r) == "" {
			paths = append(paths, r.Path)
		}
	}
	files, err := readAtRev(*root, *rev, paths)
	if err != nil {
		return err
	}

	mix := textMix{forced: map[string]bool{}, excluded: map[string]int{}}
	if err := mix.addRepo(records, files, *rev, *sourcesPath, *maxBytes); err != nil {
		return err
	}
	repo := len(mix.texts)

	if err := mix.addCornstack(*cache, skipped, *ratio, *maxBytes); err != nil {
		return err
	}
	sortByHash(mix.texts)
	digest, err := mix.write(*out)
	if err != nil {
		return err
	}
	fmt.Printf("files at %s; %d texts: %d chunks of %d files, %d figures, %d CoRNStack documents and %d queries; texts %s; %d forced tokens\n",
		*rev, len(mix.texts), repo-mix.figures, mix.files, mix.figures, mix.docs, mix.queries, digest, len(mix.forced))
	fmt.Printf("train files left out of the texts: %v\n", mix.excluded)
	return nil
}

// textMix is the training texts as texts assembles them, the forced tokens, and the
// counts it reports. excluded counts the train files left out, by excludedReason.
type textMix struct {
	texts          []teacher.Text
	forced         map[string]bool
	inputs         []sourceRecord
	excluded       map[string]int
	files, figures int
	docs, queries  int
}

// addRepo adds the in-repo texts the manifest records: each train-split file that
// excludedReason lets in, which must hash to its pinned digest at rev, cut into chunks
// of at most maxBytes, and each figure outside eval fold 0. It collects the forced
// tokens and the input records for addCornstack.
func (m *textMix) addRepo(records []sourceRecord, files map[string][]byte, rev, sourcesPath string, maxBytes int) error {
	for _, r := range records {
		switch r.Kind {
		case "file":
			if r.Split != "train" {
				continue
			}
			if reason := excludedReason(r); reason != "" {
				m.excluded[reason]++
				continue
			}
			if sha256Hex(files[r.Path]) != r.SHA256 {
				return fmt.Errorf("%s at %s does not hash to the manifest's %s: regenerate %s", r.Path, rev, r.SHA256, sourcesPath)
			}
			m.files++
			for _, c := range chunks(string(files[r.Path]), maxBytes) {
				m.texts = append(m.texts, teacher.Text{ID: r.Path + ":" + c.span(), Text: c.text})
			}
		case "figure":
			if r.Fold != 0 {
				m.figures++
				m.texts = append(m.texts, teacher.Text{ID: "figure:" + r.ID, Text: r.Title + "\n" + r.Desc})
			}
		case "forced":
			for _, t := range r.Tokens {
				m.forced[t] = true
			}
		case "input":
			m.inputs = append(m.inputs, r)
		default:
			return fmt.Errorf("%s: record kind %q", sourcesPath, r.Kind)
		}
	}
	return nil
}

// addCornstack adds ratio CoRNStack rows per text added so far, each a query and the
// head of its document, read from the inputs in order until the rows are filled.
func (m *textMix) addCornstack(cache string, skipped map[string]bool, ratio float64, maxBytes int) error {
	rows := int(math.Round(ratio * float64(len(m.texts))))
	for _, in := range m.inputs {
		switch {
		case skipped[in.Name]:
			fmt.Printf("input %s: left out by -skip\n", in.Name)
			continue
		case !strings.HasSuffix(in.Name, ".jsonl.gz"):
			return fmt.Errorf("input %s: no reader for its format, so name it in -skip to leave it out", in.Name)
		case rows == 0:
			fmt.Printf("input %s: not read, the earlier inputs filled -cornstack\n", in.Name)
			continue
		}
		path, err := verify(cache, inputRecord{Name: in.Name, Path: in.Path, SHA256: in.SHA256})
		if err != nil {
			return err
		}
		got, err := readCornstack(path, in.Name, rows, maxBytes)
		if err != nil {
			return err
		}
		for _, t := range got {
			if strings.HasSuffix(t.ID, ":query") {
				m.queries++
			} else {
				m.docs++
			}
		}
		m.texts = append(m.texts, got...)
		rows -= len(got) / 2
	}
	if rows > 0 {
		return fmt.Errorf("the inputs hold %d CoRNStack rows too few for -cornstack %g", rows, ratio)
	}
	return nil
}

// sortByHash orders texts by IDHash under textsSalt, then by id.
func sortByHash(texts []teacher.Text) {
	slices.SortFunc(texts, func(a, b teacher.Text) int {
		ha, hb := distill.IDHash(textsSalt, a.ID), distill.IDHash(textsSalt, b.ID)
		if ha != hb {
			if ha < hb {
				return -1
			}
			return 1
		}
		return strings.Compare(a.ID, b.ID)
	})
}

// write writes texts.jsonl and the sorted forced.txt under out and returns the texts'
// sha256.
func (m *textMix) write(out string) (string, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	for _, t := range m.texts {
		if err := enc.Encode(t); err != nil {
			return "", err
		}
	}
	tokens := slices.Sorted(maps.Keys(m.forced))
	if err := os.MkdirAll(out, 0o755); err != nil {
		return "", err
	}
	if err := writeAtomic(filepath.Join(out, "texts.jsonl"), buf.Bytes()); err != nil {
		return "", err
	}
	if err := writeAtomic(filepath.Join(out, "forced.txt"), []byte(strings.Join(tokens, "\n")+"\n")); err != nil {
		return "", err
	}
	return sha256Hex(buf.Bytes()), nil
}

func readSources(path string) ([]sourceRecord, error) {
	var out []sourceRecord
	var readErr error
	fail := func(_ sourceRecord, err error) bool {
		readErr = err
		return false
	}
	for n, line := range jsonLines(path, fail) {
		var r sourceRecord
		if err := json.Unmarshal(line, &r); err != nil {
			return nil, fmt.Errorf("%s:%d: %w", path, n, err)
		}
		out = append(out, r)
	}
	if readErr != nil {
		return nil, readErr
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%s holds no records", path)
	}
	return out, nil
}

// checkGroups refuses a manifest whose file records leave a provenance group unnamed or
// put one group on both sides of the split: either would let a holdout file's text, or
// its generated copy, reach training.
func checkGroups(records []sourceRecord) error {
	sides := map[string]string{}
	for _, r := range records {
		if r.Kind != "file" {
			continue
		}
		if r.Group == "" {
			return fmt.Errorf("file %s names no provenance group", r.Path)
		}
		if r.Split != "train" && r.Split != "holdout" {
			return fmt.Errorf("file %s has split %q", r.Path, r.Split)
		}
		if side, ok := sides[r.Group]; ok && side != r.Split {
			return fmt.Errorf("group %s spans both splits: %s is %s", r.Group, r.Path, r.Split)
		}
		sides[r.Group] = r.Split
	}
	return nil
}

func lastCommit(root, path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	b, err := exec.Command("git", "-C", root, "log", "-1", "--format=%H", "--", abs).Output()
	if err != nil {
		return "", fmt.Errorf("git log %s: %w", path, err)
	}
	rev := strings.TrimSpace(string(b))
	if rev == "" {
		return "", fmt.Errorf("no commit has written %s, so commit it or pass -rev", path)
	}
	return rev, nil
}

// readAtRev reads each path's blob at rev through one git cat-file --batch.
func readAtRev(root, rev string, paths []string) (map[string][]byte, error) {
	cmd := exec.Command("git", "-C", root, "cat-file", "--batch")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	go func() {
		w := bufio.NewWriter(stdin)
		for _, p := range paths {
			fmt.Fprintf(w, "%s:%s\n", rev, p)
		}
		_ = w.Flush()
		_ = stdin.Close()
	}()
	out, err := readBatch(bufio.NewReader(stdout), rev, paths)
	if err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return nil, err
	}
	return out, cmd.Wait()
}

func readBatch(r *bufio.Reader, rev string, paths []string) (map[string][]byte, error) {
	out := make(map[string][]byte, len(paths))
	for _, p := range paths {
		header, err := r.ReadString('\n')
		if err != nil {
			return nil, fmt.Errorf("git cat-file %s:%s: %w", rev, p, err)
		}
		f := strings.Fields(header)
		if len(f) != 3 || f[1] != "blob" {
			return nil, fmt.Errorf("git cat-file %s:%s: %s", rev, p, strings.TrimSpace(header))
		}
		size, err := strconv.Atoi(f[2])
		if err != nil {
			return nil, err
		}
		b := make([]byte, size+1)
		if _, err := io.ReadFull(r, b); err != nil {
			return nil, err
		}
		out[p] = b[:size]
	}
	return out, nil
}

// chunk is a piece of a file: lines first..last, 1-based and inclusive. A line longer
// than a chunk is cut into parts numbered from 1.
type chunk struct {
	first, last, part int
	text              string
}

func (c chunk) span() string {
	s := strconv.Itoa(c.first) + "-" + strconv.Itoa(c.last)
	if c.part > 0 {
		s += "#" + strconv.Itoa(c.part)
	}
	return s
}

// chunks cuts text into pieces of at most maxBytes, whole lines where they fit and rune
// boundaries where one line does not. Whitespace-only pieces are dropped.
func chunks(text string, maxBytes int) []chunk {
	var out []chunk
	var buf strings.Builder
	first, last := 0, 0
	emit := func(c chunk) {
		if strings.TrimSpace(c.text) != "" {
			out = append(out, c)
		}
	}
	flush := func() {
		if buf.Len() > 0 {
			emit(chunk{first: first, last: last, text: buf.String()})
			buf.Reset()
		}
	}
	n := 0
	for line := range strings.Lines(text) {
		n++
		if buf.Len()+len(line) > maxBytes {
			flush()
		}
		if len(line) > maxBytes {
			for part := 1; line != ""; part++ {
				cut := runeCut(line, maxBytes)
				emit(chunk{first: n, last: n, part: part, text: line[:cut]})
				line = line[cut:]
			}
			continue
		}
		if buf.Len() == 0 {
			first = n
		}
		buf.WriteString(line)
		last = n
	}
	flush()
	return out
}

// runeCut is the longest prefix of s within maxBytes that ends on a rune boundary.
func runeCut(s string, maxBytes int) int {
	if len(s) <= maxBytes {
		return len(s)
	}
	for i := maxBytes; i > 0; i-- {
		if utf8.RuneStart(s[i]) {
			return i
		}
	}
	return maxBytes
}

// readCornstack reads up to rows rows of a CoRNStack shard in file order, each as its
// query and the first chunk of its document.
func readCornstack(path, name string, rows, maxBytes int) ([]teacher.Text, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	gz, err := gzip.NewReader(bufio.NewReaderSize(f, 1<<20))
	if err != nil {
		return nil, err
	}
	dec := json.NewDecoder(gz)
	var out []teacher.Text
	for row := 0; len(out) < 2*rows; row++ {
		var r struct {
			Query    string `json:"query"`
			Document string `json:"document"`
		}
		if err := dec.Decode(&r); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return nil, fmt.Errorf("%s row %d: %w", name, row, err)
		}
		doc := chunks(r.Document, maxBytes)
		if strings.TrimSpace(r.Query) == "" || len(doc) == 0 {
			continue
		}
		id := name + ":" + strconv.Itoa(row)
		out = append(out,
			teacher.Text{ID: id + ":query", Text: r.Query[:runeCut(r.Query, maxBytes)]},
			teacher.Text{ID: id + ":document", Text: doc[0].text})
	}
	return out, nil
}

// runLine is one teacher line of runs.jsonl. Seconds sums the encode time of the shards
// the manifest holds, across every resume; model load and skipped shards are not in it.
type runLine struct {
	Kind           string  `json:"kind"`
	Run            string  `json:"run"`
	Teacher        string  `json:"teacher"`
	Revision       string  `json:"revision"`
	WeightsSHA256  string  `json:"weights_sha256"`
	TextsSHA256    string  `json:"texts_sha256"`
	TextsFile      string  `json:"texts_file"`
	MaxLen         int     `json:"max_len"`
	ShardSize      int     `json:"shard_size"`
	Shards         int     `json:"shards"`
	Texts          int     `json:"texts"`
	Rows           int     `json:"rows"`
	Complete       bool    `json:"complete"`
	Tokens         int     `json:"tokens"`
	Seconds        float64 `json:"seconds"`
	Manifest       string  `json:"manifest"`
	ManifestSHA256 string  `json:"manifest_sha256"`
}

// runRecord appends the state of the teacher run that embedded -texts at -max-len to
// -out. A manifest already recorded byte for byte is not recorded again.
func runRecord(args []string) error {
	fs := flag.NewFlagSet("record", flag.ExitOnError)
	cache := fs.String("cache", cacheRoot(), "the cache root teacher runs live under")
	model := fs.String("model", "", "the teacher the run used")
	texts := fs.String("texts", "", "the texts file the run embedded")
	maxLen := fs.Int("max-len", 0, "the run's token cap")
	out := fs.String("out", "runs.jsonl", "the runs log to append to")
	_ = fs.Parse(args)
	if *model == "" || *texts == "" || *maxLen == 0 {
		return errors.New("-model, -texts and -max-len are required")
	}
	digest, err := teacher.FileSHA256(*texts)
	if err != nil {
		return err
	}
	n, err := countTexts(*texts)
	if err != nil {
		return err
	}
	manifests, _ := filepath.Glob(filepath.Join(*cache, "teacher", *model, "*", "manifest.jsonl"))
	var found []string
	var line runLine
	for _, m := range manifests {
		l, err := summarize(m)
		if err != nil {
			return err
		}
		if l.TextsSHA256 == digest && l.MaxLen == *maxLen {
			found, line = append(found, m), l
		}
	}
	if len(found) != 1 {
		return fmt.Errorf("%d %s runs under %s embedded %s at max-len %d, want 1: %v", len(found), *model, *cache, *texts, *maxLen, found)
	}
	line.Texts = n
	line.Complete = line.Rows == n
	line.TextsFile = relativize(*cache, *texts)
	line.Manifest = relativize(*cache, found[0])

	prior, err := os.ReadFile(*out)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	for l := range bytes.Lines(prior) {
		var r runLine
		if json.Unmarshal(l, &r) == nil && r.ManifestSHA256 == line.ManifestSHA256 {
			fmt.Printf("%s: already recorded\n", line.Manifest)
			return nil
		}
	}
	b, err := json.Marshal(line)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(*out, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(b, '\n')); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	rate := 0.0
	if line.Seconds > 0 {
		rate = float64(line.Tokens) / line.Seconds
	}
	fmt.Printf("%s: %d of %d texts in %d shards, %d tokens, %.0fs encoding (%.0f tokens/s); manifest sha256 %s\n",
		line.Manifest, line.Rows, line.Texts, line.Shards, line.Tokens, line.Seconds, rate, line.ManifestSHA256)
	return nil
}

// summarize totals a manifest. A shard encoded twice counts once, by its later record,
// as readTeacherRun reads it.
func summarize(manifest string) (runLine, error) {
	b, err := os.ReadFile(manifest)
	if err != nil {
		return runLine{}, err
	}
	var spec *teacher.RunSpec
	shards := map[int]teacher.Record{}
	for l := range bytes.Lines(b) {
		var r teacher.Record
		if err := json.Unmarshal(l, &r); err != nil {
			return runLine{}, fmt.Errorf("%s: %w", manifest, err)
		}
		switch {
		case spec == nil && r.Kind == "run" && r.Spec != nil:
			spec = r.Spec
		case spec != nil && r.Kind == "shard":
			shards[r.Shard] = r
		default:
			return runLine{}, fmt.Errorf("%s: unexpected %q record", manifest, r.Kind)
		}
	}
	if spec == nil {
		return runLine{}, fmt.Errorf("%s holds no run record", manifest)
	}
	line := runLine{
		Kind: "teacher", Run: spec.Key(), Teacher: spec.Model, Revision: spec.Revision,
		WeightsSHA256: spec.WeightsSHA256, TextsSHA256: spec.TextsSHA256,
		MaxLen: spec.MaxLen, ShardSize: spec.ShardSize, Shards: len(shards), ManifestSHA256: sha256Hex(b),
	}
	for _, r := range shards {
		line.Rows += r.Rows
		line.Tokens += r.Tokens
		line.Seconds += r.Seconds
	}
	line.Seconds = math.Round(line.Seconds*10) / 10
	return line, nil
}

func countTexts(path string) (int, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	n := 0
	for l := range bytes.Lines(b) {
		if len(bytes.TrimSpace(l)) > 0 {
			n++
		}
	}
	return n, nil
}

// relativize keeps machine paths out of the committed runs log: a path under the cache
// root is recorded relative to it.
func relativize(root, path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return path
	}
	if rel, err := filepath.Rel(root, abs); err == nil && !strings.HasPrefix(rel, "..") {
		return filepath.ToSlash(rel)
	}
	return path
}

func cacheRoot() string {
	dir, err := xdg.CacheDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "magus-embed")
}

func writeAtomic(path string, b []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
