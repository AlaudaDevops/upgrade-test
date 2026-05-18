package operatorhub

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/AlaudaDevops/upgrade-test/pkg/config"
	"github.com/AlaudaDevops/upgrade-test/pkg/exec"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/wait"
	"knative.dev/pkg/logging"
)

// Environment variables consumed when assembling `violet push`. They are
// intentionally not part of OperatorConfig: pipelines inject them as secrets
// and the upgrade CLI must not persist them to disk or echo them back.
const (
	EnvVioletRegistryUsername = "VIOLET_REGISTRY_USERNAME"
	EnvVioletRegistryPassword = "VIOLET_REGISTRY_PASSWORD"
)

// violet CLI flag names. Kept private so the consuming code stays expressive
// (`flagSkipPush` rather than the raw string everywhere).
const (
	flagSkipPush            = "--skip-push"
	flagTargetCatalogSource = "--target-catalog-source"
	flagUsername            = "--username"
	flagPassword            = "--password"
)

// BuildPackageURL composes the .tgz URL by the agreed MinIO convention:
//
//	<prefix>/<name>/<channel>/<name>.latest.ALL.<bundleVersion>.tgz
//
// A trailing slash on prefix is tolerated. All four inputs are required; any
// empty value returns an error so callers fail loudly instead of producing a
// malformed URL that 404s later.
func BuildPackageURL(prefix, name, channel, bundleVersion string) (string, error) {
	switch {
	case prefix == "":
		return "", fmt.Errorf("packagePrefix is empty")
	case name == "":
		return "", fmt.Errorf("operator name is empty")
	case channel == "":
		return "", fmt.Errorf("channel is empty (Version.Channel is required when using violet)")
	case bundleVersion == "":
		return "", fmt.Errorf("bundleVersion is empty")
	}
	p := strings.TrimRight(prefix, "/")
	return fmt.Sprintf("%s/%s/%s/%s.latest.ALL.%s.tgz", p, name, channel, name, bundleVersion), nil
}

// BuildVioletPushArgs assembles the argv for `violet push <tgz>`. Credentials
// are read from EnvVioletRegistryUsername / EnvVioletRegistryPassword and
// injected as --username / --password when non-empty; pushArgs is appended
// verbatim. The function is a pure transform — it never touches the filesystem
// or starts a process — so callers can table-test the exact argv shape.
//
// The decision of skipPush belongs to the caller (it has already deferenced
// VioletConfig.SkipPush and applied the "nil == true" default in config), so
// this function takes a plain bool.
func BuildVioletPushArgs(tgzPath string, skipPush bool, pushArgs []string) []string {
	args := []string{"push", tgzPath, flagTargetCatalogSource, targetCatalogSource}
	if skipPush {
		args = append(args, flagSkipPush)
	}
	if u := os.Getenv(EnvVioletRegistryUsername); u != "" {
		args = append(args, flagUsername, u)
	}
	if p := os.Getenv(EnvVioletRegistryPassword); p != "" {
		args = append(args, flagPassword, p)
	}
	args = append(args, pushArgs...)
	return args
}

// MaskCommand renders the command for logging, replacing the token following
// --password with `***`. This only protects log output — the credential is
// still visible to OS-level inspection (e.g. `ps auxe`) once the child process
// runs. The README must document that risk for shared CI runners.
func MaskCommand(name string, args []string) string {
	parts := make([]string, 0, len(args)+1)
	parts = append(parts, name)
	for i := 0; i < len(args); i++ {
		if args[i] == flagPassword && i+1 < len(args) {
			parts = append(parts, args[i], "***")
			i++
			continue
		}
		parts = append(parts, args[i])
	}
	return strings.Join(parts, " ")
}

// VerifySha256 streams the file at filePath and compares its SHA-256 against
// expected (hex, case-insensitive). An empty expected disables verification —
// callers can pass Version.ExpectedSha256 directly without nil-checking. The
// returned error names the path so failures are actionable without extra
// wrapping at the call site.
func VerifySha256(filePath, expected string) error {
	if expected == "" {
		return nil
	}
	f, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("open %s: %w", filePath, err)
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return fmt.Errorf("read %s: %w", filePath, err)
	}
	actual := hex.EncodeToString(h.Sum(nil))
	if !strings.EqualFold(actual, expected) {
		return fmt.Errorf("sha256 mismatch for %s: expected %s, got %s", filePath, expected, actual)
	}
	return nil
}

