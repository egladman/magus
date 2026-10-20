package main

import (
	"errors"
	"fmt"
	"io"
	"strings"
)

// The files of a round directory. Labelers fill copies of the template as
// labels-a.jsonl and labels-b.jsonl; the adjudicator writes adjudicated.jsonl.
const (
	candidatesFile  = "candidates.jsonl"
	templateFile    = "labels-template.jsonl"
	countsFile      = "counts.json"
	freezeFile      = "freeze.json"
	guidelineFile   = "guideline.md"
	labelsAFile     = "labels-a.jsonl"
	labelsBFile     = "labels-b.jsonl"
	adjudicatedFile = "adjudicated.jsonl"
	scoresFile      = "scores.json"
)

// rulePathspecs are the files whose change can change what a rule fires on:
// the proofread package's sources, less its tests, the case replay and the
// round math, which score no text.
var rulePathspecs = []string{
	":(top,glob)libs/proofread/*.go",
	":(top,exclude,glob)libs/proofread/*_test.go",
	":(top,exclude)libs/proofread/calibrate.go",
	":(top,exclude)libs/proofread/round.go",
}

// runRound runs `proofread-train calibrate round sample|freeze|score|pool`.
func runRound(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		return failure(stderr, errors.New("round needs sample, freeze, score or pool"))
	}

	switch args[0] {
	case "sample":
		return runRoundSample(args[1:], stdout, stderr)
	case "freeze":
		return runRoundFreeze(args[1:], stdout, stderr)
	case "score":
		return runRoundScore(args[1:], stdout, stderr)
	case "pool":
		return runRoundPool(args[1:], stdout, stderr)
	default:
		return failure(stderr, fmt.Errorf("unknown round subcommand %q: want sample, freeze, score or pool", args[0]))
	}
}

// candidate is one firing a labeler judges. Sentence and Context are what a
// labeler reads first; Text is the whole judged text, so no call waits on
// context the packet cut.
type candidate struct {
	ID     string `json:"id"`
	Rule   string `json:"rule"`
	Code   string `json:"code"`
	Kind   string `json:"kind"`
	Source string `json:"source"`
	// Record is the 1-based ordinal of the record in its source file.
	Record  int    `json:"record"`
	Match   string `json:"match"`
	Message string `json:"message"`
	// Line, Column, EndLine and EndColumn place Match in Text as the finding
	// reported them, 0 when it did not.
	Line      int    `json:"line,omitempty"`
	Column    int    `json:"column,omitempty"`
	EndLine   int    `json:"end_line,omitempty"`
	EndColumn int    `json:"end_column,omitempty"`
	Sentence  string `json:"sentence"`
	Context   string `json:"context"`
	// Scope is sentence for a located match, paragraph for a finding about the
	// paragraph a line opens, and text for one about the whole text.
	Scope string `json:"scope"`
	Text  string `json:"text"`
}

type labelRow struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Note  string `json:"note"`
}

type adjudicatedRow struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Reason      string `json:"reason"`
	Adjudicated *bool  `json:"adjudicated,omitempty"`
}

type sourceSpec struct {
	Name   string `json:"name"`
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	// Records counts the records with text, Texts those left after dropping
	// a text the source already held.
	Records    int `json:"records"`
	Texts      int `json:"texts"`
	Duplicates int `json:"duplicates"`
}

type excludeSpec struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256,omitempty"`
	// Strings counts the texts, sentences and contexts it contributed.
	Strings int `json:"strings"`
}

// roundCounts is counts.json: how the sampler drew a round, enough to draw
// it again from the same sources.
type roundCounts struct {
	Round            int                   `json:"round"`
	Seed             int64                 `json:"seed"`
	TotalCandidates  int                   `json:"total_candidates"`
	ToolCommit       string                `json:"tool_commit"`
	RuleFilesDirty   bool                  `json:"rule_files_dirty"`
	ProofreadVersion string                `json:"proofread_version"`
	Rules            []string              `json:"sampled_rules"`
	PerRule          int                   `json:"per_rule"`
	MinPerSource     int                   `json:"min_per_source"`
	MaxPerText       int                   `json:"max_per_text"`
	Field            []string              `json:"field"`
	Sources          []sourceSpec          `json:"sources"`
	Exclude          []excludeSpec         `json:"exclude"`
	TextsScanned     map[string]int        `json:"texts_scanned"`
	Notes            map[string]string     `json:"notes"`
	ByRule           map[string]*ruleCount `json:"rules"`
}

type ruleCount struct {
	Code       string `json:"code"`
	Candidates int    `json:"candidates"`
	Short      bool   `json:"fewer_than_per_rule"`
	// Seen counts every firing; CappedPerText those past -max-per-text in one
	// text; Excluded those whose text, sentence or context an exclude file
	// holds; Duplicates those repeating a fresh firing's sentence or context;
	// Fresh what remains to draw from.
	Seen                int               `json:"firings_seen_all_sources"`
	CappedPerText       int               `json:"capped_per_text"`
	Excluded            int               `json:"excluded_already_used"`
	Duplicates          int               `json:"duplicates"`
	Fresh               int               `json:"fresh_unique_in_pool"`
	BySourceSeen        map[string]int    `json:"by_source_firings"`
	BySourceExcluded    map[string]int    `json:"by_source_excluded"`
	BySourceFresh       map[string]int    `json:"by_source_fresh_unique"`
	BySourceTaken       map[string]int    `json:"by_source_selected"`
	Kinds               []string          `json:"kinds"`
	KindsWithoutASource []string          `json:"kinds_without_a_source"`
	ShippedDecisions    map[string]string `json:"shipped_decisions"`
	Decisions           map[string]string `json:"decisions"`
}

type repeated []string

func (r *repeated) String() string { return strings.Join(*r, ",") }

func (r *repeated) Set(v string) error {
	*r = append(*r, v)

	return nil
}
