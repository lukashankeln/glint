package plugins

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lukashankeln/glint/internal/config"
	"github.com/lukashankeln/glint/internal/manifest"
	"github.com/lukashankeln/glint/internal/rules"
	"github.com/lukashankeln/glint/internal/subproc"
)

// TestMain allows the test binary to act as a fake plugin subprocess.
// When GLINT_PLUGIN_TEST_MODE is set the binary writes canned output and exits.
func TestMain(m *testing.M) {
	switch os.Getenv("GLINT_PLUGIN_TEST_MODE") {
	case "valid":
		os.Stdout.WriteString(`[{"rule_id":"test/rule","severity":"error","message":"hostNetwork must not be true","file_path":"deploy.yaml","resource":{"kind":"Deployment","name":"api","namespace":"prod"}}]`)
		os.Exit(0)
	case "empty":
		os.Stdout.WriteString(`[]`)
		os.Exit(0)
	case "non_zero":
		os.Stderr.WriteString("plugin crashed")
		os.Exit(1)
	case "stderr_flood":
		chunk := bytes.Repeat([]byte("x"), 32<<10)
		for range 128 { // 4 MiB
			_, _ = os.Stderr.Write(chunk)
		}
		os.Exit(1)
	case "stdout_flood":
		chunk := bytes.Repeat([]byte("x"), 32<<10)
		for {
			if _, err := os.Stdout.Write(chunk); err != nil {
				os.Exit(1)
			}
		}
	case "malformed":
		os.Stdout.WriteString(`not json at all`)
		os.Exit(0)
	case "file_mode":
		// verify a path was passed as an argument and the file exists
		if len(os.Args) < 2 {
			os.Stderr.WriteString("no file path arg")
			os.Exit(1)
		}
		if _, err := os.Stat(os.Args[len(os.Args)-1]); err != nil {
			os.Stderr.WriteString("file not found: " + err.Error())
			os.Exit(1)
		}
		os.Stdout.WriteString(`[]`)
		os.Exit(0)
	case "hang":
		time.Sleep(10 * time.Second)
		os.Exit(0)
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
		_, _ = os.Stderr.WriteString("orphan-exit stderr marker")
		_, _ = os.Stdout.WriteString(`[]`)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// startHelperGrandchild re-executes the test binary in "sleep" mode with the
// stdout/stderr this process inherited, so it holds the parent's pipes.
func startHelperGrandchild() {
	cmd := exec.Command(os.Args[0])
	cmd.Env = append(os.Environ(), "GLINT_PLUGIN_TEST_MODE=sleep")
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

var testManifests = []manifest.Manifest{
	{Kind: "Deployment", Name: "api", Namespace: "prod", Raw: []byte("apiVersion: apps/v1\nkind: Deployment\n")},
}

func selfExe(t *testing.T) string {
	t.Helper()
	exe, err := os.Executable()
	require.NoError(t, err)
	return exe
}

func TestRunner_ValidOutput(t *testing.T) {
	r, err := New(config.PluginConfig{
		Name:    "test",
		Command: selfExe(t),
		Env:     map[string]string{"GLINT_PLUGIN_TEST_MODE": "valid"},
	})
	require.NoError(t, err)

	vs, err := r.Run(context.Background(), testManifests)
	require.NoError(t, err)
	require.Len(t, vs, 1)

	v := vs[0]
	assert.Equal(t, "test/rule", v.RuleID)
	assert.Equal(t, rules.SeverityError, v.Severity)
	assert.Equal(t, "hostNetwork must not be true", v.Message)
	assert.Equal(t, "deploy.yaml", v.FilePath)
	assert.Equal(t, "Deployment", v.ResourceKind)
	assert.Equal(t, "api", v.ResourceName)
	assert.Equal(t, "prod", v.ResourceNS)
	assert.Equal(t, "test", v.Source)
}

func TestRunner_EmptyOutput(t *testing.T) {
	r, err := New(config.PluginConfig{
		Name:    "test",
		Command: selfExe(t),
		Env:     map[string]string{"GLINT_PLUGIN_TEST_MODE": "empty"},
	})
	require.NoError(t, err)

	vs, err := r.Run(context.Background(), testManifests)
	require.NoError(t, err)
	assert.Empty(t, vs)
}

func TestRunner_NonZeroExit_Error(t *testing.T) {
	// Default: non-zero exit means the plugin could not complete evaluation.
	// Surfaced as an error-severity violation so it appears in lint output.
	r, err := New(config.PluginConfig{
		Name:    "test",
		Command: selfExe(t),
		Env:     map[string]string{"GLINT_PLUGIN_TEST_MODE": "non_zero"},
	})
	require.NoError(t, err)

	vs, err := r.Run(context.Background(), testManifests)
	require.NoError(t, err)
	require.Len(t, vs, 1)
	assert.Equal(t, "error", string(vs[0].Severity))
	assert.Contains(t, vs[0].Message, "could not complete evaluation")
}

func TestRunner_StderrFlood_Truncated(t *testing.T) {
	r, err := New(config.PluginConfig{
		Name:    "test",
		Command: selfExe(t),
		Env:     map[string]string{"GLINT_PLUGIN_TEST_MODE": "stderr_flood"},
	})
	require.NoError(t, err)

	vs, err := r.Run(context.Background(), testManifests)
	require.NoError(t, err)
	require.Len(t, vs, 1)
	assert.Contains(t, vs[0].Message, "[stderr truncated]")
	assert.LessOrEqual(t, len(vs[0].Message), subproc.StderrKeep+256)
}

func TestRunner_NonZeroExit_Allowed(t *testing.T) {
	// allow_nonzero_exit: true — non-zero exit is expected for this plugin.
	r, err := New(config.PluginConfig{
		Name:             "test",
		Command:          selfExe(t),
		Env:              map[string]string{"GLINT_PLUGIN_TEST_MODE": "non_zero"},
		AllowNonZeroExit: true,
	})
	require.NoError(t, err)

	vs, err := r.Run(context.Background(), testManifests)
	require.NoError(t, err)
	assert.Empty(t, vs)
}

func TestRunner_MalformedJSON(t *testing.T) {
	// Malformed JSON surfaces as an error-severity violation so lint continues.
	r, err := New(config.PluginConfig{
		Name:    "test",
		Command: selfExe(t),
		Env:     map[string]string{"GLINT_PLUGIN_TEST_MODE": "malformed"},
	})
	require.NoError(t, err)

	vs, err := r.Run(context.Background(), testManifests)
	require.NoError(t, err)
	require.Len(t, vs, 1)
	assert.Equal(t, rules.SeverityError, vs[0].Severity)
	assert.Contains(t, vs[0].Message, "could not be parsed")
}

func TestRunner_FileMode(t *testing.T) {
	r, err := New(config.PluginConfig{
		Name:    "test",
		Command: selfExe(t),
		Input:   "file",
		Env:     map[string]string{"GLINT_PLUGIN_TEST_MODE": "file_mode"},
	})
	require.NoError(t, err)

	vs, err := r.Run(context.Background(), testManifests)
	require.NoError(t, err)
	assert.Empty(t, vs)
}

func TestRunner_SeverityOverride(t *testing.T) {
	r, err := New(config.PluginConfig{
		Name:     "test",
		Command:  selfExe(t),
		Severity: "warning",
		Env:      map[string]string{"GLINT_PLUGIN_TEST_MODE": "valid"},
	})
	require.NoError(t, err)

	vs, err := r.Run(context.Background(), testManifests)
	require.NoError(t, err)
	require.Len(t, vs, 1)
	// The plugin reports "error" but the config overrides to "warning".
	assert.Equal(t, rules.SeverityWarning, vs[0].Severity)
}

func TestRunner_UnknownAdapter(t *testing.T) {
	_, err := New(config.PluginConfig{
		Name:    "test",
		Command: "echo",
		Adapter: "nonexistent",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "adapter")
}

func TestRunner_NoManifests(t *testing.T) {
	r, err := New(config.PluginConfig{Name: "test", Command: "echo"})
	require.NoError(t, err)

	vs, err := r.Run(context.Background(), nil)
	require.NoError(t, err)
	assert.Empty(t, vs)
}

func TestRunner_Timeout(t *testing.T) {
	r, err := New(config.PluginConfig{
		Name:    "test",
		Command: selfExe(t),
		Timeout: "50ms",
		Env:     map[string]string{"GLINT_PLUGIN_TEST_MODE": "hang"},
	})
	require.NoError(t, err)

	start := time.Now()
	_, err = r.Run(context.Background(), testManifests)
	elapsed := time.Since(start)

	// Should have been killed well before the 10s sleep in the subprocess.
	assert.Less(t, elapsed, 2*time.Second)
	// Timeout is always an error, regardless of fail_on_error.
	require.Error(t, err)
	assert.Contains(t, err.Error(), "timed out")
}

func TestRunner_DefaultTimeout(t *testing.T) {
	// Verify the Runner struct carries a zero timeout when none is configured
	// (defaultPluginTimeout is applied at call time, not stored).
	r, err := New(config.PluginConfig{Name: "test", Command: "echo"})
	require.NoError(t, err)
	assert.Equal(t, time.Duration(0), r.timeout)
}

func TestBuild(t *testing.T) {
	ps, err := Build([]config.PluginConfig{
		{Name: "a", Command: "echo"},
		{Name: "b", Command: "echo"},
	})
	require.NoError(t, err)
	assert.Len(t, ps, 2)
	assert.Equal(t, "a", ps[0].Name())
	assert.Equal(t, "b", ps[1].Name())
}

func TestRunner_StdoutFlood_OutputLimit(t *testing.T) {
	old := maxPluginOutputBytes
	maxPluginOutputBytes = 1 << 20
	t.Cleanup(func() { maxPluginOutputBytes = old })

	r, err := New(config.PluginConfig{
		Name:    "flood",
		Command: os.Args[0],
		Env:     map[string]string{"GLINT_PLUGIN_TEST_MODE": "stdout_flood"},
		Timeout: "20s",
	})
	require.NoError(t, err)

	start := time.Now()
	_, err = r.Run(context.Background(), []manifest.Manifest{{Raw: []byte("kind: Pod\n")}})
	elapsed := time.Since(start)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "output exceeded")
	assert.Less(t, elapsed, 10*time.Second)
}
