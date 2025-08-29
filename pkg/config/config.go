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
	// revision is the revision to use for the version
	Channel string `yaml:"channel,omitempty"`
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

	return config
}
