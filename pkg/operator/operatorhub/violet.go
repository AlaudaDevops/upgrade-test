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
// VIOLET_REGISTRY_* covers --skip-push=false push-to-registry flow;
// VIOLET_PLATFORM_* covers ACP API authentication for AV creation.
const (
	EnvVioletRegistryUsername = "VIOLET_REGISTRY_USERNAME"
	EnvVioletRegistryPassword = "VIOLET_REGISTRY_PASSWORD"
	EnvVioletPlatformUsername = "VIOLET_PLATFORM_USERNAME"
	EnvVioletPlatformPassword = "VIOLET_PLATFORM_PASSWORD"
)

// violet CLI flag names. Kept private so the consuming code stays expressive
// (`flagSkipPush` rather than the raw string everywhere).
const (
	flagSkipPush            = "--skip-push"
	flagForce               = "--force"
	flagTargetCatalogSource = "--target-catalog-source"
	flagClusters            = "--clusters"
	flagUsername            = "--username"
	flagPassword            = "--password"
	flagPlatformAddress     = "--platform-address"
	flagPlatformUsername    = "--platform-username"
	flagPlatformPassword    = "--platform-password"
)

// isPasswordFlag reports whether `arg` is a violet flag whose immediately
// following argv token is a credential — MaskCommand redacts each in logs.
// argv-mode leakage to OS (ps auxe / /proc) remains as documented in the README.
func isPasswordFlag(arg string) bool {
	return arg == flagPassword || arg == flagPlatformPassword
}

// credentialFlagsForbiddenInPushArgs lists violet flags that the upgrade CLI
// will not let through Violet.PushArgs. The contract is "credentials only via
// env vars" — letting a user smuggle --password / --platform-password via
// config.yaml would silently put a real secret into git and into the OS argv
// table without any redaction the env-injection path provides.
var credentialFlagsForbiddenInPushArgs = []string{
	flagUsername, flagPassword,
	flagPlatformUsername, flagPlatformPassword,
}

// validatePushArgs rejects any attempt to smuggle a credential flag through
// Violet.PushArgs, in both the bare "--password value" form and the
// "--password=value" form. Returns an error naming the offending argument.
func validatePushArgs(args []string) error {
	for _, arg := range args {
		flag := arg
		if i := strings.IndexByte(arg, '='); i > 0 {
			flag = arg[:i]
		}
		for _, forbidden := range credentialFlagsForbiddenInPushArgs {
			if flag == forbidden {
				return fmt.Errorf("violet.pushArgs must not contain credential flag %q; "+
					"set VIOLET_REGISTRY_USERNAME / _PASSWORD / VIOLET_PLATFORM_USERNAME / _PASSWORD environment variables instead", arg)
			}
		}
	}
	return nil
}

// BuildPackageURL composes the .tgz URL by the agreed MinIO convention:
//
//	<prefix>/<name>/<pathChannel>/<name>.<fileChannel>.ALL.<bundleVersion>.tgz
//
// pathChannel is the directory segment (Version.EffectivePackageChannel — may
// be a release-train name like "v4.0" / "rc"). fileChannel is the in-filename
// segment, which uploaders set to the OLM channel (Version.Channel, typically
// "stable"). The two diverge when packageChannel is used to fan out path
// hierarchies, but every uploader keeps the filename tied to the OLM channel.
//
// A trailing slash on prefix is tolerated. All five inputs are required; any
// empty value returns an error so callers fail loudly instead of producing a
// malformed URL that 404s later.
func BuildPackageURL(prefix, name, pathChannel, fileChannel, bundleVersion string) (string, error) {
	switch {
	case prefix == "":
		return "", fmt.Errorf("packagePrefix is empty")
	case name == "":
		return "", fmt.Errorf("operator name is empty")
	case pathChannel == "":
		return "", fmt.Errorf("path channel is empty (Version.EffectivePackageChannel is required when using violet)")
	case fileChannel == "":
		return "", fmt.Errorf("file channel is empty (Version.Channel is required when using violet)")
	case bundleVersion == "":
		return "", fmt.Errorf("bundleVersion is empty")
	}
	p := strings.TrimRight(prefix, "/")
	return fmt.Sprintf("%s/%s/%s/%s.%s.ALL.%s.tgz", p, name, pathChannel, name, fileChannel, bundleVersion), nil
}

