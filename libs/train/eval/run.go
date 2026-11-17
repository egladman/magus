package eval

import (
	"bufio"
	"bytes"
	"cmp"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/egladman/magus/libs/train/completion/fim"
	"github.com/egladman/magus/libs/train/dedup"
)

// Languages are the three the split, the FIM sets and the dedup cover, in report order.
var Languages = []string{dedup.Buzz, dedup.Go, dedup.TypeScript}

// MaxFileBytes is the largest file the dedup and the split read. Past it a file is a
// minified bundle or a fixture, not code worth a token (sources.buzz's bound).
const MaxFileBytes = 1 << 20

// TrainSpanBytes is the text one training sample stands for: a file gets one sample per
// this many bytes, at least one and at most MaxTrainPerFile. Per-epoch upsampling
// (Buzz 4x, TypeScript 2x) is the trainer's to apply on read.
const (
	TrainSpanBytes  = 2000
	MaxTrainPerFile = 64
)

// Env is where a stage reads and writes. Dir holds every dataset: it is under the XDG
// cache root, outside git, and only the digests Pin records are committed.
type Env struct {
	Root    string
	Rev     string
	Cutoff  string
	Dir     string
	Digests string
	Magus   string
}

func (e Env) File(name string) string { return filepath.Join(e.Dir, name) }

// Candidate is a row of candidates.jsonl, which split.buzz writes: a hand-written file
// the split considers.
type Candidate struct {
	Path     string `json:"path"`
	Language string `json:"language"`
}

// FileRow is a row of files.jsonl: a candidate the dedup read.
type FileRow struct {
	Path     string `json:"path"`
	Language string `json:"language"`
	Bytes    int    `json:"bytes"`
	Blob     string `json:"blob"`
}

// ReadRows reads a JSON Lines file into T values.
func ReadRows[T any](path string) ([]T, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []T
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	for n := 1; sc.Scan(); n++ {
		var r T
		if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
			return nil, fmt.Errorf("%s:%d: %w", path, n, err)
		}
		out = append(out, r)
	}
	return out, sc.Err()
}

// rowWriter writes JSON Lines to a temporary file, hashing as it goes, and renames it
// into place on Close.
type rowWriter struct {
	path string
	tmp  *os.File
	w    *bufio.Writer
	sum  hash.Hash
	rows int
}

func newRowWriter(path string) (*rowWriter, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	tmp, err := os.Create(path + ".tmp")
	if err != nil {
		return nil, err
	}
	sum := sha256.New()
	return &rowWriter{path: path, tmp: tmp, w: bufio.NewWriter(io.MultiWriter(tmp, sum)), sum: sum}, nil
}

func (r *rowWriter) Write(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	r.w.Write(b)
	r.w.WriteByte('\n')
	r.rows++
	return nil
}

func (r *rowWriter) Close() (string, error) {
	if err := r.w.Flush(); err != nil {
		r.tmp.Close()
		return "", err
	}
	if err := r.tmp.Close(); err != nil {
		return "", err
	}
	if err := os.Rename(r.tmp.Name(), r.path); err != nil {
		return "", err
	}
	return hex.EncodeToString(r.sum.Sum(nil)), nil
}

func writeRows[T any](path string, rows []T) (string, error) {
	w, err := newRowWriter(path)
	if err != nil {
		return "", err
	}
	for _, r := range rows {
		if err := w.Write(r); err != nil {
			return "", err
		}
	}
	return w.Close()
}

func (e Env) pin(name, sum string, rows int, rev string) error {
	return Pin(e.Digests, Digest{Name: name, SHA256: sum, Rows: rows, Rev: rev})
}

// RunDedup reads the candidates at Rev, writes files.jsonl and links.jsonl, and pins
// both. Every pair, exact or near, is a link for the split's group deal.
func RunDedup(e Env) (dedup.Report, error) {
	cands, err := ReadRows[Candidate](e.File("candidates.jsonl"))
	if err != nil {
		return dedup.Report{}, fmt.Errorf("candidates (run the split target, which writes them): %w", err)
	}
	slices.SortFunc(cands, func(a, b Candidate) int { return cmp.Compare(a.Path, b.Path) })
	blobs, err := OpenBlobs(e.Root, e.Rev)
	if err != nil {
		return dedup.Report{}, err
	}
	defer blobs.Close()
	var docs []dedup.Doc
	var files []FileRow
	for _, c := range cands {
		blob, data, err := blobs.Read(c.Path)
		if err != nil {
			return dedup.Report{}, err
		}
		if len(data) > MaxFileBytes || !utf8.Valid(data) || bytes.IndexByte(data, 0) >= 0 {
			continue
		}
		docs = append(docs, dedup.Doc{Path: c.Path, Language: c.Language, Text: string(data)})
		files = append(files, FileRow{Path: c.Path, Language: c.Language, Bytes: len(data), Blob: blob})
	}
	pairs, rep := dedup.Find(docs)
	sum, err := writeRows(e.File("files.jsonl"), files)
	if err != nil {
		return rep, err
	}
	if err := e.pin("files", sum, len(files), e.Rev); err != nil {
		return rep, err
	}
	sum, err = writeRows(e.File("links.jsonl"), pairs)
	if err != nil {
		return rep, err
	}
	if err := e.pin("links", sum, len(pairs), e.Rev); err != nil {
		return rep, err
	}
	return rep, nil
}

