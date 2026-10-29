package render

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lukashankeln/glint/internal/discovery"
	"github.com/lukashankeln/glint/internal/fsutil"
)

// Tests in this file that lower maxStreamBytes or maxStreamDocuments mutate
// package state and must stay serial (no t.Parallel); they restore the
// values with t.Cleanup.

func lowerStreamLimits(t *testing.T, bytesLimit, docs int) {
	t.Helper()
	ob, od := maxStreamBytes, maxStreamDocuments
	maxStreamBytes, maxStreamDocuments = bytesLimit, docs
	t.Cleanup(func() { maxStreamBytes, maxStreamDocuments = ob, od })
}

func TestSplitYAML_DocumentCap(t *testing.T) {
	lowerStreamLimits(t, 1<<20, 5)
	doc := "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: a\n"
	ok := strings.Repeat("---\n"+doc, 5)
	ms, err := SplitYAML([]byte(ok), "app", "src", false)
	require.NoError(t, err)
	assert.Len(t, ms, 5)

	_, err = SplitYAML([]byte(ok+"---\n"+doc), "app", "src", false)
	require.Error(t, err)
	assert.True(t, errors.Is(err, errTooManyDocuments))

	// Empty documents count too.
	_, err = SplitYAML([]byte(strings.Repeat("---\n", 5)), "app", "src", false)
	require.NoError(t, err)
	_, err = SplitYAML([]byte(strings.Repeat("---\n", 6)), "app", "src", false)
	assert.True(t, errors.Is(err, errTooManyDocuments))
}

func TestSplitYAML_ByteCap(t *testing.T) {
	doc := "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: a\n"
	lowerStreamLimits(t, len(doc), 10)
	_, err := SplitYAML([]byte(doc), "app", "src", false)
	require.NoError(t, err)
	_, err = SplitYAML([]byte(doc+"#"), "app", "src", false)
	require.Error(t, err)
	assert.True(t, errors.Is(err, errStreamTooLarge))
}

func TestReadFileCapped(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "f")
	require.NoError(t, os.WriteFile(p, []byte("12345"), 0o644))
	data, err := readFileCapped(p, 5)
	require.NoError(t, err)
	assert.Equal(t, "12345", string(data))
	_, err = readFileCapped(p, 4)
	assert.True(t, errors.Is(err, errStreamTooLarge))
	// A regular file is rejected by its size, before any read.
	assert.Contains(t, err.Error(), "file is 5 bytes")

	_, err = readFileCapped(filepath.Join(dir, "missing"), 4)
	assert.Error(t, err)

	if _, statErr := os.Stat("/dev/zero"); statErr != nil {
		t.Skip("/dev/zero not available")
	}
	link := filepath.Join(dir, "zero")
	require.NoError(t, os.Symlink("/dev/zero", link))
	_, err = readFileCapped(link, 1024)
	assert.True(t, errors.Is(err, fsutil.ErrNotRegular))
}

func TestCappedBuffer_Write(t *testing.T) {
	c := &cappedBuffer{limit: 4}
	n, err := c.Write([]byte("abcd"))
	require.NoError(t, err)
	assert.Equal(t, 4, n)
	assert.False(t, c.exceeded)
	_, err = c.Write([]byte("e"))
	assert.True(t, errors.Is(err, errOutputLimit))
	assert.True(t, c.exceeded)
	assert.Equal(t, "abcd", string(c.Bytes()))
}

func TestCappedBuffer_IOCopyFromPipeStopsAtCap(t *testing.T) {
	r, w, err := os.Pipe()
	require.NoError(t, err)
	defer r.Close()
	go func() {
		defer w.Close()
		chunk := make([]byte, 1024)
		for i := 0; i < 64; i++ {
			if _, err := w.Write(chunk); err != nil {
				return
			}
		}
	}()
	c := &cappedBuffer{limit: 4096}
	_, err = io.Copy(c, r)
	assert.True(t, errors.Is(err, errOutputLimit))
	assert.True(t, c.exceeded)
	assert.LessOrEqual(t, len(c.Bytes()), 4096)
}

func TestTruncatingWriter(t *testing.T) {
	w := &truncatingWriter{}
	n, err := w.Write([]byte("hello"))
	require.NoError(t, err)
	assert.Equal(t, 5, n)
	assert.Equal(t, "hello", w.String())

	big := make([]byte, stderrKeep)
	n, err = w.Write(big)
	require.NoError(t, err)
	assert.Equal(t, len(big), n)
	n, err = w.Write([]byte("more"))
	require.NoError(t, err)
	assert.Equal(t, 4, n)
	s := w.String()
	assert.Contains(t, s, "[stderr truncated]")
	assert.Equal(t, stderrKeep, w.buf.Len())
	assert.True(t, strings.HasPrefix(s, "hello"))
}

