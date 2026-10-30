package plugins

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/lukashankeln/glint/internal/config"
	"github.com/lukashankeln/glint/internal/manifest"
	"github.com/lukashankeln/glint/internal/rules"
)

const (
	defaultPluginTimeout = 30 * time.Second
	maxPluginOutputBytes = 32 * 1024 * 1024 // 32 MB
	maxPluginStderrBytes = 64 << 10         // 64 KiB of stderr kept for errors
)

// pluginWaitDelay bounds how long a plugin's output pipes are drained after the
// plugin exits or the context is done, so a child process that inherited the
// pipes cannot hold cmd.Run open. It is a package var so tests can lower it.
var pluginWaitDelay = 2 * time.Second

var errOutputTruncated = errors.New("output exceeded limit")

// nativeViolation is the JSON schema plugins emit when using the native glint protocol.
type nativeViolation struct {
	RuleID   string `json:"rule_id"`
	Severity string `json:"severity"`
	Message  string `json:"message"`
	FilePath string `json:"file_path"`
	Resource struct {
		Kind      string `json:"kind"`
		Name      string `json:"name"`
		Namespace string `json:"namespace"`
	} `json:"resource"`
}

// limitedWriter caps how many bytes are buffered from plugin stdout.
type limitedWriter struct {
	buf       bytes.Buffer
	remaining int
	truncated bool
}

func (lw *limitedWriter) Write(p []byte) (int, error) {
	total := len(p)
	if lw.remaining <= 0 {
		lw.truncated = true
		return total, nil
	}
	if len(p) > lw.remaining {
		lw.truncated = true
		p = p[:lw.remaining]
	}
	n, err := lw.buf.Write(p)
	lw.remaining -= n
	if err != nil {
		return n, err
	}
	// Report full len(p) consumed so the subprocess keeps writing normally.
	return total, nil
}

// stderrWriter keeps the first maxPluginStderrBytes bytes of plugin stderr and
// discards the rest without ever returning an error, so a chatty plugin is
// never blocked. It is the sibling of truncatingWriter in internal/render
// (same cap and marker); that type is unexported and plugins should not import
// render's heavy dependencies.
type stderrWriter struct {
	buf       bytes.Buffer
	truncated bool
}

// Write always reports len(p), nil.
func (w *stderrWriter) Write(p []byte) (int, error) {
	total := len(p)
	room := max(maxPluginStderrBytes-w.buf.Len(), 0)
	if len(p) > room {
		w.truncated = true
		p = p[:room]
	}
	w.buf.Write(p)
	return total, nil
}

// String returns the kept stderr, with a marker if anything was dropped.
func (w *stderrWriter) String() string {
	if w.truncated {
		return w.buf.String() + "\n[stderr truncated]"
	}
	return w.buf.String()
}

// Runner is a subprocess-backed Plugin.
type Runner struct {
	cfg      config.PluginConfig
	adapter  Adapter
	severity rules.Severity
	timeout  time.Duration
}

// New constructs a Runner from a PluginConfig. Returns an error if a configured
// adapter is not registered or the timeout is unparseable.
func New(cfg config.PluginConfig) (*Runner, error) {
	r := &Runner{cfg: cfg}

	if cfg.Severity != "" {
		r.severity = rules.Severity(strings.ToLower(cfg.Severity))
	}

	if cfg.Timeout != "" {
		d, err := time.ParseDuration(cfg.Timeout)
		if err != nil {
			return nil, fmt.Errorf("plugin %q: invalid timeout %q: %w", cfg.Name, cfg.Timeout, err)
		}
		r.timeout = d
	}

	if cfg.Adapter != "" {
		a, ok := adapters[cfg.Adapter]
		if !ok {
			return nil, fmt.Errorf("plugin %q: adapter %q is not registered", cfg.Name, cfg.Adapter)
		}
		r.adapter = a
	}

	return r, nil
}

// Build creates a Plugin for every entry in cfgs.
func Build(cfgs []config.PluginConfig) ([]Plugin, error) {
	ps := make([]Plugin, 0, len(cfgs))
	for _, cfg := range cfgs {
		r, err := New(cfg)
		if err != nil {
			return nil, err
		}
		ps = append(ps, r)
	}
	return ps, nil
}

func (r *Runner) Name() string { return r.cfg.Name }