// installViaViolet onboards the bundle for `version` by invoking the external
// `violet push` binary, then waits for the resulting ArtifactVersion CR to
// reach phase=Present and for the OLM PackageManifest to expose the matching
// CSV. The upstream Artifact CR is expected to already exist (managed out of
// band); only the ArtifactVersion write path is delegated to violet.
//
// Returns (av, csv, err) so callers do not have to re-read status.version
// from the unstructured object — a second extraction site was prone to
// silently feeding an empty csv to InstallSubscription.
func (o *Operator) installViaViolet(ctx context.Context, version config.Version) (*unstructured.Unstructured, string, error) {
	log := logging.FromContext(ctx)
	log.Infow("installing artifact version via violet",
		"bundleVersion", version.BundleVersion, "channel", version.Channel)

	artifact, err := o.GetResource(ctx, o.artifact, systemNamespace, artifactGVR)
	if err != nil {
		return nil, "", fmt.Errorf("get artifact %s: %w", o.artifact, err)
	}

	url, err := BuildPackageURL(o.violet.PackagePrefix, o.name, version.EffectivePackageChannel(), version.BundleVersion)
	if err != nil {
		return nil, "", fmt.Errorf("build package url: %w", err)
	}
	log.Infow("downloading violet package", "url", url)

	tgzPath, cleanup, err := downloadToTemp(ctx, url, o.timeout)
	if err != nil {
		return nil, "", fmt.Errorf("download %s: %w", url, err)
	}
	defer cleanup()

	if err := VerifySha256(tgzPath, version.ExpectedSha256); err != nil {
		return nil, "", fmt.Errorf("verify sha256: %w", err)
	}

	avName := fmt.Sprintf("%s.%s", artifact.GetName(), version.BundleVersion)
	if err := o.deleteArtifactVersionIfExists(ctx, avName); err != nil {
		return nil, "", fmt.Errorf("ensure clean AV %s: %w", avName, err)
	}

	if err := o.execVioletPush(ctx, tgzPath); err != nil {
		return nil, "", fmt.Errorf("violet push: %w", err)
	}

	log.Infow("waiting for ArtifactVersion to reach Present", "name", avName)
	av, err := o.waitArtifactVersionPresent(ctx, avName)
	if err != nil {
		return nil, "", fmt.Errorf("wait artifact version present: %w", err)
	}

	// Cross-check: violet might use a naming convention we did not expect. If
	// the CR we waited on has a different spec.tag than the requested
	// BundleVersion, surface it loudly rather than silently accepting a stale
	// or unrelated AV.
	if tag, _, _ := unstructured.NestedString(av.Object, "spec", "tag"); tag != version.BundleVersion {
		return nil, "", fmt.Errorf("artifact version %s has spec.tag=%q, expected %q (violet may not follow the <artifact>.<bundleVersion> naming convention)",
			avName, tag, version.BundleVersion)
	}

	csv, found, _ := unstructured.NestedString(av.Object, "status", "version")
	if !found || csv == "" {
		return nil, "", fmt.Errorf("artifact version %s status.version is empty", avName)
	}
	log.Infow("waiting for package manifest", "csv", csv)
	if err := o.waitPackageManifest(ctx, csv); err != nil {
		return nil, "", fmt.Errorf("wait package manifest for csv %s: %w", csv, err)
	}

	log.Infow("artifact version installed via violet", "name", av.GetName())
	return av, csv, nil
}

