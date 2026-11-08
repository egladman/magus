package agent

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/types"
)

const (
	installedPluginsFile  = "installed_plugins.json"
	knownMarketplacesFile = "known_marketplaces.json"
	// installedPluginsVersion is the only record shape read. The host
	// migrates an older file in place when it next starts.
	installedPluginsVersion = 2
)

// readJSONFile decodes path into dst. An absent file leaves dst as it is.
func readJSONFile(path string, dst any) error {
	body, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	if err := decodeHarnessJSON(body, dst); err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}
	return nil
}

// pluginInstall is one entry of the host's install record.
type pluginInstall struct {
	Scope        string `json:"scope"`
	ProjectPath  string `json:"projectPath"`
	GitCommitSha string `json:"gitCommitSha"`
}

// pluginScopes ranks the record's scopes from most to least specific.
var pluginScopes = []string{"local", "project", "user", "managed"}

// recordedPluginInstall returns the install of id that reaches root.
// A project or local install reaches only the checkout it names.
// An absent record file means the person installed nothing.
func recordedPluginInstall(root, id string) (pluginInstall, bool, error) {
	path := filepath.Join(claudePluginsRoot(), installedPluginsFile)
	var record struct {
		Version int                        `json:"version"`
		Plugins map[string]json.RawMessage `json:"plugins"`
	}
	if err := readJSONFile(path, &record); err != nil {
		return pluginInstall{}, false, err
	}
	if record.Plugins == nil {
		return pluginInstall{}, false, nil
	}
	if record.Version != installedPluginsVersion {
		return pluginInstall{}, false, fmt.Errorf("%s is version %d, and magus reads only version %d, which the host writes when it next starts",
			path, record.Version, installedPluginsVersion)
	}
	raw, ok := record.Plugins[id]
	if !ok {
		return pluginInstall{}, false, nil
	}
	var installs []pluginInstall
	if err := decodeHarnessJSON(raw, &installs); err != nil {
		return pluginInstall{}, false, fmt.Errorf("parse %s entry %s: %w", path, id, err)
	}
	for _, in := range installs {
		if !slices.Contains(pluginScopes, in.Scope) {
			return pluginInstall{}, false, fmt.Errorf("%s records %s in scope %q, which magus does not know", path, id, in.Scope)
		}
	}
	for _, scope := range pluginScopes {
		for _, in := range installs {
			if in.Scope != scope {
				continue
			}
			if (scope == "local" || scope == "project") && !sameDir(in.ProjectPath, root) {
				continue
			}
			return in, true, nil
		}
	}
	return pluginInstall{}, false, nil
}

func sameDir(a, b string) bool {
	resolve := func(p string) string {
		if real, err := filepath.EvalSymlinks(p); err == nil {
			return real
		}
		return filepath.Clean(p)
	}
	return a != "" && resolve(a) == resolve(b)
}

// pluginTurnedOff names the local settings file beside the enabling
// one when it turns id off. Only local settings outrank the workspace's.
func pluginTurnedOff(root string, enabled types.Setting, id string) (string, error) {
	local := strings.TrimSuffix(enabled.Path, ".json") + ".local.json"
	var settings struct {
		EnabledPlugins map[string]any `json:"enabledPlugins"`
	}
	if err := readJSONFile(filepath.Join(root, local), &settings); err != nil {
		return "", err
	}
	if v, ok := settings.EnabledPlugins[id]; ok && v == false {
		return local, nil
	}
	return "", nil
}

type knownMarketplace struct {
	InstallLocation string `json:"installLocation"`
	LastUpdated     string `json:"lastUpdated"`
}

func knownMarketplaces() (map[string]knownMarketplace, error) {
	known := map[string]knownMarketplace{}
	err := readJSONFile(filepath.Join(claudePluginsRoot(), knownMarketplacesFile), &known)
	return known, err
}

