package render

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"sigs.k8s.io/kustomize/kyaml/filesys"

	"github.com/lukashankeln/glint/internal/discovery"
	"github.com/lukashankeln/glint/internal/fsutil"
	"github.com/lukashankeln/glint/internal/subproc"
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
	case "sleep":
		_ = os.WriteFile(os.Getenv("GLINT_TEST_PIDFILE"), []byte(strconv.Itoa(os.Getpid())), 0o644)
		time.Sleep(60 * time.Second)
		os.Exit(0)
	case "orphan-hang":
		startHelperGrandchild()
		waitHelperPidfile()
		select {}
	case "orphan-exit":
		startHelperGrandchild()
		waitHelperPidfile()
		_, _ = os.Stdout.WriteString("apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: a\n")
		os.Exit(0)
	case "manifest":
		_, _ = os.Stdout.WriteString("apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: a\n")
		os.Exit(0)
	}
}

// startHelperGrandchild re-executes the test binary in "sleep" mode with the
// stdout/stderr this process inherited, so it holds the parent's pipes.
func startHelperGrandchild() {
	cmd := exec.Command(os.Args[0], "-test.run=^TestHelperProcess$")
	cmd.Env = append(os.Environ(), "GO_WANT_HELPER_PROCESS=sleep")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		os.Exit(3)
	}
}

// waitHelperPidfile waits (bounded) until the grandchild has written its pidfile.
func waitHelperPidfile() {
	for i := 0; i < 200; i++ {
		if b, err := os.ReadFile(os.Getenv("GLINT_TEST_PIDFILE")); err == nil && len(b) > 0 {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	os.Exit(4)
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
			ms, err := renderKustomizeSubprocess(ctx, defaultCfg(), app)
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
			assert.True(t, errors.Is(err, subproc.ErrOutputLimit), "got %v", err)
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

const kustCM = "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: a\n"

func writeKustApp(t *testing.T, resource string) discovery.DiscoveredApp {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "kustomization.yaml"), []byte("resources:\n- r.yaml\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "r.yaml"), []byte(resource), 0o644))
	return discovery.DiscoveredApp{Name: "k", Renderer: discovery.RendererKustomize, RootPath: dir}
}

func TestKustomizeSDK_ResourceSizeCap(t *testing.T) {
	// The comment makes r.yaml larger than the rendered output, so only the
	// read cap, not SplitYAML's output cap, can reject it.
	resource := kustCM + "# " + strings.Repeat("x", 200) + "\n"
	app := writeKustApp(t, resource)
	// kustomization.yaml is read through the cap too and is smaller than r.yaml.
	lowerStreamLimits(t, len(resource), 10)
	ms, err := newKustomizeRenderer(defaultCfg()).renderSDK(context.Background(), app)
	require.NoError(t, err)
	require.Len(t, ms, 1)
	assert.Equal(t, "a", ms[0].Name)

	lowerStreamLimits(t, len(resource)-1, 10)
	_, err = newKustomizeRenderer(defaultCfg()).renderSDK(context.Background(), app)
	require.Error(t, err)
	assert.Contains(t, err.Error(), fmt.Sprintf("file is %d bytes", len(resource)))
}

func TestCappedFS_ReadFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "f")
	require.NoError(t, os.WriteFile(p, []byte("12345"), 0o644))
	fs := cappedFS{filesys.MakeFsOnDisk()}
	data, err := fs.ReadFile(p)
	require.NoError(t, err)
	assert.Equal(t, "12345", string(data))

	lowerStreamLimits(t, 4, 10)
	_, err = fs.ReadFile(p)
	assert.True(t, errors.Is(err, errStreamTooLarge))
	assert.True(t, errors.Is(err, fsutil.ErrTooLarge))
}
