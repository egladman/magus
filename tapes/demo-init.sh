#!/usr/bin/env bash
# demo-init.sh materializes the throwaway workspace the VHS tapes record
# against, and is meant to be SOURCED, not executed: it has to leave the
# recording shell sitting inside the workspace it just built.
#
#   source tapes/demo-init.sh
#
# Why a generated workspace rather than a fixture committed under tapes/:
# magus discovers a project from any magusfile.buzz below the workspace root,
# and there is no ignore mechanism. A demo magusfile checked in here would be
# picked up as a real project of the magus repo - the same failure a stale
# .claude/worktrees copy causes (MGS1002). So the fixture is written out at
# record time, outside the tree, and never exists inside it.
#
# The temp dir is also what makes the cache story honest. magus keeps its cache
# in .magus/ under the workspace root, so a fresh directory is a genuinely cold
# cache: the first `magus run build` really does the work and the second really
# replays it. Nothing here clears or touches the developer's own cache.

# Four small Go projects, stdlib only, named after the same acme monorepo the
# console demo uses so both surfaces show one product rather than two invented
# ones. Independent modules rather than cross-project replace directives: what
# is on screen is the cache and the affected set, and inter-module wiring would
# cost demo lines without adding to either.
_demo_projects=(libs/protocol libs/authkit services/ledger apps/admin)

_demo_root=$(mktemp -d "${TMPDIR:-/tmp}/magus-demo.XXXXXX")

# The tapes drive the magus built from this checkout, not whatever release
# happens to be on PATH, so a recording shows the behavior of the commit it was
# recorded at. BASH_SOURCE[0] resolves relative to this file, so sourcing works
# from any cwd.
_demo_repo=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
PATH="$_demo_repo:$PATH"
export PATH

mkdir -p "$_demo_root"
for _demo_p in "${_demo_projects[@]}"; do
	mkdir -p "$_demo_root/$_demo_p"
done

cat >"$_demo_root/magus.yaml" <<'YAML'
default_charms:
  - rw
YAML

# Root project: an aggregator whose targets fan out to the three below, so a
# bare `magus run build` from the root means "build everything".
cat >"$_demo_root/magusfile.buzz" <<'BUZZ'
import "magus";

// No "spells" list: this project binds no toolchain, and the magusfile driver
// that runs the targets below is bound by magus for every project (MGS1017).
magus\project({
    "name": "demo",
});

export fun preflight(ctx: magus\Context, args: [str]) > void {}
export fun build(ctx: magus\Context, args: [str]) > void { ctx.needs(preflight); }
export fun test(ctx: magus\Context, args: [str]) > void { ctx.needs(preflight); }
export fun lint(ctx: magus\Context, args: [str]) > void { ctx.needs(preflight); }
export fun ci(ctx: magus\Context, args: [str]) > void { ctx.needs(lint, build, test); }
BUZZ

for _demo_p in "${_demo_projects[@]}"; do
	cat >"$_demo_root/$_demo_p/magusfile.buzz" <<'BUZZ'
import "magus";
import "magus/spell/go";

magus\project({
    "spells": [go],
});

export fun preflight(ctx: magus\Context, args: [str]) > void {}
export fun lint(ctx: magus\Context, args: [str]) > void { ctx.needs(preflight); go["go-vet"](ctx); }
export fun build(ctx: magus\Context, args: [str]) > void { ctx.needs(preflight); go["go-build"](ctx); }
export fun test(ctx: magus\Context, args: [str]) > void { ctx.needs(preflight); go["go-test"](ctx); }
export fun ci(ctx: magus\Context, args: [str]) > void { ctx.needs(lint, build, test); }
BUZZ

	cat >"$_demo_root/$_demo_p/go.mod" <<BUZZ
module acme/$(basename "$_demo_p")

go 1.25
BUZZ
done

# Each project carries a handful of real test functions rather than one trivial
# assertion. Two reasons, both about the recording: `go test` prints a per-package
# line whose elapsed time has to look like work rather than 0.001s, and a reader
# pausing the GIF should see test names that could plausibly belong to a real
# service. Still stdlib only, so the fixture needs no network at record time.
cat >"$_demo_root/libs/protocol/protocol.go" <<'GO'
// Package protocol encodes and decodes the wire envelope every acme service speaks.
package protocol

