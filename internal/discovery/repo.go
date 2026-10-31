package discovery

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
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

// errOutsideRepo is returned by joinInRepo for paths that leave the repository.
var errOutsideRepo = errors.New("resolves outside the repository root")

// evalExisting resolves symlinks in p. When p does not exist it resolves the
// nearest existing ancestor (stopping at root) instead, so a symlinked parent
// is still caught. It returns "" when nothing up to root exists. A dangling
// symlink exists for Lstat but fails to resolve, so it is an error.
func evalExisting(root, p string) (string, error) {
	r, err := filepath.EvalSymlinks(p)
	if err == nil {
		return r, nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return "", err
	}
	for q := p; ; q = filepath.Dir(q) {
		if _, lerr := os.Lstat(q); lerr == nil {
			return filepath.EvalSymlinks(q)
		} else if !errors.Is(lerr, fs.ErrNotExist) {
			return "", lerr
		}
		if q == root || filepath.Dir(q) == q {
			return "", nil
		}
	}
}

// evalSymlinksRoot resolves the repository root in repoRoot.resolve. It is a
// variable only so tests can count calls; it is used for nothing else. Tests
// that swap it must not run in parallel with anything reaching joinInRepo.
var evalSymlinksRoot = filepath.EvalSymlinks

// repoRoot is a repository root whose symlink-resolved form is computed at
// most once, however many joinInRepo calls (possibly concurrent) use it.
type repoRoot struct {
	path     string
	once     sync.Once
	resolved string
	err      error
}

// newRepoRoot returns a repoRoot for path. An empty path disables
// containment in joinInRepo.
func newRepoRoot(path string) *repoRoot {
	return &repoRoot{path: path}
}

// resolve returns the symlink-resolved root, evaluating it on first use only.
func (r *repoRoot) resolve() (string, error) {
	r.once.Do(func() {
		r.resolved, r.err = evalSymlinksRoot(r.path)
	})
	return r.resolved, r.err
}

// joinInRepo joins rel onto base and verifies the result stays inside
// root, both lexically and after resolving symlinks. It returns the
// lexical path, not the symlink-resolved one. A path that does not exist
// yet is checked through its nearest existing ancestor. An empty root.path
// disables containment. The root is symlink-resolved once per *repoRoot, and a
// failure to resolve it is cached too, failing every later existing-target join.
//
// OS errors are quoted with %q rather than wrapped, because they embed
// repository-controlled bytes (symlink targets) that reach terminal output.
func joinInRepo(root *repoRoot, base, rel string) (string, error) {
	p := filepath.Clean(filepath.Join(base, rel))
	if root.path == "" {
		return p, nil
	}
	if !within(root.path, p) {
		return "", errOutsideRepo
	}
	resolved, err := evalExisting(root.path, p)
	if err != nil {
		return "", fmt.Errorf("resolving symlinks: %q", err.Error())
	}
	if resolved == "" {
		return p, nil // nothing on disk to resolve
	}
	resolvedRoot, err := root.resolve()
	if err != nil {
		return "", fmt.Errorf("resolving repository root: %q", err.Error())
	}
	if !within(resolvedRoot, resolved) {
		return "", errOutsideRepo
	}
	return p, nil
}

// within reports whether p equals root or lies below it.
func within(root, p string) bool {
	r, err := filepath.Rel(root, p)
	if err != nil {
		return false
	}
	return r != ".." && !strings.HasPrefix(r, ".."+string(filepath.Separator))
}
