// pkg/config/config.go

package config

import (
	"fmt"
	"os"
	"regexp"
	"time"

	"gopkg.in/yaml.v2"
)

// bundleVersionRegex bounds what may appear in Version.BundleVersion. The
// field is interpolated into shell command templates (kubectl cleanup hints
// emitted by preflight) and into violet argv, so anything beyond
// [a-zA-Z0-9._-] risks shell metacharacter injection at the user's terminal.
// The chosen set covers every real bundle version observed in practice
// (`v4.6.3`, `4.6.3-rc.91`, `v0.74.0`) while leaving no room for `$`, backticks,
// quotes, spaces, or `;`.
var bundleVersionRegex = regexp.MustCompile(`^[a-zA-Z0-9._-]+$`)

const (
	defaultOperatorNamespace = "testing-upgrade-namespace"
	defaultSystemNamespace   = "cpaas-system"
)

// Config represents the main configuration structure
type Config struct {
	UpgradePaths []UpgradePath `yaml:"upgradePaths,omitempty"`
	// immediate indicates whether to run the upgrade tests immediately without waiting
	Immediate bool `yaml:"immediate,omitempty"`
	// logLevel is the log level to use
	LogLevel string `yaml:"logLevel,omitempty"`

	// operatorConfig is the configuration for the operator
	OperatorConfig OperatorConfig `yaml:"operatorConfig,omitempty"`
}

type OperatorConfig struct {
	// type is the type of the operator, support operatorhub and local, default is operatorhub
	Type string `yaml:"type,omitempty"`

	// artifact is the name of the artifact to use
	Artifact string `yaml:"artifact,omitempty"`
	// operatorNamespace is the namespace to use for the operator
	Namespace string `yaml:"namespace,omitempty"`
	// operatorName is the name of the operator to use
	Name string `yaml:"name,omitempty"`
	// workspace is the path to the workspace directory
	Workspace string `yaml:"workspace,omitempty"`

	// artifactPrefix is the prefix of the artifact to use, default is "operatorhub"
	ArtifactPrefix string `yaml:"artifactPrefix,omitempty"`

	// interval is the interval to use for the operator, default is 5 seconds
	Interval time.Duration `yaml:"interval,omitempty"`
	// timeout is the timeout to use for the operator, default is 10 minutes
	Timeout time.Duration `yaml:"timeout,omitempty"`

	// command for running the operator, just for local operator, default is "make deploy"
	Command string `yaml:"command,omitempty"`

	// Violet configures how the operatorhub flow invokes the external `violet`
	// binary to create Artifact / ArtifactVersion CRs. When nil, the legacy
	// in-process path is used (kept for the local operator and for transitional
	// builds; will be removed once violet path is the only one).
	Violet *VioletConfig `yaml:"violet,omitempty"`
}