// Cutoff is the temporal holdout's commit, as inputs.jsonl pins it.
type Cutoff struct {
	Commit string `json:"revision"`
	Date   string `json:"date"`
	Status string `json:"status"`
}

// ReadCutoff reads the cutoff row of inputs.jsonl, the one whose name starts "cutoff/".
func ReadCutoff(inputs string) (Cutoff, error) {
	rows, err := ReadRows[struct {
		Name string `json:"name"`
		Cutoff
	}](inputs)
	if err != nil {
		return Cutoff{}, err
	}
	for _, r := range rows {
		if strings.HasPrefix(r.Name, "cutoff/") {
			if len(r.Commit) != 40 {
				return Cutoff{}, fmt.Errorf("%s: cutoff revision %q is not a full commit", inputs, r.Commit)
			}
			return r.Cutoff, nil
		}
	}
	return Cutoff{}, fmt.Errorf("%s pins no cutoff row", inputs)
}

// gitBlobSHA is the sha git gives a blob of data.
func gitBlobSHA(data []byte) string {
	h := sha1.New()
	fmt.Fprintf(h, "blob %d\x00", len(data))
	h.Write(data)
	return hex.EncodeToString(h.Sum(nil))
}

// SetCounts is what RunSets built.
type SetCounts struct {
	Spans          map[string]int `json:"spans"`
	Groups         map[string]int `json:"groups"`
	DevMissing     int            `json:"dev_files_absent_at_cutoff"`
	Tasks          int            `json:"tasks"`
	TaskFiles      int            `json:"task_files"`
	CheckPass      int            `json:"check_pass"`
	CheckTotal     int            `json:"check_total"`
	CheckAbsent    int            `json:"check_absent_at_cutoff"`
	CheckChanged   int            `json:"check_changed_since_cutoff"`
	CheckImport    int            `json:"check_failed_bzz2001"`
	CheckPassAtRev int            `json:"check_pass_in_place_at_rev"`
	Commits        int            `json:"commits_after_cutoff"`
	Changes        int            `json:"changes_after_cutoff"`
	SpansCheckPass int            `json:"buzz_test_spans_in_check_passing_files"`
}