// VioletPushParams is the resolved, pointer-free shape of `violet push`
// inputs. The caller (execVioletPush) is responsible for dereferencing the
// *bool defaults in config.VioletConfig before populating this struct, so
// BuildVioletPushArgs remains a pure transform — it never touches the
// filesystem, never starts a process, and never reaches into config.
type VioletPushParams struct {
	TgzPath         string
	SkipPush        bool
	PlatformAddress string
	Clusters        string
	// PlatformUsername / PlatformPassword come from VioletConfig and, when
	// non-empty, override the env-var fallback. See BuildVioletPushArgs for
	// the precedence rule.
	PlatformUsername string
	PlatformPassword string
	PushArgs         []string
}

// BuildVioletPushArgs assembles the argv for `violet push <tgz>`.
//
// Credentials precedence:
//   - Platform: VioletPushParams.PlatformUsername / PlatformPassword win when
//     non-empty; otherwise fall back to VIOLET_PLATFORM_USERNAME / _PASSWORD.
//     Mapped to --platform-username / --platform-password.
//   - Registry: env-only — VIOLET_REGISTRY_USERNAME / _PASSWORD → --username / --password.
//     Used in private-registry push flow (skipPush:false).
//
// PushArgs is appended verbatim so unsupported flags can still be threaded
// through without a code change.
func BuildVioletPushArgs(p VioletPushParams) ([]string, error) {
	if err := validatePushArgs(p.PushArgs); err != nil {
		return nil, err
	}
	// --force is always set: without it violet aborts AV upserts with
	// "already exist, skip it" and creates nothing, defeating the entire
	// flow. There is no realistic scenario where the upgrade CLI wants to
	// preserve a stale AV — if such a need arises, the caller already
	// deletes the residue before invoking violet.
	args := []string{"push", p.TgzPath, flagTargetCatalogSource, targetCatalogSource, flagForce}
	if p.SkipPush {
		args = append(args, flagSkipPush)
	}
	if p.PlatformAddress != "" {
		args = append(args, flagPlatformAddress, p.PlatformAddress)
	}
	if p.Clusters != "" {
		args = append(args, flagClusters, p.Clusters)
	}
	if u := os.Getenv(EnvVioletRegistryUsername); u != "" {
		args = append(args, flagUsername, u)
	}
	if p2 := os.Getenv(EnvVioletRegistryPassword); p2 != "" {
		args = append(args, flagPassword, p2)
	}
	platformUser := p.PlatformUsername
	if platformUser == "" {
		platformUser = os.Getenv(EnvVioletPlatformUsername)
	}
	if platformUser != "" {
		args = append(args, flagPlatformUsername, platformUser)
	}
	platformPass := p.PlatformPassword
	if platformPass == "" {
		platformPass = os.Getenv(EnvVioletPlatformPassword)
	}
	if platformPass != "" {
		args = append(args, flagPlatformPassword, platformPass)
	}
	args = append(args, p.PushArgs...)
	return args, nil
}

