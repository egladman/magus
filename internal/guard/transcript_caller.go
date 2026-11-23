package guard

import (
	"bufio"
	"cmp"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/egladman/magus/libs/codec"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
)

// withEnvelopeCaller reconciles explicit hook flags with the host envelope.
// A disagreement cannot certify a main thread or attach a transcript's sample.
func (who hookAttribution) withEnvelopeCaller(env hookAttribution, readUsage bool) hookAttribution {
	conflicts := func(a, b string) bool { return a != "" && b != "" && a != b }
	who.conflictingCaller = conflicts(who.Session, env.Session) || conflicts(who.Agent, env.Agent) || conflicts(who.Transcript, env.Transcript)
	who.Session = cmp.Or(who.Session, env.Session)
	who.Agent = cmp.Or(who.Agent, env.Agent)
	who.Transcript = cmp.Or(who.Transcript, env.Transcript)
	if who.conflictingCaller {
		return who
	}
	return who.withTranscriptCaller(readUsage)
}

// transcriptCaller joins the hook's session and explicit agent, when supplied,
// to the owner the host recorded in its transcript. The lease does not take
// part in this join.
func transcriptCaller(who hookAttribution, usage *types.ContextWindow) (types.AgentThread, string) {
	if who.Session == "" || !filepath.IsAbs(who.Transcript) {
		return types.AgentThreadUnknown, ""
	}
	if !fs.ValidPath(who.Host) || strings.Contains(who.Host, "/") || who.Host == "." {
		return types.AgentThreadUnknown, ""
	}
	var profile struct {
		Format      string `yaml:"format"`
		HomeEnv     string `yaml:"home_env"`
		DefaultHome string `yaml:"default_home"`
		Store       string `yaml:"store"`
	}
	declaration, err := fs.ReadFile(spells.Shipped(), path.Join("harness", who.Host, "transcript.yaml"))
	if err != nil || yaml.Unmarshal(declaration, &profile) != nil {
		return types.AgentThreadUnknown, ""
	}
	if profile.HomeEnv == "" || !filepath.IsLocal(profile.DefaultHome) || profile.DefaultHome == "." || !filepath.IsLocal(profile.Store) || profile.Store == "." {
		return types.AgentThreadUnknown, ""
	}
	home := hostHome(profile.HomeEnv, profile.DefaultHome)
	if !filepath.IsAbs(home) {
		return types.AgentThreadUnknown, ""
	}
	root, err := filepath.EvalSymlinks(filepath.Join(home, profile.Store))
	if err != nil {
		return types.AgentThreadUnknown, ""
	}
	path, err := filepath.EvalSymlinks(who.Transcript)
	if err != nil || !strings.HasPrefix(path, root+string(filepath.Separator)) {
		return types.AgentThreadUnknown, ""
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return types.AgentThreadUnknown, ""
	}
	f, err := os.Open(path)
	if err != nil {
		return types.AgentThreadUnknown, ""
	}
	defer f.Close()
	if opened, err := f.Stat(); err != nil || !os.SameFile(info, opened) {
		return types.AgentThreadUnknown, ""
	}
	switch profile.Format {
	case "messages":
		return messageTranscriptCaller(f, who, root)
	case "rollout":
		return rolloutTranscriptCaller(f, who, usage)
	}
	return types.AgentThreadUnknown, ""
}

func hostHome(env, dirname string) string {
	if home := os.Getenv(env); home != "" {
		return home
	}
	dir, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, dirname)
}

func messageTranscriptCaller(f *os.File, who hookAttribution, root string) (types.AgentThread, string) {
	if who.Agent != "" || filepath.Base(f.Name()) != who.Session+".jsonl" || filepath.Dir(filepath.Dir(f.Name())) != root {
		return types.AgentThreadUnknown, ""
	}
	scan := bufio.NewScanner(io.LimitReader(f, agentUsageTail))
	scan.Buffer(nil, agentUsageTail)
	for scan.Scan() {
		var row transcriptUsage
		if codec.Unmarshal(scan.Bytes(), &row) == nil && (row.Type == "user" || row.Type == "assistant") && row.SessionID == who.Session && row.IsSidechain != nil && !*row.IsSidechain {
			return types.AgentThreadMain, row.SessionID
		}
	}
	return types.AgentThreadUnknown, ""
}

func rolloutTranscriptCaller(f *os.File, who hookAttribution, usage *types.ContextWindow) (types.AgentThread, string) {
	line, err := bufio.NewReader(io.LimitReader(f, agentUsageTail)).ReadBytes('\n')
	if err != nil {
		return types.AgentThreadUnknown, ""
	}
	var row struct {
		Type    string `json:"type"`
		Payload struct {
			ID     string           `json:"id"`
			Thread string           `json:"thread_source"`
			Source codec.RawMessage `json:"source"`
		} `json:"payload"`
	}
	if codec.Unmarshal(line, &row) != nil || row.Type != "session_meta" || row.Payload.ID == "" {
		return types.AgentThreadUnknown, ""
	}
	switch row.Payload.Thread {
	case "user":
		var source string
		if who.Agent == "" && row.Payload.ID == who.Session && codec.Unmarshal(row.Payload.Source, &source) == nil && source != "" {
			return types.AgentThreadMain, row.Payload.ID
		}
	case "subagent":
		var source struct {
			Subagent struct {
				Spawn struct {
					Parent string `json:"parent_thread_id"`
				} `json:"thread_spawn"`
			} `json:"subagent"`
		}
		if codec.Unmarshal(row.Payload.Source, &source) != nil {
			break
		}
		parent := source.Subagent.Spawn.Parent
		id := row.Payload.ID
		if parent != "" && parent != id && (who.Session == parent || who.Session == id) && (who.Agent == "" || who.Agent == id) {
			// This open file identified the sample's owner.
			if usage != nil {
				*usage, _ = lastContextUsageIn(f)
			}
			return types.AgentThreadSub, id
		}
	}
	return types.AgentThreadUnknown, ""
}
