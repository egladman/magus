package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"maps"
	"math/rand/v2"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"text/tabwriter"

	"github.com/egladman/magus/libs/proofread"
	"github.com/egladman/magus/libs/proofread/internal/calibrate"
	"github.com/egladman/magus/libs/proofread/internal/xdg"
)

// runVoice runs `proofread voice build` or `proofread voice eval`.
func runVoice(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) > 0 {
		switch args[0] {
		case "build":
			return runVoiceBuild(args[1:], stdin, stderr)
		case "eval":
			return runVoiceEval(args[1:], stdout, stderr)
		}
	}

	return failure(stderr, errors.New("voice takes a subcommand, build or eval"))
}

func runVoiceBuild(args []string, stdin io.Reader, stderr io.Writer) int {
	fs := flag.NewFlagSet("proofread voice build", flag.ContinueOnError)
	fs.SetOutput(stderr)
	kind := fs.String("kind", "", "the `kind` of every text, or of every JSON line naming none")
	field := fs.String("field", "", "read JSON lines and take each text from this `field`")
	kindField := fs.String("kind-field", "", "with --field, take each text's kind from this `field`")
	out := fs.String("out", "", "write the voice file to this `path` (default $XDG_CONFIG_HOME/proofread/voice.json)")
	fromStdin := fs.Bool("stdin", false, "read the texts from stdin")

	if err := fs.Parse(args); err != nil {
		return exitError
	}

	switch {
	case *fromStdin && fs.NArg() > 0:
		return failure(stderr, errors.New("--stdin reads the texts, so name no file"))
	case !*fromStdin && fs.NArg() == 0:
		return failure(stderr, errors.New("name the files to measure, or pass --stdin to read the texts from stdin"))
	}

	texts, err := voiceTexts(proofread.Kind(*kind), *field, *kindField, fs.Args(), stdin)
	if err != nil {
		return failure(stderr, err)
	}

	v, err := proofread.BuildVoice(texts)
	if err != nil {
		return failure(stderr, err)
	}

	path := *out
	if path == "" {
		if path, err = defaultVoicePath(); err != nil {
			return failure(stderr, err)
		}

		if tree := workTree(filepath.Dir(path)); tree != "" {
			return failure(stderr, fmt.Errorf("the default voice path %s is inside the git work tree %s, "+
				"where a commit could publish it: name the path with --out", path, tree))
		}
	}

	if err := writeVoice(path, v); err != nil {
		return failure(stderr, err)
	}

	fmt.Fprint(stderr, voiceSummary(v, path))

	return exitOK
}

// runVoiceEval scores, per kind, how far each shape feature separates the
// owner's texts from the other texts. It reads the two files and writes
// nothing else.
func runVoiceEval(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("proofread voice eval", flag.ContinueOnError)
	fs.SetOutput(stderr)
	owner := fs.String("owner", "", "read the owner's texts as JSON lines from this `file`")
	other := fs.String("other", "", "read the texts to tell them from as JSON lines from this `file`")
	kind := fs.String("kind", "", "the `kind` of every JSON line naming none")
	field := fs.String("field", "text", "take each text from this `field`")
	kindField := fs.String("kind-field", "kind", "take each text's kind from this `field`")
	format := fs.String("o", "text", "write the separations as `text` or json")
	pool := fs.Int("pool", 0, "measure random chunks of at least this many `words` instead of single texts; 0 pools nothing")
	seed := fs.Uint64("seed", proofread.VoiceSeed, "shuffle the texts under this `seed` before pooling")
	paired := fs.Bool("paired", false, "read --other as rewrites of the owner's texts, joined by --id-field, and score how each feature moves")
	idField := fs.String("id-field", "id", "with --paired, join the two files on this `field`")

	if err := fs.Parse(args); err != nil {
		return exitError
	}

	switch {
	case *pool < 0:
		return failure(stderr, fmt.Errorf("--pool is %d: name a word count, or 0 to pool nothing", *pool))
	case *owner == "" || *other == "":
		return failure(stderr, errors.New("voice eval needs --owner and --other"))
	case fs.NArg() > 0:
		return failure(stderr, fmt.Errorf("voice eval reads --owner and --other, not %s", strings.Join(fs.Args(), " ")))
	case *format != "text" && *format != "json":
		return failure(stderr, fmt.Errorf("unknown -o %q: write text or json", *format))
	case *field == "":
		return failure(stderr, errors.New("voice eval reads JSON lines: name the text's field with --field"))
	case *paired && *idField == "":
		return failure(stderr, errors.New("voice eval --paired joins on an id: name its field with --id-field"))
	}

	if *paired {
		return runPairedEval(pairedEval{
			owner: *owner, other: *other, kind: proofread.Kind(*kind), field: *field, kindField: *kindField,
			idField: *idField, format: *format, pool: *pool, seed: *seed,
		}, stdout, stderr)
	}

	read := func(path string) (map[proofread.Kind][]proofread.Shape, error) {
		texts, err := voiceTexts(proofread.Kind(*kind), *field, *kindField, []string{path}, nil)
		if err != nil {
			return nil, err
		}

		out := map[proofread.Kind][]proofread.Shape{}

		for k, list := range texts {
			if !slices.ContainsFunc(subcommands, func(s subcommand) bool { return s.kind == k }) {
				return nil, fmt.Errorf("%s: unknown kind %q", path, k)
			}

			if *pool > 0 {
				out[k] = proofread.MeasureWindowShapes(list, k, *pool, *seed)

				continue
			}

			for _, text := range list {
				out[k] = append(out[k], proofread.MeasureShape(text, k))
			}
		}

		return out, nil
	}

	owners, err := read(*owner)
	if err != nil {
		return failure(stderr, err)
	}

	others, err := read(*other)
	if err != nil {
		return failure(stderr, err)
	}

	kinds := slices.Collect(maps.Keys(owners))
	for k := range others {
		if _, ok := owners[k]; !ok {
			kinds = append(kinds, k)
		}
	}

	slices.Sort(kinds)

	seps := make(map[proofread.Kind][]calibrate.Separation, len(kinds))
	for _, k := range kinds {
		seps[k] = calibrate.Separate(owners[k], others[k])
	}

	if *format == "json" {
		data, err := json.MarshalIndent(seps, "", "  ")
		if err != nil {
			return failure(stderr, fmt.Errorf("encode the separations: %w", err))
		}

		fmt.Fprintf(stdout, "%s\n", data)

		return exitOK
	}

	for i, k := range kinds {
		if i > 0 {
			fmt.Fprintln(stdout)
		}

		fmt.Fprint(stdout, separationTable(k, len(owners[k]), len(others[k]), *pool, seps[k]))
	}

	return exitOK
}

