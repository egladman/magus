package types

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestSpellOpKind covers OpKind's empty-default resolution and the IsService
// discriminator across the three op shapes (default command, explicit command,
// service).
func TestSpellOpKind(t *testing.T) {
	tests := []struct {
		name     string
		op       SpellOp
		wantKind string
		wantSvc  bool
	}{
		{"empty defaults to command", SpellOp{}, OpKindCommand, false},
		{"explicit command", SpellOp{Kind: OpKindCommand}, OpKindCommand, false},
		{"service", SpellOp{Kind: OpKindService}, OpKindService, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.wantKind, tt.op.OpKind())
			assert.Equal(t, tt.wantSvc, tt.op.IsService())
		})
	}
}
