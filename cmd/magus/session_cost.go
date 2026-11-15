package main

import (
	"io"

	"github.com/egladman/magus/internal/agent/model"
	"github.com/egladman/magus/internal/trail"
)

// compat(until: no cmd/magus file calls summarizeCost, renderCost or sessionCost): wrappers for session.go.
type sessionCost = trail.SessionCost

func summarizeCost(tr *trail.SessionTrail, table model.Table) *sessionCost {
	return trail.SummarizeCost(tr, table)
}

func renderCost(w io.Writer, c *sessionCost) { trail.RenderCost(w, c) }
