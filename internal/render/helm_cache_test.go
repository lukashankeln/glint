package render

import (
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"

	"helm.sh/helm/v4/pkg/getter"

	helmenv "helm.sh/helm/v4/pkg/cli"
)

const minimalHelmIndex = `apiVersion: v1
entries:
  demo:
    - name: demo
      version: 1.0.0
      urls:
        - https://example.invalid/demo-1.0.0.tgz
generated: "2024-01-01T00:00:00Z"
`

func TestCleanupHelmRepoCache_RecreatesDir(t *testing.T) {
	t.Cleanup(CleanupHelmRepoCache)

	dir1, err := repoIndexCacheDir()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir1); err != nil {
		t.Fatalf("dir should exist: %v", err)
	}
	CleanupHelmRepoCache()
	if _, err := os.Stat(dir1); !os.IsNotExist(err) {
		t.Fatalf("dir should be removed after cleanup, stat err = %v", err)
	}
	dir2, err := repoIndexCacheDir()
	if err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(dir2); err != nil || !fi.IsDir() {
		t.Fatalf("recreated dir should exist: %v", err)
	}
}

func TestCleanupHelmRepoCache_RefetchAfterCleanup(t *testing.T) {
	home := t.TempDir()
	for _, k := range []string{"HELM_CACHE_HOME", "HELM_CONFIG_HOME", "HELM_DATA_HOME"} {
		t.Setenv(k, home)
	}
	t.Cleanup(CleanupHelmRepoCache)

	var mu sync.Mutex
	hits := map[string]int{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hits[r.URL.Path]++
		mu.Unlock()
		if r.URL.Path != "/a/index.yaml" && r.URL.Path != "/b/index.yaml" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(minimalHelmIndex))
	}))
	defer srv.Close()

	getters := getter.All(helmenv.New())
	fetch := func(path string) {
		t.Helper()
		dir, err := repoIndexCacheDir()
		if err != nil {
			t.Fatal(err)
		}
		idx, err := loadRepoIndexCached(srv.URL+path, getters, dir)
		if err != nil {
			t.Fatalf("fetch %s: %v", path, err)
		}
		if idx == nil || len(idx.Entries["demo"]) != 1 {
			t.Fatalf("fetch %s: unexpected index %+v", path, idx)
		}
	}
	count := func(p string) int {
		mu.Lock()
		defer mu.Unlock()
		return hits[p]
	}

	fetch("/a")
	fetch("/a")
	if got := count("/a/index.yaml"); got != 1 {
		t.Fatalf("A requests before cleanup = %d, want 1", got)
	}
	CleanupHelmRepoCache()
	fetch("/b")
	fetch("/a")
	if got := count("/a/index.yaml"); got != 2 {
		t.Fatalf("A requests after cleanup = %d, want 2", got)
	}
}