// RunSets writes the FIM dev and test sets of each language, the Buzz pass@k task set
// and the post-cutoff change list, and pins each. A test set reads held-out files at Rev; a
// dev set reads the last DevTailShare of the lines of a dev-flagged train file at the
// cutoff, the version training reads.
func RunSets(e Env) (SetCounts, error) {
	c := SetCounts{Spans: map[string]int{}, Groups: map[string]int{}}
	entries, err := ReadSplit(e.File("split.jsonl"))
	if err != nil {
		return c, err
	}
	slices.SortFunc(entries, func(a, b Entry) int { return cmp.Compare(a.Path, b.Path) })
	tip, err := OpenBlobs(e.Root, e.Rev)
	if err != nil {
		return c, err
	}
	defer tip.Close()
	cut, err := OpenBlobs(e.Root, e.Cutoff)
	if err != nil {
		return c, err
	}
	defer cut.Close()

	var buzzTest []Entry
	for _, en := range entries {
		if en.Side == SideTest && en.Language == dedup.Buzz {
			buzzTest = append(buzzTest, en)
		}
	}
	tree, err := CutoffTree(e)
	if err != nil {
		return c, err
	}
	var checkPaths []string
	var checks []CheckRow
	passing := map[string]bool{}
	for _, b := range buzzTest {
		tipBlob, _, err := tip.Read(b.Path)
		if err != nil {
			return c, err
		}
		cutBlob, _, err := cut.Read(b.Path)
		switch {
		case err != nil:
			checks = append(checks, CheckRow{Path: b.Path, Absent: true})
			c.CheckAbsent++
		default:
			checkPaths = append(checkPaths, b.Path)
			if cutBlob != tipBlob {
				c.CheckChanged++
			}
		}
	}
	rows, err := CheckBuzz(e.Magus, tree, checkPaths, CheckBatch)
	if err != nil {
		return c, err
	}
	// The ruled check is the cutoff's. The set reads each file at Rev, so a task needs the
	// file to pass as it stands at Rev, in the checkout: that check runs too.
	allPaths := make([]string, len(buzzTest))
	for i, b := range buzzTest {
		allPaths[i] = b.Path
		_, data, err := tip.Read(b.Path)
		if err != nil {
			return c, err
		}
		onDisk, err := os.ReadFile(filepath.Join(e.Root, b.Path))
		if err != nil || gitBlobSHA(onDisk) != gitBlobSHA(data) {
			return c, fmt.Errorf("%s: the working tree differs from %s, so a check of it would not be a check of the set", b.Path, e.Rev)
		}
	}
	atRev, err := CheckBuzz(e.Magus, e.Root, allPaths, CheckBatch)
	if err != nil {
		return c, err
	}
	revOK := map[string]CheckRow{}
	for _, row := range atRev {
		revOK[row.Path] = row
		if row.OK {
			c.CheckPassAtRev++
			passing[row.Path] = true
		}
	}
	for i := range checks {
		checks[i].OKAtRev, checks[i].ReasonAtRev = revOK[checks[i].Path].OK, revOK[checks[i].Path].Reason
	}
	for _, row := range rows {
		c.CheckTotal++
		if row.OK {
			c.CheckPass++
		}
		if !row.OK && strings.Contains(row.Reason, "BZZ2001") {
			c.CheckImport++
		}
		row.OKAtRev, row.ReasonAtRev = revOK[row.Path].OK, revOK[row.Path].Reason
		checks = append(checks, row)
	}
	slices.SortFunc(checks, func(a, b CheckRow) int { return cmp.Compare(a.Path, b.Path) })
	sum, err := writeRows(e.File("checks.jsonl"), checks)
	if err != nil {
		return c, err
	}
	if err := e.pin("checks", sum, len(checks), e.Cutoff); err != nil {
		return c, err
	}

	tasks := []Task{}
	taskFiles := map[string]bool{}
	for _, lang := range Languages {
		test := newSetWriter(e, lang+"-test")
		dev := newSetWriter(e, lang+"-dev")
		for _, en := range entries {
			if en.Language != lang {
				continue
			}
			switch {
			case en.Side == SideTest:
				blob, data, err := tip.Read(en.Path)
				if err != nil {
					return c, err
				}
				f := fim.File{Path: en.Path, Blob: blob, Language: lang, Text: string(data)}
				for _, s := range FileSpans(f, fim.Limit{}) {
					if err := test.add(en.Group, s); err != nil {
						return c, err
					}
					if lang == dedup.Buzz && passing[en.Path] {
						c.SpansCheckPass++
					}
				}
				if lang == dedup.Buzz && passing[en.Path] {
					for _, t := range FileTasks(f, en.Group) {
						tasks = append(tasks, t)
						taskFiles[en.Path] = true
					}
				}
			case en.Dev:
				blob, data, err := cut.Read(en.Path)
				if err != nil {
					c.DevMissing++
					continue
				}
				text := string(data)
				f := fim.File{Path: en.Path, Blob: blob, Language: lang, Text: text}
				for _, s := range FileSpans(f, fim.Limit{From: DevTailStart(text)}) {
					if err := dev.add(en.Group, s); err != nil {
						return c, err
					}
				}
			}
		}
		for _, w := range []*setWriter{test, dev} {
			rows, groups, err := w.close()
			if err != nil {
				return c, err
			}
			c.Spans[w.name], c.Groups[w.name] = rows, groups
		}
	}
	slices.SortFunc(tasks, func(a, b Task) int { return cmp.Or(cmp.Compare(a.Path, b.Path), cmp.Compare(a.ID, b.ID)) })
	sum, err = writeRows(e.File("passk-buzz.jsonl"), tasks)
	if err != nil {
		return c, err
	}
	if err := e.pin("passk-buzz", sum, len(tasks), e.Rev); err != nil {
		return c, err
	}
	c.Tasks, c.TaskFiles = len(tasks), len(taskFiles)

	changes, commits, err := Temporal(e.Root, e.Cutoff, e.Rev, hasLanguage)
	if err != nil {
		return c, err
	}
	sum, err = writeRows(e.File("temporal.jsonl"), changes)
	if err != nil {
		return c, err
	}
	if err := e.pin("temporal", sum, len(changes), e.Rev); err != nil {
		return c, err
	}
	c.Commits, c.Changes = commits, len(changes)
	return c, nil
}

