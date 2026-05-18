package operatorhub

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildPackageURL(t *testing.T) {
	tests := []struct {
		name          string
		prefix        string
		opName        string
		channel       string
		bundleVersion string
		want          string
		wantErr       bool
	}{
		{
			name:          "v4.6 channel with v-prefixed version",
			prefix:        "http://package-minio.alauda.cn:9199/packages/",
			opName:        "tektoncd-operator",
			channel:       "v4.6",
			bundleVersion: "v4.6.0",
			want:          "http://package-minio.alauda.cn:9199/packages/tektoncd-operator/v4.6/tektoncd-operator.latest.ALL.v4.6.0.tgz",
		},
		{
			name:          "rc channel with rc-build suffix",
			prefix:        "http://package-minio.alauda.cn:9199/packages/",
			opName:        "tektoncd-operator",
			channel:       "rc",
			bundleVersion: "v4.2.5-rc.76.g976cff6",
			want:          "http://package-minio.alauda.cn:9199/packages/tektoncd-operator/rc/tektoncd-operator.latest.ALL.v4.2.5-rc.76.g976cff6.tgz",
		},
		{
			name:          "prefix without trailing slash",
			prefix:        "http://example.com/pkgs",
			opName:        "op",
			channel:       "stable",
			bundleVersion: "v1.0.0",
			want:          "http://example.com/pkgs/op/stable/op.latest.ALL.v1.0.0.tgz",
		},
		{name: "empty prefix", prefix: "", opName: "op", channel: "stable", bundleVersion: "v1.0.0", wantErr: true},
		{name: "empty name", prefix: "http://x/", channel: "stable", bundleVersion: "v1.0.0", wantErr: true},
		{name: "empty channel", prefix: "http://x/", opName: "op", bundleVersion: "v1.0.0", wantErr: true},
		{name: "empty bundleVersion", prefix: "http://x/", opName: "op", channel: "stable", wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := BuildPackageURL(tc.prefix, tc.opName, tc.channel, tc.bundleVersion)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("want error, got url=%q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("url mismatch:\n got: %s\nwant: %s", got, tc.want)
			}
		})
	}
}

func TestBuildVioletPushArgs(t *testing.T) {
	tests := []struct {
		name     string
		skipPush bool
		pushArgs []string
		envUser  string
		envPass  string
		want     []string
	}{
		{
			name:     "skip-push only, no creds, no extra args",
			skipPush: true,
			want:     []string{"push", "/tmp/x.tgz", "--target-catalog-source", "platform", "--skip-push"},
		},
		{
			name: "skip-push false drops the flag",
			want: []string{"push", "/tmp/x.tgz", "--target-catalog-source", "platform"},
		},
		{
			name:     "env creds appended after skip-push",
			skipPush: true,
			envUser:  "u",
			envPass:  "p",
			want:     []string{"push", "/tmp/x.tgz", "--target-catalog-source", "platform", "--skip-push", "--username", "u", "--password", "p"},
		},
		{
			name:    "only username present (rare, but accepted)",
			envUser: "u",
			want:    []string{"push", "/tmp/x.tgz", "--target-catalog-source", "platform", "--username", "u"},
		},
		{
			name:     "private-push flow: skip-push=false + pushArgs",
			pushArgs: []string{"--dest-repo", "registry.private/devops", "--plain", "--force"},
			want:     []string{"push", "/tmp/x.tgz", "--target-catalog-source", "platform", "--dest-repo", "registry.private/devops", "--plain", "--force"},
		},
		{
			name:     "push args land after credentials",
			skipPush: true,
			envUser:  "u",
			envPass:  "p",
			pushArgs: []string{"--force"},
			want:     []string{"push", "/tmp/x.tgz", "--target-catalog-source", "platform", "--skip-push", "--username", "u", "--password", "p", "--force"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(EnvVioletRegistryUsername, tc.envUser)
			t.Setenv(EnvVioletRegistryPassword, tc.envPass)
			got := BuildVioletPushArgs("/tmp/x.tgz", tc.skipPush, tc.pushArgs)
			if !stringSliceEqual(got, tc.want) {
				t.Errorf("args mismatch:\n got: %v\nwant: %v", got, tc.want)
			}
		})
	}
}

func TestMaskCommand(t *testing.T) {
	tests := []struct {
		name string
		in   []string
		want string
	}{
		{
			name: "password value masked",
			in:   []string{"push", "x.tgz", "--username", "u", "--password", "secret", "--skip-push"},
			want: "violet push x.tgz --username u --password *** --skip-push",
		},
		{
			name: "no password flag is a no-op",
			in:   []string{"push", "x.tgz", "--skip-push"},
			want: "violet push x.tgz --skip-push",
		},
		{
			name: "dangling --password (no following token) is preserved verbatim",
			in:   []string{"push", "x.tgz", "--password"},
			want: "violet push x.tgz --password",
		},
		{
			name: "multiple --password occurrences each get masked",
			in:   []string{"--password", "a", "--password", "b"},
			want: "violet --password *** --password ***",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := MaskCommand("violet", tc.in); got != tc.want {
				t.Errorf("\n got: %s\nwant: %s", got, tc.want)
			}
		})
	}
}

func TestVerifySha256(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "blob")
	payload := []byte("hello violet")
	if err := os.WriteFile(fp, payload, 0o644); err != nil {
		t.Fatal(err)
	}
	correct := computeSha256Hex(payload)

	t.Run("empty expected skips verification", func(t *testing.T) {
		if err := VerifySha256(fp, ""); err != nil {
			t.Errorf("expected nil, got %v", err)
		}
	})
	t.Run("correct sha passes", func(t *testing.T) {
		if err := VerifySha256(fp, correct); err != nil {
			t.Errorf("expected nil for correct sha, got %v", err)
		}
	})
	t.Run("uppercase hex still matches (case-insensitive)", func(t *testing.T) {
		if err := VerifySha256(fp, strings.ToUpper(correct)); err != nil {
			t.Errorf("uppercase hex should match, got %v", err)
		}
	})
	t.Run("wrong sha returns mismatch error", func(t *testing.T) {
		err := VerifySha256(fp, "deadbeef")
		if err == nil {
			t.Fatal("expected mismatch error, got nil")
		}
		if !strings.Contains(err.Error(), "sha256 mismatch") {
			t.Errorf("error should mention 'sha256 mismatch', got %v", err)
		}
	})
	t.Run("missing file returns open error", func(t *testing.T) {
		if err := VerifySha256(filepath.Join(dir, "missing"), "abc"); err == nil {
			t.Error("expected open error, got nil")
		}
	})
}

// computeSha256Hex mirrors VerifySha256's hashing so the test can assert the
// "correct sha passes" path without hardcoding a digest.
func computeSha256Hex(b []byte) string {
	h := sha256.New()
	h.Write(b)
	return hex.EncodeToString(h.Sum(nil))
}

func stringSliceEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
