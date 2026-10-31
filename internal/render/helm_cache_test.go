package render

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

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

func TestCleanupHelmRepoCache_RefetchAfterCleanup(t *testing.T) {
	home := t.TempDir()
	for _, k := range []string{"HELM_CACHE_HOME", "HELM_CONFIG_HOME", "HELM_DATA_HOME"} {
		t.Setenv(k, home)
	}
	t.Cleanup(CleanupHelmRepoCache)

	var mu sync.Mutex
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/index.yaml" {
			http.NotFound(w, r)
			return
		}
		mu.Lock()
		hits++
		mu.Unlock()
		_, _ = w.Write([]byte(minimalHelmIndex))
	}))
	defer srv.Close()

	getters := getter.All(helmenv.New())
	fetch := func() {
		t.Helper()
		idx, err := loadRepoIndexCached(srv.URL, getters)
		if err != nil {
			t.Fatalf("fetch: %v", err)
		}
		if idx == nil || len(idx.Entries["demo"]) != 1 {
			t.Fatalf("fetch: unexpected index %+v", idx)
		}
	}
	count := func() int {
		mu.Lock()
		defer mu.Unlock()
		return hits
	}

	fetch()
	fetch()
	if got := count(); got != 1 {
		t.Fatalf("requests before cleanup = %d, want 1", got)
	}
	CleanupHelmRepoCache()
	fetch()
	if got := count(); got != 2 {
		t.Fatalf("requests after cleanup = %d, want 2", got)
	}
}

// A cleanup while a fetch is in flight must neither break that fetch nor
// leave its private dir behind, and the next call must fetch again.
func TestCleanupHelmRepoCache_InFlightFetchUnaffected(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	t.Cleanup(CleanupHelmRepoCache)

	var mu sync.Mutex
	hits := 0
	release := make(chan struct{})
	entered := make(chan struct{}, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hits++
		mu.Unlock()
		select {
		case entered <- struct{}{}:
		default:
		}
		<-release
		_, _ = w.Write([]byte(minimalHelmIndex))
	}))
	// Cleanups run LIFO: the handler is released before the server closes.
	t.Cleanup(srv.Close)
	var once sync.Once
	t.Cleanup(func() { once.Do(func() { close(release) }) })

	getters := getter.All(helmenv.New())
	type result struct{ err error }
	done := make(chan result, 1)
	go func() {
		idx, err := loadRepoIndexCached(srv.URL, getters)
		if err == nil && len(idx.Entries["demo"]) != 1 {
			err = errUnexpectedIndex
		}
		done <- result{err}
	}()

	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("handler never entered")
	}
	CleanupHelmRepoCache()
	once.Do(func() { close(release) })

	select {
	case r := <-done:
		if r.err != nil {
			t.Fatalf("in-flight fetch: %v", r.err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("in-flight fetch did not finish")
	}
	if matches, _ := filepath.Glob(filepath.Join(tmp, "glint-helm-repo-index-*")); len(matches) != 0 {
		t.Fatalf("expected in-flight dir removed, got %v", matches)
	}

	if _, err := loadRepoIndexCached(srv.URL, getters); err != nil {
		t.Fatalf("fetch after cleanup: %v", err)
	}
	mu.Lock()
	got := hits
	mu.Unlock()
	if got != 2 {
		t.Fatalf("requests = %d, want 2 (refetch after cleanup)", got)
	}
}

var errUnexpectedIndex = errors.New("unexpected index contents")
