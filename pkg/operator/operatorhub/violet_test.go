package operatorhub

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/AlaudaDevops/upgrade-test/pkg/config"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/dynamic/fake"
)

func TestVersionEffectivePackageChannel(t *testing.T) {
	tests := []struct {
		name           string
		channel        string
		packageChannel string
		want           string
	}{
		{"packageChannel wins when set", "stable", "v4.0", "v4.0"},
		{"falls back to channel when packageChannel empty", "stable", "", "stable"},
		{"both empty returns empty (caller fails loudly downstream)", "", "", ""},
		{"different segments stay independent", "pipelines-4.6", "rc", "rc"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			v := config.Version{Channel: tc.channel, PackageChannel: tc.packageChannel}
			if got := v.EffectivePackageChannel(); got != tc.want {
				t.Errorf("EffectivePackageChannel() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestBuildPackageURL(t *testing.T) {
	tests := []struct {
		name          string
		prefix        string
		opName        string
		pathChannel   string
		fileChannel   string
		bundleVersion string
		want          string
		wantErr       bool
	}{
		{
			name:          "tektoncd v4.6 stable: path uses train, file uses OLM channel",
			prefix:        "http://package-minio.alauda.cn:9199/packages/",
			opName:        "tektoncd-operator",
			pathChannel:   "v4.6",
			fileChannel:   "stable",
			bundleVersion: "v4.6.0",
			want:          "http://package-minio.alauda.cn:9199/packages/tektoncd-operator/v4.6/tektoncd-operator.stable.ALL.v4.6.0.tgz",
		},
		{
			name:          "rc train still uses stable filename segment",
			prefix:        "http://package-minio.alauda.cn:9199/packages/",
			opName:        "tektoncd-operator",
			pathChannel:   "rc",
			fileChannel:   "stable",
			bundleVersion: "v4.2.5-rc.76.g976cff6",
			want:          "http://package-minio.alauda.cn:9199/packages/tektoncd-operator/rc/tektoncd-operator.stable.ALL.v4.2.5-rc.76.g976cff6.tgz",
		},
		{
			name:          "gitlab v17.11 stable: cross-operator parity",
			prefix:        "http://package-minio.alauda.cn:9199/packages/",
			opName:        "gitlab-ce-operator",
			pathChannel:   "v17.11",
			fileChannel:   "stable",
			bundleVersion: "v17.11.4",
			want:          "http://package-minio.alauda.cn:9199/packages/gitlab-ce-operator/v17.11/gitlab-ce-operator.stable.ALL.v17.11.4.tgz",
		},
		{
			name:          "prefix without trailing slash, two channels coincide",
			prefix:        "http://example.com/pkgs",
			opName:        "op",
			pathChannel:   "stable",
			fileChannel:   "stable",
			bundleVersion: "v1.0.0",
			want:          "http://example.com/pkgs/op/stable/op.stable.ALL.v1.0.0.tgz",
		},
		{name: "empty prefix", prefix: "", opName: "op", pathChannel: "stable", fileChannel: "stable", bundleVersion: "v1.0.0", wantErr: true},
		{name: "empty name", prefix: "http://x/", pathChannel: "stable", fileChannel: "stable", bundleVersion: "v1.0.0", wantErr: true},
		{name: "empty path channel", prefix: "http://x/", opName: "op", fileChannel: "stable", bundleVersion: "v1.0.0", wantErr: true},
		{name: "empty file channel", prefix: "http://x/", opName: "op", pathChannel: "stable", bundleVersion: "v1.0.0", wantErr: true},
		{name: "empty bundleVersion", prefix: "http://x/", opName: "op", pathChannel: "stable", fileChannel: "stable", wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := BuildPackageURL(tc.prefix, tc.opName, tc.pathChannel, tc.fileChannel, tc.bundleVersion)
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
		params   VioletPushParams
		regUser  string
		regPass  string
		platUser string
		platPass string
		want     []string
	}{
		{
			name:   "minimal: force is always present",
			params: VioletPushParams{TgzPath: "/tmp/x.tgz", SkipPush: true},
			want:   []string{"push", "/tmp/x.tgz", "--target-catalog-source", "platform", "--force", "--skip-push"},
		},
		{
			name:   "skip-push=false drops only that flag (push images path)",
			params: VioletPushParams{TgzPath: "/tmp/x.tgz", SkipPush: false},
			want:   []string{"push", "/tmp/x.tgz", "--target-catalog-source", "platform", "--force"},
		},
		{
			name:   "platform-address surfaces after skip-push",
			params: VioletPushParams{TgzPath: "/tmp/x.tgz", SkipPush: true, PlatformAddress: "https://example.acp"},
			want:   []string{"push", "/tmp/x.tgz", "--target-catalog-source", "platform", "--force", "--skip-push", "--platform-address", "https://example.acp"},
		},
		{
			name:   "clusters list lands after platform-address",
			params: VioletPushParams{TgzPath: "/tmp/x.tgz", SkipPush: true, PlatformAddress: "https://example.acp", Clusters: "devops"},
			want:   []string{"push", "/tmp/x.tgz", "--target-catalog-source", "platform", "--force", "--skip-push", "--platform-address", "https://example.acp", "--clusters", "devops"},
		},
		{
			name:    "registry creds env auto-injected as --username/--password",
			params:  VioletPushParams{TgzPath: "/tmp/x.tgz", SkipPush: true},
			regUser: "u",
			regPass: "p",
			want:    []string{"push", "/tmp/x.tgz", "--target-catalog-source", "platform", "--force", "--skip-push", "--username", "u", "--password", "p"},
		},
		{
			name:     "platform creds env auto-injected as --platform-username/--platform-password",
			params:   VioletPushParams{TgzPath: "/tmp/x.tgz", SkipPush: true},
			platUser: "admin@example.invalid",
			platPass: "redacted-password",
			want:     []string{"push", "/tmp/x.tgz", "--target-catalog-source", "platform", "--force", "--skip-push", "--platform-username", "admin@example.invalid", "--platform-password", "redacted-password"},
		},
		{
			name: "full shape: address + clusters + both creds + push-args",
			params: VioletPushParams{
				TgzPath: "/tmp/x.tgz", SkipPush: false,
				PlatformAddress: "https://example.acp",
				Clusters:        "devops",
				PushArgs:        []string{"--dest-repo", "registry.private/devops"},
			},
			regUser: "r-u", regPass: "r-p",
			platUser: "p-u", platPass: "p-p",
			want: []string{
				"push", "/tmp/x.tgz", "--target-catalog-source", "platform",
				"--force",
				"--platform-address", "https://example.acp",
				"--clusters", "devops",
				"--username", "r-u", "--password", "r-p",
				"--platform-username", "p-u", "--platform-password", "p-p",
				"--dest-repo", "registry.private/devops",
			},
		},
		{
			name:   "pushArgs always land last so user overrides win",
			params: VioletPushParams{TgzPath: "/tmp/x.tgz", SkipPush: true, PushArgs: []string{"--plain", "--image-pull-secret", "pull"}},
			want:   []string{"push", "/tmp/x.tgz", "--target-catalog-source", "platform", "--force", "--skip-push", "--plain", "--image-pull-secret", "pull"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(EnvVioletRegistryUsername, tc.regUser)
			t.Setenv(EnvVioletRegistryPassword, tc.regPass)
			t.Setenv(EnvVioletPlatformUsername, tc.platUser)
			t.Setenv(EnvVioletPlatformPassword, tc.platPass)
			got, err := BuildVioletPushArgs(tc.params)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !stringSliceEqual(got, tc.want) {
				t.Errorf("args mismatch:\n got: %v\nwant: %v", got, tc.want)
			}
		})
	}
}

func TestBuildVioletPushArgs_RejectsCredentialFlagsInPushArgs(t *testing.T) {
	// PushArgs is a free-form escape hatch but must never carry credentials —
	// the env-only contract is what makes log redaction meaningful. Both the
	// bare "--flag value" and combined "--flag=value" forms must be rejected.
	forbidden := []struct {
		name     string
		pushArgs []string
	}{
		{"bare --password", []string{"--password", "secret"}},
		{"bare --platform-password", []string{"--platform-password", "secret"}},
		{"bare --username", []string{"--username", "user"}},
		{"bare --platform-username", []string{"--platform-username", "user"}},
		{"--password=value form", []string{"--password=secret"}},
		{"--platform-password=value form", []string{"--platform-password=secret"}},
		{"credential flag mixed with safe flags", []string{"--plain", "--password", "secret", "--force"}},
	}
	for _, tc := range forbidden {
		t.Run(tc.name, func(t *testing.T) {
			_, err := BuildVioletPushArgs(VioletPushParams{
				TgzPath:  "/tmp/x.tgz",
				SkipPush: true,
				PushArgs: tc.pushArgs,
			})
			if err == nil {
				t.Fatalf("want rejection for pushArgs=%v, got nil", tc.pushArgs)
			}
			if !strings.Contains(err.Error(), "credential flag") {
				t.Errorf("error should mention 'credential flag', got %v", err)
			}
		})
	}
}

func TestBuildVioletPushArgs_AllowsNonCredentialFlags(t *testing.T) {
	// Sanity check: --plain / --image-pull-secret / --dest-repo and similar
	// non-sensitive flags must continue to flow through PushArgs unchanged
	// (the very reason PushArgs exists).
	_, err := BuildVioletPushArgs(VioletPushParams{
		TgzPath:  "/tmp/x.tgz",
		SkipPush: false,
		PushArgs: []string{"--plain", "--dest-repo", "registry.private/x", "--image-pull-secret", "pull"},
	})
	if err != nil {
		t.Fatalf("non-credential flags should pass: %v", err)
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
		{
			name: "--platform-password value masked too",
			in:   []string{"--platform-username", "admin@example.invalid", "--platform-password", "redacted-password"},
			want: "violet --platform-username admin@example.invalid --platform-password ***",
		},
		{
			name: "both --password and --platform-password masked in one command",
			in:   []string{"--username", "u", "--password", "rp", "--platform-username", "pu", "--platform-password", "pp"},
			want: "violet --username u --password *** --platform-username pu --platform-password ***",
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

// --- validateVioletBin --------------------------------------------------------

func TestValidateVioletBin(t *testing.T) {
	dir := t.TempDir()

	// Prepare three fixtures: an executable file, a non-executable file, and a directory.
	exePath := filepath.Join(dir, "violet")
	if err := os.WriteFile(exePath, []byte("#!/bin/sh\necho fake"), 0o755); err != nil {
		t.Fatal(err)
	}
	nonExePath := filepath.Join(dir, "not-exe")
	if err := os.WriteFile(nonExePath, []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}
	subDir := filepath.Join(dir, "subdir")
	if err := os.Mkdir(subDir, 0o755); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name       string
		path       string
		wantErrSub string // substring expected in the error message ("" = expect nil)
	}{
		{"absolute executable file is accepted", exePath, ""},
		{"relative path is rejected", "violet", "must be an absolute path"},
		{"missing file surfaces stat error", filepath.Join(dir, "missing"), "stat violet binary"},
		{"directory is rejected", subDir, "is a directory"},
		{"non-executable file is rejected", nonExePath, "is not executable"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validateVioletBin(tc.path)
			if tc.wantErrSub == "" {
				if err != nil {
					t.Errorf("want nil error, got %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("want error containing %q, got nil", tc.wantErrSub)
			}
			if !strings.Contains(err.Error(), tc.wantErrSub) {
				t.Errorf("error %q does not contain %q", err.Error(), tc.wantErrSub)
			}
		})
	}
}

// --- downloadToTemp -----------------------------------------------------------

func TestDownloadToTemp_Success(t *testing.T) {
	payload := []byte("hello violet bundle")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/pkg/tektoncd.tgz" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		_, _ = w.Write(payload)
	}))
	defer srv.Close()

	url := srv.URL + "/pkg/tektoncd.tgz"
	path, cleanup, err := downloadToTemp(context.Background(), url, 5*time.Second)
	if err != nil {
		t.Fatalf("downloadToTemp: %v", err)
	}
	defer cleanup()

	if filepath.Base(path) != "tektoncd.tgz" {
		t.Errorf("expected filename from URL, got %s", filepath.Base(path))
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(body) != string(payload) {
		t.Errorf("body mismatch: got %q, want %q", string(body), string(payload))
	}

	// cleanup must remove the file (and its temp dir).
	cleanup()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("cleanup should have removed %s; stat err=%v", path, err)
	}
}

func TestDownloadToTemp_404(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.NotFound(w, nil)
	}))
	defer srv.Close()

	_, _, err := downloadToTemp(context.Background(), srv.URL+"/missing.tgz", 5*time.Second)
	if err == nil {
		t.Fatal("want error for 404, got nil")
	}
	if !strings.Contains(err.Error(), "404") {
		t.Errorf("error should mention status 404, got %v", err)
	}
}

func TestDownloadToTemp_5xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	_, _, err := downloadToTemp(context.Background(), srv.URL+"/x.tgz", 5*time.Second)
	if err == nil {
		t.Fatal("want error for 500, got nil")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("error should mention status 500, got %v", err)
	}
}

func TestDownloadToTemp_DefaultFilename(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	// URL has no path beyond "/" → filepath.Base returns "/", which the
	// helper must rewrite to "package.tgz" so io.Copy has a real target.
	path, cleanup, err := downloadToTemp(context.Background(), srv.URL+"/", 5*time.Second)
	if err != nil {
		t.Fatalf("downloadToTemp: %v", err)
	}
	defer cleanup()
	if filepath.Base(path) != "package.tgz" {
		t.Errorf("expected fallback filename package.tgz, got %s", filepath.Base(path))
	}
}

func TestDownloadToTemp_ContextCancel(t *testing.T) {
	// Server blocks long enough for context cancel to fire before headers
	// are sent, ensuring the net/http request honours the context.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(2 * time.Second)
		_, _ = w.Write([]byte("late"))
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, _, err := downloadToTemp(ctx, srv.URL+"/slow.tgz", 5*time.Second)
	if err == nil {
		t.Fatal("want context error, got nil")
	}
	if !strings.Contains(err.Error(), "context") && !strings.Contains(err.Error(), "deadline") {
		t.Errorf("expected context/deadline error, got %v", err)
	}
}

func TestDownloadToTemp_TimeoutEnforcedWithoutParentDeadline(t *testing.T) {
	// Parent ctx has no deadline; downloadToTemp's own timeout must still
	// abort the request when the server stalls. Regression guard for "the
	// upgrade CLI passes a bare ctx and downloads hang on TCP keepalive".
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(2 * time.Second)
		_, _ = w.Write([]byte("late"))
	}))
	defer srv.Close()

	start := time.Now()
	_, _, err := downloadToTemp(context.Background(), srv.URL+"/slow.tgz", 80*time.Millisecond)
	if err == nil {
		t.Fatal("want deadline error, got nil")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("downloadToTemp should honour its own timeout (~80ms), took %v", elapsed)
	}
	if !strings.Contains(err.Error(), "context") && !strings.Contains(err.Error(), "deadline") {
		t.Errorf("expected context/deadline error, got %v", err)
	}
}