// VioletConfig captures everything upgrade CLI needs to invoke `violet push`
// for the operatorhub onboarding flow.
type VioletConfig struct {
	// Bin is an optional absolute path to the violet binary. Empty falls back
	// to looking up `violet` in $PATH.
	Bin string `yaml:"bin,omitempty"`

	// PackagePrefix is the MinIO (or HTTP) root from which the per-version
	// .tgz is downloaded. Required when Violet is configured — the prefix
	// varies across environments (private vs shared MinIO, regional mirrors)
	// so the CLI deliberately refuses to hardcode a default. Empty prefix
	// surfaces as a "packagePrefix is empty" error at URL build time.
	PackagePrefix string `yaml:"packagePrefix,omitempty"`

	// SkipPush controls whether `--skip-push` is passed to `violet push`.
	// Pointer so we can distinguish "unset" (treated as true) from "explicit
	// false" (private-registry scenario that wants violet to also push images).
	SkipPush *bool `yaml:"skipPush,omitempty"`

	// PushArgs are extra arguments appended verbatim to `violet push`, used to
	// inject options such as --dest-repo, --plain, --image-pull-secret
	// in private-registry scenarios. Credentials must come from environment
	// variables (VIOLET_REGISTRY_USERNAME / VIOLET_REGISTRY_PASSWORD,
	// VIOLET_PLATFORM_USERNAME / VIOLET_PLATFORM_PASSWORD), not here.
	PushArgs []string `yaml:"pushArgs,omitempty"`

	// PlatformAddress is the ACP platform URL violet authenticates against
	// (e.g. https://devops-env1-hcvt43--idp.alaudatech.net). Required in
	// real-cluster integrations — violet refuses to start without it. Not a
	// credential so it lives in config rather than env.
	PlatformAddress string `yaml:"platformAddress,omitempty"`

	// Clusters names the target subcluster(s) for the Artifact/AV write —
	// violet defaults to "global", but multi-cluster ACP deployments expose
	// a per-workload subcluster (e.g. "devops") that kubectl is also pointed
	// at. A mismatch silently writes to the wrong place: violet reports
	// "updated successfully" while the CRs never appear in the cluster the
	// upgrade CLI is watching.
	Clusters string `yaml:"clusters,omitempty"`

	// PlatformUsername / PlatformPassword authenticate violet against the
	// ACP platform for Artifact/AV writes. They take precedence over the
	// environment variables VIOLET_PLATFORM_USERNAME / _PASSWORD, which
	// remain supported as a fallback for CI/pipeline injection. When both
	// are set, the config value wins.
	//
	// WARNING: writing credentials here will commit them to git if the
	// config file is checked in. Prefer environment variables in shared
	// repos; reserve this field for local one-off runs or for configs
	// stored outside source control.
	PlatformUsername string `yaml:"platformUsername,omitempty"`
	PlatformPassword string `yaml:"platformPassword,omitempty"`

	// LocalPackageDir, when non-empty, is the on-disk cache root for
	// downloaded .tgz packages. Layout mirrors the MinIO URL convention:
	//
	//	<LocalPackageDir>/<operatorName>/<packageChannel>/<operatorName>.latest.ALL.<bundleVersion>.tgz
	//
	// On cache hit the HTTP download is skipped. On miss the file is
	// downloaded directly into the cache path (parent dirs auto-created)
	// so subsequent runs hit the cache. SHA-256 verification (when
	// ExpectedSha256 is set) still runs against the cached file — a
	// corrupted cache entry will surface as a verification failure rather
	// than silently propagate. Relative paths resolve to the upgrade CLI
	// working directory.
	//
	// Leave empty to keep the legacy behavior: download to a per-call
	// /tmp dir and remove on exit (no cross-run reuse).
	LocalPackageDir string `yaml:"localPackageDir,omitempty"`
}

// UpgradePath represents a single upgrade path
type UpgradePath struct {
	// name is the name of the upgrade path
	Name string `yaml:"name,omitempty"`
	// versions is the list of versions to test
	Versions []Version `yaml:"versions,omitempty"`
}

// Version represents a single version in the upgrade path
type Version struct {
	// name is the name of the version
	Name string `yaml:"name,omitempty"`
	// bundleVersion is the version of the bundle to use
	BundleVersion string `yaml:"bundleVersion,omitempty"`
	// testCommand is the command to run to test the version. first version is "REPO=allure make prepare", other versions default is "REPO=allure make upgrade"
	TestCommand string `yaml:"testCommand,omitempty"`
	// testSubPath is the path to the test sub-directory, default is "testing"
	TestSubPath string `yaml:"testSubPath,omitempty"`
	// Channel is the OLM Subscription channel (e.g. "stable",
	// "pipelines-4.0", "latest"). Used verbatim as Subscription.spec.channel
	// when InstallSubscription installs the operator.
	Channel string `yaml:"channel,omitempty"`
	// PackageChannel is the MinIO repository path segment used by the violet
	// download URL — it is NOT the same as the OLM Channel. For example, on
	// the platform-tektoncd-operator the OLM channel is "stable" but the
	// MinIO segment is "v4.0" / "v4.2" / "rc" / etc.
	//
	// When empty, BuildPackageURL falls back to Channel for backward
	// compatibility with repositories where the two coincide. Set this
	// explicitly when the two differ.
	PackageChannel string `yaml:"packageChannel,omitempty"`
	// ExpectedSha256, when non-empty, is the lowercase hex SHA-256 of the
	// downloaded .tgz. The upgrade CLI verifies the digest after download and
	// fails fast on mismatch. Optional; leave empty to skip verification.
	ExpectedSha256 string `yaml:"expectedSha256,omitempty"`
}

