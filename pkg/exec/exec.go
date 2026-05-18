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

// redactingWriter returns an io.Writer that scans every Write for the given
// secret byte strings and substitutes "***" before forwarding to w. Empty
// secrets are skipped; an empty secrets list returns w unchanged so the
// hot path pays no per-write overhead when no redaction is configured.
func redactingWriter(w io.Writer, secrets []string) io.Writer {
	live := make([][]byte, 0, len(secrets))
	for _, s := range secrets {
		if s != "" {
			live = append(live, []byte(s))
		}
	}
	if len(live) == 0 {
		return w
	}
	return &scrubbingWriter{w: w, secrets: live}
}

type scrubbingWriter struct {
	w       io.Writer
	secrets [][]byte
}

func (s *scrubbingWriter) Write(p []byte) (int, error) {
	scrubbed := p
	for _, secret := range s.secrets {
		scrubbed = bytes.ReplaceAll(scrubbed, secret, []byte("***"))
	}
	if _, err := s.w.Write(scrubbed); err != nil {
		return 0, err
	}
	// Report the original byte count so callers using io.Copy do not see a
	// short write when the replacement is shorter (or longer) than the
	// input.
	return len(p), nil
}

type Command struct {
	Name string
	Args []string
	Dir  string
	Env  []string

	// EnvAllowlist limits which host environment variables are passed to the
	// child process — entries are exact names ("KUBECONFIG"). When the list
	// is empty, the child inherits os.Environ() in full (backward-compatible
	// default). Env is always appended after the filtered host set.
	EnvAllowlist []string

	// RedactSecrets is a list of literal byte strings that must never appear
	// in the child's stdout/stderr stream as captured by this CLI. Any byte
	// match is replaced with "***" before the output is written to the
	// console, the captured buffer, or the stderr-tail wrapped into Err.
	//
	// This guards against the child process echoing its own argv when
	// verbose/debug logging is on — argv-mode credentials still leak at the
	// OS level (ps auxe, /proc), but the CI build log no longer carries the
	// secret verbatim. The match is byte-literal and stateless, so a secret
	// split across two Write calls can slip through; in practice CI loggers
	// are line-buffered and the residual risk is acceptable.
	RedactSecrets []string
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

	// Create multi-writers to both capture and print output. When the caller
	// supplies RedactSecrets, every writer in the chain (console + capture
	// buffer) receives the scrubbed bytes — that way the stderr-tail wrap
	// downstream cannot read the unredacted secret back out of the buffer.
	stdoutWriter := redactingWriter(io.MultiWriter(os.Stdout, &stdoutBuf), cmd.RedactSecrets)
	stderrWriter := redactingWriter(io.MultiWriter(os.Stderr, &stderrBuf), cmd.RedactSecrets)

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
	// Membership lookup over a fixed list of env names. The list is always
	// short (operational essentials + an explicit credential enumeration)
	// so a linear scan is no worse than a map, and a map would obscure the
	// "every name was reviewed explicitly" property the allowlist gives us.
	out := make([]string, 0, len(host))
	for _, entry := range host {
		eq := strings.IndexByte(entry, '=')
		if eq < 0 {
			continue
		}
		name := entry[:eq]
		for _, want := range allowlist {
			if name == want {
				out = append(out, entry)
				break
			}
		}
	}
	return out
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
