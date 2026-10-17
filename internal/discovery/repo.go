package discovery

import (
	"bufio"
	"bytes"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

// loadRepoURLs returns the normalized URLs under which the repository at
// repoRoot is known: its git remotes plus any configured aliases. GitOps CRDs
// whose source URL matches one of these refer to the local working tree.
func loadRepoURLs(repoRoot string, aliases []string) map[string]bool {
	urls := make(map[string]bool)
	for _, a := range aliases {
		if n := normalizeGitURL(a); n != "" {
			urls[n] = true
		}
	}

	cfgPath, err := gitConfigPath(repoRoot)
	if err != nil {
		slog.Debug("could not locate git config, using repo aliases only", "repo", repoRoot, "err", err)
		return urls
	}
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		slog.Debug("could not read git config, using repo aliases only", "file", cfgPath, "err", err)
		return urls
	}
	for _, u := range parseRemoteURLs(data) {
		if n := normalizeGitURL(u); n != "" {
			urls[n] = true
		}
	}
	return urls
}

// gitConfigPath returns the config file of the repository at repoRoot. In
// worktrees and submodules .git is a file pointing at the git dir, and linked
// worktrees keep the shared config in the directory named by commondir.
func gitConfigPath(repoRoot string) (string, error) {
	gitDir := filepath.Join(repoRoot, ".git")
	info, err := os.Stat(gitDir)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		data, err := os.ReadFile(gitDir)
		if err != nil {
			return "", err
		}
		target, ok := strings.CutPrefix(strings.TrimSpace(string(data)), "gitdir:")
		if !ok {
			return "", fmt.Errorf("%s: missing gitdir line", gitDir)
		}
		gitDir = resolveFrom(repoRoot, strings.TrimSpace(target))
	}
	if data, err := os.ReadFile(filepath.Join(gitDir, "commondir")); err == nil {
		gitDir = resolveFrom(gitDir, strings.TrimSpace(string(data)))
	}
	return filepath.Join(gitDir, "config"), nil
}

func resolveFrom(base, path string) string {
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	return filepath.Join(base, path)
}

// parseRemoteURLs extracts the remote.<name>.url values from git config
// content. include directives are not followed.
func parseRemoteURLs(data []byte) []string {
	var urls []string
	inRemote := false
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || line[0] == '#' || line[0] == ';' {
			continue
		}
		if line[0] == '[' {
			// Section names are case-insensitive: [remote "origin"].
			inRemote = strings.HasPrefix(strings.ToLower(line), `[remote "`)
			continue
		}
		if !inRemote {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok || !strings.EqualFold(strings.TrimSpace(key), "url") {
			continue
		}
		if v := configValue(value); v != "" {
			urls = append(urls, v)
		}
	}
	return urls
}

// configValue strips quotes and trailing comments from a git config value.
func configValue(raw string) string {
	v := strings.TrimSpace(raw)
	if strings.HasPrefix(v, `"`) {
		if end := strings.Index(v[1:], `"`); end >= 0 {
			return v[1 : end+1]
		}
		return strings.TrimPrefix(v, `"`)
	}
	if i := strings.IndexAny(v, "#;"); i >= 0 {
		v = strings.TrimSpace(v[:i])
	}
	return v
}

// normalizeGitURL reduces a git URL to a lowercase "host/path" form so that
// the https, ssh and scp-like spellings of the same repository compare equal.
// Returns "" for values that are not URLs (e.g. local paths).
func normalizeGitURL(raw string) string {
	u := strings.TrimSpace(raw)
	if i := strings.Index(u, "://"); i >= 0 {
		u = u[i+3:]
	} else if i := strings.Index(u, ":"); i > 1 && !strings.Contains(u[:i], "/") {
		// scp-like syntax: [user@]host:path. i > 1 excludes Windows drive letters.
		u = u[:i] + "/" + u[i+1:]
	} else {
		return ""
	}

	host, path, _ := strings.Cut(u, "/")
	if at := strings.LastIndex(host, "@"); at >= 0 {
		host = host[at+1:]
	}
	// Ports differ between ssh and https endpoints of the same server.
	if colon := strings.LastIndex(host, ":"); colon >= 0 {
		host = host[:colon]
	}
	path = strings.Trim(path, "/")
	path = strings.TrimSuffix(path, ".git")
	path = strings.TrimSuffix(path, "/")
	if host == "" || path == "" {
		return ""
	}
	return strings.ToLower(host + "/" + path)
}
