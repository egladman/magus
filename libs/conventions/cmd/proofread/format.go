package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/egladman/magus/libs/conventions/proofread"
)

const (
	// toolName names proofread as the tool behind a SARIF run and an rdjson
	// result.
	toolName = "proofread"
	// toolURL is the page that documents the rules.
	toolURL = "https://eli.gladman.cc/magus/reference/proofread/"
	// sarifSchema is the schema a SARIF 2.1.0 log names.
	sarifSchema = "https://json.schemastore.org/sarif-2.1.0.json"
	// fingerprintKey names the stable hash a SARIF result carries, which lets
	// a code scanning service tell a finding that moved from a new one.
	fingerprintKey = "proofread/v1"
)

// writeFindings writes out to w in format: json, the array of findings
// itself, sarif, a SARIF 2.1.0 log, or rdjson, reviewdog's diagnostic result.
func writeFindings(w io.Writer, format string, out []finding) error {
	var doc any

	switch format {
	case "json":
		doc = out
	case "sarif":
		doc = toSARIF(out)
	case "rdjson":
		doc = toRDJSON(out)
	default:
		return fmt.Errorf("unknown -format %q: want json, sarif or rdjson", format)
	}

	if err := json.NewEncoder(w).Encode(doc); err != nil {
		return fmt.Errorf("write findings: %w", err)
	}

	return nil
}

// splitSource separates a finding's source, "path:line" or "path:line:column",
// into its path and its first number, 0 when the source carries none.
func splitSource(source string) (path string, line int) {
	path = source

	for range 2 {
		i := strings.LastIndexByte(path, ':')
		if i < 0 {
			break
		}

		n, err := strconv.Atoi(path[i+1:])
		if err != nil || n < 0 {
			break
		}

		path, line = path[:i], n
	}

	return path, line
}

// findingPath is the file f is in: its source without the position.
func findingPath(f finding) string {
	path, _ := splitSource(f.Source)

	return path
}

// position is where f starts and ends, with a field 0 when it is not known.
// A finding with no line of its own, such as a doc comment's, starts on the
// line its source names, and has no columns.
func position(f finding) (line, column, endLine, endColumn int) {
	if f.Line > 0 {
		return f.Line, f.Column, f.EndLine, f.EndColumn
	}

	_, line = splitSource(f.Source)

	return line, 0, 0, 0
}

// sarifLevel is the SARIF level of a decision: deny fails a gate and advise
// only warns.
func sarifLevel(decision string) string {
	if decision == string(proofread.DecisionDeny) {
		return "error"
	}

	return "warning"
}

type sarifLog struct {
	Schema  string     `json:"$schema"`
	Version string     `json:"version"`
	Runs    []sarifRun `json:"runs"`
}

type sarifRun struct {
	Tool    sarifTool     `json:"tool"`
	Results []sarifResult `json:"results"`
}

type sarifTool struct {
	Driver sarifDriver `json:"driver"`
}

type sarifDriver struct {
	Name           string      `json:"name"`
	InformationURI string      `json:"informationUri"`
	Rules          []sarifRule `json:"rules"`
}

type sarifRule struct {
	ID               string         `json:"id"`
	HelpURI          string         `json:"helpUri"`
	ShortDescription sarifText      `json:"shortDescription"`
	FullDescription  *sarifText     `json:"fullDescription,omitempty"`
	Properties       map[string]any `json:"properties,omitempty"`
}

type sarifText struct {
	Text string `json:"text"`
}

type sarifResult struct {
	RuleID              string            `json:"ruleId"`
	RuleIndex           int               `json:"ruleIndex"`
	Level               string            `json:"level"`
	Message             sarifText         `json:"message"`
	Locations           []sarifLocation   `json:"locations"`
	PartialFingerprints map[string]string `json:"partialFingerprints"`
}

type sarifLocation struct {
	PhysicalLocation sarifPhysical `json:"physicalLocation"`
}

type sarifPhysical struct {
	ArtifactLocation sarifArtifact `json:"artifactLocation"`
	Region           *sarifRegion  `json:"region,omitempty"`
}

type sarifArtifact struct {
	URI string `json:"uri"`
}

type sarifRegion struct {
	StartLine   int `json:"startLine"`
	StartColumn int `json:"startColumn,omitempty"`
	EndLine     int `json:"endLine,omitempty"`
	EndColumn   int `json:"endColumn,omitempty"`
}