// pluginCurrency compares the recorded commit with the marketplace
// clone's HEAD. That holds only for a plugin at a path inside the
// clone; any other source is unknown.
func pluginCurrency(id string, install pluginInstall) HarnessCheck {
	name, marketplace, _ := strings.Cut(id, "@")
	update := pluginCommand("update", id)
	unknown := func(why string) HarnessCheck {
		return HarnessCheck{Status: HarnessUnprobed, Reason: fmt.Sprintf(
			"%s is installed and enabled, but whether it is current is unknown: %s; %s fetches the newest", id, why, update)}
	}
	if install.GitCommitSha == "" {
		return unknown(fmt.Sprintf("%s records no commit for it", filepath.Join(claudePluginsRoot(), installedPluginsFile)))
	}
	known, err := knownMarketplaces()
	if err != nil {
		return unknown(err.Error())
	}
	clone := known[marketplace]
	if clone.InstallLocation == "" {
		return unknown(fmt.Sprintf("%s records no clone of marketplace %s", knownMarketplacesFile, marketplace))
	}
	relative, err := marketplacePluginIsRelative(clone.InstallLocation, name)
	if err != nil {
		return unknown(err.Error())
	}
	if !relative {
		return unknown(fmt.Sprintf("%s comes from a source of its own, not from the clone of marketplace %s", name, marketplace))
	}
	head, err := gitHead(clone.InstallLocation)
	if err != nil {
		return unknown(err.Error())
	}
	if !sameCommit(head, install.GitCommitSha) {
		return HarnessCheck{Status: HarnessPluginBehind, Reason: fmt.Sprintf(
			"%s is installed at %s, but marketplace %s is at %s (fetched %s); run: %s",
			id, shortCommit(install.GitCommitSha), marketplace, shortCommit(head), clone.LastUpdated, update)}
	}
	return HarnessCheck{Status: HarnessVerified, Reason: fmt.Sprintf(
		"at %s, the commit of marketplace %s as last fetched (%s)", shortCommit(head), marketplace, clone.LastUpdated)}
}

// marketplacePluginIsRelative reports whether the clone's catalog
// gives name a path inside the clone. Only then is the recorded
// commit the clone's.
func marketplacePluginIsRelative(clone, name string) (bool, error) {
	path := filepath.Join(clone, filepath.FromSlash(".claude-plugin/marketplace.json"))
	var catalog struct {
		Plugins []struct {
			Name   string          `json:"name"`
			Source json.RawMessage `json:"source"`
		} `json:"plugins"`
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return false, fmt.Errorf("read %s: %w", path, err)
	}
	if err := decodeHarnessJSON(body, &catalog); err != nil {
		return false, fmt.Errorf("parse %s: %w", path, err)
	}
	for _, p := range catalog.Plugins {
		if p.Name == name {
			return bytes.HasPrefix(bytes.TrimSpace(p.Source), []byte(`"`)), nil
		}
	}
	return false, fmt.Errorf("%s lists no plugin %s", path, name)
}

// gitHead reads the commit a clone has checked out from its .git
// files, so no git binary is needed.
func gitHead(clone string) (string, error) {
	gitDir := filepath.Join(clone, ".git")
	head, err := os.ReadFile(filepath.Join(gitDir, "HEAD"))
	if err != nil {
		return "", fmt.Errorf("read the HEAD of %s: %w", clone, err)
	}
	ref, symbolic := strings.CutPrefix(strings.TrimSpace(string(head)), "ref: ")
	if !symbolic {
		return ref, nil
	}
	loose, err := os.ReadFile(filepath.Join(gitDir, filepath.FromSlash(ref)))
	if err == nil {
		return strings.TrimSpace(string(loose)), nil
	}
	if !os.IsNotExist(err) {
		return "", fmt.Errorf("read %s of %s: %w", ref, clone, err)
	}
	packed, err := os.ReadFile(filepath.Join(gitDir, "packed-refs"))
	if err != nil {
		return "", fmt.Errorf("resolve %s of %s: %w", ref, clone, err)
	}
	for line := range strings.Lines(string(packed)) {
		if commit, name, ok := strings.Cut(strings.TrimSpace(line), " "); ok && name == ref {
			return commit, nil
		}
	}
	return "", fmt.Errorf("%s of %s names no commit", ref, clone)
}

// sameCommit accepts an abbreviated commit on either side.
func sameCommit(a, b string) bool {
	const minAbbrev = 7
	if len(a) < minAbbrev || len(b) < minAbbrev {
		return false
	}
	return strings.HasPrefix(a, b) || strings.HasPrefix(b, a)
}

func shortCommit(c string) string {
	return c[:min(len(c), 12)]
}