import (
	"encoding/binary"
	"errors"
)

// ErrTruncated reports an envelope shorter than its declared payload.
var ErrTruncated = errors.New("protocol: truncated envelope")

// Envelope is one framed message: a version byte, then a length-prefixed payload.
type Envelope struct {
	Version uint8
	Payload []byte
}

// Encode renders e as bytes.
func Encode(e Envelope) []byte {
	out := make([]byte, 5+len(e.Payload))
	out[0] = e.Version
	binary.BigEndian.PutUint32(out[1:5], uint32(len(e.Payload)))
	copy(out[5:], e.Payload)
	return out
}

// Decode parses an envelope, rejecting one whose payload is short.
func Decode(b []byte) (Envelope, error) {
	if len(b) < 5 {
		return Envelope{}, ErrTruncated
	}
	n := binary.BigEndian.Uint32(b[1:5])
	if uint32(len(b)-5) < n {
		return Envelope{}, ErrTruncated
	}
	return Envelope{Version: b[0], Payload: b[5 : 5+n]}, nil
}
GO

cat >"$_demo_root/libs/protocol/protocol_test.go" <<'GO'
package protocol

import (
	"bytes"
	"errors"
	"testing"
)

func TestEncodeDecodeRoundTrip(t *testing.T) {
	in := Envelope{Version: 2, Payload: []byte("ledger.credit")}
	got, err := Decode(Encode(in))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if got.Version != in.Version || !bytes.Equal(got.Payload, in.Payload) {
		t.Fatalf("round trip = %+v, want %+v", got, in)
	}
}

func TestDecodeRejectsTruncatedHeader(t *testing.T) {
	if _, err := Decode([]byte{2, 0, 0}); !errors.Is(err, ErrTruncated) {
		t.Fatalf("err = %v, want ErrTruncated", err)
	}
}

func TestDecodeRejectsShortPayload(t *testing.T) {
	b := Encode(Envelope{Version: 1, Payload: []byte("abcdef")})
	if _, err := Decode(b[:len(b)-2]); !errors.Is(err, ErrTruncated) {
		t.Fatalf("err = %v, want ErrTruncated", err)
	}
}

func TestEncodeEmptyPayload(t *testing.T) {
	got, err := Decode(Encode(Envelope{Version: 1}))
	if err != nil || len(got.Payload) != 0 {
		t.Fatalf("Decode(empty) = %+v, %v", got, err)
	}
}
GO

cat >"$_demo_root/libs/authkit/authkit.go" <<'GO'
// Package authkit parses and validates the bearer tokens acme services accept.
package authkit

import (
	"errors"
	"strings"
	"time"
)

// ErrMalformed reports a token that is not "subject.expiry".
var ErrMalformed = errors.New("authkit: malformed token")

// Token is a parsed bearer token.
type Token struct {
	Subject string
	Expires time.Time
}

// Parse reads a token of the form "subject.unix-expiry".
func Parse(raw string) (Token, error) {
	sub, exp, ok := strings.Cut(raw, ".")
	if !ok || sub == "" {
		return Token{}, ErrMalformed
	}
	var secs int64
	for _, r := range exp {
		if r < '0' || r > '9' {
			return Token{}, ErrMalformed
		}
		secs = secs*10 + int64(r-'0')
	}
	return Token{Subject: sub, Expires: time.Unix(secs, 0)}, nil
}

// Valid reports whether tok has not expired as of now.
func Valid(tok Token, now time.Time) bool { return now.Before(tok.Expires) }
GO

cat >"$_demo_root/libs/authkit/authkit_test.go" <<'GO'
package authkit

import (
	"errors"
	"testing"
	"time"
)

func TestParseSubjectAndExpiry(t *testing.T) {
	tok, err := Parse("svc-ledger.1900000000")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if tok.Subject != "svc-ledger" {
		t.Fatalf("Subject = %q", tok.Subject)
	}
}

func TestParseRejectsMissingExpiry(t *testing.T) {
	if _, err := Parse("svc-ledger"); !errors.Is(err, ErrMalformed) {
		t.Fatalf("err = %v, want ErrMalformed", err)
	}
}

