package discovery

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNormalizeGitURL(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"https://git.example.com/org/repo", "git.example.com/org/repo"},
		{"https://git.example.com/org/repo.git", "git.example.com/org/repo"},
		{"https://git.example.com/org/repo/", "git.example.com/org/repo"},
		{"https://user:token@Git.Example.com/Org/Repo.git", "git.example.com/org/repo"},
		{"ssh://git@git.example.com:2222/org/repo.git", "git.example.com/org/repo"},
		{"git@git.example.com:org/repo.git", "git.example.com/org/repo"},
		{"git+ssh://git@git.example.com/org/repo", "git.example.com/org/repo"},
		{"./charts/frontend", ""},
		{"/abs/path", ""},
		{"C:/repo", ""},
		{"https://git.example.com", ""},
		{"", ""},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.want, normalizeGitURL(tt.in), tt.in)
	}
}

const selfRefApplication = `apiVersion: argoproj.io/v1alpha1
kind: Application
metadata:
  name: platform
spec:
  source:
    repoURL: https://git.example.com/org/repo
    targetRevision: HEAD
    path: platform
  destination:
    namespace: platform
`

// newSelfRefRepo creates a repository with an Application in apps/ that points
// at the repository's own platform/ directory.
func newSelfRefRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	write := func(rel, content string) {
		p := filepath.Join(root, rel)
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, []byte(content), 0o644))
	}
	write("apps/platform.yaml", selfRefApplication)
	write("platform/top.yaml", "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: top\n")
	return root
}

func TestDiscover_ArgoCDSelfReferenceWithoutMatchIsSkipped(t *testing.T) {
	root := newSelfRefRepo(t)

	apps, _, err := Discover(context.Background(), []string{root}, defaultCfg())
	require.NoError(t, err)
	assert.Empty(t, filterByFramework(apps, FrameworkArgoCD))
}

func TestDiscover_ArgoCDSelfReferenceViaAlias(t *testing.T) {
	root := newSelfRefRepo(t)
	cfg := defaultCfg()
	cfg.Discovery.RepoAliases = []string{"git@git.example.com:org/repo.git"}

	apps, _, err := Discover(context.Background(), []string{root}, cfg)
	require.NoError(t, err)

	argoApps := filterByFramework(apps, FrameworkArgoCD)
	require.Len(t, argoApps, 1)
	assert.Equal(t, "platform", argoApps[0].Name)
	assert.Equal(t, RendererRaw, argoApps[0].Renderer)
	assert.Equal(t, filepath.Join(root, "platform"), argoApps[0].RootPath)
	assert.Equal(t, "platform", argoApps[0].Namespace)
}

func TestDiscover_ArgoCDSelfReferenceViaGitRemote(t *testing.T) {
	root := newSelfRefRepo(t)
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".git"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, ".git", "config"),
		[]byte("[remote \"origin\"]\n\turl = ssh://git@git.example.com/org/repo.git\n"), 0o644))

	apps, _, err := Discover(context.Background(), []string{root}, defaultCfg())
	require.NoError(t, err)

	argoApps := filterByFramework(apps, FrameworkArgoCD)
	require.Len(t, argoApps, 1)
	assert.Equal(t, filepath.Join(root, "platform"), argoApps[0].RootPath)
}

func TestParseRemoteURLs(t *testing.T) {
	cfg := []byte(`[core]
	bare = false
	url = not-a-remote
[remote "origin"]
	url = ssh://git@git.example.com/org/repo.git
	fetch = +refs/heads/*:refs/remotes/origin/*
[Remote "Upstream"]
	URL = "https://git.example.com/up/repo" # quoted
[remote "mirror"]
	; comment
	url = git@mirror.example.com:org/repo.git ; trailing
[branch "main"]
	remote = origin
`)
	assert.Equal(t, []string{
		"ssh://git@git.example.com/org/repo.git",
		"https://git.example.com/up/repo",
		"git@mirror.example.com:org/repo.git",
	}, parseRemoteURLs(cfg))
}

func TestGitConfigPath(t *testing.T) {
	mkfile := func(path, content string) {
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
	}

	t.Run("repository", func(t *testing.T) {
		root := t.TempDir()
		mkfile(filepath.Join(root, ".git", "config"), "")
		got, err := gitConfigPath(root)
		require.NoError(t, err)
		assert.Equal(t, filepath.Join(root, ".git", "config"), got)
	})

	t.Run("submodule", func(t *testing.T) {
		parent := t.TempDir()
		sub := filepath.Join(parent, "sub")
		mkfile(filepath.Join(parent, ".git", "modules", "sub", "config"), "")
		mkfile(filepath.Join(sub, ".git"), "gitdir: ../.git/modules/sub\n")
		got, err := gitConfigPath(sub)
		require.NoError(t, err)
		assert.Equal(t, filepath.Join(parent, ".git", "modules", "sub", "config"), got)
	})

	t.Run("linked worktree", func(t *testing.T) {
		main := t.TempDir()
		wt := t.TempDir()
		wtGitDir := filepath.Join(main, ".git", "worktrees", "wt")
		mkfile(filepath.Join(main, ".git", "config"), "")
		mkfile(filepath.Join(wtGitDir, "commondir"), "../..\n")
		mkfile(filepath.Join(wt, ".git"), "gitdir: "+wtGitDir+"\n")
		got, err := gitConfigPath(wt)
		require.NoError(t, err)
		assert.Equal(t, filepath.Join(main, ".git", "config"), got)
	})

	t.Run("no repository", func(t *testing.T) {
		_, err := gitConfigPath(t.TempDir())
		assert.Error(t, err)
	})
}

func TestJoinInRepo(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "apps", "a"), 0o755))

	tests := []struct {
		name, base, rel string
		want            string
		wantErr         bool
	}{
		{"inside", root, "apps/a", filepath.Join(root, "apps", "a"), false},
		{"dot", root, ".", root, false},
		{"in-repo dotdot", filepath.Join(root, "apps", "a"), "../../apps", filepath.Join(root, "apps"), false},
		{"escape", root, "../x", "", true},
		{"deep escape", filepath.Join(root, "apps"), "../../../etc", "", true},
		{"absolute rel stays under root", root, "/etc/passwd", filepath.Join(root, "etc", "passwd"), false},
		{"nonexistent inside", root, "missing/dir", filepath.Join(root, "missing", "dir"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := joinInRepo(newRepoRoot(root), tt.base, tt.rel)
			if tt.wantErr {
				require.Error(t, err)
				assert.ErrorIs(t, err, errOutsideRepo)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}

	t.Run("empty repoRoot disables containment", func(t *testing.T) {
		got, err := joinInRepo(newRepoRoot(""), "/a/b", "../../../x")
		require.NoError(t, err)
		assert.Equal(t, "/x", got)
	})
}

func TestJoinInRepoResolvesRootOnce(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "apps", "a"), 0o755))

	var calls atomic.Int32
	orig := evalSymlinksRoot
	evalSymlinksRoot = func(p string) (string, error) {
		calls.Add(1)
		return orig(p)
	}
	t.Cleanup(func() { evalSymlinksRoot = orig })

	rr := newRepoRoot(root)
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, err := joinInRepo(rr, root, "apps/a")
			assert.NoError(t, err)
			assert.Equal(t, filepath.Join(root, "apps", "a"), got)
			_, err = joinInRepo(rr, root, "../x")
			assert.ErrorIs(t, err, errOutsideRepo)
		}()
	}
	wg.Wait()
	assert.EqualValues(t, 1, calls.Load())
}
