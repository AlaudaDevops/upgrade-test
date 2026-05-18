package exec

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

type Command struct {
	Name string
	Args []string
	Dir  string
	Env  []string

	// EnvAllowlist limits which host environment variables are passed to the child
	// process. Entries support an exact name ("KUBECONFIG") or a "PREFIX_*" pattern
	// ("VIOLET_*"). When EnvAllowlist is empty, the child inherits os.Environ() in
	// full (backward-compatible default). Env is always appended after the filtered
	// host set.
	EnvAllowlist []string
}

// CommandResult represents the result of a command execution
type CommandResult struct {
	Stdout string
	Stderr string
	Err    error
}

type CommandOption func(*exec.Cmd)

func (c *Command) WithDir(dir string) CommandOption {
	return func(cmd *exec.Cmd) {
		cmd.Dir = dir
	}
}

// WithEnv adds environment variables to the command
func (c *Command) WithEnv(env []string) CommandOption {
	return func(cmd *exec.Cmd) {
		cmd.Env = append(cmd.Env, env...)
	}
}

// stderrTailLines bounds how many trailing stderr lines are wrapped into the error.
const stderrTailLines = 20

// RunCommand executes a command and returns its stdout, stderr and error
// If the command fails, it will return the error along with the captured output
// The command's output will be printed to console in real-time while also being captured
func RunCommand(ctx context.Context, cmd Command) CommandResult {
	runCmd := exec.CommandContext(ctx, cmd.Name, cmd.Args...)
	runCmd.Dir = cmd.Dir

	// Build child environment: allowlist-filtered host env (or full passthrough when empty),
	// then append custom Env entries.
	runCmd.Env = filterHostEnv(cmd.EnvAllowlist)
	if len(cmd.Env) > 0 {
		runCmd.Env = append(runCmd.Env, cmd.Env...)
	}

	// Create buffers to capture output
	var stdoutBuf, stderrBuf bytes.Buffer

	// Create multi-writers to both capture and print output
	stdoutWriter := io.MultiWriter(os.Stdout, &stdoutBuf)
	stderrWriter := io.MultiWriter(os.Stderr, &stderrBuf)

	runCmd.Stdout = stdoutWriter
	runCmd.Stderr = stderrWriter

	// Run the command
	err := runCmd.Run()
	if err != nil {
		err = wrapWithStderrTail(err, stderrBuf.String(), stderrTailLines)
	}
	return CommandResult{
		Stdout: stdoutBuf.String(),
		Stderr: stderrBuf.String(),
		Err:    err,
	}
}

// filterHostEnv returns os.Environ() filtered by allowlist. An empty allowlist
// returns the full os.Environ() (existing behaviour preserved).
func filterHostEnv(allowlist []string) []string {
	host := os.Environ()
	if len(allowlist) == 0 {
		return host
	}
	out := make([]string, 0, len(host))
	for _, entry := range host {
		eq := strings.IndexByte(entry, '=')
		if eq < 0 {
			continue
		}
		name := entry[:eq]
		if matchAllowlist(name, allowlist) {
			out = append(out, entry)
		}
	}
	return out
}

// matchAllowlist reports whether name matches any pattern in allowlist. A pattern
// ending in "*" matches by prefix; anything else is an exact match.
func matchAllowlist(name string, allowlist []string) bool {
	for _, pat := range allowlist {
		if strings.HasSuffix(pat, "*") {
			if strings.HasPrefix(name, strings.TrimSuffix(pat, "*")) {
				return true
			}
			continue
		}
		if name == pat {
			return true
		}
	}
	return false
}

// wrapWithStderrTail enriches err with the trailing stderr lines so callers see
// the actionable failure context without re-reading CommandResult.Stderr.
func wrapWithStderrTail(err error, stderr string, maxLines int) error {
	tail := lastLines(stderr, maxLines)
	if tail == "" {
		return err
	}
	return fmt.Errorf("%w; stderr tail:\n%s", err, tail)
}

func lastLines(s string, n int) string {
	if s == "" || n <= 0 {
		return ""
	}
	trimmed := strings.TrimRight(s, "\n")
	if trimmed == "" {
		return ""
	}
	lines := strings.Split(trimmed, "\n")
	if len(lines) <= n {
		return trimmed
	}
	return strings.Join(lines[len(lines)-n:], "\n")
}
