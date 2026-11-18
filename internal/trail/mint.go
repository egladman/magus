package trail

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

// AppendMint records one mint under base as a token_lifecycle event whose request blob is the
// MintRecord and whose preview reads "token 3fa9c1d2 (laptop) console=write until 2026-12-22".
// action names the door ("cli.mint", "cli.generate", "share.mint", "link.code",
// "link.redeem"). Best-effort,
// like every Append; the minting credential of a server request is also stamped on the event's
// origin from ctx.
func AppendMint(ctx context.Context, base, action string, rec MintRecord) {
	if base == "" {
		return
	}
	blob, err := codec.Marshal(rec)
	if err != nil {
		return
	}
	request := WriteBlob(ctx, base, "tok", blob)
	preview := fmt.Sprintf("%s %s", rec.Minted.Phrase(), rec.Minted.Grant.String())
	if !rec.Expires.IsZero() {
		preview += " until " + rec.Expires.UTC().Format("2006-01-02 15:04")
	}
	Append(ctx, base, journal.Call{
		Time:    time.Now(),
		Kind:    journal.CallKindTokenLifecycle,
		Name:    action,
		Outcome: journal.OutcomeOK,
		Request: request,
		Preview: preview,
	})
}
