// pkg/config/config.go

package config

import (
	"os"
	"time"

	"gopkg.in/yaml.v2"
)

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

	// Force defaults to true. Without --force violet aborts AV upserts with
	// "already exist, skip it" — verified on tektoncd-operator v4.0.17 on
	// 40-devops: the same input that succeeded with --force was a no-op
	// without it, leaving wait AV Present to time out. Pointer so users can
	// opt out (e.g. when a stale AV must be preserved exactly).
	Force *bool `yaml:"force,omitempty"`
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

// LoadConfig loads the configuration from a YAML file
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var config Config
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, err
	}

	return defaultConfig(&config), nil
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
		if v.Force == nil {
			t := true
			v.Force = &t
		}
	}

	return config
}