// toSARIF renders out as a SARIF 2.1.0 log whose driver lists every rule in
// the catalog, so a viewer can describe a rule the run found nothing for.
// Each result's partial fingerprint hashes its rule, match and path, with the
// occurrence number added to the second and later findings that share all
// three, so no two results of a log share one.
func toSARIF(out []finding) sarifLog {
	catalog := proofread.Catalog()
	rules := make([]sarifRule, len(catalog))
	index := make(map[string]int, len(catalog))

	for i, d := range catalog {
		index[string(d.Name)] = i
		rules[i] = sarifRule{
			ID: string(d.Name), HelpURI: d.URL(), ShortDescription: sarifText{Text: d.Catches},
			Properties: map[string]any{"code": string(d.Code)},
		}

		if d.Why != "" {
			rules[i].FullDescription = &sarifText{Text: d.Why}
		}
	}

	results := make([]sarifResult, 0, len(out))
	seen := map[string]int{}

	for _, f := range out {
		path := findingPath(f)
		key := f.Rule + "\x00" + f.Match + "\x00" + path
		fingerprint := key

		if n := seen[key]; n > 0 {
			fingerprint += "\x00" + strconv.Itoa(n)
		}

		seen[key]++

		sum := sha256.Sum256([]byte(fingerprint))
		physical := sarifPhysical{ArtifactLocation: sarifArtifact{URI: path}}

		if line, column, endLine, endColumn := position(f); line > 0 {
			physical.Region = &sarifRegion{StartLine: line, StartColumn: column, EndLine: endLine, EndColumn: endColumn}
		}

		results = append(results, sarifResult{
			RuleID: f.Rule, RuleIndex: index[f.Rule], Level: sarifLevel(f.Decision), Message: sarifText{Text: f.Message},
			Locations:           []sarifLocation{{PhysicalLocation: physical}},
			PartialFingerprints: map[string]string{fingerprintKey: hex.EncodeToString(sum[:16])},
		})
	}

	return sarifLog{
		Schema: sarifSchema, Version: "2.1.0",
		Runs: []sarifRun{{
			Tool:    sarifTool{Driver: sarifDriver{Name: toolName, InformationURI: toolURL, Rules: rules}},
			Results: results,
		}},
	}
}

type rdResult struct {
	Source      rdSource       `json:"source"`
	Diagnostics []rdDiagnostic `json:"diagnostics"`
}

type rdSource struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

type rdDiagnostic struct {
	Message     string         `json:"message"`
	Location    rdLocation     `json:"location"`
	Severity    string         `json:"severity"`
	Code        rdCode         `json:"code"`
	Suggestions []rdSuggestion `json:"suggestions,omitempty"`
}

type rdLocation struct {
	Path  string   `json:"path"`
	Range *rdRange `json:"range,omitempty"`
}

type rdRange struct {
	Start rdPosition  `json:"start"`
	End   *rdPosition `json:"end,omitempty"`
}

type rdPosition struct {
	Line   int `json:"line"`
	Column int `json:"column,omitempty"`
}

type rdCode struct {
	Value string `json:"value"`
	URL   string `json:"url"`
}

type rdSuggestion struct {
	Range rdRange `json:"range"`
	Text  string  `json:"text"`
}

// toRDJSON renders out as a reviewdog Diagnostic Result. A finding that has a
// replacement and a full span carries one suggestion per replacement, since a
// suggestion without a range cannot be applied.
func toRDJSON(out []finding) rdResult {
	diagnostics := make([]rdDiagnostic, 0, len(out))

	for _, f := range out {
		d := rdDiagnostic{
			Message:  f.Message,
			Location: rdLocation{Path: findingPath(f)},
			Severity: "WARNING",
			Code:     rdCode{Value: f.Code, URL: f.URL},
		}

		if f.Decision == string(proofread.DecisionDeny) {
			d.Severity = "ERROR"
		}

		line, column, endLine, endColumn := position(f)
		if line > 0 {
			r := rdRange{Start: rdPosition{Line: line, Column: column}}
			if endLine > 0 {
				r.End = &rdPosition{Line: endLine, Column: endColumn}
			}

			d.Location.Range = &r

			if column > 0 && endLine > 0 {
				for _, text := range f.Replacements {
					d.Suggestions = append(d.Suggestions, rdSuggestion{Range: r, Text: text})
				}
			}
		}

		diagnostics = append(diagnostics, d)
	}

	return rdResult{Source: rdSource{Name: toolName, URL: toolURL}, Diagnostics: diagnostics}
}