// separationTable is a header naming kind and its counts, then one aligned
// line per separation. A pool above 0 counts chunks of at least that many
// words instead of texts.
func separationTable(kind proofread.Kind, owner, other, pool int, seps []calibrate.Separation) string {
	var b strings.Builder

	unit := "texts"
	if pool > 0 {
		unit = "chunks"
	}

	fmt.Fprintf(&b, "%s: %d owner %s, %d other %s", kind, owner, unit, other, unit)

	if pool > 0 {
		fmt.Fprintf(&b, ", chunks of %d+ words", pool)
	}

	b.WriteString("\n")

	if len(seps) == 0 {
		fmt.Fprintf(&b, "  no feature measured on %d %s on each side\n", calibrate.MinSeparationTexts, unit)

		return b.String()
	}

	tw := tabwriter.NewWriter(&b, 0, 0, 2, ' ', 0)

	for _, s := range seps {
		direction := "no difference"

		switch {
		case s.AUC > 0.5:
			direction = "owner higher"
		case s.AUC < 0.5:
			direction = "owner lower"
		}

		fmt.Fprintf(tw, "  %s\t%.3f\t%d\t%d\t%s\n", s.Feature, s.AUC, s.OwnerTexts, s.OtherTexts, direction)
	}

	_ = tw.Flush()

	return b.String()
}

// voiceTexts reads the texts to measure, keyed by kind. With field, every
// line of each file in paths, or of stdin, is a JSON object holding a text.
// Otherwise each file is one text of kind, and stdin holds texts separated by
// NUL bytes, so `git log --format=%B%x00` feeds it whole messages.
func voiceTexts(kind proofread.Kind, field, kindField string, paths []string, stdin io.Reader) (map[proofread.Kind][]string, error) {
	switch {
	case kindField != "" && field == "":
		return nil, errors.New("--kind-field needs --field, since only JSON lines name a kind")
	case kind == "" && kindField == "":
		return nil, errors.New("name the texts' kind with --kind, or with --kind-field for JSON lines")
	}

	out := map[proofread.Kind][]string{}

	read := func(name string, r io.Reader, fromStdin bool) error {
		if field != "" {
			return jsonLineTexts(name, r, kind, field, kindField, out)
		}

		data, err := io.ReadAll(r)
		if err != nil {
			return fmt.Errorf("read %s: %w", name, err)
		}

		if !fromStdin {
			out[kind] = append(out[kind], string(data))

			return nil
		}

		for text := range bytes.SplitSeq(data, []byte{0}) {
			if len(bytes.TrimSpace(text)) > 0 {
				out[kind] = append(out[kind], string(text))
			}
		}

		return nil
	}

	if len(paths) == 0 {
		return out, read("stdin", stdin, true)
	}

	for _, p := range paths {
		f, err := os.Open(p)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", p, err)
		}

		err = read(p, f, false)
		f.Close()

		if err != nil {
			return nil, err
		}
	}

	return out, nil
}