// TestHelperProcess is re-executed by the subprocess tests; it is a no-op
// when GO_WANT_HELPER_PROCESS is unset.
func TestHelperProcess(t *testing.T) {
	switch os.Getenv("GO_WANT_HELPER_PROCESS") {
	case "flood":
		chunk := []byte(strings.Repeat("x", 4096))
		for {
			if _, err := os.Stdout.Write(chunk); err != nil {
				os.Exit(1)
			}
		}
	case "manifest":
		_, _ = os.Stdout.WriteString("apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: a\n")
		os.Exit(0)
	}
}

func fakeExec(t *testing.T, mode string) {
	t.Helper()
	orig := execCommandContext
	execCommandContext = func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestHelperProcess$")
		cmd.Env = append(os.Environ(), "GO_WANT_HELPER_PROCESS="+mode)
		return cmd
	}
	t.Cleanup(func() { execCommandContext = orig })
}

func TestSubprocessRenderers_OutputCap(t *testing.T) {
	app := discovery.DiscoveredApp{Name: "x", RootPath: t.TempDir()}
	renderers := map[string]func(context.Context) (int, error){
		"helm": func(ctx context.Context) (int, error) {
			ms, err := renderHelmSubprocess(ctx, app, nil)
			return len(ms), err
		},
		"kustomize": func(ctx context.Context) (int, error) {
			ms, err := renderKustomizeSubprocess(ctx, app)
			return len(ms), err
		},
	}
	for name, run := range renderers {
		t.Run(name+"/over cap", func(t *testing.T) {
			lowerStreamLimits(t, 64<<10, 10)
			fakeExec(t, "flood")
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			_, err := run(ctx)
			require.Error(t, err)
			assert.True(t, errors.Is(err, errOutputLimit), "got %v", err)
			assert.NoError(t, ctx.Err(), "must not hang until the timeout")
		})
		t.Run(name+"/small output", func(t *testing.T) {
			fakeExec(t, "manifest")
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			n, err := run(ctx)
			require.NoError(t, err)
			assert.Equal(t, 1, n)
		})
	}
}

// Lowers the package-level stream cap, so it must stay serial.
func TestBuildValues_OversizedValuesFile(t *testing.T) {
	dir := t.TempDir()
	vf := filepath.Join(dir, "values.yaml")
	require.NoError(t, os.WriteFile(vf, []byte("a: 1\nb: 2\n"), 0o644))
	lowerStreamLimits(t, 4, maxStreamDocuments)

	r := newHelmRenderer(defaultCfg())
	_, err := r.buildValues(discovery.DiscoveredApp{Name: "x", ValuesFiles: []string{vf}})
	require.Error(t, err)
	assert.True(t, errors.Is(err, errStreamTooLarge))
	assert.Contains(t, err.Error(), "values file")
}

func TestHelmRenderer_ChartYAMLTooLarge(t *testing.T) {
	dir := t.TempDir()
	chartYAML := "apiVersion: v2\nname: c\nversion: 0.1.0\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "Chart.yaml"), []byte(chartYAML), 0o644))
	app := discovery.DiscoveredApp{Name: "c", RootPath: dir}

	lowerStreamLimits(t, len(chartYAML)-1, maxStreamDocuments)
	_, err := newHelmRenderer(defaultCfg()).renderSDK(context.Background(), app)
	require.Error(t, err)
	assert.True(t, errors.Is(err, errStreamTooLarge))

	// At the limit the check passes; any later failure is not a limit error.
	lowerStreamLimits(t, len(chartYAML), maxStreamDocuments)
	_, err = newHelmRenderer(defaultCfg()).renderSDK(context.Background(), app)
	assert.False(t, errors.Is(err, errStreamTooLarge))
	assert.False(t, errors.Is(err, fsutil.ErrNotRegular))
}

func TestHelmRenderer_HelmignoreTooLarge(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "Chart.yaml"), []byte("apiVersion: v2\nname: c\nversion: 0.1.0\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".helmignore"), []byte(strings.Repeat("x\n", 100)), 0o644))
	lowerStreamLimits(t, 100, maxStreamDocuments)

	_, err := newHelmRenderer(defaultCfg()).renderSDK(context.Background(), discovery.DiscoveredApp{Name: "c", RootPath: dir})
	require.Error(t, err)
	assert.True(t, errors.Is(err, errStreamTooLarge))
	assert.Contains(t, err.Error(), ".helmignore")
}

// A root without a Chart.yaml, or one that is a packaged chart file, is left to
// Helm's loader: only non-regular and oversized files are rejected up front.
func TestCheckLocalChart_DefersOtherErrorsToLoader(t *testing.T) {
	dir := t.TempDir()
	assert.NoError(t, checkLocalChart(dir))
	assert.NoError(t, checkLocalChart(filepath.Join(dir, "missing")))

	tgz := filepath.Join(dir, "chart.tgz")
	require.NoError(t, os.WriteFile(tgz, []byte("not really a tarball"), 0o644))
	assert.NoError(t, checkLocalChart(tgz))
}
