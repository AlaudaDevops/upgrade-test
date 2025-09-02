package exec

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"

	"knative.dev/pkg/logging"
)

type Command struct {
	Name string
	Args []string
	Dir  string
	Env  []string
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

// RunCommand executes a command and returns its stdout, stderr and error
// If the command fails, it will return the error along with the captured output
// The command's output will be printed to console in real-time while also being captured
func RunCommand(ctx context.Context, cmd Command) CommandResult {
	logger := logging.FromContext(ctx)
	runCmd := exec.CommandContext(ctx, cmd.Name, cmd.Args...)
	runCmd.Dir = cmd.Dir

	// Inherit current process environment variables
	runCmd.Env = os.Environ()

	// Add custom environment variables if specified
	if len(cmd.Env) > 0 {
		runCmd.Env = append(runCmd.Env, cmd.Env...)
	}
	logger.Infow("injecting env", "env", runCmd.Env)

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
		return CommandResult{
			Stdout: stdoutBuf.String(),
			Stderr: stderrBuf.String(),
			Err:    err,
		}
	}

	return CommandResult{
		Stdout: stdoutBuf.String(),
		Stderr: stderrBuf.String(),
		Err:    nil,
	}
}
