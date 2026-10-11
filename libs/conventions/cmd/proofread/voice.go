package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/egladman/magus/libs/conventions/proofread"
)

// runVoice runs `proofread voice build`, the one voice subcommand.
func runVoice(args []string, stdin io.Reader, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "build" {
		return failure(stderr, errors.New("voice takes one subcommand, build"))
	}

	fs := flag.NewFlagSet("proofread voice build", flag.ContinueOnError)
	fs.SetOutput(stderr)
	kind := fs.String("kind", "", "the `kind` of every text, or of every JSON line naming none")
	field := fs.String("field", "", "read JSON lines and take each text from this `field`")
	kindField := fs.String("kind-field", "", "with -field, take each text's kind from this `field`")
	out := fs.String("o", "", "write the voice file to this `path` (default $XDG_CONFIG_HOME/proofread/voice.json)")

	if err := fs.Parse(args[1:]); err != nil {
		return exitError
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
				"where a commit could publish it: name the path with -o", path, tree))
		}
	}

	if err := writeVoice(path, v); err != nil {
		return failure(stderr, err)
	}

	fmt.Fprint(stderr, voiceSummary(v, path))

	return exitOK
}

// voiceTexts reads the texts to measure, keyed by kind. With field, every
// line of each file in paths, or of stdin, is a JSON object holding a text.
// Otherwise each file is one text of kind, and stdin holds texts separated by
// NUL bytes, so `git log --format=%B%x00` feeds it whole messages.
func voiceTexts(kind proofread.Kind, field, kindField string, paths []string, stdin io.Reader) (map[proofread.Kind][]string, error) {
	switch {
	case kindField != "" && field == "":
		return nil, errors.New("-kind-field needs -field, since only JSON lines name a kind")
	case kind == "" && kindField == "":
		return nil, errors.New("name the texts' kind with -kind, or with -kind-field for JSON lines")
	}

	out := map[proofread.Kind][]string{}

	read := func(name string, r io.Reader) error {
		if field != "" {
			return jsonLineTexts(name, r, kind, field, kindField, out)
		}

		data, err := io.ReadAll(r)
		if err != nil {
			return fmt.Errorf("read %s: %w", name, err)
		}

		if name != "stdin" {
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
		return out, read("stdin", stdin)
	}

	for _, p := range paths {
		f, err := os.Open(p)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", p, err)
		}

		err = read(p, f)
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
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)

	for n := 1; scanner.Scan(); n++ {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}

		var record map[string]any
		if err := json.Unmarshal(line, &record); err != nil {
			return fmt.Errorf("%s:%d: %w", name, n, err)
		}

		text, ok := record[field].(string)
		if !ok {
			return fmt.Errorf("%s:%d: no string field %q", name, n, field)
		}

		k := kind

		if kindField != "" {
			if named, ok := record[kindField].(string); ok && named != "" {
				k = proofread.Kind(named)
			}
		}

		if k == "" {
			return fmt.Errorf("%s:%d: no string field %q names a kind, and -kind gives none", name, n, kindField)
		}

		out[k] = append(out[k], text)
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read %s: %w", name, err)
	}

	return nil
}

// defaultVoicePath is $XDG_CONFIG_HOME/proofread/voice.json, or
// ~/.config/proofread/voice.json when that is unset or relative.
func defaultVoicePath() (string, error) {
	if dir := os.Getenv("XDG_CONFIG_HOME"); filepath.IsAbs(dir) {
		return filepath.Join(dir, "proofread", "voice.json"), nil
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("find the voice file's directory: %w", err)
	}

	return filepath.Join(home, ".config", "proofread", "voice.json"), nil
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

// voiceSummary says per kind how many texts and words v measured, which kinds
// are too few for voice-drift to judge, whether v relaxes tense, and where v
// went.
func voiceSummary(v *proofread.Voice, path string) string {
	var b strings.Builder

	kinds := make([]proofread.Kind, 0, len(v.Kinds))
	for k := range v.Kinds {
		kinds = append(kinds, k)
	}

	slices.Sort(kinds)

	for _, k := range kinds {
		kv := v.Kinds[k]
		fmt.Fprintf(&b, "%s: %d texts, %d words", k, kv.Texts, kv.Words)

		if kv.Texts < proofread.MinVoiceTexts {
			fmt.Fprintf(&b, ", under %d, so voice-drift does not judge it", proofread.MinVoiceTexts)
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
