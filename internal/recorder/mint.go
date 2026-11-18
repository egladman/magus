package recorder

import (
	"context"
	"fmt"
	"time"

	"github.com/egladman/magus/internal/auth"
	"github.com/egladman/magus/libs/codec"
	"github.com/egladman/magus/libs/safetyscissors/journal"
)

// MintRecord is what the trail keeps about a minted token: which token, until when, and the
// grant it was minted under. Never the secret: the credential carries an 8-hex id and no hash.
// Expires is zero for the operator token, which does not expire.
type MintRecord struct {
	Minted  auth.Credential `json:"minted"`
	Expires time.Time       `json:"expires,omitzero"`
	Minter  auth.Credential `json:"minter"`
}

// AppendMint records one mint under base as a token_lifecycle call whose
// request payload is rec and whose preview reads like "token 3fa9c1d2
// (laptop) console=write until 2026-12-22". name is the door: "cli.mint",
// "share.mint", "link.code" and the rest. It is best-effort, as
// [AppendCall] is.
func AppendMint(ctx context.Context, base, name string, rec MintRecord) {
	if base == "" {
		return
	}
	blob, err := codec.Marshal(rec)
	if err != nil {
		return
	}
	request := WritePayload(ctx, base, "tok", blob)
	preview := fmt.Sprintf("%s %s", rec.Minted.Phrase(), rec.Minted.Grant.String())
	if !rec.Expires.IsZero() {
		preview += " until " + rec.Expires.UTC().Format("2006-01-02 15:04")
	}
	AppendCall(ctx, base, journal.Call{
		Time:    time.Now(),
		Kind:    journal.CallKindTokenLifecycle,
		Name:    name,
		Outcome: journal.OutcomeOK,
		Request: request,
		Preview: preview,
	})
}
