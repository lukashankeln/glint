package render

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	helmenv "helm.sh/helm/v4/pkg/cli"
	"helm.sh/helm/v4/pkg/getter"
)

// repoIndexCache is process-global and keyed by repo URL. Each httptest server
// has a unique URL, which keeps these tests independent of each other and of
// any other test that loads a repo index.

const testRepoIndex = `apiVersion: v1
entries:
  mychart:
    - name: mychart
      version: 1.0.0
      urls:
        - mychart-1.0.0.tgz
`

func TestLoadRepoIndexCached_RemovesPrivateDir(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(testRepoIndex))
	}))
	t.Cleanup(srv.Close)

	idx, err := loadRepoIndexCached(srv.URL, getter.All(helmenv.New()))
	if err != nil {
		t.Fatalf("loadRepoIndexCached: %v", err)
	}
	if _, err := idx.Get("mychart", "1.0.0"); err != nil {
		t.Fatalf("chart missing from index: %v", err)
	}
	entries, err := os.ReadDir(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected empty TMPDIR, got %d entries", len(entries))
	}
}

func TestLoadRepoIndexCached_InFlightDirOwnedByFetch(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)

	release := make(chan struct{})
	entered := make(chan struct{}, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case entered <- struct{}{}:
		default:
		}
		<-release
		_, _ = w.Write([]byte(testRepoIndex))
	}))
	// Cleanups run LIFO: the handler is released before the server closes.
	t.Cleanup(srv.Close)
	var once sync.Once
	t.Cleanup(func() { once.Do(func() { close(release) }) })

	type result struct{ err error }
	done := make(chan result, 1)
	go func() {
		_, err := loadRepoIndexCached(srv.URL, getter.All(helmenv.New()))
		done <- result{err}
	}()

	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("handler never entered")
	}
	pattern := filepath.Join(tmp, "glint-helm-repo-index-*")
	matches, err := filepath.Glob(pattern)
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 {
		t.Fatalf("expected 1 in-flight dir, got %v", matches)
	}

	once.Do(func() { close(release) })
	select {
	case r := <-done:
		if r.err != nil {
			t.Fatalf("loadRepoIndexCached: %v", r.err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("load did not finish")
	}
	matches, _ = filepath.Glob(pattern)
	if len(matches) != 0 {
		t.Fatalf("expected dir removed, got %v", matches)
	}
}