// jsonLineTexts adds the text each JSON line of r holds in field to out,
// under the kind kindField names, or kind when the line names none.
func jsonLineTexts(name string, r io.Reader, kind proofread.Kind, field, kindField string, out map[proofread.Kind][]string) error {
	records, err := jsonLineRecords(name, r, kind, field, kindField, "")
	if err != nil {
		return err
	}

	for _, rec := range records {
		out[rec.kind] = append(out[rec.kind], rec.text)
	}

	return nil
}

// textRecord is one JSON line's text, its kind and, when asked for, its id.
type textRecord struct {
	kind     proofread.Kind
	id, text string
}

// jsonLineRecords reads r as [jsonLineTexts] does. With idField, each line
// must also hold a string id in that field.
func jsonLineRecords(name string, r io.Reader, kind proofread.Kind, field, kindField, idField string) ([]textRecord, error) {
	var out []textRecord

	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)

	for n := 1; scanner.Scan(); n++ {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}

		var record map[string]any
		if err := json.Unmarshal(line, &record); err != nil {
			return nil, fmt.Errorf("%s:%d: %w", name, n, err)
		}

		text, ok := record[field].(string)
		if !ok {
			return nil, fmt.Errorf("%s:%d: no string field %q", name, n, field)
		}

		k := kind

		if kindField != "" {
			if named, ok := record[kindField].(string); ok && named != "" {
				k = proofread.Kind(named)
			}
		}

		if k == "" {
			return nil, fmt.Errorf("%s:%d: no string field %q names a kind, and --kind gives none", name, n, kindField)
		}

		rec := textRecord{kind: k, text: text}

		if idField != "" {
			if rec.id, ok = record[idField].(string); !ok || rec.id == "" {
				return nil, fmt.Errorf("%s:%d: no string field %q holds an id", name, n, idField)
			}
		}

		out = append(out, rec)
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read %s: %w", name, err)
	}

	return out, nil
}

// defaultVoicePath is $XDG_CONFIG_HOME/proofread/voice.json (see
// [xdg.ConfigDir]).
func defaultVoicePath() (string, error) {
	dir, err := xdg.ConfigDir()
	if err != nil {
		return "", fmt.Errorf("find the voice file's directory: %w", err)
	}

	return filepath.Join(dir, "proofread", "voice.json"), nil
}

// workTree returns the root of the git work tree holding dir, or "" when none
// does. dir need not exist: its nearest existing ancestor is resolved through
// any symlink first, so a config directory linked into a dotfiles repository
// is found.
func workTree(dir string) string {
	dir = filepath.Clean(dir)

	for {
		if resolved, err := filepath.EvalSymlinks(dir); err == nil {
			dir = resolved

			break
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}

		dir = parent
	}

	for {
		if _, err := os.Lstat(filepath.Join(dir, ".git")); err == nil {
			return dir
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}

		dir = parent
	}
}

// writeVoice writes v to path, readable by its owner alone.
func writeVoice(path string, v *proofread.Voice) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("encode the voice: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("write the voice: %w", err)
	}

	if err := os.WriteFile(path, append(data, '\n'), 0o600); err != nil {
		return fmt.Errorf("write the voice: %w", err)
	}

	if err := os.Chmod(path, 0o600); err != nil {
		return fmt.Errorf("write the voice: %w", err)
	}

	return nil
}

// voiceSummary says per kind how many texts and words v measured and the
// reference voice-drift judges it by, or that it skipped the kind and why,
// then whether v relaxes tense and where v went.
func voiceSummary(v *proofread.Voice, path string) string {
	var b strings.Builder

	for _, k := range slices.Sorted(maps.Keys(v.Kinds)) {
		kv := v.Kinds[k]
		fmt.Fprintf(&b, "%s: %d texts, %d words", k, kv.Texts, kv.Words)

		switch {
		case kv.Words < proofread.MinVoiceWords:
			fmt.Fprintf(&b, ", under %d, so voice-drift skips it", proofread.MinVoiceWords)
		case kv.SubjectP95 > 0:
			fmt.Fprintf(&b, ", subjects of %.0f characters or fewer 95%% of the time", kv.SubjectP95)
		case kv.Windows != nil:
			fmt.Fprintf(&b, ", %d windows", len(kv.Windows))
		default:
			b.WriteString(", but no window measured two features, so voice-drift skips it")
		}

		b.WriteString("\n")
	}

	if v.RelaxesTense() {
		b.WriteString("tense advises rather than denies on your change descriptions\n")
	}

	fmt.Fprintf(&b, "wrote %s\n", path)

	return b.String()
}

