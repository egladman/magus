package vcs

import (
	"context"
	"strings"
	"time"

	"github.com/egladman/magus/types"
)

// Each driver emits one commit as NUL-delimited fields in this fixed order, so a
// single shared parser (parseCommit) builds the normalized types.Commit and the
// per-driver code is reduced to the right query + template.
const (
	fieldID = iota
	fieldShort
	fieldAuthorName
	fieldAuthorEmail
	fieldDate
	fieldParents
	fieldMessage
	numCommitFields
)

// commitDelim is the NUL field separator. git (%x00), hg (\0) and jj ("\0") all
// emit it, and it cannot occur inside a commit field.
const commitDelim = "\x00"

// parseCommit builds a types.Commit from one driver's NUL-delimited record. A
// record with too few fields yields a Commit with whatever parsed — drivers
// always emit numCommitFields, so a short record signals a template bug, not
// user data.
func parseCommit(raw string) types.Commit {
	f := strings.Split(raw, commitDelim)
	if len(f) < numCommitFields {
		// Pad so field access never panics; missing tail fields stay empty.
		f = append(f, make([]string, numCommitFields-len(f))...)
	}
	c := types.Commit{
		ID:    strings.TrimSpace(f[fieldID]),
		Short: strings.TrimSpace(f[fieldShort]),
		Author: types.Person{
			Name:  strings.TrimSpace(f[fieldAuthorName]),
			Email: strings.TrimSpace(f[fieldAuthorEmail]),
		},
		Date:    parseWhen(f[fieldDate]),
		Parents: parents(f[fieldParents]),
	}
	c.Subject, c.Body = splitMessage(f[fieldMessage])
	return c
}

// commitWhenLayouts are the timestamp forms drivers emit. git %cI, hg
// rfc3339date and jj %:z all produce RFC 3339 with a colon offset; the second
// layout tolerates a numeric offset without the colon (e.g. +0000) so a driver
// build that formats that way doesn't silently zero every Date.
var commitWhenLayouts = []string{time.RFC3339, "2006-01-02T15:04:05Z0700"}

// parseWhen parses a driver's record-date timestamp; an unparseable or empty
// value yields the zero time.
func parseWhen(s string) time.Time {
	s = strings.TrimSpace(s)
	for _, layout := range commitWhenLayouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t
		}
	}
	return time.Time{}
}

// splitMessage splits a full commit message into its first line (subject) and the
// remainder (body, with the blank separator line trimmed).
func splitMessage(msg string) (subject, body string) {
	msg = strings.TrimSpace(msg)
	if msg == "" {
		return "", ""
	}
	if i := strings.IndexByte(msg, '\n'); i >= 0 {
		return strings.TrimSpace(msg[:i]), strings.TrimSpace(msg[i+1:])
	}
	return msg, ""
}

// resolveEach maps revision ids to commits via v.FindCommit, the shared back end
// of every driver's History (list the ids, then resolve each through the one
// tested FindCommit path rather than a second, divergent multi-commit parser).
func resolveEach(ctx context.Context, dir string, v types.VCSDriver, ids []string) ([]types.Commit, error) {
	commits := make([]types.Commit, 0, len(ids))
	for _, id := range ids {
		c, err := v.FindCommit(ctx, dir, id)
		if err != nil {
			return nil, err
		}
		commits = append(commits, c)
	}
	return commits, nil
}

// parents splits the space-separated parent field, dropping empties and any
// all-zero token. An all-zero id is the null-parent sentinel a VCS emits for an
// absent parent (e.g. hg's p2node); no real commit id is all zeros, so removing
// them is safe.
func parents(field string) []string {
	var out []string
	for _, p := range strings.Fields(field) {
		if p == "" || strings.Trim(p, "0") == "" {
			continue
		}
		out = append(out, p)
	}
	return out
}