// --- deleteArtifactVersionIfExists --------------------------------------------

// newOperatorWithFakeClient builds a minimal *Operator backed by a fake
// dynamic client seeded with the supplied objects. Only fields touched by
// deleteArtifactVersionIfExists need to be populated.
func newOperatorWithFakeClient(t *testing.T, seed ...runtime.Object) *Operator {
	t.Helper()
	scheme := runtime.NewScheme()
	// Register ArtifactVersion as an unstructured list kind so the fake
	// client knows how to list/get/delete it.
	scheme.AddKnownTypeWithName(
		artifactVersionGVR.GroupVersion().WithKind("ArtifactVersion"),
		&unstructured.Unstructured{},
	)
	scheme.AddKnownTypeWithName(
		artifactVersionGVR.GroupVersion().WithKind("ArtifactVersionList"),
		&unstructured.UnstructuredList{},
	)

	listKinds := map[schemaGVR]string{
		{artifactVersionGVR.Group, artifactVersionGVR.Version, artifactVersionGVR.Resource}: "ArtifactVersionList",
	}
	_ = listKinds // placeholder if extending

	client := fake.NewSimpleDynamicClient(scheme, seed...)
	return &Operator{
		client:   client,
		name:     "tektoncd-operator",
		artifact: "operatorhub-tektoncd-operator",
		interval: 10 * time.Millisecond,
		timeout:  2 * time.Second,
	}
}

