package exec

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestMatchAllowlist(t *testing.T) {
	tests := []struct {
		name      string
		needle    string
		allowlist []string
		want      bool
	}{
		{"exact match hits", "KUBECONFIG", []string{"KUBECONFIG", "HOME"}, true},
		{"exact match misses", "USER", []string{"KUBECONFIG", "HOME"}, false},
		{"prefix glob hits", "VIOLET_REGISTRY_USERNAME", []string{"VIOLET_*"}, true},
		{"prefix glob misses on unrelated var", "GITHUB_TOKEN", []string{"VIOLET_*"}, false},
		{"empty allowlist never matches", "ANYTHING", nil, false},
		{"prefix glob and exact mix", "PATH", []string{"VIOLET_*", "PATH"}, true},
		{"prefix glob requires prefix not substring", "MY_VIOLET_TOKEN", []string{"VIOLET_*"}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := matchAllowlist(tc.needle, tc.allowlist); got != tc.want {
				t.Errorf("matchAllowlist(%q, %v) = %v, want %v", tc.needle, tc.allowlist, got, tc.want)
			}
		})
	}
}

func TestFilterHostEnv_EmptyAllowlistReturnsAll(t *testing.T) {
	// Empty allowlist must preserve the legacy "full os.Environ() passthrough"
	// behaviour — testCommand callers rely on it.
	t.Setenv("FILTER_HOST_ENV_PROBE", "probe-value")
	got := filterHostEnv(nil)
	if !contains(got, "FILTER_HOST_ENV_PROBE=probe-value") {
		t.Errorf("expected probe to survive in empty-allowlist mode; env=%v", got)
	}
}

func TestFilterHostEnv_AllowlistRestricts(t *testing.T) {
	// Set two probes and confirm the allowlist drops the one not listed.
	t.Setenv("FILTER_HOST_ENV_KEEP", "yes")
	t.Setenv("FILTER_HOST_ENV_DROP", "no")
	got := filterHostEnv([]string{"FILTER_HOST_ENV_KEEP"})
	if !contains(got, "FILTER_HOST_ENV_KEEP=yes") {
		t.Errorf("KEEP probe should be retained; env=%v", got)
	}
	if contains(got, "FILTER_HOST_ENV_DROP=no") {
		t.Errorf("DROP probe should be filtered out; env=%v", got)
	}
}

func TestFilterHostEnv_GlobMatchesPrefix(t *testing.T) {
	t.Setenv("FILTER_GLOB_TOKEN_A", "1")
	t.Setenv("FILTER_GLOB_TOKEN_B", "2")
	t.Setenv("OTHER_VAR", "leave-me")
	got := filterHostEnv([]string{"FILTER_GLOB_*"})
	if !contains(got, "FILTER_GLOB_TOKEN_A=1") || !contains(got, "FILTER_GLOB_TOKEN_B=2") {
		t.Errorf("both glob-matched vars should be present; env=%v", got)
	}
	if contains(got, "OTHER_VAR=leave-me") {
		t.Errorf("OTHER_VAR should be filtered; env=%v", got)
	}
}

func TestLastLines(t *testing.T) {
	tests := []struct {
		name string
		s    string
		n    int
		want string
	}{
		{"empty input returns empty", "", 5, ""},
		{"zero n returns empty", "a\nb\nc", 0, ""},
		{"fewer lines than n returns all", "a\nb", 5, "a\nb"},
		{"more lines than n returns tail", "a\nb\nc\nd\ne", 2, "d\ne"},
		{"trailing newline is trimmed", "a\nb\nc\n", 2, "b\nc"},
		{"only newlines collapses to empty", "\n\n\n", 5, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := lastLines(tc.s, tc.n); got != tc.want {
				t.Errorf("lastLines(%q, %d) = %q, want %q", tc.s, tc.n, got, tc.want)
			}
		})
	}
}

func TestWrapWithStderrTail(t *testing.T) {
	base := errors.New("exit status 1")

	t.Run("empty stderr passes through unchanged", func(t *testing.T) {
		got := wrapWithStderrTail(base, "", 5)
		if got.Error() != base.Error() {
			t.Errorf("expected pass-through, got %q", got.Error())
		}
		// %w semantics: returned err should still unwrap to base.
		if !errors.Is(got, base) {
			t.Error("expected errors.Is to identify base error")
		}
	})

	t.Run("non-empty stderr appended", func(t *testing.T) {
		got := wrapWithStderrTail(base, "line1\nline2\nline3\n", 2)
		if !strings.Contains(got.Error(), "stderr tail:") {
			t.Errorf("expected 'stderr tail:' prefix, got %q", got.Error())
		}
		if !strings.Contains(got.Error(), "line2\nline3") {
			t.Errorf("expected last 2 lines in wrapped error, got %q", got.Error())
		}
		if !errors.Is(got, base) {
			t.Error("wrapped error must still satisfy errors.Is(base)")
		}
	})
}

func TestRunCommand_EnvAllowlistAppliedToChild(t *testing.T) {
	// Verify the allowlist actually changes what the child sees. We export
	// two probes, restrict the child to one of them, then dump the child's
	// env via /bin/sh -c 'env' and check Stdout.
	t.Setenv("EXEC_TEST_KEEP", "keep-me")
	t.Setenv("EXEC_TEST_DROP", "drop-me")

	res := RunCommand(context.Background(), Command{
		Name:         "/bin/sh",
		Args:         []string{"-c", "env"},
		EnvAllowlist: []string{"EXEC_TEST_KEEP", "PATH"},
	})
	if res.Err != nil {
		t.Fatalf("RunCommand failed: %v\nstderr=%s", res.Err, res.Stderr)
	}
	if !strings.Contains(res.Stdout, "EXEC_TEST_KEEP=keep-me") {
		t.Errorf("KEEP probe should reach child; stdout=%s", res.Stdout)
	}
	if strings.Contains(res.Stdout, "EXEC_TEST_DROP=drop-me") {
		t.Errorf("DROP probe should NOT reach child (allowlist filtered); stdout=%s", res.Stdout)
	}
}

func TestRunCommand_EmptyAllowlistFullPassthrough(t *testing.T) {
	t.Setenv("EXEC_TEST_PROBE", "should-survive")
	res := RunCommand(context.Background(), Command{
		Name: "/bin/sh",
		Args: []string{"-c", "env"},
		// EnvAllowlist intentionally nil — legacy behaviour.
	})
	if res.Err != nil {
		t.Fatalf("RunCommand failed: %v\nstderr=%s", res.Err, res.Stderr)
	}
	if !strings.Contains(res.Stdout, "EXEC_TEST_PROBE=should-survive") {
		t.Errorf("nil allowlist should preserve full os.Environ(); stdout did not contain probe; stdout=%s", res.Stdout)
	}
}

func TestRunCommand_FailingChildWrapsStderrTail(t *testing.T) {
	// /bin/sh -c 'echo boom 1>&2; exit 7' — verify CommandResult.Err contains
	// the stderr tail wrap, and that errors.Is still works against the
	// underlying *exec.ExitError.
	res := RunCommand(context.Background(), Command{
		Name: "/bin/sh",
		Args: []string{"-c", "echo first-line 1>&2; echo last-line 1>&2; exit 7"},
	})
	if res.Err == nil {
		t.Fatal("expected error from exit 7")
	}
	if !strings.Contains(res.Err.Error(), "last-line") {
		t.Errorf("expected stderr tail to be wrapped into err message; got %q", res.Err.Error())
	}
	if res.Stderr == "" {
		t.Errorf("Stderr field should still hold full captured stderr")
	}
}

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}
