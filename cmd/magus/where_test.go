package main

import (
	"testing"

	"github.com/egladman/magus/types"

	"github.com/egladman/magus/internal/interactive"
)

func TestWhereUniqueMatch(t *testing.T) {
	all := []*types.Project{
		{Path: "api/gateway", Dir: "/tmp/api/gateway"},
		{Path: "api/auth", Dir: "/tmp/api/auth"},
		{Path: "web/dashboard", Dir: "/tmp/web/dashboard"},
	}
	scored := interactive.ScoreProjects(all, []string{"dash"})
	if len(scored) != 1 || scored[0].P.Path != "web/dashboard" {
		t.Fatalf("expected unique match web/dashboard, got %v", scored)
	}
}

func TestWhereAmbiguous(t *testing.T) {
	all := []*types.Project{
		{Path: "api/gateway", Dir: "/tmp/api/gateway"},
		{Path: "api/auth", Dir: "/tmp/api/auth"},
	}
	scored := interactive.ScoreProjects(all, []string{"api"})
	if len(scored) < 2 {
		t.Fatalf("expected ambiguous results, got %d", len(scored))
	}
}

func TestWhereNoMatch(t *testing.T) {
	all := []*types.Project{
		{Path: "api/gateway", Dir: "/tmp/api/gateway"},
	}
	scored := interactive.ScoreProjects(all, []string{"zzznope"})
	if len(scored) != 0 {
		t.Fatalf("expected no match, got %v", scored)
	}
}
