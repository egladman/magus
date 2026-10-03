// Package readlog records what a workspace load read from outside its own sources: the
// environment variables it looked up and the files it opened. A load's declarations are a
// function of those reads as much as of the magusfiles, so a stamp that stands for "the
// evaluated workspace is unchanged" folds them in, and a read answered from a stored
// knowledge graph never has to evaluate the workspace to know it still holds.
//
// The log rides on the context during the load; every host read that can shape a top
// level (environ.Lookup, fs.readFile, the Buzz source reader) records into it when one is
// there and costs nothing when none is.
package readlog

import (
	"context"
	"slices"
	"sync"
)

// Log is what one load read. Safe for concurrent use: a load evaluates projects in
// parallel.
type Log struct {
	mu     sync.Mutex
	files  map[string]struct{}
	env    map[string]struct{}
	envAll bool
}

// Reads is a Log's content, sorted, as a stamp folds it.
type Reads struct {
	// Files are the absolute paths the load opened, inside the tree or not.
	Files []string `json:"files,omitempty"`
	// Env are the variable names the load looked up.
	Env []string `json:"env,omitempty"`
	// EnvAll records a read of the whole environment, which no name list covers.
	EnvAll bool `json:"env_all,omitempty"`
}

type ctxKey struct{}

// With returns ctx carrying log, so reads under it record there.
func With(ctx context.Context, log *Log) context.Context {
	return context.WithValue(ctx, ctxKey{}, log)
}

// From returns the log ctx carries, or nil.
func From(ctx context.Context) *Log {
	log, _ := ctx.Value(ctxKey{}).(*Log)
	return log
}

// File records that the load read path.
func File(ctx context.Context, path string) {
	if l := From(ctx); l != nil {
		l.mu.Lock()
		if l.files == nil {
			l.files = map[string]struct{}{}
		}
		l.files[path] = struct{}{}
		l.mu.Unlock()
	}
}

// Env records that the load looked name up.
func Env(ctx context.Context, name string) {
	if l := From(ctx); l != nil {
		l.mu.Lock()
		if l.env == nil {
			l.env = map[string]struct{}{}
		}
		l.env[name] = struct{}{}
		l.mu.Unlock()
	}
}

// EnvAll records that the load read the whole environment.
func EnvAll(ctx context.Context) {
	if l := From(ctx); l != nil {
		l.mu.Lock()
		l.envAll = true
		l.mu.Unlock()
	}
}

// Reads returns what the log holds, sorted. A nil log reads nothing.
func (l *Log) Reads() Reads {
	if l == nil {
		return Reads{}
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	out := Reads{EnvAll: l.envAll}
	for p := range l.files {
		out.Files = append(out.Files, p)
	}
	for n := range l.env {
		out.Env = append(out.Env, n)
	}
	slices.Sort(out.Files)
	slices.Sort(out.Env)
	return out
}