// MaskCommand renders the command for logging, replacing the token following
// any sensitive-password flag (--password, --platform-password) with `***`.
// This only protects log output — the credential is still visible to OS-level
// inspection (e.g. `ps auxe`) once the child process runs. The README
// documents that risk for shared CI runners.
func MaskCommand(name string, args []string) string {
	parts := make([]string, 0, len(args)+1)
	parts = append(parts, name)
	for i := 0; i < len(args); i++ {
		if isPasswordFlag(args[i]) && i+1 < len(args) {
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

	url, err := BuildPackageURL(o.violet.PackagePrefix, o.name, version.EffectivePackageChannel(), version.Channel, version.BundleVersion)
	if err != nil {
		return nil, "", fmt.Errorf("build package url: %w", err)
	}

	tgzPath, cleanup, err := o.acquirePackage(ctx, url, version)
	if err != nil {
		return nil, "", fmt.Errorf("acquire package %s: %w", url, err)
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

// acquirePackage returns a local .tgz path for `version` and a cleanup
// callback to release any transient resources. When VioletConfig.LocalPackageDir
// is non-empty the on-disk cache layout mirrors the MinIO URL convention; on
// hit the HTTP fetch is skipped, on miss the file is downloaded directly into
// the cache path (so subsequent runs hit). The returned cleanup is a no-op in
// the cache path because surviving the run is the whole point.
//
// When LocalPackageDir is empty the legacy flow runs: download to a per-call
// /tmp directory and remove on cleanup, no cross-run reuse.
func (o *Operator) acquirePackage(ctx context.Context, rawURL string, version config.Version) (string, func(), error) {
	log := logging.FromContext(ctx)
	noop := func() {}

	if o.violet.LocalPackageDir != "" {
		cachePath := filepath.Join(
			o.violet.LocalPackageDir,
			o.name,
			version.EffectivePackageChannel(),
			fmt.Sprintf("%s.%s.ALL.%s.tgz", o.name, version.Channel, version.BundleVersion),
		)
		if info, err := os.Stat(cachePath); err == nil && !info.IsDir() {
			log.Infow("reusing cached violet package", "path", cachePath, "size", info.Size())
			return cachePath, noop, nil
		} else if err != nil && !os.IsNotExist(err) {
			return "", nil, fmt.Errorf("stat cache %s: %w", cachePath, err)
		}
		log.Infow("downloading violet package to cache", "url", rawURL, "path", cachePath)
		if err := os.MkdirAll(filepath.Dir(cachePath), 0o755); err != nil {
			return "", nil, fmt.Errorf("mkdir cache dir: %w", err)
		}
		if err := downloadFile(ctx, rawURL, cachePath, o.timeout); err != nil {
			// Drop a half-written file so the next run does a clean retry
			// instead of falsely "hitting" the cache with a truncated blob.
			_ = os.Remove(cachePath)
			return "", nil, err
		}
		return cachePath, noop, nil
	}

	log.Infow("downloading violet package", "url", rawURL)
	return downloadToTemp(ctx, rawURL, o.timeout)
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

	if err := downloadFile(ctx, rawURL, filePath, timeout); err != nil {
		cleanup()
		return "", nil, err
	}
	return filePath, cleanup, nil
}

// downloadFile streams rawURL into destPath. The destination's parent
// directory must already exist (caller is responsible). On any error the
// caller should clean up destPath — downloadFile does not, because it does
// not know whether destPath is a throwaway tmp file or a persistent cache
// entry that the caller wants to retry into.
//
// timeout bounds the entire HTTP exchange (dial + headers + body) via a
// derived context, so a slow or half-open peer cannot pin the goroutine.
func downloadFile(ctx context.Context, rawURL, destPath string, timeout time.Duration) error {
	dlCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(dlCtx, http.MethodGet, rawURL, nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("http get: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("unexpected status %d from %s", resp.StatusCode, rawURL)
	}
	f, err := os.Create(destPath)
	if err != nil {
		return fmt.Errorf("create %s: %w", destPath, err)
	}
	// Explicit close-and-check: on many filesystems write buffers are only
	// flushed during Close(), so swallowing its error via defer can let a
	// truncated .tgz reach VerifySha256 (which is optional today) and then
	// violet push, producing a misleading "violet push:" error.
	if _, err := io.Copy(f, resp.Body); err != nil {
		_ = f.Close()
		return fmt.Errorf("copy body to %s: %w", destPath, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close %s after download: %w", destPath, err)
	}
	return nil
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
// essentials; the four VIOLET_* names cover registry + platform credential
// pairs. Exact enumeration (not a prefix glob) so introducing a new
// VIOLET_* env requires a code change reviewed alongside the env reader.
var violetEnvAllowlist = []string{
	"KUBECONFIG",
	"PATH",
	"HOME",
	"USER",
	EnvVioletRegistryUsername,
	EnvVioletRegistryPassword,
	EnvVioletPlatformUsername,
	EnvVioletPlatformPassword,
}

// execVioletPush runs `violet push <tgz>` as a child process. Credentials
// for the registry (VIOLET_REGISTRY_*) and ACP platform (VIOLET_PLATFORM_*)
// are auto-injected into argv by BuildVioletPushArgs; the rendered command
// is mask-logged before exec.
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
	args, err := BuildVioletPushArgs(VioletPushParams{
		TgzPath:          tgzPath,
		SkipPush:         skipPush,
		PlatformAddress:  o.violet.PlatformAddress,
		Clusters:         o.violet.Clusters,
		PlatformUsername: o.violet.PlatformUsername,
		PlatformPassword: o.violet.PlatformPassword,
		PushArgs:         o.violet.PushArgs,
	})
	if err != nil {
		return err
	}

	log.Infow("invoking violet", "cmd", MaskCommand(bin, args))

	// Also redact the credential values themselves from violet's own
	// stdout/stderr — MaskCommand only protects the line WE log, not what
	// violet itself might echo (e.g. verbose argv dumps). Resolve platform
	// password with the same precedence as BuildVioletPushArgs (config wins,
	// env falls back) so the redactor sees whichever value will actually be
	// passed to violet.
	platformPass := o.violet.PlatformPassword
	if platformPass == "" {
		platformPass = os.Getenv(EnvVioletPlatformPassword)
	}
	result := exec.RunCommand(ctx, exec.Command{
		Name:         bin,
		Args:         args,
		EnvAllowlist: violetEnvAllowlist,
		RedactSecrets: []string{
			os.Getenv(EnvVioletRegistryPassword),
			platformPass,
		},
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