// schemaGVR is a local alias used only inside the test helper so we do not
// drag the full k8s schema package into the file headers when not needed.
type schemaGVR struct{ G, V, R string }

func newAVUnstructured(name string) *unstructured.Unstructured {
	return &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": artifactVersionGVR.GroupVersion().String(),
			"kind":       "ArtifactVersion",
			"metadata": map[string]interface{}{
				"name":      name,
				"namespace": systemNamespace,
			},
			"spec": map[string]interface{}{
				"present": true,
				"tag":     "v4.6.0",
			},
		},
	}
}

func TestDeleteArtifactVersionIfExists_NoResidueIsNoop(t *testing.T) {
	op := newOperatorWithFakeClient(t)
	if err := op.deleteArtifactVersionIfExists(context.Background(), "missing.av"); err != nil {
		t.Errorf("expected nil for missing AV (NotFound is fine), got %v", err)
	}
}

func TestDeleteArtifactVersionIfExists_RemovesExistingAV(t *testing.T) {
	avName := "operatorhub-tektoncd-operator.v4.6.0"
	op := newOperatorWithFakeClient(t, newAVUnstructured(avName))

	// Sanity: the seed object is visible before delete.
	got, err := op.client.Resource(artifactVersionGVR).Namespace(systemNamespace).Get(
		context.Background(), avName, metav1.GetOptions{},
	)
	if err != nil || got == nil {
		t.Fatalf("seed AV should be present, got err=%v", err)
	}

	if err := op.deleteArtifactVersionIfExists(context.Background(), avName); err != nil {
		t.Fatalf("delete: %v", err)
	}

	// After delete + poll the resource must be gone.
	_, err = op.client.Resource(artifactVersionGVR).Namespace(systemNamespace).Get(
		context.Background(), avName, metav1.GetOptions{},
	)
	if !errors.IsNotFound(err) {
		t.Errorf("after delete, expected NotFound, got %v", err)
	}
}

func TestDeleteArtifactVersionIfExists_ConcurrentRaceVanishesGracefully(t *testing.T) {
	// Simulates "Get says exists, but Delete returns NotFound because something
	// else removed it between the two calls". The helper must absorb that
	// race rather than propagating it as a hard error.
	avName := "operatorhub-tektoncd-operator.v4.6.0"
	op := newOperatorWithFakeClient(t, newAVUnstructured(avName))

	// Pre-delete the object so the helper's own Delete call sees NotFound.
	if err := op.client.Resource(artifactVersionGVR).Namespace(systemNamespace).
		Delete(context.Background(), avName, metav1.DeleteOptions{}); err != nil {
		t.Fatalf("pre-delete: %v", err)
	}

	// Helper will Get (NotFound → return nil) and exit cleanly.
	if err := op.deleteArtifactVersionIfExists(context.Background(), avName); err != nil {
		t.Errorf("expected nil for race-vanished AV, got %v", err)
	}
}