// hasLanguage is the filter for post-cutoff changes: a hand-written file of a language
// the sets cover. The split decides what is generated; here the path says enough.
func hasLanguage(path string) bool {
	if strings.HasSuffix(path, ".d.ts") || strings.Contains(path, "/gen/") || strings.Contains(path, "testdata/") {
		return false
	}
	switch filepath.Ext(path) {
	case ".go", ".buzz", ".ts", ".tsx":
		return true
	}
	return false
}

type setWriter struct {
	e      Env
	name   string
	rows   []Span
	groups map[string]bool
}

func newSetWriter(e Env, name string) *setWriter {
	return &setWriter{e: e, name: name, groups: map[string]bool{}}
}

func (w *setWriter) add(group string, s fim.Sample) error {
	w.rows = append(w.rows, Span{ID: SpanID(s), Set: w.name, Group: group, Sample: s})
	w.groups[group] = true
	return nil
}

func (w *setWriter) close() (int, int, error) {
	sum, err := writeRows(w.e.File("fim-"+w.name+".jsonl"), w.rows)
	if err != nil {
		return 0, 0, err
	}
	rev := w.e.Rev
	if strings.HasSuffix(w.name, "-dev") {
		rev = w.e.Cutoff
	}
	return len(w.rows), len(w.groups), w.e.pin("fim-"+w.name, sum, len(w.rows), rev)
}

// HeldOutTexts are the texts the 13-gram check holds training text against: every FIM span
// of every set, and every pass@k task.
func HeldOutTexts(e Env) ([]string, error) {
	var out []string
	for _, lang := range Languages {
		for _, side := range []string{"test", "dev"} {
			spans, err := ReadRows[Span](e.File("fim-" + lang + "-" + side + ".jsonl"))
			if err != nil {
				return nil, fmt.Errorf("eval sets (run the eval-sets target): %w", err)
			}
			for _, s := range spans {
				out = append(out, Text(s.Sample))
			}
		}
	}
	tasks, err := ReadRows[Task](e.File("passk-buzz.jsonl"))
	if err != nil {
		return nil, err
	}
	for _, t := range tasks {
		out = append(out, Text(fim.Sample{Prefix: t.Prefix, Middle: t.Middle, Suffix: t.Suffix}))
	}
	return out, nil
}

// TrainFile is a row of fim-train-files.jsonl: a training file that kept some text after
// the decontamination, at the cutoff blob training reads. Removed is the bytes taken out.
type TrainFile struct {
	Path    string `json:"path"`
	Blob    string `json:"blob"`
	Group   string `json:"group"`
	Dev     bool   `json:"dev"`
	Bytes   int    `json:"bytes"`
	Removed int    `json:"removed"`
	Samples int    `json:"samples"`
}

// Dropped is a row of fim-train-dropped.jsonl: a training file the check emptied.
type Dropped struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

// TrainRow is a row of fim-train.jsonl.
type TrainRow struct {
	Group string `json:"group"`
	fim.Sample
}

// TrainCounts is what RunFIM built. Touched counts the files that lost lines, Removed
// the bytes taken out, Emptied the files left with nothing, and NotReparsed the files
// whose scrubbed text no longer parses into FIM-eligible constructs.
type TrainCounts struct {
	Files       map[string]int `json:"files"`
	Touched     map[string]int `json:"files_touched"`
	Removed     map[string]int `json:"bytes_removed"`
	Scanned     map[string]int `json:"bytes_scanned"`
	Emptied     map[string]int `json:"files_emptied"`
	NotReparsed map[string]int `json:"files_not_reparsed"`
	Absent      int            `json:"absent_at_cutoff"`
	Samples     map[string]int `json:"samples"`
}

// trainText is the text of a train-side file training reads: the cutoff blob, and for a
// dev file only the lines before its dev tail, so dev text never reaches a prefix or a
// suffix window.
func trainText(en Entry, data []byte) string {
	text := string(data)
	if en.Dev {
		return text[:DevTailStart(text)]
	}
	return text
}