func TestParseRejectsNonNumericExpiry(t *testing.T) {
	if _, err := Parse("svc-ledger.soon"); !errors.Is(err, ErrMalformed) {
		t.Fatalf("err = %v, want ErrMalformed", err)
	}
}

func TestValidIsFalseAtExpiry(t *testing.T) {
	at := time.Unix(1_900_000_000, 0)
	if Valid(Token{Expires: at}, at) {
		t.Fatal("Valid at the expiry instant = true, want false")
	}
}
GO

cat >"$_demo_root/services/ledger/ledger.go" <<'GO'
// Package ledger applies debits and credits to an account balance.
package ledger

import "errors"

// ErrInsufficient reports a debit larger than the available balance.
var ErrInsufficient = errors.New("ledger: insufficient funds")

// Account holds a balance in minor units (cents).
type Account struct {
	ID      string
	Balance int64
}

// Debit subtracts amount, refusing to overdraw.
func Debit(a Account, amount int64) (Account, error) {
	if amount > a.Balance {
		return a, ErrInsufficient
	}
	a.Balance -= amount
	return a, nil
}

// Credit adds amount.
func Credit(a Account, amount int64) Account {
	a.Balance += amount
	return a
}
GO

cat >"$_demo_root/services/ledger/ledger_test.go" <<'GO'
package ledger

import (
	"errors"
	"testing"
)

func TestDebitReducesBalance(t *testing.T) {
	got, err := Debit(Account{ID: "acct-1", Balance: 5_000}, 1_250)
	if err != nil {
		t.Fatalf("Debit: %v", err)
	}
	if got.Balance != 3_750 {
		t.Fatalf("Balance = %d, want 3750", got.Balance)
	}
}

func TestDebitRefusesOverdraft(t *testing.T) {
	if _, err := Debit(Account{Balance: 100}, 101); !errors.Is(err, ErrInsufficient) {
		t.Fatalf("err = %v, want ErrInsufficient", err)
	}
}

func TestDebitExactBalanceSucceeds(t *testing.T) {
	got, err := Debit(Account{Balance: 100}, 100)
	if err != nil || got.Balance != 0 {
		t.Fatalf("Debit(exact) = %+v, %v", got, err)
	}
}

func TestCreditAccumulates(t *testing.T) {
	a := Credit(Credit(Account{}, 250), 750)
	if a.Balance != 1_000 {
		t.Fatalf("Balance = %d, want 1000", a.Balance)
	}
}
GO

cat >"$_demo_root/apps/admin/main.go" <<'GO'
// Command admin is the acme back-office entry point.
package main

import (
	"flag"
	"fmt"
)

func main() {
	addr := flag.String("addr", ":8080", "listen address")
	flag.Parse()
	fmt.Printf("admin listening on %s\n", *addr)
}
GO

cat >"$_demo_root/apps/admin/main_test.go" <<'GO'
package main

import (
	"flag"
	"testing"
)

func TestAddrFlagDefaults(t *testing.T) {
	fs := flag.NewFlagSet("admin", flag.ContinueOnError)
	addr := fs.String("addr", ":8080", "listen address")
	if err := fs.Parse(nil); err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if *addr != ":8080" {
		t.Fatalf("addr = %q, want :8080", *addr)
	}
}

func TestAddrFlagOverride(t *testing.T) {
	fs := flag.NewFlagSet("admin", flag.ContinueOnError)
	addr := fs.String("addr", ":8080", "listen address")
	if err := fs.Parse([]string{"-addr", ":9900"}); err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if *addr != ":9900" {
		t.Fatalf("addr = %q, want :9900", *addr)
	}
}
GO

# A repo, because affected tracking and the drift gates read VCS state. Paths
# are named explicitly rather than `git add -A`: the magus guard denies the
# sweep form, and this way the commit is exactly the fixture.
git -C "$_demo_root" init -q .
git -C "$_demo_root" add -- magus.yaml magusfile.buzz libs apps
git -C "$_demo_root" \
	-c user.email=demo@example.com \
	-c user.name=magus \
	commit -qm "demo workspace"

cd "$_demo_root" || return 1

# A short, stable prompt. The recording is 900px wide and a real prompt would
# spend most of it on a path nobody can read anyway.
PS1='$ '
export PS1

unset _demo_p _demo_projects _demo_root _demo_repo
