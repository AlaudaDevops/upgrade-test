package operatorhub

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"strings"
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
