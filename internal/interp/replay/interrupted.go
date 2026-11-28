package replay

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/gofrs/flock"
)

// stoppedRun is a recorded run that stopped before its end, as [stoppedRuns] lists it.
// It stays unexported until a caller outside the package reads it.
type stoppedRun struct {
	Ref    string
	Script string
	Args   []string
	// Open is the innermost call the run was inside when it stopped, nil when it
	// stopped between calls.
	Open *Call
	// Resume is the command that resumes the run. It resumes only under the lease the
	// run was recorded under, since the lease is part of the ref.
	Resume string
}

// stoppedRuns lists, in ref order, each record under dir that did not finish and whose
// writer is dead. A missing dir holds none. An unreadable record is left out and its
// error joined into the one returned.
func stoppedRuns(dir string) ([]stoppedRun, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("replay: %w", err)
	}
	var runs []stoppedRun
	var errs []error
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		run, ok, err := readStopped(dir, e.Name())
		switch {
		case err != nil:
			errs = append(errs, err)
		case ok:
			runs = append(runs, run)
		}
	}
	return runs, errors.Join(errs...)
}

func readStopped(dir, ref string) (stoppedRun, bool, error) {
	path := filepath.Join(dir, ref, recordFile)
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return stoppedRun{}, false, nil
	}
	if err != nil {
		return stoppedRun{}, false, fmt.Errorf("replay: %w", err)
	}
	h, calls, finished, err := parse(path, raw[:bytes.LastIndexByte(raw, '\n')+1])
	if err != nil || finished {
		return stoppedRun{}, false, err
	}
	alive, err := writerAlive(path + ".lock")
	if err != nil || alive {
		return stoppedRun{}, false, err
	}
	run := stoppedRun{Ref: ref, Script: h.Script, Args: h.Args, Resume: resumeCommand(h)}
	for i := len(calls) - 1; i >= 0; i-- {
		if calls[i].End == callOpen {
			run.Open = &calls[i]
			break
		}
	}
	return run, true, nil
}

// writerAlive reports whether a process holds the record's one-writer lock. Taking the
// lock proves its holder is gone, since the kernel drops it when the holder dies.
func writerAlive(lockPath string) (bool, error) {
	lock := flock.New(lockPath)
	got, err := lock.TryLock()
	if err != nil {
		return false, fmt.Errorf("replay: probe %s: %w", lockPath, err)
	}
	if !got {
		return true, nil
	}
	return false, lock.Unlock()
}

// resumeCommand is the command line that reruns h's script with its arguments. The
// arguments follow `--`, so none is read as a magus flag.
func resumeCommand(h Header) string {
	words := []string{"magus", "buzz", "--record", shellWord(h.Script)}
	if len(h.Args) > 0 {
		words = append(words, "--")
		for _, a := range h.Args {
			words = append(words, shellWord(a))
		}
	}
	return strings.Join(words, " ")
}

var plainWord = regexp.MustCompile(`^[A-Za-z0-9_./=:@%+-]+$`)

// shellWord quotes w for a POSIX shell when it holds anything a shell would read.
func shellWord(w string) string {
	if plainWord.MatchString(w) {
		return w
	}
	return fmt.Sprintf("'%s'", strings.ReplaceAll(w, "'", `'\''`))
}