// Run serializes manifests to YAML, invokes the plugin subprocess, and parses the output.
func (r *Runner) Run(ctx context.Context, manifests []manifest.Manifest) ([]rules.Violation, error) {
	if len(manifests) == 0 {
		return nil, nil
	}

	timeout := r.timeout
	if timeout == 0 {
		timeout = defaultPluginTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	input := marshalManifests(manifests)

	var (
		stdout []byte
		err    error
	)
	if r.cfg.Input == "file" {
		stdout, err = r.runWithFile(ctx, input)
	} else {
		stdout, err = r.runWithStdin(ctx, input)
	}

	// Non-zero exit and violations are independent concerns. Always parse stdout
	// so violations are never silently dropped. A non-zero exit means the plugin
	// could not complete evaluation — surface it as an error-severity violation
	// so it appears in the output alongside any other findings, unless
	// allow_nonzero_exit is set (for plugins that legitimately exit non-zero).
	var evalErr *rules.Violation
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("plugin %q: timed out after %s", r.cfg.Name, timeout)
		}
		if errors.Is(err, exec.ErrWaitDelay) && ctx.Err() == nil {
			// Hard error: a plugin that leaves a child holding its output open
			// cannot be trusted, and allow_nonzero_exit must not mask it.
			return nil, fmt.Errorf("plugin %q: exited but a child process kept its output open: %w", r.cfg.Name, err)
		}
		if errors.Is(err, errOutputTruncated) {
			return nil, fmt.Errorf("plugin %q: output exceeded %d MB limit", r.cfg.Name, maxPluginOutputBytes/1024/1024)
		}
		if !r.cfg.AllowNonZeroExit {
			v := rules.Violation{
				RuleID:   r.cfg.Name,
				Severity: rules.SeverityError,
				Message:  fmt.Sprintf("plugin could not complete evaluation: %v", err),
				Source:   r.cfg.Name,
				FilePath: sourceFile(manifests),
			}
			evalErr = &v
		}
	}

	var violations []rules.Violation
	if len(bytes.TrimSpace(stdout)) > 0 {
		var parseErr error
		if r.adapter != nil {
			violations, parseErr = r.adapter(stdout, r.severity)
		} else {
			violations, parseErr = parseNative(stdout, r.cfg.Name, r.severity)
		}
		if parseErr != nil {
			violations = append(violations, rules.Violation{
				RuleID:   r.cfg.Name,
				Severity: rules.SeverityError,
				Message:  fmt.Sprintf("plugin output could not be parsed: %v", parseErr),
				Source:   r.cfg.Name,
			})
		}
	}

	if evalErr != nil {
		violations = append(violations, *evalErr)
	}
	return violations, nil
}

func (r *Runner) runWithStdin(ctx context.Context, input []byte) ([]byte, error) {
	cmd := r.buildCmd(ctx, r.cfg.Args)
	cmd.Stdin = bytes.NewReader(input)
	return r.collectOutput(cmd)
}

func (r *Runner) runWithFile(ctx context.Context, input []byte) ([]byte, error) {
	f, err := os.CreateTemp("", "glint-plugin-*.yaml")
	if err != nil {
		return nil, fmt.Errorf("creating temp file: %w", err)
	}
	defer os.Remove(f.Name())

	if _, err := f.Write(input); err != nil {
		f.Close()
		return nil, fmt.Errorf("writing temp file: %w", err)
	}
	f.Close()

	args := append(r.cfg.Args, f.Name())
	cmd := r.buildCmd(ctx, args)
	return r.collectOutput(cmd)
}

func (r *Runner) buildCmd(ctx context.Context, args []string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, r.cfg.Command, args...)
	cmd.Env = os.Environ()
	for k, v := range r.cfg.Env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	return cmd
}

func (r *Runner) collectOutput(cmd *exec.Cmd) ([]byte, error) {
	lw := &limitedWriter{remaining: maxPluginOutputBytes}
	cmd.Stdout = lw
	stderr := &stderrWriter{}
	cmd.Stderr = stderr
	cmd.WaitDelay = pluginWaitDelay
	err := cmd.Run()
	if lw.truncated {
		return lw.buf.Bytes(), errOutputTruncated
	}
	return lw.buf.Bytes(), wrapRunErr(err, []byte(stderr.String()))
}

func wrapRunErr(err error, stderr []byte) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, exec.ErrWaitDelay) {
		return fmt.Errorf("%w\nstderr: %s", err, strings.TrimSpace(string(stderr)))
	}
	if ee, ok := errors.AsType[*exec.ExitError](err); ok {
		return fmt.Errorf("non-zero exit (%d): %s", ee.ExitCode(), strings.TrimSpace(string(stderr)))
	}
	return err
}

// marshalManifests joins the raw YAML bytes of each manifest with --- separators.
func marshalManifests(manifests []manifest.Manifest) []byte {
	var buf bytes.Buffer
	for i, m := range manifests {
		if i > 0 {
			buf.WriteString("---\n")
		}
		buf.Write(m.Raw)
		if len(m.Raw) > 0 && m.Raw[len(m.Raw)-1] != '\n' {
			buf.WriteByte('\n')
		}
	}
	return buf.Bytes()
}

// parseNative parses the native glint JSON protocol output from a plugin.
func parseNative(data []byte, source string, severityOverride rules.Severity) ([]rules.Violation, error) {
	var raw []nativeViolation
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("invalid JSON: %w", err)
	}

	vs := make([]rules.Violation, 0, len(raw))
	for _, nv := range raw {
		sev := rules.Severity(strings.ToLower(nv.Severity))
		if severityOverride != "" {
			sev = severityOverride
		}
		vs = append(vs, rules.Violation{
			RuleID:       nv.RuleID,
			Severity:     sev,
			Message:      nv.Message,
			FilePath:     nv.FilePath,
			ResourceKind: nv.Resource.Kind,
			ResourceName: nv.Resource.Name,
			ResourceNS:   nv.Resource.Namespace,
			Source:       source,
		})
	}
	return vs, nil
}

// sourceFile returns the common source path when all manifests share one file,
// or empty string when they span multiple files or have no path info.
// The path is made relative to the working directory when possible.
func sourceFile(manifests []manifest.Manifest) string {
	if len(manifests) == 0 {
		return ""
	}
	first := manifests[0].SourcePath
	if first == "" {
		return ""
	}
	for _, m := range manifests[1:] {
		if m.SourcePath != first {
			return ""
		}
	}
	if wd, err := os.Getwd(); err == nil {
		if rel, err := filepath.Rel(wd, first); err == nil {
			return rel
		}
	}
	return first
}