// readVoiceFile reads the voice file -voice names.
func readVoiceFile(path string) (*proofread.Voice, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("read voice %s: %w", path, err)
	}
	defer f.Close()

	v, err := proofread.ReadVoice(f)
	if err != nil {
		return nil, fmt.Errorf("voice %s: %w", path, err)
	}

	return v, nil
}

// voiceOptions reads the voice file -voice names. The name default is the
// file voice build writes with no --out. With none there, it says so on
// stderr and returns no option.
func voiceOptions(name string, stderr io.Writer) ([]proofread.Option, error) {
	path := name
	if name == "default" {
		var err error
		if path, err = defaultVoicePath(); err != nil {
			return nil, err
		}
	}

	v, err := readVoiceFile(path)

	switch {
	case name == "default" && errors.Is(err, os.ErrNotExist):
		fmt.Fprintf(stderr, "proofread: no voice file at %s, so this text is judged with no voice; "+
			"proofread voice build writes one\n", path)

		return []proofread.Option{}, nil
	case err != nil:
		return nil, err
	}

	return []proofread.Option{proofread.WithVoice(v)}, nil
}

// pairedEval is what `voice eval --paired` was asked for.
type pairedEval struct {
	owner, other, field, kindField, idField, format string
	kind                                            proofread.Kind
	pool                                            int
	seed                                            uint64
}

// runPairedEval joins the owner's texts with their rewrites by id, per kind,
// and writes how far each feature moves across the pairs.
func runPairedEval(e pairedEval, stdout, stderr io.Writer) int {
	owners, err := e.read(e.owner)
	if err != nil {
		return failure(stderr, err)
	}

	others, err := e.read(e.other)
	if err != nil {
		return failure(stderr, err)
	}

	byKind := calibrate.JoinPairs(owners, others)
	kinds := slices.Sorted(maps.Keys(byKind))
	seps := make(map[proofread.Kind][]calibrate.PairedSeparation, len(kinds))
	units := make(map[proofread.Kind]int, len(kinds))

	for _, k := range kinds {
		owners, others := e.measure(k, byKind[k].Pairs)
		seps[k], units[k] = calibrate.SeparatePairs(owners, others), len(owners)
	}

	if e.format == "json" {
		data, err := json.MarshalIndent(seps, "", "  ")
		if err != nil {
			return failure(stderr, fmt.Errorf("encode the separations: %w", err))
		}

		fmt.Fprintf(stdout, "%s\n", data)

		return exitOK
	}

	for i, k := range kinds {
		if i > 0 {
			fmt.Fprintln(stdout)
		}

		fmt.Fprint(stdout, calibrate.PairedTable(k, units[k], byKind[k].Unpaired, e.pool, seps[k]))
	}

	return exitOK
}

// read returns path's texts by kind and id.
func (e pairedEval) read(path string) (map[proofread.Kind]map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()

	records, err := jsonLineRecords(path, f, e.kind, e.field, e.kindField, e.idField)
	if err != nil {
		return nil, err
	}

	texts := make([]calibrate.KeyedText, 0, len(records))

	for _, rec := range records {
		if !slices.ContainsFunc(subcommands, func(s subcommand) bool { return s.kind == rec.kind }) {
			return nil, fmt.Errorf("%s: unknown kind %q", path, rec.kind)
		}

		texts = append(texts, calibrate.KeyedText{Kind: rec.kind, ID: rec.id, Text: rec.text})
	}

	return calibrate.IndexTexts(path, texts)
}

// measure returns each pair's two shapes. A pool groups shuffled pairs until
// the owner side holds pool words. Each chunk and its rewrite then cover the
// same texts, which keeps the topic fixed.
func (e pairedEval) measure(kind proofread.Kind, pairs []calibrate.TextPair) (owners, others []proofread.Shape) {
	if e.pool == 0 {
		for _, p := range pairs {
			owners = append(owners, proofread.MeasureShape(p.Owner, kind))
			others = append(others, proofread.MeasureShape(p.Other, kind))
		}

		return owners, others
	}

	shuffled := slices.Clone(pairs)
	rand.New(rand.NewPCG(e.seed, e.seed)).Shuffle(len(shuffled), func(i, j int) {
		shuffled[i], shuffled[j] = shuffled[j], shuffled[i]
	})

	var (
		ownerChunk, otherChunk []string
		words                  int
	)

	for _, p := range shuffled {
		ownerChunk, otherChunk = append(ownerChunk, p.Owner), append(otherChunk, p.Other)
		words += proofread.Measure(p.Owner, kind).Words

		if words >= e.pool {
			owners = append(owners, proofread.MeasureShape(strings.Join(ownerChunk, "\n\n"), kind))
			others = append(others, proofread.MeasureShape(strings.Join(otherChunk, "\n\n"), kind))
			ownerChunk, otherChunk, words = nil, nil, 0
		}
	}

	return owners, others
}