// RunFIM writes the training samples of every train-side file at the cutoff, with the
// lines that repeat an evaluation text under the 13-gram check taken out (see Scrub).
func RunFIM(e Env) (TrainCounts, error) {
	c := TrainCounts{Files: map[string]int{}, Touched: map[string]int{}, Removed: map[string]int{}, Scanned: map[string]int{},
		Emptied: map[string]int{}, NotReparsed: map[string]int{}, Samples: map[string]int{}}
	texts, err := HeldOutTexts(e)
	if err != nil {
		return c, err
	}
	dec := NewDecontaminator(texts)
	entries, err := ReadSplit(e.File("split.jsonl"))
	if err != nil {
		return c, err
	}
	slices.SortFunc(entries, func(a, b Entry) int { return cmp.Compare(a.Path, b.Path) })
	cut, err := OpenBlobs(e.Root, e.Cutoff)
	if err != nil {
		return c, err
	}
	defer cut.Close()
	samples, err := newRowWriter(e.File("fim-train.jsonl"))
	if err != nil {
		return c, err
	}
	var files []TrainFile
	var dropped []Dropped
	for _, en := range entries {
		if en.Side != SideTrain {
			continue
		}
		blob, data, err := cut.Read(en.Path)
		if err != nil {
			c.Absent++
			continue
		}
		original := trainText(en, data)
		c.Scanned[en.Language] += len(original)
		sc := Scrub(dec, en.Language, original)
		if sc.Removed > 0 {
			c.Touched[en.Language]++
			c.Removed[en.Language] += sc.Removed
		}
		if strings.TrimSpace(sc.Text) == "" {
			c.Emptied[en.Language]++
			dropped = append(dropped, Dropped{Path: en.Path, Reason: "every line repeats an eval text"})
			continue
		}
		if !sc.Reparsed {
			c.NotReparsed[en.Language]++
		}
		n := min(max(len(sc.Text)/TrainSpanBytes, 1), MaxTrainPerFile)
		for _, s := range fim.Build(fim.File{Path: en.Path, Blob: blob, Language: en.Language, Text: sc.Text}, n, fim.Limit{}) {
			if err := samples.Write(TrainRow{Group: en.Group, Sample: s}); err != nil {
				return c, err
			}
		}
		c.Files[en.Language]++
		c.Samples[en.Language] += n
		files = append(files, TrainFile{Path: en.Path, Blob: blob, Group: en.Group, Dev: en.Dev, Bytes: len(sc.Text), Removed: sc.Removed, Samples: n})
	}
	sum, err := samples.Close()
	if err != nil {
		return c, err
	}
	if err := e.pin("fim-train", sum, samples.rows, e.Cutoff); err != nil {
		return c, err
	}
	sum, err = writeRows(e.File("fim-train-files.jsonl"), files)
	if err != nil {
		return c, err
	}
	if err := e.pin("fim-train-files", sum, len(files), e.Cutoff); err != nil {
		return c, err
	}
	sum, err = writeRows(e.File("fim-train-dropped.jsonl"), dropped)
	if err != nil {
		return c, err
	}
	return c, e.pin("fim-train-dropped", sum, len(dropped), e.Cutoff)
}

// CutoffTree returns a checkout of the cutoff commit under the cache root, adding it with
// git worktree when it is not there. A check of a file in place there resolves imports the
// way the workspace at that commit does.
func CutoffTree(e Env) (string, error) {
	dir := e.File("cutoff-tree")
	if _, err := os.Stat(dir); err == nil {
		out, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
		if err != nil || strings.TrimSpace(string(out)) != e.Cutoff {
			return "", fmt.Errorf("%s exists but is not a checkout of %s: remove it (git worktree remove --force)", dir, e.Cutoff)
		}
		return dir, overlayConfig(e, dir)
	}
	if out, err := exec.Command("git", "-C", e.Root, "worktree", "add", "--detach", dir, e.Cutoff).CombinedOutput(); err != nil {
		return "", fmt.Errorf("git worktree add %s: %s: %w", dir, strings.TrimSpace(string(out)), err)
	}
	return dir, overlayConfig(e, dir)
}

// overlayConfig puts Rev's magus.yaml in the cutoff tree. The binary that checks is built
// at Rev and refuses a workspace file with a key it does not know, which the older file
// has; every other file stays the cutoff's.
func overlayConfig(e Env, dir string) error {
	out, err := exec.Command("git", "-C", e.Root, "show", e.Rev+":magus.yaml").Output()
	if err != nil {
		return fmt.Errorf("git show %s:magus.yaml: %w", e.Rev, err)
	}
	return os.WriteFile(filepath.Join(dir, "magus.yaml"), out, 0o644)
}

// PinSplit pins split.jsonl, which split.buzz wrote, in the digest file.
func PinSplit(e Env) error {
	entries, err := ReadSplit(e.File("split.jsonl"))
	if err != nil {
		return err
	}
	sum, err := FileDigest(e.File("split.jsonl"))
	if err != nil {
		return err
	}
	return e.pin("split", sum, len(entries), e.Rev)
}