// EffectivePackageChannel returns the MinIO URL segment to use for this
// version: PackageChannel when set, falling back to Channel when not. This
// keeps simple cases (where OLM channel name == MinIO segment) ergonomic
// while still letting callers split the two when they diverge.
func (v Version) EffectivePackageChannel() string {
	if v.PackageChannel != "" {
		return v.PackageChannel
	}
	return v.Channel
}

// LoadConfig loads the configuration from a YAML file, fills defaults, and
// validates that the result is internally consistent. Validation runs after
// defaulting so it sees the same shape the runtime will use — e.g. an empty
// OperatorConfig.Type already resolves to "operatorhub" by the time we
// check whether per-version Channel fields are required.
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var config Config
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, err
	}

	cfg := defaultConfig(&config)
	if err := validateConfig(cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

// validateConfig fails fast on shape errors that would otherwise only surface
// mid-upgrade (e.g. when path 2 step 3 happens to need the missing field).
// Catching them at load time avoids partially-completed upgrades that leave
// the cluster in an awkward state.
//
// Today this only enforces "Channel is required for operatorhub-type runs",
// but it is the right home for future cross-field checks (e.g. violet.bin
// existence, bundleVersion uniqueness within a path).
func validateConfig(cfg *Config) error {
	// Channel feeds both the MinIO download URL (BuildPackageURL refuses an
	// empty channel) and Subscription.spec.channel (no safe fallback — a
	// silent "stable" default would install the wrong CSV stream on a typo).
	// The local operator type runs `make deploy` and never reads Channel, so
	// the requirement is gated on Type to avoid forcing irrelevant fields on
	// local-dev configs.
	if cfg.OperatorConfig.Type == "operatorhub" {
		// Namespace is required: preflight's Get / List against Subscription /
		// InstallPlan would otherwise target an empty namespace, which dynamic
		// client treats as cluster-scope — leading to false-positive residue
		// reports from completely unrelated operators in other namespaces.
		// Catching at load time is cheaper than discovering mid-run.
		if cfg.OperatorConfig.Namespace == "" {
			return fmt.Errorf("operatorConfig.namespace is required for operatorhub type")
		}
		for i, path := range cfg.UpgradePaths {
			for j, v := range path.Versions {
				if v.Channel == "" {
					return fmt.Errorf("upgradePaths[%d].versions[%d] (%q): channel is required for operatorhub type", i, j, v.Name)
				}
				// bundleVersion gets interpolated into shell commands (preflight
				// cleanup hints) and into violet argv. Rejecting shell-active
				// characters here is a single chokepoint that closes the
				// injection vector for every downstream consumer.
				if v.BundleVersion != "" && !bundleVersionRegex.MatchString(v.BundleVersion) {
					return fmt.Errorf("upgradePaths[%d].versions[%d] (%q): bundleVersion %q must match %s", i, j, v.Name, v.BundleVersion, bundleVersionRegex.String())
				}
			}
		}
	}
	return nil
}

func defaultConfig(config *Config) *Config {
	if config.OperatorConfig.Workspace == "" {
		config.OperatorConfig.Workspace = "./"
	}

	if config.OperatorConfig.Type == "" {
		config.OperatorConfig.Type = "operatorhub"
	}

	if config.OperatorConfig.ArtifactPrefix == "" {
		config.OperatorConfig.ArtifactPrefix = "operatorhub"
	}

	if config.OperatorConfig.Interval == 0 {
		config.OperatorConfig.Interval = 5 * time.Second
	}
	if config.OperatorConfig.Timeout == 0 {
		config.OperatorConfig.Timeout = 10 * time.Minute
	}

	if v := config.OperatorConfig.Violet; v != nil {
		if v.SkipPush == nil {
			t := true
			v.SkipPush = &t
		}
	}

	return config
}