// downloadToTemp fetches rawURL into a fresh temporary directory and returns
// the local file path plus a cleanup function. The caller MUST invoke cleanup
// (typically via defer) once the file is no longer needed. The temp dir is
// per-call so concurrent installs cannot race on the same path.
//
// `timeout` bounds the entire HTTP exchange (dial + headers + body). It is
// applied via a derived context so a slow or half-open MinIO peer cannot pin
// the goroutine for hours waiting on OS-level TCP keepalive.
func downloadToTemp(ctx context.Context, rawURL string, timeout time.Duration) (string, func(), error) {
	dlCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	dir, err := os.MkdirTemp("", "upgrade-violet-*")
	if err != nil {
		return "", nil, fmt.Errorf("mkdir temp: %w", err)
	}
	cleanup := func() { _ = os.RemoveAll(dir) }

	// Use net/url + path.Base to extract the last segment of the URL path
	// itself; filepath.Base("http://host/x.tgz") would return the host on
	// some platforms and never the file name. path.Base is also the right
	// operator for forward-slash URL paths regardless of OS.
	fileName := "package.tgz"
	if parsed, perr := url.Parse(rawURL); perr == nil {
		if base := path.Base(parsed.Path); base != "" && base != "." && base != "/" {
			fileName = base
		}
	}
	filePath := filepath.Join(dir, fileName)

	req, err := http.NewRequestWithContext(dlCtx, http.MethodGet, rawURL, nil)
	if err != nil {
		cleanup()
		return "", nil, fmt.Errorf("build request: %w", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		cleanup()
		return "", nil, fmt.Errorf("http get: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		cleanup()
		return "", nil, fmt.Errorf("unexpected status %d from %s", resp.StatusCode, rawURL)
	}

	f, err := os.Create(filePath)
	if err != nil {
		cleanup()
		return "", nil, fmt.Errorf("create %s: %w", filePath, err)
	}

	// Explicit close-and-check: on many filesystems write buffers are only
	// flushed during Close(), so swallowing its error via defer can let a
	// truncated .tgz reach VerifySha256 (which is optional today) and then
	// violet push, producing a misleading "violet push:" error.
	if _, err := io.Copy(f, resp.Body); err != nil {
		_ = f.Close()
		cleanup()
		return "", nil, fmt.Errorf("copy body to %s: %w", filePath, err)
	}
	if err := f.Close(); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("close %s after download: %w", filePath, err)
	}

	return filePath, cleanup, nil
}

// deleteArtifactVersionIfExists removes a stale ArtifactVersion with the same
// name and polls until the API confirms the deletion. This prevents
// waitArtifactVersionPresent from instantly matching a residue object from a
// previous upgrade attempt and reporting a false success.
func (o *Operator) deleteArtifactVersionIfExists(ctx context.Context, name string) error {
	log := logging.FromContext(ctx)

	existing, err := o.GetResource(ctx, name, systemNamespace, artifactVersionGVR)
	if err != nil {
		if errors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("get existing AV: %w", err)
	}

	log.Infow("deleting residue ArtifactVersion before invoking violet",
		"name", existing.GetName(), "uid", existing.GetUID())

	if err := o.client.Resource(artifactVersionGVR).Namespace(systemNamespace).
		Delete(ctx, name, metav1.DeleteOptions{}); err != nil && !errors.IsNotFound(err) {
		return fmt.Errorf("delete: %w", err)
	}

	return wait.PollUntilContextTimeout(ctx, o.interval, o.timeout, true, func(ctx context.Context) (bool, error) {
		_, err := o.client.Resource(artifactVersionGVR).Namespace(systemNamespace).
			Get(ctx, name, metav1.GetOptions{})
		if errors.IsNotFound(err) {
			return true, nil
		}
		return false, err
	})
}

// violetEnvAllowlist constrains which host environment variables flow into
// the violet child process. KUBECONFIG / PATH / HOME / USER are operational
// essentials; VIOLET_* covers VIOLET_REGISTRY_USERNAME / _PASSWORD and any
// future violet-specific overrides without leaking unrelated CI secrets such
// as GITHUB_TOKEN or AWS_*.
var violetEnvAllowlist = []string{
	"KUBECONFIG",
	"PATH",
	"HOME",
	"USER",
	"VIOLET_*",
}

// execVioletPush runs `violet push <tgz>` as a child process. Credentials
// from VIOLET_REGISTRY_USERNAME / _PASSWORD are auto-injected into argv by
// BuildVioletPushArgs; the rendered command is mask-logged before exec.
func (o *Operator) execVioletPush(ctx context.Context, tgzPath string) error {
	log := logging.FromContext(ctx)

	bin := o.violet.Bin
	if bin == "" {
		bin = "violet"
	} else if err := validateVioletBin(bin); err != nil {
		return err
	}

	skipPush := true
	if o.violet.SkipPush != nil {
		skipPush = *o.violet.SkipPush
	}
	args := BuildVioletPushArgs(tgzPath, skipPush, o.violet.PushArgs)

	log.Infow("invoking violet", "cmd", MaskCommand(bin, args))

	result := exec.RunCommand(ctx, exec.Command{
		Name:         bin,
		Args:         args,
		EnvAllowlist: violetEnvAllowlist,
	})
	return result.Err
}

// validateVioletBin guards against accidentally executing a binary at an
// arbitrary user-supplied path. When Violet.Bin is non-empty it must be an
// absolute path to an executable regular file; otherwise return a typed
// error so the upgrade fails before any network or cluster action.
func validateVioletBin(path string) error {
	if !filepath.IsAbs(path) {
		return fmt.Errorf("violet.bin must be an absolute path, got %q", path)
	}
	fi, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("stat violet binary %s: %w", path, err)
	}
	if fi.IsDir() {
		return fmt.Errorf("violet.bin %s is a directory, not an executable", path)
	}
	if fi.Mode()&0o111 == 0 {
		return fmt.Errorf("violet.bin %s is not executable", path)
	}
	return nil
}
