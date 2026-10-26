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

	"github.com/egladman/magus/libs/embed"
	"github.com/egladman/magus/libs/embed/train/distill"
	"github.com/egladman/magus/libs/embed/train/teacher"
	"github.com/egladman/magus/libs/xdg"
)

const usage = `usage: embed <command> [flags]

commands:
  fetch           download the pinned inputs named in inputs.jsonl and verify their sha256
  texts           write the training texts and forced tokens from embed-mine's source manifest
  vocab           build the vocabulary; write its token texts for a teacher run and its counts for convert
  teacher         embed a texts file with a teacher model into resumable, content-addressed shards
  teacher-parity  check the Go teacher against the reference vectors and report texts/s
  train           distill, train and quantize the table from finished teacher runs
  convert         write potion-code-16M-v2 as a table over the vocabulary, mean of each token's word pieces
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
	case "train":
		err = runTrain(os.Args[2:])
	case "convert":
		err = runConvert(os.Args[2:])
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

// sourceRecord is one line of libs/embed/train/gen/sources.jsonl, as far as texts reads it.
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
	root := fs.String("root", "../../..", "the repository the manifest's paths are relative to")
	rev := fs.String("rev", "", "the commit to read files at; default: the last commit that changed -sources")
	cache := fs.String("cache", cacheRoot(), "the cache root the manifest's input paths are relative to")
	out := fs.String("out", "", "the directory to write texts.jsonl and forced.txt to; default <cache>/training")
	maxBytes := fs.Int("chunk-bytes", 512, "a text holds at most this many bytes, so the teacher's 256-token cap rarely cuts it")
	ratio := fs.Float64("cornstack", 1, "CoRNStack rows per in-repo text; each row adds two texts")
	skip := fs.String("skip", "", "comma-separated input names left out; an input with no reader must be named here")
	evalDir := fs.String("eval", "", "the directory of external eval sets, one <name>/ per set whose queries.jsonl and corpus.jsonl hold {\"id\", \"text\"} lines, the layout embed bench reads")
	noEval := fs.String("no-eval", "", "comma-separated eval sets left out of decontamination, each recorded in runs.jsonl")
	common := fs.Int("common", 10, "a 13-word window more eval texts than this hold is boilerplate and drops nothing")
	_ = fs.Parse(args)
	if *out == "" {
		*out = filepath.Join(*cache, "training")
	}
	skipped := commaSet(*skip)
	leftOut := commaSet(*noEval)

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
		if r.Kind == "file" && (r.Split == "holdout" || excludedReason(r) == "") {
			paths = append(paths, r.Path)
		}
	}
	files, err := readAtRev(*root, *rev, paths)
	if err != nil {
		return err
	}

	evals, err := evalTexts(records, files, *root, *rev, *evalDir, leftOut)
	if err != nil {
		return err
	}
	d, err := newDecontaminator(evals, *common)
	if err != nil {
		return err
	}
	d.report.LeftOut = slices.Sorted(maps.Keys(leftOut))

	mix := textMix{forced: map[string]bool{}, excluded: map[string]int{}, decontam: d}
	if err := mix.addRepository(records, files, *rev, *sourcesPath, *maxBytes); err != nil {
		return err
	}
	repo := len(mix.texts)

	if err := mix.addCornstack(*cache, skipped, *ratio, *maxBytes); err != nil {
		return err
	}
	return writeTextsArtifacts(&mix, records, files, repo, *rev, *cache, *out)
}

// writeTextsArtifacts persists a completed text mix after every input text has passed
// decontamination, and writes the run's runs.jsonl line to texts-run.json beside it for
// the vocab target to append.
func writeTextsArtifacts(mix *textMix, records []sourceRecord, files map[string][]byte, repo int, rev, cache, out string) error {
	d := mix.decontam
	sortByHash(mix.texts)
	digest, err := mix.write(out)
	if err != nil {
		return err
	}
	if err := writeDropped(filepath.Join(out, "dropped.jsonl"), d.dropped); err != nil {
		return err
	}
	var holdout [][]byte
	for _, r := range records {
		if r.Kind == "file" && r.Split == "holdout" {
			holdout = append(holdout, files[r.Path])
		}
	}
	forced, err := countForced(mix.forced, mix.texts, holdout)
	if err != nil {
		return err
	}
	line := textsLine{
		Kind: "texts", Rev: rev, TextsSHA256: digest, TextsFile: relativize(cache, filepath.Join(out, "texts.jsonl")),
		Texts: len(mix.texts), Excluded: mix.excluded, Decontam: d.report, Forced: forced,
	}
	b, err := json.Marshal(line)
	if err != nil {
		return err
	}
	if err := writeAtomic(filepath.Join(out, "texts-run.json"), append(b, '\n')); err != nil {
		return err
	}
	fmt.Printf("files at %s; %d texts: %d chunks of %d files, %d figures, %d CoRNStack documents and %d queries; texts %s; %d forced tokens\n",
		rev, len(mix.texts), repo-mix.figures, mix.files, mix.figures, mix.docs, mix.queries, digest, len(mix.forced))
	fmt.Printf("train files left out of the texts: %v; dropped as eval repeats: %v, by kind/reason/set %v; %d of %d eval grams are boilerplate\n",
		mix.excluded, d.report.Dropped, d.report.DroppedBy, d.report.CommonGrams, d.report.Grams)
	fmt.Printf("forced tokens: %d, %d in the training texts, %d only in holdout files, %d in neither\n",
		forced.Tokens, forced.InTrainingTexts, forced.HoldoutOnly, forced.Unseen)
	return nil
}

// textsLine is the runs.jsonl line a texts run writes to texts-run.json.
type textsLine struct {
	Kind        string         `json:"kind"`
	Rev         string         `json:"rev"`
	TextsSHA256 string         `json:"texts_sha256"`
	TextsFile   string         `json:"texts_file"`
	Texts       int            `json:"texts"`
	Excluded    map[string]int `json:"excluded"`
	Decontam    decontamReport `json:"decontam"`
	Forced      forcedReport   `json:"forced"`
}

// forcedReport places every forced token. The whole product vocabulary is forced, holdout
// files' names included, so HoldoutOnly counts the forced tokens an eval query can match
// only because they were forced: no training text holds them, a holdout file does.
type forcedReport struct {
	Tokens          int `json:"tokens"`
	InTrainingTexts int `json:"in_training_texts"`
	HoldoutOnly     int `json:"holdout_only"`
	Unseen          int `json:"unseen"`
}

// countForced tokenizes the training texts and the holdout files as the runtime does and
// places each token distill.ForcedTokens makes of forced.
func countForced(forced map[string]bool, texts []teacher.Text, holdout [][]byte) (forcedReport, error) {
	tokens, err := distill.ForcedTokens(slices.Sorted(maps.Keys(forced)))
	if err != nil {
		return forcedReport{}, err
	}
	trained, held := map[string]bool{}, map[string]bool{}
	for _, t := range texts {
		markTokens(trained, tokens, t.Text)
	}
	for _, b := range holdout {
		markTokens(held, tokens, string(b))
	}
	r := forcedReport{Tokens: len(tokens)}
	for tok := range tokens {
		switch {
		case trained[tok]:
			r.InTrainingTexts++
		case held[tok]:
			r.HoldoutOnly++
		default:
			r.Unseen++
		}
	}
	return r, nil
}

// markTokens sets found[tok] for every token of body that wanted holds.
func markTokens(found, wanted map[string]bool, body string) {
	for _, tok := range embed.Tokens(body) {
		if wanted[tok] {
			found[tok] = true
		}
	}
}

func commaSet(s string) map[string]bool {
	out := map[string]bool{}
	for name := range strings.SplitSeq(s, ",") {
		if name != "" {
			out[name] = true
		}
	}
	return out
}

// evalTexts gathers every text the training texts must not repeat: the external sets
// under dir, each holdout file at rev, the eval fold's figure titles and descriptions,
// every commit subject up to rev, and the judged and harvested queries under
// testdata/retrieval. A set leftOut names is skipped; any other that is missing is an
// error.
func evalTexts(records []sourceRecord, files map[string][]byte, root, rev, dir string, leftOut map[string]bool) (*evalCorpus, error) {
	e := &evalCorpus{}
	if err := readEvalSets(e, dir, leftOut); err != nil {
		return nil, err
	}
	for _, r := range records {
		switch {
		case r.Kind == "file" && r.Split == "holdout" && !leftOut["repo-holdout"]:
			if sha256Hex(files[r.Path]) != r.SHA256 {
				return nil, fmt.Errorf("%s at %s does not hash to the manifest's %s", r.Path, rev, r.SHA256)
			}
			e.add("repo-holdout", string(files[r.Path]))
		case r.Kind == "figure" && r.Fold == 0 && !leftOut["figure-queries"]:
			e.add("figure-queries", r.Title, r.Desc)
		}
	}
	if !leftOut["commit-subjects"] {
		b, err := exec.Command("git", "-C", root, "log", "--format=%s", rev).Output()
		if err != nil {
			return nil, fmt.Errorf("git log %s: %w", rev, err)
		}
		e.add("commit-subjects", strings.Split(strings.TrimSpace(string(b)), "\n")...)
	}
	for _, q := range [][2]string{{"judged-queries", "queries.jsonl"}, {"harvested-queries", "judgments.jsonl"}} {
		if leftOut[q[0]] {
			continue
		}
		queries, err := readQueries(filepath.Join(root, "testdata", "retrieval", q[1]))
		if err != nil {
			return nil, fmt.Errorf("eval set %s (name it in -no-eval to leave it out): %w", q[0], err)
		}
		e.add(q[0], queries...)
	}
	return e, nil
}

// readQueries reads the query field of every line of a rankeval queries or judgments file.
func readQueries(path string) ([]string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var out []string
	n := 0
	for line := range bytes.Lines(b) {
		n++
		var r struct {
			Query string `json:"query"`
		}
		if err := json.Unmarshal(line, &r); err != nil {
			return nil, fmt.Errorf("%s:%d: %w", path, n, err)
		}
		out = append(out, r.Query)
	}
	return out, nil
}

func writeDropped(path string, dropped []droppedText) error {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	for _, t := range dropped {
		if err := enc.Encode(t); err != nil {
			return err
		}
	}
	return writeAtomic(path, buf.Bytes())
}

// textMix is the training texts as texts assembles them, the forced tokens, and the
// counts it reports. excluded counts the train files left out, by excludedReason.
type textMix struct {
	texts          []teacher.Text
	forced         map[string]bool
	inputs         []sourceRecord
	excluded       map[string]int
	decontam       *decontaminator
	files, figures int
	docs, queries  int
}

// addRepository adds the in-repo texts the manifest records: each train-split file that
// excludedReason lets in, which must hash to its pinned digest at rev, cut into chunks
// of at most maxBytes, and each figure outside eval fold 0. It collects the forced
// tokens and the input records for addCornstack.
func (m *textMix) addRepository(records []sourceRecord, files map[string][]byte, rev, sourcesPath string, maxBytes int) error {
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
				id := r.Path + ":" + c.span()
				if m.decontam.admit("repo", id, c.text) {
					m.texts = append(m.texts, teacher.Text{ID: id, Text: c.text})
				}
			}
		case "figure":
			id := "figure:" + r.ID
			if r.Fold != 0 && m.decontam.admit("figure", id, r.Title+"\n"+r.Desc) {
				m.figures++
				m.texts = append(m.texts, teacher.Text{ID: id, Text: r.Title + "\n" + r.Desc})
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
		got, err := readCornstack(path, in.Name, rows, maxBytes, m.decontam)
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
	var readError error
	fail := func(_ sourceRecord, err error) bool {
		readError = err
		return false
	}
	for n, line := range jsonLines(path, fail) {
		var r sourceRecord
		if err := json.Unmarshal(line, &r); err != nil {
			return nil, fmt.Errorf("%s:%d: %w", path, n, err)
		}
		out = append(out, r)
	}
	if readError != nil {
		return nil, readError
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
// query and the first chunk of its document. A row whose query or whole document d
// refuses is dropped and the next row read in its place.
func readCornstack(path, name string, rows, maxBytes int, d *decontaminator) ([]teacher.Text, error) {
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
		if !d.admit("cornstack", id, r.Query, r.Document) {
			continue
		}
		out = append(out,
			teacher.Text{ID: id + ":query", Text: r.Query[:runeCut(r.Query, maxBytes)]},
			teacher.Text{ID: id + ":document", Text: doc[0].text})
	}
	return out, nil
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
