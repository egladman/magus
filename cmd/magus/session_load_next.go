package main

// The served-next join: `session load` is the one place that holds a host's command
// text and magus's own record of what it printed at the same time, so it is where the
// two meet.
//
// Neither side can answer alone. The store never keeps a command's text, so a later
// reader cannot tell `magus explain` from `magus refs`; the journal keeps only a
// recency window, so it cannot say how often one was taken.

import (
	"cmp"
	"slices"
	"time"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/sessions"
)

const (
	// nextJoinWindow bounds how long after a call's start a breadcrumb may have been
	// printed and still belong to it. A host stamps the call when it STARTS and magus
	// writes the journal line while it runs, so the gap is one command's runtime; ten
	// minutes covers a slow gate without reaching the call after it.
	nextJoinWindow = 10 * time.Minute

	// nextLookahead is how many calls later a breadcrumb still counts as taken up. The
	// measurement this metric has to be comparable with used five.
	nextLookahead = 5
)

// joinServedNext stamps events with the hint ids each call served and took up.
//
// commands is positional against events and holds the shell text the store is about
// to drop. journal is this checkout's served-next lines, oldest first.
//
// Attribution is by time, since a journal line records no call: the serving event is
// the most recent shell command that had started when the line was written. A stream
// carrying several interleaved sessions can therefore credit the wrong one, which
// costs a per-id rate some precision and costs the totals nothing.
func joinServedNext(events []sessions.LoadEvent, commands []string, journal []hint.ServedNextEntry) {
	if len(events) == 0 || len(journal) == 0 {
		return
	}
	order := shellCommandOrder(events, commands)
	if len(order) == 0 {
		return
	}
	ran := make(map[int][][]string, len(order))
	for _, at := range order {
		ran[at] = ranArgv(commands[at])
	}
	for _, served := range journal {
		i, ok := servingCall(events, order, served.AtMs)
		if !ok {
			continue
		}
		at := order[i]
		events[at].Event.NextServed = appendUnique(events[at].Event.NextServed, served.ID)

		want := hint.NormalizeServedArgv(served.Argv)
		if len(want) == 0 {
			continue
		}
		for _, next := range order[i+1 : min(i+1+nextLookahead, len(order))] {
			if events[next].Session != events[at].Session {
				continue
			}
			// The whole argv, not a substring of the line: `explain spell:go`
			// otherwise counts `explain spell:gomod` as having taken it up, and the
			// rate is the one number this join exists to produce.
			if slices.ContainsFunc(ran[next], func(argv []string) bool { return slices.Equal(argv, want) }) {
				events[next].Event.NextFollowed = appendUnique(events[next].Event.NextFollowed, served.ID)
			}
		}
	}
}

// ranArgv renders every command a recorded shell line would run, normalized the way
// the journal's own argv is, so the two are comparable.
func ranArgv(command string) [][]string {
	cmds, ok := parseGuardCommands(command)
	if !ok {
		return nil
	}
	out := make([][]string, 0, len(cmds))
	for _, c := range cmds {
		out = append(out, hint.NormalizeServedArgv(append([]string{c.Name}, c.Args...)))
	}
	return out
}

// shellCommandOrder indexes the shell commands in the stream, oldest first. Only
// those: a file read between two calls is not a call the lookahead should spend.
func shellCommandOrder(events []sessions.LoadEvent, commands []string) []int {
	var order []int
	for i := range events {
		if i < len(commands) && events[i].Event.Kind == sessions.EventShellCommand {
			order = append(order, i)
		}
	}
	slices.SortStableFunc(order, func(a, b int) int {
		return cmp.Compare(events[a].Event.AtMs, events[b].Event.AtMs)
	})
	return order
}

// servingCall returns the position in order of the call a breadcrumb printed at atMs
// belongs to: the newest one that had already started, inside the join window.
func servingCall(events []sessions.LoadEvent, order []int, atMs int64) (int, bool) {
	for i := len(order) - 1; i >= 0; i-- {
		at := events[order[i]].Event.AtMs
		if at > atMs {
			continue
		}
		if atMs-at > nextJoinWindow.Milliseconds() {
			return 0, false
		}
		return i, true
	}
	return 0, false
}
